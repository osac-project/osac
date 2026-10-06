#!/usr/bin/env bash
# Build and load the arm64 replacement for the OpenShift CLI image used by the
# installer hooks. The image keeps the upstream tag so no chart values change
# is needed. Use kind-runtime.sh so Docker and Podman use the same provider for
# both the build and the Kind image load.

set -euo pipefail

cluster_name=${1:?usage: $0 <kind-cluster-name> [image]}
image=${2:-quay.io/openshift/origin-cli:4.20.0}
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
kind_runtime=${KIND_RUNTIME:-"${script_dir}/kind-runtime.sh"}
archive=$(mktemp "${TMPDIR:-/tmp}/osac-cli-image-XXXXXX.tar")
trap 'rm -f "${archive}"' EXIT

[[ -x "${kind_runtime}" ]] || {
  echo "kind runtime wrapper is not executable: ${kind_runtime}" >&2
  exit 1
}

echo "Building ${image} for linux/arm64..."
"${kind_runtime}" container-build-file "${script_dir}/Containerfile.arm64-cli" \
  --platform linux/arm64 -t "${image}"

"${kind_runtime}" container save "${image}" -o "${archive}"

echo "Loading ${image} into kind cluster ${cluster_name}..."
"${kind_runtime}" load image-archive "${archive}" --name "${cluster_name}"
