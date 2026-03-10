package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/golang/glog"

	"github.com/livepeer/go-livepeer/core"
)

type ffmpegFeatures struct {
	encoders        map[string]bool
	decoders        map[string]bool
	filters         map[string]bool
	hwaccels        map[string]bool
	h264PixelFormat map[string]bool
}

type capabilityInfo struct {
	caps     []core.Capability
	features ffmpegFeatures
}

func detectCapabilities(ctx context.Context, cfg Config, backend Backend) (capabilityInfo, error) {
	ffmpegPath, err := resolveBinary(cfg.FfmpegPath)
	if err != nil {
		return capabilityInfo{}, fmt.Errorf("ffmpeg not found: %w", err)
	}
	ffprobePath, err := resolveBinary(deriveFFprobePath(ffmpegPath))
	if err != nil {
		return capabilityInfo{}, fmt.Errorf("ffprobe not found: %w", err)
	}
	if err := validateBinary(ctx, ffprobePath); err != nil {
		return capabilityInfo{}, fmt.Errorf("ffprobe validation failed: %w", err)
	}

	features, err := detectFFmpegFeatures(ctx, ffmpegPath)
	if err != nil {
		return capabilityInfo{}, err
	}
	if err := validateBackend(features, backend); err != nil {
		return capabilityInfo{}, err
	}

	caps := buildCapabilities(features, backend)
	return capabilityInfo{caps: caps, features: features}, nil
}

func detectFFmpegFeatures(ctx context.Context, ffmpegPath string) (ffmpegFeatures, error) {
	encodersOut, err := runFFmpegQuery(ctx, ffmpegPath, []string{"-hide_banner", "-encoders"})
	if err != nil {
		return ffmpegFeatures{}, err
	}
	decodersOut, err := runFFmpegQuery(ctx, ffmpegPath, []string{"-hide_banner", "-decoders"})
	if err != nil {
		return ffmpegFeatures{}, err
	}
	filtersOut, err := runFFmpegQuery(ctx, ffmpegPath, []string{"-hide_banner", "-filters"})
	if err != nil {
		return ffmpegFeatures{}, err
	}
	hwaccelsOut, err := runFFmpegQuery(ctx, ffmpegPath, []string{"-hide_banner", "-hwaccels"})
	if err != nil {
		return ffmpegFeatures{}, err
	}
	h264Pix, err := detectDecoderPixelFormats(ctx, ffmpegPath, "h264")
	if err != nil {
		glog.Warningf("Failed to detect h264 pixel formats: %v", err)
	}
	return ffmpegFeatures{
		encoders:        parseEncoders(encodersOut),
		decoders:        parseDecoders(decodersOut),
		filters:         parseFilters(filtersOut),
		hwaccels:        parseHwaccels(hwaccelsOut),
		h264PixelFormat: h264Pix,
	}, nil
}

func runFFmpegQuery(ctx context.Context, ffmpegPath string, args []string) (string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg query failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func parseEncoders(output string) map[string]bool {
	encoders := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Encoders:") || strings.HasPrefix(line, "------") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		encoders[fields[1]] = true
	}
	return encoders
}

func parseDecoders(output string) map[string]bool {
	decoders := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Decoders:") || strings.HasPrefix(line, "------") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		decoders[fields[1]] = true
	}
	return decoders
}

func parseFilters(output string) map[string]bool {
	filters := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Filters:") || strings.HasPrefix(line, "------") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		filters[fields[1]] = true
	}
	return filters
}

func parseHwaccels(output string) map[string]bool {
	hw := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Hardware acceleration methods:") {
			continue
		}
		hw[line] = true
	}
	return hw
}

func detectDecoderPixelFormats(ctx context.Context, ffmpegPath, decoder string) (map[string]bool, error) {
	out, err := runFFmpegQuery(ctx, ffmpegPath, []string{"-hide_banner", "-h", "decoder=" + decoder})
	if err != nil {
		return nil, err
	}
	return parsePixelFormats(out), nil
}

