#!/usr/bin/env bash
# Manual cleanup for orphaned e2e-*-gate API checks on HEAD_SHA -- either
# still in_progress, or already (mis-)finalized by an earlier run of this
# same script.
#
# MODE=complete: mirror the orphan onto whatever the native gate job's real,
#   terminal conclusion actually is (success or skipped). Never touches a
#   native failure: leaving the orphan as-is or matching a real failure both
#   correctly keep the PR blocked, and this script should never be what
#   turns a genuinely broken PR green.
# MODE=dismiss: cancel unlock-time orphans, but ONLY when the native gate
#   job has not posted a terminal result yet. Cancelling an orphan whose
#   native gate already completed can make that cancellation the *latest*
#   check-run for the name (required-status-check evaluation uses the most
#   recent entry per name), silently re-blocking a PR that was actually
#   fine -- confirmed this happening in practice: an earlier run of this
#   script in dismiss mode did exactly that after the real gate had already
#   succeeded hours earlier. Use MODE=complete instead once a native result
#   exists.
#
# Re-fetches check-runs immediately before each per-orphan PATCH decision
# (not just once at the top) so a gate that transitions state while this
# script is mid-run (e.g. a still-in-flight e2e suite finishes between the
# first gate's processing and the last) is judged on current data. This
# narrows, but by construction of the GitHub REST API cannot fully close,
# the gap between reading a check-run's state and PATCHing it -- a run
# could still change state in between those two calls for a given orphan.

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

failed=0

# Fetches every check-run on HEAD_SHA, fresh, paginating as needed.
fetch_check_runs() {
  local tmpdir page resp count
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
  jq -s 'add' "${tmpdir}"/page-*.json
  rm -rf "${tmpdir}"
}

# Prints the native gate job's real conclusion, or "pending" (not completed
# yet) / "missing" (no native job found at all), from the given check-runs
# snapshot.
native_gate_conclusion() {
  local gate="$1" runs="$2"
  jq -r --arg g "${gate}" '
    ([.[] | select(
      .name == $g
      and ((.details_url // "") | test("/actions/runs/[0-9]+/job/"))
    )]
    | sort_by(.started_at)
    | last) as $last
    | if $last == null then "missing"
      elif $last.status != "completed" then "pending"
      else $last.conclusion
      end
  ' <<<"${runs}"
}

# In_progress orphans, or already-(mis-)finalized ones from an earlier run of
# this script -- identified structurally (external_id prefix, or an
# API-created details_url with no /job/ segment), not by current status.
orphan_ids_for_gate() {
  local gate="$1" runs="$2"
  jq -r --arg g "${gate}" --arg prefix "${INVALIDATE_EXTERNAL_ID_PREFIX}" '
    [.[] | select(
      .name == $g
      and (
        ((.external_id // "") | startswith($prefix))
        or ((.details_url // "") | test("^https://github.com/[^/]+/[^/]+/actions/runs/[0-9]+$"))
        or ((.details_url // "") | test("^https://github.com/[^/]+/[^/]+/runs/[0-9]+$"))
      )
    ) | .id] | .[]
  ' <<<"${runs}"
}

for gate in "${GATES[@]}"; do
  check_runs="$(fetch_check_runs)"
  native="$(native_gate_conclusion "${gate}" "${check_runs}")"

  if [[ "${MODE}" == "complete" ]]; then
    if [[ "${native}" != "success" && "${native}" != "skipped" ]]; then
      echo "Skipping ${gate}: native gate is '${native}' (not success/skipped) on ${HEAD_SHA:0:7}"
      continue
    fi
    title="Superseded by native e2e gate job"
  else
    if [[ "${native}" != "pending" && "${native}" != "missing" ]]; then
      echo "Skipping ${gate} dismiss: native gate already '${native}' on ${HEAD_SHA:0:7} -- rerun with MODE=complete instead"
      continue
    fi
    title="Superseded by full-install gate job"
  fi

  while IFS= read -r id; do
    [[ -z "${id}" || "${id}" == "null" ]] && continue

    # Re-fetch immediately before deciding this specific orphan's fate: a
    # gate can transition (e.g. from pending to success) while this script
    # works through the others, and each orphan should be judged on the
    # freshest data available right before its own PATCH, not the snapshot
    # from when the gate-level check above ran.
    check_runs="$(fetch_check_runs)"
    current="$(native_gate_conclusion "${gate}" "${check_runs}")"
    if [[ "${MODE}" == "complete" ]]; then
      if [[ "${current}" != "success" && "${current}" != "skipped" ]]; then
        echo "Skipping stale ${gate} check ${id}: native gate now '${current}'"
        continue
      fi
      conclusion="${current}"
      summary="Manual cleanup; merge-required gate is ${current} on this SHA."
    else
      if [[ "${current}" != "pending" && "${current}" != "missing" ]]; then
        echo "Skipping ${gate} dismiss of check ${id}: native gate now '${current}' -- rerun with MODE=complete instead"
        continue
      fi
      conclusion="cancelled"
      summary="Manual dismiss of unlock orphan API check on this SHA."
    fi

    completed_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
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
      echo "${MODE} stale ${gate} check ${id} on ${HEAD_SHA:0:7} -> ${conclusion}"
    else
      echo "Could not patch ${gate} check ${id}" >&2
      failed=1
    fi
  done < <(orphan_ids_for_gate "${gate}" "${check_runs}")
done

exit "${failed}"
