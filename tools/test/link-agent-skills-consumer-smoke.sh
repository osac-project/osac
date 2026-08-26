#!/usr/bin/env bash
# Smoke test: osac consumer wrapper for osac-ai-skills fan-out.
# Run from osac/: bash tools/test/link-agent-skills-consumer-smoke.sh
#
# Self-contained: prefers a real PROJECT_ROOT-capable fan-out when present,
# otherwise embeds a minimal stub so a clean checkout can run without bootstrap.
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
WRAPPER="${REPO_ROOT}/tools/link-agent-skills.sh"

OSAC_SKILL_NAMES=(
  browser-demo-recording capture-tasks-from-meeting-notes create-pr
  design-review generate-status-report github-actions-workflows jira-task-management
  milestone-scope osac-cluster osac-demo-recording osac-feature osac-release
  performance-review prd-review presentation quick-fix report-bug review-gate
  security-review
)

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

[[ -f "$WRAPPER" ]] || fail "missing $WRAPPER"
[[ -x "$WRAPPER" ]] || fail "$WRAPPER is not executable"

TMPDIR_ROOT=$(mktemp -d)
trap 'rm -rf "$TMPDIR_ROOT"' EXIT

# Minimal fan-out stub: enough for wrapper smoke (umbrellas, ai-workflows, verify).
write_stub_fanout() {
  local dest="$1"
  cat >"$dest" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
if [[ -n "${PROJECT_ROOT:-}" ]]; then
  PROJECT_ROOT="$(realpath "${PROJECT_ROOT}")"
else
  PROJECT_ROOT="$(realpath "$(dirname "${BASH_SOURCE[0]}")/..")"
fi
LINK_CLAUDE=false LINK_CURSOR=false LINK_GEMINI=false LINK_AI=false VERIFY=false
if [[ $# -eq 0 ]]; then LINK_CLAUDE=true; LINK_CURSOR=true; LINK_GEMINI=true; fi
while [[ $# -gt 0 ]]; do
  case "$1" in
    --claude) LINK_CLAUDE=true ;;
    --cursor) LINK_CURSOR=true ;;
    --gemini) LINK_GEMINI=true ;;
    --all) LINK_CLAUDE=true; LINK_CURSOR=true; LINK_GEMINI=true ;;
    --with-ai-workflows) LINK_AI=true ;;
    --verify) VERIFY=true ;;
    -h|--help) exit 0 ;;
    *) echo "Unknown option: $1" >&2; exit 1 ;;
  esac
  shift
done
safe_symlink() {
  local link_path="$1" target="$2"
  if [[ -L "${link_path}" ]]; then rm -f "${link_path}"
  elif [[ -e "${link_path}" ]]; then
    echo "ERROR: ${link_path} exists and is not a symlink; refusing to replace" >&2
    return 1
  fi
  ln -sfn "${target}" "${link_path}"
}
link_agent() {
  mkdir -p "$1"
  safe_symlink "$1/skills" ../skills
  echo "  Linked $1/skills -> ../skills  ($2)"
}
if [[ "${VERIFY}" == true ]]; then
  errors=0
  for pair in ".claude:Claude" ".cursor:Cursor" ".gemini:Gemini"; do
    dir="${PROJECT_ROOT}/${pair%%:*}"; label="${pair##*:}"
    if [[ ! -L "${dir}/skills" ]]; then
      echo "ERROR: ${label}: ${dir}/skills is not a symlink" >&2; errors=1; continue
    fi
    if [[ ! -r "${dir}/skills/create-pr/SKILL.md" ]]; then
      echo "ERROR: ${label}: cannot read create-pr via ${dir}/skills" >&2; errors=1
    else
      echo "  OK ${label}: ${dir}/skills -> ../skills"
    fi
  done
  [[ "${errors}" -eq 0 ]] || { echo "Verification failed." >&2; exit 1; }
  echo "Verification passed."
  exit 0
