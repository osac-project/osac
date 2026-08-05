#!/usr/bin/env bash
# Detect floating image tags (:latest, :main) in Helm values files that are
# not marked with a PLACEHOLDER comment. Exit 1 if any unmarked floating tags
# are found.
set -euo pipefail

if [ ! -d charts/ ]; then
  echo "::error::charts/ directory not found -- cannot validate image tags"
  exit 1
fi

rc=0
while IFS= read -r -d '' f; do
  lineno=0
  prev_line=""
  while IFS= read -r line || [ -n "$line" ]; do
    lineno=$((lineno + 1))
    code_line="$(printf '%s\n' "$line" | sed -E 's/[[:space:]]+#.*$//')"
    if printf '%s\n' "$code_line" | grep -qE "(^|[[:space:]])tag:[[:space:]]*['\"]?(latest|main)['\"]?([]},[:space:]]|$)|:(latest|main)['\"]?([]},[:space:]]|$)" && \
       printf '%s\n' "$code_line" | grep -qvE '^[[:space:]]*#'; then
      if printf '%s\n' "$prev_line" | grep -qE '(^|[[:space:]])#[[:space:]]PLACEHOLDER -- overwritten at release time([[:space:]]|$)' || \
         printf '%s\n' "$line" | grep -qE '(^|[[:space:]])#[[:space:]]PLACEHOLDER -- overwritten at release time([[:space:]]|$)'; then
        : # properly documented
      else
        tag_match="$(printf '%s\n' "$code_line" | grep -oE '\b(latest|main)\b' | head -1)"
        echo "$f:$lineno: unmarked floating tag ':${tag_match}' found"
        rc=1
      fi
    fi
    prev_line="$line"
  done < "$f"
done < <(find charts/ -name 'values.yaml' -print0)

if [ "$rc" -eq 0 ]; then
  echo "No unmarked floating image tags found."
fi
exit "$rc"
