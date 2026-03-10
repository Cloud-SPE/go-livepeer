# Remote Transcoder Replacement Design (QuickSync + NVIDIA)

## Context
This document defines a drop-in replacement for the existing Remote Transcoder path in go-livepeer. The gateway and orchestrator flow must remain unchanged. This is not for BYOC flows. The replacement must keep the gRPC `RegisterTranscoder` stream and the HTTP `/transcodeResults` contract. Session stickiness and load accounting must be preserved. The replacement must be at least as fast as the current path.

See [remote-transcoder-worker-ops.md](/home/mazup/git-repos/livepeer-cloud-spe/go-livepeer-master/doc/remote-transcoder-worker-ops.md) for build/run instructions.

## Decisions (Explicit)
We will not change BYOC paths. We will keep the gRPC `RegisterTranscoder` and HTTP `/transcodeResults` protocols unchanged. We will keep the per-session stickiness semantics in `RemoteTranscoderManager`. We will preserve existing timeouts and error semantics, so the gateway and orchestrator behavior stays the same. We will add Intel QuickSync and NVIDIA hardware transcoding options as selectable backends in the remote transcoder worker.

## Goals
Drop-in replacement for the Remote Transcoder path with identical external behavior. Preserve gateway to orchestrator selection, session start, per-segment processing, and teardown sequence. Support Intel QuickSync and NVIDIA acceleration in the remote transcoder worker. Keep existing timeouts, backpressure, and error handling semantics.

## Non-Goals
No changes to gateway selection logic or discovery. No changes to `/segment` protocol or payment flow. No changes to BYOC or AI flows. No changes to the orchestrator public APIs.

## Existing Flow Summary (Must Remain Identical)
1. Gateway selects orchestrators via discovery and `GetOrchestratorInfo`. It creates `BroadcastSession` objects with `AuthToken`, `TicketParams`, and `PriceInfo`. See `server/broadcast.go` and `discovery/discovery.go`.
2. For each segment, the gateway calls `SubmitSegment` which POSTs `/segment` to the orchestrator with signed segment credentials and payment. See `server/segment_rpc.go`.
3. Orchestrator `/segment` validates signature, capabilities, auth token, capacity, and balance. It then calls `TranscodeSeg`. See `server/segment_rpc.go`.
4. `TranscodeSeg` enqueues work into a per-session channel. If the channel is full, it returns `ErrOrchBusy`. See `core/orchestrator.go`.
5. The remote transcoder path is invoked via `RemoteTranscoderManager.Transcode`. This selects a remote transcoder for the session (sticky assignment) and sends `NotifySegment` on the gRPC stream. See `core/orchestrator.go`.
6. The remote transcoder worker downloads the segment from the provided URL, transcodes, and POSTs `/transcodeResults` with multipart output or an error body. See `server/ot_rpc.go`.
7. Orchestrator receives `/transcodeResults`, maps `TaskId` to the waiting task channel, and returns results to the gateway. See `server/ot_rpc.go`.
8. Gateway downloads results, updates playlists, and continues with the next segment. See `server/broadcast.go`.
9. When the stream ends, gateway calls `EndTranscodingSession` for each session. Orchestrator validates auth token and tears down session resources and remote transcoder session mapping. See `server/rpc.go` and `core/orchestrator.go`.

## Current Remote Worker Implementation (Phase 0)
The current remote transcoder worker is implemented in `server/ot_rpc.go`. It:
- Receives `NotifySegment` from the gRPC stream, parses `SegData`, downloads the segment, writes it to disk, and calls `n.Transcoder.Transcode`.
- `n.Transcoder` is implemented in `core/transcoder.go` using lpms/ffmpeg bindings (software or hardware).
- Output formats and profiles are derived from `SegData.Profiles` via `profilesToTranscodeOptions`.

This means the existing remote worker uses the lpms/ffmpeg toolchain directly.

## Phase 1 Change (Only Worker Transcode Engine) — Implemented
A separate remote worker binary and Docker image speaks the same gRPC and HTTP protocols but **does not use lpms/ffmpeg** for transcoding. Instead it shells out to a custom ffmpeg binary built and bundled in the image. The orchestrator and gateway are unchanged.

### HLS Timestamp Continuity (Implemented)
The LPMS path maintains cross-segment timestamp continuity via a persistent C-library session that tracks `dts_diff` across segments (`lpms/ffmpeg/transcoder.c`). The external worker is stateless — each ffmpeg invocation is a fresh process that would reset PTS to 0, causing HLS players to stall after segment 1.

