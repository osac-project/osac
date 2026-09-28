#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
CHART_DIR=$(cd -- "${SCRIPT_DIR}/../charts/osac" && pwd)
OPERATOR_CHART_DIR=$(cd -- "${SCRIPT_DIR}/../../osac-operator/charts/operator" && pwd)
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

# Render the operator chart directly so we can exercise the
# networkManagerCapabilities helper with both array and legacy string forms
# without the umbrella schema forcing capabilities to be an array.
render_operator_success() {
  local name=$1
  local expected=$2
  shift 2

  local output="${TMP_DIR}/operator-${name}.yaml"
  echo "checking operator ${name} (expected success)"
  "${HELM_BIN}" template "op-${name}" "${OPERATOR_CHART_DIR}" "$@" >"${output}"
  if ! grep -Fq -- "${expected}" "${output}"; then
    echo "ERROR: operator ${name} rendered, but did not contain ${expected@Q}" >&2
    exit 1
  fi
}

# Agentless networking derives the Kubernetes-only manager and backend.
render_success \
  agentless \
  'k8s_manager\":\"k8s_only' \
  --set global.networking.fabricManager= \
  --set global.networking.k8sManager=k8s_only

# Facade auto-enables the k8s_only manager ConfigMap for agentless profiles.
render_success \
  agentless-manager-configmap \
  'name: osac-network-k8s-manager-k8s-only' \
  --set global.networking.fabricManager= \
  --set global.networking.k8sManager=k8s_only

# Default operator values advertise ipv4-only capabilities on manager ConfigMaps.
render_success \
  agentless-manager-capabilities \
  'capabilities: "ipv4"' \
  --set global.networking.fabricManager= \
  --set global.networking.k8sManager=k8s_only

# AgentlessNet is a distinct fabric-manager profile for the fail-fast
# unified-resource operations; its NetworkClass and AAP backend must agree.
render_success \
  agentless-net-stub \
  'fabric_manager\":\"agentless_net' \
  --set global.networking.fabricManager=agentless_net \
  --set global.networking.k8sManager=

render_success \
  agentless-net-stub-manager-configmap \
  'name: osac-network-fabric-manager-agentless-net' \
  --set global.networking.fabricManager=agentless_net \
  --set global.networking.k8sManager=

# The disposable CaaS test profile normally preserves ci.steps through the AAP
# expert override. The overlay clears it so the facade installs AgentlessNet.
render_success \
  agentless-net-stub-caas-overlay \
  'fabric_manager\":\"agentless_net' \
  --values "${SCRIPT_DIR}/../values/caas-ci/instance.yaml" \
  --values "${SCRIPT_DIR}/../values/agentless-net-stub.yaml" \
  --set-string service.externalHostname=fulfillment-api.test.example.com \
  --set-string service.internalHostname=fulfillment-internal-api.test.example.com

# Netris requires the fabric-specific connection details and derives its
# manager and NetworkClass. These are test-only placeholders, never credentials.
render_success \
  netris \
  'fabric_manager\":\"netris' \
  --set global.networking.fabricManager=netris \
  --set global.networking.k8sManager= \
  --set-string global.networking.netris.controllerUrl=https://netris.example.com \
  --set-string global.networking.netris.credentials.username=test-user \
  --set-string global.networking.netris.credentials.password="${TEST_NETRIS_PASSWORD}" \
  --set-string global.networking.netris.siteId=1 \
  --set-string global.networking.netris.tenantId=1 \
  --set-string global.networking.netris.tenantName=test

# Facade auto-enables the netris fabric manager ConfigMap.
render_success \
  netris-manager-configmap \
  'name: osac-network-fabric-manager-netris' \
  --set global.networking.fabricManager=netris \
  --set global.networking.k8sManager= \
  --set-string global.networking.netris.controllerUrl=https://netris.example.com \
  --set-string global.networking.netris.credentials.username=test-user \
  --set-string global.networking.netris.credentials.password="${TEST_NETRIS_PASSWORD}" \
  --set-string global.networking.netris.siteId=1 \
  --set-string global.networking.netris.tenantId=1 \
  --set-string global.networking.netris.tenantName=test

# An externally managed Secret is an alternative to putting the password in
# Helm values. Prefer credentials.externalSecret when the fixed AAP Secret
# netris-credentials supplies NETRIS_PASSWORD.
render_success \
  netris-external-secret \
  'fabric_manager\":\"netris' \
  --set global.networking.fabricManager=netris \
  --set global.networking.k8sManager= \
  --set-string global.networking.netris.controllerUrl=https://netris.example.com \
  --set-string global.networking.netris.credentials.username=test-user \
  --set global.networking.netris.credentials.externalSecret=true \
  --set-string global.networking.netris.siteId=1 \
  --set-string global.networking.netris.tenantId=1 \
  --set-string global.networking.netris.tenantName=test

# Inline passwords and externalSecret are mutually exclusive.
render_failure \
  netris-both-password-sources \
  --set global.networking.fabricManager=netris \
  --set global.networking.k8sManager= \
  --set-string global.networking.netris.controllerUrl=https://netris.example.com \
  --set-string global.networking.netris.credentials.username=test-user \
  --set-string global.networking.netris.credentials.password="${TEST_NETRIS_PASSWORD}" \
  --set global.networking.netris.credentials.externalSecret=true \
  --set-string global.networking.netris.siteId=1 \
  --set-string global.networking.netris.tenantId=1 \
  --set-string global.networking.netris.tenantName=test

# An explicit NetworkClass field takes precedence over the generated value.
render_success \
  network-class-override \
  'title\":\"Custom network' \
  --set global.networking.fabricManager= \
  --set global.networking.k8sManager=k8s_only \
  --set-string global.networking.networkClass.title=Custom\ network

