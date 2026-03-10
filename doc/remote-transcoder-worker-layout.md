# Remote Transcoder Worker Layout (Draft)

This is the planned layout for the separate remote transcoder worker binary and its runtime contract. It is a draft to guide implementation and review.

## Directory Layout
```
cmd/remote_transcoder_worker/
  README.md
  main.go                # CLI + env parsing, runtime config, startup
  worker.go              # gRPC stream loop, NotifySegment handling
  ffmpeg_exec.go         # external ffmpeg invocation and result collection
  results_post.go        # /transcodeResults HTTP client
  capabilities.go        # capability detection and advertisement
  errors.go              # error mapping to RemoteTranscoderFatalError
```

## Responsibilities
- `main.go`
  - Parse CLI flags (and 1:1 env overrides).
  - Decide backend selection (default: NVIDIA -> QSV -> software).
  - Initialize worker and connect to orchestrator via gRPC.

- `worker.go`
  - Receive `NotifySegment` messages.
  - Handle session teardown signals.
  - Execute transcoding tasks and submit results.

- `ffmpeg_exec.go`
  - Construct ffmpeg CLI from profile metadata and runtime tuning options.
  - Execute ffmpeg, collect segment bytes and pixel counts.
  - Ensure output format is unchanged in Phase 1 (MPEGTS/MP4).

- `results_post.go`
  - POST multipart results to `/transcodeResults`.
  - Keep headers identical to the existing worker implementation.

- `capabilities.go`
  - Detect backend capabilities (NVIDIA or QSV).
  - Advertise capabilities via `RegisterTranscoder`.

- `errors.go`
  - Preserve current error semantics.
  - Map fatal errors to `RemoteTranscoderFatalError`.

## Configuration
No config file in Phase 1. CLI flags with environment variable overrides.

## Docker Images
Two images: NVIDIA and QSV (separate toolchains and drivers).
See `docker/Dockerfile.remote-transcoder-nvidia` and `docker/Dockerfile.remote-transcoder-qsv`.
