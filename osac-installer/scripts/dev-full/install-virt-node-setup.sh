#!/usr/bin/env bash
# install-virt-node-setup.sh - Install bridge CNI plugin into kind node
#
# Runs on the host (not in a Helm Job) because it requires container runtime
# access to exec into the kind node container. The rest of the virtualization
# stack (Multus, KubeVirt, CDI operators) is installed by the Helm chart's
# install-virt hook Job.
#
# Usage: install-virt-node-setup.sh [cluster-name]

set -euo pipefail

CLUSTER_NAME="${1:-osac-dev}"
BRIDGE_CNI_VERSION="${2:-v1.6.2}"

log() { echo "[+] $*"; }
die() { echo "[!] $*" >&2; exit 1; }

# Detect container runtime (try rootful first, then rootless)
if command -v podman >/dev/null 2>&1; then
  RUNTIME=podman
  export KIND_EXPERIMENTAL_PROVIDER=podman
  # Check if cluster exists in rootful podman (created with sudo)
  if sudo podman ps --filter "name=${CLUSTER_NAME}-control-plane" --format '{{.Names}}' 2>/dev/null | grep -q "${CLUSTER_NAME}-control-plane"; then
    RUNTIME="sudo podman"
  # Check if cluster exists in rootless podman
  elif podman ps --filter "name=${CLUSTER_NAME}-control-plane" --format '{{.Names}}' 2>/dev/null | grep -q "${CLUSTER_NAME}-control-plane"; then
    RUNTIME=podman
  else
    die "Kind cluster '${CLUSTER_NAME}' not found in podman (tried both rootful and rootless)"
  fi
elif command -v docker >/dev/null 2>&1; then
  RUNTIME=docker
  # Verify cluster exists
  $RUNTIME ps --filter "name=${CLUSTER_NAME}-control-plane" --format '{{.Names}}' | grep -q "${CLUSTER_NAME}-control-plane" \
    || die "Kind cluster '${CLUSTER_NAME}' not found"
else
  die "Neither podman nor docker found"
fi

NODE_NAME="${CLUSTER_NAME}-control-plane"

log "Installing bridge CNI plugin into ${NODE_NAME}..."
$RUNTIME exec "${NODE_NAME}" bash -c \
  "curl -sL https://github.com/containernetworking/plugins/releases/download/${BRIDGE_CNI_VERSION}/cni-plugins-linux-amd64-${BRIDGE_CNI_VERSION}.tgz | tar -C /opt/cni/bin -xz" \
  || die "Failed to install bridge CNI plugin"

log "Bridge CNI plugin installed successfully"
