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

ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    arm64)   ARCH="arm64" ;;   # macOS Apple Silicon reports "arm64"
    *)       echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

log() { echo "[+] $*"; }
die() { echo "[!] $*" >&2; exit 1; }

use_podman() {
  command -v podman >/dev/null 2>&1 || die "podman is required by KIND_EXPERIMENTAL_PROVIDER=podman"

  RUNTIME=podman
  # Podman Desktop on macOS is user-scoped. Avoid an unnecessary sudo prompt
  # there; Linux retains the rootful-first compatibility path for clusters
  # created with sudo.
  if [[ "$(uname -s)" != "Darwin" ]] && \
    sudo podman ps --filter "name=${CLUSTER_NAME}-control-plane" --format '{{.Names}}' 2>/dev/null | grep -q "${CLUSTER_NAME}-control-plane"; then
    RUNTIME="sudo podman"
  elif podman ps --filter "name=${CLUSTER_NAME}-control-plane" --format '{{.Names}}' 2>/dev/null | grep -q "${CLUSTER_NAME}-control-plane"; then
    RUNTIME=podman
  else
    die "Kind cluster '${CLUSTER_NAME}' not found in podman"
  fi
}

use_docker() {
  command -v docker >/dev/null 2>&1 || die "docker is required by KIND_EXPERIMENTAL_PROVIDER=docker"

  RUNTIME=docker
  # Verify cluster exists
  $RUNTIME ps --filter "name=${CLUSTER_NAME}-control-plane" --format '{{.Names}}' | grep -q "${CLUSTER_NAME}-control-plane" \
    || die "Kind cluster '${CLUSTER_NAME}' not found"
}

# Honor an explicit Kind provider before falling back to the existing Podman-first
# detection used for local development.
case "${KIND_EXPERIMENTAL_PROVIDER:-}" in
  podman)
    use_podman
    ;;
  docker)
    use_docker
    ;;
  "")
    if command -v podman >/dev/null 2>&1; then
      use_podman
    elif command -v docker >/dev/null 2>&1; then
      use_docker
    else
      die "Neither podman nor docker found"
    fi
    ;;
  *)
    die "Unsupported KIND_EXPERIMENTAL_PROVIDER: ${KIND_EXPERIMENTAL_PROVIDER}"
    ;;
esac

NODE_NAME="${CLUSTER_NAME}-control-plane"

log "Installing bridge CNI plugin into ${NODE_NAME}..."
$RUNTIME exec "${NODE_NAME}" bash -c \
  "curl -sL https://github.com/containernetworking/plugins/releases/download/${BRIDGE_CNI_VERSION}/cni-plugins-linux-${ARCH}-${BRIDGE_CNI_VERSION}.tgz | tar -C /opt/cni/bin -xz" \
  || die "Failed to install bridge CNI plugin"

log "Bridge CNI plugin installed successfully"
