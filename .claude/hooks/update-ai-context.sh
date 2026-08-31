#!/usr/bin/env bash
# SessionStart hook: fetch+rebase ai-workflows so the AI agent
# always has the latest skills and workflow phases.

# True only for the root of a clone or linked worktree. A leftover directory
# inside some other checkout would otherwise inherit that parent
# (git -C would fetch/rebase the enclosing repo).
is_git_work_tree_root() {
  local dir="$1"
  [[ -n "$dir" && -d "$dir" ]] \
    && git -C "$dir" rev-parse --is-inside-work-tree >/dev/null 2>&1 \
    && [[ -z "$(git -C "$dir" rev-parse --show-prefix 2>/dev/null)" ]]
}

fetch_and_rebase() {
  local dir="$1" name="$2"
  is_git_work_tree_root "$dir" || return 0

  local branch
  branch=$(git -C "$dir" symbolic-ref --short HEAD 2>/dev/null || echo "")
  if [[ "$branch" != "main" ]]; then
    echo "$name: on ${branch:-unknown}, skipped"
    return 0
  fi

  if ! git -C "$dir" fetch origin -q 2>/dev/null; then
    echo "$name: fetch failed"
    return 0
  fi

  local head_before head_after
  head_before="$(git -C "$dir" rev-parse HEAD)"

  trap 'git -C "$dir" rebase --abort 2>/dev/null || true' INT TERM

  if git -C "$dir" rebase origin/main --autostash -q >/dev/null 2>&1; then
    head_after="$(git -C "$dir" rev-parse HEAD)"
    if [[ "$head_before" == "$head_after" ]]; then
      echo "$name: up to date"
    else
      echo "$name: updated"
    fi
  else
    git -C "$dir" rebase --abort 2>/dev/null || true
    echo "$name: rebase conflict, skipped"
  fi
  trap - INT TERM
}

home_wf="${HOME}/.ai-workflows"
if is_git_work_tree_root "$home_wf"; then
  fetch_and_rebase "$home_wf" "ai-workflows"
elif [[ -n "${CLAUDE_PROJECT_DIR:-}" ]]; then
  proj_wf="${CLAUDE_PROJECT_DIR}/.ai-workflows"
  if is_git_work_tree_root "$proj_wf"; then
    fetch_and_rebase "$proj_wf" "ai-workflows"
  fi
fi