fi
echo "Linking agent skill directories to skills/..."
if [[ "${LINK_AI}" == true ]]; then
  ai=""
  for d in "${HOME}/.ai-workflows" "${PROJECT_ROOT}/.ai-workflows"; do
    [[ -d "$d" ]] && { ai="$(cd "$d" && pwd -P)"; break; }
  done
  if [[ -n "$ai" ]]; then
    mkdir -p "${PROJECT_ROOT}/skills"
    for wf in _shared bugfix design e2e implement prd; do
      [[ -d "${ai}/${wf}" ]] || continue
      safe_symlink "${PROJECT_ROOT}/skills/${wf}" "${ai}/${wf}"
      echo "  Linked skills/${wf} -> ${ai}/${wf}"
    done
  fi
fi
[[ "${LINK_CLAUDE}" == true ]] && link_agent "${PROJECT_ROOT}/.claude" Claude
[[ "${LINK_CURSOR}" == true ]] && link_agent "${PROJECT_ROOT}/.cursor" Cursor
[[ "${LINK_GEMINI}" == true ]] && link_agent "${PROJECT_ROOT}/.gemini" Gemini
# One shared rule is enough for the refuse-real-file contract on the stub.
STUB_REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ "${LINK_CLAUDE}" == true && -f "${STUB_REPO}/.claude/rules/architecture-patterns.md" ]]; then
  mkdir -p "${PROJECT_ROOT}/.claude/rules"
  safe_symlink "${PROJECT_ROOT}/.claude/rules/architecture-patterns.md" \
    "${STUB_REPO}/.claude/rules/architecture-patterns.md"
fi
exit 0
STUB
  chmod +x "$dest"
}

VENDOR_FANOUT=""
USING_STUB=false
for candidate in \
  "${REPO_ROOT}/.osac-ai-skills/tools/link-agent-skills.sh" \
  "${REPO_ROOT}/../.osac-ai-skills/tools/link-agent-skills.sh" \
  "${REPO_ROOT}/../osac-ai-skills/tools/link-agent-skills.sh"; do
  if [[ -f "$candidate" ]] && grep -q 'PROJECT_ROOT:-' "$candidate" 2>/dev/null; then
    VENDOR_FANOUT=$(cd "$(dirname "$candidate")" && pwd)/link-agent-skills.sh
    break
  fi
done

if [[ -z "$VENDOR_FANOUT" ]]; then
  VENDOR_FANOUT="${TMPDIR_ROOT}/stub-link-agent-skills.sh"
  write_stub_fanout "$VENDOR_FANOUT"
  USING_STUB=true
  echo "NOTE: using embedded fan-out stub (no real osac-ai-skills fan-out found)"
fi

# Isolate HOME so fixtures never pick up the developer's ~/.osac-ai-skills.
run_wrapper() {
  local ws="$1"
  shift
  mkdir -p "${ws}/home"
  (cd "$ws" && HOME="${ws}/home" ./tools/link-agent-skills.sh "$@")
}

# Copy shared canonical files the real fan-out materializes (rules, agent,
# hooks, design context, templates). No-op when the stub is in use except
# for ensuring architecture-patterns.md exists as a vendor target.
seed_shared_canonicals() {
  local vendor="$1"
  local fanout_root rel
  if [[ "$USING_STUB" != true ]]; then
    fanout_root=$(cd "$(dirname "$VENDOR_FANOUT")/.." && pwd)
    for rel in .claude/rules .claude/agents .claude/hooks \
      .design/context .design/templates .prd/templates; do
      if [[ -d "${fanout_root}/${rel}" ]]; then
        mkdir -p "${vendor}/${rel}"
        cp -R "${fanout_root}/${rel}/." "${vendor}/${rel}/"
      fi
    done
  fi
  mkdir -p "${vendor}/.claude/rules"
  if [[ ! -f "${vendor}/.claude/rules/architecture-patterns.md" ]]; then
    echo "# stub architecture-patterns" >"${vendor}/.claude/rules/architecture-patterns.md"
  fi
}

