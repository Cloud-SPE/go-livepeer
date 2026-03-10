package main

import (
	"strings"
	"testing"
	"time"

	"github.com/livepeer/lpms/ffmpeg"
)

func TestSelectEncoder(t *testing.T) {
	cases := []struct {
		name    string
		enc     ffmpeg.VideoCodec
		backend Backend
		want    string
	}{
		{name: "h264-nvenc", enc: ffmpeg.H264, backend: BackendNvidia, want: "h264_nvenc"},
		{name: "h264-qsv", enc: ffmpeg.H264, backend: BackendQSV, want: "h264_qsv"},
		{name: "h264-software", enc: ffmpeg.H264, backend: BackendSoftware, want: "libx264"},
		{name: "h265-nvenc", enc: ffmpeg.H265, backend: BackendNvidia, want: "hevc_nvenc"},
		{name: "h265-qsv", enc: ffmpeg.H265, backend: BackendQSV, want: "hevc_qsv"},
		{name: "vp9", enc: ffmpeg.VP9, backend: BackendSoftware, want: "libvpx-vp9"},
	}
	for _, tc := range cases {
		prof := ffmpeg.VideoProfile{Encoder: tc.enc}
		got, err := selectEncoder(prof, tc.backend)
		if err != nil {
			t.Fatalf("%s: unexpected err=%v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got=%q want=%q", tc.name, got, tc.want)
		}
	}
}

func TestProfileString(t *testing.T) {
	if got := profileString(ffmpeg.VideoProfile{Profile: ffmpeg.ProfileH264Baseline}, "h264_nvenc"); got != "baseline" {
		t.Fatalf("baseline got=%q", got)
	}
	if got := profileString(ffmpeg.VideoProfile{Profile: ffmpeg.ProfileH264Main}, "libx264"); got != "main" {
		t.Fatalf("main got=%q", got)
	}
	if got := profileString(ffmpeg.VideoProfile{Profile: ffmpeg.ProfileH264High}, "libx264"); got != "high" {
		t.Fatalf("high got=%q", got)
	}
	if got := profileString(ffmpeg.VideoProfile{Profile: ffmpeg.ProfileH264High}, "libvpx"); got != "" {
		t.Fatalf("non-h264 got=%q", got)
	}
}

func TestFormatString(t *testing.T) {
	if got, err := formatString(ffmpeg.FormatMPEGTS); err != nil || got != "mpegts" {
		t.Fatalf("mpegts got=%q err=%v", got, err)
	}
	if got, err := formatString(ffmpeg.FormatMP4); err != nil || got != "mp4" {
		t.Fatalf("mp4 got=%q err=%v", got, err)
	}
}

func TestParseResolution(t *testing.T) {
	w, h := parseResolution("1280x720")
	if w != 1280 || h != 720 {
		t.Fatalf("got=%dx%d", w, h)
	}
	if w, h := parseResolution("bad"); w != 0 || h != 0 {
		t.Fatalf("expected 0 for bad parse")
	}
}

func TestGopFrames(t *testing.T) {
	prof := ffmpeg.VideoProfile{GOP: -60}
	if got := gopFrames(prof, 30, 1, ""); got != 60 {
		t.Fatalf("neg gop got=%d", got)
	}
	prof.GOP = 2 * time.Second
	if got := gopFrames(prof, 30, 1, ""); got != 60 {
		t.Fatalf("duration gop got=%d", got)
	}
	if got := gopFrames(prof, 30, 1, "90"); got != 90 {
		t.Fatalf("override frames got=%d", got)
	}
	if got := gopFrames(prof, 30, 1, "2s"); got != 60 {
		t.Fatalf("override duration got=%d", got)
	}
}

