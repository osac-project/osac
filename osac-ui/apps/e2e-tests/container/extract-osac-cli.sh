#!/usr/bin/env bash
# Extract the CLI from the specified locally built or digest-pinned image.
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "Usage: bash $0 SERVICE_IMAGE OUTPUT_FILE" >&2
  exit 2
fi
image=$1
output=$2
[[ -n "$image" && "$image" != -* && -n "$output" ]] || {
  echo "A valid image and output path are required." >&2
  exit 2
}
[[ ! -e "$output" && ! -L "$output" ]] || { echo "Output already exists: $output" >&2; exit 1; }

# A tag may identify a locally built CI image, but must never be pulled as a
# mutable remote reference. Pin the local image ID before creating a container.
if ! podman image exists "$image"; then
  [[ "$image" =~ @sha256:[[:xdigit:]]{64}$ ]] || {
    echo "Image is not local; a digest-qualified reference is required: $image" >&2
    exit 1
  }
  podman pull "$image" >&2
fi
image_id=$(podman image inspect "$image" --format '{{.Id}}')
image_id=${image_id#sha256:}
[[ "$image_id" =~ ^[[:xdigit:]]{64}$ ]] || {
  echo "Cannot determine immutable local image ID." >&2
  exit 1
}
image_id="sha256:$image_id"
temporary=$(mktemp -- "${output}.XXXXXX")
container=
cleanup() {
  [[ -z "$container" ]] || podman rm "$container" >/dev/null 2>&1 || true
  rm -f -- "$temporary"
}
trap cleanup EXIT
container=$(podman create --pull=never "$image_id")
podman cp "$container:/usr/local/bin/osac" "$temporary"
chmod 0755 "$temporary"
# Fail instead of replacing an existing path, even if it appeared during copy.
python3 -c 'import os, sys; os.link(sys.argv[1], sys.argv[2])' "$temporary" "$output"
printf 'Extracted osac CLI from %s (local image ID %s)\n' "$image" "$image_id"
