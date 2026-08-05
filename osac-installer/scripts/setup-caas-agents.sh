#!/usr/bin/env bash
# Sets up CaaS agent infrastructure: InfraEnv + agent VM + label + approve.
# Runs after OSAC install (MCE + AgentServiceConfig must be ready).
#
# When SSH_CONFIG is set, privileged operations (DNS, VM creation) run on
# a remote host via SSH. Otherwise they run locally (requires libvirt group
# membership and either osac-hosts-entry or direct root access for DNS).

set -o nounset
set -o errexit
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

INSTALLER_NAMESPACE=${INSTALLER_NAMESPACE:-"osac-e2e-ci"}
AGENT_NAMESPACE=${AGENT_NAMESPACE:-"hardware-inventory"}
AGENT_RESOURCE_CLASS=${AGENT_RESOURCE_CLASS:-"ci-worker"}
AGENT_VM_NAME=${AGENT_VM_NAME:?"AGENT_VM_NAME must be set"}
AGENT_VM_MEMORY=${AGENT_VM_MEMORY:-"16384"}
AGENT_VM_VCPUS=${AGENT_VM_VCPUS:-"4"}

[[ "${AGENT_VM_MEMORY}" =~ ^[1-9][0-9]*$ ]] || { echo "ERROR: AGENT_VM_MEMORY must be a positive integer: ${AGENT_VM_MEMORY}" >&2; exit 1; }
[[ "${AGENT_VM_VCPUS}" =~ ^[1-9][0-9]*$ ]] || { echo "ERROR: AGENT_VM_VCPUS must be a positive integer: ${AGENT_VM_VCPUS}" >&2; exit 1; }
AGENT_VM_DISK_SIZE=${AGENT_VM_DISK_SIZE:-"120G"}
AGENT_VM_STORAGE_DIR=${AGENT_VM_STORAGE_DIR:-"/data/osac-storage"}
LIBVIRT_NETWORK=${LIBVIRT_NETWORK:?"LIBVIRT_NETWORK must be set"}
SSH_CONFIG=${SSH_CONFIG:-""}

# Validate inputs that are interpolated into the generated hypervisor script
validate_safe() {
    local name="$1" value="$2"
    if [[ ! "$value" =~ ^[a-zA-Z0-9._/-]+$ ]]; then
        echo "ERROR: ${name} contains unsafe characters: ${value}" >&2
        exit 1
    fi
}
[[ "${AGENT_VM_NAME}" =~ ^[a-zA-Z0-9._-]+$ ]] || { echo "ERROR: AGENT_VM_NAME contains unsafe characters (path separators not allowed): ${AGENT_VM_NAME}" >&2; exit 1; }
validate_safe "AGENT_VM_STORAGE_DIR" "${AGENT_VM_STORAGE_DIR}"
validate_safe "LIBVIRT_NETWORK" "${LIBVIRT_NETWORK}"
validate_safe "AGENT_VM_DISK_SIZE" "${AGENT_VM_DISK_SIZE}"

echo "=== Setting up CaaS agent infrastructure ==="
echo "Agent namespace: ${AGENT_NAMESPACE}"
echo "Agent VM name: ${AGENT_VM_NAME}"
echo "Resource class: ${AGENT_RESOURCE_CLASS}"
echo ""

