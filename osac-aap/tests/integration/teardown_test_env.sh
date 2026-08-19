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

# Stop local OCI registry for csi_driver_install tests, if running. Mirrors
# setup_test_env.sh's own 3-way container-tool detection (KIND_EXPERIMENTAL_PROVIDER >
# docker > podman) -- a 2-way docker/podman-only cascade here could remove the wrong
# tool's container (docker rm -f exits 0 even when the container doesn't exist, silently
# leaking a podman-created one) when both tools are installed and
# KIND_EXPERIMENTAL_PROVIDER=podman is set.
if [ -n "${KIND_EXPERIMENTAL_PROVIDER:-}" ]; then
  _CSI_DRIVER_TEST_CONTAINER_TOOL="${KIND_EXPERIMENTAL_PROVIDER}"
elif command -v docker > /dev/null 2>&1; then
  _CSI_DRIVER_TEST_CONTAINER_TOOL="docker"
else
  _CSI_DRIVER_TEST_CONTAINER_TOOL="podman"
fi
"${_CSI_DRIVER_TEST_CONTAINER_TOOL}" rm -f osac-test-csi-registry > /dev/null 2>&1 || true
rm -rf "${SCRIPT_DIR}/.csi_driver_chart_pkgs"

# Remove the local CA installed into the system trust store by setup_test_env.sh's local
# OCI registry section, so it doesn't persist beyond this test run on a real workstation
# (CI runners are ephemeral, but this is also run locally).
if [ -f /usr/local/share/ca-certificates/osac-test-registry-ca.crt ]; then
  sudo rm -f /usr/local/share/ca-certificates/osac-test-registry-ca.crt
  sudo update-ca-certificates > /dev/null 2>&1 || true
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
