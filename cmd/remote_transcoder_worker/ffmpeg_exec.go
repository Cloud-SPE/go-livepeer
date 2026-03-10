package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/golang/glog"

	"github.com/livepeer/go-livepeer/clog"
	"github.com/livepeer/go-livepeer/common"
	"github.com/livepeer/go-livepeer/core"
	"github.com/livepeer/lpms/ffmpeg"
)

type ExecOptions struct {
	Backend           Backend
	NvidiaDevices     string
	QsvDevices        string
	FfmpegPath        string
	FfmpegLogLevel    string
	WorkDir           string
	TranscoderPreset  string
	TranscoderRC      string
	TranscoderCRF     int
	TranscoderMaxRate string
	TranscoderBufSize string
	TranscoderGOP     string
	TranscoderTune    string
	RequireSignature  bool
}

type TranscodeExecutor interface {
	Transcode(ctx context.Context, md *core.SegTranscodingMetadata) (*core.TranscodeData, error)
	EndTranscodingSession(sessionId string)
}

type ExternalFFmpegExecutor struct {
	opts ExecOptions
}

func NewExternalFFmpegExecutor(opts ExecOptions) *ExternalFFmpegExecutor {
	return &ExternalFFmpegExecutor{opts: opts}
}

func (e *ExternalFFmpegExecutor) EndTranscodingSession(string) {
	// No-op for external ffmpeg invocation in Phase 1.
}

type outputSpec struct {
	profile ffmpeg.VideoProfile
	outPath string
	format  string
	width   int
	height  int
	fpsNum  int
	fpsDen  int
}

func (e *ExternalFFmpegExecutor) Transcode(ctx context.Context, md *core.SegTranscodingMetadata) (*core.TranscodeData, error) {
	if md == nil {
		return nil, fmt.Errorf("missing metadata")
	}
	if md.Fname == "" {
		return nil, fmt.Errorf("missing input file")
	}

	outputs, err := buildOutputSpecs(e.opts.WorkDir, md)
	if err != nil {
		return nil, err
	}

	if err := runFFmpegTranscode(ctx, e.opts, md, outputs); err != nil {
		return nil, err
	}

	ffprobePath := deriveFFprobePath(e.opts.FfmpegPath)
	decodedPixels, err := probePixels(ctx, ffprobePath, md.Fname)
	if err != nil {
		return nil, fmt.Errorf("probe input pixels failed: %w", err)
	}

	segments := make([]*core.TranscodedSegmentData, len(outputs))
	for i, out := range outputs {
		data, err := os.ReadFile(out.outPath)
		if err != nil {
			return nil, err
		}
		encodedPixels, err := probePixels(ctx, ffprobePath, out.outPath)
		if err != nil {
			return nil, fmt.Errorf("probe output pixels failed: %w", err)
		}
		var phash []byte
		if md.CalcPerceptualHash {
			sig, sigErr := generateSignature(ctx, e.opts.FfmpegPath, e.opts.FfmpegLogLevel, out.outPath)
			if sigErr != nil {
				if e.opts.RequireSignature {
					return nil, fmt.Errorf("mpeg7 signature generation failed: %w", sigErr)
				}
				clog.Errorf(ctx, "Unable to generate perceptual hash fname=%s err=%q", out.outPath, sigErr)
			} else {
				phash = sig
			}
		}
		segments[i] = &core.TranscodedSegmentData{
			Data:   data,
			Pixels: encodedPixels,
			PHash:  phash,
		}
		_ = os.Remove(out.outPath)
	}

	return &core.TranscodeData{
		Segments: segments,
		Pixels:   decodedPixels,
	}, nil
}

func buildOutputSpecs(workDir string, md *core.SegTranscodingMetadata) ([]outputSpec, error) {
	if len(md.Profiles) == 0 {
		return nil, fmt.Errorf("missing profiles")
	}
	outputs := make([]outputSpec, 0, len(md.Profiles))
	for _, prof := range md.Profiles {
		ext, err := common.ProfileFormatExtension(prof.Format)
		if err != nil {
			return nil, err
		}
		format, err := formatString(prof.Format)
		if err != nil {
			return nil, err
		}
		outPath := filepath.Join(workDir, fmt.Sprintf("out_%s-%d-%s%s", md.ManifestID, md.Seq, common.RandName(), ext))
		w, h := parseResolution(prof.Resolution)
		fpsNum := int(prof.Framerate)
		fpsDen := int(prof.FramerateDen)
		outputs = append(outputs, outputSpec{
			profile: prof,
			outPath: outPath,
			format:  format,
			width:   w,
			height:  h,
			fpsNum:  fpsNum,
			fpsDen:  fpsDen,
		})
	}
	return outputs, nil
}

