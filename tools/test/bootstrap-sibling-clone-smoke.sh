#!/usr/bin/env bash
# Smoke test: tools/bootstrap.sh declarative sibling clones (under PROJECT_ROOT).
# Run from osac/: bash tools/test/bootstrap-sibling-clone-smoke.sh
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
BOOTSTRAP="${REPO_ROOT}/tools/bootstrap.sh"
REAL_GIT=$(command -v git)

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

[[ -f "$BOOTSTRAP" ]] || fail "missing $BOOTSTRAP"
[[ -x "$BOOTSTRAP" ]] || fail "$BOOTSTRAP is not executable"
[[ -n "$REAL_GIT" ]] || fail "git not on PATH"

TMPDIR_ROOT=$(mktemp -d)
trap 'rm -rf "$TMPDIR_ROOT"' EXIT

EXPECTED_DIRS=(
  enhancement-proposals
  osac-ux
  osac-ui
  osac-test-infra
  osac-docs
)

write_git_wrapper() {
  local dest="$1"
  cat >"$dest" <<EOF
#!/usr/bin/env bash
set -euo pipefail
REAL="${REAL_GIT}"
LOG="\${OSAC_SMOKE_CLONE_LOG}"
if [[ " \$* " == *" clone "* ]]; then
  dest="\${!#}"
  url=""
  prev=""
  for a in "\$@"; do
    if [[ "\$prev" == "clone" ]]; then
      url="\$a"
    fi
    prev="\$a"
  done
  if [[ -n "\${OSAC_SMOKE_FAIL_CLONE:-}" && "\$dest" == *"\${OSAC_SMOKE_FAIL_CLONE}" ]]; then
    mkdir -p "\$dest"
    echo partial > "\$dest/partial"
    exit 1
  fi
  printf '%s %s\\n' "\$url" "\$dest" >> "\$LOG"
  mkdir -p "\$dest"
  "\$REAL" init -q "\$dest"
  "\$REAL" -C "\$dest" checkout -q -b main
  "\$REAL" -C "\$dest" remote add origin "\$url"
  exit 0
fi
if [[ " \$* " == *" fetch "* ]] || [[ " \$* " == *" rebase "* ]]; then
  exit 0
fi
exec "\$REAL" "\$@"
EOF
  chmod +x "$dest"
}

seed_vendor_ok() {
  local dir="$1"
  mkdir -p "${dir}/skills" "${dir}/tools"
  printf '#!/bin/sh\nexit 0\n' > "${dir}/tools/link-agent-skills.sh"
  chmod +x "${dir}/tools/link-agent-skills.sh"
  "$REAL_GIT" init -q "$dir"
  "$REAL_GIT" -C "$dir" checkout -q -b main
  "$REAL_GIT" -C "$dir" add skills tools
  "$REAL_GIT" -C "$dir" -c user.email=smoke@test -c user.name=smoke \
    commit -q --allow-empty -m seed
  "$REAL_GIT" -C "$dir" remote add origin "$dir"
}

seed_ai_workflows() {
  local dir="$1"
  mkdir -p "$dir"
  printf '#!/bin/sh\necho stub-install\nexit 0\n' > "${dir}/install.sh"
  chmod +x "${dir}/install.sh"
  "$REAL_GIT" init -q "$dir"
  "$REAL_GIT" -C "$dir" checkout -q -b main
  "$REAL_GIT" -C "$dir" remote add origin "$dir"
}

prepare_osac() {
  local root="$1"
  mkdir -p "${root}/tools" "${root}/docs"
  cp "$BOOTSTRAP" "${root}/tools/bootstrap.sh"
  chmod +x "${root}/tools/bootstrap.sh"
  printf '#!/bin/sh\necho stub-link\nexit 0\n' > "${root}/tools/link-agent-skills.sh"
  chmod +x "${root}/tools/link-agent-skills.sh"
  echo "in-tree" > "${root}/docs/ARCHITECTURE.md"
}

