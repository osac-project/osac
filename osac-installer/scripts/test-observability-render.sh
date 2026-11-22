#!/usr/bin/env bash

# Verifies the OSAC metrics scrape wiring renders as designed: platform
# monitoring labels and scrape RBAC, the eight plain-HTTP ServiceMonitors, the
# metering scrape targets, and the disabled path producing none of it.

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
CHART_DIR=$(cd -- "${SCRIPT_DIR}/../charts/osac" && pwd)
HELM_BIN=${HELM_BIN:-helm}
TMP_DIR=$(mktemp -d)
trap 'rm -rf "${TMP_DIR}"' EXIT

COMMON_ARGS=(
  template osac "${CHART_DIR}"
  --values "${CHART_DIR}/ci/default-values.yaml"
  # Required once metering is enabled, which the scrape checks need.
  --set global.osacDeploymentId=observability-render
)
CRD_ARGS=(
  --api-versions monitoring.coreos.com/v1
  --api-versions monitoring.coreos.com/v1/ServiceMonitor
)
METERING_ARGS=(
  --set metering.enabled=true
  --set metering.echoAdapter.enabled=true
  --set metering.echoAdapter.image.repository=example/echo
  --set metering.echoAdapter.image.tag=v1
  --set metering.m360Adapter.enabled=true
  --set metering.m360Adapter.m360.apiUrl=https://m360.example.com
  --set metering.m360Adapter.m360.apiKeySecret=m360-key
)

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

require() {
  local file=$1
  local pattern=$2
  if ! grep -Fq -- "${pattern}" "${file}"; then
    echo "ERROR: expected ${pattern@Q} in ${file}" >&2
    exit 1
  fi
}

reject() {
  local file=$1
  local pattern=$2
  if grep -Fq -- "${pattern}" "${file}"; then
    echo "ERROR: did not expect ${pattern@Q} in ${file}" >&2
    exit 1
  fi
}

expect_count() {
  local file=$1
  local pattern=$2
  local expected=$3
  local actual
  actual=$(grep -c -- "${pattern}" "${file}" || true)
  if [ "${actual}" != "${expected}" ]; then
    echo "ERROR: expected ${expected} occurrences of ${pattern@Q} in ${file}, found ${actual}" >&2
    exit 1
  fi
}

expect_render_failure() {
  local name=$1
  local expected=$2
  shift 2

  local error="${TMP_DIR}/${name}.stderr"
  echo "checking ${name} (expected failure)"
  if "${HELM_BIN}" "${COMMON_ARGS[@]}" "$@" >/dev/null 2>"${error}"; then
    fail "${name} unexpectedly rendered successfully"
  fi
  if ! grep -Fq -- "${expected}" "${error}"; then
    echo "ERROR: ${name} failed without the expected message ${expected@Q}" >&2
    cat "${error}" >&2
    exit 1
  fi
}

enabled="${TMP_DIR}/enabled.yaml"
echo "checking enabled render"
"${HELM_BIN}" "${COMMON_ARGS[@]}" "${CRD_ARGS[@]}" \
  --set observability.enabled=true "${METERING_ARGS[@]}" >"${enabled}"

expect_count "${enabled}" '^kind: ServiceMonitor' 8
for name in \
  fulfillment-grpc-server \
  fulfillment-rest-gateway \
  fulfillment-controller \
  fulfillment-event-publisher \
  fulfillment-console-proxy \
  osac-metering \
  echo-adapter \
  m360-adapter; do
  require "${enabled}" "name: ${name}"
done
expect_count "${enabled}" 'jobLabel: app$' 5
expect_count "${enabled}" 'jobLabel: app.kubernetes.io/component' 2
expect_count "${enabled}" 'jobLabel: app.kubernetes.io/name' 1
require "${enabled}" 'port: metrics'
require "${enabled}" 'port: http'
require "${enabled}" 'scheme: http'
require "${enabled}" 'path: /metrics'

echo "checking platform monitoring discovery"
require "${enabled}" 'name: osac-observability'
require "${enabled}" 'openshift.io/cluster-monitoring: "true"'
require "${enabled}" 'name: osac-label-namespaces'
require "${enabled}" 'openshift.io/cluster-monitoring=true'
require "${enabled}" 'name: osac-prometheus-scrape'
require "${enabled}" 'name: prometheus-k8s'
require "${enabled}" 'namespace: openshift-monitoring'

echo "checking metering scrape target"
require "${enabled}" 'targetPort: health'

echo "checking custom scrape interval"
interval="${TMP_DIR}/interval.yaml"
"${HELM_BIN}" "${COMMON_ARGS[@]}" "${CRD_ARGS[@]}" \
  --set observability.enabled=true "${METERING_ARGS[@]}" \
  --set observability.collection.osacControlPlane=45s >"${interval}"
expect_count "${interval}" 'interval: "45s"' 8
expect_count "${interval}" 'interval: "30s"' 0

echo "checking disabled render"
disabled="${TMP_DIR}/disabled.yaml"
"${HELM_BIN}" "${COMMON_ARGS[@]}" >"${disabled}"
expect_count "${disabled}" '^kind: ServiceMonitor' 0
reject "${disabled}" 'name: osac-observability'
reject "${disabled}" 'name: osac-prometheus-scrape'
reject "${disabled}" 'name: osac-label-namespaces'
reject "${disabled}" 'openshift.io/cluster-monitoring'

echo "checking monitoring CRD guard"
no_crd="${TMP_DIR}/no-crd.yaml"
"${HELM_BIN}" "${COMMON_ARGS[@]}" --set observability.enabled=true >"${no_crd}"
expect_count "${no_crd}" '^kind: ServiceMonitor' 0
require "${no_crd}" 'name: osac-observability'

echo "checking invalid scrape interval rejection"
expect_render_failure zero-interval \
  'observability.collection.osacControlPlane must be a positive Prometheus duration' \
  --set observability.enabled=true --set observability.collection.osacControlPlane=0s
expect_render_failure malformed-interval \
  "does not match pattern" \
  --set observability.enabled=true --set observability.collection.osacControlPlane=30x

echo 'Observability render checks passed.'
