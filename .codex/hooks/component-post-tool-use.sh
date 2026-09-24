#!/usr/bin/env bash

set -euo pipefail

command -v jq >/dev/null 2>&1 || exit 0

input=$(cat)
cwd=$(jq -r '.cwd // empty' <<<"$input")
tool_name=$(jq -r '.tool_name // empty' <<<"$input")
patch=$(jq -r '.tool_input.command // empty' <<<"$input")
[[ "$tool_name" == "apply_patch" && -n "$cwd" && -n "$patch" ]] || exit 0

# Extract file paths from Codex apply_patch commands without evaluating patch text.
file_paths=$(jq -cn --arg patch "$patch" '
  [
    $patch
    | split("\n")[]
    | sub("\r$"; "")
    | if startswith("*** Update File: ") then ltrimstr("*** Update File: ")
      elif startswith("*** Add File: ") then ltrimstr("*** Add File: ")
      elif startswith("*** Delete File: ") then ltrimstr("*** Delete File: ")
      elif startswith("*** Move to: ") then ltrimstr("*** Move to: ")
      else empty
      end
  ]
  | map(select(length > 0))
  | unique
')
[[ $(jq 'length' <<<"$file_paths") -gt 0 ]] || exit 0

script_dir=$(cd -P -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -P -- "$script_dir/../.." && pwd)
event_repo_root=$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null) || exit 0
[[ "$event_repo_root" == "$repo_root" ]] || exit 0
normalized_input=$(jq -cn --arg cwd "$cwd" --argjson file_paths "$file_paths" \
  '{cwd:$cwd,tool_input:{file_paths:$file_paths}}')
printf '%s\n' "$normalized_input" \
  | bash "$repo_root/.claude/hooks/component-post-tool-use.sh"
