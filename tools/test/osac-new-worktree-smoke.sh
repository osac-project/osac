#!/usr/bin/env bash
# Smoke test: tools/osac-helpers.sh osac-new-worktree guards.
# Run from osac/: bash tools/test/osac-new-worktree-smoke.sh
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
HELPERS="${REPO_ROOT}/tools/osac-helpers.sh"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

[[ -f "$HELPERS" ]] || fail "missing $HELPERS"

TMPDIR_ROOT=$(mktemp -d)
trap 'rm -rf "$TMPDIR_ROOT"' EXIT

# shellcheck source=../osac-helpers.sh
source "$HELPERS"

out=$(osac-new-worktree 2>&1) && fail "empty branch should fail: $out" || true
echo "$out" | grep -q "please provide a branch name" \
  || fail "expected missing-branch error: $out"
pass "rejects a missing branch name"

out=$(osac-new-worktree "feat/has space" 2>&1) && fail "spaced branch should fail: $out" || true
echo "$out" | grep -q "must not contain spaces" \
  || fail "expected invalid-branch error: $out"
pass "rejects a branch name with spaces"

out=$(osac-new-worktree "feat/../escape" 2>&1) && fail ".. branch should fail: $out" || true
echo "$out" | grep -q "must not contain spaces" \
  || fail "expected invalid-branch error for ..: $out"
pass "rejects a branch name containing .."

not_osac="${TMPDIR_ROOT}/other-repo"
mkdir -p "$not_osac"
git -C "$not_osac" init -q
git -C "$not_osac" checkout -q -b main
git -C "$not_osac" -c user.email=smoke@test -c user.name=smoke \
  commit -q --allow-empty -m seed
(
  cd "$not_osac"
  # shellcheck source=../osac-helpers.sh
  source "$HELPERS"
  out=$(osac-new-worktree feat/nope 2>&1) && fail "non-osac repo should fail: $out" || true
  echo "$out" | grep -q "must be run from an osac clone" \
    || fail "expected osac-clone guard: $out"
)
pass "rejects a git repo that is not an osac clone"

echo "All osac-new-worktree smoke tests passed."
