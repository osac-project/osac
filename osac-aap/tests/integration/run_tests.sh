#!/bin/bash
set -e

# Set KUBECONFIG to dedicated file for kind cluster
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export KUBECONFIG="${SCRIPT_DIR}/kubeconfig-osac-test"
export K8S_AUTH_KUBECONFIG="${KUBECONFIG}"
echo "Using kubeconfig: ${KUBECONFIG}"

# Set Pod environment variables for lease creation (normally set by Kubernetes).
# The placeholder UID ensures leases are garbage-collected between tests
# (no real pod owns them). The lease role integration test creates its own
# real pod when it needs a persistent ownerReference.
export POD_NAMESPACE="osac-system"
export POD_NAME="test-runner"
export POD_UID="00000000-0000-0000-0000-000000000000"

# Suppress inventory parsing warnings
export ANSIBLE_INVENTORY_UNPARSED_WARNING=False
export ANSIBLE_LOCALHOST_WARNING=False

FAILED=()
PASSED=()

# Test workflows
WORKFLOWS=(
  "cluster_create"
  "cluster_delete"
  "cluster_post_install"
  "compute_instance_create"
  "compute_instance_with_gpu_create"
  "compute_instance_delete"
  "cluster_status_reporting"
)

# Role-level integration tests.
# Roles with a single baseline.yml are listed in ROLE_TESTS.
# Roles with multiple scenarios (due to set_fact persistence across plays)
# list each scenario file separately in ROLE_SCENARIO_TESTS.
ROLE_TESTS=(
  "config_as_code_pod_specs"
  "finalizer"
  "lease"
)

ROLE_SCENARIO_TESTS=(
  "cluster_working_namespace:test_not_found"
  "cluster_working_namespace:test_predefined"
  "cluster_working_namespace:test_found"
  "tenant_target_namespace:test_not_found"
  "tenant_target_namespace:test_predefined"
  "tenant_target_namespace:test_found"
)

echo "=== Running Workflow Integration Tests ==="
echo ""

for workflow in "${WORKFLOWS[@]}"; do
  echo "----------------------------------------"
  echo "Testing: $workflow"
  echo "----------------------------------------"

  # Baseline test
  echo "  [1/2] Running baseline test..."
  if ansible-playbook "targets/${workflow}/tasks/baseline.yml" -e "@common_vars.yml" -v; then
    echo "  ✓ Baseline passed"
    PASSED+=("$workflow:baseline")
  else
    echo "  ✗ Baseline failed"
    FAILED+=("$workflow:baseline")
  fi

  # Override test (skip if no overrides playbook exists)
  if [ -f "targets/${workflow}/tasks/overrides.yml" ]; then
    echo "  [2/2] Running override test..."
    # Clear override log
    > /tmp/osac_test_overrides.log

    if ansible-playbook "targets/${workflow}/tasks/overrides.yml" -e "@common_vars.yml" -v; then
      # Verify override log has entries
      if [ -s /tmp/osac_test_overrides.log ]; then
        echo "  ✓ Override test passed"
        PASSED+=("$workflow:overrides")
      else
        echo "  ✗ Override test failed (no override log entries)"
        FAILED+=("$workflow:overrides-no-log")
      fi
    else
      echo "  ✗ Override test failed"
      FAILED+=("$workflow:overrides")
    fi
  else
    echo "  [2/2] No override test (skipped)"
  fi

  echo ""
done

echo "=== Running Role Integration Tests ==="
echo ""

# Create a real pod for lease ownerReference tests (prevents K8s GC).
# Scoped to role tests only -- workflow tests use the placeholder UID
# so leases get GC'd between baseline and override runs.
echo "Creating test-runner pod for lease role tests..."
kubectl run lease-test-pod --image=registry.k8s.io/pause:3.9 --restart=Never -n osac-system 2>/dev/null || true
kubectl wait --for=condition=Ready pod/lease-test-pod -n osac-system --timeout=60s 2>/dev/null || true
LEASE_POD_UID=$(kubectl get pod lease-test-pod -n osac-system -o jsonpath='{.metadata.uid}' 2>/dev/null || echo "")
if [ -n "${LEASE_POD_UID}" ]; then
  export POD_NAME="lease-test-pod"
  export POD_UID="${LEASE_POD_UID}"
  echo "Lease test pod ready (UID: ${POD_UID})"
