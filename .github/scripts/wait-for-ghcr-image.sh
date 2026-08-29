#!/usr/bin/env bash
# Polls a GHCR image manifest until it exists (bounded retries). Used by
# chart-publish jobs that depend on more than one sibling tag-triggered
# image-build workflow completing for the same release tag -- those builds
# run as fully independent GitHub Actions workflows with no cross-workflow
# ordering, so whichever one finishes first must wait for the others rather
# than assume they're already done.
#
# Usage: wait-for-ghcr-image.sh <image_repo> <tag> [max_attempts] [sleep_seconds]
#   image_repo: e.g. ghcr.io/osac-project/metering-m360-adapter
set -euo pipefail

IMAGE_REPO="$1"
TAG="$2"
MAX_ATTEMPTS="${3:-30}"
SLEEP_SECONDS="${4:-20}"
REPO_PATH="${IMAGE_REPO#ghcr.io/}"

for attempt in $(seq 1 "${MAX_ATTEMPTS}"); do
  token="$(curl -sf "https://ghcr.io/token?scope=repository:${REPO_PATH}:pull" | jq -r '.token // empty')"
  if [[ -n "${token}" ]] && curl -sf -o /dev/null \
      -H "Authorization: Bearer ${token}" \
      -H "Accept: application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json" \
      "https://ghcr.io/v2/${REPO_PATH}/manifests/${TAG}"; then
    echo "Found ${IMAGE_REPO}:${TAG} (attempt ${attempt}/${MAX_ATTEMPTS})"
    exit 0
  fi
  echo "Waiting for ${IMAGE_REPO}:${TAG} (attempt ${attempt}/${MAX_ATTEMPTS})..."
  sleep "${SLEEP_SECONDS}"
done

echo "::error::${IMAGE_REPO}:${TAG} did not appear in GHCR within $((MAX_ATTEMPTS * SLEEP_SECONDS))s -- its build may have failed or is taking unusually long"
exit 1
