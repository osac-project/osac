#!/usr/bin/env bash
# Manual cleanup for orphaned in_progress e2e-*-gate API checks on HEAD_SHA.
# MODE=complete: mark orphans success when native gate job already succeeded.
# MODE=dismiss: mark unlock-time orphans cancelled (mislabeled /runs/<id> checks).

set -euo pipefail

readonly GATES=(e2e-vmaas-gate e2e-bmaas-gate e2e-caas-gate)
readonly INVALIDATE_EXTERNAL_ID_PREFIX="osac-invalidate-e2e-gate"
MODE="${MODE:-complete}"

if [[ -z "${HEAD_SHA:-}" || -z "${REPO:-}" ]]; then
  echo "HEAD_SHA and REPO are required" >&2
  exit 1
fi
if [[ "${MODE}" != "complete" && "${MODE}" != "dismiss" ]]; then
  echo "MODE must be complete or dismiss" >&2
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
failed=0

native_gate_job_success() {
  local gate="$1"
  jq -e --arg g "${gate}" '
    [.[] | select(
      .name == $g
      and ((.details_url // "") | test("/actions/runs/[0-9]+/job/"))
    )]
    | sort_by(.created_at)
    | last
    | .status == "completed" and .conclusion == "success"
  ' <<<"${check_runs}" >/dev/null
}

orphan_ids_for_gate() {
  local gate="$1"
  jq -r --arg g "${gate}" --arg prefix "${INVALIDATE_EXTERNAL_ID_PREFIX}" '
    [.[] | select(
      .name == $g
      and .status == "in_progress"
      and (
        ((.external_id // "") | startswith($prefix))
        or ((.details_url // "") | test("^https://github.com/[^/]+/[^/]+/actions/runs/[0-9]+$"))
        or ((.details_url // "") | test("^https://github.com/[^/]+/[^/]+/runs/[0-9]+$"))
      )
    ) | .id] | .[]
  ' <<<"${check_runs}"
}

for gate in "${GATES[@]}"; do
  if [[ "${MODE}" == "complete" ]]; then
    if ! native_gate_job_success "${gate}"; then
      echo "Skipping ${gate}: no native gate job success on ${HEAD_SHA:0:7}"
      continue
    fi
    title="Superseded by native e2e gate job"
    summary="Manual cleanup; merge-required gate already success on this SHA."
    conclusion="success"
  else
    title="Superseded by full-install gate job"
    summary="Manual dismiss of unlock orphan API check on this SHA."
    conclusion="cancelled"
  fi

  while IFS= read -r id; do
    [[ -z "${id}" || "${id}" == "null" ]] && continue
    if [[ "${MODE}" == "complete" ]] && ! native_gate_job_success "${gate}"; then
      echo "Skipping stale ${gate} check ${id}: native gate no longer success"
      continue
    fi
    payload=$(jq -n \
      --arg status "completed" \
      --arg conclusion "${conclusion}" \
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
      echo "${MODE} stale ${gate} check ${id} on ${HEAD_SHA:0:7}"
    else
      echo "Could not patch ${gate} check ${id}" >&2
      failed=1
    fi
  done < <(orphan_ids_for_gate "${gate}")
done

exit "${failed}"
