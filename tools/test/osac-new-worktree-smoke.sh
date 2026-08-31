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
  got=$(osac_jira_ticket_from_branch "feat/OSAC-1234-follow-up-OSAC-5678")
  [[ "$got" == "OSAC-1234" ]] || fail "expected first key OSAC-1234, got ${got}"
  got=$(osac_jira_ticket_from_branch "feat/no-ticket")
  [[ -z "$got" ]] || fail "expected empty ticket, got ${got}"
  pass "osac_jira_ticket_from_branch extracts the first OSAC-NNNN"
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
    got=$(osac_jira_ticket_from_branch "feat/OSAC-1234-follow-up-OSAC-5678")
    [[ "$got" == "OSAC-1234" ]]
  ' zsh "$HELPERS" || fail "zsh nounset failed to parse first OSAC-NNNN from branch"
  pass "osac_jira_ticket_from_branch works under zsh nounset"
}

# PATH with only this bin (no host timeout/gtimeout). Requires git wrapper already in $bin.
isolated_core_path() {
  local bin="$1" c src
  for c in awk jq basename dirname mkdir chmod cat cp bash tr; do
    src=$(command -v "$c") || fail "need $c for isolated PATH"
    ln -sf "$src" "${bin}/$c"
  done
  printf '%s' "$bin"
}

write_git_worktree_stub() {
  local bin="$1" repo="$2" log="$3"
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
}

test_bootstrap_fail_prints_recovery() {
  local parent repo dest bin log out
  parent="${TMPDIR_ROOT}/wt-fail-parent"
  repo="${parent}/osac"
  dest="${parent}/osac-OSAC-3958"
  bin="${TMPDIR_ROOT}/wt-fail-bin"
  log="${TMPDIR_ROOT}/wt-fail.log"
  mkdir -p "${repo}/tools" "$bin"
  printf '#!/bin/sh\necho bootstrap-fail >> "%s"\nexit 1\n' "$log" \
    > "${repo}/tools/bootstrap.sh"
  chmod +x "${repo}/tools/bootstrap.sh"
  "$REAL_GIT" init -q "$repo"
  "$REAL_GIT" -C "$repo" checkout -q -b main
  "$REAL_GIT" -C "$repo" -c user.email=smoke@test -c user.name=smoke \
    commit -q --allow-empty -m seed
  write_git_worktree_stub "$bin" "$repo" "$log"

  (
    cd "$repo"
    # shellcheck source=../osac-helpers.sh
    source "$HELPERS"
    export PATH="${bin}:${PATH}"
    export OSAC_WORKTREE_PARENT="$parent"
    out=$(osac-new-worktree feat/OSAC-3958 2>&1) && fail "bootstrap fail should fail: $out" || true
    echo "$out" | grep -q "tools/bootstrap.sh failed" \
      || fail "expected bootstrap failure: $out"
    echo "$out" | grep -q "Worktree exists at ${dest}" \
      || fail "expected worktree path in recovery: $out"
    echo "$out" | grep -Fq "Recovery: cd ${dest} && ./tools/bootstrap.sh --no-fork" \
      || fail "expected --no-fork recovery: $out"
    echo "$out" | grep -q "Current directory is already ${dest}" \
      || fail "expected cwd hint on bootstrap failure: $out"
  )
  pass "bootstrap failure prints --no-fork recovery"
}

