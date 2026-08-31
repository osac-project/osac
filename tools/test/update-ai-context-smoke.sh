#!/usr/bin/env bash
# Smoke test: SessionStart update-ai-context.sh leftover dest and skip-off-main.
# Run from osac/: bash tools/test/update-ai-context-smoke.sh
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
HOOK="${REPO_ROOT}/.claude/hooks/update-ai-context.sh"
REAL_GIT=$(command -v git)

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

[[ -x "$HOOK" ]] || fail "missing executable $HOOK"
[[ -n "$REAL_GIT" ]] || fail "git not on PATH"

TMPDIR_ROOT=$(mktemp -d)
trap 'rm -rf "$TMPDIR_ROOT"' EXIT

init_repo() {
  local dir="$1"
  mkdir -p "$dir"
  "$REAL_GIT" init -q "$dir"
  "$REAL_GIT" -C "$dir" checkout -q -b main
  "$REAL_GIT" -C "$dir" -c user.email=smoke@test -c user.name=smoke \
    commit -q --allow-empty -m seed
  "$REAL_GIT" -C "$dir" remote add origin "$dir"
  "$REAL_GIT" -C "$dir" update-ref refs/remotes/origin/main HEAD
}

run_hook() {
  local home="$1" project="$2"
  HOME="$home" CLAUDE_PROJECT_DIR="$project" bash "$HOOK"
}

test_leftover_home_dir_does_not_rebase_enclosing_repo() {
  local home project before after out
  home="${TMPDIR_ROOT}/dotfiles-home"
  project="${TMPDIR_ROOT}/dotfiles-proj"
  mkdir -p "$home" "$project"
  init_repo "$home"
  mkdir -p "${home}/.ai-workflows"
  before=$("$REAL_GIT" -C "$home" rev-parse HEAD)
  out=$(run_hook "$home" "$project")
  after=$("$REAL_GIT" -C "$home" rev-parse HEAD)
  [[ "$before" == "$after" ]] || fail "leftover ~/.ai-workflows rebased HOME: $before -> $after"
  echo "$out" | grep -q 'ai-workflows:' \
    && fail "leftover dest must not report ai-workflows status: $out"
  pass "leftover ~/.ai-workflows does not fetch/rebase enclosing HOME"
}

test_leftover_home_falls_back_to_repo_local() {
  local home project out
  home="${TMPDIR_ROOT}/fallback-home"
  project="${TMPDIR_ROOT}/fallback-proj"
  mkdir -p "$home" "$project"
  init_repo "$home"
  mkdir -p "${home}/.ai-workflows"
  init_repo "${project}/.ai-workflows"
  out=$(run_hook "$home" "$project")
  echo "$out" | grep -q 'ai-workflows: up to date' \
    || fail "expected repo-local update, got: $out"
  pass "leftover HOME dest falls back to repo-local .ai-workflows"
}

test_skip_rebase_off_main() {
  local home project out
  home="${TMPDIR_ROOT}/feat-home"
  project="${TMPDIR_ROOT}/feat-proj"
  mkdir -p "$home" "$project"
  init_repo "${home}/.ai-workflows"
  "$REAL_GIT" -C "${home}/.ai-workflows" checkout -q -b feat/not-main
  out=$(run_hook "$home" "$project")
  echo "$out" | grep -q 'ai-workflows: on feat/not-main, skipped' \
    || fail "expected skip off main, got: $out"
  pass "skips rebase when ~/.ai-workflows is not on main"
}

test_empty_claude_project_dir_skips_repo_local() {
  local home project out
  home="${TMPDIR_ROOT}/empty-proj-home"
  project="${TMPDIR_ROOT}/empty-proj-proj"
  mkdir -p "$home" "$project"
  init_repo "$home"
  mkdir -p "${home}/.ai-workflows"
  init_repo "${project}/.ai-workflows"
  out=$(HOME="$home" CLAUDE_PROJECT_DIR="" bash "$HOOK")
  echo "$out" | grep -q 'ai-workflows:' \
    && fail "empty CLAUDE_PROJECT_DIR must not update repo-local: $out"
  pass "empty CLAUDE_PROJECT_DIR does not fall back to repo-local"
}

test_leftover_home_dir_does_not_rebase_enclosing_repo
test_leftover_home_falls_back_to_repo_local
test_skip_rebase_off_main
test_empty_claude_project_dir_skips_repo_local

echo "All update-ai-context smoke tests passed."