func parsePixelFormats(output string) map[string]bool {
	formats := map[string]bool{}
	lines := strings.Split(output, "\n")
	collecting := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Supported pixel formats:") {
			collecting = true
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "Supported pixel formats:"))
			addTokens(formats, rest)
			continue
		}
		if collecting {
			if trimmed == "" {
				break
			}
			if strings.Contains(trimmed, ":") && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
				break
			}
			addTokens(formats, trimmed)
		}
	}
	return formats
}

func addTokens(dst map[string]bool, raw string) {
	for _, tok := range strings.Fields(raw) {
		dst[tok] = true
	}
}

func validateBackend(features ffmpegFeatures, backend Backend) error {
	enc := ""
	switch backend {
	case BackendNvidia:
		enc = "h264_nvenc"
	case BackendQSV:
		enc = "h264_qsv"
	case BackendSoftware:
		enc = "libx264"
	default:
		return fmt.Errorf("unknown backend: %s", backend)
	}
	if !features.encoders[enc] {
		return fmt.Errorf("ffmpeg missing required encoder for backend %s: %s", backend, enc)
	}
	return nil
}

func supportsHEVCEncode(features ffmpegFeatures, backend Backend) bool {
	switch backend {
	case BackendNvidia:
		return features.encoders["hevc_nvenc"]
	case BackendQSV:
		return features.encoders["hevc_qsv"]
	case BackendSoftware:
		return features.encoders["libx265"]
	default:
		return false
	}
}

func buildCapabilities(features ffmpegFeatures, backend Backend) []core.Capability {
	caps := core.DefaultCapabilities()
	if !features.filters["signature"] {
		caps = core.RemoveCapability(caps, core.Capability_MPEG7VideoSignature)
	}
	if supportsHEVCEncode(features, backend) {
		caps = addCapability(caps, core.Capability_HEVC_Encode)
	}
	if features.decoders["hevc"] {
		caps = addCapability(caps, core.Capability_HEVC_Decode)
	}
	if features.decoders["vp8"] {
		caps = addCapability(caps, core.Capability_VP8_Decode)
	}
	if features.decoders["vp9"] {
		caps = addCapability(caps, core.Capability_VP9_Decode)
	}
	if features.h264PixelFormat["yuv444p"] {
		caps = addCapability(caps, core.Capability_H264_Decode_444_8bit)
	}
	if features.h264PixelFormat["yuv422p"] {
		caps = addCapability(caps, core.Capability_H264_Decode_422_8bit)
	}
	if features.h264PixelFormat["yuv444p10le"] {
		caps = addCapability(caps, core.Capability_H264_Decode_444_10bit)
	}
	if features.h264PixelFormat["yuv422p10le"] {
		caps = addCapability(caps, core.Capability_H264_Decode_422_10bit)
	}
	if features.h264PixelFormat["yuv420p10le"] {
		caps = addCapability(caps, core.Capability_H264_Decode_420_10bit)
	}
	return caps
}

func addCapability(caps []core.Capability, cap core.Capability) []core.Capability {
	if core.HasCapability(caps, cap) {
		return caps
	}
	return append(caps, cap)
}

func resolveBinary(path string) (string, error) {
	if strings.Contains(path, string(os.PathSeparator)) {
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			return "", fmt.Errorf("binary path is a directory: %s", path)
		}
		return path, nil
	}
	return exec.LookPath(path)
}

func validateBinary(ctx context.Context, path string) error {
	cmd := exec.CommandContext(ctx, path, "-hide_banner", "-version")
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func deriveFFprobePath(ffmpegPath string) string {
	base := filepath.Base(ffmpegPath)
	if base == "ffmpeg" {
		return filepath.Join(filepath.Dir(ffmpegPath), "ffprobe")
	}
	if strings.HasSuffix(base, "ffmpeg") {
		return strings.Replace(ffmpegPath, "ffmpeg", "ffprobe", 1)
	}
	return "ffprobe"
}
