#!/usr/bin/env bash
# Load one locally built image into the PROFILE=dev-full Kind cluster.
# Component Makefiles call this script so the runtime and kubeconfig details stay
# owned by the installer rather than being duplicated in each component.

set -euo pipefail

IMAGE="${1:?usage: kind-load-image.sh IMAGE}"
PLATFORM="${PLATFORM:?PLATFORM is required}"
PROFILE="${PROFILE:?PROFILE is required}"
NS="${NS:?NS is required}"
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-osac-dev}"
CONTAINER_TOOL="${CONTAINER_TOOL:-podman}"
INSTALLER_DIR="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null && pwd)"
KIND_RUNTIME="${INSTALLER_DIR}/scripts/dev-full/kind-runtime.sh"

if [[ "${PLATFORM}/${PROFILE}" != "kind/dev-full" ]]; then
  echo "ERROR: image loading requires PLATFORM=kind PROFILE=dev-full" >&2
  exit 1
fi

if [[ -z "${KUBECONFIG:-}" ]]; then
  KUBECONFIG="${HOME}/.kube/${KIND_CLUSTER_NAME}-kind-root.kubeconfig"
  export KUBECONFIG
fi

"${KIND_RUNTIME}" check
"${KIND_RUNTIME}" get clusters | grep -q "^${KIND_CLUSTER_NAME}$" || {
  echo "ERROR: Kind cluster '${KIND_CLUSTER_NAME}' does not exist; run make install-infra first" >&2
  exit 1
}

tmpfile="$(mktemp /tmp/kind-image-XXXXXX.tar)"
trap 'rm -f "${tmpfile}"' EXIT
echo "Loading ${IMAGE} into Kind cluster ${KIND_CLUSTER_NAME}..."
"${CONTAINER_TOOL}" save "${IMAGE}" > "${tmpfile}"
"${KIND_RUNTIME}" load image-archive "${tmpfile}" --name "${KIND_CLUSTER_NAME}"

if kubectl get namespace "${NS}" >/dev/null 2>&1; then
  for resource in deployment daemonset; do
    names="$(kubectl -n "${NS}" get "${resource}" -o json 2>/dev/null |
      jq -r --arg image "${IMAGE}" \
        '.items[] | select(any(.spec.template.spec.containers[]?; .image == $image)) | .metadata.name' || true)"
    while IFS= read -r name; do
      [[ -n "${name}" ]] || continue
      echo "Restarting ${resource}/${name} after loading ${IMAGE}..."
      kubectl -n "${NS}" rollout restart "${resource}/${name}"
    done <<< "${names}"
  done
else
  echo "Namespace ${NS} does not exist yet; the image is loaded for the next install."
fi
