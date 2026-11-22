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
extract_kind "${enabled}" Namespace >"${TMP_DIR}/enabled-namespace.yaml"
extract_kind "${disabled}" Namespace >"${TMP_DIR}/disabled-namespace.yaml"
require "${TMP_DIR}/enabled-namespace.yaml" 'name: osac-observability'
if [ -s "${TMP_DIR}/disabled-namespace.yaml" ]; then
  fail "the observability namespace rendered while disabled"
fi

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

echo "checking four network policies gate the store pods"
extract_kind "${enabled}" NetworkPolicy >"${TMP_DIR}/networkpolicies.yaml"
for policy in thanos-query thanos-authorizer thanos-store thanos-receive; do
  require "${TMP_DIR}/networkpolicies.yaml" "name: ${policy}"
done
reject "${TMP_DIR}/networkpolicies.yaml" 'podSelector: {}'
reject "${TMP_DIR}/networkpolicies.yaml" 'osac.openshift.io/tenant'
reject "${TMP_DIR}/networkpolicies.yaml" 'egress:'

echo "checking the remote-write Job follows the switch"
extract_kind "${enabled}" Job >"${TMP_DIR}/enabled-jobs.yaml"
extract_kind "${disabled}" Job >"${TMP_DIR}/disabled-jobs.yaml"
require "${TMP_DIR}/enabled-jobs.yaml" 'name: osac-observability-remote-write'
require "${TMP_DIR}/disabled-jobs.yaml" 'name: osac-observability-remote-write'
require "${TMP_DIR}/enabled-jobs.yaml" 'value: "true"'
require "${TMP_DIR}/disabled-jobs.yaml" 'value: "false"'
require "${TMP_DIR}/enabled-jobs.yaml" 'value: "30s"'
require "${TMP_DIR}/enabled-jobs.yaml" \
  'value: https://thanos-receive.osac-observability.svc.cluster.local:19291/api/v1/receive'

echo "checking object storage is referenced by Secret name only"
require "${enabled}" 'bucket: "thanos"'
require "${enabled}" 'endpoint: "https://s3.example.com"'
require "${TMP_DIR}/thanos-receive.yaml" 'secretRef:'
reject "${enabled}" 'AWS_SECRET_ACCESS_KEY'
reject "${enabled}" 'MINIO_ROOT_PASSWORD'
reject "${enabled}" 'secretKey:'
extract_kind "${enabled}" Secret >"${TMP_DIR}/secrets.yaml"
reject "${TMP_DIR}/secrets.yaml" 'namespace: osac-observability'

echo "Observability component render checks passed."
