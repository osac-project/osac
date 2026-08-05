#!/bin/bash
set -e

echo "=== Tearing down test environment ==="

# Stop mock VMS server if running
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ -f "${SCRIPT_DIR}/.mock_vms_pid" ]; then
  MOCK_VMS_PID=$(cat "${SCRIPT_DIR}/.mock_vms_pid")
  echo "Stopping mock VMS server (PID: ${MOCK_VMS_PID})..."
  kill "${MOCK_VMS_PID}" 2>/dev/null || true
  rm -f "${SCRIPT_DIR}/.mock_vms_pid"
fi

# Delete kind cluster
kind delete cluster --name osac-test

# Clean up temporary files
rm -f /tmp/osac_test_overrides.log
rm -rf /tmp/osac-operator
rm -rf "${SCRIPT_DIR}/certs"
rm -f "${SCRIPT_DIR}/kubeconfig-osac-test"
rm -f "${SCRIPT_DIR}/.storage_env"

echo "=== Cleanup complete ==="
