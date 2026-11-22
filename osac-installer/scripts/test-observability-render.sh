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
  # Object storage is referenced by Secret name; the render only needs the
  # names, never key material.
  --set global.observability.objectStorage.secretName=thanos-object-storage
  --set global.observability.objectStorage.endpoint=https://s3.example.com
  --set global.observability.objectStorage.bucket=thanos
  --set global.observability.objectStorage.region=us-east-1
  # The chart refuses an empty image tag; these renders never pull.
  --set global.observability.services.metricsAuth.tag=test-metrics-auth
  --set global.observability.thanos.image.tag=test-thanos
  --set global.observability.services.oauth2Proxy.tag=test-oauth2-proxy
  --set global.observability.services.openshiftOauthProxy.tag=test-ose-oauth-proxy
  # The gate refuses an empty issuer; the render never reaches Keycloak.
  --set global.observability.keycloak.issuerUrl=https://keycloak.example.com/realms/osac
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
require "${enabled_sms}" 'caFile: /etc/prometheus/configmaps/service-ca-bundle/service-ca.crt'
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

# Extract one kind that also carries a namespace, so the checks cannot match a
# same-kind object owned by another component.
extract_kind_ns() {
  local file=$1
  local kind=$2
  local namespace=$3
  awk -v kind="${kind}" -v ns="${namespace}" '
    /^---[[:space:]]*$/ {
      if (isKind && isNs) printf "%s", doc
      doc = ""
      isKind = 0
      isNs = 0
      next
    }
    { doc = doc $0 "\n" }
    /^kind: / { if ($0 == "kind: " kind) isKind = 1 }
    /^  namespace: / { if ($0 == "  namespace: " ns) isNs = 1 }
    END { if (isKind && isNs) printf "%s", doc }
  ' "${file}"
}

# Extract one kind with one metadata name, so per-route and per-proxy
# assertions cannot match a sibling object of the same kind.
extract_kind_name() {
  local file=$1
  local kind=$2
  local name=$3
  awk -v kind="${kind}" -v name="${name}" '
    /^---[[:space:]]*$/ {
      if (isKind && isName) printf "%s", doc
      doc = ""
      isKind = 0
      isName = 0
      next
    }
    { doc = doc $0 "\n" }
    /^kind: / { if ($0 == "kind: " kind) isKind = 1 }
    /^  name: / { if ($0 == "  name: " name) isName = 1 }
    END { if (isKind && isName) printf "%s", doc }
  ' "${file}"
}

