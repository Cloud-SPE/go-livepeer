# Remote Transcoder Worker Ops

## Build
Local binary:
```bash
make remote_transcoder_worker
```

Docker images:
```bash
make docker_remote_transcoder_nvidia DOCKER_TAG=latest FFMPEG_VERSION=7.1.1 CUDA_VERSION=12.4.1
make docker_remote_transcoder_qsv DOCKER_TAG=latest FFMPEG_VERSION=7.1.1
```

Or use the helper script:
```bash
TAG=latest FFMPEG_VERSION=7.1.1 CUDA_VERSION=12.4.1 docker/build-remote-transcoder-worker.sh
```

## Run

Minimum flags (NVIDIA):
```bash
remote-transcoder-worker \
  -orchAddr <host:port> \
  -orchSecret <secret> \
  -capacity 1 \
  -transcoderBackend nvidia \
  -nvidia all
```

QSV example (explicit device path):
```bash
remote-transcoder-worker \
  -orchAddr <host:port> \
  -orchSecret <secret> \
  -capacity 1 \
  -transcoderBackend qsv \
  -qsv /dev/dri/renderD128
```

## Environment Overrides
CLI flags take precedence over environment variables. All worker flags support 1:1 env overrides:

| Env var | CLI flag | Description |
|---|---|---|
| `ORCH_ADDR` | `-orchAddr` | Orchestrator address in `host:port` form |
| `ORCH_SECRET` | `-orchSecret` | Shared secret with the orchestrator |
| `TRANSCODER_CAPACITY` | `-capacity` | Concurrent transcode slots to register |
| `TRANSCODER_BACKEND` | `-transcoderBackend` | Hardware backend: `nvidia` or `qsv` |
| `NVIDIA_DEVICES` | `-nvidia` | Comma-separated GPU indices or `all` |
| `QSV_DEVICES` | `-qsv` | Comma-separated DRI render node paths |
| `WORK_DIR` | `-workDir` | Staging directory for segment temp files |
| `FFMPEG_PATH` | `-ffmpegPath` | Path to the ffmpeg binary |
| `FFMPEG_LOGLEVEL` | `-ffmpegLogLevel` | ffmpeg log verbosity: `quiet` \| `error` \| `warning` \| `info` \| `debug` |
| `TRANSCODER_PRESET` | `-transcoderPreset` | Encoder preset (see Quality Tuning below) |
| `TRANSCODER_RC` | `-transcoderRC` | Rate control mode (see Quality Tuning below) |
| `TRANSCODER_CRF` | `-transcoderCRF` | Quality target (0 = unset) |
| `TRANSCODER_MAXRATE` | `-transcoderMaxRate` | Peak bitrate cap (e.g. `8000k`) |
| `TRANSCODER_BUFSIZE` | `-transcoderBufSize` | VBV buffer size (e.g. `16000k`) |
| `TRANSCODER_GOP` | `-transcoderGOP` | Keyframe interval override: frames (`60`) or duration (`2s`) |
| `TRANSCODER_TUNE` | `-transcoderTune` | Encoder tune (libx264/libx265 only) |

## HLS Segment Continuity

The worker spawns a fresh ffmpeg process per segment. By default ffmpeg resets
timestamps to 0 on each invocation, causing HLS players to stall after segment 1.
The following flags are applied globally (before `-i`) to preserve absolute timestamps:

```
-copyts                      Copy input PTS/DTS to output unchanged
-avoid_negative_ts disabled  Do not shift timestamps for B-frame negative DTS
```

Per-output flags that maintain mux alignment:
```
-fps_mode passthrough   Pass frames with exact original timestamps (no dup/drop)
-muxdelay 0             Remove per-segment muxer padding (~0.7s default)
-muxpreload 0           Remove initial muxer pre-load delay
-flags +cgop            Force closed GOP so each segment is independently decodable
```

These flags replicate the cross-segment `dts_diff` tracking that the LPMS C library
maintains in persistent transcoder sessions.

## Quality Tuning

### NVIDIA (h264_nvenc / hevc_nvenc)

The recommended defaults are set in `docker-compose.remote-transcoder-nvidia.yml`:

| Setting | Value | Rationale |
|---|---|---|
| `TRANSCODER_PRESET` | `p5` | Better quality than p4; still fast on all supported GPUs |
| `TRANSCODER_RC` | `vbr` | Allocates bits where scene complexity demands them; better perceptual quality than CBR at the same average bitrate |

The following flags are hardcoded in the worker (not configurable via env):

| Flag | Effect |
|---|---|
| `-spatial-aq 1` | Redistributes bits within frames to perceptually important regions |
| `-temporal-aq 1` | Redistributes bits across frames to reduce temporal noise |
| `-rc-lookahead 32` | 32-frame HW lookahead for better VBR bit allocation; runs in dedicated NVENC silicon, no shader cost. Safe on Pascal (GTX 10xx) and newer |
| `-color_range tv` | Tags output as limited range for HLS/broadcast compatibility. Input from RTMP is often full-range (`yuvj420p`/`pc`); without this flag some players display washed-out colors |

