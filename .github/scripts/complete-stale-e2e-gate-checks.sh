#!/usr/bin/env bash
# One-off cleanup: complete orphaned in_progress e2e-*-gate API checks when
# native gate jobs already succeeded on HEAD_SHA. Run via workflow_dispatch.

set -euo pipefail

readonly GATES=(e2e-vmaas-gate e2e-bmaas-gate e2e-caas-gate)

if [[ -z "${HEAD_SHA:-}" || -z "${REPO:-}" ]]; then
  echo "HEAD_SHA and REPO are required" >&2
  exit 1
fi

tmpdir=$(mktemp -d)
page=1
while true; do
  resp=$(gh api "repos/${REPO}/commits/${HEAD_SHA}/check-runs?per_page=100&page=${page}")
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
  latest=$(jq -r --arg g "${gate}" '
    [.[] | select(.name == $g)]
    | sort_by(.started_at) | last | .conclusion // "missing"
  ' <<<"${check_runs}")
  if [[ "${latest}" != "success" ]]; then
    echo "Skipping ${gate}: latest conclusion is ${latest}"
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
  done < <(jq -r --arg g "${gate}" '
    [.[] | select(.name == $g and .status == "in_progress") | .id] | .[]
  ' <<<"${check_runs}")
done

exit "${failed}"
