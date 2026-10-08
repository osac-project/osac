#!/usr/bin/env bash
# Source from the repository root after calculating the caller's base ref.

changed() {
  [ -z "$base" ] && return 0
  # Both producers must finish: grep -q can close the pipe and turn SIGPIPE
  # into a false negative when this function is tested under pipefail.
  { git diff --name-only "$base" -- "$@"; git ls-files --others --exclude-standard -- "$@"; } | grep . >/dev/null
}
