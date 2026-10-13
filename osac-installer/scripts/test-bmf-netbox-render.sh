#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "$0")" && pwd)
INSTALLER_DIR=$(cd -- "$SCRIPT_DIR/.." && pwd)
cd "$INSTALLER_DIR"

BMF_CHART=../bare-metal-fulfillment-operator/charts/operator
OSAC_CHART=charts/osac
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

cat > "$TMP_DIR/netbox-values.yaml" <<'YAML'
secrets:
  inventoryConfig: osac-inventory-config
  managementConfig: osac-management-config
  netboxToken: custom-netbox-token
  netboxCA: custom-netbox-ca
netbox:
  enabled: true
  bmhNamespace: host-inventory
  url: https://netbox.example.test
  tokenValue: render-test-netbox-token
  caCert: |
    TEST-CA-CERTIFICATE
  allowInsecureHTTP: false
metal3:
  enabled: false
secretAPI:
  url: https://fulfillment-internal-api:8001
  issuerUrl: https://keycloak.example.test/realms/osac
  caBundle:
    configMap: ca-bundle
    key: bundle.pem
YAML

NETBOX_VALUES="$TMP_DIR/netbox-values.yaml"
NETBOX_RENDER="$TMP_DIR/netbox-render.yaml"
NO_CA_RENDER="$TMP_DIR/netbox-no-ca-render.yaml"
METAL3_RENDER="$TMP_DIR/metal3-render.yaml"
BCM_RENDER="$TMP_DIR/bcm-render.yaml"
OSAC_RENDER="$TMP_DIR/osac-netbox-render.yaml"
HELM_ERROR_FILE="$TMP_DIR/helm-error.log"
source "$SCRIPT_DIR/helm-test-helpers.sh"

helm template bmf "$BMF_CHART" --namespace osac --values "$NETBOX_VALUES" > "$NETBOX_RENDER"
python3 - "$NETBOX_RENDER" <<'PY'
import sys

render = open(sys.argv[1], encoding="utf-8").read()
documents = render.split("\n---\n")
inventory = next(
    doc for doc in documents
    if "kind: Secret" in doc and "name: osac-inventory-config" in doc
    and "inventory.yaml: |" in doc
)
config = inventory.split("inventory.yaml: |", 1)[1]
assert "type: netbox" in config
assert "hostClass: metal3" in config
assert 'url: "https://netbox.example.test"' in config
assert 'tokenFile: "/etc/osac/secrets/netbox/token"' in config
assert 'caFile: "/etc/osac/secrets/netbox-ca/ca.crt"' in config
assert "allowInsecureHTTP: false" in config
assert "render-test-netbox-token" not in config
assert "TEST-CA-CERTIFICATE" not in config
management = next(
    doc for doc in documents
    if "kind: Secret" in doc and "name: osac-management-config" in doc
    and "management.yaml: |" in doc
)
assert 'namespace: "host-inventory"' in management
token_secret = next(doc for doc in documents if "name: custom-netbox-token" in doc)
assert 'token: "render-test-netbox-token"' in token_secret
ca_secret = next(doc for doc in documents if "name: custom-netbox-ca" in doc)
assert "TEST-CA-CERTIFICATE" in ca_secret
role = next(
    doc for doc in documents
    if "kind: Role" in doc and "name: bmf-operator-bmc-secrets" in doc
)
assert "namespace: host-inventory" in role
for verb in ("list", "watch"):
    assert f"  - {verb}" not in role
