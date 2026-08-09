#!/usr/bin/env bash
# Tear down the OSAC Kind development environment
#
# Usage:
#   ./teardown.sh              # Delete the kind cluster
#   ./teardown.sh --keep-data  # Uninstall OSAC but keep the cluster
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-osac-dev}"
KEEP_DATA=false

for arg in "$@"; do
  case "$arg" in
    --keep-data) KEEP_DATA=true ;;
    --help|-h)
      echo "Usage: $0 [--keep-data]"
      echo "  --keep-data  Uninstall OSAC services but keep the kind cluster"
      exit 0
      ;;
  esac
done

SCRIPT_DIR="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"

# Source runtime detection, kind_cmd(), and helpers (log, err, etc.) from setup.sh
# shellcheck source=setup.sh
source "${SCRIPT_DIR}/setup.sh"
detect_podman_mode

KC_FILE="${HOME}/.kube/${CLUSTER_NAME}-kind.kubeconfig"

if [[ "$KEEP_DATA" == "true" ]]; then
  log "Uninstalling OSAC services (keeping cluster)..."
  export KUBECONFIG="${KC_FILE}"

  helm uninstall osac -n osac 2>/dev/null || true
  helm uninstall openbao -n openbao 2>/dev/null || true
  helm uninstall keycloak -n keycloak 2>/dev/null || true
  helm uninstall postgres -n osac 2>/dev/null || true

  log "OSAC services uninstalled. Cluster '${CLUSTER_NAME}' still running."
  log "To delete the cluster: $0"
else
  log "Deleting kind cluster '${CLUSTER_NAME}'..."
  if kind_cmd delete cluster --name "${CLUSTER_NAME}"; then
    log "Cluster deleted"
  else
    err "Failed to delete cluster '${CLUSTER_NAME}'"
    exit 1
  fi
  log "Kubeconfig at ${KC_FILE} — remove manually if no longer needed"
fi
