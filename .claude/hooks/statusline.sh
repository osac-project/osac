#!/bin/bash
# Project statusline: extends user statusline with osac, osac-ai-skills, and
# ai-workflows sync status.
# Opt out: set CLAUDE_REPO_STATUSLINE_DISABLED=1 in .claude/settings.local.json env
# to skip the repo sync status (the user's own statusline still runs)

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

# True only for the root of a clone or linked worktree. A leftover directory
# inside some other checkout would otherwise inherit that parent's branch.
is_git_work_tree_root() {
  local dir="$1"
  [[ -n "$dir" && -d "$dir" ]] \
    && git -C "$dir" rev-parse --is-inside-work-tree >/dev/null 2>&1 \
    && [[ -z "$(git -C "$dir" rev-parse --show-prefix 2>/dev/null)" ]]
}

repo_status() {
  local dir="$1" name="$2"
  if ! is_git_work_tree_root "$dir"; then
    log_muted "$name: not found"
    return
  fi

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

resolve_osac_ai_skills_dir() {
  local home_skills="${HOME}/.osac-ai-skills"
  local repo_skills="${REPO_DIR}/.osac-ai-skills"
  if is_git_work_tree_root "${home_skills}"; then
    printf '%s\n' "${home_skills}"
  else
    printf '%s\n' "${repo_skills}"
  fi
}

resolve_ai_workflows_dir() {
  local home_wf="${HOME}/.ai-workflows"
  local repo_wf="${REPO_DIR}/.ai-workflows"
  if is_git_work_tree_root "${home_wf}"; then
    printf '%s\n' "${home_wf}"
  else
    printf '%s\n' "${repo_wf}"
  fi
}

AI_DIR="$(resolve_ai_workflows_dir)"
SKILLS_DIR="$(resolve_osac_ai_skills_dir)"

ws=$(repo_status "$REPO_DIR" "osac")
sk=$(repo_status "$SKILLS_DIR" "osac-ai-skills")
ai=$(repo_status "$AI_DIR" "ai-workflows")

printf '%b %b %b %b %b\n' "$ws" "${GRAY}|${RESET}" "$sk" "${GRAY}|${RESET}" "$ai"
