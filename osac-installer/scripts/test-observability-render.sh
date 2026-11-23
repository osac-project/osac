#!/usr/bin/env bash

# Verifies the OSAC metrics scrape wiring renders as designed: platform
# monitoring labels and scrape RBAC, the six plain-HTTP ServiceMonitors with
# their eight targets, the four store-component ServiceMonitors for pipeline
# health, the two HTTPS operator ServiceMonitors, the label-removal hooks, and
# the disabled path producing none of the scrapes.

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
# Eight component scrapes plus the four store-component scrapes that carry the
# pipeline-health series.
expect_count "${enabled_sms}" '^kind: ServiceMonitor' 12
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
# The store components promote a Service label to the Prometheus job, so their
# series arrive as job=thanos-receive|thanos-store|thanos-query|thanos-compactor.
require "${enabled_sms}" 'jobLabel: osac.openshift.io/metrics-job'
expect_count "${enabled_sms}" 'jobLabel: osac.openshift.io/metrics-job' 4
for component in receive store query compactor; do
  require "${enabled_sms}" "app.kubernetes.io/component: ${component}"
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
# adapter endpoint (derived from the Service component label) is absent. The
# store-component scrapes belong to observability, so they are still present.
expect_count "${default_sms}" '^kind: ServiceMonitor' 12
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
for forbidden in MINIO_ROOT_PASSWORD secret_key: access_key:; do
  reject "${enabled}" "${forbidden}"
done
# The prerequisite gate's embedded script names the two AWS variables it
# requires in the Secret; that is a check, not credential material. The three
# workloads that read the Secret must not carry either name at all.
for forbidden in AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY; do
  for workload in thanos-receive thanos-store thanos-compactor; do
    reject "${TMP_DIR}/${workload}.yaml" "${forbidden}"
  done
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

echo "checking six network policies gate the store pods"
enabled_policies="${TMP_DIR}/thanos-networkpolicies.yaml"
extract_kind_ns "${enabled}" NetworkPolicy osac-observability >"${enabled_policies}"
expect_count "${enabled_policies}" '^kind: NetworkPolicy' 6
for policy in thanos-query thanos-authorizer thanos-store thanos-receive \
  thanos-compactor thanos-query-auth-proxy; do
  require "${enabled_policies}" "name: ${policy}"
done
# The admin Route reaches the gate on 9092. The allowlist (9091) and the two
# OAuth proxies (4180, 4181) must not be admitted from anywhere else, or a pod
# can skip the gate by connecting to the proxy pod IP.
require "${enabled_policies}" 'network.openshift.io/policy-group: ingress'
require "${enabled_policies}" 'port: 9092'
reject "${enabled_policies}" 'port: 9091'
reject "${enabled_policies}" 'port: 4180'
reject "${enabled_policies}" 'port: 4181'
require "${enabled_policies}" 'app.kubernetes.io/component: receive'
require "${enabled_policies}" 'app.kubernetes.io/component: store'
require "${enabled_policies}" 'app.kubernetes.io/component: query'
require "${enabled_policies}" 'app.kubernetes.io/component: compactor'
# Every policy admits its scrape source, so none is left with an empty ingress
# list. Query's own /metrics is on 9090 beside the admin API; admitting the
# whole openshift-monitoring namespace would let any pod there bypass the
# authorizer, so the scrape rule narrows to the CMO Prometheus pod labels.
reject "${enabled_policies}" 'ingress: []'
query_policy="${TMP_DIR}/thanos-query-policy.yaml"
extract_kind_name "${enabled}" NetworkPolicy thanos-query >"${query_policy}"
expect_count "${query_policy}" 'port: 9090' 3
require "${query_policy}" 'prometheus: k8s'
require "${query_policy}" 'app.kubernetes.io/name: prometheus'
# The Compactor serves metrics on 10902; the gRPC port 10901 stays closed.
compactor_policy="${TMP_DIR}/thanos-compactor-policy.yaml"
extract_kind_name "${enabled}" NetworkPolicy thanos-compactor >"${compactor_policy}"
require "${compactor_policy}" 'port: 10902'
reject "${compactor_policy}" 'port: 10901'
reject "${enabled_policies}" 'podSelector: {}'
reject "${enabled_policies}" 'osac.openshift.io/tenant'
reject "${enabled_policies}" 'egress:'

