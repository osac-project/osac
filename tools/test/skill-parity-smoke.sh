#!/usr/bin/env bash
# Smoke test: osac/ skill parity for a standalone osac/-only clone.
# Run from osac/: bash tools/test/skill-parity-smoke.sh
#
# Validates that a developer working from a fresh, standalone osac/ clone — no
# osac-workspace sibling and no ~/.osac-ai-skills — gets full skill parity with
# the old osac-workspace experience across Claude Code, Cursor, and Gemini CLI:
#   - create-pr / osac-release resolve resolve-remotes.sh from ./.osac-ai-skills
#   - jira-task-management resolves and sources jira-safe-create.sh from ./.osac-ai-skills
#   - all three harness umbrellas (.claude/.cursor/.gemini) resolve the full
#     skill inventory (OSAC skills + ai-workflow skills)
#   - link-agent-skills.sh --verify passes
#
# HOME is pinned to an empty dir for every fixture so resolution can never fall
# back to a real ~/.osac-ai-skills or an osac-workspace sibling — the dual-path
# lookup is forced onto the repo-local ./.osac-ai-skills, exactly the standalone
# path OSAC-4005 fixed. Prefers the real vendored tools when present; the
# harness-discovery checks embed a minimal fan-out stub so a clean checkout / CI
# can run without bootstrap. The OSAC-4005 resolution checks require the real
# vendored tools and SKIP (loudly) when they are absent.
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
WRAPPER="${REPO_ROOT}/tools/link-agent-skills.sh"

# OSAC-native skills fanned out by osac-ai-skills (kept in sync with
# link-agent-skills-consumer-smoke.sh's inventory).
OSAC_SKILL_NAMES=(
  browser-demo-recording capture-tasks-from-meeting-notes create-pr
  design-review generate-status-report github-actions-workflows jira-task-management
  milestone-scope osac-cluster osac-demo-recording osac-feature osac-release
  performance-review prd-review presentation quick-fix report-bug review-gate
  security-review
)
# ai-workflow skills linked under skills/ via --with-ai-workflows.
WORKFLOW_SKILL_NAMES=(bugfix design e2e implement prd)

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }
skip() { echo "SKIP: $*"; }

[[ -f "$WRAPPER" ]] || fail "missing $WRAPPER"
[[ -x "$WRAPPER" ]] || fail "$WRAPPER is not executable"

TMPDIR_ROOT=$(mktemp -d)
trap 'rm -rf "$TMPDIR_ROOT"' EXIT

# Locate a real vendored tool (resolve-remotes.sh / jira-safe-create.sh /
# link-agent-skills.sh). Prefer the repo-local vendor a fresh osac/ bootstrap
# produces; fall back to the developer's home vendor. Echoes an absolute path or
# nothing.
find_real_tool() {
  local tool="$1" cand
  for cand in "${REPO_ROOT}/.osac-ai-skills" "${HOME}/.osac-ai-skills"; do
    if [[ -f "${cand}/tools/${tool}" ]]; then
      echo "$(cd "${cand}/tools" && pwd)/${tool}"
      return 0
    fi
  done
  return 0
}

# Minimal fan-out stub: enough for the harness-discovery parity check (umbrellas,
# ai-workflows, verify) when no real osac-ai-skills fan-out is present.
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
link_agent() { mkdir -p "$1"; safe_symlink "$1/skills" ../skills; }
if [[ "${VERIFY}" == true ]]; then
  errors=0
  for pair in ".claude:Claude" ".cursor:Cursor" ".gemini:Gemini"; do
    dir="${PROJECT_ROOT}/${pair%%:*}"; label="${pair##*:}"
    if [[ ! -L "${dir}/skills" ]]; then
      echo "ERROR: ${label}: ${dir}/skills is not a symlink" >&2; errors=1; continue
    fi
    if [[ ! -r "${dir}/skills/create-pr/SKILL.md" ]]; then
      echo "ERROR: ${label}: cannot read create-pr via ${dir}/skills" >&2; errors=1
    fi
  done
  [[ "${errors}" -eq 0 ]] || { echo "Verification failed." >&2; exit 1; }
  echo "Verification passed."
  exit 0
fi
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
    done
  fi