namespace_job = next(
    doc for doc in documents
    if "kind: Job" in doc and "name: bmf-operator-netbox-ns-validation" in doc
)
assert '"helm.sh/hook": pre-install,pre-upgrade' in namespace_job
assert 'name: BMH_NAMESPACE' in namespace_job
assert 'value: "host-inventory"' in namespace_job
assert "osac\\.openshift\\.io/bmh-secret-namespace" in namespace_job
namespace_role = next(
    doc for doc in documents
    if "kind: ClusterRole" in doc
    and "name: bmf-operator-netbox-ns-validation-osac" in doc
)
assert 'resourceNames: ["host-inventory"]' in namespace_role
assert 'verbs: ["get"]' in namespace_role
deployment = next(doc for doc in documents if "kind: Deployment" in doc)
assert "name: OSAC_SECRET_API_URL" in deployment
assert "name: OSAC_SECRET_API_ISSUER_URL" in deployment
assert 'value: "https://keycloak.example.test/realms/osac"' in deployment
assert "name: OSAC_SECRET_API_CLIENT_ID_FILE" in deployment
assert "name: OSAC_SECRET_API_CLIENT_SECRET_FILE" in deployment
assert "OSAC_SECRET_API_TOKEN_FILE" not in deployment
assert "secretName: osac-bmf-secret-api-credentials" in deployment
assert "mountPath: /etc/osac/secret-api/credentials/" in deployment
assert "name: OSAC_SECRET_API_CA_FILE" in deployment
assert "mountPath: /etc/osac/secrets/netbox/" in deployment
assert "mountPath: /etc/osac/secrets/netbox-ca/" in deployment
assert "mountPath: /etc/osac/secret-api/ca/" in deployment
assert deployment.count("readOnly: true") >= 3
PY

helm template bmf "$BMF_CHART" --namespace osac --values "$NETBOX_VALUES" \
  --set-string netbox.tokenSecretKey=true > "$TMP_DIR/netbox-string-token-key-render.yaml"
python3 - "$TMP_DIR/netbox-string-token-key-render.yaml" <<'PY'
import sys

render = open(sys.argv[1], encoding="utf-8").read()
documents = render.split("\n---\n")
token_secret = next(doc for doc in documents if "name: custom-netbox-token" in doc)
assert '"true": "render-test-netbox-token"' in token_secret
deployment = next(doc for doc in documents if "kind: Deployment" in doc)
assert '- key: "true"' in deployment
PY

python3 - "$NETBOX_RENDER" "$TMP_DIR/bmf-netbox-namespace-validation.sh" <<'PY'
import sys

render = open(sys.argv[1], encoding="utf-8").read()
job = next(
    doc for doc in render.split("\n---\n")
    if "kind: Job" in doc and "name: bmf-operator-netbox-ns-validation" in doc
)
lines = job.splitlines()
script_start = next(i for i, line in enumerate(lines) if line.strip() == "- |")
script_lines = []
for line in lines[script_start + 1:]:
    if line and not line.startswith("          "):
        break
    script_lines.append(line[10:] if line else "")

with open(sys.argv[2], "w", encoding="utf-8") as script:
    script.write("\n".join(script_lines) + "\n")
PY

helm template bmf "$BMF_CHART" --namespace osac --values "$NETBOX_VALUES" \
  --set-string netbox.caCert= > "$NO_CA_RENDER"
python3 - "$NO_CA_RENDER" <<'PY'
import sys

render = open(sys.argv[1], encoding="utf-8").read()
inventory = next(
    doc for doc in render.split("\n---\n")
    if "kind: Secret" in doc and "name: osac-inventory-config" in doc
    and "inventory.yaml: |" in doc
)
config = inventory.split("inventory.yaml: |", 1)[1]
assert "caFile:" not in config
assert "name: custom-netbox-ca" not in render
assert "name: netbox-ca" not in render
PY

helm template bmf "$BMF_CHART" --namespace osac \
  --set metal3.enabled=true --set-string metal3.namespace=host-inventory > "$METAL3_RENDER"
python3 - "$METAL3_RENDER" <<'PY'
import sys

render = open(sys.argv[1], encoding="utf-8").read()
inventory = next(
    doc for doc in render.split("\n---\n")
    if "kind: Secret" in doc and "name: osac-inventory-config" in doc
    and "inventory.yaml: |" in doc
)
config = inventory.split("inventory.yaml: |", 1)[1]
assert "type: metal3" in config
assert "type: netbox" not in config
PY

