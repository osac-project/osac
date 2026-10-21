#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
CHART_DIR=$(cd -- "${SCRIPT_DIR}/../charts/osac" && pwd)
OPERATOR_CHART_DIR=$(cd -- "${SCRIPT_DIR}/../../osac-operator/charts/operator" && pwd)
AAP_CHART_DIR=$(cd -- "${SCRIPT_DIR}/../../osac-aap/charts/aap" && pwd)
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

render_ipv4_only_manager_success() {
  local name=$1
  shift

  local output="${TMP_DIR}/${name}.yaml"
  echo "checking ${name} (expected IPv4-only manager declarations)"
  "${HELM_BIN}" "${COMMON_ARGS[@]}" "$@" >"${output}"
  if ! grep -Fq -- 'capabilities: "ipv4"' "${output}"; then
    echo "ERROR: ${name} rendered, but did not contain the IPv4 manager capability" >&2
    exit 1
  fi
  if grep -Ein -- 'capabilities: "[^"]*(ipv6|dual[_-]?stack)' "${output}"; then
    echo "ERROR: ${name} rendered an IPv6 or dual-stack manager capability" >&2
    exit 1
  else
    search_status=$?
    if [[ ${search_status} -ne 1 ]]; then
      echo "ERROR: failed to check ${name} for IPv6 or dual-stack manager capabilities" >&2
      exit 1
    fi
  fi
}

