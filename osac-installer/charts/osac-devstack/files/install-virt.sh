#!/usr/bin/env bash
# dev-full: install the virtualization stack on Kind — Multus CNI, KubeVirt, CDI.
# Requires KUBECONFIG to point at the kind cluster (set by the Makefile).
#
# Usage: install-virt.sh <kind-cluster-name>

set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null && pwd)"
# shellcheck source=./kind-runtime.sh
source "${SCRIPT_DIR}/kind-runtime.sh"

CLUSTER_NAME="${1:-${KIND_CLUSTER_NAME:-osac-dev}}"
BRIDGE_CNI_VERSION="${BRIDGE_CNI_VERSION:-v1.6.2}"

install_multus() {
  log "Installing Multus CNI..."
  kubectl apply -f https://raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/master/deployments/multus-daemonset.yml 2>&1 | tail -3
  kubectl -n kube-system wait --for=condition=Ready pods -l app=multus --timeout=120s

  log "Installing bridge CNI plugin into the kind node..."
  local node_name="${CLUSTER_NAME}-control-plane"
  container_cmd exec "${node_name}" bash -c \
    "curl -sL https://github.com/containernetworking/plugins/releases/download/${BRIDGE_CNI_VERSION}/cni-plugins-linux-amd64-${BRIDGE_CNI_VERSION}.tgz | tar -C /opt/cni/bin -xz"
  log "Multus installed"
}

install_kubevirt() {
  log "Installing KubeVirt..."

  # Remove fake KubeVirt CRDs (installed as controller stubs) — they conflict
  # with the real operator's CRDs.
  kubectl delete crd virtualmachines.kubevirt.io virtualmachineinstances.kubevirt.io 2>/dev/null || true

  local version
  version=$(curl -s https://storage.googleapis.com/kubevirt-prow/release/kubevirt/kubevirt/stable.txt)
  log "KubeVirt version: ${version}"

  kubectl apply -f "https://github.com/kubevirt/kubevirt/releases/download/${version}/kubevirt-operator.yaml" 2>&1 | tail -3
  kubectl wait --for=condition=available --timeout=120s -n kubevirt deployments -l kubevirt.io

  kubectl apply -f "https://github.com/kubevirt/kubevirt/releases/download/${version}/kubevirt-cr.yaml"
  kubectl -n kubevirt wait kv kubevirt --for condition=Available --timeout=300s

  # Register the l2bridge network-binding plugin (replicates what HCO does on
  # OpenShift). The osac-aap ocp_virt_vm role builds VM specs with
  # "binding: name: l2bridge"; managedTap wires a tap device through a bridge to
  # the pod interface.
  log "Registering l2bridge network binding plugin..."
  kubectl patch kubevirts -n kubevirt kubevirt --type=merge \
    -p='{"spec":{"configuration":{"network":{"binding":{"l2bridge":{"domainAttachmentType":"managedTap"}}}}}}'

  log "KubeVirt installed"
}

install_cdi() {
  log "Installing CDI (Containerized Data Importer)..."

  # Resolve the latest CDI tag via the GitHub "releases/latest" redirect rather
  # than the REST API — the unauthenticated API is rate-limited (returns an error
  # object with no 'tag_name'), while the redirect is not.
  local version
  version=$(basename "$(curl -fsSL -o /dev/null -w '%{url_effective}' \
    https://github.com/kubevirt/containerized-data-importer/releases/latest)")
  if [[ -z "${version}" || "${version}" == "latest" ]]; then
    err "Could not resolve latest CDI release tag from GitHub"
    exit 1
  fi
  log "CDI version: ${version}"

  kubectl apply -f "https://github.com/kubevirt/containerized-data-importer/releases/download/${version}/cdi-operator.yaml" 2>&1 | tail -3
  kubectl apply -f "https://github.com/kubevirt/containerized-data-importer/releases/download/${version}/cdi-cr.yaml"
  kubectl wait --for=condition=available --timeout=120s -n cdi deployments -l cdi.kubevirt.io

  # local-path-provisioner deadlocks with WaitForFirstConsumer when CDI imports a
  # disk (no consumer pod exists yet to trigger provisioning). Dropping the
  # feature gate lets CDI create the importer pod immediately.
  kubectl patch cdi cdi --type=json \
    -p '[{"op":"replace","path":"/spec/config/featureGates","value":["WebhookPvcRendering"]}]'

  log "CDI installed"
}

install_multus
install_kubevirt
install_cdi
log "Virtualization stack ready (Multus + KubeVirt + CDI)"