echo "checking the observability gate renders before install"
require "${enabled}" 'name: osac-observability-preflight'
enabled_jobs="${TMP_DIR}/enabled-jobs.yaml"
extract_kind "${enabled}" Job >"${enabled_jobs}"
require "${enabled_jobs}" 'name: osac-observability-preflight'
require "${enabled_jobs}" 'pre-install,pre-upgrade'
# Helm must not own the component namespace: a rendered Namespace object makes
# the upgrade that disables observability delete the namespace, the admin's
# Secret and the Receive and Compactor PVCs. The gate creates and labels it
# instead, then requires the object-storage Secret on every install, including
# the first one.
enabled_namespaces="${TMP_DIR}/enabled-namespaces.yaml"
extract_kind "${enabled}" Namespace >"${enabled_namespaces}"
reject "${enabled_namespaces}" 'name: osac-observability'
require "${enabled_jobs}" 'oc create namespace "${namespace}"'
require "${enabled_jobs}" \
  'oc label namespace "${namespace}" openshift.io/cluster-monitoring=true --overwrite'
require "${enabled_jobs}" 'oc get secret "${secret_name}" -n "${namespace}"'
preflight_role="${TMP_DIR}/preflight-clusterrole.yaml"
extract_kind_name "${enabled}" ClusterRole osac-observability-preflight >"${preflight_role}"
require "${preflight_role}" 'resourceNames: ["osac-observability"]'
require "${preflight_role}" 'verbs: ["get", "patch", "update"]'
# create cannot be restricted by resourceNames, so the grant is a hook-only
# ClusterRole that Helm deletes whether the gate succeeds or fails.
require "${preflight_role}" 'verbs: ["create"]'
require "${preflight_role}" 'before-hook-creation,hook-succeeded,hook-failed'

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
# Disabling removes the osac-observability cluster-monitoring label but never
# the namespace: the admin's Secret and the Receive and Compactor PVCs live
# there and must survive a disable/re-enable cycle.
unlabel_obs="${TMP_DIR}/unlabel-observability-namespace.yaml"
extract_kind_name "${disabled}" Job osac-unlabel-observability-namespace \
  >"${unlabel_obs}"
require "${unlabel_obs}" 'name: osac-unlabel-observability-namespace'
require "${unlabel_obs}" 'pre-upgrade,pre-delete'
require "${unlabel_obs}" 'openshift.io/cluster-monitoring-'
reject "${unlabel_obs}" 'create namespace'
reject "${unlabel_obs}" 'delete namespace'
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
# Enabling observability without the Prometheus Operator API would leave the
# pipeline with no scrape configuration and no error to explain it, so the
# render fails by name instead of silently dropping the ServiceMonitors. The
# disabled render above already proves the gate is scoped to observability.
expect_render_failure no-crd \
  'global.observability.enabled requires the monitoring.coreos.com/v1 ServiceMonitor API' \
  "${OBS_ARGS[@]}" "${METERING_ARGS[@]}"

echo "checking the pipeline health queries render"
health_cm="${TMP_DIR}/health-queries.yaml"
extract_kind_name "${enabled}" ConfigMap osac-observability-health-queries >"${health_cm}"
# The stopped-cluster query measures each cluster's newest sample over the
# Thanos retention window, not the five-minute instant-query lookback, and it
# takes the sample timestamp inside a subquery so the age is real.
require "${health_cm}" \
  'last_over_time(timestamp(up{managed_by=\"osac\"})[365d:30s])'
# The comment block explains why the range-function form is unusable; only the
# value of the query itself must be free of it.
reject "${health_cm}" \
  'stopped-clusters: "time() - max by (cluster) (timestamp(last_over_time('
# The row keeps the OSAC-8004 name; the shipped Receive metric is a pending
# request count rather than a queue age.
require "${health_cm}" \
  'receive-queue-length: "sum by (cluster) (thanos_receive_pending_write_requests)"'

