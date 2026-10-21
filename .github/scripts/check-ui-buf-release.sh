#!/usr/bin/env bash
# Find the newest stable release shared by the UI's public and private BSR
# modules. The script only reports a release newer than the versions pinned in
# the generator config; it does not modify the checkout.
set -Eeuo pipefail

readonly VERSION_FILE="${UI_BUF_VERSION_FILE:-osac-ui/libs/types/.buf-api-version}"
readonly BUF_BIN="${BUF_BIN:-buf}"
readonly PUBLIC_MODULE="buf.build/osac-project/public-api"
readonly PRIVATE_MODULE="buf.build/osac-project/private-api"
readonly STABLE_SEMVER_RE='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'

fail() {
  echo "::error::$*" >&2
  exit 1
}

write_output() {
  local name="$1"
  local value="$2"

  if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
    printf '%s=%s\n' "$name" "$value" >> "$GITHUB_OUTPUT"
  else
    printf '%s=%s\n' "$name" "$value"
  fi
}

fetch_labels() {
  local module="$1"
  local output_file="$2"
  local page_token=""
  local response
  local next_page

  : > "$output_file"
  while :; do
    local -a args=(
      registry module label list "$module"
      --archive-status unarchived
      --format json
      --page-size 100
    )
    if [[ -n "$page_token" ]]; then
      args+=(--page-token "$page_token")
    fi

    if ! response="$($BUF_BIN "${args[@]}" 2>&1)"; then
      printf '%s\n' "$response" >&2
      fail "Buf could not list labels for ${module}"
    fi

    if ! jq -e '(.labels | type) == "array" and (.labels | all(.[]; (.name | type) == "string"))' \
      <<< "$response" >/dev/null; then
      printf '%s\n' "$response" >&2
      fail "Buf returned malformed label data for ${module}"
    fi

    if ! jq -r '.labels[].name' <<< "$response" >> "$output_file"; then
      fail "Could not read labels returned for ${module}"
    fi

    if ! next_page="$(jq -r '.next_page // empty' <<< "$response")"; then
      fail "Could not read the next-page token returned for ${module}"
    fi
    if [[ -z "$next_page" ]]; then
      break
    fi
    if [[ "$next_page" == "$page_token" ]]; then
      fail "Buf returned a repeated page token for ${module}"
    fi
    page_token="$next_page"
  done
}

[[ -f "$VERSION_FILE" ]] || fail "UI Buf version file not found: ${VERSION_FILE}"
command -v "$BUF_BIN" >/dev/null 2>&1 || fail "Buf executable not found: ${BUF_BIN}"
command -v jq >/dev/null 2>&1 || fail "jq is required to inspect BSR labels"

if ! mapfile -t version_lines < "$VERSION_FILE"; then
  fail "Could not read the UI Buf version file: ${VERSION_FILE}"
fi
[[ "${#version_lines[@]}" -eq 1 ]] || fail "Expected exactly one version in ${VERSION_FILE}"
current_version="${version_lines[0]}"
[[ "$current_version" =~ $STABLE_SEMVER_RE ]] || fail "Unsupported UI Buf version: ${current_version}"
temp_dir="$(mktemp -d)"
trap 'rm -rf "$temp_dir"' EXIT

fetch_labels "$PUBLIC_MODULE" "$temp_dir/public-labels"
fetch_labels "$PRIVATE_MODULE" "$temp_dir/private-labels"

filter_stable_versions() {
  local input_file="$1"
  local output_file="$2"
  local label

  : > "$output_file"
  while IFS= read -r label; do
    if [[ "$label" =~ $STABLE_SEMVER_RE ]]; then
      printf '%s\n' "$label" >> "$output_file"
    fi
  done < "$input_file"
  # comm below uses its default lexical ordering, so keep these files in that
  # same order. Apply version ordering only when selecting the newest value.
  sort -u "$output_file" -o "$output_file"
}

filter_stable_versions "$temp_dir/public-labels" "$temp_dir/public-versions"
filter_stable_versions "$temp_dir/private-labels" "$temp_dir/private-versions"

if ! comm -12 "$temp_dir/public-versions" "$temp_dir/private-versions" > "$temp_dir/common-versions"; then
  fail "Could not determine the versions shared by the UI's BSR modules"
fi
[[ -s "$temp_dir/common-versions" ]] || fail "No stable version is shared by the UI's BSR modules"

latest_version="$(sort -V "$temp_dir/common-versions" | tail -n 1)"
[[ "$latest_version" =~ $STABLE_SEMVER_RE ]] || fail "Invalid latest shared Buf version: ${latest_version}"

write_output current-version "$current_version"
write_output latest-version "$latest_version"
if [[ "$latest_version" != "$current_version" ]] && \
  [[ "$(printf '%s\n' "$current_version" "$latest_version" | sort -V | tail -n 1)" == "$latest_version" ]]; then
  write_output update-available true
  echo "A newer shared UI Buf release is available: ${current_version} -> ${latest_version}"
else
  write_output update-available false
  echo "The UI already uses the newest shared stable Buf release: ${current_version}"
fi
