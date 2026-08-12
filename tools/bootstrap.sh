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

OSAC_AI_SKILLS_REPO="osac-project/osac-ai-skills"
OSAC_AI_SKILLS_DIR=""

osac_ai_skills_vendor_ok() {
  local dir="$1"
  [[ -d "${dir}/.git" ]] \
    && [[ -d "${dir}/skills" ]] \
    && [[ -x "${dir}/tools/link-agent-skills.sh" ]]
}

# --- osac-ai-skills ---

if [[ -d "${HOME}/.osac-ai-skills" ]] && osac_ai_skills_vendor_ok "${HOME}/.osac-ai-skills"; then
  OSAC_AI_SKILLS_DIR="$(readlink -f "${HOME}/.osac-ai-skills")"
  echo "Updating osac-ai-skills (${OSAC_AI_SKILLS_DIR})..."
  if ! (cd "$OSAC_AI_SKILLS_DIR" && git fetch origin -q); then
    echo "  Fetch failed for osac-ai-skills. Skipping update."
  elif ! (cd "$OSAC_AI_SKILLS_DIR" && git rebase origin/main --autostash -q); then
    (cd "$OSAC_AI_SKILLS_DIR" && git rebase --abort 2>/dev/null || true)
    echo "  Rebase failed for osac-ai-skills. Resolve manually: cd $OSAC_AI_SKILLS_DIR && git rebase origin/main"
  else
    echo "  osac-ai-skills up to date"
  fi
elif [[ -d "${PROJECT_ROOT}/.osac-ai-skills" ]] && osac_ai_skills_vendor_ok "${PROJECT_ROOT}/.osac-ai-skills"; then
  OSAC_AI_SKILLS_DIR="${PROJECT_ROOT}/.osac-ai-skills"
  echo "Updating osac-ai-skills (.osac-ai-skills)..."
  if ! (cd "$OSAC_AI_SKILLS_DIR" && git fetch origin -q); then
    echo "  Fetch failed for osac-ai-skills. Skipping update."
  elif ! (cd "$OSAC_AI_SKILLS_DIR" && git rebase origin/main --autostash -q); then
    (cd "$OSAC_AI_SKILLS_DIR" && git rebase --abort 2>/dev/null || true)
    echo "  Rebase failed for osac-ai-skills. Resolve manually: cd $OSAC_AI_SKILLS_DIR && git rebase origin/main"
  else
    echo "  osac-ai-skills up to date"
  fi
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
  if ! (cd "$AI_WORKFLOWS_DIR" && git fetch origin -q); then
    echo "  Fetch failed for ai-workflows. Skipping update."
  elif ! (cd "$AI_WORKFLOWS_DIR" && git rebase origin/main --autostash -q); then
    (cd "$AI_WORKFLOWS_DIR" && git rebase --abort 2>/dev/null || true)
    echo "  Rebase failed for ai-workflows. Resolve manually: cd $AI_WORKFLOWS_DIR && git rebase origin/main"
  else
    echo "  ai-workflows up to date"
  fi
elif [[ -d "${PROJECT_ROOT}/.ai-workflows" ]]; then
  AI_WORKFLOWS_DIR="${PROJECT_ROOT}/.ai-workflows"
  echo "Updating ai-workflows (.ai-workflows)..."
  if ! (cd "$AI_WORKFLOWS_DIR" && git fetch origin -q); then
    echo "  Fetch failed. Skipping update."
  elif ! (cd "$AI_WORKFLOWS_DIR" && git rebase origin/main --autostash -q); then
    (cd "$AI_WORKFLOWS_DIR" && git rebase --abort 2>/dev/null || true)
    echo "  Rebase failed. Resolve manually: cd $AI_WORKFLOWS_DIR && git rebase origin/main"
  else
    echo "  ai-workflows up to date"
  fi
else
  AI_WORKFLOWS_DIR="${PROJECT_ROOT}/.ai-workflows"
  echo "Cloning ai-workflows..."
  git clone "https://github.com/${AI_WORKFLOWS_REPO}.git" "${AI_WORKFLOWS_DIR}"
fi

# Umbrella .*/skills -> ../skills must exist before install.sh, which mkdir -p's
# those paths and writes workflow symlinks into them (through the umbrellas).
echo "Linking agent skill directories..."
"${PROJECT_ROOT}/tools/link-agent-skills.sh"

echo "Installing ai-workflows skills..."
AI_WORKFLOWS="bugfix,implement,prd,design,e2e"
"${AI_WORKFLOWS_DIR}/install.sh" all --project "${PROJECT_ROOT}" --workflows "${AI_WORKFLOWS}"

echo ""
echo "AI workflows and OSAC skills installed."
