#!/usr/bin/env bash
# OSAC-5507: if a pull request has lgtm, dismiss every Request changes review
# so the merge queue can run its tests and merge.
#
# sync     — one PR. Env: REPO, PR, AUTHOR, ACTION
# maintain — open PRs. Env: REPO

set -euo pipefail

if [[ -z "${GH_TOKEN:-}" ]]; then
  echo "MERGE_QUEUE_TOKEN is not set"
  exit 0
fi
if [[ -z "${REPO:-}" ]]; then
  echo "REPO is required" >&2
  exit 1
fi

# Dismiss every Request changes review. lgtm is the approval signal.
dismiss_changes_requested() {
  local pr="$1" id
  while read -r id; do
    [[ -z "${id}" ]] && continue
    echo "Dismissing review ${id} on PR #${pr} because lgtm is present"
    gh api "repos/${REPO}/pulls/${pr}/reviews/${id}/dismissals" \
      -X PUT -f message="Auto-dismissed because lgtm is present"
  done < <(gh api --paginate --slurp "repos/${REPO}/pulls/${pr}/reviews" \
    | jq -r '[.[][]] | .[] | select(.state == "CHANGES_REQUESTED") | .id')
}

labels_ok() {
  jq -r '
    . as $labels
    | (["lgtm", "approved", "jira/valid-reference"]
        | all(. as $n | $labels | index($n) != null))
      and (["do-not-merge/hold", "do-not-merge/work-in-progress",
            "do-not-merge/invalid-owners-file", "needs-rebase"]
        | all(. as $n | $labels | index($n) == null))
  ' <<<"$1"
}

enqueue() {
  local pr="$1" id="$2" head="$3"
  echo "Checking whether PR #${pr} is already queued"
  if [[ "$(gh api graphql -f query='
    query($id: ID!) {
      node(id: $id) { ... on PullRequest { mergeQueueEntry { id } } }
    }' -f id="$id" --jq '.data.node.mergeQueueEntry != null')" == "true" ]]; then
    echo "PR #${pr} is already queued"
    return 0
  fi
  echo "Enqueuing PR #${pr} at ${head}"
  gh api graphql -f query='
    mutation($id: ID!, $head: GitObjectID!) {
      enqueuePullRequest(input: {pullRequestId: $id, expectedHeadOid: $head}) {
        mergeQueueEntry { position state }
      }
    }' -f id="$id" -f head="$head"
}

# Read the PR, dismiss Request changes when lgtm is present, then enable
# auto-merge so required tests can finish and the queue can merge.
process_pr() {
  set -euo pipefail
  local pr="$1" json labels head id
  json=$(gh pr view "$pr" --repo "$REPO" --json \
    id,headRefOid,isDraft,mergeable,mergeStateStatus,autoMergeRequest,labels)
  labels=$(jq -c '[.labels[].name]' <<<"${json}")
  echo "PR #${pr} labels: ${labels}"

  if [[ "$(jq -r 'index("lgtm") != null' <<<"${labels}")" == "true" ]]; then
    dismiss_changes_requested "$pr"
  fi

  if [[ "$(jq -r '.isDraft' <<<"${json}")" == "true" ]]; then
    echo "PR #${pr} is a draft"
    return 0
  fi
  if [[ "$(labels_ok "${labels}")" != "true" ]]; then
    echo "PR #${pr} is not eligible for the queue"
    if [[ "$(jq -r '.autoMergeRequest != null' <<<"${json}")" == "true" ]]; then
      gh pr merge "$pr" --repo "$REPO" --disable-auto || echo "Could not disable auto-merge on PR #${pr}"
    fi
    return 0
  fi

  head=$(jq -r '.headRefOid' <<<"${json}")
  if [[ "$(jq -r '.autoMergeRequest == null' <<<"${json}")" == "true" ]]; then
    echo "Enabling auto-merge on PR #${pr} at ${head}"
    gh pr merge "$pr" --repo "$REPO" --auto --match-head-commit "$head" || echo "Could not enable auto-merge on PR #${pr}"
    return 0
  fi
  if [[ "$(jq -r '.mergeable == "MERGEABLE" and .mergeStateStatus == "CLEAN"' <<<"${json}")" == "true" ]]; then
    id=$(jq -r '.id' <<<"${json}")
    enqueue "$pr" "$id" "$head" || echo "Could not enqueue PR #${pr}"
  fi
}

sync_one() {
  local labels
  if [[ "${ACTION:-}" == "synchronize" ]]; then
    labels=$(gh pr view "$PR" --repo "$REPO" --json labels --jq '[.labels[].name]')
    if [[ "$(jq -r 'index("lgtm") != null' <<<"${labels}")" == "true" ]]; then
      echo "Removing lgtm from PR #${PR} after a new push"
      gh pr edit "$PR" --repo "$REPO" --remove-label lgtm
    fi
  fi
  echo "Checking collaborator ${AUTHOR}"
  if ! gh api "repos/${REPO}/collaborators/${AUTHOR}"; then
    echo "Author ${AUTHOR} is not a repo collaborator; leaving PR #${PR} alone"
    return 0
  fi
  process_pr "$PR"
}

maintain() {
  local row pr
  echo "Maintaining open pull requests"
  while read -r row; do
    [[ -z "${row}" ]] && continue
    if [[ "$(jq -r '.isDraft' <<<"${row}")" == "true" ]]; then
      continue
    fi
    pr=$(jq -r '.number' <<<"${row}")
    set +e
    (process_pr "$pr")
    rc=$?
    set -e
    if [[ "${rc}" -ne 0 ]]; then
      echo "PR #${pr} failed (rc=${rc}), will retry next run"
    fi
  done < <(gh pr list --repo "$REPO" --state open --limit 200 --json number,isDraft | jq -c '.[]')
  echo "Scan complete"
}

case "${1:-}" in
  sync)
    if [[ -z "${PR:-}" || -z "${AUTHOR:-}" ]]; then
      echo "PR and AUTHOR are required" >&2
      exit 1
    fi
    sync_one
    ;;
  maintain)
    maintain
    ;;
  *)
    echo "usage: auto-queue.sh sync|maintain" >&2
    exit 2
    ;;
esac
