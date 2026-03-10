# Remote Transcoder Worker (Draft)

This directory is reserved for the standalone remote transcoder worker binary.

Phase 1 goals:
- Keep the gRPC `RegisterTranscoder` + HTTP `/transcodeResults` protocols unchanged.
- Replace lpms/ffmpeg with an external ffmpeg binary built into the worker image.
- Support NVIDIA and Intel QuickSync via separate Docker images.
- Keep output formats unchanged (MPEGTS/MP4) to avoid gateway/orchestrator changes.
- Compute pixel counts via `ffprobe` and generate MPEG-7 signatures when requested (strict enforcement).
- Capability detection is derived from `ffmpeg -encoders`, `ffmpeg -filters`, and `ffmpeg -decoders`.

See [remote-transcoder-worker-ops.md](/home/mazup/git-repos/livepeer-cloud-spe/go-livepeer-master/doc/remote-transcoder-worker-ops.md) for build/run and dependency notes.

Configuration:
- CLI flags with 1:1 environment variable overrides.
- Defaults align with existing profile settings.