The following global flags replicate LPMS session behavior:
```
-copyts                      Copy input PTS/DTS to output unchanged
-avoid_negative_ts disabled  Do not shift timestamps for B-frame negative DTS
```

Per-output flags:
```
-fps_mode passthrough   Pass frames with exact original timestamps
-muxdelay 0             Remove per-segment muxer padding (~0.7s default)
-muxpreload 0           Remove initial muxer pre-load delay
-flags +cgop            Force closed GOP for independently decodable HLS segments
```

## Compatibility Contracts (Do Not Change)
gRPC: `RegisterTranscoder` is used for connection and capability advertisement. It must accept the same `RegisterRequest` fields and keep the same error semantics.

NotifySegment: `RemoteTranscoder.Transcode` sends a `NotifySegment` with `Url`, `TaskId`, `OrchId`, and `SegData`. The replacement must accept the same fields and behave identically.

HTTP `/transcodeResults`: The remote transcoder must POST to `https://<orchAddr>/transcodeResults` with:
- `Authorization: Livepeer-Transcoder-1.0`
- `Credentials: <orchSecret>`
- `TaskId: <id>`
- `Pixels: <decodedPixels>`
- `Content-Type: multipart/mixed; boundary=...` or `livepeer/transcoding-error`

Multipart parts must include per-rendition `Pixels` headers and optional perceptual hash parts as `application/octet-stream`. See `server/ot_rpc.go`.

## Timeouts and Backpressure (Must Be Preserved)
The orchestrator path uses timeouts and backpressure that trigger failover or teardown. The replacement must stay within these timeboxes.
- Per-segment remote transcoder timeout is `max(common.HTTPTimeout, 4 * segmentDuration)`. See `core/orchestrator.go`.
- Per-session idle teardown uses `transcodeLoopTimeout` (70 seconds). See `core/orchestrator.go`.
- Gateway upload and total timeouts are in `SubmitSegment`. See `server/segment_rpc.go`.
- Orchestrator backpressure is enforced by a non-blocking per-session channel with size `maxSegmentChannels`. See `core/orchestrator.go`.

## Drop-In Replacement Architecture
The replacement is a new remote transcoder worker implementation that keeps the same control-plane and data-plane interfaces. The orchestrator and gateway are unchanged.

### Control Plane
The worker uses the existing `RegisterTranscoder` gRPC stream. It advertises capabilities via the existing `Capabilities` field. Capabilities are derived from the selected hardware backend and supported profiles.

### Data Plane
On each `NotifySegment`:
1. Parse and validate `SegData` via `coreSegMetadata`.
2. Download the segment from `notify.Url`.
3. Write to local workdir if required by backend.
4. Transcode using the selected hardware backend (QuickSync or NVIDIA).
5. Compute pixel counts using `ffprobe` (decoded pixels for input, encoded pixels per output).
6. If `CalcPerceptualHash` is set, generate MPEG-7 signatures via ffmpeg’s `signature` filter. Phase 3 enforces this strictly: missing filter or generation failure is a transcode error.
7. Construct multipart results and POST `/transcodeResults` with the same headers and body format.
8. For errors, POST `livepeer/transcoding-error` with the error body.

### Session Lifecycle
The orchestrator signals session teardown by sending a `NotifySegment` with an empty `Url` and an `AuthToken.SessionId`. The worker must call `EndTranscodingSession(sessionId)` on its local transcoder implementation to release any backend session state.

### Sticky Session Semantics
Sticky session assignment is enforced on the orchestrator side via `RemoteTranscoderManager.streamSessions`. The replacement worker must not introduce behavior that breaks this, such as rejecting tasks based on session state or rerouting tasks. It must process tasks in the order received from the stream.

## Hardware Backends
The worker provides two hardware backends. Each backend implements the same internal `Transcode` interface so the worker logic remains identical.

### NVIDIA Backend — Implemented
Uses NVIDIA NVENC via external ffmpeg. Key behavior:
- Device selection via `NVIDIA_DEVICES` / `-nvidia`; defaults to `all`.
- `-hwaccel cuda` enables CUDA-accelerated decode.
- Encoder: `h264_nvenc` or `hevc_nvenc` based on profile.
- Hardcoded quality flags applied to all NVENC outputs:
  - `-spatial-aq 1 -temporal-aq 1`: adaptive quantization (dedicated NVENC silicon, no shader cost)
  - `-rc-lookahead 32`: 32-frame HW lookahead for better VBR bit allocation (Pascal/GTX 10xx and newer)
  - `-color_range tv`: limited range tag for HLS/broadcast compatibility
