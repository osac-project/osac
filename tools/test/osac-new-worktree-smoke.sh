#!/usr/bin/env bash
# Smoke test: tools/osac-helpers.sh osac-new-worktree guards.
# Run from osac/: bash tools/test/osac-new-worktree-smoke.sh
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
HELPERS="${REPO_ROOT}/tools/osac-helpers.sh"
REAL_GIT=$(command -v git)

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

[[ -f "$HELPERS" ]] || fail "missing $HELPERS"
[[ -n "$REAL_GIT" ]] || fail "git not on PATH"

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

test_dest_basename_and_repo_bootstrap() {
  local parent repo dest bin log
  parent="${TMPDIR_ROOT}/wt-parent"
  repo="${parent}/osac"
  dest="${parent}/osac-OSAC-1234"
  bin="${TMPDIR_ROOT}/wt-bin"
  log="${TMPDIR_ROOT}/wt.log"
  mkdir -p "${repo}/tools" "$bin"
  printf '#!/bin/sh\nprintf "bootstrap %%s\\n" "$0" >> "%s"\nexit 0\n' "$log" \
    > "${repo}/tools/bootstrap.sh"
  chmod +x "${repo}/tools/bootstrap.sh"
  "$REAL_GIT" init -q "$repo"
  "$REAL_GIT" -C "$repo" checkout -q -b main
  "$REAL_GIT" -C "$repo" -c user.email=smoke@test -c user.name=smoke \
    commit -q --allow-empty -m seed

  cat > "${bin}/git" <<EOF
#!/usr/bin/env bash
set -euo pipefail
if [[ " \$* " == *" worktree "* && " \$* " == *" add "* ]]; then
  dest="\${!#}"
  mkdir -p "\$dest/tools"
  cp "${repo}/tools/bootstrap.sh" "\$dest/tools/bootstrap.sh"
  chmod +x "\$dest/tools/bootstrap.sh"
  echo "worktree-add \$dest" >> "${log}"
  exit 0
fi
exec "${REAL_GIT}" "\$@"
EOF
  chmod +x "${bin}/git"

  cat > "${bin}/timeout" <<'EOF'
#!/bin/sh
shift
exec "$@"
EOF
  chmod +x "${bin}/timeout"

  cat > "${bin}/jira" <<'EOF'
#!/bin/sh
printf '%s\n' '{"fields":{"summary":"dogfood ticket","issuetype":{"name":"Task"}}}'
EOF
  chmod +x "${bin}/jira"

  (
    cd "$repo"
    # shellcheck source=../osac-helpers.sh
    source "$HELPERS"
    export PATH="${bin}:${PATH}"
    export OSAC_WORKTREE_PARENT="$parent"
    osac-new-worktree feat/OSAC-1234 >/dev/null
    [[ "$(pwd)" == "$dest" ]] || fail "sourced call should cd into ${dest}, got $(pwd)"
  )
  grep -q "worktree-add ${dest}" "$log" \
    || fail "expected worktree dest ${dest}: $(cat "$log")"
  grep -q "tools/bootstrap.sh" "$log" \
    || fail "expected tools/bootstrap.sh: $(cat "$log")"
  grep -q "bootstrap.sh" "$log" \
    || fail "bootstrap was not invoked: $(cat "$log")"
  [[ -x "${dest}/tools/bootstrap.sh" ]] \
    || fail "dest should contain tools/bootstrap.sh"
  grep -q 'redhat.atlassian.net/browse/OSAC-1234' "${dest}/.claude/CLAUDE.md" \
    || fail "expected Jira context in ${dest}/.claude/CLAUDE.md"
  pass "worktree dest is osac-<basename> and runs tools/bootstrap.sh"
}

test_jira_ticket_from_branch() {
  local got
  got=$(osac_jira_ticket_from_branch "feat/OSAC-4040-wt-dogfood")
  [[ "$got" == "OSAC-4040" ]] || fail "expected OSAC-4040, got ${got}"
  got=$(osac_jira_ticket_from_branch "feat/no-ticket")
  [[ -z "$got" ]] || fail "expected empty ticket, got ${got}"
  pass "osac_jira_ticket_from_branch extracts OSAC-NNNN"
}

test_zsh_nounset_jira_ticket() {
  if ! command -v zsh >/dev/null 2>&1; then
    echo "SKIP: zsh not on PATH (Jira ticket parse under nounset)"
    return 0
  fi
  zsh -c '
    set -eu
    source "$1"
    got=$(osac_jira_ticket_from_branch "feat/OSAC-4040-wt-dogfood")
    [[ "$got" == "OSAC-4040" ]]
  ' zsh "$HELPERS" || fail "zsh nounset failed to parse OSAC-4040 from branch"
  pass "osac_jira_ticket_from_branch works under zsh nounset"
}

test_dest_basename_and_repo_bootstrap
test_jira_ticket_from_branch
test_zsh_nounset_jira_ticket

echo "All osac-new-worktree smoke tests passed."