NODE_IP=$(oc get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')
CLUSTER_DOMAIN=$(oc get ingresses.config/cluster -o jsonpath='{.spec.domain}')
echo "Node IP: ${NODE_IP}"
echo "Cluster domain: ${CLUSTER_DOMAIN}"

SUBNET_PREFIX=$(echo "${NODE_IP}" | cut -d. -f1-3)
cat <<METALLBEOF | oc apply -f -
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: caas-address-pool
  namespace: metallb-system
spec:
  addresses:
    - ${SUBNET_PREFIX}.240-${SUBNET_PREFIX}.250
  autoAssign: true
METALLBEOF
echo "MetalLB IPAddressPool configured for ${SUBNET_PREFIX}.240-${SUBNET_PREFIX}.250"

echo "[1/6] Registering '${AGENT_RESOURCE_CLASS}' host type in fulfillment service..."
INTERNAL_API="https://$(oc get route fulfillment-internal-api -n "${INSTALLER_NAMESPACE}" -o jsonpath='{.status.ingress[0].host}')"
TOKEN=$(oc create token -n "${INSTALLER_NAMESPACE}" admin)
RESPONSE_BODY=$(mktemp)
HTTP_CODE=$(curl -sk -w "%{http_code}" -X POST "${INTERNAL_API}/api/private/v1/host_types" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "Content-Type: application/json" \
    -d "{\"id\": \"${AGENT_RESOURCE_CLASS}\", \"title\": \"CI Worker\", \"description\": \"Worker nodes for CI testing\"}" \
    -o "${RESPONSE_BODY}") || true
if [[ "${HTTP_CODE}" == "200" || "${HTTP_CODE}" == "201" ]]; then
    echo "  Host type '${AGENT_RESOURCE_CLASS}' created"
elif [[ "${HTTP_CODE}" == "409" ]]; then
    echo "  Host type '${AGENT_RESOURCE_CLASS}' already exists"
else
    echo "  ERROR: Failed to create host type (HTTP ${HTTP_CODE})"
    cat "${RESPONSE_BODY}"
    rm -f "${RESPONSE_BODY}"
    exit 1
fi
rm -f "${RESPONSE_BODY}"

echo "[2/6] Creating agent namespace and CAPI provider role..."
oc create namespace "${AGENT_NAMESPACE}" --dry-run=client -o yaml | oc apply -f -

cat <<EOF | oc apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: capi-provider-role
  namespace: ${AGENT_NAMESPACE}
rules:
- apiGroups: ["agent-install.openshift.io"]
  resources: ["agents"]
  verbs: ["*"]
EOF

echo "[3/6] Creating InfraEnv..."

oc get secret pull-secret -n openshift-config -o json \
  | python3 -c "import json,sys; s=json.load(sys.stdin); s['metadata']={'name':'pull-secret','namespace':'${AGENT_NAMESPACE}'}; json.dump(s,sys.stdout)" \
  | oc apply -f -

cat <<EOF | oc apply -f -
apiVersion: agent-install.openshift.io/v1beta1
kind: InfraEnv
metadata:
  name: ${AGENT_NAMESPACE}
  namespace: ${AGENT_NAMESPACE}
spec:
  pullSecretRef:
    name: pull-secret
EOF

echo "Waiting for discovery ISO URL..."
retry_until 300 5 '[[ -n "$(oc get infraenv ${AGENT_NAMESPACE} -n ${AGENT_NAMESPACE} -o jsonpath="{.status.isoDownloadURL}" 2>/dev/null)" ]]' || {
    echo "Timed out waiting for ISO URL. Current InfraEnv state:"
    oc get infraenv "${AGENT_NAMESPACE}" -n "${AGENT_NAMESPACE}" -o yaml 2>&1 || true
    exit 1
}
ISO_URL=$(oc get infraenv "${AGENT_NAMESPACE}" -n "${AGENT_NAMESPACE}" -o jsonpath='{.status.isoDownloadURL}')
echo "ISO URL: ${ISO_URL}"

# --- Steps 4-5 run on the hypervisor (locally or via SSH) ---

ISO_FILE="${AGENT_VM_STORAGE_DIR}/${AGENT_VM_NAME}-discovery.iso"

# Shell function containing all hypervisor-level commands.
# Runs either directly (local) or piped through SSH (remote).
generate_hypervisor_script() {
    cat <<HVEOF
set -euo pipefail

command -v virt-install >/dev/null || { echo "ERROR: virt-install not found" >&2; exit 1; }
command -v virsh >/dev/null || { echo "ERROR: virsh not found" >&2; exit 1; }
command -v qemu-img >/dev/null || { echo "ERROR: qemu-img not found" >&2; exit 1; }
[[ -d "${AGENT_VM_STORAGE_DIR}" ]] || { echo "ERROR: ${AGENT_VM_STORAGE_DIR} does not exist" >&2; exit 1; }

# DNS: resolve the assisted-image-service hostname to the cluster node IP
ISO_HOST=\$(python3 -c "from urllib.parse import urlparse; import sys; print(urlparse(sys.argv[1]).hostname)" '${ISO_URL}')
if command -v osac-hosts-entry >/dev/null 2>&1; then
    sudo /usr/local/sbin/osac-hosts-entry add "\${ISO_HOST}" "${NODE_IP}"
    trap 'sudo /usr/local/sbin/osac-hosts-entry remove "\${ISO_HOST}" 2>/dev/null || true' EXIT
    echo "  \${ISO_HOST} -> ${NODE_IP} (via osac-hosts-entry)"
else
    SLUG=\$(echo "${CLUSTER_DOMAIN}" | sed 's/[^a-zA-Z0-9]/-/g')
    CONF_ENTRY="address=/.${CLUSTER_DOMAIN}/${NODE_IP}"
    NM_DNSMASQ_PID="/run/NetworkManager/dnsmasq.pid"
    if [ -f "\${NM_DNSMASQ_PID}" ] && kill -0 "\$(cat "\${NM_DNSMASQ_PID}")" 2>/dev/null; then
        CONF_FILE="/etc/NetworkManager/dnsmasq.d/\${SLUG}.conf"
        echo "\${CONF_ENTRY}" > "\${CONF_FILE}"
        systemctl reload NetworkManager
        trap 'rm -f "\${CONF_FILE}"; systemctl reload NetworkManager 2>/dev/null || true' EXIT
    else
        CONF_FILE="/etc/dnsmasq.d/\${SLUG}.conf"
        echo "\${CONF_ENTRY}" > "\${CONF_FILE}"
        systemctl restart dnsmasq
        trap 'rm -f "\${CONF_FILE}"; systemctl restart dnsmasq 2>/dev/null || true' EXIT
    fi
    echo "  *.${CLUSTER_DOMAIN} -> ${NODE_IP} (via dnsmasq)"
fi

# Wait for assisted-image-service
echo "Waiting for assisted-image-service to be ready..."
for attempt in \$(seq 1 30); do
    HTTP_CODE=\$(curl -k -s -o /dev/null -w "%{http_code}" -L '${ISO_URL}') || true
    [[ "\${HTTP_CODE}" == "200" ]] && break
    echo "  attempt \${attempt}/30 - HTTP \${HTTP_CODE}, retrying in 10s..."
    sleep 10
done
[[ "\${HTTP_CODE}" != "200" ]] && { echo "ERROR: assisted-image-service not ready after 30 attempts (last HTTP \${HTTP_CODE})"; exit 1; }

# Download discovery ISO
echo "Downloading discovery ISO..."
curl -k -L --fail-with-body -o '${ISO_FILE}' '${ISO_URL}'

# Create agent VM
virsh --connect qemu:///system destroy '${AGENT_VM_NAME}' 2>/dev/null || true
virsh --connect qemu:///system undefine '${AGENT_VM_NAME}' 2>/dev/null || true
rm -f '${AGENT_VM_STORAGE_DIR}/${AGENT_VM_NAME}.qcow2'

qemu-img create -f qcow2 '${AGENT_VM_STORAGE_DIR}/${AGENT_VM_NAME}.qcow2' '${AGENT_VM_DISK_SIZE}'

virt-install --connect qemu:///system \
  --name '${AGENT_VM_NAME}' \
  --memory '${AGENT_VM_MEMORY}' \
  --vcpus '${AGENT_VM_VCPUS}' \
  --disk '${AGENT_VM_STORAGE_DIR}/${AGENT_VM_NAME}.qcow2' \
  --disk '${ISO_FILE},device=cdrom,readonly=on' \
  --network network='${LIBVIRT_NETWORK}' \
  --os-variant rhel9.0 \
  --boot hd,cdrom \
  --events on_poweroff=restart \
  --noautoconsole

echo "Agent VM created and booting"
HVEOF
}

echo "[4/6] Configuring host DNS for ISO download..."
echo "[5/6] Creating agent VM..."

if [[ -n "${SSH_CONFIG}" ]]; then
    generate_hypervisor_script | timeout -s 9 10m ssh -F "${SSH_CONFIG}" ci_machine bash -s
else
    generate_hypervisor_script | bash -s
fi

echo "[6/6] Waiting for agent to register..."
retry_until 600 10 '[[ $(oc get agent -n ${AGENT_NAMESPACE} --no-headers 2>/dev/null | wc -l) -gt 0 ]]' || {
    echo "Timed out waiting for agent to register. Current state:"
    oc get infraenv -n "${AGENT_NAMESPACE}" -o yaml 2>&1 || true
    oc get agent -n "${AGENT_NAMESPACE}" -o yaml 2>&1 || true
    exit 1
}

AGENT_NAME=$(oc get agent -n "${AGENT_NAMESPACE}" -o jsonpath='{.items[0].metadata.name}')
echo "Agent registered: ${AGENT_NAME}"

oc label agent/"${AGENT_NAME}" -n "${AGENT_NAMESPACE}" "osac.openshift.io/resource_class=${AGENT_RESOURCE_CLASS}" --overwrite
oc patch agent/"${AGENT_NAME}" -n "${AGENT_NAMESPACE}" --type=merge -p '{"spec":{"approved":true}}'

echo ""
echo "=== CaaS agent setup complete ==="
echo "Agent: ${AGENT_NAME}"
echo "Resource class: ${AGENT_RESOURCE_CLASS}"
echo "Namespace: ${AGENT_NAMESPACE}"