else
  echo "WARNING: could not create lease test pod; lease tests may fail"
fi

for role in "${ROLE_TESTS[@]}"; do
  echo "----------------------------------------"
  echo "Testing role: $role"
  echo "----------------------------------------"

  if ansible-playbook "targets/${role}/tasks/baseline.yml" -e "@common_vars.yml" -v; then
    echo "  ✓ Passed"
    PASSED+=("$role:baseline")
  else
    echo "  ✗ Failed"
    FAILED+=("$role:baseline")
  fi

  echo ""
done

for entry in "${ROLE_SCENARIO_TESTS[@]}"; do
  role="${entry%%:*}"
  scenario="${entry##*:}"
  echo "----------------------------------------"
  echo "Testing role: $role ($scenario)"
  echo "----------------------------------------"

  if ansible-playbook "targets/${role}/tasks/${scenario}.yml" -e "@common_vars.yml" -v; then
    echo "  ✓ Passed"
    PASSED+=("$role:$scenario")
  else
    echo "  ✗ Failed"
    FAILED+=("$role:$scenario")
  fi

  echo ""
done

# Clean up lease test pod
kubectl delete pod lease-test-pod -n osac-system --ignore-not-found 2>/dev/null || true

# Storage provider tests (conditional)
if [ "${STORAGE_TESTS_ENABLED:-}" = "true" ]; then
  # Source env vars written by setup_test_env.sh (Make runs each recipe line in a separate shell)
  if [ -f "${SCRIPT_DIR}/.storage_env" ]; then
    # shellcheck source=/dev/null
    . "${SCRIPT_DIR}/.storage_env"
  fi
  echo "=== Running Storage Provider Tests ==="
  echo ""

  # Reset mock server once before parallel tests (individual tests no longer reset)
  curl -sk -X POST https://127.0.0.1:18443/_reset > /dev/null 2>&1 || true

  # Storage tests share a mock VMS server with a global call log and object
  # store. Tests that assert on the call log or pre-seed VMS resources cannot
  # run in parallel without cross-contamination. Run all sequentially — each
  # test takes ~7s so the total overhead is negligible.
  STORAGE_TESTS=(
    "storage_provider_setup"
    "storage_provider_teardown"
    "storage_provider_ensure_sc"
    "storage_provider_onboarding"
    "storage_provider_setup_rollback"
  )

  for storage_test in "${STORAGE_TESTS[@]}"; do
    echo "  Running: $storage_test"
    log_file="/tmp/osac_storage_test_${storage_test}.log"
    if ansible-playbook "targets/${storage_test}/tasks/main.yml" -e "@common_vars.yml" -v > "${log_file}" 2>&1; then
      echo "  ✓ ${storage_test} passed"
      PASSED+=("$storage_test:baseline")
    else
      echo "  ✗ ${storage_test} failed (see ${log_file})"
      echo "  --- ${storage_test} failure log (last 60 lines) ---"
      tail -60 "${log_file}" 2>/dev/null || true
      echo "  --- end ${storage_test} failure log ---"
      FAILED+=("$storage_test:baseline")
    fi
  done
fi

echo "========================================"
echo "Test Results"
echo "========================================"
echo "Passed: ${#PASSED[@]}"
echo "Failed: ${#FAILED[@]}"

if [ ${#FAILED[@]} -eq 0 ]; then
  echo ""
  echo "✓ All tests passed!"
  exit 0
else
  echo ""
  echo "✗ Failed tests:"
  for test in "${FAILED[@]}"; do
    echo "  - $test"
  done
  exit 1
fi
