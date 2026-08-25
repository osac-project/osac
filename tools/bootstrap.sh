#!/usr/bin/env bash
# Install AI workflow skills for this repo.
#
# Usage: tools/bootstrap.sh
#
# This script:
#   1. Clones or updates osac-project/osac-ai-skills (prefers ~/.osac-ai-skills)
#   2. Clones or updates flightctl/ai-workflows (prefers ~/.ai-workflows)
#   3. Materializes skills/ and links Claude/Cursor/Gemini discovery dirs
#      (umbrella symlinks must exist before ai-workflows install.sh, which
#      writes workflow links into those paths)
#   4. Installs workflows (bugfix, implement, prd, design, e2e)
#
# Re-run anytime to update to latest main.
set -euo pipefail

SCRIPT_DIR="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"
PROJECT_ROOT="$(realpath "${SCRIPT_DIR}/..")"

# Nested osac/ inside osac-workspace would install a second skill overlay
# (two PROJECT_ROOTs). Workspace ./bootstrap.sh already covers this clone.
# Temporary until osac-workspace is decommissioned.
# Override: OSAC_ALLOW_NESTED_BOOTSTRAP=1
if [[ "${OSAC_ALLOW_NESTED_BOOTSTRAP:-}" != "1" ]] \
   && [[ -x "${PROJECT_ROOT}/../bootstrap.sh" ]]; then
  nested_osac="$(realpath "${PROJECT_ROOT}/../osac" 2>/dev/null || true)"
  if [[ -n "${nested_osac}" && "${nested_osac}" == "${PROJECT_ROOT}" ]]; then
    echo "ERROR: this osac/ checkout is inside osac-workspace." >&2
    echo "Run the workspace ./bootstrap.sh instead (it already covers this clone)." >&2
    echo "To force this script anyway: OSAC_ALLOW_NESTED_BOOTSTRAP=1 $0" >&2
    exit 1
  fi
fi

OSAC_AI_SKILLS_REPO="osac-project/osac-ai-skills"

# Git-capable check — gates the git fetch/rebase below. tools/link-agent-skills.sh's
# resolve_osac_ai_skills_dir() intentionally uses a weaker, content-based check
# instead (skills/ + executable fan-out, no .git): it never runs git against the
# vendor dir, and staying content-based matches the reference osac-workspace
# wrapper and the upstream osac-ai-skills fan-out itself (neither checks .git),
# while leaving room for non-git vendoring mechanisms ADR 0001 Decision item 3
# still has open (git subtree / copy-bot) for automated-framework consumption.
osac_ai_skills_vendor_ok() {
  local dir="$1"
  [[ -d "${dir}/.git" ]] \
    && [[ -d "${dir}/skills" ]] \
    && [[ -x "${dir}/tools/link-agent-skills.sh" ]]
}

# Fetch + rebase a vendored git checkout onto origin/main. Warns and continues
# on failure rather than exiting — a stale vendor is recoverable manually, and
# this runs before any skill-discovery step that depends on it.
update_git_repo() {
  local dir="$1" label="$2"
  if ! (cd "${dir}" && git fetch origin -q); then
    echo "  Fetch failed for ${label}. Skipping update."
  elif ! (cd "${dir}" && git rebase origin/main --autostash -q); then
    (cd "${dir}" && git rebase --abort 2>/dev/null || true)
    echo "  Rebase failed for ${label}. Resolve manually: cd ${dir} && git rebase origin/main"
  else
    echo "  ${label} up to date"
  fi
}

# --- osac-ai-skills ---

if [[ -d "${HOME}/.osac-ai-skills" ]] && osac_ai_skills_vendor_ok "${HOME}/.osac-ai-skills"; then
  OSAC_AI_SKILLS_DIR="$(readlink -f "${HOME}/.osac-ai-skills")"
  echo "Updating osac-ai-skills (${OSAC_AI_SKILLS_DIR})..."
  update_git_repo "${OSAC_AI_SKILLS_DIR}" "osac-ai-skills"
elif [[ -d "${PROJECT_ROOT}/.osac-ai-skills" ]] && osac_ai_skills_vendor_ok "${PROJECT_ROOT}/.osac-ai-skills"; then
  OSAC_AI_SKILLS_DIR="${PROJECT_ROOT}/.osac-ai-skills"
  echo "Updating osac-ai-skills (.osac-ai-skills)..."
  update_git_repo "${OSAC_AI_SKILLS_DIR}" "osac-ai-skills"
elif [[ -d "${PROJECT_ROOT}/.osac-ai-skills" ]]; then
  echo "ERROR: ${PROJECT_ROOT}/.osac-ai-skills exists but is not a usable vendor checkout." >&2
  echo "Expected a git clone with skills/ and an executable tools/link-agent-skills.sh." >&2
  echo "Remove or rename that directory, then re-run tools/bootstrap.sh to clone a fresh copy." >&2
  exit 1
else
  if [[ -d "${HOME}/.osac-ai-skills" ]]; then
    echo "  ${HOME}/.osac-ai-skills exists but is not a usable vendor checkout; using ${PROJECT_ROOT}/.osac-ai-skills"
  fi
  OSAC_AI_SKILLS_DIR="${PROJECT_ROOT}/.osac-ai-skills"
  echo "Cloning osac-ai-skills..."
  git clone "https://github.com/${OSAC_AI_SKILLS_REPO}.git" "${OSAC_AI_SKILLS_DIR}"
fi

AI_WORKFLOWS_REPO="flightctl/ai-workflows"
AI_WORKFLOWS_DIR=""

# --- ai-workflows (flightctl) ---

if [[ -d "${HOME}/.ai-workflows" ]]; then
  AI_WORKFLOWS_DIR="$(readlink -f "${HOME}/.ai-workflows")"
  echo "Updating ai-workflows (${AI_WORKFLOWS_DIR})..."
  update_git_repo "${AI_WORKFLOWS_DIR}" "ai-workflows"
elif [[ -d "${PROJECT_ROOT}/.ai-workflows" ]]; then
  AI_WORKFLOWS_DIR="${PROJECT_ROOT}/.ai-workflows"
  echo "Updating ai-workflows (.ai-workflows)..."
  update_git_repo "${AI_WORKFLOWS_DIR}" "ai-workflows"
else
  AI_WORKFLOWS_DIR="${PROJECT_ROOT}/.ai-workflows"
  echo "Cloning ai-workflows..."
  git clone "https://github.com/${AI_WORKFLOWS_REPO}.git" "${AI_WORKFLOWS_DIR}"
fi

# Umbrella .*/skills -> ../skills must exist before install.sh, which mkdir -p's
# those paths and writes workflow symlinks into them (through the umbrellas).
#
# Export the vendor dir this script just resolved/updated/cloned above so the
# wrapper uses that exact directory instead of independently re-resolving one
# — the wrapper's resolve_osac_ai_skills_dir() uses a weaker, content-only
# check (see its own comment) and could otherwise pick a different, possibly
# stale, ~/.osac-ai-skills that this script already rejected for git updates.
echo "Linking agent skill directories..."
OSAC_AI_SKILLS_VENDOR_DIR="${OSAC_AI_SKILLS_DIR}" "${PROJECT_ROOT}/tools/link-agent-skills.sh"

echo "Installing ai-workflows skills..."
AI_WORKFLOWS="bugfix,implement,prd,design,e2e"
"${AI_WORKFLOWS_DIR}/install.sh" all --project "${PROJECT_ROOT}" --workflows "${AI_WORKFLOWS}"

echo ""
echo "AI workflows and OSAC skills installed."