- Recommended runtime defaults: `TRANSCODER_PRESET=p5`, `TRANSCODER_RC=vbr`
- `-multipass fullres` is intentionally omitted: requires Turing (RTX 20xx+), hard-errors on Pascal
- LPMS comparison: LPMS uses CBR (`rc_min=rc_max=bitrate`) with no AQ or lookahead. The worker's VBR+AQ+lookahead configuration is strictly better in perceptual quality at equal average bitrate.
- Pixel accounting: ffprobe-based (decoded pixels for input, encoded pixels per output).

**NVENC session limits:** Consumer Pascal GPUs (GTX 1070/1080) cap at 3 concurrent NVENC sessions. Each rendition consumes one session; a 3-rendition job uses all 3. Set `TRANSCODER_CAPACITY=1` on Pascal hardware without the [nvidia-patch](https://github.com/keylase/nvidia-patch). With nvidia-patch applied, the session cap is removed and capacity can be set freely. Turing and Ampere have no meaningful session cap by default.

### Intel QuickSync Backend — Implemented
Uses Intel QSV via external ffmpeg. Key behavior:
- Device selection via `QSV_DEVICES` / `-qsv`; expects `/dev/dri/renderD*` paths.
- Full zero-copy HW pipeline: `-hwaccel qsv -hwaccel_output_format qsv` keeps decoded frames in QSV surface memory through to encode.
- Encoder: `h264_qsv` or `hevc_qsv` based on profile.
- Hardcoded quality flags:
  - `-vf scale_qsv=W:H`: GPU-native scaling (required; `-s:v` forces GPU→CPU download)
  - `-bf 0`: B-frames disabled to prevent DTS reordering across segment boundaries
- Recommended runtime defaults: `TRANSCODER_RC=vbr`, `TRANSCODER_CRF=23`
- Pixel accounting: ffprobe-based.

### Capability Advertisement
On startup, detect available backends and advertise capabilities accordingly. If the chosen backend cannot satisfy the requested profiles, the worker should return a transcode error and allow the orchestrator to handle failover.

Phase 2 update: capability detection inspects `ffmpeg -encoders` and `ffmpeg -filters`:
- Required encoder for the selected backend must be present or startup fails (h264_nvenc/h264_qsv/libx264).
- `Capability_MPEG7VideoSignature` is advertised only if the `signature` filter exists.
- `Capability_HEVC_Encode` is advertised if the backend supports HEVC encoding.
Phase 3 update:
- Optional decode capabilities are derived from `ffmpeg -decoders`, and H.264 pixel format support is derived from `ffmpeg -h decoder=h264`:
  - `HEVC_Decode`, `VP8_Decode`, `VP9_Decode`
  - `H264_Decode_444_8bit`, `H264_Decode_422_8bit`, `H264_Decode_444_10bit`, `H264_Decode_422_10bit`, `H264_Decode_420_10bit`

Phase 3 update:
- If `CalcPerceptualHash` is true and `signature` is unavailable, the worker fails the segment before transcoding.
- If signature generation fails for any output, the worker returns a transcode error (no best‑effort fallback).

## Docker Images (Draft)
We will ship separate worker images for NVIDIA and QSV to avoid driver and library conflicts.

### NVIDIA Worker (Draft, Multi-stage)
```dockerfile
ARG CUDA_VERSION=12.4.1
FROM nvidia/cuda:${CUDA_VERSION}-devel-ubuntu22.04 AS build

ARG FFMPEG_VERSION=7.1.1
ARG NV_CODEC_HEADERS_VERSION=12.2.72.0
ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential git pkg-config yasm nasm ca-certificates curl \
    libx264-dev libx265-dev libvpx-dev \
    && rm -rf /var/lib/apt/lists/*

# Install NVENC headers (ffnvcodec) for ffmpeg
RUN git clone --depth 1 --branch n${NV_CODEC_HEADERS_VERSION} https://github.com/FFmpeg/nv-codec-headers.git /nv-codec-headers \
    && cd /nv-codec-headers \
    && make -j"$(nproc)" \
    && make install \
    && rm -rf /nv-codec-headers

# Build ffmpeg with NVENC/NVDEC support (flags may vary by version)
RUN git clone --depth 1 --branch n${FFMPEG_VERSION} https://github.com/FFmpeg/FFmpeg.git /ffmpeg \
    && cd /ffmpeg \
    && ./configure \
      --prefix=/opt/ffmpeg \
      --enable-gpl --enable-nonfree \
      --enable-libx264 --enable-libx265 --enable-libvpx \
      --enable-cuda-nvcc --enable-nvenc --enable-libnpp \
      --extra-cflags=-I/usr/local/cuda/include \
      --extra-ldflags=-L/usr/local/cuda/lib64 \
    && make -j"$(nproc)" && make install

FROM nvidia/cuda:${CUDA_VERSION}-runtime-ubuntu22.04
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libx264-163 libx265-199 libvpx7 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /opt/ffmpeg /opt/ffmpeg
ENV PATH="/opt/ffmpeg/bin:${PATH}"
ENV LD_LIBRARY_PATH="/opt/ffmpeg/lib:${LD_LIBRARY_PATH}"

# Copy worker binary
COPY bin/remote-transcoder-worker /usr/local/bin/remote-transcoder-worker
ENTRYPOINT ["/usr/local/bin/remote-transcoder-worker"]
```

### QSV Worker (Draft, Multi-stage)
```dockerfile
FROM ubuntu:22.04 AS build

ARG FFMPEG_VERSION=7.1.1
ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential git pkg-config yasm nasm ca-certificates curl \
    libx264-dev libx265-dev libvpx-dev \
    libva-dev libmfx-dev intel-media-va-driver-non-free \
    && rm -rf /var/lib/apt/lists/*

# Install Intel media drivers / oneVPL as required by QSV (exact packages may vary)
# RUN apt-get install -y intel-media-va-driver-non-free libmfx-dev

RUN git clone --depth 1 --branch n${FFMPEG_VERSION} https://github.com/FFmpeg/FFmpeg.git /ffmpeg \
    && cd /ffmpeg \
    && ./configure \
      --prefix=/opt/ffmpeg \
      --enable-gpl --enable-nonfree \
      --enable-libx264 --enable-libx265 --enable-libvpx \
      --enable-libmfx --enable-vaapi \
    && make -j"$(nproc)" && make install

FROM ubuntu:22.04
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libx264-163 libx265-199 libvpx7 \
    libva2 libmfx1 \
    intel-media-va-driver-non-free \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /opt/ffmpeg /opt/ffmpeg
ENV PATH="/opt/ffmpeg/bin:${PATH}"
ENV LD_LIBRARY_PATH="/opt/ffmpeg/lib:${LD_LIBRARY_PATH}"

COPY bin/remote-transcoder-worker /usr/local/bin/remote-transcoder-worker
ENTRYPOINT ["/usr/local/bin/remote-transcoder-worker"]
```

Notes:
- These Dockerfiles are intentionally draft; exact flags and packages should be finalized based on the target ffmpeg version and driver stack.
- Ensure `ffprobe` is included in the image; pixel accounting uses ffprobe output.
- This approach removes any runtime dependency on lpms/ffmpeg in the worker.

## Keeping ffmpeg Current (Process Guidance)
Using a Docker build is enough **only if you rebuild it**. Without a rebuild process, the ffmpeg version will drift.
Recommended approach:
- Pin `FFMPEG_VERSION` in the Dockerfile.
- Add a periodic rebuild (manual or scripted) that bumps the version, runs worker tests, and publishes a new image.
- Keep the worker binary stable so only ffmpeg changes between releases.

Build pipeline hooks:
- `make docker_remote_transcoder_nvidia` / `make docker_remote_transcoder_qsv`
- `docker/build-remote-transcoder-worker.sh`

## Configuration
Decision: no config file in Phase 1. Use CLI flags with container-level environment variables for 1:1 overrides. CLI takes precedence over environment variables.

Existing CLI flags in this repo (must continue to work):
- `-transcoder` (enable standalone transcoder)
- `-orchSecret` (shared secret with orchestrator)
- `-nvidia` (comma-separated NVIDIA GPU device IDs or `all`)
- `-netint` (comma-separated Netint device GUIDs or `all`)
- `-maxSessions` (capacity for transcoder)
- `-transcodingOptions` (profiles)
See `cmd/livepeer/starter/flags.go` and `cmd/livepeer/starter/starter.go`.

New flags proposed for QuickSync (to add, not currently present):
- `-qsv` (comma-separated Intel QuickSync device IDs or `all`)
- `-transcoderBackend` (optional override: `nvidia` or `qsv`)

Worker behavior:
- If `-transcoderBackend` is set, prefer that backend and fail fast if unavailable.
- If not set, default selection order is `nvidia` first, then `qsv`, then software.
- `-capacity` maps to `RegisterRequest.Capacity` via existing `-maxSessions` semantics.
- `-workDir` remains the local segment staging directory (existing `WorkDir`).

## Runtime Quality Tuning (Worker-Only) — Implemented

Runtime configuration adjusts quality without changing gateway/orchestrator behavior. All tuning is global (no per-profile overrides). CLI flags take precedence over env vars.

| Env var | Description | NVIDIA default | QSV default |
|---|---|---|---|
| `TRANSCODER_PRESET` | Encoder preset | `p5` | — |
| `TRANSCODER_RC` | Rate control mode | `vbr` | `vbr` |
| `TRANSCODER_CRF` | Quality target (0 = unset) | — | `23` |
| `TRANSCODER_MAXRATE` | Peak bitrate cap (e.g. `8000k`) | unset | unset |
| `TRANSCODER_BUFSIZE` | VBV buffer size (e.g. `16000k`) | unset | unset |
| `TRANSCODER_GOP` | Keyframe interval: frames (`60`) or duration (`2s`) | unset | unset |
| `TRANSCODER_TUNE` | Encoder tune (libx264/libx265 only) | unset | unset |

**VBV guidance:** Leave `TRANSCODER_MAXRATE` and `TRANSCODER_BUFSIZE` unset unless
you have a CDN peak-bitrate delivery constraint. A global maxrate applied to a
multi-rendition ladder is problematic — a single cap value simultaneously over-constrains
high-bitrate renditions and under-constrains low-bitrate ones. If set, use a value
≥ the highest rendition bitrate and `bufsize = 2 × maxrate`.

**Additional hardcoded NVENC flags** (not configurable via env, applied to all nvenc outputs):
- `-spatial-aq 1`, `-temporal-aq 1`: AQ — see NVIDIA Backend section
- `-rc-lookahead 32`: HW lookahead — see NVIDIA Backend section
- `-color_range tv`: color range correction — see NVIDIA Backend section

## Dependency Impact (Leaving lpms/ffmpeg in Repo)
Leaving lpms/ffmpeg in the repository does not affect the new worker runtime as long as the new worker binary does not import or invoke those packages. The only impact is build/dependency surface in the main repo. Since we are using a separate worker binary/image, we can keep the dependency out of that binary entirely.

## Implementation Touch Points (Concrete Files)
Transcoder flags and config wiring:
- `cmd/livepeer/starter/flags.go`: add `-qsv` and `-transcoderBackend` flags.
- `cmd/livepeer/starter/starter.go`: add defaults, validate conflicts (`-qsv` vs `-nvidia` vs `-netint`), select accel and device list, and pass into transcoder factory.

Transcoder backend implementation:
- `core/transcoder.go`: add `IntelQSVTranscoder` and `NewIntelQSVTranscoder`, wire into `GetTranscoderFactoryByAccel`, and ensure `profilesToTranscodeOptions` supports the new accel value.
- `core/transcoder_test.go`: add QSV tests analogous to NVIDIA/Netint tests.

Device parsing:
- `common/util.go`: extend `ParseAccelDevices` to support QSV device discovery (or parse explicit IDs only).
- `common/util_test.go`: add QSV parsing tests.

Benchmark and docs:
- `cmd/livepeer_bench/livepeer_bench.go`: optionally add `-qsv` to mirror `-nvidia` and `-netint`.
- `doc/gpu.md`: document Intel QuickSync support and requirements.

## Error Handling (Must Match Current Semantics)
Errors must be reported via `/transcodeResults` with `livepeer/transcoding-error` to preserve orchestrator behavior. Fatal errors must follow the same `RemoteTranscoderFatalError` semantics to avoid changing the retry loop in `RunTranscoder`.

## Metrics and Observability
Preserve existing logging and monitor hooks in the worker path for segment upload and transcode timing. The orchestrator and gateway metrics remain unchanged.

## Rollout Plan
1. Implement new worker backend selection and capability detection.
2. Keep the existing worker logic and transport unchanged.
3. Validate using existing tests around `ot_rpc` and remote transcoder flows.
4. Run mixed deployments with existing and new workers; orchestrator will treat them uniformly.

## Risks
Hardware-specific encoding differences must not change output format expectations. Exceeding existing timeouts will trigger failover or teardown, so backend performance must be at least as fast as current behavior.

## Test Plan (Existing Tests To Reuse)
- `server/ot_rpc_test.go` validates `/transcodeResults` parsing, auth headers, and multipart results.
- `core/orch_test.go` exercises `RemoteTranscoderManager`, sticky session assignment, and timeout/error handling.
- `server/rpc_test.go` includes `RegisterTranscoder` gRPC behavior stubs.

## Additional Tests (Recommended)
- New unit tests for backend selection precedence (`-transcoderBackend`, `-qsv`, `-nvidia`).
- New integration test that runs a remote transcoder worker with QSV and NVIDIA backends against a local orchestrator using existing `/transcodeResults` contract.
