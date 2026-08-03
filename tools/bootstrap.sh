#!/usr/bin/env bash
# Install AI workflow skills for this repo.
#
# Usage: tools/bootstrap.sh
#
# This script:
#   1. Clones or updates flightctl/ai-workflows (prefers ~/.ai-workflows)
#   2. Installs workflows (bugfix, implement, prd, design, e2e)
#
# Re-run anytime to update to latest main.
set -euo pipefail

SCRIPT_DIR="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"
PROJECT_ROOT="$(realpath "${SCRIPT_DIR}/..")"

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

echo "Installing ai-workflows skills..."
AI_WORKFLOWS="bugfix,implement,prd,design,e2e"
"${AI_WORKFLOWS_DIR}/install.sh" all --project "${PROJECT_ROOT}" --workflows "${AI_WORKFLOWS}"

echo ""
echo "AI workflows installed."