func runFFmpegTranscode(ctx context.Context, opts ExecOptions, md *core.SegTranscodingMetadata, outputs []outputSpec) error {
	logLevel := opts.FfmpegLogLevel
	if logLevel == "" {
		logLevel = "error"
	}
	args := []string{"-hide_banner", "-loglevel", logLevel, "-y"}

	// Preserve absolute timestamps from input — required for HLS segment continuity.
	// Each ffmpeg invocation is stateless; without these flags timestamps reset to 0
	// per segment and HLS players stall after segment 1.
	args = append(args,
		"-copyts",                        // Copy input PTS/DTS to output unchanged
		"-avoid_negative_ts", "disabled", // Don't shift timestamps for B-frame negative DTS
	)

	clip := md.SegmentParameters
	if clip != nil && clip.Clip != nil {
		args = append(args, "-ss", formatDuration(clip.Clip.From))
		args = append(args, "-to", formatDuration(clip.Clip.To))
	}

	switch opts.Backend {
	case BackendNvidia:
		args = append(args, "-hwaccel", "cuda")
	case BackendQSV:
		if qsv := firstDevice(opts.QsvDevices); qsv != "" && qsv != "all" {
			args = append(args, "-qsv_device", qsv)
		}
		// Enable HW decode so encoder receives native QSV surfaces (zero-copy pipeline)
		args = append(args, "-hwaccel", "qsv", "-hwaccel_output_format", "qsv")
	}

	args = append(args, "-i", md.Fname)

	for _, out := range outputs {
		outArgs, err := outputArgs(opts, out)
		if err != nil {
			return err
		}
		args = append(args, outArgs...)
		args = append(args, out.outPath)
	}

	cmd := exec.CommandContext(ctx, opts.FfmpegPath, args...)
	cmd.Env = ffmpegEnv(opts)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	clog.Infof(ctx, "ffmpeg command: %s %s", opts.FfmpegPath, strings.Join(args, " "))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg transcode failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stderr.Len() > 0 {
		clog.V(common.VERBOSE).Infof(ctx, "ffmpeg stderr: %s", stderr.String())
	}
	return nil
}

func outputArgs(opts ExecOptions, out outputSpec) ([]string, error) {
	args := []string{
		"-map", "0:v:0",
		"-map", "0:a?",
	}

	encoder, err := selectEncoder(out.profile, opts.Backend)
	if err != nil {
		return nil, err
	}
	args = append(args, "-c:v", encoder)

	// Disable B-frames for QSV: avoids DTS reordering across segment boundaries
	if opts.Backend == BackendQSV {
		args = append(args, "-bf", "0")
	}

	if out.width > 0 && out.height > 0 {
		// scale_qsv operates on GPU surfaces; -s:v requires CPU frames incompatible with hwaccel_output_format qsv
		if opts.Backend == BackendQSV {
			args = append(args, "-vf", fmt.Sprintf("scale_qsv=%d:%d", out.width, out.height))
		} else {
			args = append(args, "-s:v", fmt.Sprintf("%dx%d", out.width, out.height))
		}
	}

	if out.fpsNum > 0 && out.fpsDen > 0 {
		args = append(args, "-r", fmt.Sprintf("%d/%d", out.fpsNum, out.fpsDen))
	}

	if prof := profileString(out.profile, encoder); prof != "" {
		args = append(args, "-profile:v", prof)
	}

	if gop := gopFrames(out.profile, out.fpsNum, out.fpsDen, opts.TranscoderGOP); gop > 0 {
		args = append(args, "-g", strconv.Itoa(gop))
	}

	// Force closed GOP so each HLS segment is independently decodable
	args = append(args, "-flags", "+cgop")

	if opts.TranscoderPreset != "" && supportsPreset(encoder) {
		args = append(args, "-preset", opts.TranscoderPreset)
	}

	if opts.TranscoderTune != "" && supportsTune(encoder) {
		args = append(args, "-tune", opts.TranscoderTune)
	}

	if out.profile.Bitrate != "" {
		args = append(args, "-b:v", out.profile.Bitrate)
	}

	if opts.TranscoderMaxRate != "" {
		args = append(args, "-maxrate", opts.TranscoderMaxRate)
	}
	if opts.TranscoderBufSize != "" {
		args = append(args, "-bufsize", opts.TranscoderBufSize)
	}

	if opts.TranscoderRC != "" && supportsRateControl(encoder) {
		args = append(args, "-rc", opts.TranscoderRC)
	}

	if opts.TranscoderCRF > 0 {
		args = append(args, qualityArgs(encoder, opts.TranscoderCRF)...)
	}

	// NVENC adaptive quantization — redistributes bits within/across frames to
	// perceptually important regions without increasing average bitrate.
	// Also signals limited color range (tv) for HLS/broadcast compatibility: RTMP
	// input may arrive tagged full-range (yuvj420p/pc) and some HLS players display
	// washed-out colors without an explicit limited-range tag on the output.
	if strings.Contains(encoder, "nvenc") {
		args = append(args, "-spatial-aq", "1", "-temporal-aq", "1")
		args = append(args, "-color_range", "tv")
		// Lookahead runs in a dedicated NVENC HW buffer — not shader compute.
		// 32 frames covers half a typical 60-frame segment, giving VBR enough
		// lookahead to avoid over-spending bits on the first I-frame.
		// Safe on Pascal (GTX 10xx) and newer; multipass is NOT used as it
		// requires Turing (RTX 20xx) and would hard-error on 1070/1080.
		args = append(args, "-rc-lookahead", "32")
	}

	// fps_mode is an output option: pass frames with exact original timestamps (no dup/drop)
	args = append(args, "-fps_mode", "passthrough")

	// Prevent muxer from adding per-segment padding (~0.7s default) that tears apart timestamp alignment
	if out.format == "mpegts" {
		args = append(args, "-muxdelay", "0", "-muxpreload", "0")
	}

	args = append(args, "-c:a", "copy")
	args = append(args, "-f", out.format)
	return args, nil
}

