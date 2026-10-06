#!/usr/bin/env bash

set -euo pipefail

command -v jq >/dev/null 2>&1 || exit 0

input=$(cat)
cwd=$(jq -r '.cwd // empty' <<<"$input")
tool_command=$(jq -r '.tool_input.command // empty' <<<"$input")
[[ -n "$cwd" && -n "$tool_command" ]] || exit 0

repo_root=$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null) || exit 0

# Check whether a repository-relative path is one of the shared proto sources.
is_proto_source() {
  case "$1" in
    proto/private/*.proto|proto/tests/*.proto) return 0 ;;
    *) return 1 ;;
  esac
}

# Block the requested tool action with a validation error message.
validation_failed() {
  echo "OSAC hook validation failed: $*" >&2
  exit 2
}

# Fingerprint tracked changes and untracked files generated under a component.
workspace_fingerprint() {
  local component_path=$1 path
  {
    git -C "$repo_root" diff --binary HEAD -- "$component_path"
    {
      find "$repo_root/$component_path" -type f -name '*.go' -exec cksum {} +
      while IFS= read -r -d '' path; do
        cksum "$repo_root/$path"
      done < <(git -C "$repo_root" ls-files --others --exclude-standard -z -- "$component_path")
    } | LC_ALL=C sort
  }
}

# Run canonical shared proto lint and block the tool action on errors.
run_proto_lint() {
  make -C "$repo_root/proto" lint || validation_failed "proto lint failed; fix it before continuing."
}

# Lint proto sources present in the staged, modified, or untracked worktree.
run_commit_checks() {
  local paths path
  paths=$({
    git -C "$repo_root" diff --name-only HEAD --
    git -C "$repo_root" ls-files --others --exclude-standard
  })
  while IFS= read -r path; do
    if is_proto_source "$path"; then
      run_proto_lint
      return 0
    fi
  done <<<"$paths"
}

# Normalize supported GitHub remote URL forms to owner/repository.
github_repo_from_url() {
  local url=$1
  case "$url" in
    https://github.com/*) url=${url#https://github.com/} ;;
    http://github.com/*) url=${url#http://github.com/} ;;
    git@github.com:*) url=${url#git@github.com:} ;;
    ssh://git@github.com/*) url=${url#ssh://git@github.com/} ;;
    ssh://git@github.com:22/*) url=${url#ssh://git@github.com:22/} ;;
    *) return 1 ;;
  esac
  url=${url%.git}
  printf '%s' "$url"
}

# Read explicit --base and --repo options from the gh pr create command.
parse_create_target() {
  local -a words
  local line i
  PR_BASE=
  PR_REPO=
  while IFS= read -r line; do
    read -r -a words <<<"$line"
    for ((i = 0; i < ${#words[@]}; i++)); do
      case "${words[i]}" in
        --base|-B)
          if ((i + 1 < ${#words[@]})); then
            PR_BASE=${words[i + 1]}
            ((i += 1))
          fi
          ;;
        --base=*) PR_BASE=${words[i]#*=} ;;
        --repo|-R)
          if ((i + 1 < ${#words[@]})); then
            PR_REPO=${words[i + 1]}
            ((i += 1))
          fi
          ;;
        --repo=*) PR_REPO=${words[i]#*=} ;;
      esac
    done
  done <<<"$tool_command"
}

# Resolve a local ref for the target repository's pull-request base branch.
resolve_base_ref() {
  local repository_info target_repo default_branch remote remote_repo remote_url
  parse_create_target

  if [[ -n "$PR_REPO" ]]; then
    repository_info=$(gh repo view "$PR_REPO" --json nameWithOwner,defaultBranchRef --jq '[.nameWithOwner, .defaultBranchRef.name] | @tsv') || return 1
  else
    repository_info=$(gh repo view --json nameWithOwner,defaultBranchRef --jq '[.nameWithOwner, .defaultBranchRef.name] | @tsv') || return 1
  fi
  IFS=$'\t' read -r target_repo default_branch <<<"$repository_info"
  [[ -n "$target_repo" && -n "$default_branch" ]] || return 1
  [[ -n "$PR_BASE" ]] || PR_BASE=$default_branch

  while IFS= read -r remote; do
    remote_url=$(git -C "$repo_root" remote get-url "$remote" 2>/dev/null) || continue
    remote_repo=$(github_repo_from_url "$remote_url") || continue
    if [[ "$remote_repo" == "$target_repo" ]]; then
      local candidate="refs/remotes/$remote/$PR_BASE"
      if git -C "$repo_root" show-ref --verify --quiet "$candidate"; then
        printf '%s' "$candidate"
        return 0
      fi
    fi
  done < <(git -C "$repo_root" remote)

  if git -C "$repo_root" show-ref --verify --quiet "refs/heads/$PR_BASE"; then
    printf 'refs/heads/%s' "$PR_BASE"
    return 0
  fi
  return 1
}

# Collect committed, uncommitted, and untracked paths changed from the PR base.
get_pr_paths() {
  local base_ref merge_base
  base_ref=$(resolve_base_ref) || return 1
  merge_base=$(git -C "$repo_root" merge-base "$base_ref" HEAD 2>/dev/null) || return 1
  {
    git -C "$repo_root" diff --name-only "$merge_base" HEAD --
    git -C "$repo_root" diff --name-only HEAD --
    git -C "$repo_root" ls-files --others --exclude-standard
  } | sort -u
}

# Run fulfillment-service formatting drift detection, proto lint, and unit tests.
run_service_checks() {
  local paths=$1 path before after
  before=$(workspace_fingerprint fulfillment-service)
  while IFS= read -r path; do
    case "$path" in
      fulfillment-service/*.go)
        [[ -f "$repo_root/$path" ]] || continue
        gofmt -s -w "$repo_root/$path" || validation_failed "gofmt failed for $path."
      ;;
    esac
  done <<<"$paths"

  after=$(workspace_fingerprint fulfillment-service)
  if [[ "$before" != "$after" ]]; then
    validation_failed "gofmt changed fulfillment-service files; review and commit those changes before creating the PR."
  fi

  run_proto_lint
  (cd "$repo_root/fulfillment-service" && ginkgo run -r internal) || validation_failed "fulfillment-service unit tests failed."
}

# Run operator formatting, lint, tests, and post-test generation drift detection.
run_operator_checks() {
  local before after after_test
  before=$(workspace_fingerprint osac-operator)
  make -C "$repo_root/osac-operator" fmt || validation_failed "osac-operator formatting failed."
  after=$(workspace_fingerprint osac-operator)
  if [[ "$before" != "$after" ]]; then
    validation_failed "make fmt changed osac-operator files; review and commit those changes before creating the PR."
  fi
  make -C "$repo_root/osac-operator" lint || validation_failed "osac-operator lint failed."
  make -C "$repo_root/osac-operator" test || validation_failed "osac-operator tests failed."
  after_test=$(workspace_fingerprint osac-operator)
  if [[ "$after" != "$after_test" ]]; then
    validation_failed "make test modified or generated osac-operator files; review and commit those changes before creating the PR."
  fi
}

# Select changed-component checks, falling back to both when base resolution fails.
run_pr_checks() {
  local paths path service_changed=0 operator_changed=0
  if ! paths=$(get_pr_paths); then
    echo "OSAC hook could not resolve the PR base and changed paths; validating both components." >&2
    paths=$({
      git -C "$repo_root" ls-files -- fulfillment-service
      git -C "$repo_root" ls-files --others --exclude-standard -- fulfillment-service
    })
    service_changed=1
    operator_changed=1
  else
    while IFS= read -r path; do
      case "$path" in
        proto/*)
          service_changed=1
          operator_changed=1
          ;;
        fulfillment-service/*) service_changed=1 ;;
        osac-operator/*) operator_changed=1 ;;
      esac
    done <<<"$paths"
  fi

  if ((service_changed)); then
    run_service_checks "$paths"
  fi
  if ((operator_changed)); then
    run_operator_checks
  fi
}

if [[ "$tool_command" == *"git commit"* ]]; then
  run_commit_checks
fi
if [[ "$tool_command" == *"gh pr create"* ]]; then
  run_pr_checks
fi