# East-west NetworkClass configuration is schema-validated and sent in the
# private API's spec using the protobuf JSON field names.
render_success \
  network-class-east-west-config \
  'east_west_config\":{\"ethernet_ew\":{\"template_id\":\"fabric-domain-template\"' \
  --set global.networking.fabricManager=netris \
  --set global.networking.k8sManager= \
  --set-string global.networking.netris.controllerUrl=https://netris.example.com \
  --set-string global.networking.netris.credentials.username=test-user \
  --set global.networking.netris.credentials.externalSecret=true \
  --set-string global.networking.netris.siteId=1 \
  --set-string global.networking.netris.tenantId=1 \
  --set-string global.networking.netris.tenantName=test \
  --set-string global.networking.networkClass.east_west_config.ethernet_ew.template_id=fabric-domain-template

render_success \
  legacy-network-class-east-west-config \
  'east_west_config\":{\"ethernet_ew\":{\"template_id\":\"legacy-template\"' \
  --set global.expertOverrides.networkClass=true \
  --set networkClass.enabled=true \
  --set-string networkClass.title=Legacy\ networking \
  --set networkClass.k8sManager=k8s_only \
  --set networkClass.fabricManager= \
  --set-string networkClass.east_west_config.ethernet_ew.template_id=legacy-template

# The umbrella chart already installs the operator's generated CRD subchart;
# assert that FabricDomain remains part of the rendered deployment.
render_success \
  fabricdomain-crd \
  'name: fabricdomains.osac.openshift.io'

render_failure \
  network-class-east-west-missing-template \
  --set global.networking.fabricManager= \
  --set global.networking.k8sManager=k8s_only \
  --set-string global.networking.networkClass.east_west_config.ethernet_ew.template_id=

# Port ranges are meaningful only for TCP and UDP rules.
render_failure \
  icmp-with-ports \
  --set global.networking.fabricManager= \
  --set global.networking.k8sManager=k8s_only \
  --set global.networking.networkClass.defaults.egressRules[0].protocol=PROTOCOL_ICMP \
  --set global.networking.networkClass.defaults.egressRules[0].portFrom=80 \
  --set global.networking.networkClass.defaults.egressRules[0].portTo=443

render_failure \
  all-with-ports \
  --set global.networking.fabricManager= \
  --set global.networking.k8sManager=k8s_only \
  --set global.networking.networkClass.defaults.egressRules[0].protocol=PROTOCOL_ALL \
  --set global.networking.networkClass.defaults.egressRules[0].portFrom=80 \
  --set global.networking.networkClass.defaults.egressRules[0].portTo=443

# Required fabric details and unsupported manager combinations must
# fail before Helm produces installable manifests.
render_failure \
  netris-missing-details \
  --set global.networking.fabricManager=netris \
  --set global.networking.k8sManager=

# CUDN has no facade implementation yet and must fail at render time.
render_failure \
  cudn-not-implemented \
  --set global.networking.fabricManager=cudn_net \
  --set global.networking.k8sManager=

render_failure \
  legacy-fabric-manager-facade \
  --set global.fabricManager.netris.enabled=true

render_failure \
  legacy-k8s-manager-facade \
  --set global.k8sManager.agentlessNet.enabled=true

render_failure \
  removed-provider-key \
  --set global.networking.provider=netris

render_failure \
  removed-overlay-key \
  --set global.networking.overlay=k8s_only

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
  netris-with-k8s-only \
  --set global.networking.fabricManager=netris \
  --set global.networking.k8sManager=k8s_only \
  --set-string global.networking.netris.controllerUrl=https://netris.example.com \
  --set-string global.networking.netris.credentials.username=test-user \
  --set-string global.networking.netris.credentials.password="${TEST_NETRIS_PASSWORD}" \
  --set-string global.networking.netris.siteId=1 \
  --set-string global.networking.netris.tenantId=1 \
  --set-string global.networking.netris.tenantName=test

render_failure \
  vlan-not-implemented \
  --set global.networking.fabricManager=vlan \
  --set global.networking.k8sManager=

render_failure \
  k8s-only-with-fabric-manager \
  --set global.networking.fabricManager= \
  --set global.networking.k8sManager=k8s_only \
  --set global.networking.networkClass.fabricManager=netris

render_failure \
  no-managers \
  --set global.networking.fabricManager= \
  --set global.networking.k8sManager=

# networkManagerCapabilities helper: array form (join with commas).
render_operator_success \
  capabilities-array \
  'capabilities: "ipv4"' \
  --set networkManagers.enabled=true \
  --set networkManagers.k8sManagers.k8s_only.enabled=true \
  --set networkManagers.k8sManagers.k8s_only.capabilities[0]=ipv4

# networkManagerCapabilities helper: legacy string form (pass through).
render_operator_success \
  capabilities-string \
  'capabilities: "ipv4"' \
  --set networkManagers.enabled=true \
  --set networkManagers.k8sManagers.k8s_only.enabled=true \
  --set-string networkManagers.k8sManagers.k8s_only.capabilities=ipv4

# Facade on the operator chart auto-enables k8s_only when k8sManager is k8s_only.
render_operator_success \
  facade-auto-enable-k8s-only \
  'name: osac-network-k8s-manager-k8s-only' \
  --set networkManagers.enabled=true \
  --set global.networking.fabricManager= \
  --set global.networking.k8sManager=k8s_only

render_operator_success \
  facade-auto-enable-agentless-net \
  'name: osac-network-fabric-manager-agentless-net' \
  --set networkManagers.enabled=true \
  --set global.networking.fabricManager=agentless_net \
  --set global.networking.k8sManager=

echo "networking values validation passed"
