#!/usr/bin/env bash
# Build and push the multi-arch OSAC CI CLI image.
#
# Usage:
#   tools/ci-cli/build-push.sh                  # build + push :latest
#   tools/ci-cli/build-push.sh v0.1.0           # build + push :v0.1.0 + :latest
#   BUILD_ONLY=1 tools/ci-cli/build-push.sh     # build locally, skip push
#
# Requirements:
#   - podman
#   - Push access to the target registry (quay.io/osac-project/ci-cli)
#
# The script must be run from the mono-repo root because the
# Containerfile build context is the repository root.
set -euo pipefail

IMAGE="${IMAGE:-quay.io/osac-project/ci-cli}"
TAG="${1:-latest}"
CONTAINERFILE="tools/ci-cli/Containerfile"
BUILD_ONLY="${BUILD_ONLY:-}"

# Ensure we are at the repo root (Containerfile expects mono-repo context).
if [[ ! -f "${CONTAINERFILE}" ]]; then
  echo "ERROR: Run this script from the mono-repo root." >&2
  exit 1
fi

if ! command -v podman &>/dev/null; then
  echo "ERROR: podman is required but not found." >&2
  exit 1
fi

echo "==> Building ${IMAGE}:${TAG} for linux/amd64,linux/arm64"

# Clean up any previous manifest with the same name.
podman manifest rm "${IMAGE}:${TAG}" 2>/dev/null || true

podman manifest create "${IMAGE}:${TAG}"

for arch in amd64 arm64; do
  echo "==> Building linux/${arch}"
  podman build \
    --platform "linux/${arch}" \
    --build-arg VERSION="${TAG}" \
    --file "${CONTAINERFILE}" \
    -t "${IMAGE}:${TAG}-${arch}" \
    .
  podman manifest add "${IMAGE}:${TAG}" "${IMAGE}:${TAG}-${arch}"
done

if [[ -z "${BUILD_ONLY}" ]]; then
  echo "==> Pushing manifest ${IMAGE}:${TAG}"
  podman manifest push --all "${IMAGE}:${TAG}" "docker://${IMAGE}:${TAG}"

  if [[ "${TAG}" != "latest" ]]; then
    echo "==> Tagging and pushing ${IMAGE}:latest"
    podman tag "${IMAGE}:${TAG}" "${IMAGE}:latest"
    podman manifest push --all "${IMAGE}:${TAG}" "docker://${IMAGE}:latest"
  fi
fi

echo "==> Done: ${IMAGE}:${TAG}"
