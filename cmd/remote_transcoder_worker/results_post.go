package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"

	"github.com/livepeer/go-livepeer/clog"
	"github.com/livepeer/go-livepeer/common"
	"github.com/livepeer/go-livepeer/core"
	"github.com/livepeer/go-livepeer/monitor"
	"github.com/livepeer/go-livepeer/net"
)

func postResults(ctx context.Context, httpc *httpClient, orchAddr, orchSecret string, notify *net.NotifySegment, md *core.SegTranscodingMetadata, tData *core.TranscodeData, err error) {
	if notify == nil {
		return
	}
	var body bytes.Buffer
	contentType := ""
	if err != nil {
		clog.Errorf(ctx, "Unable to transcode err=%q", err)
		body.Write([]byte(err.Error()))
		contentType = transcodingErrorMimeType
	} else {
		if md == nil {
			body.Write([]byte("missing metadata"))
			contentType = transcodingErrorMimeType
		} else {
			ct, berr := buildMultipartResult(&body, md, tData)
			if berr != nil {
				body.Reset()
				body.Write([]byte(berr.Error()))
				contentType = transcodingErrorMimeType
			} else {
				contentType = ct
			}
		}
	}

	req, err := http.NewRequest("POST", "https://"+orchAddr+"/transcodeResults", &body)
	if err != nil {
		clog.Errorf(ctx, "Error posting results orch=%s taskId=%d url=%s err=%q", orchAddr, notify.TaskId, notify.Url, err)
		return
	}
	req.Header.Set("Authorization", protoVerLPT)
	req.Header.Set("Credentials", orchSecret)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("TaskId", strconv.FormatInt(notify.TaskId, 10))

	pixels := int64(0)
	if tData != nil {
		pixels = tData.Pixels
	}
	req.Header.Set("Pixels", strconv.FormatInt(pixels, 10))

	uploadStart := timeNow()
	resp, err := httpc.client.Do(req)
	if err != nil {
		clog.Errorf(ctx, "Error submitting results err=%q", err)
	} else {
		rbody, rerr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if rerr != nil {
				clog.Errorf(ctx, "Orchestrator returned HTTP statusCode=%v with unreadable body err=%q", resp.StatusCode, rerr)
			} else {
				clog.Errorf(ctx, "Orchestrator returned HTTP statusCode=%v err=%q", resp.StatusCode, string(rbody))
			}
		}
	}
	uploadDur := since(uploadStart)
	clog.V(common.VERBOSE).InfofErr(ctx, "Transcoding done results sent for taskId=%d url=%s uploadDur=%v", notify.TaskId, notify.Url, uploadDur, err)

	if monitor.Enabled {
		monitor.SegmentUploaded(ctx, 0, uint64(notify.TaskId), uploadDur, "")
	}
}

func buildMultipartResult(body *bytes.Buffer, md *core.SegTranscodingMetadata, tData *core.TranscodeData) (string, error) {
	if tData == nil {
		return "", fmt.Errorf("missing transcode data")
	}
	if len(tData.Segments) != len(md.Profiles) {
		return "", fmt.Errorf("segment / profile mismatch")
	}

	boundary := common.RandName()
	w := multipart.NewWriter(body)
	for i, v := range tData.Segments {
		ctyp, err := common.ProfileFormatMimeType(md.Profiles[i].Format)
		if err != nil {
			return "", err
		}
		w.SetBoundary(boundary)
		hdrs := textproto.MIMEHeader{
			"Content-Type":   {ctyp},
			"Content-Length": {strconv.Itoa(len(v.Data))},
			"Pixels":         {strconv.FormatInt(v.Pixels, 10)},
		}
		fw, err := w.CreatePart(hdrs)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(fw, bytes.NewBuffer(v.Data)); err != nil {
			return "", err
		}
		if md.CalcPerceptualHash {
			w.SetBoundary(boundary)
			hdrs := textproto.MIMEHeader{
				"Content-Type":   {"application/octet-stream"},
				"Content-Length": {strconv.Itoa(len(v.PHash))},
			}
			fw, err := w.CreatePart(hdrs)
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(fw, bytes.NewBuffer(v.PHash)); err != nil {
				return "", err
			}
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return "multipart/mixed; boundary=" + boundary, nil
}