echo "checking the managed write path stays opt-in and denylist-safe"
write="${TMP_DIR}/write-path.yaml"
"${HELM_BIN}" "${COMMON_ARGS[@]}" "${CRD_ARGS[@]}" "${OBS_ARGS[@]}" \
  --set global.observability.clusterRemoteWrite.enabled=true \
  --set global.clusterDomain=apps.example.com >"${write}"
# The CA key pair is generated once; a rotated CA key would strand every issued
# tenant certificate while the Certificate still reports Ready.
write_certs="${TMP_DIR}/write-certificates.yaml"
extract_kind_ns "${write}" Certificate osac-observability >"${write_certs}"
require "${write_certs}" 'rotationPolicy: Never'
require "${write_certs}" 'isCA: true'
# Helm must never own the denylist: a rendered ConfigMap would carry a stale
# data.revoked and every upgrade would drop the serials osac-operator appended.
no_hooks="${TMP_DIR}/write-path-no-hooks.yaml"
"${HELM_BIN}" "${COMMON_ARGS[@]}" "${CRD_ARGS[@]}" "${OBS_ARGS[@]}" \
  --set global.observability.clusterRemoteWrite.enabled=true \
  --set global.clusterDomain=apps.example.com --no-hooks >"${no_hooks}"
revocation="${TMP_DIR}/write-revocation.yaml"
extract_kind_name "${no_hooks}" ConfigMap osac-metrics-write-revocation \
  >"${revocation}"
if [ -s "${revocation}" ]; then
  fail "the revocation ConfigMap rendered as a release manifest; Helm would overwrite the denylist"
fi
# The one-shot pre-install Job creates the ConfigMap only when it is missing,
# so a reinstall keeps the denylist.
bootstrap_job="${TMP_DIR}/write-revocation-bootstrap.yaml"
extract_kind_name "${write}" Job osac-metrics-write-revocation-bootstrap \
  >"${bootstrap_job}"
require "${bootstrap_job}" '"helm.sh/hook": pre-install'
require "${bootstrap_job}" 'oc get configmap "${REVOCATION_NAME}"'
require "${bootstrap_job}" 'oc create configmap "${REVOCATION_NAME}"'
# The umbrella supplies the image the hook runs.
require "${bootstrap_job}" 'quay.io/openshift/origin-cli'

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
# Disable observability for these renders: a bad interval also fails the
# component charts' ServiceMonitor helpers, and Helm reports the subchart error
# before the umbrella one. The umbrella validates the interval on every render,
# so this still proves an empty value is not defaulted to 30s.
expect_render_failure empty-interval \
  'global.observability.collection.osacControlPlane must be a positive Prometheus duration' \
  "${CRD_ARGS[@]}" \
  --skip-schema-validation \
  "${OBS_ARGS[@]}" \
  --set global.observability.enabled=false \
  --set global.observability.collection.osacControlPlane=
expect_render_failure zero-interval \
  'must be a positive Prometheus duration' \
  "${CRD_ARGS[@]}" \
  "${OBS_ARGS[@]}" \
  --set global.observability.enabled=false \
  --set global.observability.collection.osacControlPlane=0s
expect_render_failure malformed-interval \
  'does not match pattern' \
  "${CRD_ARGS[@]}" \
  "${OBS_ARGS[@]}" \
  --set global.observability.enabled=false \
  --set global.observability.collection.osacControlPlane=30x

echo "checking thanos retention and resolution validation"
expect_render_failure zero-retention \
  'global.observability.thanos.retention must be a positive Prometheus duration' \
  "${CRD_ARGS[@]}" \
  "${OBS_ARGS[@]}" \
  --set global.observability.enabled=false \
  --set global.observability.thanos.retention=0d
expect_render_failure retention-resolution-mismatch \
  'global.observability.thanos.resolution.5m must equal global.observability.thanos.retention' \
  "${CRD_ARGS[@]}" \
  "${OBS_ARGS[@]}" \
  --set global.observability.enabled=false \
  --set global.observability.thanos.retention=30d \
  --set global.observability.thanos.resolution.raw=30d \
  --set global.observability.thanos.resolution.5m=365d \
  --set global.observability.thanos.resolution.1h=30d