render_ipv4_only_network_class_success() {
  local name=$1
  shift

  local output="${TMP_DIR}/${name}.yaml"
  echo "checking ${name} (expected IPv4-only NetworkClass defaults)"
  "${HELM_BIN}" "${COMMON_ARGS[@]}" "$@" >"${output}"
  if ! grep -Fq -- '\"ipv4_cidr\":\"0.0.0.0/0\"' "${output}"; then
    echo "ERROR: ${name} rendered, but did not contain the IPv4 egress default" >&2
    exit 1
  fi
  if grep -Fq -- '\"ipv6_cidr\"' "${output}"; then
    echo "ERROR: ${name} rendered an IPv6 egress field" >&2
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

render_failure_with_message() {
  local name=$1
  local expected=$2
  shift 2

  local error="${TMP_DIR}/${name}.stderr"
  echo "checking ${name} (expected failure containing ${expected@Q})"
  if "${HELM_BIN}" "${COMMON_ARGS[@]}" "$@" >"/dev/null" 2>"${error}"; then
    echo "ERROR: ${name} unexpectedly rendered successfully" >&2
    exit 1
  fi
  if ! grep -Fq -- "${expected}" "${error}"; then
    echo "ERROR: ${name} failed without expected message ${expected@Q}" >&2
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
render_ipv4_only_manager_success \
  agentless-manager-capabilities \
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

# The networking facade forwards certificate validation to config-as-code and
# both enabled AAP instance groups, and keeps verification enabled by default.
render_success \
  netris-validate-certs-default \
  'NETRIS_VALIDATE_CERTS: "true"' \
  --set global.networking.fabricManager=netris \
  --set global.networking.k8sManager= \
  --set-string global.networking.netris.controllerUrl=https://netris.example.com \
  --set-string global.networking.netris.credentials.username=test-user \
  --set global.networking.netris.credentials.externalSecret=true \
  --set-string global.networking.netris.siteId=1 \
  --set-string global.networking.netris.tenantId=1 \
  --set-string global.networking.netris.tenantName=test \
  --set aap.instanceGroups.clusterFulfillment.enabled=true \
  --set aap.instanceGroups.networkFulfillment.enabled=true
if [[ $(grep -Fc 'NETRIS_VALIDATE_CERTS: "true"' "${TMP_DIR}/netris-validate-certs-default.yaml") -ne 3 ]]; then
  echo "ERROR: certificate validation default was not rendered to config-as-code and both AAP instance groups" >&2
  exit 1
fi

render_success \
  netris-validate-certs-disabled \
  'NETRIS_VALIDATE_CERTS: "false"' \
  --set global.networking.fabricManager=netris \
  --set global.networking.k8sManager= \
  --set global.networking.netris.validateCerts=false \
  --set-string global.networking.netris.controllerUrl=https://netris.example.com \
  --set-string global.networking.netris.credentials.username=test-user \
  --set global.networking.netris.credentials.externalSecret=true \
  --set-string global.networking.netris.siteId=1 \
  --set-string global.networking.netris.tenantId=1 \
  --set-string global.networking.netris.tenantName=test \
  --set aap.instanceGroups.clusterFulfillment.enabled=true \
  --set aap.instanceGroups.networkFulfillment.enabled=true
if [[ $(grep -Fc 'NETRIS_VALIDATE_CERTS: "false"' "${TMP_DIR}/netris-validate-certs-disabled.yaml") -ne 3 ]]; then
  echo "ERROR: certificate validation opt-out was not rendered to config-as-code and both AAP instance groups" >&2
  exit 1
fi

# Netris-backed CI profiles opt out explicitly because their lab controllers
# do not use a certificate trusted by the execution environment.
render_success \
  netris-bmaas-ci-validate-certs \
  'NETRIS_VALIDATE_CERTS: "false"' \
  --values "${SCRIPT_DIR}/../values/bmaas-ci/instance.yaml" \
  --set-string service.externalHostname=fulfillment-api.test.example.com \
  --set-string service.internalHostname=fulfillment-internal-api.test.example.com

render_success \
  netris-cudn-evpn-test-validate-certs \
  'NETRIS_VALIDATE_CERTS: "false"' \
  --values "${SCRIPT_DIR}/../values/cudn-evpn-netris-test/instance.yaml" \
  --set-string service.externalHostname=fulfillment-api.test.example.com \
  --set-string service.internalHostname=fulfillment-internal-api.test.example.com

render_success \
  netris-full-ci-validate-certs \
  'NETRIS_VALIDATE_CERTS: "false"' \
  --values "${CHART_DIR}/ci/full-values.yaml"
if [[ $(grep -Fc 'NETRIS_VALIDATE_CERTS: "false"' "${TMP_DIR}/netris-full-ci-validate-certs.yaml") -ne 3 ]]; then
  echo "ERROR: full CI certificate validation opt-out was not rendered to config-as-code and both AAP instance groups" >&2
  exit 1
fi

# Storage TLS policy is exposed in the AAP storage instance-group environment.
# Keep this contract alongside the existing AAP provider-certificate render checks.
assert_storage_validate_certs() {
  local name="$1" rendered="$2" expected="$3"
  echo "checking ${name} storage AAP TLS validation=${expected}"
  python3 - "${rendered}" "${expected}" <<'PY'
import sys

import yaml

with open(sys.argv[1], encoding="utf-8") as rendered_file:
    resources = [item for item in yaml.safe_load_all(rendered_file) if isinstance(item, dict)]
matches = [
    item for item in resources
    if item.get("kind") == "ConfigMap"
    and item.get("metadata", {}).get("name") == "storage-operations-ig"
]
assert len(matches) == 1, (
    f"expected one storage-operations-ig ConfigMap, found {len(matches)}"
)
data = matches[0].get("data", {})
assert data.get("VAST_VALIDATE_CERTS") == sys.argv[2], (
    f"expected VAST_VALIDATE_CERTS={sys.argv[2]!r}, got {data.get('VAST_VALIDATE_CERTS')!r}"
)
assert "ENABLE_CERTIFICATE_VALIDATION" not in data, "generic worker variable must not be rendered"
PY
  if [[ $(grep -Fc 'VAST_VALIDATE_CERTS:' "${rendered}") -ne 1 ]]; then
    echo "ERROR: expected exactly one VAST_VALIDATE_CERTS setting in the AAP render" >&2
    exit 1
  fi
}

render_storage_validate_certs() {
  local name="$1" expected="$2"
  shift 2
  local rendered="${TMP_DIR}/${name}.yaml"
  "${HELM_BIN}" "${COMMON_ARGS[@]}" "$@" >"${rendered}"
  assert_storage_validate_certs "${name}" "${rendered}" "${expected}"
}

render_storage_validate_certs \
  storage-validate-certs-default true \
  --set aap.instanceGroups.storageFulfillment.enabled=true

# The storage policy owns this worker variable; a low-level config value cannot
# override the explicit Helm setting.
storage_override_render="${TMP_DIR}/storage-validate-certs-override.yaml"
"${HELM_BIN}" template osac-aap "${AAP_CHART_DIR}" \
  --set instanceGroups.storageFulfillment.enabled=true \
  --set instanceGroups.storageFulfillment.enable_certificate_validation=false \
  --set instanceGroups.storageFulfillment.config.VAST_VALIDATE_CERTS=true \
  >"${storage_override_render}"
assert_storage_validate_certs override "${storage_override_render}" false

render_storage_validate_certs \
  storage-validate-certs-dev-profile false \
  --values "${SCRIPT_DIR}/../values/dev/instance.yaml" \
  --set-string service.externalHostname=fulfillment-api.test.example.com \
  --set-string service.internalHostname=fulfillment-internal-api.test.example.com \
  --set aap.instanceGroups.storageFulfillment.enabled=true

render_storage_validate_certs \
  storage-validate-certs-vmaas-ci-profile false \
  --values "${SCRIPT_DIR}/../values/vmaas-ci/instance.yaml" \
  --set-string service.externalHostname=fulfillment-api.test.example.com \
  --set-string service.internalHostname=fulfillment-internal-api.test.example.com \
  --set aap.instanceGroups.storageFulfillment.enabled=true

python3 - "${CHART_DIR}/values.yaml" "${CHART_DIR}/values.schema.json" <<'PY'
import json
import sys

import yaml

with open(sys.argv[1], encoding="utf-8") as values_file:
    values = yaml.safe_load(values_file)
storage_tls = values["aap"]["instanceGroups"]["storageFulfillment"]["enable_certificate_validation"]
assert storage_tls is True

with open(sys.argv[2], encoding="utf-8") as schema_file:
    schema = json.load(schema_file)
setting = (
    schema["properties"]["aap"]["properties"]["instanceGroups"]["properties"][
        "storageFulfillment"
    ]["properties"]["enable_certificate_validation"]
)
assert setting["type"] == "boolean" and setting["default"] is True
PY

# Protected connection settings cannot be supplied through low-level AAP maps,
# including when unrelated expert overrides are enabled.
render_failure_with_message \
  netris-protected-cluster-validate-certs-disabled \
  'managed by global.networking.netris' \
  --skip-schema-validation \
  --set global.expertOverrides.aap=true \
  --set aap.instanceGroups.clusterFulfillment.enabled=true \
  --set-string aap.instanceGroups.clusterFulfillment.config.NETRIS_VALIDATE_CERTS=false

render_failure_with_message \
  netris-protected-network-validate-certs-disabled \
  'managed by global.networking.netris' \
  --set global.expertOverrides.aap=true \
  --set aap.instanceGroups.networkFulfillment.enabled=true \
  --set-string aap.instanceGroups.networkFulfillment.config.NETRIS_VALIDATE_CERTS=false

# A customer-created CA ConfigMap must reach the AAP config-as-code Secret.
# The AAP pod-spec contract test verifies that the same value is consumed by
# both Netris container groups.
render_success \
  netris-ca-configmap-empty \
  'NETRIS_CA_CONFIGMAP: ""' \
  --set global.networking.fabricManager=netris \
  --set global.networking.k8sManager= \
  --set-string global.networking.netris.controllerUrl=https://netris.example.com \
  --set-string global.networking.netris.credentials.username=test-user \
  --set global.networking.netris.credentials.externalSecret=true \
  --set-string global.networking.netris.siteId=1 \
  --set-string global.networking.netris.tenantId=1 \
  --set-string global.networking.netris.tenantName=test \
  --set global.networking.netris.validateCerts=true

render_success \
  netris-ca-configmap \
  'NETRIS_CA_CONFIGMAP: "customer.netris-ca"' \
  --set global.networking.fabricManager=netris \
  --set global.networking.k8sManager= \
  --set-string global.networking.netris.controllerUrl=https://netris.example.com \
  --set-string global.networking.netris.credentials.username=test-user \
  --set global.networking.netris.credentials.externalSecret=true \
  --set-string global.networking.netris.siteId=1 \
  --set-string global.networking.netris.tenantId=1 \
  --set-string global.networking.netris.tenantName=test \
  --set global.networking.netris.validateCerts=true \
  --set-string global.networking.netris.caBundle.configMap=customer.netris-ca

# ConfigMap names use the Kubernetes DNS-subdomain form: dots may separate
# valid DNS labels, but empty labels and leading dots remain invalid.
render_failure \
  netris-ca-configmap-invalid-dot \
  --set global.networking.fabricManager=netris \
  --set global.networking.k8sManager= \
  --set-string global.networking.netris.controllerUrl=https://netris.example.com \
  --set-string global.networking.netris.credentials.username=test-user \
  --set global.networking.netris.credentials.externalSecret=true \
  --set-string global.networking.netris.siteId=1 \
  --set-string global.networking.netris.tenantId=1 \
  --set-string global.networking.netris.tenantName=test \
  --set global.networking.netris.validateCerts=true \
  --set-string global.networking.netris.caBundle.configMap=.customer-netris-ca

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

render_ipv4_only_network_class_success \
  network-class-ipv4-egress \
  --set global.networking.fabricManager= \
  --set global.networking.k8sManager=k8s_only

# IPv6 egress declarations are no longer part of either installer values path.
render_failure \
  ipv6-egress-global \
  --set global.networking.fabricManager= \
  --set global.networking.k8sManager=k8s_only \
  --set global.networking.networkClass.defaults.egressRules[0].protocol=PROTOCOL_ALL \
  --set-string global.networking.networkClass.defaults.egressRules[0].ipv6Cidr=2001:db8::/32

render_failure \
  ipv6-egress-expert \
  --set global.expertOverrides.networkClass=true \
  --set networkClass.enabled=true \
  --set-string networkClass.title=Custom\ network \
  --set networkClass.k8sManager=k8s_only \
  --set networkClass.defaults.egressRules[0].protocol=PROTOCOL_ALL \
  --set-string networkClass.defaults.egressRules[0].ipv6Cidr=2001:db8::/32

# Manager capability declarations are IPv4-only at the installer schema boundary.
render_failure \
  ipv6-manager-capability \
  --set networkManagers.enabled=true \
  --set networkManagers.k8sManagers.k8s_only.enabled=true \
  --set networkManagers.k8sManagers.k8s_only.capabilities[0]=ipv6

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

# CaaS on virtual BareMetalHosts uses the CUDN overlay manager with ci.steps
# (operator-bound Agent waits), not a physical Netris fabric.
render_success \
  caas-cudn-profile \
  'fabric_manager\":\"cudn_net' \
  --values "${SCRIPT_DIR}/../values/caas-ci/instance.yaml" \
  --set-string global.clusterDomain=apps.example.test \
  --set-string service.externalHostname=api.example.test \
  --set-string service.internalHostname=api-internal.example.test
# Parse only whitelisted, non-secret documents/fields. Never print the render:
# it may include credential-bearing Secrets from other chart components.
python3 - "${TMP_DIR}/caas-cudn-profile.yaml" <<'PY'
import json
import sys
import yaml

docs = [d for d in yaml.safe_load_all(open(sys.argv[1], encoding='utf-8')) if isinstance(d, dict)]
def resource(kind, name):
    matches = [d for d in docs if d.get('kind') == kind and d.get('metadata', {}).get('name') == name]
    assert len(matches) == 1, f'expected one {kind}/{name}, got {len(matches)}'
    return matches[0]

job = resource('Job', 'create-network-class')
containers = [c for c in job['spec']['template']['spec']['containers']
              if c['name'] == 'create-network-class']
assert len(containers) == 1, f"expected one create-network-class container, got {len(containers)}"
body_env = [e for e in containers[0]['env'] if e['name'] == 'NETWORK_CLASS_BODY']
assert len(body_env) == 1 and 'value' in body_env[0], 'expected one literal NETWORK_CLASS_BODY env var'
body = json.loads(body_env[0]['value'])
assert body.get('fabric_manager') == 'cudn_net', 'default class must select CUDN'
assert not body.get('k8s_manager'), 'default class must have no k8s manager'
manager = resource('ConfigMap', 'osac-network-fabric-manager-cudn-net')
assert manager['data']['name'] == 'cudn_net'
cluster = resource('ConfigMap', 'cluster-fulfillment-ig')['data']
assert cluster['NETWORK_CLASS'] == 'ci'
assert cluster['NETWORK_STEPS_COLLECTION'] == 'ci.steps'
resource('ConfigMap', 'network-fulfillment-ig')
PY

# A CUDN profile without CaaS, with wrong AAP pairing, a conflicting class,
# or without registered manager must fail before any deployable render.
for name in cudn-without-caas cudn-wrong-steps cudn-wrong-class cudn-disabled-manager cudn-k8s-conflict cudn-operatorless-openshift cudn-operatorless-kind; do
  case "${name}" in
    cudn-without-caas) overrides=(--set global.services.caas.enabled=false) ;;
    cudn-wrong-steps) overrides=(--set-string aap.instanceGroups.clusterFulfillment.config.NETWORK_STEPS_COLLECTION=agentless_net.steps) ;;
    cudn-wrong-class) overrides=(--set global.networking.networkClass.fabricManager=netris) ;;
    cudn-disabled-manager) overrides=(--set operator.networkManagers.fabricManagers.cudn_net.enabled=false) ;;
    cudn-k8s-conflict) overrides=(--set global.networking.k8sManager=k8s_only) ;;
    cudn-operatorless-openshift) overrides=(--set operator.enabled=false --set aap.aap.instance.enabled=false --set aap.bootstrap.enabled=false) ;;
    cudn-operatorless-kind) overrides=(--set service.variant=kind --set hubAccess.enabled=true --set operator.enabled=false --set aap.aap.instance.enabled=false --set aap.bootstrap.enabled=false) ;;
  esac
  render_failure "${name}" \
    --values "${SCRIPT_DIR}/../values/caas-ci/instance.yaml" \
    --set-string global.clusterDomain=apps.example.test \
    --set-string service.externalHostname=api.example.test \
    --set-string service.internalHostname=api-internal.example.test \
    "${overrides[@]}"
