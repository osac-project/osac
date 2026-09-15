#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
CHART_DIR=$(cd -- "${SCRIPT_DIR}/../charts/osac" && pwd)
HELM_BIN=${HELM_BIN:-helm}
TMP_DIR=$(mktemp -d)
trap 'rm -rf "${TMP_DIR}"' EXIT
TEST_NETRIS_PASSWORD=${TEST_NETRIS_PASSWORD:-ci-validation-$(date +%s%N)}

COMMON_ARGS=(
  template osac "${CHART_DIR}"
  --values "${CHART_DIR}/ci/default-values.yaml"
)

render_success() {
  local name=$1
  local expected=$2
  shift 2

  local output="${TMP_DIR}/${name}.yaml"
  echo "checking ${name} (expected success)"
  "${HELM_BIN}" "${COMMON_ARGS[@]}" "$@" >"${output}"
  if ! grep -Fq -- "${expected}" "${output}"; then
    echo "ERROR: ${name} rendered, but did not contain ${expected@Q}" >&2
    exit 1
  fi
}

render_failure() {
  local name=$1
  shift

  local error="${TMP_DIR}/${name}.stderr"
  echo "checking ${name} (expected failure)"
  if "${HELM_BIN}" "${COMMON_ARGS[@]}" "$@" >"/dev/null" 2>"${error}"; then
    echo "ERROR: ${name} unexpectedly rendered successfully" >&2
    exit 1
  fi
}

# The default profile derives the CUDN manager and NetworkClass.
render_success \
  default-cudn \
  'fabric_manager\":\"cudn_net' \
  --set global.networking.provider=cudn \
  --set global.networking.overlay=none

# Agentless networking derives the Kubernetes-only manager and backend.
render_success \
  agentless \
  'k8s_manager\":\"k8s_only' \
  --set global.networking.provider=none \
  --set global.networking.overlay=k8s_only

# Netris requires the provider-specific connection details and derives its
# manager and NetworkClass. These are test-only placeholders, never credentials.
render_success \
  netris \
  'fabric_manager\":\"netris' \
  --set global.networking.provider=netris \
  --set global.networking.overlay=none \
  --set-string global.networking.netris.controllerUrl=https://netris.example.com \
  --set-string global.networking.netris.credentials.username=test-user \
  --set-string global.networking.netris.credentials.password="${TEST_NETRIS_PASSWORD}" \
  --set-string global.networking.netris.siteId=1 \
  --set-string global.networking.netris.tenantId=1 \
  --set-string global.networking.netris.tenantName=test

# An externally managed Secret is an alternative to putting the password in
# Helm values. The chart must render the corresponding optional Secret mount.
render_success \
  netris-external-secret \
  'fabric_manager\":\"netris' \
  --set global.networking.provider=netris \
  --set global.networking.overlay=none \
  --set-string global.networking.netris.controllerUrl=https://netris.example.com \
  --set-string global.networking.netris.credentials.username=test-user \
  --set-string global.networking.netris.credentials.passwordSecretRef.name=netris-credentials \
  --set-string global.networking.netris.credentials.passwordSecretRef.key=NETRIS_PASSWORD \
  --set-string global.networking.netris.siteId=1 \
  --set-string global.networking.netris.tenantId=1 \
  --set-string global.networking.netris.tenantName=test

# An explicit NetworkClass field takes precedence over the generated value.
render_success \
  network-class-override \
  'title\":\"Custom network' \
  --set global.networking.provider=cudn \
  --set global.networking.overlay=none \
  --set-string global.networking.networkClass.title=Custom\ network

# Required provider details and unsupported provider/overlay combinations must
# fail before Helm produces installable manifests.
render_failure \
  netris-missing-details \
  --set global.networking.provider=netris \
  --set global.networking.overlay=none

render_failure \
  legacy-fabric-manager-facade \
  --set global.fabricManager.netris.enabled=true

render_failure \
  legacy-k8s-manager-facade \
  --set global.k8sManager.agentlessNet.enabled=true

render_failure \
  legacy-provider-in-new-facade \
  --set global.networking.provider=esi

render_failure \
  legacy-network-class \
  --set global.expertOverrides.aap=true \
  --set aap.instanceGroups.clusterFulfillment.enabled=true \
  --set-string aap.instanceGroups.clusterFulfillment.config.NETWORK_CLASS=esi

render_failure \
  legacy-network-steps-collection \
  --set global.expertOverrides.aap=true \
  --set aap.instanceGroups.clusterFulfillment.enabled=true \
  --set-string aap.instanceGroups.clusterFulfillment.config.NETWORK_STEPS_COLLECTION=nico.steps

render_failure \
  k8s-only-with-fabric-manager \
  --set global.networking.provider=none \
  --set global.networking.overlay=k8s_only \
  --set global.networking.networkClass.fabricManager=netris

render_failure \
  cudn-evpn-not-implemented \
  --set global.networking.provider=cudn \
  --set global.networking.overlay=cudn_evpn

render_failure \
  no-provider-or-overlay \
  --set global.networking.provider=none \
  --set global.networking.overlay=none

echo "networking values validation passed"
