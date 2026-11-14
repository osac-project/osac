#!/usr/bin/env bash
# Prepare the Mac dev-full guest once using the container build cache, then
# preload every Kind node for CDI's node pull method (IfNotPresent).
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "${script_dir}/../lib.sh"
kind_runtime=${KIND_RUNTIME:-"${script_dir}/kind-runtime.sh"}
cluster_name=${1:?usage: $0 <kind-cluster-name> <image>}
image=${2:?usage: $0 <kind-cluster-name> <image>}
archive=$(mktemp "${TMPDIR:-/tmp}/osac-fedora-image-XXXXXX.tar")
trap 'rm -f "${archive}"' EXIT

echo "Preparing the Fedora guest disk for Mac dev-full..."
"${kind_runtime}" container-build-file "${script_dir}/Containerfile.fedora-arm64" \
  --platform linux/arm64 -t "${image}"
"${kind_runtime}" container save "${image}" -o "${archive}"
echo "Loading the prepared Fedora image into Kind cluster ${cluster_name}..."
"${kind_runtime}" load image-archive "${archive}" --name "${cluster_name}"
