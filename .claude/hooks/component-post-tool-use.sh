#!/usr/bin/env bash

set -euo pipefail

command -v jq >/dev/null 2>&1 || exit 0

input=$(cat)
cwd=$(jq -r '.cwd // empty' <<<"$input")
mapfile -t file_paths < <(
  jq -r '
    [
      (.tool_input.file_paths? // [] | if type == "array" then .[] else empty end),
      .tool_input.file_path?,
      .tool_input.filePath?,
      .tool_response.filePath?
    ]
    | map(select(type == "string" and length > 0))
    | unique[]
  ' <<<"$input"
)
[[ -n "$cwd" && ${#file_paths[@]} -gt 0 ]] || exit 0

repo_root=$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null) || exit 0

# Collapse paths into actions so a multi-file edit runs each generator once.
proto_changed=0
service_go_mod_changed=0
operator_go_mod_changed=0
operator_api_go_mod_changed=0
operator_types_changed=0
for file_path in "${file_paths[@]}"; do
  if [[ "$file_path" != /* ]]; then
    file_path="$cwd/$file_path"
  fi
  file_dir=$(cd -P -- "$(dirname -- "$file_path")" 2>/dev/null && pwd) || continue
  file_path="$file_dir/$(basename -- "$file_path")"
  case "$file_path" in
    "$repo_root"/*) relative_path=${file_path#"$repo_root"/} ;;
    *) continue ;;
  esac

  case "$relative_path" in
    proto/private/*.proto|proto/tests/*.proto) proto_changed=1 ;;
    fulfillment-service/go.mod) service_go_mod_changed=1 ;;
    osac-operator/go.mod) operator_go_mod_changed=1 ;;
    osac-operator/api/go.mod) operator_api_go_mod_changed=1 ;;
    osac-operator/api/v1alpha1/*_types.go) operator_types_changed=1 ;;
  esac
done

if ((proto_changed)); then
  make -C "$repo_root/proto" lint
  make -C "$repo_root/proto" generate
fi
if ((service_go_mod_changed)); then
  (cd "$repo_root/fulfillment-service" && go mod tidy)
fi
if ((operator_go_mod_changed)); then
  (cd "$repo_root/osac-operator" && go mod tidy)
fi
if ((operator_api_go_mod_changed)); then
  (cd "$repo_root/osac-operator/api" && go mod tidy)
fi
if ((operator_types_changed)); then
  make -C "$repo_root/osac-operator" manifests generate
fi
