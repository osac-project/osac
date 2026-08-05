#!/usr/bin/env bash
# SessionStart hook: fetch+rebase ai-workflows so the AI agent
# always has the latest skills and workflow phases.

fetch_and_rebase() {
  local dir="$1" name="$2"
  [[ -d "$dir" ]] || return 0

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

fetch_and_rebase "${HOME}/.ai-workflows" "ai-workflows"
