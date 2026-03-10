# Remote Transcoder Replacement: Future Work

## Integration
1. Define deployment wiring for the new worker (systemd, Kubernetes, docker compose).
2. Add CI steps to build and publish NVIDIA/QSV images with pinned `FFMPEG_VERSION`.

## Validation
1. Run end‑to‑end smoke tests against a real orchestrator/gateway (segment in, multipart results out, verification enabled).
2. Verify image ffmpeg builds include `signature` and the target encoders (`h264_nvenc`, `h264_qsv`) on actual hardware.

## Performance
1. Benchmark against the current remote transcoder path (latency per segment, CPU/GPU utilization, throughput).
2. Tune presets/RC defaults for target hardware and bitrate ladder.

## Operational Hardening
1. Add a healthcheck (ffmpeg/ffprobe version, filter/encoder presence).
2. Improve failure logging for ffmpeg stderr to speed up debugging.
