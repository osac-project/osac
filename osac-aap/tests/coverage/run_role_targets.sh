#!/bin/bash
# The integration targets of the network manager roles, on a Kind cluster of
# their own. Used by `make role-coverage`; `make test` runs the same targets
# through tests/integration/run_tests.sh.
set -euo pipefail

CLUSTER="${ROLE_COVERAGE_CLUSTER:-osac-role-coverage}"
INTEGRATION="$(cd "$(dirname "${BASH_SOURCE[0]}")/../integration" && pwd)"
cd "${INTEGRATION}"

WORK="$(mktemp -d)"
export KUBECONFIG="${WORK}/kubeconfig"
export K8S_AUTH_KUBECONFIG="${KUBECONFIG}"
CREATED=false
cleanup() {
  if [ "${CREATED}" = true ]; then
    kind delete cluster --name "${CLUSTER}" >/dev/null 2>&1 || true
  fi
  rm -rf "${WORK}"
}
trap cleanup EXIT

kind create cluster --name "${CLUSTER}" --wait 5m
CREATED=true
export ANSIBLE_INVENTORY_UNPARSED_WARNING=False ANSIBLE_LOCALHOST_WARNING=False
INTERPRETER="$(uv run python -c 'import sys; print(sys.executable)')"

for target in network_task_playbook cudn_evpn netris_fabric; do
  echo "=== ${target} ==="
  uv run ansible-playbook -e "ansible_python_interpreter=${INTERPRETER}" \
    "targets/${target}/tasks/baseline.yml" -e "@common_vars.yml"
done
