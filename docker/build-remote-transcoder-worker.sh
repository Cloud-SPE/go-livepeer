#!/usr/bin/env bash
set -euo pipefail

TAG="${TAG:-latest}"
FFMPEG_VERSION="${FFMPEG_VERSION:-7.1.1}"
CUDA_VERSION="${CUDA_VERSION:-12.4.1}"
NV_CODEC_HEADERS_VERSION="${NV_CODEC_HEADERS_VERSION:-12.2.72.0}"

make remote_transcoder_worker

docker build \
  -t "remote-transcoder-worker-nvidia:${TAG}" \
  -f docker/Dockerfile.remote-transcoder-nvidia \
  --build-arg "FFMPEG_VERSION=${FFMPEG_VERSION}" \
  --build-arg "CUDA_VERSION=${CUDA_VERSION}" \
  --build-arg "NV_CODEC_HEADERS_VERSION=${NV_CODEC_HEADERS_VERSION}" \
  .

docker build \
  -t "remote-transcoder-worker-qsv:${TAG}" \
  -f docker/Dockerfile.remote-transcoder-qsv \
  --build-arg "FFMPEG_VERSION=${FFMPEG_VERSION}" \
  .
