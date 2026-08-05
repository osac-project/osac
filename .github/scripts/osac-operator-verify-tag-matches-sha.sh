#!/usr/bin/env bash
# Verifies that a git tag still resolves to the guarded commit SHA that was
# validated by this workflow's guard job, protecting against a force-push/
# retag race between the image build and any subsequent chart-publish or
# release step.
#
# Required env vars: GH_TOKEN, REPO, TAG, GUARDED_SHA
set -euo pipefail

ref_json="$(gh api "repos/${REPO}/git/ref/tags/${TAG}" --jq '[.object.type, .object.sha] | @tsv')"
read -r current_type current_sha <<< "$ref_json"
if [[ "$current_type" == "tag" ]]; then
  # Annotated tag: the ref's object.sha is the tag object, not the commit - peel it.
  current_sha="$(gh api "repos/${REPO}/git/tags/${current_sha}" --jq .object.sha)"
fi

if [[ "$current_sha" != "$GUARDED_SHA" ]]; then
  echo "::error::Tag '${TAG}' now points at ${current_sha}, not the guarded commit ${GUARDED_SHA} (force-pushed?). Refusing to proceed."
  exit 1
fi

echo "Tag '${TAG}' still points at the guarded commit ${GUARDED_SHA}."