write_gh_wrapper() {
  local dest="$1"
  cat >"$dest" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
LOG="${OSAC_SMOKE_GH_LOG:-/dev/null}"
printf 'gh %s\n' "$*" >> "$LOG"
case "${1:-}" in
  auth) exit 0 ;;
  api)
    if [[ "${2:-}" == "user" ]]; then
      echo smokeuser
      exit 0
    fi
    ;;
  config)
    echo https
    exit 0
    ;;
  repo)
    if [[ "${2:-}" == "fork" ]]; then
      for a in "$@"; do
        if [[ -n "${OSAC_SMOKE_FORK_COLLISION:-}" && "$a" == "osac-project/${OSAC_SMOKE_FORK_COLLISION}" ]]; then
          exit 1
        fi
      done
      exit 0
    fi
    if [[ "${2:-}" == "view" ]]; then
      if [[ " $* " == *" --json "* && " $* " == *" parent "* ]]; then
        echo ""
        exit 0
      fi
      exit 0
    fi
    exit 0
    ;;
esac
exit 0
EOF
  chmod +x "$dest"
}

run_bootstrap() {
  local root="$1" home="$2" bin="$3"
  shift 3
  HOME="$home" PATH="${bin}:${PATH}" \
    OSAC_SMOKE_CLONE_LOG="${home}/clone.log" \
    OSAC_SMOKE_GH_LOG="${home}/gh.log" \
    bash "${root}/tools/bootstrap.sh" --no-fork "$@"
}

run_bootstrap_fork() {
  local root="$1" home="$2" bin="$3"
  HOME="$home" PATH="${bin}:${PATH}" \
    OSAC_SMOKE_CLONE_LOG="${home}/clone.log" \
    OSAC_SMOKE_GH_LOG="${home}/gh.log" \
    bash "${root}/tools/bootstrap.sh"
}

assert_no_fork_remote() {
  local dest="$1" label="$2"
  if git -C "$dest" remote get-url fork >/dev/null 2>&1; then
    fail "$label must not have a fork remote: $(git -C "$dest" remote -v)"
  fi
}

assert_fork_remote() {
  local dest="$1" repo="$2"
  local url
  url=$(git -C "$dest" remote get-url fork 2>/dev/null) \
    || fail "missing fork remote on ${dest}: $(git -C "$dest" remote -v 2>/dev/null || true)"
  [[ "${url%.git}" == *"smokeuser/${repo}" ]] \
    || fail "fork remote on ${dest} expected smokeuser/${repo}, got: $url"
}

assert_expected_clones() {
  local root="$1" log="$2"
  local dir
  for dir in "${EXPECTED_DIRS[@]}"; do
    [[ -d "${root}/${dir}/.git" ]] || fail "missing sibling checkout ${root}/${dir}"
  done
  [[ -f "${root}/docs/ARCHITECTURE.md" ]] || fail "tracked docs/ARCHITECTURE.md was removed"
  grep -q 'in-tree' "${root}/docs/ARCHITECTURE.md" \
    || fail "tracked docs/ARCHITECTURE.md was overwritten"
  [[ ! -d "${root}/docs/.git" ]] || fail "docs repo must not land in docs/"

  grep -q 'osac-project/enhancement-proposals' "$log" \
    || fail "clone log missing enhancement-proposals: $(cat "$log")"
  grep -q 'osac-project/osac-ux' "$log" || fail "clone log missing osac-ux: $(cat "$log")"
  grep -q 'osac-project/osac-ui' "$log" || fail "clone log missing osac-ui: $(cat "$log")"
  grep -q 'osac-project/osac-test-infra' "$log" \
    || fail "clone log missing osac-test-infra: $(cat "$log")"
  grep -q 'osac-project/docs' "$log" || fail "clone log missing osac-project/docs: $(cat "$log")"
  grep -q "${root}/osac-docs" "$log" || fail "docs repo dest must be osac-docs: $(cat "$log")"
  if grep -F "osac-project/docs.git ${root}/docs" "$log"; then
    fail "docs repo cloned to docs/ rather than osac-docs/: $(cat "$log")"
  fi
}

