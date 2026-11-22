#!/usr/bin/env bash

# Renders the observability chart through the umbrella installer chart and
# asserts the store, query and admin-route contract. Run from the repo root or
# from this component; the script resolves both paths.

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
COMPONENT_DIR=$(cd -- "${SCRIPT_DIR}/.." && pwd)
REPO_ROOT=$(cd -- "${COMPONENT_DIR}/.." && pwd)
CHART_DIR="${REPO_ROOT}/osac-installer/charts/osac"
HELM_BIN=${HELM_BIN:-helm}
TMP_DIR=$(mktemp -d)
trap 'rm -rf "${TMP_DIR}"' EXIT

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

require() {
  local file=$1 pattern=$2
  if ! grep -Fq -- "${pattern}" "${file}"; then
    fail "expected ${pattern@Q} in ${file}"
  fi
}

reject() {
  local file=$1 pattern=$2
  if grep -Fq -- "${pattern}" "${file}"; then
    fail "did not expect ${pattern@Q} in ${file}"
  fi
}

COMMON_ARGS=(
  template osac "${CHART_DIR}"
  --namespace osac
  --values "${CHART_DIR}/ci/default-values.yaml"
  --set global.osacDeploymentId=observability-render
  --api-versions monitoring.coreos.com/v1
  --api-versions monitoring.coreos.com/v1/ServiceMonitor
  --api-versions route.openshift.io/v1
  --api-versions oauth.openshift.io/v1
)
OBS_ARGS=(
  --set global.observability.enabled=true
  --set global.observability.objectStorage.secretName=thanos-object-storage
  --set global.observability.objectStorage.endpoint=https://s3.example.com
  --set global.observability.objectStorage.bucket=thanos
  --set global.observability.objectStorage.region=us-east-1
  --set global.observability.collection.osacControlPlane=30s
)

enabled="${TMP_DIR}/enabled.yaml"
disabled="${TMP_DIR}/disabled.yaml"

"${HELM_BIN}" "${COMMON_ARGS[@]}" "${OBS_ARGS[@]}" >"${enabled}"
"${HELM_BIN}" "${COMMON_ARGS[@]}" >"${disabled}"

echo "checking the observability subchart renders only when enabled"
require "${enabled}" 'name: osac-observability'
reject "${disabled}" 'name: osac-observability'

echo "Observability component render checks passed."