helm template bmf "$BMF_CHART" --namespace osac \
  --set bcm.enabled=true \
  --set-string bcm.url=https://bcm.example.test \
  --set-string bcm.bmhNamespace=host-inventory \
  --set-string bcm.cert=TEST-BCM-CERT \
  --set-string bcm.key=TEST-BCM-KEY > "$BCM_RENDER"
python3 - "$BCM_RENDER" <<'PY'
import sys

render = open(sys.argv[1], encoding="utf-8").read()
documents = render.split("\n---\n")
inventory = next(
    doc for doc in documents
    if "kind: Secret" in doc and "name: osac-inventory-config" in doc
    and "inventory.yaml: |" in doc
)
assert "type: bcm" in inventory
assert 'credentialsSecret: "osac-bcm-certs"' in inventory
management = next(
    doc for doc in documents
    if "kind: Secret" in doc and "name: osac-management-config" in doc
    and "management.yaml: |" in doc
)
assert "type: metal3" in management
assert 'namespace: "host-inventory"' in management
role = next(
    doc for doc in documents
    if "kind: Role" in doc and "name: bmf-operator-bmc-secrets" in doc
)
assert "namespace: host-inventory" in role
certs = next(doc for doc in documents if "name: osac-bcm-certs" in doc)
assert "TEST-BCM-CERT" in certs
assert "TEST-BCM-KEY" in certs
PY

assert_helm_failure "bcm.enabled and metal3.enabled are mutually exclusive" \
  helm template bmf "$BMF_CHART" --namespace osac \
  --set bcm.enabled=true --set-string bcm.url=https://bcm.example.test \
  --set-string bcm.bmhNamespace=host-inventory \
  --set metal3.enabled=true --set-string metal3.namespace=host-inventory

assert_helm_failure "netbox.url is required" \
  helm template bmf "$BMF_CHART" --namespace osac \
  --set netbox.enabled=true --set-string netbox.tokenValue=render-test \
  --set-string netbox.bmhNamespace=host-inventory
assert_helm_failure "netbox.tokenValue is required" \
  helm template bmf "$BMF_CHART" --namespace osac \
  --set netbox.enabled=true --set-string netbox.url=https://netbox.example.test \
  --set-string netbox.tokenValue= --set-string netbox.bmhNamespace=host-inventory
assert_helm_failure "netbox.bmhNamespace is required" \
  helm template bmf "$BMF_CHART" --namespace osac \
  --set netbox.enabled=true --set-string netbox.url=https://netbox.example.test \
  --set-string netbox.tokenValue=render-test
assert_helm_failure "netbox.enabled and metal3.enabled are mutually exclusive" \
  helm template bmf "$BMF_CHART" --namespace osac --values "$NETBOX_VALUES" \
  --set metal3.enabled=true
assert_helm_failure "netbox.enabled and bcm.enabled are mutually exclusive" \
  helm template bmf "$BMF_CHART" --namespace osac --values "$NETBOX_VALUES" \
  --set bcm.enabled=true
assert_helm_failure "netbox.url must start with http:// or https://" \
  helm template bmf "$BMF_CHART" --namespace osac --values "$NETBOX_VALUES" \
  --set-string netbox.url=netbox.example.test
assert_helm_failure "netbox.url must start with http:// or https://" \
  helm template bmf "$BMF_CHART" --namespace osac --values "$NETBOX_VALUES" \
  --set-string netbox.url=ftp://netbox.example.test
assert_helm_failure "netbox.url uses HTTP" \
  helm template bmf "$BMF_CHART" --namespace osac --values "$NETBOX_VALUES" \
  --set-string netbox.url=http://netbox.example.test
assert_helm_failure "secretAPI.url must use HTTPS when NetBox is enabled" \
  helm template bmf "$BMF_CHART" --namespace osac --values "$NETBOX_VALUES" \
  --set-string secretAPI.url=http://fulfillment-internal-api:8001