test_no_timeout_skips_jira() {
  local parent repo dest bin log jira_log out
  parent="${TMPDIR_ROOT}/wt-skip-parent"
  repo="${parent}/osac"
  dest="${parent}/osac-OSAC-1234"
  bin="${TMPDIR_ROOT}/wt-skip-bin"
  log="${TMPDIR_ROOT}/wt-skip.log"
  jira_log="${TMPDIR_ROOT}/wt-skip-jira.log"
  mkdir -p "${repo}/tools" "$bin"
  printf '#!/bin/sh\nprintf "bootstrap %%s\\n" "$0" >> "%s"\nexit 0\n' "$log" \
    > "${repo}/tools/bootstrap.sh"
  chmod +x "${repo}/tools/bootstrap.sh"
  "$REAL_GIT" init -q "$repo"
  "$REAL_GIT" -C "$repo" checkout -q -b main
  "$REAL_GIT" -C "$repo" -c user.email=smoke@test -c user.name=smoke \
    commit -q --allow-empty -m seed
  write_git_worktree_stub "$bin" "$repo" "$log"
  isolated_core_path "$bin" >/dev/null
  cat > "${bin}/jira" <<EOF
#!/bin/sh
echo jira-called >> "${jira_log}"
printf '%s\\n' '{"fields":{"summary":"should-not-run","issuetype":{"name":"Task"}}}'
EOF
  chmod +x "${bin}/jira"

  out=$(
    cd "$repo"
    # shellcheck source=../osac-helpers.sh
    source "$HELPERS"
    export PATH="$bin"
    export OSAC_WORKTREE_PARENT="$parent"
    osac-new-worktree feat/OSAC-1234 2>&1
  ) || fail "skip-jira path should succeed: $out"
  echo "$out" | grep -q "no timeout or gtimeout on PATH" \
    || fail "expected timeout skip warning: $out"
  echo "$out" | grep -q "could not fetch Jira ticket" \
    && fail "skip path must not also warn about fetch: $out"
  [[ -f "$jira_log" ]] && fail "jira must not be invoked without timeout: $(cat "$jira_log")"
  [[ -f "${dest}/.claude/CLAUDE.md" ]] \
    && fail "skip path must not write Jira context"
  pass "missing timeout/gtimeout skips Jira fetch"
}

test_gtimeout_used_when_timeout_missing() {
  local parent repo dest bin log
  parent="${TMPDIR_ROOT}/wt-gto-parent"
  repo="${parent}/osac"
  dest="${parent}/osac-OSAC-1234"
  bin="${TMPDIR_ROOT}/wt-gto-bin"
  log="${TMPDIR_ROOT}/wt-gto.log"
  mkdir -p "${repo}/tools" "$bin"
  printf '#!/bin/sh\nprintf "bootstrap %%s\\n" "$0" >> "%s"\nexit 0\n' "$log" \
    > "${repo}/tools/bootstrap.sh"
  chmod +x "${repo}/tools/bootstrap.sh"
  "$REAL_GIT" init -q "$repo"
  "$REAL_GIT" -C "$repo" checkout -q -b main
  "$REAL_GIT" -C "$repo" -c user.email=smoke@test -c user.name=smoke \
    commit -q --allow-empty -m seed
  write_git_worktree_stub "$bin" "$repo" "$log"
  isolated_core_path "$bin" >/dev/null
  cat > "${bin}/gtimeout" <<EOF
#!/bin/sh
printf 'gtimeout %s\\n' "\$*" >> "${log}"
shift
exec "\$@"
EOF
  chmod +x "${bin}/gtimeout"
  cat > "${bin}/jira" <<'EOF'
#!/bin/sh
printf '%s\n' '{"fields":{"summary":"gtimeout ticket","issuetype":{"name":"Task"}}}'
EOF
  chmod +x "${bin}/jira"

  (
    cd "$repo"
    # shellcheck source=../osac-helpers.sh
    source "$HELPERS"
    export PATH="$bin"
    export OSAC_WORKTREE_PARENT="$parent"
    osac-new-worktree feat/OSAC-1234 >/dev/null
  )
  grep -q 'gtimeout ticket' "${dest}/.claude/CLAUDE.md" \
    || fail "expected gtimeout-backed Jira context in ${dest}/.claude/CLAUDE.md"
  grep -q '^gtimeout 15 jira issue view OSAC-1234 --raw$' "$log" \
    || fail "expected Jira to run through gtimeout: $(cat "$log")"
  pass "gtimeout is used when timeout is missing"
}