test_clones_all_five_into_project_root() {
  local home="${TMPDIR_ROOT}/home-five"
  local root="${TMPDIR_ROOT}/osac-five"
  local bin="${TMPDIR_ROOT}/bin-five"
  mkdir -p "$home" "$bin"
  write_git_wrapper "${bin}/git"
  seed_vendor_ok "${home}/.osac-ai-skills"
  seed_ai_workflows "${home}/.ai-workflows"
  prepare_osac "$root"

  local out
  out=$(run_bootstrap "$root" "$home" "$bin" 2>&1) || fail "bootstrap failed: $out"
  assert_expected_clones "$root" "${home}/clone.log"
  pass "clones all five sibling repos under PROJECT_ROOT, not into docs/"
}

test_rerun_updates_expected_clone() {
  local home="${TMPDIR_ROOT}/home-update"
  local root="${TMPDIR_ROOT}/osac-update"
  local bin="${TMPDIR_ROOT}/bin-update"
  mkdir -p "$home" "$bin"
  write_git_wrapper "${bin}/git"
  seed_vendor_ok "${home}/.osac-ai-skills"
  seed_ai_workflows "${home}/.ai-workflows"
  prepare_osac "$root"

  run_bootstrap "$root" "$home" "$bin" >/dev/null
  echo marker > "${root}/enhancement-proposals/keep-me"
  : > "${home}/clone.log"
  local out
  out=$(run_bootstrap "$root" "$home" "$bin" 2>&1) || fail "bootstrap re-run failed: $out"
  [[ -f "${root}/enhancement-proposals/keep-me" ]] \
    || fail "re-run deleted an expected clone"
  [[ ! -s "${home}/clone.log" ]] \
    || fail "re-run should not git clone again: $(cat "${home}/clone.log")"
  echo "$out" | grep -q 'enhancement-proposals' \
    || fail "re-run should mention enhancement-proposals update: $out"
  pass "re-run updates an expected clone and does not delete it"
}

test_skips_unrelated_existing_dir() {
  local home="${TMPDIR_ROOT}/home-skip"
  local root="${TMPDIR_ROOT}/osac-skip"
  local bin="${TMPDIR_ROOT}/bin-skip"
  mkdir -p "$home" "$bin"
  write_git_wrapper "${bin}/git"
  seed_vendor_ok "${home}/.osac-ai-skills"
  seed_ai_workflows "${home}/.ai-workflows"
  prepare_osac "$root"
  mkdir -p "${root}/osac-ux"
  echo leftover > "${root}/osac-ux/not-the-repo"

  local out
  out=$(run_bootstrap "$root" "$home" "$bin" 2>&1) || fail "bootstrap failed: $out"
  grep -q leftover "${root}/osac-ux/not-the-repo" \
    || fail "unrelated osac-ux/ dir was overwritten"
  echo "$out" | grep -qi 'skip' \
    || fail "expected skip warning for unrelated osac-ux/: $out"
  [[ ! -d "${root}/osac-ux/.git" ]] || fail "skip path must not init a git repo"
  [[ -d "${root}/osac-ui/.git" ]] \
    || fail "skip of osac-ux must still clone later siblings (osac-ui)"
  [[ -d "${root}/osac-docs/.git" ]] \
    || fail "skip of osac-ux must still clone later siblings (osac-docs)"
  pass "skips an existing dir that is not the expected clone"
}

