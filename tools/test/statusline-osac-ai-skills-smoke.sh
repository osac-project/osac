#!/usr/bin/env bash
# Smoke test: Claude statusline flags missing/stale osac-ai-skills.
# Run from osac/: bash tools/test/statusline-osac-ai-skills-smoke.sh
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
STATUSLINE="${REPO_ROOT}/.claude/hooks/statusline.sh"
REAL_GIT=$(command -v git)

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

[[ -x "$STATUSLINE" ]] || fail "missing executable $STATUSLINE"
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

make_behind() {
  local dir="$1" n="$2"
  local i
  for i in $(seq 1 "$n"); do
    "$REAL_GIT" -C "$dir" -c user.email=smoke@test -c user.name=smoke \
      commit -q --allow-empty -m "ahead-$i"
  done
  "$REAL_GIT" -C "$dir" update-ref refs/remotes/origin/main HEAD
  "$REAL_GIT" -C "$dir" reset -q --hard "HEAD~${n}"
}

run_statusline() {
  local home="$1" project="$2"
  printf '%s' '{}' | HOME="$home" bash "$STATUSLINE" "$project"
}

test_missing_vendor_is_muted_not_found() {
  local home="${TMPDIR_ROOT}/home-missing"
  local project="${TMPDIR_ROOT}/proj-missing"
  mkdir -p "$home"
  init_repo "$project"
  local out
  out=$(run_statusline "$home" "$project")
  echo "$out" | grep -q 'osac-ai-skills: not found' \
    || fail "expected muted osac-ai-skills: not found, got: $out"
  pass "missing vendor → osac-ai-skills: not found"
}

test_repo_local_up_to_date() {
  local home="${TMPDIR_ROOT}/home-local"
  local project="${TMPDIR_ROOT}/proj-local"
  mkdir -p "$home"
  init_repo "$project"
  init_repo "${project}/.osac-ai-skills"
  local out
  out=$(run_statusline "$home" "$project")
  echo "$out" | grep -q 'osac-ai-skills: main ✓' \
    || fail "expected repo-local ✓, got: $out"
  pass "repo-local .osac-ai-skills at origin/main → ✓"
}

test_repo_local_behind() {
  local home="${TMPDIR_ROOT}/home-behind"
  local project="${TMPDIR_ROOT}/proj-behind"
  mkdir -p "$home"
  init_repo "$project"
  init_repo "${project}/.osac-ai-skills"
  make_behind "${project}/.osac-ai-skills" 3
  local out
  out=$(run_statusline "$home" "$project")
  echo "$out" | grep -q 'osac-ai-skills: main ↓3 behind' \
    || fail "expected ↓3 behind, got: $out"
  pass "repo-local N behind origin/main → ↓N"
}

test_home_git_wins_over_repo_local() {
  local home="${TMPDIR_ROOT}/home-pref"
  local project="${TMPDIR_ROOT}/proj-pref"
  mkdir -p "$home"
  init_repo "$project"
  init_repo "${project}/.osac-ai-skills"
  make_behind "${project}/.osac-ai-skills" 2
  init_repo "${home}/.osac-ai-skills"
  local out
  out=$(run_statusline "$home" "$project")
  echo "$out" | grep -q 'osac-ai-skills: main ✓' \
    || fail "HOME git checkout should win (✓), got: $out"
  echo "$out" | grep -q '↓2' \
    && fail "should not report repo-local behind when HOME wins: $out"
  pass "~/.osac-ai-skills/.git wins over repo-local"
}

test_home_without_git_falls_back_to_repo_local() {
  local home="${TMPDIR_ROOT}/home-nongit"
  local project="${TMPDIR_ROOT}/proj-nongit"
  mkdir -p "${home}/.osac-ai-skills"
  init_repo "$project"
  init_repo "${project}/.osac-ai-skills"
  local out
  out=$(run_statusline "$home" "$project")
  echo "$out" | grep -q 'osac-ai-skills: not found' \
    && fail "HOME dir without .git must not hide repo-local: $out"
  echo "$out" | grep -q 'osac-ai-skills: main ✓' \
    || fail "expected fallback to repo-local ✓, got: $out"
  pass "HOME without .git falls back to repo-local"
}

test_home_worktree_wins_over_repo_local() {
  local home="${TMPDIR_ROOT}/home-wt"
  local project="${TMPDIR_ROOT}/proj-wt"
  local source="${TMPDIR_ROOT}/skills-src"
  mkdir -p "$home"
  init_repo "$source"
  init_repo "$project"
  init_repo "${project}/.osac-ai-skills"
  make_behind "${project}/.osac-ai-skills" 2
  # Free `main` in the source checkout so the linked worktree can occupy it.
  "$REAL_GIT" -C "$source" checkout -q --detach
  "$REAL_GIT" -C "$source" worktree add -q "${home}/.osac-ai-skills" main
  [[ -f "${home}/.osac-ai-skills/.git" ]] || fail "expected linked worktree .git file"
  [[ ! -d "${home}/.osac-ai-skills/.git" ]] || fail "worktree .git should not be a directory"
  local out
  out=$(run_statusline "$home" "$project")
  echo "$out" | grep -q 'osac-ai-skills: main ✓' \
    || fail "HOME worktree should win (✓), got: $out"
  echo "$out" | grep -q '↓2' \
    && fail "should not report repo-local behind when HOME worktree wins: $out"
  pass "HOME linked worktree wins over repo-local"
}

test_repo_local_nongit_dir_is_not_found() {
  local home="${TMPDIR_ROOT}/home-leftover"
  local project="${TMPDIR_ROOT}/proj-leftover"
  mkdir -p "$home"
  init_repo "$project"
  mkdir -p "${project}/.osac-ai-skills"
  local out
  out=$(run_statusline "$home" "$project")
  echo "$out" | grep -q 'osac-ai-skills: not found' \
    || fail "leftover non-git dir must not inherit osac's branch: $out"
  echo "$out" | grep -q 'osac-ai-skills: main ✓' \
    && fail "false green for leftover .osac-ai-skills: $out"
  pass "repo-local leftover dir → osac-ai-skills: not found"
}

test_home_git_subdir_falls_back_to_repo_local() {
  local home="${TMPDIR_ROOT}/home-dotfiles"
  local project="${TMPDIR_ROOT}/proj-dotfiles"
  mkdir -p "$home"
  init_repo "$home"
  make_behind "$home" 5
  mkdir -p "${home}/.osac-ai-skills"
  init_repo "$project"
  init_repo "${project}/.osac-ai-skills"
  local out
  out=$(run_statusline "$home" "$project")
  echo "$out" | grep -q 'osac-ai-skills: main ✓' \
    || fail "HOME git with plain .osac-ai-skills subdir must fall back: $out"
  echo "$out" | grep -q '↓5' \
    && fail "must not report HOME's behind-count as osac-ai-skills: $out"
  pass "HOME git checkout with plain subdir falls back to repo-local"
}

test_missing_vendor_is_muted_not_found
test_repo_local_up_to_date
test_repo_local_behind
test_home_git_wins_over_repo_local
test_home_without_git_falls_back_to_repo_local
test_home_worktree_wins_over_repo_local
test_repo_local_nongit_dir_is_not_found
test_home_git_subdir_falls_back_to_repo_local

echo "All statusline osac-ai-skills smoke tests passed."
