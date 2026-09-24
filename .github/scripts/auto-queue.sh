#!/usr/bin/env bash
# Decide whether one pull request may enter the merge queue.
#
# OSAC-5507: a human "Request changes" review submitted at or before the
# current lgtm label is dismissed, so GitHub's pull-request rule allows the
# queue. A newer human review still blocks. Bot "Request changes" reviews
# are dismissed and do not block.
#
# sync     — one PR. Env: REPO, PR, AUTHOR, ACTION
# maintain — open PRs. Env: REPO
#
# reviews_allow_queue returns 0 (clear), 1 (blocking review), or 2 (unknown).

set -euo pipefail

BLOCKING_LABELS=(
  do-not-merge/hold
  do-not-merge/work-in-progress
  do-not-merge/invalid-owners-file
  needs-rebase
)
REQUIRED_LABELS=(lgtm approved jira/valid-reference)

require_env() {
  if [[ -z "${REPO:-}" || -z "${GH_TOKEN:-}" ]]; then
    echo "REPO and GH_TOKEN are required" >&2
    exit 1
  fi
}

has_label() {
  jq -e --arg name "$1" 'index($name) != null' >/dev/null <<<"$2"
}

view_pr() {
  gh pr view "$1" --repo "$REPO" --json \
    id,headRefOid,isDraft,mergeable,mergeStateStatus,autoMergeRequest,labels
}

labels_of() {
  jq -c '[.labels[].name]' <<<"$1"
}

load_reviews() {
  gh api --paginate --slurp "repos/${REPO}/pulls/${1}/reviews" | jq 'add // []'
}

lgtm_label_time() {
  gh api --paginate --slurp "repos/${REPO}/issues/${1}/events" \
    | jq -r '[.[][] | select(.event == "labeled" and (.label.name // "") == "lgtm") | .created_at] | max // empty'
}