func TestOutputArgs(t *testing.T) {
	opts := ExecOptions{
		Backend:           BackendNvidia,
		TranscoderPreset:  "p4",
		TranscoderRC:      "vbr",
		TranscoderCRF:     23,
		TranscoderMaxRate: "2000k",
		TranscoderBufSize: "4000k",
	}
	out := outputSpec{
		profile: ffmpeg.VideoProfile{
			Encoder:    ffmpeg.H264,
			Profile:    ffmpeg.ProfileH264Baseline,
			Bitrate:    "1500k",
			Resolution: "640x360",
			GOP:        -60,
			Framerate:  30,
			Format:     ffmpeg.FormatMPEGTS,
		},
		width:  640,
		height: 360,
		fpsNum: 30,
		fpsDen: 1,
		format: "mpegts",
	}
	args, err := outputArgs(opts, out)
	if err != nil {
		t.Fatalf("outputArgs err=%v", err)
	}
	joined := strings.Join(args, " ")
	for _, needle := range []string{
		"-c:v h264_nvenc", "-profile:v baseline", "-b:v 1500k", "-g 60",
		"-preset p4", "-rc vbr", "-cq 23",
		"-spatial-aq 1", "-temporal-aq 1", // NVENC adaptive quantization
		"-color_range tv",       // limited range tag for HLS/broadcast
		"-rc-lookahead 32",      // NVENC HW lookahead (Pascal+), no shader cost
		"-fps_mode passthrough", // output-side flag: pass frames with exact original timestamps
		"-flags +cgop",          // closed GOP for HLS segment independence
		"-muxdelay 0",           // no per-segment muxer padding
		"-muxpreload 0",         // no initial muxer pre-load delay
		"-s:v 640x360",          // software scaling for non-QSV backend
	} {
		if !strings.Contains(joined, needle) {
			t.Fatalf("missing %q in %q", needle, joined)
		}
	}
	if strings.Contains(joined, "scale_qsv") {
		t.Fatalf("unexpected scale_qsv in nvidia args: %q", joined)
	}
	if !strings.Contains(joined, "-c:a copy") {
		t.Fatalf("missing audio copy")
	}
	if !strings.Contains(joined, "-f mpegts") {
		t.Fatalf("missing format")
	}
}

func TestOutputArgsQSV(t *testing.T) {
	opts := ExecOptions{
		Backend:    BackendQSV,
		QsvDevices: "/dev/dri/renderD128",
	}
	out := outputSpec{
		profile: ffmpeg.VideoProfile{
			Encoder:   ffmpeg.H264,
			Bitrate:   "3000k",
			Framerate: 30,
			Format:    ffmpeg.FormatMPEGTS,
		},
		width:  1280,
		height: 720,
		fpsNum: 30,
		fpsDen: 1,
		format: "mpegts",
	}
	args, err := outputArgs(opts, out)
	if err != nil {
		t.Fatalf("outputArgs err=%v", err)
	}
	joined := strings.Join(args, " ")
	for _, needle := range []string{
		"-c:v h264_qsv",
		"-bf 0",                  // B-frames disabled to prevent DTS reordering across segment boundaries
		"-vf scale_qsv=1280:720", // GPU-native scaling (not -s:v which forces CPU frame download)
		"-fps_mode passthrough",  // output-side flag: pass frames with exact original timestamps
		"-flags +cgop",           // closed GOP for HLS segment independence
		"-muxdelay 0",            // no per-segment muxer padding
		"-muxpreload 0",          // no initial muxer pre-load delay
	} {
		if !strings.Contains(joined, needle) {
			t.Fatalf("missing %q in %q", needle, joined)
		}
	}
	if strings.Contains(joined, "-s:v") {
		t.Fatalf("unexpected -s:v in QSV args (should use scale_qsv): %q", joined)
	}
}

func TestDeriveFFprobePath(t *testing.T) {
	if got := deriveFFprobePath("/usr/local/bin/ffmpeg"); got != "/usr/local/bin/ffprobe" {
		t.Fatalf("got=%q", got)
	}
	if got := deriveFFprobePath("ffmpeg"); got != "ffprobe" {
		t.Fatalf("got=%q", got)
	}
}
