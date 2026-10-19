#!/usr/bin/env bash
set -euo pipefail

minimum_major=1
minimum_minor=36

if ! server_version=$(kubectl version -o json 2>/dev/null \
  | jq -r '.serverVersion.gitVersion // empty') \
  || [[ -z "${server_version}" ]]; then
  echo "ERROR: could not read the Kubernetes API server version. Ensure the dev-full Kind API server is ready and KUBECONFIG selects it." >&2
  exit 1
fi

if [[ ! "${server_version}" =~ ^v?([0-9]+)\.([0-9]+)\.([0-9]+)([-+][0-9A-Za-z.-]+)?$ ]]; then
  echo "ERROR: the Kubernetes API server returned an unrecognized version '${server_version}'." >&2
  exit 1
fi

server_major=$((10#${BASH_REMATCH[1]}))
server_minor=$((10#${BASH_REMATCH[2]}))
if (( server_major < minimum_major || (server_major == minimum_major && server_minor < minimum_minor) )); then
  echo "ERROR: dev-full requires a Kubernetes API server v1.36 or later for the admission policy; this server reports ${server_version}." >&2
  echo "Upgrade this Kind cluster or recreate it with: make uninstall PLATFORM=kind PROFILE=dev-full NS=osac && make install PLATFORM=kind PROFILE=dev-full NS=osac" >&2
  exit 1
fi

if ! node_architectures=$(kubectl get nodes -o jsonpath='{range .items[*]}{.status.nodeInfo.architecture}{"\n"}{end}' 2>/dev/null) \
  || [[ -z "${node_architectures}" ]]; then
  echo "ERROR: could not read the Kind node architectures. Ensure the cluster is ready before installing the Apple Silicon dev-full compatibility policy." >&2
  exit 1
fi

while IFS= read -r architecture; do
  if [[ -z "${architecture}" ]]; then
    echo "ERROR: could not read the architecture of every Kind node. Ensure the cluster is ready before installing the Apple Silicon dev-full compatibility policy." >&2
    exit 1
  fi
  if [[ "${architecture}" != "arm64" ]]; then
    echo "ERROR: Apple Silicon dev-full requires arm64 Kind nodes; this cluster reports '${architecture}'. Recreate the cluster with an arm64 Kind node before installing the devstack release." >&2
    exit 1
  fi
done <<< "${node_architectures}"

echo "Kubernetes API server ${server_version} satisfies the dev-full compatibility requirement."
