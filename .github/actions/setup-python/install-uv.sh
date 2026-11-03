#!/usr/bin/env bash
#
# Copyright (c) 2026 Red Hat Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
# in compliance with the License. You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software distributed under the License
# is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express
# or implied. See the License for the specific language governing permissions and limitations under
# the License.

set -euo pipefail
if [[ "$(uname -s):$(uname -m)" != Linux:x86_64 ]]; then
  echo "::error::uv bootstrap supports only Linux x86_64" >&2
  exit 1
fi

# Update the version and SHA-256 together from the official uv release assets.
# This direct bootstrap intentionally omits setup-uv's automatic cache management.
version=0.13.6
checksum=f29c3e94ffabd7e6f3ba521dba169a9b7ec12100bf3257087f0f24f6123f3645
target=uv-x86_64-unknown-linux-gnu
scratch=$(mktemp -d "$RUNNER_TEMP/osac-uv-download.XXXXXX")
trap 'rm -rf "$scratch"' EXIT
for attempt in 1 2; do
  if curl --disable --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
    --max-redirs 1 --max-time 15 \
    --output "$scratch/uv.tar.gz" \
    "https://github.com/astral-sh/uv/releases/download/$version/$target.tar.gz"; then
    break
  else
    status=$?
    if [[ "$attempt" == 2 ]]; then
      echo "::error::uv download failed after 2 attempts (curl exit $status)" >&2
      exit "$status"
    fi
    sleep 1
  fi
done
echo "$checksum  $scratch/uv.tar.gz" | sha256sum --check --strict -
tar -xzf "$scratch/uv.tar.gz" -C "$scratch" "$target/uv" "$target/uvx"
bin_dir=$(mktemp -d "$RUNNER_TEMP/osac-uv-bin.XXXXXX")
install -m 0755 "$scratch/$target/uv" "$scratch/$target/uvx" "$bin_dir/"
echo "$HOME/.local/bin" >> "$GITHUB_PATH"
echo "$bin_dir" >> "$GITHUB_PATH"