assert_helm_failure "secretAPI.issuerUrl is required when netbox.enabled is true" \
  helm template bmf "$BMF_CHART" --namespace osac --values "$NETBOX_VALUES" \
  --set-string secretAPI.issuerUrl=
helm template bmf "$BMF_CHART" --namespace osac --values "$NETBOX_VALUES" \
  --set-string netbox.url=http://netbox.example.test \
  --set netbox.allowInsecureHTTP=true > "$TMP_DIR/netbox-http-render.yaml"
rg -Fq "allowInsecureHTTP: true" "$TMP_DIR/netbox-http-render.yaml"

cat > "$TMP_DIR/osac-netbox-values.yaml" <<'YAML'
global:
  services:
    bmaas:
      enabled: true
service:
  auth:
    issuerUrl: https://keycloak.example.test/realms/osac
bmf:
  secrets:
    netboxToken: custom-netbox-token
  secretAPI:
    issuerUrl: https://keycloak.example.test/realms/osac
  netbox:
    enabled: true
    bmhNamespace: host-inventory
    url: https://netbox.example.test
    tokenValue: render-test-netbox-token
  metal3:
    enabled: false
YAML

helm template osac "$OSAC_CHART" --values "$OSAC_CHART/ci/default-values.yaml" \
  --values "$TMP_DIR/osac-netbox-values.yaml" > "$OSAC_RENDER"
python3 - "$OSAC_RENDER" <<'PY'
import sys

render = open(sys.argv[1], encoding="utf-8").read()
documents = render.split("\n---\n")
inventories = [
    doc for doc in documents
    if "kind: Secret" in doc and "name: osac-inventory-config" in doc
    and "inventory.yaml: |" in doc
]
assert len(inventories) == 1
assert "type: netbox" in inventories[0]
deployment = next(
    doc for doc in documents
    if "kind: Deployment" in doc and "name: bmf-operator-controller-manager" in doc
)
assert "OSAC_SECRET_API_ISSUER_URL" in deployment
assert 'value: "https://keycloak.example.test/realms/osac"' in deployment
assert "osac-bmf-secret-api-credentials" in deployment
assert "OSAC_SECRET_API_TOKEN_FILE" not in deployment
management = next(
    doc for doc in documents
    if "kind: Secret" in doc and "name: osac-management-config" in doc
    and "management.yaml: |" in doc
)
assert "type: metal3" in management
assert 'namespace: "host-inventory"' in management
assert "Checking for Metal3 BareMetalHost CRD..." in render
assert "kubectl get provisioning provisioning-configuration" in render
assert "BMH_NAMESPACE" in render
assert "osac\\.openshift\\.io/bmh-secret-namespace" in render
assert "Label namespace $BMH_NAMESPACE with osac.openshift.io/bmh-secret-namespace=true." in render

provisioning_role = next(
    doc for doc in documents
    if "kind: ClusterRole" in doc and 'resources: ["provisionings"]' in doc
)
assert 'resourceNames: ["provisioning-configuration"]' in provisioning_role
assert 'verbs: ["get"]' in provisioning_role

namespace_role = next(
    doc for doc in documents
    if "kind: ClusterRole" in doc
    and "name: bmf-operator-netbox-ns-validation-default" in doc
)
assert 'resourceNames: ["host-inventory"]' in namespace_role
assert 'verbs: ["get"]' in namespace_role
PY

python3 - "$OSAC_RENDER" "$TMP_DIR/pre-install-validate.sh" <<'PY'
import sys

render = open(sys.argv[1], encoding="utf-8").read()
job = next(
    doc for doc in render.split("\n---\n")
    if "kind: Job" in doc and "name: osac-pre-install-validate" in doc
)
lines = job.splitlines()
script_start = next(i for i, line in enumerate(lines) if line.strip() == "- |")
script_lines = []
for line in lines[script_start + 1:]:
    if line and not line.startswith("          "):
        break
    script_lines.append(line[10:] if line else "")

