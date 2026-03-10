package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"sync"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/golang/glog"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/livepeer/go-livepeer/clog"
	"github.com/livepeer/go-livepeer/common"
	"github.com/livepeer/go-livepeer/core"
	"github.com/livepeer/go-livepeer/net"
)

type worker struct {
	cfg     Config
	backend Backend
	caps    []core.Capability
	hasSig  bool

	exec     TranscodeExecutor
	httpDoer *httpClient
}

func runWithBackoff(cfg Config) error {
	expb := backoff.NewExponentialBackOff()
	expb.MaxInterval = time.Minute
	expb.MaxElapsedTime = 0
	return backoff.Retry(func() error {
		glog.Info("Registering remote transcoder worker to ", cfg.OrchAddr)
		err := runOnce(cfg)
		glog.Info("Unregistering remote transcoder worker: ", err)
		if isFatalTranscoderError(err) {
			// Returning nil stops backoff retries
			return nil
		}
		return err
	}, expb)
}

func runOnce(cfg Config) error {
	backend, err := selectBackend(cfg)
	if err != nil {
		return err
	}

	info, err := detectCapabilities(context.Background(), cfg, backend)
	if err != nil {
		return err
	}

	exec := NewExternalFFmpegExecutor(ExecOptions{
		Backend:           backend,
		NvidiaDevices:     cfg.NvidiaDevices,
		QsvDevices:        cfg.QsvDevices,
		FfmpegPath:        cfg.FfmpegPath,
		FfmpegLogLevel:    cfg.FfmpegLogLevel,
		WorkDir:           cfg.WorkDir,
		TranscoderPreset:  cfg.TranscoderPreset,
		TranscoderRC:      cfg.TranscoderRC,
		TranscoderCRF:     cfg.TranscoderCRF,
		TranscoderMaxRate: cfg.TranscoderMaxRate,
		TranscoderBufSize: cfg.TranscoderBufSize,
		TranscoderGOP:     cfg.TranscoderGOP,
		TranscoderTune:    cfg.TranscoderTune,
		RequireSignature:  true,
	})

	w := &worker{
		cfg:     cfg,
		backend: backend,
		caps:    info.caps,
		hasSig:  info.features.filters["signature"],
		exec:    exec,
		httpDoer: &httpClient{
			client: newHTTPClient(),
		},
	}

	return w.runLoop()
}

func (w *worker) runLoop() error {
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	conn, err := grpc.Dial(w.cfg.OrchAddr, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	if err != nil {
		return err
	}
	defer conn.Close()

	client := net.NewTranscoderClient(conn)
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	caps := w.caps
	if len(caps) == 0 {
		caps = core.DefaultCapabilities()
	}
	req := &net.RegisterRequest{
		Secret:       w.cfg.OrchSecret,
		Capacity:     int64(w.cfg.Capacity),
		Capabilities: core.NewCapabilities(caps, []core.Capability{}).ToNetCapabilities(),
	}
	stream, err := client.RegisterTranscoder(ctx, req)
	if err := checkTranscoderError(err); err != nil {
		return err
	}

	var wg sync.WaitGroup
	for {
		notify, err := stream.Recv()
		if err := checkTranscoderError(err); err != nil {
			wg.Wait()
			return err
		}

		if isTeardownSignal(notify) {
			w.exec.EndTranscodingSession(notify.SegData.AuthToken.SessionId)
			continue
		}

		wg.Add(1)
		go func(n *net.NotifySegment) {
			w.handleNotify(n)
			wg.Done()
		}(notify)
	}
}

func (w *worker) handleNotify(notify *net.NotifySegment) {
	ctx := context.Background()
	ctx = clog.AddVal(ctx, "taskId", fmt.Sprintf("%d", notify.TaskId))

	md, err := segDataToMetadata(notify.SegData)
	if err != nil {
		postResults(ctx, w.httpDoer, w.cfg.OrchAddr, w.cfg.OrchSecret, notify, md, nil, err)
		return
	}
	ctx = clog.AddManifestID(ctx, string(md.ManifestID))
	if md.AuthToken != nil {
		ctx = clog.AddOrchSessionID(ctx, md.AuthToken.SessionId)
	}
	ctx = clog.AddSeqNo(ctx, uint64(md.Seq))

	caps := w.caps
	if len(caps) == 0 {
		caps = core.DefaultCapabilities()
	}
	if !md.Caps.CompatibleWith(core.NewCapabilities(caps, nil).ToNetCapabilities()) {
		err = errCapabilities
		postResults(ctx, w.httpDoer, w.cfg.OrchAddr, w.cfg.OrchSecret, notify, md, nil, err)
		return
	}
	if md.CalcPerceptualHash && !w.hasSig {
		err = fmt.Errorf("mpeg7 signature filter unavailable")
		postResults(ctx, w.httpDoer, w.cfg.OrchAddr, w.cfg.OrchSecret, notify, md, nil, err)
		return
	}

	data, err := core.DownloadData(ctx, notify.Url)
	if err != nil {
		postResults(ctx, w.httpDoer, w.cfg.OrchAddr, w.cfg.OrchSecret, notify, md, nil, err)
		return
	}

	if err := os.MkdirAll(w.cfg.WorkDir, 0700); err != nil {
		postResults(ctx, w.httpDoer, w.cfg.OrchAddr, w.cfg.OrchSecret, notify, md, nil, err)
		return
	}

	fname := path.Join(w.cfg.WorkDir, fmt.Sprintf("%s-%d-%s.tempfile", md.ManifestID, md.Seq, common.RandName()))
	if err := ioutil.WriteFile(fname, data, 0600); err != nil {
		postResults(ctx, w.httpDoer, w.cfg.OrchAddr, w.cfg.OrchSecret, notify, md, nil, err)
		return
	}
	md.Fname = fname
	md.Metadata = core.MakeMetadata(notify.OrchId)

	start := time.Now()
	tData, err := w.exec.Transcode(ctx, md)
	clog.V(common.VERBOSE).InfofErr(ctx, "Transcoding done taskId=%d url=%s dur=%v", notify.TaskId, notify.Url, time.Since(start), err)

	keepInput := false
	if tData != nil {
		for i, seg := range tData.Segments {
			if seg.Pixels > 7_378_560_000 || len(seg.Data) > 1_000_000_000 {
				keepInput = true
				clog.Info(ctx, "Extremely large output detected!", "manifestID", md.ManifestID, "seq", md.Seq, "pixels", seg.Pixels, "bytes", len(seg.Data), "profile", md.Profiles[i])
			}
		}
	}
	if !keepInput {
		_ = os.Remove(fname)
	}

	postResults(ctx, w.httpDoer, w.cfg.OrchAddr, w.cfg.OrchSecret, notify, md, tData, err)
}

func isTeardownSignal(notify *net.NotifySegment) bool {
	if notify == nil || notify.SegData == nil || notify.SegData.AuthToken == nil {
		return false
	}
	return notify.SegData.AuthToken.SessionId != "" && notify.Url == ""
}

type httpClient struct {
	client *http.Client
}

func newHTTPClient() *http.Client {
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	return &http.Client{Transport: &http2.Transport{TLSClientConfig: tlsConfig}}
}