seed_vendor() {
  local ws="$1"
  local vendor="${ws}/.osac-ai-skills"
  mkdir -p "${vendor}/tools" "${vendor}/skills"
  cp "$VENDOR_FANOUT" "${vendor}/tools/link-agent-skills.sh"
  chmod +x "${vendor}/tools/link-agent-skills.sh"

  local real_skills=""
  for candidate in \
    "${REPO_ROOT}/.osac-ai-skills/skills" \
    "${REPO_ROOT}/../.osac-ai-skills/skills" \
    "${REPO_ROOT}/../osac-ai-skills/skills" \
    "${REPO_ROOT}/../skills"; do
    if [[ -d "${candidate}/create-pr" ]]; then
      real_skills=$(cd "$candidate" && pwd -P)
      break
    fi
  done

  local name
  for name in "${OSAC_SKILL_NAMES[@]}"; do
    if [[ -n "$real_skills" && -d "${real_skills}/${name}" ]]; then
      ln -sfn "${real_skills}/${name}" "${vendor}/skills/${name}"
    else
      mkdir -p "${vendor}/skills/${name}"
      echo "# stub ${name}" >"${vendor}/skills/${name}/SKILL.md"
    fi
  done

  seed_shared_canonicals "$vendor"
}

install_wrapper() {
  local ws="$1"
  mkdir -p "${ws}/tools" "${ws}/home"
  cp "$WRAPPER" "${ws}/tools/link-agent-skills.sh"
  chmod +x "${ws}/tools/link-agent-skills.sh"
}

test_missing_vendor_fails() {
  local ws
  ws=$(mktemp -d "${TMPDIR_ROOT}/missing.XXXXXX")
  install_wrapper "$ws"
  local rc=0
  run_wrapper "$ws" --claude >/dev/null 2>&1 || rc=$?
  [[ "$rc" -ne 0 ]] || fail "expected non-zero exit when vendor missing"
  pass "missing vendor dir fails loudly"
}

