#!/usr/bin/env bash
# Cancel in-progress full-install runs on obsolete SHAs for this PR branch.
# gh run rerun (unlock replay) can outlive concurrency cancel-in-progress on
# newer pull_request runs; synchronize must force-cancel stale SHAs explicitly.
# Cancels all three suite workflows (VMaaS/BMaaS/CaaS) regardless of which
# caller invokes this script.
#
# CANCEL_ALL=true (PR closed/merged): cancel every in-flight run for this PR
# branch, including ones matching HEAD_SHA, and skip the "did the PR head
# move on" guard -- there's no newer push to defer to once the PR is closed,
# and nothing further needs this SHA's e2e result.
#
# Env: REPO, HEAD_SHA, HEAD_BRANCH, HEAD_REPO, PR_NUMBER, CANCEL_ALL

set -euo pipefail

readonly CANCEL_ALL="${CANCEL_ALL:-false}"

if [[ -z "${REPO:-}" || -z "${HEAD_SHA:-}" || -z "${HEAD_BRANCH:-}" || -z "${HEAD_REPO:-}" ]]; then
  echo "REPO, HEAD_SHA, HEAD_BRANCH, and HEAD_REPO are required" >&2
  exit 1
fi

# CANCEL_ALL drops the "differs from HEAD_SHA" scoping below, so it leans on
# repo+branch alone to identify "this PR's runs" -- not enough on its own if a
# branch name is ever reused across PRs (delete-and-recreate, or a stale run
# from a previous close/reopen cycle). Require an exact PR_NUMBER match too.
if [[ "${CANCEL_ALL}" == "true" && -z "${PR_NUMBER:-}" ]]; then
  echo "PR_NUMBER is required when CANCEL_ALL=true" >&2
  exit 1
fi

# Return 0 when PR head still matches HEAD_SHA; 1 when moved or lookup failed.
# Always 0 in CANCEL_ALL mode: a closed PR's head won't move, and there's no
# newer push for this cleanup to defer to.
pr_head_still_current() {
  local current_head

  if [[ "${CANCEL_ALL}" == "true" || -z "${PR_NUMBER:-}" ]]; then
    return 0
  fi
  current_head=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}" --jq '.head.sha // empty')
  if [[ -z "${current_head}" ]]; then
    echo "Could not read PR #${PR_NUMBER} head; skipping stale-run cancel." >&2
    return 1
  fi
  if [[ "${current_head}" != "${HEAD_SHA}" ]]; then
    echo "PR #${PR_NUMBER} head moved (${HEAD_SHA:0:7} -> ${current_head:0:7}); stopping stale-run cancel."
    return 1
  fi
  return 0
}

if ! pr_head_still_current; then
  exit 0
fi

E2E_NAMES='["E2E VMaaS Full Install","E2E BMaaS Full Install","E2E CaaS Full Install"]'
cancelled=0
failed=0
for status in in_progress queued waiting pending requested; do
  if ! pr_head_still_current; then
    break
  fi
  runs=$(gh api --method GET "repos/${REPO}/actions/runs" \
    -f event=pull_request \
    -f branch="${HEAD_BRANCH}" \
    -f status="${status}" \
    -F per_page=100 \
    --jq '.workflow_runs')
  while IFS=$'\t' read -r id name sha; do
    [[ -z "${id}" ]] && continue
    if ! pr_head_still_current; then
      break 2
    fi
    if [[ "${CANCEL_ALL}" != "true" && "${sha}" == "${HEAD_SHA}" ]]; then
      echo "Skipping ${name} #${id}: matches current PR head"
      continue
    fi
    echo "Cancelling ${name} #${id} (${sha:0:7})"
    if gh api -X POST "repos/${REPO}/actions/runs/${id}/force-cancel" 2>/dev/null \
      || gh run cancel "${id}" -R "${REPO}"; then
      cancelled=$((cancelled + 1))
    else
      echo "Could not cancel run #${id}" >&2
      failed=1
    fi
  done < <(jq -r --arg head "${HEAD_SHA}" --arg repo "${HEAD_REPO}" --argjson names "${E2E_NAMES}" \
    --argjson all "$([[ "${CANCEL_ALL}" == "true" ]] && echo true || echo false)" \
    --argjson pr "${PR_NUMBER:-0}" '
    .[] | select(
      .head_repository.full_name == $repo
      and ($all or .head_sha != $head)
      and (if $all then ((.pull_requests // []) | any(.number == $pr)) else true end)
      and (.name as $n | $names | index($n))
    ) | "\(.id)\t\(.name)\t\(.head_sha)"
  ' <<<"${runs}")
done
echo "Cancelled ${cancelled} run(s)."
exit "${failed}"
