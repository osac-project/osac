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

# Reject values that could break or inject a workflow command into any echo
# below -- every line in this script's output starts with one of these two
# caller-controlled values, and GitHub Actions treats any log line starting
# with "::" as a workflow command, not just ones this script intends as such.
# Current callers only pass hardcoded repos and semver-validated tags, but
# this script is written to be reusable.
for value in "${IMAGE_REPO}" "${TAG}"; do
  if [[ "${value}" == *$'\n'* || "${value}" == *$'\r'* || "${value}" == *"::"* ]]; then
    echo "::error::Invalid argument '${value//$'\n'/\\n}' -- must not contain newlines or '::'"
    exit 1
  fi
done

REPO_PATH="${IMAGE_REPO#ghcr.io/}"

for attempt in $(seq 1 "${MAX_ATTEMPTS}"); do
  # Token fetch is inside the if condition (not a separate `token=$(...)`
  # assignment) so a transient failure here is caught by the retry loop
  # instead of tripping `set -e` and aborting the whole script on attempt 1.
  if token="$(curl -sf --connect-timeout 5 --max-time 15 \
        "https://ghcr.io/token?scope=repository:${REPO_PATH}:pull" | jq -r '.token // empty')" \
      && [[ -n "${token}" ]] \
      && curl -sf --connect-timeout 5 --max-time 15 -o /dev/null \
        -H "Authorization: Bearer ${token}" \
        -H "Accept: application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json" \
        "https://ghcr.io/v2/${REPO_PATH}/manifests/${TAG}"; then
    echo "Found ${IMAGE_REPO}:${TAG} (attempt ${attempt}/${MAX_ATTEMPTS})"
    exit 0
  fi
  echo "Waiting for ${IMAGE_REPO}:${TAG} (attempt ${attempt}/${MAX_ATTEMPTS})..."
  sleep "${SLEEP_SECONDS}"
done

echo "::error::${IMAGE_REPO}:${TAG} did not appear in GHCR within $((MAX_ATTEMPTS * SLEEP_SECONDS))s -- its build may have failed, the package may not be public (anonymous pull token would come back empty), or it's taking unusually long"
exit 1
