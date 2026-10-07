#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?GH_TOKEN is required}"
: "${REPO:?REPO is required}"
: "${PR_NUMBER:?PR_NUMBER is required}"
: "${HEAD_SHA:?HEAD_SHA is required}"
: "${GATE_NAME:?GATE_NAME is required}"
: "${GATE_RESULT:?GATE_RESULT is required}"
: "${WORKFLOW_FILE:?WORKFLOW_FILE is required}"
: "${RUN_URL:?RUN_URL is required}"

if [[ ! "${PR_NUMBER}" =~ ^[0-9]+$ || ! "${HEAD_SHA}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "PR_NUMBER must be numeric and HEAD_SHA must be a 40-character SHA." >&2
  exit 1
fi

pr_json=$(gh api "repos/${REPO}/pulls/${PR_NUMBER}")
if [[ "$(jq -r '.state' <<<"${pr_json}")" != "open" || \
      "$(jq -r '.base.ref // empty' <<<"${pr_json}")" != "main" || \
      "$(jq -r '.head.sha // empty' <<<"${pr_json}")" != "${HEAD_SHA}" ]]; then
  echo "PR #${PR_NUMBER} is closed, retargeted, or has moved; do not report a stale recovery gate."
  exit 0
fi

# A delayed native pull_request run may have appeared while the recovery run
# was building. It owns the canonical gate check for this SHA, so avoid posting
# a duplicate with the same name.
native_runs=$(gh api \
  "repos/${REPO}/actions/workflows/${WORKFLOW_FILE}/runs?event=pull_request&head_sha=${HEAD_SHA}&per_page=100" \
  --jq '.workflow_runs | length')
if [[ "${native_runs}" != "0" ]]; then
  echo "A native pull_request run now exists for ${HEAD_SHA}; its gate owns this SHA."
  exit 0
fi

case "${GATE_RESULT}" in
  success) conclusion=success ;;
  cancelled) conclusion=cancelled ;;
  *) conclusion=failure ;;
esac

payload=$(jq -n \
  --arg name "${GATE_NAME}" \
  --arg sha "${HEAD_SHA}" \
  --arg conclusion "${conclusion}" \
  --arg started "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg completed "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg details "${RUN_URL}" \
  --arg title "Full-install E2E ${conclusion}" \
  --arg summary "Recovery run for PR #${PR_NUMBER} at ${HEAD_SHA}. See the linked workflow run for logs." \
  '{
    name: $name,
    head_sha: $sha,
    status: "completed",
    conclusion: $conclusion,
    started_at: $started,
    completed_at: $completed,
    details_url: $details,
    output: {title: $title, summary: $summary}
  }')

gh api --method POST "repos/${REPO}/check-runs" --input - <<<"${payload}"