func ffmpegEnv(opts ExecOptions) []string {
	env := os.Environ()
	if opts.Backend == BackendNvidia {
		if dev := strings.TrimSpace(opts.NvidiaDevices); dev != "" && dev != "all" {
			env = append(env, "CUDA_VISIBLE_DEVICES="+dev)
		}
	}
	return env
}

func selectEncoder(profile ffmpeg.VideoProfile, backend Backend) (string, error) {
	switch profile.Encoder {
	case ffmpeg.H264:
		switch backend {
		case BackendNvidia:
			return "h264_nvenc", nil
		case BackendQSV:
			return "h264_qsv", nil
		default:
			return "libx264", nil
		}
	case ffmpeg.H265:
		switch backend {
		case BackendNvidia:
			return "hevc_nvenc", nil
		case BackendQSV:
			return "hevc_qsv", nil
		default:
			return "libx265", nil
		}
	case ffmpeg.VP8:
		return "libvpx", nil
	case ffmpeg.VP9:
		return "libvpx-vp9", nil
	default:
		return "", fmt.Errorf("unsupported encoder: %v", profile.Encoder)
	}
}

func profileString(profile ffmpeg.VideoProfile, encoder string) string {
	if !strings.Contains(encoder, "264") {
		return ""
	}
	switch profile.Profile {
	case ffmpeg.ProfileH264Baseline:
		return "baseline"
	case ffmpeg.ProfileH264Main:
		return "main"
	case ffmpeg.ProfileH264High, ffmpeg.ProfileH264ConstrainedHigh:
		return "high"
	default:
		return ""
	}
}

func supportsPreset(encoder string) bool {
	return strings.Contains(encoder, "x264") || strings.Contains(encoder, "x265") || strings.Contains(encoder, "nvenc") || strings.Contains(encoder, "qsv")
}

func supportsTune(encoder string) bool {
	return strings.Contains(encoder, "x264") || strings.Contains(encoder, "x265")
}

func supportsRateControl(encoder string) bool {
	return strings.Contains(encoder, "nvenc") || strings.Contains(encoder, "qsv")
}

func qualityArgs(encoder string, crf int) []string {
	if strings.Contains(encoder, "nvenc") {
		return []string{"-cq", strconv.Itoa(crf)}
	}
	if strings.Contains(encoder, "qsv") {
		return []string{"-global_quality", strconv.Itoa(crf)}
	}
	if strings.Contains(encoder, "vpx") {
		return []string{"-crf", strconv.Itoa(crf)}
	}
	return []string{"-crf", strconv.Itoa(crf)}
}

func gopFrames(profile ffmpeg.VideoProfile, fpsNum, fpsDen int, override string) int {
	if override != "" {
		if frames, ok := parseGopOverride(override, fpsNum, fpsDen); ok {
			return frames
		}
	}
	if profile.GOP < 0 {
		frames := int(-profile.GOP)
		if frames > 0 && frames < 100000 {
			return frames
		}
	}
	if profile.GOP > 0 && fpsNum > 0 && fpsDen > 0 {
		fps := float64(fpsNum) / float64(fpsDen)
		return int(math.Round(profile.GOP.Seconds() * fps))
	}
	return 0
}