test_materialize_and_link() {
  local ws
  ws=$(mktemp -d "${TMPDIR_ROOT}/ok.XXXXXX")
  seed_vendor "$ws"
  install_wrapper "$ws"

  mkdir -p "${ws}/.ai-workflows/bugfix" \
    "${ws}/.ai-workflows/design" \
    "${ws}/.ai-workflows/e2e" \
    "${ws}/.ai-workflows/implement" \
    "${ws}/.ai-workflows/prd" \
    "${ws}/.ai-workflows/_shared"
  echo '# stub' >"${ws}/.ai-workflows/bugfix/SKILL.md"
  echo '# stub' >"${ws}/.ai-workflows/design/SKILL.md"
  echo '# stub' >"${ws}/.ai-workflows/e2e/SKILL.md"
  echo '# stub' >"${ws}/.ai-workflows/implement/SKILL.md"
  echo '# stub' >"${ws}/.ai-workflows/prd/SKILL.md"

  run_wrapper "$ws" --all --with-ai-workflows >/dev/null

  [[ -L "${ws}/skills/create-pr" ]] || fail "skills/create-pr is not a symlink"
  [[ -r "${ws}/skills/create-pr/SKILL.md" ]] || fail "cannot read create-pr via skills/"
  local target
  target=$(readlink "${ws}/skills/create-pr")
  [[ "$target" = /* ]] || fail "expected absolute symlink target, got: $target"

  [[ -L "${ws}/.claude/skills" ]] || fail ".claude/skills is not a symlink"
  [[ -L "${ws}/.cursor/skills" ]] || fail ".cursor/skills is not a symlink"
  [[ -L "${ws}/.gemini/skills" ]] || fail ".gemini/skills is not a symlink"
  [[ -r "${ws}/.claude/skills/create-pr/SKILL.md" ]] || fail "cannot read create-pr via .claude/skills"
  [[ -r "${ws}/.cursor/skills/create-pr/SKILL.md" ]] || fail "cannot read create-pr via .cursor/skills"
  [[ -r "${ws}/.gemini/skills/create-pr/SKILL.md" ]] || fail "cannot read create-pr via .gemini/skills"
  [[ -L "${ws}/skills/bugfix" ]] || fail "expected skills/bugfix from --with-ai-workflows"
  pass "materialize + vendored fan-out links consumer tree"
}

test_refuse_real_shared_rule_file() {
  local ws
  ws=$(mktemp -d "${TMPDIR_ROOT}/refuse-rule.XXXXXX")
  seed_vendor "$ws"
  install_wrapper "$ws"

  mkdir -p "${ws}/.claude/rules"
  echo "stale local copy, not the vendor canonical" >"${ws}/.claude/rules/architecture-patterns.md"

  local rc=0
  local err
  err=$(run_wrapper "$ws" --claude 2>&1) || rc=$?
  [[ "$rc" -ne 0 ]] || fail "expected failure when .claude/rules/architecture-patterns.md is a real file that differs from vendor"
  echo "$err" | grep -qi 'not a symlink\|refusing to replace' \
    || fail "expected refusal message, got: $err"
  pass "refuses to replace a real shared rule file that differs from vendor"
}

test_verify_shared_files_are_symlinks() {
  if [[ "$USING_STUB" == true ]]; then
    echo "SKIP: full shared-file --verify needs the real osac-ai-skills fan-out"
    return 0
  fi
  local ws
  ws=$(mktemp -d "${TMPDIR_ROOT}/verify-shared.XXXXXX")
  seed_vendor "$ws"
  install_wrapper "$ws"

  run_wrapper "$ws" --claude >/dev/null || fail "clean wrapper run failed before --verify"

  local out
  out=$(run_wrapper "$ws" --verify --claude 2>&1) || fail "--verify failed after clean run: $out"
  echo "$out" | grep -qi 'Verification passed' \
    || fail "expected Verification passed, got: $out"

  local path
  for path in \
    .claude/rules/architecture-patterns.md \
    .claude/rules/networking-design-alignment.md \
    .claude/rules/request-path-tracing.md \
    .claude/rules/dev-conventions.md \
    .claude/agents/quick-fix.md \
    .claude/hooks/README.md \
    .design/context/enclave-wizard-pipeline.md \
    .design/context/networking-decisions.md \
    .design/context/osac-dimensions.md \
    .design/context/review-patterns.md; do
    [[ -L "${ws}/${path}" ]] || fail "expected ${path} to be a symlink after clean fan-out"
  done
  pass "clean run + --verify reports shared rule/agent/hooks/context files as symlinks"
}

test_refuse_real_skill_directory() {
  local ws
  ws=$(mktemp -d "${TMPDIR_ROOT}/refuse.XXXXXX")
  seed_vendor "$ws"
  install_wrapper "$ws"
  mkdir -p "${ws}/skills/create-pr"
  echo "real leftover" >"${ws}/skills/create-pr/SKILL.md"

  local rc=0
  local err
  err=$(run_wrapper "$ws" --claude 2>&1) || rc=$?
  [[ "$rc" -ne 0 ]] || fail "expected failure when skills/create-pr is a real directory"
  echo "$err" | grep -qi 'not a symlink\|refusing\|real directory' \
    || fail "expected refusal message, got: $err"
  pass "refuses to replace a real skill directory"
}

test_prunes_removed_vendor_skill() {
  local ws
  ws=$(mktemp -d "${TMPDIR_ROOT}/prune.XXXXXX")
  seed_vendor "$ws"
  install_wrapper "$ws"
  mkdir -p "${ws}/.osac-ai-skills/skills/obsolete-skill"
  echo '# obsolete' >"${ws}/.osac-ai-skills/skills/obsolete-skill/SKILL.md"

  local out
  out=$(run_wrapper "$ws" --claude 2>&1) || fail "first prune wrapper failed: $out"
  [[ -L "${ws}/skills/obsolete-skill" ]] || fail "expected obsolete-skill link after first materialize"

  rm -rf "${ws}/.osac-ai-skills/skills/obsolete-skill"
  out=$(run_wrapper "$ws" --claude 2>&1) || fail "second prune wrapper failed: $out"
  [[ ! -e "${ws}/skills/obsolete-skill" && ! -L "${ws}/skills/obsolete-skill" ]] \
    || fail "expected obsolete-skill symlink to be pruned after vendor removal"
  [[ -L "${ws}/skills/create-pr" ]] || fail "create-pr should still be linked after prune"
  pass "prunes stale vendor skill symlinks"
}

test_prunes_after_vendor_switch() {
  local ws
  ws=$(mktemp -d "${TMPDIR_ROOT}/switch.XXXXXX")
  seed_vendor "$ws"
  install_wrapper "$ws"

  # A skill that only exists in the project-local vendor, absent from the
  # home vendor seeded below — reproduces resolution flipping between the
  # two candidate vendor roots across runs (e.g. ~/.osac-ai-skills appears
  # after an earlier run resolved to the project-local fallback).
  mkdir -p "${ws}/.osac-ai-skills/skills/old-only-skill"
  echo '# old-only-skill' >"${ws}/.osac-ai-skills/skills/old-only-skill/SKILL.md"

  run_wrapper "$ws" --claude >/dev/null || fail "initial (project-local vendor) wrapper run failed"
  [[ -L "${ws}/skills/old-only-skill" ]] || fail "expected old-only-skill link from project-local vendor"
  [[ -L "${ws}/skills/create-pr" ]] || fail "expected create-pr link from project-local vendor"

  local home_vendor="${ws}/home/.osac-ai-skills"
  mkdir -p "${home_vendor}/tools" "${home_vendor}/skills/create-pr"
  cp "$VENDOR_FANOUT" "${home_vendor}/tools/link-agent-skills.sh"
  chmod +x "${home_vendor}/tools/link-agent-skills.sh"
  echo '# stub create-pr (home vendor)' >"${home_vendor}/skills/create-pr/SKILL.md"
  seed_shared_canonicals "$home_vendor"

  run_wrapper "$ws" --claude >/dev/null || fail "second (home vendor) wrapper run failed"

  [[ ! -e "${ws}/skills/old-only-skill" && ! -L "${ws}/skills/old-only-skill" ]] \
    || fail "expected old-only-skill (project-local-only) to be pruned once resolution switched to the home vendor"
  grep -q "home vendor" "${ws}/skills/create-pr/SKILL.md" \
    || fail "expected create-pr to now resolve through the home vendor"
  pass "prunes symlinks left over from a prior, now-inactive vendor after resolution switches"
}

test_vendor_override_env_var_is_authoritative() {
  local ws
  ws=$(mktemp -d "${TMPDIR_ROOT}/override.XXXXXX")
  seed_vendor "$ws"
  install_wrapper "$ws"

  # Seed a second, distinguishable vendor at the home path — the wrapper's own
  # default resolution checks HOME first, so without the override it would
  # pick this one instead of the project-local vendor passed via the env var.
  # Reproduces the bug where tools/bootstrap.sh resolves/clones one vendor
  # dir but the wrapper it invokes independently re-resolves and silently
  # picks a different one (e.g. a content-valid-but-non-git ~/.osac-ai-skills
  # that bootstrap.sh already rejected for git updates).
  local home_vendor="${ws}/home/.osac-ai-skills"
  mkdir -p "${home_vendor}/tools" "${home_vendor}/skills/create-pr"
  cp "$VENDOR_FANOUT" "${home_vendor}/tools/link-agent-skills.sh"
  chmod +x "${home_vendor}/tools/link-agent-skills.sh"
  echo '# stub create-pr (home vendor — must NOT be used)' >"${home_vendor}/skills/create-pr/SKILL.md"

  local project_vendor="${ws}/.osac-ai-skills"
  (cd "$ws" && HOME="${ws}/home" OSAC_AI_SKILLS_VENDOR_DIR="${project_vendor}" \
    ./tools/link-agent-skills.sh --claude >/dev/null) \
    || fail "wrapper run with OSAC_AI_SKILLS_VENDOR_DIR override failed"

  grep -q "home vendor" "${ws}/skills/create-pr/SKILL.md" \
    && fail "override was ignored — wrapper used the home vendor instead of OSAC_AI_SKILLS_VENDOR_DIR"
  [[ -r "${ws}/skills/create-pr/SKILL.md" ]] || fail "expected create-pr to resolve via the overridden vendor"

  local rc=0
  local err
  err=$(cd "$ws" && HOME="${ws}/home" OSAC_AI_SKILLS_VENDOR_DIR="${ws}/no-such-vendor" \
    ./tools/link-agent-skills.sh --claude 2>&1) || rc=$?
  [[ "$rc" -ne 0 ]] || fail "expected failure for an invalid OSAC_AI_SKILLS_VENDOR_DIR (no silent fallback)"
  echo "$err" | grep -qi 'OSAC_AI_SKILLS_VENDOR_DIR' \
    || fail "expected error to reference OSAC_AI_SKILLS_VENDOR_DIR, got: $err"
  pass "OSAC_AI_SKILLS_VENDOR_DIR override is authoritative (no independent re-resolution, no silent fallback)"
}

test_promotes_legacy_real_umbrella_dirs() {
  local ws
  ws=$(mktemp -d "${TMPDIR_ROOT}/legacy-umbrella.XXXXXX")
  seed_vendor "$ws"
  install_wrapper "$ws"

  # origin/main install.sh shape: real directory, not a symlink.
  mkdir -p "${ws}/.claude/skills" "${ws}/.cursor/skills" "${ws}/.gemini/skills"
  echo "legacy workflow stand-in" >"${ws}/.claude/skills/bugfix"
  echo "legacy workflow stand-in" >"${ws}/.cursor/skills/bugfix"
  echo "legacy workflow stand-in" >"${ws}/.gemini/skills/bugfix"

  run_wrapper "$ws" --all >/dev/null \
    || fail "wrapper should convert origin/main real umbrella dirs, not refuse them"

  [[ -L "${ws}/.claude/skills" ]] || fail ".claude/skills should be a symlink after conversion"
  [[ -L "${ws}/.cursor/skills" ]] || fail ".cursor/skills should be a symlink after conversion"
  [[ -L "${ws}/.gemini/skills" ]] || fail ".gemini/skills should be a symlink after conversion"
  [[ -r "${ws}/.claude/skills/create-pr/SKILL.md" ]] \
    || fail "cannot read create-pr via converted .claude/skills umbrella"
  [[ -e "${ws}/.claude/skills/bugfix" ]] \
    && fail "legacy real-dir contents should not survive conversion to a symlink umbrella"
  pass "converts origin/main real .*/skills directories into symlink umbrellas"
}

test_verify_does_not_clear_legacy_umbrella_dirs() {
  local ws
  ws=$(mktemp -d "${TMPDIR_ROOT}/verify-legacy.XXXXXX")
  seed_vendor "$ws"
  install_wrapper "$ws"

  mkdir -p "${ws}/.claude/skills" "${ws}/.cursor/skills" "${ws}/.gemini/skills"
  echo "legacy workflow stand-in" >"${ws}/.claude/skills/bugfix"
  echo "legacy workflow stand-in" >"${ws}/.cursor/skills/bugfix"
  echo "legacy workflow stand-in" >"${ws}/.gemini/skills/bugfix"

  # --verify with no link flags is the fan-out read-only path. Cleanup must
  # not run even though real leftover umbrellas are present.
  run_wrapper "$ws" --verify >/dev/null 2>&1 || true

  [[ -d "${ws}/.claude/skills" && ! -L "${ws}/.claude/skills" ]] \
    || fail "--verify must not convert real .claude/skills"
  [[ -d "${ws}/.cursor/skills" && ! -L "${ws}/.cursor/skills" ]] \
    || fail "--verify must not convert real .cursor/skills"
  [[ -d "${ws}/.gemini/skills" && ! -L "${ws}/.gemini/skills" ]] \
    || fail "--verify must not convert real .gemini/skills"
  [[ -f "${ws}/.claude/skills/bugfix" ]] \
    || fail "--verify must not delete leftover files in real umbrella dirs"
  pass "--verify does not rm -rf unselected leftover real umbrella dirs"
}

test_targeted_run_preserves_unselected_legacy_umbrella_dirs() {
  local ws
  ws=$(mktemp -d "${TMPDIR_ROOT}/targeted-legacy.XXXXXX")
  seed_vendor "$ws"
  install_wrapper "$ws"

  mkdir -p "${ws}/.claude/skills" "${ws}/.cursor/skills" "${ws}/.gemini/skills"
  echo "legacy workflow stand-in" >"${ws}/.claude/skills/bugfix"
  echo "legacy workflow stand-in" >"${ws}/.cursor/skills/bugfix"
  echo "legacy workflow stand-in" >"${ws}/.gemini/skills/bugfix"

  run_wrapper "$ws" --cursor >/dev/null \
    || fail "targeted --cursor should convert the Cursor leftover umbrella"

  [[ -L "${ws}/.cursor/skills" ]] || fail ".cursor/skills should be a symlink after --cursor"
  [[ -d "${ws}/.claude/skills" && ! -L "${ws}/.claude/skills" ]] \
    || fail "--cursor must not convert real .claude/skills"
  [[ -d "${ws}/.gemini/skills" && ! -L "${ws}/.gemini/skills" ]] \
    || fail "--cursor must not convert real .gemini/skills"
  [[ -f "${ws}/.claude/skills/bugfix" ]] \
    || fail "--cursor must not delete leftover files in unselected umbrella dirs"
  pass "targeted --cursor preserves unselected leftover real umbrella dirs"
}

test_bootstrap_aborts_when_nested_in_workspace() {
  local nest="${TMPDIR_ROOT}/nested-ws"
  local empty_home="${TMPDIR_ROOT}/nested-ws-home"
  mkdir -p "${nest}/osac/tools" "${nest}/bin" "${empty_home}"
  printf '#!/bin/sh\necho workspace-bootstrap\n' > "${nest}/bootstrap.sh"
  chmod +x "${nest}/bootstrap.sh"
  cp "${REPO_ROOT}/tools/bootstrap.sh" "${nest}/osac/tools/bootstrap.sh"
  chmod +x "${nest}/osac/tools/bootstrap.sh"
  printf '#!/bin/sh\necho stub-git "$@"; exit 42\n' > "${nest}/bin/git"
  chmod +x "${nest}/bin/git"

  local out rc=0
  out=$(HOME="${empty_home}" bash "${nest}/osac/tools/bootstrap.sh" 2>&1) || rc=$?
  [[ "$rc" -eq 1 ]] || fail "nested bootstrap expected exit 1, got $rc: $out"
  echo "$out" | grep -q "inside osac-workspace" \
    || fail "nested abort message missing: $out"
  [[ ! -e "${nest}/osac/.osac-ai-skills" ]] \
    || fail "nested bootstrap must not clone a vendor before aborting"
  pass "bootstrap.sh aborts when nested inside a workspace-shaped parent"

  rc=0
  out=$(HOME="${empty_home}" PATH="${nest}/bin:${PATH}" \
    OSAC_ALLOW_NESTED_BOOTSTRAP=1 bash "${nest}/osac/tools/bootstrap.sh" 2>&1) || rc=$?
  echo "$out" | grep -q "inside osac-workspace" \
    && fail "override must skip the nested abort: $out"
  echo "$out" | grep -q "stub-git" \
    || fail "override should proceed to git (stub): $out"
  [[ "$rc" -eq 42 ]] || fail "override expected stub git exit 42, got $rc: $out"
  pass "OSAC_ALLOW_NESTED_BOOTSTRAP=1 skips the nested abort"
}

test_missing_vendor_fails
test_prunes_after_vendor_switch
test_vendor_override_env_var_is_authoritative
test_materialize_and_link
test_refuse_real_skill_directory
test_prunes_removed_vendor_skill
test_refuse_real_shared_rule_file
test_verify_shared_files_are_symlinks
test_promotes_legacy_real_umbrella_dirs
test_verify_does_not_clear_legacy_umbrella_dirs
test_targeted_run_preserves_unselected_legacy_umbrella_dirs
test_bootstrap_aborts_when_nested_in_workspace

echo "All link-agent-skills consumer smoke tests passed."
