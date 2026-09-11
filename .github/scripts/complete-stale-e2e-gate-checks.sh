#!/usr/bin/env bash
# One-off cleanup: complete orphaned in_progress e2e-*-gate API checks when
# native gate jobs already succeeded on HEAD_SHA. Run via workflow_dispatch.

set -euo pipefail

readonly GATES=(e2e-vmaas-gate e2e-bmaas-gate e2e-caas-gate)
# Matches external_id set by invalidate-e2e-gates when present on newer checks.
readonly INVALIDATE_EXTERNAL_ID_PREFIX="osac-invalidate-e2e-gate"

if [[ -z "${HEAD_SHA:-}" || -z "${REPO:-}" ]]; then
  echo "HEAD_SHA and REPO are required" >&2
  exit 1
fi

tmpdir=$(mktemp -d)
page=1
while true; do
  resp=$(gh api "repos/${REPO}/commits/${HEAD_SHA}/check-runs?per_page=100&page=${page}&filter=all")
  jq -c '.check_runs' <<<"${resp}" > "${tmpdir}/page-${page}.json"
  count=$(jq '.check_runs | length' <<<"${resp}")
  if [[ "${count}" -lt 100 ]]; then
    break
  fi
  page=$((page + 1))
done
check_runs=$(jq -s 'add' "${tmpdir}"/page-*.json)
rm -rf "${tmpdir}"

completed_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
title="Superseded by native e2e gate job"
summary="Manual cleanup of stale invalidate-e2e-gates check; gate already success on this SHA."
failed=0

for gate in "${GATES[@]}"; do
  if ! jq -e --arg g "${gate}" '
    [.[] | select(
      .name == $g
      and .status == "completed"
      and .conclusion == "success"
      and ((.details_url // "") | test("/actions/runs/[0-9]+/job/"))
    )] | length > 0
  ' <<<"${check_runs}" >/dev/null; then
    echo "Skipping ${gate}: no native gate job success on this SHA"
    continue
  fi
  while IFS= read -r id; do
    [[ -z "${id}" || "${id}" == "null" ]] && continue
    payload=$(jq -n \
      --arg status "completed" \
      --arg conclusion "success" \
      --arg completed_at "${completed_at}" \
      --arg title "${title}" \
      --arg summary "${summary}" \
      '{
        status: $status,
        conclusion: $conclusion,
        completed_at: $completed_at,
        output: {title: $title, summary: $summary}
      }')
    if gh api "repos/${REPO}/check-runs/${id}" -X PATCH --input - <<<"${payload}"; then
      echo "Completed stale in_progress ${gate} check ${id}"
    else
      echo "Failed to complete ${gate} check ${id}" >&2
      failed=1
    fi
  done < <(jq -r --arg g "${gate}" --arg prefix "${INVALIDATE_EXTERNAL_ID_PREFIX}" '
    [.[] | select(
      .name == $g
      and .status == "in_progress"
      and (
        ((.external_id // "") | startswith($prefix))
        or ((.details_url // "") | test("^https://github.com/[^/]+/[^/]+/runs/[0-9]+$"))
      )
      and not ((.details_url // "") | test("/actions/runs/[0-9]+/job/"))
    ) | .id] | .[]
  ' <<<"${check_runs}")
done

exit "${failed}"