echo "checking the receive replication factor fits the replica count"
# Receive rejects every write it cannot replicate, so one replica with the
# default factor of two would silently drop all remote write.
expect_render_failure replication-factor-exceeds-replicas \
  'global.observability.thanos.receive.replicationFactor (2) must not exceed global.observability.thanos.receive.replicas (1)' \
  "${CRD_ARGS[@]}" \
  "${OBS_ARGS[@]}" \
  --set global.observability.enabled=false \
  --set global.observability.thanos.receive.replicas=1

retention_ok="${TMP_DIR}/retention-ok.yaml"
"${HELM_BIN}" "${COMMON_ARGS[@]}" "${CRD_ARGS[@]}" \
  "${OBS_ARGS[@]}" \
  --set global.observability.thanos.retention=30d \
  --set global.observability.thanos.resolution.raw=30d \
  --set global.observability.thanos.resolution.5m=30d \
  --set global.observability.thanos.resolution.1h=30d >"${retention_ok}"
require "${retention_ok}" 'name: osac-observability'
# The umbrella plumbs its own retention value into the component query, so the
# stopped-cluster lookback narrows with the storage window.
retention_health="${TMP_DIR}/retention-health-queries.yaml"
extract_kind_name "${retention_ok}" ConfigMap \
  osac-observability-health-queries >"${retention_health}"
require "${retention_health}" \
  'last_over_time(timestamp(up{managed_by=\"osac\"})[30d:30s])'

echo "checking infrastructure collection interval validation"
# The schema pattern already rejects an empty string; skip it so this proves
# osac.validateValues never rewrites a missing interval to the default.
expect_render_failure empty-infrastructure \
  'global.observability.collection.infrastructure must be a positive Prometheus duration' \
  "${CRD_ARGS[@]}" \
  --skip-schema-validation \
  "${OBS_ARGS[@]}" \
  --set global.observability.enabled=false \
  --set global.observability.collection.infrastructure=

echo "checking the values descriptions match what is enforced"
# clusterRemoteWrite is consumed by osac-operator (tenant certificate issuance
# and revocation), so its description must not deny a consumer. tenant.limits
# still has none, so that description must say so. Neither may point at a later
# epic. The downsampling description must match the Compactor template, which
# passes explicit resolution flags rather than Thanos's defaults.
if grep -Fq -- 'does not deploy storage or querying' "${CHART_DIR}/values.yaml"; then
  fail "values.yaml still says the observability switch does not deploy the store"
fi
if grep -A2 '^    clusterRemoteWrite:$' "${CHART_DIR}/values.yaml" | grep -Fq -- '      enabled: true'; then
  fail "clusterRemoteWrite.enabled defaults to true; the write path must stay opt-in"
fi
python3 - "${CHART_DIR}/values.schema.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    schema = json.load(handle)
props = schema["properties"]["global"]["properties"]["observability"]["properties"]


def fail(message):
    print(f"ERROR: {message}", file=sys.stderr)
    sys.exit(1)


remote = props["clusterRemoteWrite"]
if remote["properties"]["enabled"]["default"] is not False:
    fail("clusterRemoteWrite.enabled schema default is not false")
# osac-operator now consumes these settings, so the description may not claim
# they are unenforced. The tenant limits still have no consumer and must say so.
remote_description = remote["description"]
if "no controller enforces" in remote_description.lower():
    fail(f"clusterRemoteWrite description still says it is unenforced: {remote_description!r}")
if "epic" in remote_description.lower():
    fail(f"clusterRemoteWrite description still points at a later epic: {remote_description!r}")
tenant_description = props["tenant"]["description"]
if "no controller enforces" not in tenant_description.lower():
    fail(f"tenant description does not say no controller enforces it: {tenant_description!r}")
if "epic" in tenant_description.lower():
    fail(f"tenant description still points at a later epic: {tenant_description!r}")
down = props["thanos"]["properties"]["downsampling"]["properties"]["enabled"]["description"]
if "--retention.resolution-5m" not in down or "thanos.retention" not in down:
    fail(f"downsampling description does not match the Compactor flags: {down!r}")
PY

echo 'Observability render checks passed.'
