#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AAP_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${SCRIPT_DIR}"

RUN_SUFFIX="$$"
ROUTING_TENANT_NAME="storage-route-${RUN_SUFFIX}"
ROUTING_STORAGE_CLASS_NAME="osac-${ROUTING_TENANT_NAME}-fast"
ROUTING_MARKER_NAMESPACE="storage-route-api-${RUN_SUFFIX}"
TARGET_CLUSTER_NAME="osac-storage-route-${RUN_SUFFIX}"
MANAGEMENT_CLUSTER_NAME="osac-test"
TARGET_KUBECONFIG="$(mktemp "${TMPDIR:-/tmp}/osac-storage-route-kubeconfig.XXXXXX")"
TARGET_CLUSTER_CREATED=false
ANSIBLE_PYTHON_INTERPRETER="$(uv run python -c 'import sys; print(sys.executable)')"

run_ansible_playbook() {
  uv run ansible-playbook \
    -e "ansible_python_interpreter=${ANSIBLE_PYTHON_INTERPRETER}" \
    "$@"
}

run_without_remote_kubeconfig() (
  unset OSAC_REMOTE_CLUSTER_KUBECONFIG
  run_ansible_playbook "$@"
)

routing_vars=(
  -e "routing_test_tenant_name=${ROUTING_TENANT_NAME}"
  -e "routing_test_storage_class_name=${ROUTING_STORAGE_CLASS_NAME}"
  -e "routing_test_marker_namespace=${ROUTING_MARKER_NAMESPACE}"
  -e "routing_test_kubeconfig_path=${TARGET_KUBECONFIG}"
)

cleanup() {
  set +e
  if [ "${TARGET_CLUSTER_CREATED}" = true ]; then
    run_ansible_playbook \
      "targets/storage_target_routing/tasks/cleanup.yml" \
      "${routing_vars[@]}" >/dev/null 2>&1
    kind delete cluster --name "${TARGET_CLUSTER_NAME}" >/dev/null 2>&1 || true
  fi
  rm -f "${TARGET_KUBECONFIG}"
}
trap cleanup EXIT

echo "Creating isolated workload test cluster ${TARGET_CLUSTER_NAME}..."
TARGET_CLUSTER_CREATED=true
kind export kubeconfig --name "${MANAGEMENT_CLUSTER_NAME}" --kubeconfig "${KUBECONFIG}"
kind create cluster --name "${TARGET_CLUSTER_NAME}" --kubeconfig "${TARGET_KUBECONFIG}" --wait 5m
kind export kubeconfig --name "${TARGET_CLUSTER_NAME}" --kubeconfig "${TARGET_KUBECONFIG}"

MANAGEMENT_API_SERVER="$(kubectl --kubeconfig "${KUBECONFIG}" config view --minify -o jsonpath='{.clusters[0].cluster.server}')"
TARGET_API_SERVER="$(kubectl --kubeconfig "${TARGET_KUBECONFIG}" config view --minify -o jsonpath='{.clusters[0].cluster.server}')"
if [ "${MANAGEMENT_API_SERVER}" = "${TARGET_API_SERVER}" ]; then
  echo "The workload test kubeconfig points to the management API."
  exit 1
fi

run_ansible_playbook targets/storage_target_routing/tasks/assert_distinct_apis.yml \
  "${routing_vars[@]}"

tenant_fixture="fixtures/storage/tenant-storage-routing-test.yaml"
clusterorder_fixture="fixtures/storage/clusterorder-storage-routing-test.yaml"
create_playbook="${AAP_ROOT}/playbook_osac_create_tenant_cluster_storage.yml"
delete_playbook="${AAP_ROOT}/playbook_osac_delete_tenant_cluster_storage.yml"

run_ansible_playbook targets/storage_target_routing/tasks/cleanup.yml "${routing_vars[@]}"

echo "Checking single-cluster Tenant provisioning and teardown..."
run_without_remote_kubeconfig "${create_playbook}" \
  -e "@${tenant_fixture}" "${routing_vars[@]}" \
  -e csi_driver_install_enabled=false \
  -e csi_driver_install_lvms_storage_class_enabled=false
run_ansible_playbook targets/storage_target_routing/tasks/verify.yml \
  "${routing_vars[@]}" -e routing_test_management_count=1 -e routing_test_workload_count=0
run_without_remote_kubeconfig "${delete_playbook}" \
  -e "@${tenant_fixture}" "${routing_vars[@]}"
run_ansible_playbook targets/storage_target_routing/tasks/verify.yml \
  "${routing_vars[@]}" -e routing_test_management_count=0 -e routing_test_workload_count=0

echo "Checking split-cluster Tenant provisioning and teardown..."
OSAC_REMOTE_CLUSTER_KUBECONFIG="${TARGET_KUBECONFIG}" run_ansible_playbook "${create_playbook}" \
  -e "@${tenant_fixture}" "${routing_vars[@]}" \
  -e csi_driver_install_enabled=false \
  -e csi_driver_install_lvms_storage_class_enabled=false
run_ansible_playbook targets/storage_target_routing/tasks/verify.yml \
  "${routing_vars[@]}" -e routing_test_management_count=0 -e routing_test_workload_count=1
run_ansible_playbook targets/storage_target_routing/tasks/seed_management.yml "${routing_vars[@]}"
OSAC_REMOTE_CLUSTER_KUBECONFIG="${TARGET_KUBECONFIG}" run_ansible_playbook "${delete_playbook}" \
  -e "@${tenant_fixture}" "${routing_vars[@]}"
run_ansible_playbook targets/storage_target_routing/tasks/verify.yml \
  "${routing_vars[@]}" -e routing_test_management_count=1 -e routing_test_workload_count=0
run_ansible_playbook targets/storage_target_routing/tasks/cleanup.yml "${routing_vars[@]}"

echo "Checking ClusterOrder teardown uses its job-provided kubeconfig..."
run_ansible_playbook targets/storage_target_routing/tasks/seed_both.yml "${routing_vars[@]}"
run_without_remote_kubeconfig "${delete_playbook}" \
  -e "@${clusterorder_fixture}" "${routing_vars[@]}"
run_ansible_playbook targets/storage_target_routing/tasks/verify.yml \
  "${routing_vars[@]}" -e routing_test_management_count=1 -e routing_test_workload_count=0

echo "Storage target routing integration checks passed."
