#!/usr/bin/env bash
# Build and push the multi-arch OSAC CI CLI image.
#
# Usage:
#   tools/ci-cli/build-push.sh                  # build + push :latest
#   tools/ci-cli/build-push.sh v0.1.0           # build + push :v0.1.0 + :latest
#   BUILD_ONLY=1 tools/ci-cli/build-push.sh     # build locally, skip push
#
# Requirements:
#   - docker with buildx OR podman with buildah
#   - Push access to the target registry (quay.io/osac-project/ci-cli)
#
# The script must be run from the mono-repo root because the
# Containerfile build context is the repository root.
set -euo pipefail

IMAGE="${IMAGE:-quay.io/osac-project/ci-cli}"
TAG="${1:-latest}"
PLATFORMS="${PLATFORMS:-linux/amd64,linux/arm64}"
CONTAINERFILE="tools/ci-cli/Containerfile"
BUILD_ONLY="${BUILD_ONLY:-}"

# Ensure we are at the repo root (Containerfile expects mono-repo context).
if [[ ! -f "${CONTAINERFILE}" ]]; then
  echo "ERROR: Run this script from the mono-repo root." >&2
  exit 1
fi

tags=("--tag" "${IMAGE}:${TAG}")
if [[ "${TAG}" != "latest" ]]; then
  tags+=("--tag" "${IMAGE}:latest")
fi

echo "==> Building ${IMAGE}:${TAG} for ${PLATFORMS}"

if command -v docker &>/dev/null && docker buildx version &>/dev/null; then
  # ---------- docker buildx ----------
  if [[ -n "${BUILD_ONLY}" ]]; then
    # Local build (single platform only -- buildx multi-arch requires push or
    # --output type=oci).
    docker buildx build \
      --file "${CONTAINERFILE}" \
      "${tags[@]}" \
      --load \
      .
  else
    docker buildx build \
      --platform "${PLATFORMS}" \
      --file "${CONTAINERFILE}" \
      "${tags[@]}" \
      --push \
      .
  fi
elif command -v podman &>/dev/null; then
  # ---------- podman manifest ----------
  manifest="${IMAGE}:${TAG}"
  podman manifest rm "${manifest}" 2>/dev/null || true

  podman manifest create "${manifest}"
  IFS=',' read -ra archs <<< "${PLATFORMS}"
  for platform in "${archs[@]}"; do
    podman build \
      --platform "${platform}" \
      --file "${CONTAINERFILE}" \
      --manifest "${manifest}" \
      .
  done

  if [[ -z "${BUILD_ONLY}" ]]; then
    echo "==> Pushing manifest ${manifest}"
    podman manifest push --all "${manifest}" "docker://${manifest}"
    if [[ "${TAG}" != "latest" ]]; then
      podman tag "${manifest}" "${IMAGE}:latest"
      podman manifest push --all "${IMAGE}:latest" "docker://${IMAGE}:latest"
    fi
  fi
else
  echo "ERROR: Neither docker (with buildx) nor podman found." >&2
  exit 1
fi

echo "==> Done: ${IMAGE}:${TAG}"
