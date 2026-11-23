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

expect_count() {
  local file=$1 pattern=$2 expected=$3 actual
  actual=$(grep -c -- "${pattern}" "${file}" || true)
  if [ "${actual}" != "${expected}" ]; then
    fail "expected ${expected} occurrences of ${pattern@Q} in ${file}, found ${actual}"
  fi
}

# Extract the documents of one kind so workload assertions cannot match an
# unrelated object in the umbrella render.
extract_kind() {
  local file=$1 kind=$2
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

# Extract one kind with one metadata name, so per-route and per-proxy
# assertions cannot match a sibling object of the same kind.
extract_kind_name() {
  local file=$1 kind=$2 name=$3
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

# Extract one kind that also carries a namespace, so the checks cannot match a
# same-kind object owned by another component of the umbrella chart.
extract_kind_ns() {
  local file=$1 kind=$2 namespace=$3
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
  # The chart refuses an empty image tag; these renders never pull.
  --set global.observability.services.metricsAuth.tag=test-metrics-auth
  --set global.observability.thanos.image.tag=test-thanos
  --set global.observability.services.oauth2Proxy.tag=test-oauth2-proxy
  --set global.observability.services.openshiftOauthProxy.tag=test-ose-oauth-proxy
  # The gate refuses an empty issuer; the render never reaches Keycloak.
  --set global.observability.keycloak.issuerUrl=https://keycloak.example.com/realms/osac
)

enabled="${TMP_DIR}/enabled.yaml"
disabled="${TMP_DIR}/disabled.yaml"

"${HELM_BIN}" "${COMMON_ARGS[@]}" "${OBS_ARGS[@]}" >"${enabled}"
"${HELM_BIN}" "${COMMON_ARGS[@]}" >"${disabled}"

echo "checking the observability namespace is not owned by Helm"
# A rendered Namespace object is deleted by the upgrade that disables
# observability, which would also delete the admin's Secret and the Receive and
# Compactor PVCs. The pre-install gate creates and labels the namespace instead
# and only removes the label when observability is disabled.
extract_kind "${enabled}" Namespace >"${TMP_DIR}/enabled-namespace.yaml"
extract_kind "${disabled}" Namespace >"${TMP_DIR}/disabled-namespace.yaml"
reject "${TMP_DIR}/enabled-namespace.yaml" 'name: osac-observability'
if [ -s "${TMP_DIR}/disabled-namespace.yaml" ]; then
  fail "the observability namespace rendered while disabled"
fi
extract_kind "${enabled}" Job >"${TMP_DIR}/enabled-hook-jobs.yaml"
require "${TMP_DIR}/enabled-hook-jobs.yaml" 'oc create namespace "${namespace}"'
require "${TMP_DIR}/enabled-hook-jobs.yaml" \
  'oc label namespace "${namespace}" openshift.io/cluster-monitoring=true --overwrite'
extract_kind "${disabled}" Job >"${TMP_DIR}/disabled-hook-jobs.yaml"
require "${TMP_DIR}/disabled-hook-jobs.yaml" \
  'kubectl label namespace osac-observability openshift.io/cluster-monitoring-'

echo "checking the four Thanos workloads render in the component namespace"
for kind_name in \
  'StatefulSet:thanos-receive' \
  'Deployment:thanos-store' \
  'Deployment:thanos-query' \
  'StatefulSet:thanos-compactor'; do
  kind=${kind_name%%:*}
  name=${kind_name##*:}
  extract_kind "${enabled}" "${kind}" >"${TMP_DIR}/${name}.yaml"
  require "${TMP_DIR}/${name}.yaml" "name: ${name}"
  require "${TMP_DIR}/${name}.yaml" 'namespace: osac-observability'
done

echo "checking remote write is served over TLS on the service-CA certificate"
require "${enabled}" 'service.beta.openshift.io/serving-cert-secret-name: thanos-receive-tls'
require "${enabled}" 'name: remote-write'
require "${enabled}" 'port: 19291'
require "${TMP_DIR}/thanos-receive.yaml" '--remote-write.server-tls-cert=/etc/thanos/tls/tls.crt'
require "${TMP_DIR}/thanos-receive.yaml" '--remote-write.server-tls-key=/etc/thanos/tls/tls.key'

echo "checking the Compactor keeps downsampling off by default"
require "${TMP_DIR}/thanos-compactor.yaml" '--retention.resolution-raw=365d'
require "${TMP_DIR}/thanos-compactor.yaml" '--downsampling.disable'
reject "${TMP_DIR}/thanos-compactor.yaml" '--retention.resolution-5m'
reject "${TMP_DIR}/thanos-compactor.yaml" '--retention.resolution-1h'

echo "checking six network policies gate the store pods"
policies="${TMP_DIR}/networkpolicies.yaml"
extract_kind "${enabled}" NetworkPolicy >"${policies}"
expect_count "${policies}" '^kind: NetworkPolicy' 6
for policy in thanos-query thanos-authorizer thanos-store thanos-receive \
  thanos-compactor thanos-query-auth-proxy; do
  require "${policies}" "name: ${policy}"
done
# Only the gate port is admitted from the router. The allowlist (9091) and the
# OAuth proxies (4180, 4181) must stay unreachable from other pods, or the
# token check can be skipped by connecting to the proxy pod IP.
require "${policies}" 'network.openshift.io/policy-group: ingress'
require "${policies}" 'port: 9092'
reject "${policies}" 'port: 9091'
reject "${policies}" 'port: 4180'
reject "${policies}" 'port: 4181'
require "${policies}" 'app.kubernetes.io/component: compactor'
reject "${policies}" 'podSelector: {}'
reject "${policies}" 'osac.openshift.io/tenant'
reject "${policies}" 'egress:'
# Query serves its own /metrics on 9090, so cluster monitoring reaches that one
# port with the CMO Prometheus pods as the source. Admitting the namespace alone
# would let any pod in openshift-monitoring skip the authorizer.
query_policy="${TMP_DIR}/networkpolicy-query.yaml"
extract_kind_name "${enabled}" NetworkPolicy thanos-query >"${query_policy}"
require "${query_policy}" 'port: 9090'
expect_count "${query_policy}" 'port: 9090' 3
require "${query_policy}" 'kubernetes.io/metadata.name: openshift-monitoring'
require "${query_policy}" 'prometheus: k8s'
require "${query_policy}" 'app.kubernetes.io/name: prometheus'
# The Compactor serves Prometheus metrics on 10902; its gRPC port 10901 stays
# closed to every source, so the metrics scrape is its only admitted rule.
compactor_policy="${TMP_DIR}/networkpolicy-compactor.yaml"
extract_kind_name "${enabled}" NetworkPolicy thanos-compactor >"${compactor_policy}"
require "${compactor_policy}" 'port: 10902'
reject "${compactor_policy}" 'port: 10901'
reject "${compactor_policy}" 'ingress: []'

echo "checking the remote-write Job follows the switch"
extract_kind "${enabled}" Job >"${TMP_DIR}/enabled-jobs.yaml"
extract_kind "${disabled}" Job >"${TMP_DIR}/disabled-jobs.yaml"
require "${TMP_DIR}/enabled-jobs.yaml" 'name: osac-observability-remote-write'
require "${TMP_DIR}/enabled-jobs.yaml" 'value: "true"'
# A fresh disabled render has no in-cluster ConfigMap to inspect, so it must
# not create anything in openshift-monitoring.
reject "${TMP_DIR}/disabled-jobs.yaml" 'name: osac-observability-remote-write'
reject "${disabled}" 'namespace: openshift-monitoring'
require "${TMP_DIR}/enabled-jobs.yaml" 'value: "30s"'
require "${TMP_DIR}/enabled-jobs.yaml" \
  'value: https://thanos-receive.osac-observability.svc.cluster.local:19291/api/v1/receive'

echo "checking the admin query routes and proxies"
# The Routes take their hosts from global.clusterDomain, which the enabled
# render leaves empty, so render one revision with the domain set.
admin="${TMP_DIR}/admin.yaml"
"${HELM_BIN}" "${COMMON_ARGS[@]}" "${OBS_ARGS[@]}" \
  --set global.clusterDomain=apps.example.com >"${admin}"
admin_routes="${TMP_DIR}/admin-routes.yaml"
extract_kind_ns "${admin}" Route osac-observability >"${admin_routes}"
# Exactly one Route per admin path: a Route to Thanos Query would expose the
# unrestricted API beside the allowlisted one.
expect_count "${admin_routes}" '^kind: Route' 2
for route in thanos-query-admin thanos-query-admin-openshift; do
  route_doc="${TMP_DIR}/route-${route}.yaml"
  extract_kind_name "${admin}" Route "${route}" >"${route_doc}"
  require "${route_doc}" "name: ${route}"
  require "${route_doc}" "host: ${route}-osac-observability.apps.example.com"
  # Each Route reaches its own proxy Service, never thanos-query directly.
  expect_count "${route_doc}" "^    name: ${route}\$" 1
  service_doc="${TMP_DIR}/service-${route}.yaml"
  extract_kind_name "${admin}" Service "${route}" >"${service_doc}"
  require "${service_doc}" 'port: 9091'
  require "${service_doc}" 'targetPort: gate'
  require "${service_doc}" 'app.kubernetes.io/component: query-auth-proxy'
done
expect_count "${admin_routes}" '^    name: thanos-query$' 0

# Both proxies serve the same five-endpoint allowlist through one shared
# implementation, so both must mount the same ConfigMap.
for proxy in thanos-query-admin thanos-query-admin-openshift; do
  proxy_doc="${TMP_DIR}/deployment-${proxy}.yaml"
  extract_kind_name "${enabled}" Deployment "${proxy}" >"${proxy_doc}"
  require "${proxy_doc}" "name: ${proxy}"
  require "${proxy_doc}" 'osac.openshift.io/observability-role: query-auth-proxy'
  # The gate is the only way in: it answers 401/403 itself and forwards a
  # cookie session to the proxy, which forwards to the allowlist sidecar.
  require "${proxy_doc}" '- name: query-auth-gate'
  require "${proxy_doc}" '- /query-auth-gate'
  require "${proxy_doc}" '- name: query-allowlist'
  require "${proxy_doc}" 'name: osac-observability-query-allowlist'
  require "${proxy_doc}" '--upstream=http://127.0.0.1:9091'
  require "${proxy_doc}" \
    'value: http://thanos-query.osac-observability.svc.cluster.local:9090'
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

echo "checking the managed write path"
write_path="${TMP_DIR}/write-path.yaml"
"${HELM_BIN}" "${COMMON_ARGS[@]}" "${OBS_ARGS[@]}" \
  --set global.observability.clusterRemoteWrite.enabled=true \
  --set global.clusterDomain=apps.example.com >"${write_path}"
# A self-signed bootstrap Issuer, the CA Certificate and the CA Issuer. The CA
# key pair lives only in cert-manager's Secret.
extract_kind_ns "${write_path}" Issuer osac-observability \
  >"${TMP_DIR}/write-issuers.yaml"
expect_count "${TMP_DIR}/write-issuers.yaml" '^kind: Issuer' 2
require "${TMP_DIR}/write-issuers.yaml" 'name: osac-metrics-write-ca-selfsigned'
require "${TMP_DIR}/write-issuers.yaml" 'selfSigned: {}'
require "${TMP_DIR}/write-issuers.yaml" 'secretName: osac-metrics-write-ca'
require "${TMP_DIR}/write-issuers.yaml" 'namespace: osac-observability'
extract_kind_ns "${write_path}" Certificate osac-observability \
  >"${TMP_DIR}/write-certs.yaml"
expect_count "${TMP_DIR}/write-certs.yaml" '^kind: Certificate' 2
require "${TMP_DIR}/write-certs.yaml" 'name: osac-metrics-write-ca-cert'
require "${TMP_DIR}/write-certs.yaml" 'isCA: true'
require "${TMP_DIR}/write-certs.yaml" 'name: thanos-receive-managed-tls'
require "${TMP_DIR}/write-certs.yaml" 'duration: 90d'
require "${TMP_DIR}/write-certs.yaml" 'renewBefore: 30d'
# Passthrough TLS means the tenant cluster verifies the external Route host.
require "${TMP_DIR}/write-certs.yaml" \
  'thanos-receive-managed-osac-observability.apps.example.com'
# No key material may appear in a rendered template or in `helm get values`.
reject "${TMP_DIR}/write-certs.yaml" 'BEGIN CERTIFICATE'
reject "${TMP_DIR}/write-certs.yaml" 'PRIVATE KEY'
reject "${TMP_DIR}/write-certs.yaml" 'tls.key:'

echo "checking Helm never owns the revocation denylist"
# A rendered ConfigMap would carry a stale data.revoked, and each upgrade would
# drop the serials osac-operator appended. Under --no-hooks the release carries
# no revocation object at all; the normal render carries only the bootstrap
# hook, which creates the ConfigMap when it is missing and never overwrites it.
"${HELM_BIN}" "${COMMON_ARGS[@]}" "${OBS_ARGS[@]}" \
  --set global.observability.clusterRemoteWrite.enabled=true \
  --set global.clusterDomain=apps.example.com \
  --no-hooks >"${TMP_DIR}/write-path-no-hooks.yaml"
revocation_release="${TMP_DIR}/write-revocation-release.yaml"
extract_kind_name "${TMP_DIR}/write-path-no-hooks.yaml" ConfigMap \
  osac-metrics-write-revocation >"${revocation_release}"
if [ -s "${revocation_release}" ]; then
  fail "the revocation ConfigMap rendered as a release manifest; Helm would overwrite the denylist"
fi
revocation_cm="${TMP_DIR}/write-revocation-cm.yaml"
extract_kind_name "${write_path}" ConfigMap osac-metrics-write-revocation \
  >"${revocation_cm}"
if [ -s "${revocation_cm}" ]; then
  fail "the revocation ConfigMap rendered outside the bootstrap hook"
fi
bootstrap_job="${TMP_DIR}/revocation-bootstrap.yaml"
extract_kind_name "${write_path}" Job osac-metrics-write-revocation-bootstrap \
  >"${bootstrap_job}"
# Helm quotes annotation keys that contain a slash.
require "${bootstrap_job}" '"helm.sh/hook": pre-install'
require "${bootstrap_job}" \
  '"helm.sh/hook-delete-policy": before-hook-creation,hook-succeeded,hook-failed'
require "${bootstrap_job}" 'oc get configmap "${REVOCATION_NAME}"'
require "${bootstrap_job}" 'oc create configmap "${REVOCATION_NAME}"'
require "${bootstrap_job}" 'osac-metrics-write-revocation'

echo "checking the single write Route passes TLS through to the proxy"
write_route="${TMP_DIR}/write-route.yaml"
extract_kind_name "${write_path}" Route thanos-receive-managed >"${write_route}"
require "${write_route}" 'name: thanos-receive-managed'
require "${write_route}" \
  'host: thanos-receive-managed-osac-observability.apps.example.com'
require "${write_route}" 'termination: passthrough'
require "${write_route}" 'insecureEdgeTerminationPolicy: None'
require "${write_route}" 'targetPort: https'
expect_count "${write_route}" "^    name: thanos-receive-managed\$" 1
reject "${write_route}" 'termination: edge'

echo "checking the write proxy verifies certificates and enforces the drop rule"
write_proxy="${TMP_DIR}/write-proxy.yaml"
extract_kind_name "${write_path}" Deployment thanos-receive-managed >"${write_proxy}"
require "${write_proxy}" 'osac.openshift.io/observability-role: write-proxy'
require "${write_proxy}" 'automountServiceAccountToken: true'
require "${write_proxy}" '--client-ca-file=/etc/write-ca/ca.crt'
require "${write_proxy}" \
  '--upstream-ca-file=/etc/service-ca/service-ca.crt'
require "${write_proxy}" \
  '--upstream=https://thanos-receive.osac-observability.svc.cluster.local:19291/api/v1/receive'
require "${write_proxy}" '--revocation-name=osac-metrics-write-revocation'
require "${write_proxy}" '--keep-kube-prefix=kube_node_'
require "${write_proxy}" \
  '--drop-prefixes=container_,claim,mount,kserve_,vllm_,dcgm_'
require "${write_proxy}" 'secretName: osac-metrics-write-ca'
# Only the CA certificate is mounted, never its private key.
require "${write_proxy}" 'key: tls.crt'
reject "${write_proxy}" 'key: tls.key'
require "${write_proxy}" 'secretName: thanos-receive-tls'
service_doc="${TMP_DIR}/service-thanos-receive-managed.yaml"
extract_kind_name "${write_path}" Service thanos-receive-managed \
  >"${service_doc}"
require "${service_doc}" 'port: 8443'
require "${service_doc}" 'targetPort: https'
require "${service_doc}" 'app.kubernetes.io/component: write-proxy'

echo "checking the write proxy admits only the router on its TLS port"
write_policy="${TMP_DIR}/networkpolicy-write-proxy.yaml"
extract_kind_name "${write_path}" NetworkPolicy thanos-write-proxy \
  >"${write_policy}"
require "${write_policy}" 'osac.openshift.io/observability-role: write-proxy'
require "${write_policy}" 'network.openshift.io/policy-group: ingress'
require "${write_policy}" 'port: 8443'
expect_count "${write_policy}" 'port: 8443' 1
# 8080 serves only health probes; no Service exposes it and nothing else may
# reach the pod IP.
reject "${write_policy}" 'port: 8080'

echo "checking the write path stays off unless it is enabled"
reject "${enabled}" 'osac-metrics-write-ca'
# The operator still names the denylist while the flag is off, because that is
# how an identity issued earlier is withdrawn; no object may take the name.
reject "${enabled}" 'name: osac-metrics-write-revocation'
reject "${disabled}" 'osac-metrics-write-ca'
reject "${disabled}" 'thanos-receive-managed'

echo "checking the disabled path removes the admin surface"
for name in thanos-query-admin thanos-query-admin-openshift \
  osac-observability-query-allowlist osac-observability-admin-credentials; do
  reject "${disabled}" "name: ${name}"
done

echo "checking object storage is referenced by Secret name only"
require "${enabled}" 'bucket: "thanos"'
require "${enabled}" 'endpoint: "https://s3.example.com"'
require "${TMP_DIR}/thanos-receive.yaml" 'secretRef:'
objstore="${TMP_DIR}/object-storage-configmap.yaml"
extract_kind_name "${enabled}" ConfigMap thanos-object-storage >"${objstore}"
# Without aws_sdk_auth Thanos ignores the pod credentials and falls back to
# anonymous access, so the bucket reads fail even though the Secret exists.
require "${objstore}" 'aws_sdk_auth: true'
reject "${objstore}" 'access_key'
reject "${objstore}" 'secret_key'
reject "${enabled}" 'MINIO_ROOT_PASSWORD'
reject "${enabled}" 'secretKey:'
# The prerequisite gate's embedded script names the two AWS variables it
# requires in the Secret; that is a check, not credential material. The
# workloads that consume the Secret must still not carry the values.
for forbidden in AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY; do
  for workload in thanos-receive thanos-store thanos-compactor; do
    reject "${TMP_DIR}/${workload}.yaml" "${forbidden}"
  done
done
extract_kind "${enabled}" Secret >"${TMP_DIR}/secrets.yaml"
reject "${TMP_DIR}/secrets.yaml" 'namespace: osac-observability'

echo "checking the stopped-cluster query names a cluster that stopped before the lookback"
# Evaluate the rendered expression against synthetic series. The test file is
# generated from the render, so the expression under test is the one shipped.
# With a five-minute collection interval the threshold is 330s, which is longer
# than the five-minute instant-query lookback; the stale cluster stopped at 4m,
# so at 10m its age is 360s and it must still be named. The fresh cluster's age
# is 60s and it must be absent. A query that cannot see past the lookback, or
# that reports the evaluation time instead of the sample timestamp, names
# neither cluster and fails this check.
"${HELM_BIN}" "${COMMON_ARGS[@]}" "${OBS_ARGS[@]}" \
  --set global.observability.collection.osacControlPlane=5m \
  >"${TMP_DIR}/enabled-5m.yaml"
health="${TMP_DIR}/health-queries-5m.yaml"
extract_kind_name "${TMP_DIR}/enabled-5m.yaml" ConfigMap \
  osac-observability-health-queries >"${health}"
expr=$(sed -n 's/^  stopped-clusters: "\(.*\)"$/\1/p' "${health}" | sed 's/\\"/"/g')
[ -n "${expr}" ] || fail "the health ConfigMap has no stopped-clusters expression"
promql_dir="${TMP_DIR}/promql"
mkdir -p "${promql_dir}"
cat >"${promql_dir}/stopped-clusters.test.yaml" <<EOF
tests:
  - interval: 1m
    input_series:
      - series: 'up{managed_by="osac",cluster="stale"}'
        values: '1x4 _x20'
      - series: 'up{managed_by="osac",cluster="fresh"}'
        values: '1x10'
    promql_expr_test:
      # Comparisons return the left-hand value, not 1, so this asserts the age.
      - expr: '${expr}'
        eval_time: 10m
        exp_samples:
          - labels: '{cluster="stale"}'
            value: 360
EOF
promtool_bin="${PROMTOOL_BIN:-}"
if [ -z "${promtool_bin}" ] && command -v promtool >/dev/null 2>&1; then
  promtool_bin=$(command -v promtool)
fi
if [ -n "${promtool_bin}" ]; then
  "${promtool_bin}" test rules "${promql_dir}/stopped-clusters.test.yaml"
else
  echo "SKIP: promtool was not found, so the stopped-cluster PromQL semantics were NOT evaluated." >&2
  echo "SKIP: install promtool or set PROMTOOL_BIN to run this check." >&2
fi

echo "Observability component render checks passed."