with open(sys.argv[2], "w", encoding="utf-8") as script:
    script.write("\n".join(script_lines) + "\n")
PY

mkdir "$TMP_DIR/mock-bin"
cat > "$TMP_DIR/mock-bin/kubectl" <<'SH'
#!/usr/bin/env bash
set -euo pipefail

if [[ "$1" == get && "$2" == crd ]]; then
  exit 0
fi
if [[ "$1" == get && "$2" == sc ]]; then
  echo default
  exit 0
fi
if [[ "$1" == get && "$2" == provisioning && "$3" == provisioning-configuration ]]; then
  printf '%s' "$MOCK_WATCH_ALL_NAMESPACES"
  exit 0
fi
if [[ "$1" == get && "$2" == namespace && "$3" == "${BMH_NAMESPACE:-}" ]]; then
  if [[ "$MOCK_NAMESPACE_LABEL" == true ]]; then
    echo true
  fi
  exit 0
fi

echo "Unexpected kubectl call: $*" >&2
exit 1
SH
chmod +x "$TMP_DIR/mock-bin/kubectl"

if MOCK_NAMESPACE_LABEL=true BMH_NAMESPACE=host-inventory \
  PATH="$TMP_DIR/mock-bin:$PATH" /bin/sh "$TMP_DIR/bmf-netbox-namespace-validation.sh" \
  > "$TMP_DIR/bmf-namespace-success.log" 2>&1; then
  :
else
  cat "$TMP_DIR/bmf-namespace-success.log" >&2
  echo "BMF validation rejected a labeled NetBox BMH namespace." >&2
  exit 1
fi

if MOCK_NAMESPACE_LABEL=missing BMH_NAMESPACE=host-inventory \
  PATH="$TMP_DIR/mock-bin:$PATH" /bin/sh "$TMP_DIR/bmf-netbox-namespace-validation.sh" \
  > "$TMP_DIR/bmf-namespace-missing-label.log" 2>&1; then
  echo "BMF validation accepted an unlabeled NetBox BMH namespace." >&2
  exit 1
fi
rg -Fq "Label namespace host-inventory with osac.openshift.io/bmh-secret-namespace=true." \
  "$TMP_DIR/bmf-namespace-missing-label.log"

if MOCK_WATCH_ALL_NAMESPACES=true \
  PATH="$TMP_DIR/mock-bin:$PATH" /bin/sh "$TMP_DIR/pre-install-validate.sh" \
  > "$TMP_DIR/hook-success.log" 2>&1; then
  :
else
  cat "$TMP_DIR/hook-success.log" >&2
  echo "OSAC pre-install validation rejected valid prerequisites." >&2
  exit 1
fi

assert_helm_failure "bmf.netbox.enabled and bmf.testBackend.metal3.enabled cannot both be true" \
  helm template osac "$OSAC_CHART" --values "$OSAC_CHART/ci/default-values.yaml" \
  --values "$TMP_DIR/osac-netbox-values.yaml" \
  --set bmf.testBackend.metal3.enabled=true
helm template osac "$OSAC_CHART" --values "$OSAC_CHART/ci/default-values.yaml" \
  --values "$TMP_DIR/osac-netbox-values.yaml" \
  --set validation.enabled=false > "$TMP_DIR/osac-validation-disabled-render.yaml"
rg -Fq "name: bmf-operator-netbox-ns-validation" "$TMP_DIR/osac-validation-disabled-render.yaml"
assert_helm_failure "bmf.secretAPI.issuerUrl must match service.auth.issuerUrl when bmf.netbox.enabled is true" \
  helm template osac "$OSAC_CHART" --values "$OSAC_CHART/ci/default-values.yaml" \
  --values "$TMP_DIR/osac-netbox-values.yaml" \
  --set-string bmf.secretAPI.issuerUrl=https://wrong-issuer.example.test/realms/osac

echo "BMF NetBox Helm render checks passed."