done
render_failure \
  cudn-unscoped \
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

HELM_BIN="${HELM_BIN}" python3 "${SCRIPT_DIR}/test-fabric-domain-values.py"

# The schema drives the Enclave Wizard. Protected connection settings must not
# be advertised as low-level AAP overrides, while supported expert identity and
# password fields remain available.
python3 - "${CHART_DIR}/values.schema.json" <<'PY'
import json
import sys
from pathlib import Path

schema = json.loads(Path(sys.argv[1]).read_text())
instance_groups = schema["properties"]["aap"]["properties"]["instanceGroups"]["properties"]
shared_config = schema["$defs"]["aapNetrisConfig"]["properties"]
cluster_config = instance_groups["clusterFulfillment"]["properties"]["config"]
network_config = instance_groups["networkFulfillment"]["properties"]["config"]

expected_ref = "#/$defs/aapNetrisConfig"
for name, config in (("clusterFulfillment", cluster_config), ("networkFulfillment", network_config)):
    refs = {entry.get("$ref") for entry in config.get("allOf", [])}
    if expected_ref not in refs:
        raise AssertionError(f"{name}.config does not reference {expected_ref}")

protected = {"NETRIS_CONTROLLER_URL", "NETRIS_VALIDATE_CERTS", "NETRIS_CA_PATH"}
advertised = set(shared_config)
advertised.update(cluster_config.get("properties", {}))
advertised.update(network_config.get("properties", {}))
unexpected = sorted(protected & advertised)
if unexpected:
    raise AssertionError(f"protected AAP settings are advertised by the schema: {unexpected}")

expert_identity = {"NETRIS_USERNAME", "NETRIS_SITE_ID", "NETRIS_TENANT_ID", "NETRIS_TENANT_NAME"}
missing_identity = sorted(expert_identity - set(shared_config))
if missing_identity:
    raise AssertionError(f"supported AAP expert settings are missing from the schema: {missing_identity}")

for name in ("clusterFulfillment", "networkFulfillment"):
    secret_properties = instance_groups[name]["properties"]["secret"]["properties"]
    if "NETRIS_PASSWORD" not in secret_properties:
        raise AssertionError(f"{name}.secret does not define NETRIS_PASSWORD")
PY

# AgentlessNet uses the ExternalIP v1 reservation and result contract.
render_operator_success \
  agentless-net-eip-contract \
  'externalIPContractVersion: "v1"' \
  --set networkManagers.enabled=true \
  --set global.networking.fabricManager=agentless_net \
  --set global.networking.k8sManager=

render_failure \
  agentless-net-unsupported-external-ip-contract \
  --set operator.networkManagers.fabricManagers.agentless_net.externalIPContractVersion=v2

echo "networking values validation passed"