test_extra_list_entry_clones_without_other_edits() {
  local home="${TMPDIR_ROOT}/home-extra"
  local root="${TMPDIR_ROOT}/osac-extra"
  local bin="${TMPDIR_ROOT}/bin-extra"
  mkdir -p "$home" "$bin"
  write_git_wrapper "${bin}/git"
  seed_vendor_ok "${home}/.osac-ai-skills"
  seed_ai_workflows "${home}/.ai-workflows"
  prepare_osac "$root"

  grep -q 'SIBLINGS=(' "${root}/tools/bootstrap.sh" \
    || fail "bootstrap.sh has no SIBLINGS=( list"
  # Insert one extra entry after the opening SIBLINGS=( line only.
  local tmp="${root}/tools/bootstrap.sh.new"
  awk '
    { print }
    /SIBLINGS=\(/ && !done { print "  \"fixture-sibling\""; done=1 }
  ' "${root}/tools/bootstrap.sh" >"$tmp"
  mv "$tmp" "${root}/tools/bootstrap.sh"
  chmod +x "${root}/tools/bootstrap.sh"

  local out
  out=$(run_bootstrap "$root" "$home" "$bin" 2>&1) || fail "bootstrap failed: $out"
  assert_expected_clones "$root" "${home}/clone.log"
  [[ -d "${root}/fixture-sibling/.git" ]] \
    || fail "extra SIBLINGS entry was not cloned"
  grep -q 'osac-project/fixture-sibling' "${home}/clone.log" \
    || fail "extra entry clone URL missing: $(cat "${home}/clone.log")"
  pass "adding a SIBLINGS list entry clones an extra dest with no other code edits"
}

test_nested_abort_skips_sibling_clones() {
  local nest="${TMPDIR_ROOT}/nested-ws"
  local empty_home="${TMPDIR_ROOT}/nested-ws-home"
  mkdir -p "${nest}/osac/tools" "${nest}/osac/docs" "${empty_home}"
  printf '#!/bin/sh\necho workspace-bootstrap\n' > "${nest}/bootstrap.sh"
  chmod +x "${nest}/bootstrap.sh"
  cp "$BOOTSTRAP" "${nest}/osac/tools/bootstrap.sh"
  chmod +x "${nest}/osac/tools/bootstrap.sh"
  echo "in-tree" > "${nest}/osac/docs/ARCHITECTURE.md"

  local out rc=0
  out=$(HOME="${empty_home}" bash "${nest}/osac/tools/bootstrap.sh" 2>&1) || rc=$?
  [[ "$rc" -eq 1 ]] || fail "nested bootstrap expected exit 1, got $rc: $out"
  [[ ! -d "${nest}/osac/osac-docs" ]] \
    || fail "nested abort must not clone osac-docs"
  [[ ! -d "${nest}/osac/enhancement-proposals" ]] \
    || fail "nested abort must not clone enhancement-proposals"
  pass "nested workspace abort happens before sibling clones"
}

test_failed_clone_cleans_dest() {
  local home="${TMPDIR_ROOT}/home-fail"
  local root="${TMPDIR_ROOT}/osac-fail"
  local bin="${TMPDIR_ROOT}/bin-fail"
  mkdir -p "$home" "$bin"
  write_git_wrapper "${bin}/git"
  seed_vendor_ok "${home}/.osac-ai-skills"
  seed_ai_workflows "${home}/.ai-workflows"
  prepare_osac "$root"

  local out
  out=$(OSAC_SMOKE_FAIL_CLONE=osac-ux run_bootstrap "$root" "$home" "$bin" 2>&1) \
    || fail "bootstrap should continue after a sibling clone failure: $out"
  echo "$out" | grep -qi 'Clone failed for osac-ux' \
    || fail "expected clone-failed message for osac-ux: $out"
  [[ ! -e "${root}/osac-ux" ]] \
    || fail "failed clone must not leave dest behind: $(ls -la "${root}/osac-ux" 2>/dev/null || true)"
  [[ -d "${root}/osac-ui/.git" ]] \
    || fail "clone failure of osac-ux must still clone later siblings"
  [[ -d "${root}/enhancement-proposals/.git" ]] \
    || fail "clone failure of osac-ux must still clone earlier siblings"

  out=$(run_bootstrap "$root" "$home" "$bin" 2>&1) || fail "retry after failed clone failed: $out"
  [[ -d "${root}/osac-ux/.git" ]] || fail "retry did not clone osac-ux after cleanup"
  pass "failed clone removes dest so a later run can retry"
}

test_expected_sibling_requires_org_boundary() {
  local home="${TMPDIR_ROOT}/home-boundary"
  local root="${TMPDIR_ROOT}/osac-boundary"
  local bin="${TMPDIR_ROOT}/bin-boundary"
  mkdir -p "$home" "$bin"
  write_git_wrapper "${bin}/git"
  seed_vendor_ok "${home}/.osac-ai-skills"
  seed_ai_workflows "${home}/.ai-workflows"
  prepare_osac "$root"

  run_bootstrap "$root" "$home" "$bin" >/dev/null
  "$REAL_GIT" -C "${root}/osac-docs" remote set-url origin \
    "https://github.com/evil-osac-project/docs.git"
  echo keep > "${root}/osac-docs/keep-me"

  local out
  out=$(run_bootstrap "$root" "$home" "$bin" 2>&1) || fail "bootstrap failed: $out"
  echo "$out" | grep -qi 'skip' \
    || fail "evil-osac-project/docs must not count as osac-project/docs: $out"
  grep -q keep "${root}/osac-docs/keep-me" \
    || fail "unrelated osac-docs/ dir was overwritten"

  "$REAL_GIT" -C "${root}/enhancement-proposals" remote set-url origin \
    "git@github.com:osac-project/enhancement-proposals.git"
  out=$(run_bootstrap "$root" "$home" "$bin" 2>&1) || fail "bootstrap failed on SSH remote: $out"
  echo "$out" | grep -q 'Updating enhancement-proposals' \
    || fail "SSH osac-project remote should still update: $out"
  pass "expected-clone match requires / or : before osac-project"
}

test_missing_gh_without_no_fork_exits() {
  local home="${TMPDIR_ROOT}/home-no-gh"
  local root="${TMPDIR_ROOT}/osac-no-gh"
  local bin="${TMPDIR_ROOT}/bin-no-gh"
  mkdir -p "$home" "$bin"
  write_git_wrapper "${bin}/git"
  seed_vendor_ok "${home}/.osac-ai-skills"
  seed_ai_workflows "${home}/.ai-workflows"
  prepare_osac "$root"

  ln -sf "$(command -v realpath)" "${bin}/realpath"
  ln -sf "$(command -v dirname)" "${bin}/dirname"
  local out rc=0
  # PATH is $bin only so a host /usr/bin/gh cannot satisfy command -v gh.
  # Invoke bash by absolute path so PATH isolation does not hide the interpreter.
  out=$(HOME="$home" PATH="$bin" \
    OSAC_SMOKE_CLONE_LOG="${home}/clone.log" \
    /bin/bash "${root}/tools/bootstrap.sh" 2>&1) || rc=$?
  [[ "$rc" -eq 1 ]] || fail "missing gh expected exit 1, got $rc: $out"
  echo "$out" | grep -qi 'gh CLI is not installed' \
    || fail "expected gh-missing error: $out"
  [[ ! -d "${root}/enhancement-proposals" ]] \
    || fail "must not clone siblings when gh is missing"
  pass "missing gh without --no-fork exits before sibling clones"
}

test_no_fork_leaves_writeable_without_fork_remote() {
  local home="${TMPDIR_ROOT}/home-nofork"
  local root="${TMPDIR_ROOT}/osac-nofork"
  local bin="${TMPDIR_ROOT}/bin-nofork"
  mkdir -p "$home" "$bin"
  write_git_wrapper "${bin}/git"
  write_gh_wrapper "${bin}/gh"
  seed_vendor_ok "${home}/.osac-ai-skills"
  seed_ai_workflows "${home}/.ai-workflows"
  prepare_osac "$root"

  local out
  out=$(run_bootstrap "$root" "$home" "$bin" 2>&1) || fail "bootstrap --no-fork failed: $out"
  echo "$out" | grep -q 'read-only, no forks' \
    || fail "expected read-only banner: $out"
  assert_expected_clones "$root" "${home}/clone.log"
  assert_no_fork_remote "${root}/enhancement-proposals" "enhancement-proposals"
  assert_no_fork_remote "${root}/osac-ui" "osac-ui"
  assert_no_fork_remote "${root}/osac-test-infra" "osac-test-infra"
  assert_no_fork_remote "${root}/osac-docs" "osac-docs"
  assert_no_fork_remote "${root}/osac-ux" "osac-ux"
  [[ ! -s "${home}/gh.log" ]] || fail "--no-fork must not invoke gh: $(cat "${home}/gh.log")"
  pass "--no-fork clones siblings without fork remotes even if gh is on PATH"
}

test_forks_writeable_siblings_not_osac_ux_or_vendors() {
  local home="${TMPDIR_ROOT}/home-fork"
  local root="${TMPDIR_ROOT}/osac-fork"
  local bin="${TMPDIR_ROOT}/bin-fork"
  mkdir -p "$home" "$bin"
  write_git_wrapper "${bin}/git"
  write_gh_wrapper "${bin}/gh"
  seed_vendor_ok "${home}/.osac-ai-skills"
  seed_ai_workflows "${home}/.ai-workflows"
  prepare_osac "$root"

  local out
  out=$(run_bootstrap_fork "$root" "$home" "$bin" 2>&1) || fail "bootstrap fork path failed: $out"
  echo "$out" | grep -q 'GitHub user: smokeuser' \
    || fail "expected GitHub user banner: $out"
  assert_expected_clones "$root" "${home}/clone.log"
  assert_fork_remote "${root}/enhancement-proposals" "enhancement-proposals"
  assert_fork_remote "${root}/osac-ui" "osac-ui"
  assert_fork_remote "${root}/osac-test-infra" "osac-test-infra"
  assert_fork_remote "${root}/osac-docs" "docs"
  assert_no_fork_remote "${root}/osac-ux" "osac-ux"
  assert_no_fork_remote "${home}/.osac-ai-skills" "osac-ai-skills vendor"
  assert_no_fork_remote "${home}/.ai-workflows" "ai-workflows vendor"
  grep -q 'repo fork osac-project/enhancement-proposals' "${home}/gh.log" \
    || fail "expected gh repo fork for enhancement-proposals: $(cat "${home}/gh.log")"
  grep -q 'repo fork osac-project/docs' "${home}/gh.log" \
    || fail "docs fork must use GitHub repo name docs: $(cat "${home}/gh.log")"
  if grep -q 'repo fork osac-project/osac-ux' "${home}/gh.log"; then
    fail "must not gh fork osac-ux: $(cat "${home}/gh.log")"
  fi
  if grep -q 'repo fork osac-project/osac-ai-skills' "${home}/gh.log"; then
    fail "must not gh fork osac-ai-skills: $(cat "${home}/gh.log")"
  fi
  pass "forks writeable siblings; skips osac-ux and vendors"
}

test_rerun_adds_fork_remote_to_existing_clone() {
  local home="${TMPDIR_ROOT}/home-fork-rerun"
  local root="${TMPDIR_ROOT}/osac-fork-rerun"
  local bin="${TMPDIR_ROOT}/bin-fork-rerun"
  mkdir -p "$home" "$bin"
  write_git_wrapper "${bin}/git"
  write_gh_wrapper "${bin}/gh"
  seed_vendor_ok "${home}/.osac-ai-skills"
  seed_ai_workflows "${home}/.ai-workflows"
  prepare_osac "$root"

  run_bootstrap "$root" "$home" "$bin" >/dev/null
  assert_no_fork_remote "${root}/enhancement-proposals" "enhancement-proposals after --no-fork"
  : > "${home}/gh.log"
  local out
  out=$(run_bootstrap_fork "$root" "$home" "$bin" 2>&1) || fail "fork re-run failed: $out"
  assert_fork_remote "${root}/enhancement-proposals" "enhancement-proposals"
  assert_fork_remote "${root}/osac-docs" "docs"
  assert_no_fork_remote "${root}/osac-ux" "osac-ux"
  echo "$out" | grep -q 'Adding fork remote for enhancement-proposals' \
    || fail "re-run should add missing fork remotes: $out"
  pass "re-run adds fork remotes to existing origin-only clones"
}

test_unrelated_same_name_github_repo_is_not_used_as_fork() {
  local home="${TMPDIR_ROOT}/home-fork-collision"
  local root="${TMPDIR_ROOT}/osac-fork-collision"
  local bin="${TMPDIR_ROOT}/bin-fork-collision"
  mkdir -p "$home" "$bin"
  write_git_wrapper "${bin}/git"
  write_gh_wrapper "${bin}/gh"
  seed_vendor_ok "${home}/.osac-ai-skills"
  seed_ai_workflows "${home}/.ai-workflows"
  prepare_osac "$root"

  local out
  out=$(OSAC_SMOKE_FORK_COLLISION=docs run_bootstrap_fork "$root" "$home" "$bin" 2>&1) \
    || fail "bootstrap should continue when docs fork collides: $out"
  echo "$out" | grep -qi 'Failed to fork osac-project/docs' \
    || fail "expected skip for unrelated github.com/smokeuser/docs: $out"
  assert_no_fork_remote "${root}/osac-docs" "osac-docs after docs name collision"
  assert_fork_remote "${root}/enhancement-proposals" "enhancement-proposals"
  pass "does not point fork at an unrelated same-name GitHub repo"
}

test_fork_remote_match_requires_user_boundary() {
  local home="${TMPDIR_ROOT}/home-fork-boundary"
  local root="${TMPDIR_ROOT}/osac-fork-boundary"
  local bin="${TMPDIR_ROOT}/bin-fork-boundary"
  mkdir -p "$home" "$bin"
  write_git_wrapper "${bin}/git"
  write_gh_wrapper "${bin}/gh"
  seed_vendor_ok "${home}/.osac-ai-skills"
  seed_ai_workflows "${home}/.ai-workflows"
  prepare_osac "$root"

  run_bootstrap "$root" "$home" "$bin" >/dev/null
  "$REAL_GIT" -C "${root}/osac-docs" remote add fork \
    "https://github.com/evilsmokeuser/docs.git"

  local out
  out=$(run_bootstrap_fork "$root" "$home" "$bin" 2>&1) || fail "bootstrap failed: $out"
  echo "$out" | grep -q 'already exists with a different URL' \
    || fail "evilsmokeuser/docs must not count as smokeuser/docs: $out"
  url=$(git -C "${root}/osac-docs" remote get-url fork)
  [[ "$url" == *evilsmokeuser/docs* ]] \
    || fail "must not overwrite an existing mismatched fork remote: $url"
  pass "fork-remote match requires / or : before \$GH_USER/repo"
}

test_clones_all_five_into_project_root
test_rerun_updates_expected_clone
test_skips_unrelated_existing_dir
test_extra_list_entry_clones_without_other_edits
test_nested_abort_skips_sibling_clones
test_failed_clone_cleans_dest
test_expected_sibling_requires_org_boundary
test_missing_gh_without_no_fork_exits
test_no_fork_leaves_writeable_without_fork_remote
test_forks_writeable_siblings_not_osac_ux_or_vendors
test_rerun_adds_fork_remote_to_existing_clone
test_unrelated_same_name_github_repo_is_not_used_as_fork
test_fork_remote_match_requires_user_boundary

echo "All bootstrap sibling-clone smoke tests passed."