**Note on multipass:** `-multipass fullres` (two-pass NVENC) is intentionally omitted.
It requires Turing (RTX 20xx) or newer and will hard-error on Pascal (GTX 1070/1080).
The combination of p5 + VBR + AQ + lookahead achieves comparable quality on all
supported GPU generations.

**Note on `-color_range tv` vs LPMS:** The LPMS path uses CBR
(`rc_min_rate = rc_max_rate = bitrate`) with no AQ or lookahead. The worker's
VBR + AQ + lookahead configuration is strictly better in perceptual quality at
equal average bitrate.

**VBV (maxrate/bufsize):** Leave `TRANSCODER_MAXRATE` and `TRANSCODER_BUFSIZE` unset
unless you have a specific CDN peak-bitrate delivery constraint. A global maxrate
applied across a multi-rendition ladder is problematic when renditions have very
different target bitrates — e.g. a 4500k cap constrains a 6500k 1080p rendition.
If you do set these, use a value ≥ the highest rendition's bitrate and set
`bufsize = 2 × maxrate`.

### NVIDIA Concurrent Session Limits

Consumer NVIDIA GPUs (Pascal and earlier) enforce a maximum of 3 concurrent NVENC
sessions per GPU via the driver. Each ffmpeg output stream consumes one session, so
a 3-rendition job uses 3 sessions. With the driver limit:

- **Without nvidia-patch**: `TRANSCODER_CAPACITY` must be set to `1` on GTX 1070/1080
  to avoid exceeding the 3-session cap.
- **With [nvidia-patch](https://github.com/keylase/nvidia-patch)**: the driver limit
  is removed and `CAPACITY` can be set freely based on GPU compute headroom.
  Turing (RTX 20xx) and Ampere (RTX 30xx) have no meaningful session cap by default.

### Intel QSV (h264_qsv / hevc_qsv)

The recommended defaults are set in `docker-compose.remote-transcoder-qsv.yml`:

| Setting | Value | Rationale |
|---|---|---|
| `TRANSCODER_RC` | `vbr` | Variable bitrate for better perceptual quality |
| `TRANSCODER_CRF` | `23` | Perceptual quality target mapped to `-global_quality`; 23 is a good starting point for 720p/1080p |

The following flags are hardcoded for the QSV backend:

| Flag | Effect |
|---|---|
| `-hwaccel qsv -hwaccel_output_format qsv` | Full zero-copy GPU pipeline: decoded frames stay in QSV surface memory and are fed directly to the encoder |
| `-vf scale_qsv=W:H` | GPU-native scaling. `-s:v` is incompatible with `hwaccel_output_format qsv` as it forces a GPU→CPU frame download |
| `-bf 0` | Disables B-frames to prevent DTS reordering across segment boundaries |

**Available QSV rate control modes:** `cbr`, `vbr`, `avbr`, `la`, `la_hrd`, `icq`,
`la_icq`, `cqp`. The `la` and `la_icq` modes use lookahead for better bit distribution
but require driver support — verify with your hardware before deploying.

## Dependencies
- `ffmpeg` and `ffprobe` must be present in the image. Pixel accounting uses `ffprobe`
  and the worker validates ffprobe at startup.
- MPEG-7 perceptual hashes are generated via ffmpeg's `signature` filter when
  `CalcPerceptualHash` is set. Missing filter or generation failure returns a transcode
  error. Verify with:
```bash
ffmpeg -filters | grep signature
```
- For NVIDIA builds, ffmpeg requires `nvcc` (use a CUDA devel base image) and the
  NVENC headers (`nv-codec-headers`). The Dockerfile installs these from the FFmpeg
  `nv-codec-headers` repo.
- For QSV builds, ffmpeg requires `libmfx-dev` and a VAAPI media driver
  (e.g. `intel-media-va-driver-non-free`).

## Image Size
Dockerfiles use multi-stage builds: ffmpeg is built in a builder stage and only the
ffmpeg install plus runtime libraries are copied into the final image.

## Notes
- **Version injection is required.** The orchestrator rejects transcoders with
  `version == "undefined"` (the Go default) if it has a `minVersion` constraint set by
  the gateway. The Dockerfiles and Makefile targets inject `core.LivepeerVersion` via
  `-ldflags` using `print_version.sh` at build time. Always build images via
  `make docker_remote_transcoder_nvidia` / `make docker_remote_transcoder_qsv` rather
  than raw `docker build`, or pass `--build-arg BUILD_VERSION=<version>` explicitly.
- The worker validates required encoders for the selected backend at startup
  (h264_nvenc / h264_qsv / libx264). Missing encoder is a fatal startup error.
- Capability detection is derived from `ffmpeg -encoders`, `ffmpeg -filters`, and
  `ffmpeg -decoders`.
- Optional H.264 decode capabilities are derived from `ffmpeg -h decoder=h264`
  supported pixel formats.
- QSV device selection expects explicit `/dev/dri/renderD*` paths.
- The worker logs the full ffmpeg command at Info level before each transcode, and
  ffmpeg stderr at Verbose level (requires `-v=6`).