fi
[[ "${LINK_CLAUDE}" == true ]] && link_agent "${PROJECT_ROOT}/.claude"
[[ "${LINK_CURSOR}" == true ]] && link_agent "${PROJECT_ROOT}/.cursor"
[[ "${LINK_GEMINI}" == true ]] && link_agent "${PROJECT_ROOT}/.gemini"
exit 0
STUB
  chmod +x "$dest"
}

# Seed a standalone osac/-only clone fixture: a git repo with upstream+fork
# remotes, a repo-local .osac-ai-skills vendor (real tools when available,
# stub fan-out otherwise), the consumer wrapper, and stub ai-workflow skills.
# Echoes "real" or "stub" to indicate which fan-out backs the vendor.
seed_standalone_clone() {
  local ws="$1"
  local vendor="${ws}/.osac-ai-skills"
  mkdir -p "${ws}/home" "${ws}/tools" "${vendor}/tools" "${vendor}/skills"

  # Standalone git repo with the remote shape resolve-remotes.sh expects.
  git -C "$ws" init -q
  git -C "$ws" remote add upstream https://github.com/osac-project/osac.git
  git -C "$ws" remote add fork https://github.com/test-developer/osac.git

  # Consumer wrapper under test.
  cp "$WRAPPER" "${ws}/tools/link-agent-skills.sh"
  chmod +x "${ws}/tools/link-agent-skills.sh"

  # Real vendored fan-out when present, else embedded stub.
  local real_fanout backing="stub"
  real_fanout=$(find_real_tool link-agent-skills.sh)
  if [[ -n "$real_fanout" ]] && grep -q 'PROJECT_ROOT:-' "$real_fanout" 2>/dev/null; then
    cp "$real_fanout" "${vendor}/tools/link-agent-skills.sh"
    backing="real"
  else
    write_stub_fanout "${vendor}/tools/link-agent-skills.sh"
  fi
  chmod +x "${vendor}/tools/link-agent-skills.sh"

  # Copy the real shared tools when available so the resolution checks exercise
  # the genuine scripts (SKIPed otherwise).
  local t src
  for t in resolve-remotes.sh jira-safe-create.sh; do
    src=$(find_real_tool "$t")
    [[ -n "$src" ]] && cp "$src" "${vendor}/tools/${t}"
  done

  # Skill inventory: mirror the full real vendored skills/ tree when available
  # (symlink every skill so the wrapper's own OSAC_SKILLS verify list is
  # satisfied without this test tracking a duplicate list). Otherwise stub the
  # representative names in OSAC_SKILL_NAMES.
  local real_skills="" name src_skill
  for src in "${REPO_ROOT}/.osac-ai-skills/skills" "${HOME}/.osac-ai-skills/skills"; do
    if [[ -d "${src}/create-pr" ]]; then real_skills=$(cd "$src" && pwd -P); break; fi
  done
  if [[ -n "$real_skills" ]]; then
    for src_skill in "${real_skills}"/*/; do
      name=$(basename "$src_skill")
      ln -sfn "${real_skills}/${name}" "${vendor}/skills/${name}"
    done
  else
    for name in "${OSAC_SKILL_NAMES[@]}"; do
      mkdir -p "${vendor}/skills/${name}"
      echo "# stub ${name}" >"${vendor}/skills/${name}/SKILL.md"
    done
  fi

  # Shared canonicals the real wrapper materializes and --verify checks
  # (rules/agents/hooks/design context/templates). Copy from the real vendor
  # root when the fan-out is real; the stub fan-out does not materialize these.
  if [[ "$backing" == "real" && -n "$real_skills" ]]; then
    local vendor_root rel
    vendor_root=$(cd "${real_skills}/.." && pwd -P)
    for rel in .claude/rules .claude/agents .claude/hooks \
      .design/context .design/templates .prd/templates; do
      if [[ -d "${vendor_root}/${rel}" ]]; then
        mkdir -p "${vendor}/${rel}"
        cp -R "${vendor_root}/${rel}/." "${vendor}/${rel}/"
      fi
    done
  fi

  # Stub ai-workflow skills so --with-ai-workflows has something to link.
  local wf
  for wf in _shared "${WORKFLOW_SKILL_NAMES[@]}"; do
    mkdir -p "${ws}/.ai-workflows/${wf}"
    [[ "$wf" == "_shared" ]] || echo "# stub ${wf}" >"${ws}/.ai-workflows/${wf}/SKILL.md"
  done

  echo "$backing"
}

# The create-pr / osac-release resolution snippet, verbatim from
# skills/create-pr/references/resolve-remotes.md, run against $REPO_DIR.
# Prints the resolved vendor dir on success.
run_resolve_remotes_snippet() {
  local REPO_DIR="$1"
  OSAC_AI_SKILLS_DIR=""
  local _cand
  for _cand in "${HOME}/.osac-ai-skills" "${REPO_DIR}/.osac-ai-skills"; do
    if [[ -x "${_cand}/tools/resolve-remotes.sh" ]]; then
      OSAC_AI_SKILLS_DIR="${_cand}"; break
    fi
  done
  [[ -n "$OSAC_AI_SKILLS_DIR" ]] || { echo "resolve-remotes.sh not found" >&2; return 1; }
  local _resolve_out
  _resolve_out=$("${OSAC_AI_SKILLS_DIR}/tools/resolve-remotes.sh" "$REPO_DIR") || return 1
  eval "$_resolve_out"
  [[ -n "${UPSTREAM_REMOTE:-}" ]] || { echo "UPSTREAM_REMOTE not set" >&2; return 1; }
  echo "$OSAC_AI_SKILLS_DIR"
}

# The jira-task-management resolution snippet, verbatim from
# skills/jira-task-management/references/resolve-jira-safe-create.md.
# Sources jira-safe-create.sh and confirms it defined its functions.
run_jira_safe_create_snippet() {
  local REPO_DIR="$1"
  local _jsc="" _cand
  for _cand in "${HOME}/.osac-ai-skills" "${REPO_DIR}/.osac-ai-skills"; do
    [[ -f "${_cand}/tools/jira-safe-create.sh" ]] && { _jsc="${_cand}/tools/jira-safe-create.sh"; break; }
  done
  [[ -n "$_jsc" ]] || { echo "jira-safe-create.sh not found" >&2; return 1; }
  # shellcheck disable=SC1090
  source "$_jsc"
  [[ "${JIRA_SAFE_CREATE_LOADED:-}" == "1" ]] || { echo "source did not set JIRA_SAFE_CREATE_LOADED" >&2; return 1; }
  declare -F new_temp >/dev/null || { echo "new_temp not defined" >&2; return 1; }
  declare -F jira_login >/dev/null || { echo "jira_login not defined" >&2; return 1; }
  echo "${_jsc%/tools/jira-safe-create.sh}"
}

test_repo_local_vendor_present() {
  if [[ ! -f "${REPO_ROOT}/.osac-ai-skills/tools/link-agent-skills.sh" ]]; then
    skip "repo-local .osac-ai-skills absent (run tools/bootstrap.sh) — standalone-clone precondition unverified"
    return 0
  fi
  # A standalone osac/ clone must not be nested inside an osac-workspace parent,
  # which bootstrap.sh refuses. Assert the guard would not trip for this clone.
  [[ ! -f "${REPO_ROOT}/../bootstrap.sh" || ! -d "${REPO_ROOT}/../osac" ]] \
    || fail "REPO_ROOT looks nested inside an osac-workspace-shaped parent"
  pass "standalone osac/ clone precondition holds (repo-local vendor, no workspace nesting)"
}

test_standalone_remote_resolution() {
  local ws backing
  ws=$(mktemp -d "${TMPDIR_ROOT}/resolve.XXXXXX")
  backing=$(seed_standalone_clone "$ws")
  if [[ ! -x "${ws}/.osac-ai-skills/tools/resolve-remotes.sh" ]]; then
    skip "real resolve-remotes.sh unavailable (backing=${backing}) — OSAC-4005 remote-resolution path"
    return 0
  fi
  local resolved
  # Empty HOME forces the dual-path lookup onto ./.osac-ai-skills (standalone).
  resolved=$(cd "$ws" && HOME="${ws}/home" bash -c "$(declare -f run_resolve_remotes_snippet); run_resolve_remotes_snippet '$ws'") \
    || fail "create-pr/osac-release resolve-remotes snippet failed in a standalone clone"
  [[ "$resolved" == "${ws}/.osac-ai-skills" ]] \
    || fail "resolve-remotes.sh resolved to '${resolved}', expected the repo-local ${ws}/.osac-ai-skills (standalone fallback)"
  pass "create-pr/osac-release resolve resolve-remotes.sh from ./.osac-ai-skills (OSAC-4005)"
}

test_standalone_jira_safe_create_resolution() {
  local ws backing
  ws=$(mktemp -d "${TMPDIR_ROOT}/jsc.XXXXXX")
  backing=$(seed_standalone_clone "$ws")
  if [[ ! -f "${ws}/.osac-ai-skills/tools/jira-safe-create.sh" ]]; then
    skip "real jira-safe-create.sh unavailable (backing=${backing}) — OSAC-4005 jira-credential path"
    return 0
  fi
  local resolved
  resolved=$(cd "$ws" && HOME="${ws}/home" bash -c "$(declare -f run_jira_safe_create_snippet); run_jira_safe_create_snippet '$ws'") \
    || fail "jira-task-management jira-safe-create snippet failed in a standalone clone"
  [[ "$resolved" == "${ws}/.osac-ai-skills" ]] \
    || fail "jira-safe-create.sh resolved to '${resolved}', expected the repo-local ${ws}/.osac-ai-skills (standalone fallback)"
  pass "jira-task-management resolves and sources jira-safe-create.sh from ./.osac-ai-skills (OSAC-4005)"
}

test_all_harness_discovery_parity() {
  local ws
  ws=$(mktemp -d "${TMPDIR_ROOT}/harness.XXXXXX")
  seed_standalone_clone "$ws" >/dev/null

  (cd "$ws" && HOME="${ws}/home" ./tools/link-agent-skills.sh --all --with-ai-workflows >/dev/null) \
    || fail "wrapper --all --with-ai-workflows failed in a standalone clone"

  local harness skill
  for harness in .claude .cursor .gemini; do
    [[ -L "${ws}/${harness}/skills" ]] \
      || fail "${harness}/skills is not a symlink — ${harness#.} harness has no skill discovery"
    for skill in create-pr jira-task-management osac-release; do
      [[ -r "${ws}/${harness}/skills/${skill}/SKILL.md" ]] \
        || fail "cannot read ${skill} via ${harness}/skills — parity gap for ${harness#.}"
    done
  done

  # Every vendored OSAC skill resolves via each harness umbrella (not just the
  # canonical skills/ tree) — the actual cross-harness parity claim.
  local seeded
  for seeded in "${ws}"/.osac-ai-skills/skills/*/; do
    skill=$(basename "$seeded")
    for harness in .claude .cursor .gemini; do
      [[ -r "${ws}/${harness}/skills/${skill}/SKILL.md" ]] \
        || fail "OSAC skill '${skill}' does not resolve via ${harness}/skills — parity gap for ${harness#.}"
    done
  done
  # ai-workflow skills resolve when linked with --with-ai-workflows.
  for skill in "${WORKFLOW_SKILL_NAMES[@]}"; do
    [[ -r "${ws}/skills/${skill}/SKILL.md" ]] \
      || fail "ai-workflow skill '${skill}' does not resolve under skills/"
  done
  pass "all three harnesses (Claude/Cursor/Gemini) resolve the full skill inventory"
}

test_verify_passes_in_standalone_clone() {
  local ws out
  ws=$(mktemp -d "${TMPDIR_ROOT}/verify.XXXXXX")
  seed_standalone_clone "$ws" >/dev/null
  (cd "$ws" && HOME="${ws}/home" ./tools/link-agent-skills.sh --all >/dev/null) \
    || fail "clean wrapper run failed before --verify"
  out=$(cd "$ws" && HOME="${ws}/home" ./tools/link-agent-skills.sh --verify 2>&1) \
    || fail "link-agent-skills.sh --verify failed in a standalone clone: ${out}"
  echo "$out" | grep -qi 'Verification passed' \
    || fail "expected 'Verification passed', got: ${out}"
  pass "link-agent-skills.sh --verify passes in a standalone clone"
}

test_repo_local_vendor_present
test_standalone_remote_resolution
test_standalone_jira_safe_create_resolution
test_all_harness_discovery_parity
test_verify_passes_in_standalone_clone

echo "All skill parity smoke tests passed."