func parseGopOverride(raw string, fpsNum, fpsDen int) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if strings.HasSuffix(raw, "ms") || strings.HasSuffix(raw, "s") {
		dur, err := time.ParseDuration(raw)
		if err != nil {
			return 0, false
		}
		if fpsNum <= 0 || fpsDen <= 0 {
			return 0, false
		}
		fps := float64(fpsNum) / float64(fpsDen)
		return int(math.Round(dur.Seconds() * fps)), true
	}
	frames, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return frames, true
}

func parseResolution(res string) (int, int) {
	parts := strings.Split(res, "x")
	if len(parts) != 2 {
		return 0, 0
	}
	w, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0
	}
	h, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0
	}
	return w, h
}

func formatString(format ffmpeg.Format) (string, error) {
	switch format {
	case ffmpeg.FormatMPEGTS:
		return "mpegts", nil
	case ffmpeg.FormatMP4:
		return "mp4", nil
	default:
		return "", fmt.Errorf("unsupported output format: %v", format)
	}
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0"
	}
	return fmt.Sprintf("%.3f", d.Seconds())
}

func firstDevice(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, ",")
	if len(parts) == 0 {
		return raw
	}
	return strings.TrimSpace(parts[0])
}

type ffprobeOutput struct {
	Streams []struct {
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		AvgFrameRate string `json:"avg_frame_rate"`
		NbFrames     string `json:"nb_frames"`
		NbReadFrames string `json:"nb_read_frames"`
		Duration     string `json:"duration"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func probePixels(ctx context.Context, ffprobePath, fname string) (int64, error) {
	info, err := probeStream(ctx, ffprobePath, fname, false)
	if err != nil {
		return 0, err
	}
	frames := info.frames
	if frames <= 0 {
		info, err = probeStream(ctx, ffprobePath, fname, true)
		if err != nil {
			return 0, err
		}
		frames = info.frames
	}
	if info.width <= 0 || info.height <= 0 || frames <= 0 {
		return 0, fmt.Errorf("invalid probe data")
	}
	return int64(info.width) * int64(info.height) * frames, nil
}

type streamInfo struct {
	width  int
	height int
	frames int64
}

func probeStream(ctx context.Context, ffprobePath, fname string, countFrames bool) (streamInfo, error) {
	args := []string{"-v", "error", "-select_streams", "v:0"}
	if countFrames {
		args = append(args, "-count_frames")
	}
	args = append(args, "-show_entries", "stream=width,height,avg_frame_rate,nb_frames,nb_read_frames,duration", "-show_entries", "format=duration", "-of", "json", fname)

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, ffprobePath, args...)
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return streamInfo{}, fmt.Errorf("ffprobe failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	var parsed ffprobeOutput
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		return streamInfo{}, fmt.Errorf("ffprobe parse failed: %w", err)
	}
	if len(parsed.Streams) == 0 {
		return streamInfo{}, fmt.Errorf("ffprobe missing stream data")
	}
	s := parsed.Streams[0]
	width := s.Width
	height := s.Height
	frames := parseFrames(s.NbFrames)
	if frames <= 0 {
		frames = parseFrames(s.NbReadFrames)
	}
	if frames <= 0 {
		fps := parseRate(s.AvgFrameRate)
		dur := parseDuration(s.Duration)
		if dur == 0 {
			dur = parseDuration(parsed.Format.Duration)
		}
		if fps > 0 && dur > 0 {
			frames = int64(math.Round(dur * fps))
		}
	}
	return streamInfo{width: width, height: height, frames: frames}, nil
}

func parseFrames(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	val, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return val
}

func parseRate(raw string) float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0/0" {
		return 0
	}
	parts := strings.Split(raw, "/")
	if len(parts) != 2 {
		val, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return 0
		}
		return val
	}
	num, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0
	}
	den, err := strconv.ParseFloat(parts[1], 64)
	if err != nil || den == 0 {
		return 0
	}
	return num / den
}

func parseDuration(raw string) float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	val, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0
	}
	return val
}

func generateSignature(ctx context.Context, ffmpegPath, logLevel, input string) ([]byte, error) {
	sigPath := input + ".bin"
	if logLevel == "" {
		logLevel = "error"
	}
	args := []string{
		"-hide_banner", "-loglevel", logLevel, "-y",
		"-i", input,
		"-an",
		"-vf", "signature=filename=" + sigPath,
		"-f", "null", "-",
	}
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg signature failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	data, err := os.ReadFile(sigPath)
	if err != nil {
		return nil, err
	}
	if err := os.Remove(sigPath); err != nil {
		glog.Warningf("Failed to remove signature file=%s err=%v", sigPath, err)
	}
	return data, nil
}