dismiss_bots() {
  local pr="$1" reviews="$2" id
  while read -r id; do
    [[ -z "${id}" ]] && continue
    echo "Dismissing bot review ${id} on PR #${pr}"
    gh api "repos/${REPO}/pulls/${pr}/reviews/${id}/dismissals" \
      -X PUT -f message="Auto-dismissed: bot Request changes do not block merge" \
      || echo "Failed to dismiss bot review ${id} on PR #${pr}"
  done < <(jq -r '
    .[]
    | select(.state == "CHANGES_REQUESTED")
    | select(.user != null)
    | select(((.user.login // "") | endswith("[bot]")) or ((.user.type // "") == "Bot"))
    | .id
  ' <<<"${reviews}")
}

# Latest human CHANGES_REQUESTED reviews, classified against the lgtm time.
# An empty lgtm time marks every such review blocking (no label to dismiss against).
classify_human_reviews() {
  jq -c --arg lgtm "$2" '
    [.[]
      | select(.user != null)
      | select(((.user.login // "") | endswith("[bot]")) | not)
      | select((.user.type // "User") != "Bot")
      | select(.state == "APPROVED" or .state == "CHANGES_REQUESTED" or .state == "DISMISSED")
    ]
    | group_by(.user.login)
    | map(max_by([(.submitted_at // ""), (.id // 0)]))
    | map(select(.state == "CHANGES_REQUESTED"))
    | map({
        id,
        submitted_at: (.submitted_at // ""),
        disposition: (
          if $lgtm == "" or (.submitted_at // "") == "" then "blocking"
          elif .submitted_at <= $lgtm then "stale"
          else "newer"
          end
        )
      })
  ' <<<"$1"
}

# 0 clear, 1 a human review still blocks, 2 lookup or dismiss failed.
reviews_allow_queue() {
  local pr="$1" labels="$2" reviews lgtm_at classified row id submitted disposition
  local blocking=false
  if ! reviews=$(load_reviews "$pr"); then
    echo "Unable to read reviews for PR #${pr}"
    return 2
  fi
  dismiss_bots "$pr" "$reviews"
  if has_label lgtm "$labels"; then
    if ! lgtm_at=$(lgtm_label_time "$pr"); then
      echo "Unable to read lgtm label time for PR #${pr}"
      return 2
    fi
    if [[ -z "${lgtm_at}" ]]; then
      echo "PR #${pr} has lgtm but no labeled event"
      return 2
    fi
  else
    lgtm_at=""
  fi
  if ! classified=$(classify_human_reviews "$reviews" "$lgtm_at"); then
    echo "Cannot classify human reviews for PR #${pr}"
    return 2
  fi
  while read -r row; do
    [[ -z "${row}" ]] && continue
    id=$(jq -r '.id' <<<"${row}")
    submitted=$(jq -r '.submitted_at' <<<"${row}")
    disposition=$(jq -r '.disposition' <<<"${row}")
    if [[ "${disposition}" == "stale" ]]; then
      echo "Dismissing stale human review ${id} on PR #${pr} (submitted ${submitted}, lgtm ${lgtm_at})"
      if ! gh api "repos/${REPO}/pulls/${pr}/reviews/${id}/dismissals" \
        -X PUT -f message="Auto-dismissed: lgtm label is newer than this Request changes review"; then
        echo "Failed to dismiss review ${id} on PR #${pr}"
        return 2
      fi
    else
      echo "PR #${pr}: human CHANGES_REQUESTED review ${id} still blocks"
      blocking=true
    fi
  done < <(jq -c '.[]' <<<"${classified}")
  if [[ "${blocking}" == true ]]; then
    return 1
  fi
  return 0
}

labels_allow_queue() {
  local labels="$1" name
  for name in "${BLOCKING_LABELS[@]}"; do
    if has_label "$name" "$labels"; then
      echo "Blocking label on PR: ${name}"
      return 1
    fi
  done
  for name in "${REQUIRED_LABELS[@]}"; do
    if ! has_label "$name" "$labels"; then
      echo "Missing required merge label: ${name}"
      return 1
    fi
  done
  return 0
}

disable_auto() {
  local pr="$1" err
  if err=$(gh pr merge "$pr" --repo "$REPO" --disable-auto 2>&1); then
    echo "Disabled auto-merge on PR #${pr}"
    return 0
  fi
  if [[ "${err}" == *"Can't disable auto-merge"* ]]; then
    return 0
  fi
  echo "${err}" >&2
  return 1
}

enable_auto() {
  local pr="$1" head="$2"
  echo "Enabling auto-merge on PR #${pr} at ${head}"
  gh pr merge "$pr" --repo "$REPO" --auto --match-head-commit "$head"
}

enqueue_if_needed() {
  local pr="$1" id="$2" head="$3" queued
  if ! queued=$(gh api graphql -f query='
    query($id: ID!) {
      node(id: $id) {
        ... on PullRequest { mergeQueueEntry { id } }
      }
    }' -f id="$id" --jq '.data.node.mergeQueueEntry != null' 2>&1); then
    echo "Failed to check merge queue for PR #${pr}: ${queued}"
    return 0
  fi
  if [[ "${queued}" == "true" ]]; then
    return 0
  fi
  echo "Enqueuing PR #${pr} at ${head}"
  gh api graphql -f query='
    mutation($id: ID!, $head: GitObjectID!) {
      enqueuePullRequest(input: {pullRequestId: $id, expectedHeadOid: $head}) {
        mergeQueueEntry { position state }
      }
    }' -f id="$id" -f head="$head" \
    || echo "Failed to enqueue PR #${pr}, will retry next run"
}

# Apply the queue decision for one already-loaded PR object.
# review_rc is 0 or 1. Unknown (2) is handled by the caller.
apply_decision() {
  local pr="$1" current="$2" review_rc="$3" labels head id
  labels=$(labels_of "$current")
  head=$(jq -r '.headRefOid' <<<"${current}")
  if [[ "${review_rc}" -eq 0 ]] && labels_allow_queue "$labels" \
    && [[ "$(jq -r '.isDraft' <<<"${current}")" == "false" ]]; then
    if jq -e '.autoMergeRequest == null' <<<"${current}" >/dev/null; then
      enable_auto "$pr" "$head"
      return
    fi
    if jq -e '.mergeable == "MERGEABLE" and .mergeStateStatus == "CLEAN"' <<<"${current}" >/dev/null; then
      id=$(jq -r '.id' <<<"${current}")
      enqueue_if_needed "$pr" "$id" "$head"
    fi
    return
  fi
  if jq -e '.autoMergeRequest != null' <<<"${current}" >/dev/null; then
    echo "PR #${pr} is not eligible; disabling auto-merge"
    disable_auto "$pr"
  fi
}

# 0 decided, 1 enable/disable failed, 2 reviews or the PR could not be read.
# Does not disable on 2; callers choose fail-closed or skip.
decide() {
  local pr="$1" current labels review_rc
  if ! current=$(view_pr "$pr"); then
    echo "Unable to read PR #${pr}"
    return 2
  fi
  labels=$(labels_of "$current")
  if reviews_allow_queue "$pr" "$labels"; then
    review_rc=0
  else
    review_rc=$?
  fi
  if [[ "${review_rc}" -eq 2 ]]; then
    return 2
  fi
  if ! current=$(view_pr "$pr"); then
    echo "Unable to reread PR #${pr}"
    return 2
  fi
  apply_decision "$pr" "$current" "$review_rc"
}

sync_one() {
  local pr="$1" author="$2" action="$3" rc
  if [[ "${action}" == "synchronize" ]]; then
    if gh pr view "$pr" --repo "$REPO" --json labels --jq '[.labels[].name]' \
      | jq -e 'index("lgtm")' >/dev/null; then
      echo "Removing lgtm label from PR #${pr} after new push"
      gh pr edit "$pr" --repo "$REPO" --remove-label lgtm
    fi
  fi
  if ! gh api "repos/${REPO}/collaborators/${author}" >/dev/null 2>&1; then
    echo "Author ${author} is not a repo collaborator; leaving PR #${pr} alone"
    return 0
  fi
  if decide "$pr"; then
    return 0
  else
    rc=$?
  fi
  if [[ "${rc}" -eq 2 ]]; then
    echo "Unable to evaluate PR #${pr}; disabling auto-merge"
    disable_auto "$pr" || true
  fi
  return 1
}

maintain() {
  local list row pr rc
  echo "Maintaining open pull requests..."
  list=$(gh pr list --repo "$REPO" --state open --limit 200 --json number,isDraft)
  while read -r row; do
    [[ -z "${row}" ]] && continue
    if jq -e '.isDraft' <<<"${row}" >/dev/null; then
      continue
    fi
    pr=$(jq -r '.number' <<<"${row}")
    if decide "$pr"; then
      continue
    else
      rc=$?
    fi
    echo "PR #${pr} maintenance skipped (rc=${rc}), will retry next run"
  done < <(jq -c '.[]' <<<"${list}")
  echo "Scan complete."
}

main() {
  require_env
  case "${1:-}" in
    sync)
      if [[ -z "${PR:-}" || -z "${AUTHOR:-}" ]]; then
        echo "PR and AUTHOR are required" >&2
        exit 1
      fi
      sync_one "$PR" "$AUTHOR" "${ACTION:-}"
      ;;
    maintain)
      maintain
      ;;
    *)
      echo "usage: auto-queue.sh sync|maintain" >&2
      exit 2
      ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
