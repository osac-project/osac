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

OSAC_AI_SKILLS_NAME=".osac-ai-skills"
AI_WORKFLOWS_NAME=".ai-workflows"

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
  "\$REAL" -C "\$dest" -c user.email=smoke@test -c user.name=smoke \
    commit -q --allow-empty -m seed
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
  printf '#\n' > "${dir}/skills/.keep"
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

# Sets caller's home, root, bin, home_skills, home_workflows, repo_skills, clone_log.
prepare_fixture() {
  home="${TMPDIR_ROOT}/home-$1"
  root="${TMPDIR_ROOT}/osac-$1"
  bin="${TMPDIR_ROOT}/bin-$1"
  home_skills="${home}/${OSAC_AI_SKILLS_NAME}"
  home_workflows="${home}/${AI_WORKFLOWS_NAME}"
  repo_skills="${root}/${OSAC_AI_SKILLS_NAME}"
  clone_log="${home}/clone.log"
  mkdir -p "$home" "$bin"
  write_git_wrapper "${bin}/git"
  seed_vendor_ok "$home_skills"
  seed_ai_workflows "$home_workflows"
  prepare_osac "$root"
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
  [[ "${url%.git}" == *"/smokeuser/${repo}" || "${url%.git}" == *":smokeuser/${repo}" ]] \
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
  local home root bin home_skills home_workflows repo_skills clone_log out
  prepare_fixture five

  out=$(run_bootstrap "$root" "$home" "$bin" 2>&1) || fail "bootstrap failed: $out"
  assert_expected_clones "$root" "$clone_log"
  pass "clones all five sibling repos under PROJECT_ROOT, not into docs/"
}

test_rerun_updates_expected_clone() {
  local home root bin home_skills home_workflows repo_skills clone_log out ep
  prepare_fixture update
  ep="${root}/enhancement-proposals"

  run_bootstrap "$root" "$home" "$bin" >/dev/null
  echo marker > "${ep}/keep-me"
  : > "$clone_log"
  out=$(run_bootstrap "$root" "$home" "$bin" 2>&1) || fail "bootstrap re-run failed: $out"
  [[ -f "${ep}/keep-me" ]] || fail "re-run deleted an expected clone"
  [[ ! -s "$clone_log" ]] || fail "re-run should not git clone again: $(cat "$clone_log")"
  echo "$out" | grep -q 'enhancement-proposals' \
    || fail "re-run should mention enhancement-proposals update: $out"
  pass "re-run updates an expected clone and does not delete it"
}

test_skips_unrelated_existing_dir() {
  local home root bin home_skills home_workflows repo_skills clone_log out ux
  prepare_fixture skip
  ux="${root}/osac-ux"
  mkdir -p "$ux"
  echo leftover > "${ux}/not-the-repo"

  out=$(run_bootstrap "$root" "$home" "$bin" 2>&1) || fail "bootstrap failed: $out"
  grep -q leftover "${ux}/not-the-repo" || fail "unrelated osac-ux/ dir was overwritten"
  echo "$out" | grep -qi 'skip' \
    || fail "expected skip warning for unrelated osac-ux/: $out"
  [[ ! -d "${ux}/.git" ]] || fail "skip path must not init a git repo"
  [[ -d "${root}/osac-ui/.git" ]] \
    || fail "skip of osac-ux must still clone later siblings (osac-ui)"
  [[ -d "${root}/osac-docs/.git" ]] \
    || fail "skip of osac-ux must still clone later siblings (osac-docs)"
  pass "skips an existing dir that is not the expected clone"
}

