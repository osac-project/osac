#!/usr/bin/env bash

# Verifies the OSAC metrics scrape wiring renders as designed: platform
# monitoring labels and scrape RBAC, the six plain-HTTP ServiceMonitors with
# their eight targets, the two HTTPS operator ServiceMonitors, the
# label-removal hooks, and the disabled path producing none of the scrapes.

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
CHART_DIR=$(cd -- "${SCRIPT_DIR}/../charts/osac" && pwd)
REPO_ROOT=$(cd -- "${SCRIPT_DIR}/../.." && pwd)
FULFILLMENT_CHART="${REPO_ROOT}/fulfillment-service/charts/service"
METERING_CHART="${REPO_ROOT}/osac-metering/charts/osac-metering"
OPERATOR_CHART="${REPO_ROOT}/osac-operator/charts/operator"
BMF_CHART="${REPO_ROOT}/bare-metal-fulfillment-operator/charts/operator"
HELM_BIN=${HELM_BIN:-helm}
TMP_DIR=$(mktemp -d)
trap 'rm -rf "${TMP_DIR}"' EXIT

COMMON_ARGS=(
  template osac "${CHART_DIR}"
  # Pin the release namespace so the assertions do not depend on the local
  # kubeconfig context.
  --namespace osac
  --values "${CHART_DIR}/ci/default-values.yaml"
  # Required once metering is enabled, which the scrape checks need.
  --set global.osacDeploymentId=observability-render
)
CRD_ARGS=(
  --api-versions monitoring.coreos.com/v1
  --api-versions monitoring.coreos.com/v1/ServiceMonitor
)
# One switch: global.observability reaches both the umbrella templates and the
# subcharts. Enabling it must not require a second --set.
OBS_ARGS=(
  --set global.observability.enabled=true
)
# Metering is always deployed (billing is not optional), so the scrape checks
# only switch its billing adapters on.
METERING_ARGS=(
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

# Extract the documents of one kind so endpoint assertions cannot accidentally
# match unrelated objects in the umbrella render.
extract_kind() {
  local file=$1
  local kind=$2
  awk -v kind="${kind}" '
    /^---[[:space:]]*$/ {
      if (isKind) printf "%s", doc
      doc = ""
      isKind = 0
      next
    }
    { doc = doc $0 "\n" }
    /^kind: / { if ($0 == "kind: " kind) isKind = 1 }
    END { if (isKind) printf "%s", doc }
  ' "${file}"
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
  "${OBS_ARGS[@]}" "${METERING_ARGS[@]}" >"${enabled}"

enabled_sms="${TMP_DIR}/enabled-servicemonitors.yaml"
extract_kind "${enabled}" ServiceMonitor >"${enabled_sms}"
expect_count "${enabled_sms}" '^kind: ServiceMonitor' 8
for job in \
  fulfillment-grpc-server \
  fulfillment-rest-gateway \
  fulfillment-controller \
  fulfillment-event-publisher \
  fulfillment-console-proxy \
  osac-metering \
  osac-operator \
  bmf-operator; do
  require "${enabled_sms}" "replacement: ${job}"
done
require "${enabled_sms}" 'targetLabel: job'
require "${enabled_sms}" 'port: metrics'
require "${enabled_sms}" 'port: http'
require "${enabled_sms}" 'scheme: http'
require "${enabled_sms}" 'scheme: https'
require "${enabled_sms}" 'port: https'
require "${enabled_sms}" 'bearerTokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token'
require "${enabled_sms}" 'caFile: /etc/prometheus/configmaps/serving-certs-ca-bundle/service-ca.crt'
require "${enabled_sms}" 'path: /metrics'
# Helm rewrites .Chart.Name to the subchart alias, so the metering selector is
# "metering" here even though the chart's own name is osac-metering.
require "${enabled_sms}" 'app.kubernetes.io/name: metering'
expect_count "${enabled_sms}" 'interval: "30s"' 9
expect_count "${enabled_sms}" '^    - osac$' 8
for forbidden in insecureSkipVerify bearerTokenSecret basicAuth oauth2; do
  reject "${enabled_sms}" "${forbidden}"
done

echo "checking platform monitoring discovery"
require "${enabled}" 'name: osac-observability'
require "${enabled}" 'openshift.io/cluster-monitoring: "true"'
require "${enabled}" 'name: osac-prometheus-scrape'
require "${enabled}" 'name: prometheus-k8s'
require "${enabled}" 'namespace: openshift-monitoring'
require "${enabled}" 'name: osac-unlabel-namespaces'
# The operators serve the service-CA certificate on 8443 and bind the platform
# Prometheus service account to their aggregate metrics-reader role.
require "${enabled}" 'serving-cert-secret-name: osac-operator-metrics-tls'
require "${enabled}" 'serving-cert-secret-name: bmf-operator-metrics-tls'
require "${enabled}" 'name: osac-operator-metrics-reader-prometheus'
require "${enabled}" 'name: bmf-operator-metrics-reader-prometheus'

echo "checking metering scrape target"
require "${enabled}" 'targetPort: health'
# The adapter ServiceMonitors derive their Prometheus job from the Service
# label, so assert the Services carry the component labels the relabel reads.
enabled_services="${TMP_DIR}/enabled-services.yaml"
extract_kind "${enabled}" Service >"${enabled_services}"
require "${enabled_services}" 'app.kubernetes.io/component: echo-adapter'
require "${enabled_services}" 'app.kubernetes.io/component: m360-adapter'
require "${enabled_sms}" '__meta_kubernetes_service_label_app_kubernetes_io_component'

echo "checking observability-only render keeps the billing adapters off"
default_render="${TMP_DIR}/default.yaml"
"${HELM_BIN}" "${COMMON_ARGS[@]}" "${CRD_ARGS[@]}" "${OBS_ARGS[@]}" >"${default_render}"
default_sms="${TMP_DIR}/default-servicemonitors.yaml"
extract_kind "${default_render}" ServiceMonitor >"${default_sms}"
# Metering is deployed unconditionally, so its own target is present; only the
# adapter endpoint (derived from the Service component label) is absent.
expect_count "${default_sms}" '^kind: ServiceMonitor' 8
require "${default_sms}" 'replacement: osac-metering'
reject "${default_sms}" '__meta_kubernetes_service_label_app_kubernetes_io_component'

echo "checking custom scrape interval"
interval="${TMP_DIR}/interval.yaml"
"${HELM_BIN}" "${COMMON_ARGS[@]}" "${CRD_ARGS[@]}" \
  "${OBS_ARGS[@]}" "${METERING_ARGS[@]}" \
  --set global.observability.collection.osacControlPlane=45s >"${interval}"
interval_sms="${TMP_DIR}/interval-servicemonitors.yaml"
extract_kind "${interval}" ServiceMonitor >"${interval_sms}"
expect_count "${interval_sms}" 'interval: "45s"' 9
expect_count "${interval_sms}" 'interval: "30s"' 0

echo "checking disabled render"
disabled="${TMP_DIR}/disabled.yaml"
"${HELM_BIN}" "${COMMON_ARGS[@]}" >"${disabled}"
disabled_sms="${TMP_DIR}/disabled-servicemonitors.yaml"
extract_kind "${disabled}" ServiceMonitor >"${disabled_sms}"
expect_count "${disabled_sms}" '^kind: ServiceMonitor' 0
reject "${disabled}" 'name: osac-observability'
reject "${disabled}" 'name: osac-prometheus-scrape'
reject "${disabled}" 'openshift.io/cluster-monitoring=true'
# The disabled revision still carries the upgrade hook that removes a label an
# earlier enabled release applied. Other umbrella hooks also run on pre-upgrade,
# so scope the hook check to the Job documents.
disabled_jobs="${TMP_DIR}/disabled-jobs.yaml"
extract_kind "${disabled}" Job >"${disabled_jobs}"
require "${disabled_jobs}" 'name: osac-unlabel-namespaces'
require "${disabled_jobs}" 'pre-upgrade'
# Disabling observability removes the scrapes and their Prometheus RBAC, but the
# operators keep serving HTTPS metrics on 8443 with the service-CA certificate.
require "${disabled}" '--metrics-bind-address=:8443'
require "${disabled}" 'serving-cert-secret-name: osac-operator-metrics-tls'
require "${disabled}" 'serving-cert-secret-name: bmf-operator-metrics-tls'
reject "${disabled}" 'name: osac-operator-metrics-reader-prometheus'
reject "${disabled}" 'name: bmf-operator-metrics-reader-prometheus'

echo "checking monitoring CRD guard"
no_crd="${TMP_DIR}/no-crd.yaml"
"${HELM_BIN}" "${COMMON_ARGS[@]}" "${OBS_ARGS[@]}" "${METERING_ARGS[@]}" >"${no_crd}"
no_crd_sms="${TMP_DIR}/no-crd-servicemonitors.yaml"
extract_kind "${no_crd}" ServiceMonitor >"${no_crd_sms}"
expect_count "${no_crd_sms}" '^kind: ServiceMonitor' 0
require "${no_crd}" 'name: osac-observability'
require "${no_crd}" 'name: osac-prometheus-scrape'

echo "checking the top-level observability key is rejected"
# global.observability is the only switch; a second top-level object would give
# the wizard a control that does nothing. Helm 4 says "additional properties
# 'observability' not allowed"; Helm 3 says "Additional property observability
# is not allowed", so match either phrasing.
top_level="${TMP_DIR}/top-level.stderr"
if "${HELM_BIN}" "${COMMON_ARGS[@]}" --set observability.enabled=true >/dev/null 2>"${top_level}"; then
  fail "a top-level observability key unexpectedly rendered"
fi
if ! grep -Eq "[Aa]dditional propert(y|ies).*observability.*not allowed" "${top_level}"; then
  echo "ERROR: top-level observability was not rejected by the schema" >&2
  cat "${top_level}" >&2
  exit 1
fi

echo "checking component serviceMonitor defaults stay off"
for chart in "${FULFILLMENT_CHART}" "${METERING_CHART}" "${OPERATOR_CHART}" "${BMF_CHART}"; do
  values="${TMP_DIR}/$(basename "${chart}")-values.yaml"
  "${HELM_BIN}" show values "${chart}" >"${values}"
  block=$(awk '/^serviceMonitor:/{f=1} f && /^[^[:space:]#]/ && !/^serviceMonitor:/{exit} f' "${values}")
  if [ -z "${block}" ]; then
    fail "no serviceMonitor block in the values of ${chart}"
  fi
  if ! printf '%s\n' "${block}" | grep -Eq '^  enabled: false$'; then
    echo "ERROR: serviceMonitor.enabled must default to false in ${chart}:" >&2
    printf '%s\n' "${block}" >&2
    exit 1
  fi
done

echo "checking invalid scrape interval rejection"
# Skip schema validation here: the schema pattern rejects an empty string first,
# so this exercises osac.validateValues and proves an empty interval is not
# silently rewritten to the 30s default.
expect_render_failure empty-interval \
  'global.observability.collection.osacControlPlane must be a positive Prometheus duration' \
  --skip-schema-validation \
  --set global.observability.enabled=true \
  --set global.observability.collection.osacControlPlane=
expect_render_failure zero-interval \
  'must be a positive Prometheus duration' \
  --set global.observability.enabled=true \
  --set global.observability.collection.osacControlPlane=0s
expect_render_failure malformed-interval \
  'does not match pattern' \
  --set global.observability.enabled=true \
  --set global.observability.collection.osacControlPlane=30x

echo 'Observability render checks passed.'