test_jira_summary_strips_newlines() {
  local parent repo dest bin log
  parent="${TMPDIR_ROOT}/wt-nl-parent"
  repo="${parent}/osac"
  dest="${parent}/osac-OSAC-1234"
  bin="${TMPDIR_ROOT}/wt-nl-bin"
  log="${TMPDIR_ROOT}/wt-nl.log"
  mkdir -p "${repo}/tools" "$bin"
  printf '#!/bin/sh\nprintf "bootstrap %%s\\n" "$0" >> "%s"\nexit 0\n' "$log" \
    > "${repo}/tools/bootstrap.sh"
  chmod +x "${repo}/tools/bootstrap.sh"
  "$REAL_GIT" init -q "$repo"
  "$REAL_GIT" -C "$repo" checkout -q -b main
  "$REAL_GIT" -C "$repo" -c user.email=smoke@test -c user.name=smoke \
    commit -q --allow-empty -m seed
  write_git_worktree_stub "$bin" "$repo" "$log"
  cat > "${bin}/timeout" <<'EOF'
#!/bin/sh
shift
exec "$@"
EOF
  chmod +x "${bin}/timeout"
  cat > "${bin}/jira" <<'EOF'
#!/bin/sh
printf '%s\n' '{"fields":{"summary":"dogfood\n## Injected","issuetype":{"name":"Task\nBad"}}}'
EOF
  chmod +x "${bin}/jira"

  (
    cd "$repo"
    # shellcheck source=../osac-helpers.sh
    source "$HELPERS"
    export PATH="${bin}:${PATH}"
    export OSAC_WORKTREE_PARENT="$parent"
    osac-new-worktree feat/OSAC-1234 >/dev/null
  )
  grep -q 'dogfood## Injected' "${dest}/.claude/CLAUDE.md" \
    || fail "expected stripped summary, got: $(cat "${dest}/.claude/CLAUDE.md")"
  grep -q 'TaskBad' "${dest}/.claude/CLAUDE.md" \
    || fail "expected stripped type, got: $(cat "${dest}/.claude/CLAUDE.md")"
  grep -q '^## Injected' "${dest}/.claude/CLAUDE.md" \
    && fail "newline in summary must not become a markdown heading"
  pass "Jira summary and type strip CR/LF before append"
}

test_forwards_bootstrap_args() {
  local parent repo dest bin log
  parent="${TMPDIR_ROOT}/wt-args-parent"
  repo="${parent}/osac"
  dest="${parent}/osac-extra-args"
  bin="${TMPDIR_ROOT}/wt-args-bin"
  log="${TMPDIR_ROOT}/wt-args.log"
  mkdir -p "${repo}/tools" "$bin"
  printf '#!/bin/sh\nprintf "bootstrap %%s\\n" "$*" >> "%s"\nexit 0\n' "$log" \
    > "${repo}/tools/bootstrap.sh"
  chmod +x "${repo}/tools/bootstrap.sh"
  "$REAL_GIT" init -q "$repo"
  "$REAL_GIT" -C "$repo" checkout -q -b main
  "$REAL_GIT" -C "$repo" -c user.email=smoke@test -c user.name=smoke \
    commit -q --allow-empty -m seed
  write_git_worktree_stub "$bin" "$repo" "$log"

  (
    cd "$repo"
    # shellcheck source=../osac-helpers.sh
    source "$HELPERS"
    export PATH="${bin}:${PATH}"
    export OSAC_WORKTREE_PARENT="$parent"
    osac-new-worktree feat/extra-args --no-fork >/dev/null
  )
  grep -q -- '--no-fork' "$log" \
    || fail "expected bootstrap --no-fork: $(cat "$log")"
  pass "forwards extra args to tools/bootstrap.sh"
}

test_dest_basename_and_repo_bootstrap
test_jira_ticket_from_branch
test_zsh_nounset_jira_ticket
test_bootstrap_fail_prints_recovery
test_no_timeout_skips_jira
test_gtimeout_used_when_timeout_missing
test_jira_summary_strips_newlines
test_forwards_bootstrap_args

echo "All osac-new-worktree smoke tests passed."