echo "checking the four Thanos workloads render with their replica counts"
for kind_name in \
  'StatefulSet:thanos-receive' \
  'Deployment:thanos-store' \
  'Deployment:thanos-query' \
  'StatefulSet:thanos-compactor'; do
  kind=${kind_name%%:*}
  name=${kind_name##*:}
  workload="${TMP_DIR}/${name}.yaml"
  extract_kind_ns "${enabled}" "${kind}" osac-observability >"${workload}"
  require "${workload}" "kind: ${kind}"
  require "${workload}" "name: ${name}"
done
require "${TMP_DIR}/thanos-receive.yaml" 'replicas: 2'
require "${TMP_DIR}/thanos-store.yaml" 'replicas: 2'
require "${TMP_DIR}/thanos-query.yaml" 'replicas: 2'
require "${TMP_DIR}/thanos-compactor.yaml" 'replicas: 1'

echo "checking remote write is served over TLS on the service-CA certificate"
thanos_services="${TMP_DIR}/thanos-services.yaml"
extract_kind_ns "${enabled}" Service osac-observability >"${thanos_services}"
require "${thanos_services}" 'name: thanos-receive'
require "${thanos_services}" 'service.beta.openshift.io/serving-cert-secret-name: thanos-receive-tls'
require "${thanos_services}" 'name: remote-write'
require "${thanos_services}" 'port: 19291'
require "${TMP_DIR}/thanos-receive.yaml" '--remote-write.server-tls-cert=/etc/thanos/tls/tls.crt'
require "${TMP_DIR}/thanos-receive.yaml" '--remote-write.server-tls-key=/etc/thanos/tls/tls.key'

echo "checking object storage is referenced by Secret name only"
require "${TMP_DIR}/thanos-receive.yaml" 'name: thanos-object-storage'
require "${TMP_DIR}/thanos-store.yaml" 'name: thanos-object-storage'
require "${TMP_DIR}/thanos-compactor.yaml" 'name: thanos-object-storage'
require "${enabled}" 'bucket: "thanos"'
require "${enabled}" 'endpoint: "https://s3.example.com"'
for forbidden in AWS_SECRET_ACCESS_KEY MINIO_ROOT_PASSWORD secret_key: access_key:; do
  reject "${enabled}" "${forbidden}"
done
objstore="${TMP_DIR}/object-storage-configmap.yaml"
extract_kind_name "${enabled}" ConfigMap thanos-object-storage >"${objstore}"
# Without aws_sdk_auth Thanos ignores the pod credentials and falls back to
# anonymous access, so the bucket reads fail even though the Secret exists.
require "${objstore}" 'aws_sdk_auth: true'
reject "${objstore}" 'access_key'
reject "${objstore}" 'secret_key'
enabled_secrets="${TMP_DIR}/thanos-secrets.yaml"
extract_kind_ns "${enabled}" Secret osac-observability >"${enabled_secrets}"
if [ -s "${enabled_secrets}" ]; then
  fail "the observability render must not create a Secret"
fi

echo "checking replication, retention and downsampling flags"
require "${TMP_DIR}/thanos-receive.yaml" '--receive.replication-factor=2'
require "${TMP_DIR}/thanos-receive.yaml" '--label=replica=$(POD_NAME)'
require "${TMP_DIR}/thanos-query.yaml" '--query.replica-label=replica'
require "${TMP_DIR}/thanos-compactor.yaml" '--retention.resolution-raw=365d'
require "${TMP_DIR}/thanos-compactor.yaml" '--downsampling.disable'
reject "${TMP_DIR}/thanos-compactor.yaml" '--retention.resolution-5m'
reject "${TMP_DIR}/thanos-compactor.yaml" '--retention.resolution-1h'

echo "checking five network policies gate the store pods"
enabled_policies="${TMP_DIR}/thanos-networkpolicies.yaml"
extract_kind_ns "${enabled}" NetworkPolicy osac-observability >"${enabled_policies}"
expect_count "${enabled_policies}" '^kind: NetworkPolicy' 5
for policy in thanos-query thanos-authorizer thanos-store thanos-receive \
  thanos-compactor; do
  require "${enabled_policies}" "name: ${policy}"
done
require "${enabled_policies}" 'app.kubernetes.io/component: receive'
require "${enabled_policies}" 'app.kubernetes.io/component: store'
require "${enabled_policies}" 'app.kubernetes.io/component: query'
require "${enabled_policies}" 'app.kubernetes.io/component: compactor'
require "${enabled_policies}" 'ingress: []'
reject "${enabled_policies}" 'podSelector: {}'
reject "${enabled_policies}" 'osac.openshift.io/tenant'
reject "${enabled_policies}" 'egress:'

echo "checking the observability gate renders before install"
require "${enabled}" 'name: osac-observability-preflight'
enabled_jobs="${TMP_DIR}/enabled-jobs.yaml"
extract_kind "${enabled}" Job >"${enabled_jobs}"
require "${enabled_jobs}" 'name: osac-observability-preflight'
require "${enabled_jobs}" 'pre-install,pre-upgrade'

echo "checking the admin query routes and proxies"
# The Routes take their hosts from global.clusterDomain, which the baseline
# render deliberately leaves empty, so render one revision with the domain set.
admin="${TMP_DIR}/admin.yaml"
"${HELM_BIN}" "${COMMON_ARGS[@]}" "${CRD_ARGS[@]}" "${OBS_ARGS[@]}" \
  --set global.clusterDomain=apps.example.com >"${admin}"
admin_routes="${TMP_DIR}/admin-routes.yaml"
extract_kind_ns "${admin}" Route osac-observability >"${admin_routes}"
# Exactly one Route per admin path: a second Route to Thanos Query would expose
# the unrestricted API beside the allowlisted one.
expect_count "${admin_routes}" '^kind: Route' 2
for route in thanos-query-admin thanos-query-admin-openshift; do
  route_doc="${TMP_DIR}/route-${route}.yaml"
  extract_kind_name "${admin}" Route "${route}" >"${route_doc}"
  require "${route_doc}" "name: ${route}"
  require "${route_doc}" 'host: '"${route}"'-osac-observability.apps.example.com'
  # Each Route reaches its own proxy Service, never thanos-query directly.
  expect_count "${route_doc}" '^    name: '"${route}"'$' 1
done
expect_count "${admin_routes}" '^    name: thanos-query$' 0

# The proxies listen behind their Service, and the Service exposes 9091 while
# the container port stays the proxy's own.
for route in thanos-query-admin thanos-query-admin-openshift; do
  service_doc="${TMP_DIR}/service-${route}.yaml"
  extract_kind_name "${admin}" Service "${route}" >"${service_doc}"
  require "${service_doc}" "name: ${route}"
  require "${service_doc}" 'port: 9091'
  require "${service_doc}" 'targetPort: gate'
  require "${service_doc}" 'app.kubernetes.io/component: query-auth-proxy'
done

# Both proxies serve the same five-endpoint allowlist through one shared
# implementation, so both must mount the same ConfigMap.
for proxy in thanos-query-admin thanos-query-admin-openshift; do
  proxy_doc="${TMP_DIR}/deployment-${proxy}.yaml"
  extract_kind_name "${enabled}" Deployment "${proxy}" >"${proxy_doc}"
  require "${proxy_doc}" "name: ${proxy}"
  require "${proxy_doc}" 'osac.openshift.io/observability-role: query-auth-proxy'
  require "${proxy_doc}" '- name: query-auth-gate'
  require "${proxy_doc}" '- /query-auth-gate'
  require "${proxy_doc}" '- name: query-allowlist'
  require "${proxy_doc}" '- /query-allowlist'
  require "${proxy_doc}" 'name: osac-observability-query-allowlist'
  require "${proxy_doc}" '--upstream=http://127.0.0.1:9091'
done
require "${TMP_DIR}/deployment-thanos-query-admin.yaml" '--client-id=thanos-admin'
require "${TMP_DIR}/deployment-thanos-query-admin.yaml" \
  '--allowed-role=cloud-provider-admin'
require "${TMP_DIR}/deployment-thanos-query-admin-openshift.yaml" \
  '--openshift-group=osac-cloud-infrastructure-admins'
require "${TMP_DIR}/deployment-thanos-query-admin.yaml" 'value: keycloak'
require "${TMP_DIR}/deployment-thanos-query-admin.yaml" \
  'value: "https://keycloak.example.com/realms/osac"'
require "${TMP_DIR}/deployment-thanos-query-admin-openshift.yaml" 'value: openshift'

# The allowlist is the whole admin API surface: one entry per documented
# endpoint and nothing else.
allowlist="${TMP_DIR}/allowlist-configmap.yaml"
extract_kind_name "${enabled}" ConfigMap osac-observability-query-allowlist >"${allowlist}"
expect_count "${allowlist}" '^    /api/v1/' 5
for endpoint in \
  '/api/v1/query' \
  '/api/v1/query_range' \
  '/api/v1/series' \
  '/api/v1/labels' \
  '/api/v1/label/<name>/values'; do
  require "${allowlist}" "${endpoint}"
done
# A directory mount of /etc/query-allowlist yields one file per key, so the key
# must be exactly the name ALLOWLIST_FILE reads. A rename on either side would
# leave the sidecar reading a missing file, which is now a startup error.
require "${allowlist}" '  endpoints.txt: |'
reject "${allowlist}" '  endpoints: |'
for proxy in thanos-query-admin thanos-query-admin-openshift; do
  require "${TMP_DIR}/deployment-${proxy}.yaml" \
    'value: /etc/query-allowlist/endpoints.txt'
  require "${TMP_DIR}/deployment-${proxy}.yaml" \
    'mountPath: /etc/query-allowlist'
done
# Only the allowlist sidecar may reach Thanos Query: the authentication proxy's
# upstream is the sidecar on loopback, checked above.
require "${TMP_DIR}/deployment-thanos-query-admin.yaml" \
  'value: http://thanos-query.osac-observability.svc.cluster.local:9090'
require "${TMP_DIR}/deployment-thanos-query-admin-openshift.yaml" \
  'value: http://thanos-query.osac-observability.svc.cluster.local:9090'

echo "checking disabled render"
disabled="${TMP_DIR}/disabled.yaml"
"${HELM_BIN}" "${COMMON_ARGS[@]}" >"${disabled}"
disabled_sms="${TMP_DIR}/disabled-servicemonitors.yaml"
extract_kind "${disabled}" ServiceMonitor >"${disabled_sms}"
expect_count "${disabled_sms}" '^kind: ServiceMonitor' 0
# The namespace check must look only at Namespace documents because other
# components' resources may carry the same string.
disabled_ns="${TMP_DIR}/disabled-namespace.yaml"
extract_kind "${disabled}" Namespace >"${disabled_ns}"
reject "${disabled_ns}" 'name: osac-observability'
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

echo "checking the disabled render keeps no Thanos resource"
for name in thanos-receive thanos-store thanos-query thanos-compactor \
  thanos-authorizer osac-observability-preflight \
  thanos-query-admin thanos-query-admin-openshift \
  osac-observability-query-allowlist osac-observability-admin-credentials; do
  reject "${disabled}" "name: ${name}"
done
reject "${disabled}" 'namespace: osac-observability'
# A fresh disabled install must not touch openshift-monitoring. The Job that
# removes a leftover osac remote-write entry only renders when an in-cluster
# lookup finds one, and helm template has no cluster to look at.
reject "${disabled}" 'name: osac-observability-remote-write'
reject "${disabled}" 'namespace: openshift-monitoring'

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
# the wizard a control that does nothing. The schema accepts the key only as the
# internal dependency alias, so Helm 4 reports "at '/observability': additional
# properties 'enabled' not allowed" while Helm 3 says "Additional property
# observability is not allowed"; match either phrasing.
top_level="${TMP_DIR}/top-level.stderr"
if "${HELM_BIN}" "${COMMON_ARGS[@]}" --set observability.enabled=true >/dev/null 2>"${top_level}"; then
  fail "a top-level observability key unexpectedly rendered"
fi
if ! grep -Eq "([Aa]dditional propert(y|ies).*['\"]?observability['\"]?.*not allowed|at '/observability'.*[Aa]dditional propert)" "${top_level}"; then
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
  "${OBS_ARGS[@]}" \
  --set global.observability.collection.osacControlPlane=
expect_render_failure zero-interval \
  'must be a positive Prometheus duration' \
  "${OBS_ARGS[@]}" \
  --set global.observability.collection.osacControlPlane=0s
expect_render_failure malformed-interval \
  'does not match pattern' \
  "${OBS_ARGS[@]}" \
  --set global.observability.collection.osacControlPlane=30x

echo "checking thanos retention and resolution validation"
expect_render_failure zero-retention \
  'global.observability.thanos.retention must be a positive Prometheus duration' \
  "${OBS_ARGS[@]}" \
  --set global.observability.thanos.retention=0d
expect_render_failure retention-resolution-mismatch \
  'global.observability.thanos.resolution.5m must equal global.observability.thanos.retention' \
  "${OBS_ARGS[@]}" \
  --set global.observability.thanos.retention=30d \
  --set global.observability.thanos.resolution.raw=30d \
  --set global.observability.thanos.resolution.5m=365d \
  --set global.observability.thanos.resolution.1h=30d

retention_ok="${TMP_DIR}/retention-ok.yaml"
"${HELM_BIN}" "${COMMON_ARGS[@]}" \
  "${OBS_ARGS[@]}" \
  --set global.observability.thanos.retention=30d \
  --set global.observability.thanos.resolution.raw=30d \
  --set global.observability.thanos.resolution.5m=30d \
  --set global.observability.thanos.resolution.1h=30d >"${retention_ok}"
require "${retention_ok}" 'name: osac-observability'

echo "checking infrastructure collection interval validation"
# The schema pattern already rejects an empty string; skip it so this proves
# osac.validateValues never rewrites a missing interval to the default.
expect_render_failure empty-infrastructure \
  'global.observability.collection.infrastructure must be a positive Prometheus duration' \
  --skip-schema-validation \
  "${OBS_ARGS[@]}" \
  --set global.observability.collection.infrastructure=

echo 'Observability render checks passed.'
