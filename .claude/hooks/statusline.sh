#!/bin/bash
# Project statusline: extends user statusline with osac + ai-workflows sync status
# Opt out: set CLAUDE_REPO_STATUSLINE_DISABLED=1 in .claude/settings.local.json env
# to skip the osac + ai-workflows sync status (the user's own statusline still runs)

input=$(cat)

project_dir="${1:-}"

if command -v jq >/dev/null 2>&1 && [[ -f "${HOME}/.claude/settings.json" ]]; then
  USER_STATUSLINE_CMD=$(jq -r '.statusLine.command // empty' "${HOME}/.claude/settings.json" 2>/dev/null)
  if [[ -n "${USER_STATUSLINE_CMD}" ]]; then
    printf "%s\n" "$input" | bash -c "${USER_STATUSLINE_CMD}"
  fi
fi

if [[ "${CLAUDE_REPO_STATUSLINE_DISABLED:-}" == "1" ]]; then
  exit 0
fi

GREEN='\033[32m'
YELLOW='\033[33m'
GRAY='\033[90m'
RESET='\033[0m'

log_info() { printf '%b%s%b' "$GREEN" "$1" "$RESET"; }
log_warning() { printf '%b%s%b' "$YELLOW" "$1" "$RESET"; }
log_muted() { printf '%b%s%b' "$GRAY" "$1" "$RESET"; }

repo_status() {
  local dir="$1" name="$2"
  [[ -d "$dir" ]] || { log_muted "$name: not found"; return; }

  local branch behind
  branch=$(git -C "$dir" branch --show-current 2>/dev/null) || branch="detached"
  [[ -n "$branch" ]] || branch="detached"
  behind=$(git -C "$dir" rev-list HEAD..origin/main --count 2>/dev/null) || { log_muted "$name: $branch ?"; return; }

  if [[ "$behind" -eq 0 ]]; then
    log_info "$name: $branch ✓"
  else
    log_warning "$name: $branch ↓${behind} behind"
  fi
}

REPO_DIR="${project_dir:-$(printf '%s' "$input" | jq -r '.workspace.project_dir // empty' 2>/dev/null)}"
AI_DIR="${HOME}/.ai-workflows"

ws=$(repo_status "$REPO_DIR" "osac")
ai=$(repo_status "$AI_DIR" "ai-workflows")

printf '%b %b %b\n' "$ws" "${GRAY}|${RESET}" "$ai"
