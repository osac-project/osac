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
  url: https://netbox.example.test/api/
  token: render-test-netbox-token
  caCert: |
    TEST-CA-CERTIFICATE
  allowInsecureHTTP: false
metal3:
  enabled: false
  namespace: host-inventory
secretAPI:
  url: https://fulfillment-internal-api:8001
  tokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
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
assert 'url: "https://netbox.example.test/api/"' in config
assert 'tokenFile: "/etc/osac/secrets/netbox/token"' in config
assert 'caFile: "/etc/osac/secrets/netbox-ca/ca.crt"' in config
assert "allowInsecureHTTP: false" in config
assert "render-test-netbox-token" not in config
assert "TEST-CA-CERTIFICATE" not in config
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
deployment = next(doc for doc in documents if "kind: Deployment" in doc)
assert "name: OSAC_SECRET_API_URL" in deployment
assert "name: OSAC_SECRET_API_TOKEN_FILE" in deployment
assert "name: OSAC_SECRET_API_CA_FILE" in deployment
assert "mountPath: /etc/osac/secrets/netbox/" in deployment
assert "mountPath: /etc/osac/secrets/netbox-ca/" in deployment
assert "mountPath: /etc/osac/secret-api/ca/" in deployment
assert deployment.count("readOnly: true") >= 3
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
  --set netbox.enabled=true --set-string netbox.token=render-test \
  --set-string metal3.namespace=host-inventory
assert_helm_failure "netbox.token is required" \
  helm template bmf "$BMF_CHART" --namespace osac \
  --set netbox.enabled=true --set-string netbox.url=https://netbox.example.test \
  --set-string netbox.token= --set-string metal3.namespace=host-inventory
assert_helm_failure "metal3.namespace is required" \
  helm template bmf "$BMF_CHART" --namespace osac \
  --set netbox.enabled=true --set-string netbox.url=https://netbox.example.test \
  --set-string netbox.token=render-test
assert_helm_failure "netbox.enabled and metal3.enabled are mutually exclusive" \
  helm template bmf "$BMF_CHART" --namespace osac --values "$NETBOX_VALUES" \
  --set metal3.enabled=true
assert_helm_failure "netbox.enabled and bcm.enabled are mutually exclusive" \
  helm template bmf "$BMF_CHART" --namespace osac --values "$NETBOX_VALUES" \
  --set bcm.enabled=true
assert_helm_failure "netbox.url uses HTTP" \
  helm template bmf "$BMF_CHART" --namespace osac --values "$NETBOX_VALUES" \
  --set-string netbox.url=http://netbox.example.test
assert_helm_failure "secretAPI.url must use HTTPS when NetBox is enabled" \
  helm template bmf "$BMF_CHART" --namespace osac --values "$NETBOX_VALUES" \
  --set-string secretAPI.url=http://fulfillment-internal-api:8001
helm template bmf "$BMF_CHART" --namespace osac --values "$NETBOX_VALUES" \
  --set-string netbox.url=http://netbox.example.test \
  --set netbox.allowInsecureHTTP=true > "$TMP_DIR/netbox-http-render.yaml"
rg -Fq "allowInsecureHTTP: true" "$TMP_DIR/netbox-http-render.yaml"

cat > "$TMP_DIR/osac-netbox-values.yaml" <<'YAML'
global:
  services:
    bmaas:
      enabled: true
bmf:
  secrets:
    netboxToken: custom-netbox-token
  netbox:
    enabled: true
    url: https://netbox.example.test/api/
    token: render-test-netbox-token
  metal3:
    enabled: false
    namespace: host-inventory
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
management = next(
    doc for doc in documents
    if "kind: Secret" in doc and "name: osac-management-config" in doc
    and "management.yaml: |" in doc
)
assert "type: metal3" in management
assert 'namespace: "host-inventory"' in management
assert "Checking for Metal3 BareMetalHost CRD..." in render
assert "kubectl get provisioning provisioning-configuration" in render
assert "BMF_BMH_NAMESPACE" in render
assert "osac\\.openshift\\.io/bmh-secret-namespace" in render
assert "Label namespace $BMF_BMH_NAMESPACE with osac.openshift.io/bmh-secret-namespace=true." in render

provisioning_role = next(
    doc for doc in documents
    if "kind: ClusterRole" in doc and 'resources: ["provisionings"]' in doc
)
assert 'resourceNames: ["provisioning-configuration"]' in provisioning_role
assert 'verbs: ["get"]' in provisioning_role

namespace_role = next(
    doc for doc in documents
    if "kind: ClusterRole" in doc and 'resources: ["namespaces"]' in doc
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
if [[ "$1" == get && "$2" == namespace && "$3" == host-inventory ]]; then
  if [[ "$MOCK_NAMESPACE_LABEL" == true ]]; then
    echo true
  fi
  exit 0
fi

echo "Unexpected kubectl call: $*" >&2
exit 1
SH
chmod +x "$TMP_DIR/mock-bin/kubectl"

if MOCK_NAMESPACE_LABEL=true MOCK_WATCH_ALL_NAMESPACES=true BMF_BMH_NAMESPACE=host-inventory \
  PATH="$TMP_DIR/mock-bin:$PATH" /bin/sh "$TMP_DIR/pre-install-validate.sh" \
  > "$TMP_DIR/hook-success.log" 2>&1; then
  :
else
  cat "$TMP_DIR/hook-success.log" >&2
  echo "Pre-install validation rejected a labeled NetBox BMH namespace." >&2
  exit 1
fi

if MOCK_NAMESPACE_LABEL=missing MOCK_WATCH_ALL_NAMESPACES=true BMF_BMH_NAMESPACE=host-inventory \
  PATH="$TMP_DIR/mock-bin:$PATH" /bin/sh "$TMP_DIR/pre-install-validate.sh" \
  > "$TMP_DIR/hook-missing-label.log" 2>&1; then
  echo "Pre-install validation accepted an unlabeled NetBox BMH namespace." >&2
  exit 1
fi
rg -Fq "Label namespace host-inventory with osac.openshift.io/bmh-secret-namespace=true." \
  "$TMP_DIR/hook-missing-label.log"

assert_helm_failure "bmf.netbox.enabled and bmf.testBackend.metal3.enabled cannot both be true" \
  helm template osac "$OSAC_CHART" --values "$OSAC_CHART/ci/default-values.yaml" \
  --values "$TMP_DIR/osac-netbox-values.yaml" \
  --set bmf.testBackend.metal3.enabled=true
assert_helm_failure "validation.enabled must be true when bmf.netbox.enabled is true" \
  helm template osac "$OSAC_CHART" --values "$OSAC_CHART/ci/default-values.yaml" \
  --values "$TMP_DIR/osac-netbox-values.yaml" \
  --set validation.enabled=false

echo "BMF NetBox Helm render checks passed."
