#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?GH_TOKEN is required}"
: "${REPO:?REPO is required}"
: "${PR_NUMBER:?PR_NUMBER is required}"
: "${EXPECTED_HEAD_SHA:?EXPECTED_HEAD_SHA is required}"
: "${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"

if [[ ! "${PR_NUMBER}" =~ ^[0-9]+$ || ! "${EXPECTED_HEAD_SHA}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "PR_NUMBER must be numeric and EXPECTED_HEAD_SHA must be a 40-character SHA." >&2
  exit 1
fi

pr_json=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}")
state=$(jq -r '.state' <<<"${pr_json}")
base_ref=$(jq -r '.base.ref // empty' <<<"${pr_json}")
head_sha=$(jq -r '.head.sha // empty' <<<"${pr_json}")
head_repo=$(jq -r '.head.repo.full_name // empty' <<<"${pr_json}")
head_is_fork=$(jq -r '.head.repo.fork // false' <<<"${pr_json}")
head_ref=$(jq -r '.head.ref // empty' <<<"${pr_json}")
author=$(jq -r '.user.login // empty' <<<"${pr_json}")
association=$(jq -r '.author_association // empty' <<<"${pr_json}")

if [[ "${state}" != "open" || "${base_ref}" != "main" ]]; then
  echo "PR #${PR_NUMBER} must be open and target main." >&2
  exit 1
fi
if [[ "${head_sha}" != "${EXPECTED_HEAD_SHA}" ]]; then
  echo "PR #${PR_NUMBER} moved: expected ${EXPECTED_HEAD_SHA}, current ${head_sha}." >&2
  exit 1
fi
if [[ -z "${head_repo}" || -z "${head_ref}" || -z "${author}" ]]; then
  echo "PR #${PR_NUMBER} is missing head repository, ref, or author metadata." >&2
  exit 1
fi

{
  echo "head-sha=${head_sha}"
  echo "head-repo=${head_repo}"
  echo "head-is-fork=${head_is_fork}"
  echo "head-ref=${head_ref}"
  echo "author=${author}"
  echo "author-association=${association}"
} >>"${GITHUB_OUTPUT}"
