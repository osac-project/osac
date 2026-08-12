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
exit 0
STUB
  chmod +x "$dest"
}

VENDOR_FANOUT=""
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
  echo "NOTE: using embedded fan-out stub (no real osac-ai-skills fan-out found)"
fi

# Isolate HOME so fixtures never pick up the developer's ~/.osac-ai-skills.
run_wrapper() {
  local ws="$1"
  shift
  mkdir -p "${ws}/home"
  (cd "$ws" && HOME="${ws}/home" ./tools/link-agent-skills.sh "$@")
}

seed_vendor() {
  local ws="$1"
  local vendor="${ws}/.osac-ai-skills"
  mkdir -p "${vendor}/.git" "${vendor}/tools" "${vendor}/skills"
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

test_non_git_vendor_rejected() {
  local ws
  ws=$(mktemp -d "${TMPDIR_ROOT}/nongit.XXXXXX")
  install_wrapper "$ws"
  local vendor="${ws}/.osac-ai-skills"
  mkdir -p "${vendor}/tools" "${vendor}/skills/create-pr"
  cp "$VENDOR_FANOUT" "${vendor}/tools/link-agent-skills.sh"
  chmod +x "${vendor}/tools/link-agent-skills.sh"
  echo "# stub" >"${vendor}/skills/create-pr/SKILL.md"
  # Deliberately no .git dir: mirrors a stale/non-clone vendor tree, which
  # bootstrap.sh's osac_ai_skills_vendor_ok() also rejects.

  local rc=0
  local err
  err=$(run_wrapper "$ws" --claude 2>&1) || rc=$?
  [[ "$rc" -ne 0 ]] || fail "expected failure when vendor dir has no .git"
  echo "$err" | grep -qi 'not found\|git clone' \
    || fail "expected vendor-not-found message, got: $err"
  pass "rejects a vendor dir without .git (matches bootstrap.sh validity check)"
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

test_missing_vendor_fails
test_non_git_vendor_rejected
test_materialize_and_link
test_refuse_real_skill_directory
test_prunes_removed_vendor_skill

echo "All link-agent-skills consumer smoke tests passed."
