package main

import (
	"testing"

	"github.com/livepeer/go-livepeer/core"
)

func TestParseEncoders(t *testing.T) {
	out := `
Encoders:
 V..... h264_nvenc           NVIDIA NVENC H.264 encoder
 V..... hevc_nvenc           NVIDIA NVENC hevc encoder
 V..... libx264              libx264 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10
`
	enc := parseEncoders(out)
	if !enc["h264_nvenc"] || !enc["hevc_nvenc"] || !enc["libx264"] {
		t.Fatalf("expected encoders missing: %#v", enc)
	}
}

func TestParseDecoders(t *testing.T) {
	out := `
Decoders:
 V..... h264                 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10
 V..... hevc                 HEVC (High Efficiency Video Coding)
 V..... vp9                  Google VP9
`
	dec := parseDecoders(out)
	if !dec["h264"] || !dec["hevc"] || !dec["vp9"] {
		t.Fatalf("expected decoders missing: %#v", dec)
	}
}

func TestParseFilters(t *testing.T) {
	out := `
Filters:
 T.. signature         Generate MPEG-7 video signature.
 T.. scale             Scale the input video.
`
	f := parseFilters(out)
	if !f["signature"] || !f["scale"] {
		t.Fatalf("expected filters missing: %#v", f)
	}
}

func TestParseHwaccels(t *testing.T) {
	out := `
Hardware acceleration methods:
vdpau
cuda
qsv
`
	hw := parseHwaccels(out)
	if !hw["cuda"] || !hw["qsv"] {
		t.Fatalf("expected hwaccels missing: %#v", hw)
	}
}

func TestBuildCapabilitiesSignature(t *testing.T) {
	features := ffmpegFeatures{
		encoders:        map[string]bool{"h264_nvenc": true},
		decoders:        map[string]bool{"hevc": true, "vp8": true, "vp9": true},
		filters:         map[string]bool{"signature": true},
		hwaccels:        map[string]bool{},
		h264PixelFormat: map[string]bool{"yuv444p": true, "yuv422p10le": true, "yuv420p10le": true},
	}
	caps := buildCapabilities(features, BackendNvidia)
	if !containsCapability(caps, core.Capability_MPEG7VideoSignature) {
		t.Fatalf("expected MPEG7 capability when signature filter exists")
	}
	if !containsCapability(caps, core.Capability_HEVC_Decode) {
		t.Fatalf("expected HEVC decode capability")
	}
	if !containsCapability(caps, core.Capability_VP8_Decode) {
		t.Fatalf("expected VP8 decode capability")
	}
	if !containsCapability(caps, core.Capability_VP9_Decode) {
		t.Fatalf("expected VP9 decode capability")
	}
	if !containsCapability(caps, core.Capability_H264_Decode_444_8bit) {
		t.Fatalf("expected H264 444 8bit capability")
	}
	if !containsCapability(caps, core.Capability_H264_Decode_422_10bit) {
		t.Fatalf("expected H264 422 10bit capability")
	}
	if !containsCapability(caps, core.Capability_H264_Decode_420_10bit) {
		t.Fatalf("expected H264 420 10bit capability")
	}

	features.filters["signature"] = false
	caps = buildCapabilities(features, BackendNvidia)
	if containsCapability(caps, core.Capability_MPEG7VideoSignature) {
		t.Fatalf("unexpected MPEG7 capability without signature filter")
	}
}

func TestParsePixelFormats(t *testing.T) {
	out := `
Decoder h264
Supported pixel formats: yuv420p yuv422p yuv444p
 yuv420p10le yuv422p10le yuv444p10le
Supported sample rates: 44100 48000
`
	fmts := parsePixelFormats(out)
	for _, want := range []string{"yuv420p", "yuv422p", "yuv444p", "yuv420p10le", "yuv422p10le", "yuv444p10le"} {
		if !fmts[want] {
			t.Fatalf("missing pixel format %s", want)
		}
	}
}

func containsCapability(caps []core.Capability, want core.Capability) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}
