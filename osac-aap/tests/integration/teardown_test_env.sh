#!/bin/bash
set -e

echo "=== Tearing down test environment ==="
CLEANUP_FAILED=0

# Stop mock VMS server if running
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export KUBECONFIG="${SCRIPT_DIR}/kubeconfig-osac-test"
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
rm -f "${SCRIPT_DIR}"/certs/registry-ca.key "${SCRIPT_DIR}"/certs/registry-ca.pem \
      "${SCRIPT_DIR}"/certs/registry-ca.srl "${SCRIPT_DIR}"/certs/registry.key \
      "${SCRIPT_DIR}"/certs/registry.pem "${SCRIPT_DIR}"/certs/registry.csr \
      "${SCRIPT_DIR}"/certs/registry.ext

# Remove the local CA installed into the system trust store by setup_test_env.sh's local
# OCI registry section, so it doesn't persist beyond this test run on a real workstation
# (CI runners are ephemeral, but this is also run locally).
if [ -f /usr/local/share/ca-certificates/osac-test-registry-ca.crt ]; then
  sudo rm -f /usr/local/share/ca-certificates/osac-test-registry-ca.crt || true
  sudo update-ca-certificates > /dev/null 2>&1 || true
fi

# Stop publish_templates test servers left behind by an interrupted scenario.
python3 - <<'PY'
import ssl
import urllib.error
import urllib.request

opener = urllib.request.build_opener(
    urllib.request.ProxyHandler({}),
    urllib.request.HTTPSHandler(context=ssl._create_unverified_context()),
)
for url in (
    "http://127.0.0.1:18081/_shutdown",
    "https://127.0.0.1:18082/_shutdown",
):
    try:
        opener.open(url, timeout=1).close()
    except (OSError, urllib.error.URLError):
        pass
PY

# Setup can fail before creating the cluster; still clean up local resources.
if command -v kind > /dev/null 2>&1; then
  if KIND_CLUSTERS=$(kind get clusters); then
    if grep -Fxq "osac-test" <<<"${KIND_CLUSTERS}"; then
      if ! kind delete cluster --name osac-test; then
        echo "WARNING: Failed to delete Kind cluster; continuing local cleanup" >&2
        CLEANUP_FAILED=1
      fi
    fi
  else
    echo "WARNING: Could not list Kind clusters; continuing local cleanup" >&2
    CLEANUP_FAILED=1
  fi
fi

# Clean up temporary files
rm -f /tmp/osac_test_overrides.log
rm -rf /tmp/osac-operator
rm -rf "${SCRIPT_DIR}/certs"
rm -f "${SCRIPT_DIR}/kubeconfig-osac-test"
rm -f "${SCRIPT_DIR}/.storage_env"

if [ "${CLEANUP_FAILED}" -ne 0 ]; then
  echo "=== Cleanup completed with errors ===" >&2
  exit 1
fi

echo "=== Cleanup complete ==="
