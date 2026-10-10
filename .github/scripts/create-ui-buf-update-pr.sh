#!/usr/bin/env bash
# Commit a tested UI API baseline update, open one PR for the release, and
# request deletion of its head branch after merge. The repository's auto-queue
# workflow remains responsible for the actual merge.
set -Eeuo pipefail

readonly REPO_NAME="${REPO:-}"
readonly BASE_BRANCH_NAME="${BASE_BRANCH:-main}"
readonly CURRENT_VERSION_VALUE="${CURRENT_VERSION:-}"
readonly VERSION_VALUE="${VERSION:-}"
readonly PUSH_TOKEN_VALUE="${PUSH_TOKEN:-}"
readonly STABLE_SEMVER_RE='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'

fail() {
  echo "::error::$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "Required command not found: $1"
}

gh_capture() {
  local output
  if ! output="$(gh "$@" 2>&1)"; then
    printf '%s\n' "$output" >&2
    fail "GitHub CLI command failed: gh $*"
  fi
  printf '%s' "$output"
}

enable_ui_auto_merge() {
  local pr="$1" head
  head="$(gh_capture pr view "$pr" --repo "$REPO_NAME" --json headRefOid --jq '.headRefOid')"
  [[ "$head" =~ ^[0-9a-f]{40}$ ]] || fail "GitHub returned an invalid PR head SHA"
  gh_capture pr merge "$pr" --repo "$REPO_NAME" --auto --delete-branch \
    --match-head-commit "$head" >/dev/null
}

configure_push_credentials() {
  # The app token is limited to pushing the tested branch. Keep the separate
  # merge-queue token as GH_TOKEN for PR API operations below.
  if ! GH_TOKEN="$PUSH_TOKEN_VALUE" gh auth setup-git >/dev/null 2>&1; then
    fail "Could not configure Git credentials for the UI update branch"
  fi
}

commit_tested_ui_update() {
  git switch --create "$BRANCH"
  git config user.name "osac-ci[bot]"
  git config user.email "osac-ci[bot]@users.noreply.github.com"

  # Stage only the generated UI baseline and bindings. In particular, do not
  # include unrelated workspace files that may be present on the runner.
  git add -- \
    osac-ui/libs/types/.buf-api-version \
    osac-ui/libs/types/src
  if git diff --cached --quiet; then
    fail "No UI Buf changes to publish"
  fi

  git commit -m "$PR_TITLE"
}

[[ "$REPO_NAME" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || fail "Invalid repository: ${REPO_NAME}"
[[ "$BASE_BRANCH_NAME" =~ ^[A-Za-z0-9._/-]+$ ]] || fail "Invalid base branch: ${BASE_BRANCH_NAME}"
[[ "$CURRENT_VERSION_VALUE" =~ $STABLE_SEMVER_RE ]] || fail "Invalid current UI Buf version: ${CURRENT_VERSION_VALUE}"
[[ "$VERSION_VALUE" =~ $STABLE_SEMVER_RE ]] || fail "Invalid new UI Buf version: ${VERSION_VALUE}"
[[ -n "${GH_TOKEN:-}" ]] || fail "MERGE_QUEUE_TOKEN is required"
[[ -n "$PUSH_TOKEN_VALUE" ]] || fail "The UI push token is required"

require_command gh
require_command git

readonly REPO_OWNER="${REPO_NAME%%/*}"
readonly BRANCH="automation/ui-buf-${VERSION_VALUE}"
readonly PR_TITLE="NO-ISSUE: update UI API baseline to ${VERSION_VALUE}"

existing_pr="$(gh_capture pr list \
  --repo "$REPO_NAME" \
  --state open \
  --head "${REPO_OWNER}:${BRANCH}" \
  --json number \
  --jq '.[0].number // empty')"
if [[ -n "$existing_pr" ]]; then
  existing_base="$(gh_capture pr view "$existing_pr" --repo "$REPO_NAME" --json baseRefName --jq '.baseRefName')"
  [[ "$existing_base" == "$BASE_BRANCH_NAME" ]] || fail "Existing PR #${existing_pr} targets ${existing_base}, expected ${BASE_BRANCH_NAME}"

  echo "PR #${existing_pr} already exists for ${VERSION_VALUE}; updating its head to the tested checkout."
  configure_push_credentials
  commit_tested_ui_update
  git fetch origin "refs/heads/${BRANCH}:refs/remotes/origin/${BRANCH}"
  git push --force-with-lease origin "$BRANCH"

  echo "Refreshing labels for PR #${existing_pr}."
  gh_capture pr edit "$existing_pr" --repo "$REPO_NAME" \
    --add-label lgtm --add-label approved >/dev/null
  enable_ui_auto_merge "$existing_pr"
  echo "Auto-queue will handle merge eligibility for PR #${existing_pr}; its head branch is marked for deletion after merge."
  exit 0
fi

if git ls-remote --exit-code --heads origin "$BRANCH" >/dev/null 2>&1; then
  fail "Remote branch ${BRANCH} exists without an open PR; refusing to overwrite it"
fi

commit_tested_ui_update
configure_push_credentials
git push --set-upstream origin "$BRANCH"

body_file="$(mktemp)"
trap 'rm -f "$body_file"' EXIT
{
  printf '%s\n' 'This automated PR advances the UI protobuf API baseline after the nightly compatibility check passed.'
  printf '\n%s\n' 'Changes:'
  printf '%s\n' "- Previous shared BSR release: ${CURRENT_VERSION_VALUE}"
  printf '%s\n' "- New shared BSR release: ${VERSION_VALUE}"
  printf '%s\n' '- Regenerated TypeScript bindings.'
  printf '\n%s\n' "Nightly run: ${RUN_URL:-unavailable}"
} > "$body_file"

pr_url="$(gh_capture pr create \
  --repo "$REPO_NAME" \
  --base "$BASE_BRANCH_NAME" \
  --head "$BRANCH" \
  --title "$PR_TITLE" \
  --body-file "$body_file" \
  --label lgtm \
  --label approved)"
enable_ui_auto_merge "$pr_url"
echo "Created ${pr_url}; auto-queue will handle merge eligibility and its head branch is marked for deletion after merge."
