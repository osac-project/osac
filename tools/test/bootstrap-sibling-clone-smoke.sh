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

run_bootstrap() {
  local root="$1" home="$2" bin="$3"
  HOME="$home" PATH="${bin}:${PATH}" \
    OSAC_SMOKE_CLONE_LOG="${home}/clone.log" \
    bash "${root}/tools/bootstrap.sh"
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
  if grep -E 'osac-project/docs .*/docs$' "$log" | grep -vq osac-docs; then
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

test_clones_all_five_into_project_root
test_rerun_updates_expected_clone
test_skips_unrelated_existing_dir
test_extra_list_entry_clones_without_other_edits
test_nested_abort_skips_sibling_clones

echo "All bootstrap sibling-clone smoke tests passed."