test_extra_list_entry_clones_without_other_edits() {
  local home root bin home_skills home_workflows repo_skills clone_log out tmp
  prepare_fixture extra

  grep -q 'SIBLINGS=(' "${root}/tools/bootstrap.sh" \
    || fail "bootstrap.sh has no SIBLINGS=( list"
  tmp="${root}/tools/bootstrap.sh.new"
  awk '
    { print }
    /SIBLINGS=\(/ && !done { print "  \"fixture-sibling\""; done=1 }
  ' "${root}/tools/bootstrap.sh" >"$tmp"
  mv "$tmp" "${root}/tools/bootstrap.sh"
  chmod +x "${root}/tools/bootstrap.sh"

  out=$(run_bootstrap "$root" "$home" "$bin" 2>&1) || fail "bootstrap failed: $out"
  assert_expected_clones "$root" "$clone_log"
  [[ -d "${root}/fixture-sibling/.git" ]] \
    || fail "extra SIBLINGS entry was not cloned"
  grep -q 'osac-project/fixture-sibling' "$clone_log" \
    || fail "extra entry clone URL missing: $(cat "$clone_log")"
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
  local home root bin home_skills home_workflows repo_skills clone_log out ux
  prepare_fixture fail
  ux="${root}/osac-ux"

  out=$(OSAC_SMOKE_FAIL_CLONE=osac-ux run_bootstrap "$root" "$home" "$bin" 2>&1) \
    || fail "bootstrap should continue after a sibling clone failure: $out"
  echo "$out" | grep -qi 'Clone failed for osac-ux' \
    || fail "expected clone-failed message for osac-ux: $out"
  [[ ! -e "$ux" ]] \
    || fail "failed clone must not leave dest behind: $(ls -la "$ux" 2>/dev/null || true)"
  [[ -d "${root}/osac-ui/.git" ]] \
    || fail "clone failure of osac-ux must still clone later siblings"
  [[ -d "${root}/enhancement-proposals/.git" ]] \
    || fail "clone failure of osac-ux must still clone earlier siblings"

  out=$(run_bootstrap "$root" "$home" "$bin" 2>&1) || fail "retry after failed clone failed: $out"
  [[ -d "${ux}/.git" ]] || fail "retry did not clone osac-ux after cleanup"
  pass "failed clone removes dest so a later run can retry"
}

test_expected_sibling_requires_org_boundary() {
  local home root bin home_skills home_workflows repo_skills clone_log out docs_dest ep
  prepare_fixture boundary
  docs_dest="${root}/osac-docs"
  ep="${root}/enhancement-proposals"

  run_bootstrap "$root" "$home" "$bin" >/dev/null
  "$REAL_GIT" -C "$docs_dest" remote set-url origin \
    "https://github.com/evil-osac-project/docs.git"
  echo keep > "${docs_dest}/keep-me"

  out=$(run_bootstrap "$root" "$home" "$bin" 2>&1) || fail "bootstrap failed: $out"
  echo "$out" | grep -qi 'skip' \
    || fail "evil-osac-project/docs must not count as osac-project/docs: $out"
  grep -q keep "${docs_dest}/keep-me" \
    || fail "unrelated osac-docs/ dir was overwritten"

  "$REAL_GIT" -C "$ep" remote set-url origin \
    "git@github.com:osac-project/enhancement-proposals.git"
  out=$(run_bootstrap "$root" "$home" "$bin" 2>&1) || fail "bootstrap failed on SSH remote: $out"
  echo "$out" | grep -q 'Updating enhancement-proposals' \
    || fail "SSH osac-project remote should still update: $out"
  pass "expected-clone match requires / or : before osac-project"
}

test_missing_gh_without_no_fork_exits() {
  local home root bin home_skills home_workflows repo_skills clone_log out rc=0
  prepare_fixture no-gh

  ln -sf "$(command -v realpath)" "${bin}/realpath"
  ln -sf "$(command -v dirname)" "${bin}/dirname"
  # PATH is $bin only so a host /usr/bin/gh cannot satisfy command -v gh.
  # Invoke bash by absolute path so PATH isolation does not hide the interpreter.
  out=$(HOME="$home" PATH="$bin" \
    OSAC_SMOKE_CLONE_LOG="$clone_log" \
    /bin/bash "${root}/tools/bootstrap.sh" 2>&1) || rc=$?
  [[ "$rc" -eq 1 ]] || fail "missing gh expected exit 1, got $rc: $out"
  echo "$out" | grep -qi 'gh CLI is not installed' \
    || fail "expected gh-missing error: $out"
  [[ ! -d "${root}/enhancement-proposals" ]] \
    || fail "must not clone siblings when gh is missing"
  pass "missing gh without --no-fork exits before sibling clones"
}

test_no_fork_leaves_writeable_without_fork_remote() {
  local home root bin home_skills home_workflows repo_skills clone_log out
  prepare_fixture nofork
  write_gh_wrapper "${bin}/gh"

  out=$(run_bootstrap "$root" "$home" "$bin" 2>&1) || fail "bootstrap --no-fork failed: $out"
  echo "$out" | grep -q 'read-only, no forks' \
    || fail "expected read-only banner: $out"
  assert_expected_clones "$root" "$clone_log"
  assert_no_fork_remote "${root}/enhancement-proposals" "enhancement-proposals"
  assert_no_fork_remote "${root}/osac-ui" "osac-ui"
  assert_no_fork_remote "${root}/osac-test-infra" "osac-test-infra"
  assert_no_fork_remote "${root}/osac-docs" "osac-docs"
  assert_no_fork_remote "${root}/osac-ux" "osac-ux"
  [[ ! -s "${home}/gh.log" ]] || fail "--no-fork must not invoke gh: $(cat "${home}/gh.log")"
  pass "--no-fork clones siblings without fork remotes even if gh is on PATH"
}

test_forks_writeable_siblings_not_osac_ux_or_vendors() {
  local home root bin home_skills home_workflows repo_skills clone_log out gh_log
  prepare_fixture fork
  write_gh_wrapper "${bin}/gh"
  gh_log="${home}/gh.log"

  out=$(run_bootstrap_fork "$root" "$home" "$bin" 2>&1) || fail "bootstrap fork path failed: $out"
  echo "$out" | grep -q 'GitHub user: smokeuser' \
    || fail "expected GitHub user banner: $out"
  assert_expected_clones "$root" "$clone_log"
  assert_fork_remote "${root}/enhancement-proposals" "enhancement-proposals"
  assert_fork_remote "${root}/osac-ui" "osac-ui"
  assert_fork_remote "${root}/osac-test-infra" "osac-test-infra"
  assert_fork_remote "${root}/osac-docs" "docs"
  assert_no_fork_remote "${root}/osac-ux" "osac-ux"
  assert_no_fork_remote "$home_skills" "osac-ai-skills vendor"
  assert_no_fork_remote "$home_workflows" "ai-workflows vendor"
  grep -q 'repo fork osac-project/enhancement-proposals' "$gh_log" \
    || fail "expected gh repo fork for enhancement-proposals: $(cat "$gh_log")"
  grep -q 'repo fork osac-project/docs' "$gh_log" \
    || fail "docs fork must use GitHub repo name docs: $(cat "$gh_log")"
  if grep -q 'repo fork osac-project/osac-ux' "$gh_log"; then
    fail "must not gh fork osac-ux: $(cat "$gh_log")"
  fi
  if grep -q 'repo fork osac-project/osac-ai-skills' "$gh_log"; then
    fail "must not gh fork osac-ai-skills: $(cat "$gh_log")"
  fi
  pass "forks writeable siblings; skips osac-ux and vendors"
}

test_rerun_adds_fork_remote_to_existing_clone() {
  local home root bin home_skills home_workflows repo_skills clone_log out ep
  prepare_fixture fork-rerun
  write_gh_wrapper "${bin}/gh"
  ep="${root}/enhancement-proposals"

  run_bootstrap "$root" "$home" "$bin" >/dev/null
  assert_no_fork_remote "$ep" "enhancement-proposals after --no-fork"
  : > "${home}/gh.log"
  out=$(run_bootstrap_fork "$root" "$home" "$bin" 2>&1) || fail "fork re-run failed: $out"
  assert_fork_remote "$ep" "enhancement-proposals"
  assert_fork_remote "${root}/osac-docs" "docs"
  assert_no_fork_remote "${root}/osac-ux" "osac-ux"
  echo "$out" | grep -q 'Adding fork remote for enhancement-proposals' \
    || fail "re-run should add missing fork remotes: $out"
  pass "re-run adds fork remotes to existing origin-only clones"
}

test_unrelated_same_name_github_repo_is_not_used_as_fork() {
  local home root bin home_skills home_workflows repo_skills clone_log out
  prepare_fixture fork-collision
  write_gh_wrapper "${bin}/gh"

  out=$(OSAC_SMOKE_FORK_COLLISION=docs run_bootstrap_fork "$root" "$home" "$bin" 2>&1) \
    || fail "bootstrap should continue when docs fork collides: $out"
  echo "$out" | grep -qi 'Failed to fork osac-project/docs' \
    || fail "expected skip for unrelated github.com/smokeuser/docs: $out"
  assert_no_fork_remote "${root}/osac-docs" "osac-docs after docs name collision"
  assert_fork_remote "${root}/enhancement-proposals" "enhancement-proposals"
  pass "does not point fork at an unrelated same-name GitHub repo"
}

test_home_worktree_vendor_is_updated_not_recloned() {
  local home root bin home_skills home_workflows repo_skills clone_log source out
  prepare_fixture vendor-wt
  source="${TMPDIR_ROOT}/skills-vendor-src"
  rm -rf "$home_skills"
  seed_vendor_ok "$source"
  "$REAL_GIT" -C "$source" checkout -q --detach
  "$REAL_GIT" -C "$source" worktree add -q "$home_skills" main
  [[ -f "${home_skills}/.git" ]] || fail "expected HOME vendor worktree"

  out=$(run_bootstrap "$root" "$home" "$bin" 2>&1) || fail "bootstrap failed: $out"
  echo "$out" | grep -q "Updating osac-ai-skills" \
    || fail "HOME worktree vendor should be updated: $out"
  echo "$out" | grep -q "Cloning osac-ai-skills" \
    && fail "must not clone a second vendor over a HOME worktree: $out"
  [[ ! -e "$repo_skills" ]] \
    || fail "must not create project-local vendor when HOME worktree is usable"
  pass "HOME linked worktree vendor is updated, not re-cloned"
}

test_skips_update_when_sibling_not_on_main() {
  local home root bin home_skills home_workflows repo_skills clone_log out branch ep
  prepare_fixture skip-rebase
  write_gh_wrapper "${bin}/gh"
  ep="${root}/enhancement-proposals"

  run_bootstrap "$root" "$home" "$bin" >/dev/null
  "$REAL_GIT" -C "$ep" checkout -q -b feat/keep-me
  echo keep > "${ep}/keep-me"

  out=$(run_bootstrap_fork "$root" "$home" "$bin" 2>&1) \
    || fail "bootstrap failed: $out"
  echo "$out" | grep -q "enhancement-proposals is on 'feat/keep-me'" \
    || fail "should skip rebase when sibling is not on main: $out"
  echo "$out" | grep -q "enhancement-proposals up to date" \
    && fail "must not claim a feature branch was rebased: $out"
  branch=$("$REAL_GIT" -C "$ep" symbolic-ref --short HEAD)
  [[ "$branch" == "feat/keep-me" ]] \
    || fail "must stay on feat/keep-me, got: $branch"
  [[ -f "${ep}/keep-me" ]] \
    || fail "must not drop uncommitted work on a feature branch"
  assert_fork_remote "$ep" "enhancement-proposals"
  pass "skips rebase on a feature branch and still adds the fork remote"
}

test_fork_remote_match_requires_user_boundary() {
  local home root bin home_skills home_workflows repo_skills clone_log out url docs_dest
  prepare_fixture fork-boundary
  write_gh_wrapper "${bin}/gh"
  docs_dest="${root}/osac-docs"

  run_bootstrap "$root" "$home" "$bin" >/dev/null
  "$REAL_GIT" -C "$docs_dest" remote add fork \
    "https://github.com/evilsmokeuser/docs.git"

  out=$(run_bootstrap_fork "$root" "$home" "$bin" 2>&1) || fail "bootstrap failed: $out"
  echo "$out" | grep -q 'already exists with a different URL' \
    || fail "evilsmokeuser/docs must not count as smokeuser/docs: $out"
  url=$(git -C "$docs_dest" remote get-url fork)
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
test_home_worktree_vendor_is_updated_not_recloned
test_skips_update_when_sibling_not_on_main
test_fork_remote_match_requires_user_boundary

echo "All bootstrap sibling-clone smoke tests passed."
