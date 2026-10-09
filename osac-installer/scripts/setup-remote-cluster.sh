#!/usr/bin/env bash
# Prepare a VMaaS workload cluster or connect it to an installed management cluster.

set -o nounset
set -o errexit
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/lib.sh"

REMOTE_KUBECONFIG=${REMOTE_KUBECONFIG:?"REMOTE_KUBECONFIG must be set"}
REMOTE_API_ADDRESS=${REMOTE_API_ADDRESS:-}
INSTALLER_NAMESPACE=${INSTALLER_NAMESPACE:-"osac"}
OPERATOR_DEPLOYMENT_NAME=${OPERATOR_DEPLOYMENT_NAME:-"osac-operator"}
REMOTE_STORAGE_CLASS=${REMOTE_STORAGE_CLASS:-"lvms-vg1"}
REMOTE_KUBECONFIG_SECRET_NAME=${REMOTE_KUBECONFIG_SECRET_NAME:-"osac-remote-kubeconfig"}
REMOTE_KUBECONFIG_SECRET_KEY=${REMOTE_KUBECONFIG_SECRET_KEY:-"kubeconfig"}
MODE=${1:-"configure"}

if [[ "${MODE}" != "prepare" && "${MODE}" != "configure" ]]; then
    echo "Usage: $0 [prepare|configure]" >&2
    exit 2
fi
if [[ "${MODE}" == "configure" ]]; then
    HUB_KUBECONFIG=${HUB_KUBECONFIG:?"HUB_KUBECONFIG must be set"}
    REMOTE_API_ADDRESS=${REMOTE_API_ADDRESS:?"REMOTE_API_ADDRESS must be set (e.g. https://192.168.128.10:6443)"}
fi
if [[ -n "${REMOTE_API_ADDRESS}" && "${REMOTE_API_ADDRESS}" != https://* ]]; then
    echo "ERROR: REMOTE_API_ADDRESS must use https://" >&2
    exit 2
fi

remote_args=(--kubeconfig "${REMOTE_KUBECONFIG}")
remote_oc="oc --kubeconfig $(printf '%q' "${REMOTE_KUBECONFIG}")"
if [[ "${MODE}" == "configure" ]]; then
    hub_args=(--kubeconfig "${HUB_KUBECONFIG}")
    hub_oc="oc --kubeconfig $(printf '%q' "${HUB_KUBECONFIG}")"
fi

SKIP_PREREQUISITES=${SKIP_PREREQUISITES:-"false"}

if [[ "${SKIP_PREREQUISITES}" != "true" ]]; then

OCP_VERSION=$(oc "${remote_args[@]}" version -o json | jq -r '.openshiftVersion' | cut -d. -f1-2)

# Remote cluster: install prerequisites

# LVMS
cat <<EOF | oc "${remote_args[@]}" apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: openshift-storage
---
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: openshift-storage
  namespace: openshift-storage
spec:
  targetNamespaces:
    - openshift-storage
---
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: lvms-operator
  namespace: openshift-storage
spec:
  source: redhat-operators
  sourceNamespace: openshift-marketplace
  name: lvms-operator
  channel: stable-${OCP_VERSION}
  installPlanApproval: Automatic
EOF
echo "Waiting for LVMS CRD..."
retry_until 180 5 "${remote_oc} get crd lvmclusters.lvm.topolvm.io 2>/dev/null" || { echo "Timed out waiting for LVMS CRD"; exit 1; }
echo "Waiting for LVMS operator webhook..."
retry_until 180 5 "${remote_oc} rollout status deployment/lvms-operator -n openshift-storage --timeout=5s 2>/dev/null" || { echo "Timed out waiting for LVMS operator"; exit 1; }

cat <<EOF | oc "${remote_args[@]}" apply -f -
apiVersion: lvm.topolvm.io/v1alpha1
kind: LVMCluster
metadata:
  name: my-lvmcluster
  namespace: openshift-storage
spec:
  storage:
    deviceClasses:
      - name: vg1
        thinPoolConfig:
          name: thin-pool-1
          sizePercent: 90
          overprovisionRatio: 10
EOF
echo "Waiting for LVMS StorageClass..."
retry_until 300 5 "${remote_oc} get sc lvms-vg1 2>/dev/null" || { echo "Timed out waiting for LVMS StorageClass"; exit 1; }
oc "${remote_args[@]}" annotate sc lvms-vg1 storageclass.kubernetes.io/is-default-class=true --overwrite

# CNV operator
cat <<EOF | oc "${remote_args[@]}" apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: openshift-cnv
  labels:
    openshift.io/cluster-monitoring: "true"
---
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: kubevirt-hyperconverged-group
  namespace: openshift-cnv
spec:
  targetNamespaces:
    - openshift-cnv
---
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: hco-operatorhub
  namespace: openshift-cnv
spec:
  source: redhat-operators
  sourceNamespace: openshift-marketplace
  name: kubevirt-hyperconverged
  channel: "stable"
EOF
echo "Waiting for CNV CRD..."
retry_until 300 10 "${remote_oc} get crd hyperconvergeds.hco.kubevirt.io 2>/dev/null" || { echo "Timed out waiting for CNV CRD"; exit 1; }

# HyperConverged instance
cat <<EOF | oc "${remote_args[@]}" apply -f -
apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: openshift-cnv
EOF
echo "Waiting for CNV to be available..."
retry_until 600 15 "${remote_oc} get hyperconverged kubevirt-hyperconverged -n openshift-cnv \
    -o jsonpath='{.status.conditions[?(@.type==\"Available\")].status}' 2>/dev/null | grep -q True" || { echo "Timed out waiting for CNV to be available"; exit 1; }
echo "CNV ready"

else
echo "Skipping prerequisites (LVMS, CNV) -- SKIP_PREREQUISITES=true"
fi

# Remote cluster: prepare for OSAC

oc "${remote_args[@]}" create namespace "${INSTALLER_NAMESPACE}" --dry-run=client -o yaml | oc "${remote_args[@]}" apply -f -
oc "${remote_args[@]}" label sc "${REMOTE_STORAGE_CLASS}" "osac.openshift.io/tenant=${INSTALLER_NAMESPACE}" --overwrite
oc "${remote_args[@]}" create serviceaccount osac-remote-access -n "${INSTALLER_NAMESPACE}" --dry-run=client -o yaml | oc "${remote_args[@]}" apply -f -
oc "${remote_args[@]}" adm policy add-cluster-role-to-user cluster-admin \
    "system:serviceaccount:${INSTALLER_NAMESPACE}:osac-remote-access"

if [[ "${MODE}" == "prepare" ]]; then
    echo "Workload cluster prepared; run install-osac with REMOTE_KUBECONFIG and REMOTE_API_ADDRESS to install OSAC in remote-cluster mode"
    exit 0
fi

# Keep kubeconfig generation and Secret staging in one implementation.
HUB_KUBECONFIG="${HUB_KUBECONFIG}" \
REMOTE_KUBECONFIG="${REMOTE_KUBECONFIG}" \
REMOTE_API_ADDRESS="${REMOTE_API_ADDRESS}" \
INSTALLER_NAMESPACE="${INSTALLER_NAMESPACE}" \
REMOTE_KUBECONFIG_SECRET_NAME="${REMOTE_KUBECONFIG_SECRET_NAME}" \
REMOTE_KUBECONFIG_SECRET_KEY="${REMOTE_KUBECONFIG_SECRET_KEY}" \
bash "${SCRIPT_DIR}/stage-remote-cluster-secret.sh"

# Legacy post-install mode: configure an already-installed management cluster.

OPERATOR_PATCH=$(cat <<EOF
{
  "spec": {"template": {"spec": {
    "volumes": [{"name": "remote-kubeconfig", "secret": {"secretName": "${REMOTE_KUBECONFIG_SECRET_NAME}"}}],
    "containers": [{"name": "manager",
      "volumeMounts": [{"name": "remote-kubeconfig", "mountPath": "/var/run/secrets/remote", "readOnly": true}],
      "env": [{"name": "OSAC_REMOTE_CLUSTER_KUBECONFIG", "value": "/var/run/secrets/remote/${REMOTE_KUBECONFIG_SECRET_KEY}"}]
    }]
  }}}
}
EOF
)
oc "${hub_args[@]}" patch deployment "${OPERATOR_DEPLOYMENT_NAME}" -n "${INSTALLER_NAMESPACE}" --type=strategic -p "${OPERATOR_PATCH}"

oc "${hub_args[@]}" patch secret config-as-code-ig -n "${INSTALLER_NAMESPACE}" --type=strategic -p "{
  \"stringData\": {
    \"REMOTE_CLUSTER_KUBECONFIG_SECRET_NAME\": \"${REMOTE_KUBECONFIG_SECRET_NAME}\",
    \"REMOTE_CLUSTER_KUBECONFIG_SECRET_KEY\": \"${REMOTE_KUBECONFIG_SECRET_KEY}\"
  }
}"

# Re-run AAP config-as-code to pick up the remote cluster kubeconfig
AAP_PASSWORD=$(oc "${hub_args[@]}" get secret osac-aap-admin-password -n "${INSTALLER_NAMESPACE}" -o jsonpath='{.data.password}' | base64 -d)
[[ -z "${AAP_PASSWORD}" ]] && echo "ERROR: Failed to get AAP password from secret osac-aap-admin-password" && exit 1

AAP_TOKEN=$(oc "${hub_args[@]}" exec deployment/fulfillment-grpc-server -n "${INSTALLER_NAMESPACE}" -- \
    sh -c "curl -sf -X POST http://osac-aap:80/api/controller/v2/tokens/ \
    -u admin:${AAP_PASSWORD} -H 'Content-Type: application/json' -d '{}'" | jq -r '.token')
[[ -z "${AAP_TOKEN}" || "${AAP_TOKEN}" == "null" ]] && echo "ERROR: Failed to create AAP token" && exit 1

JOB_ID=$(oc "${hub_args[@]}" exec deployment/fulfillment-grpc-server -n "${INSTALLER_NAMESPACE}" -- \
    sh -c "curl -sf -X POST http://osac-aap:80/api/controller/v2/job_templates/osac-config-as-code/launch/ \
    -H 'Authorization: Bearer ${AAP_TOKEN}' -H 'Content-Type: application/json' -d '{}'" | jq -r '.id')
[[ -z "${JOB_ID}" || "${JOB_ID}" == "null" ]] && echo "ERROR: Failed to launch config-as-code job" && exit 1

echo "Waiting for config-as-code job ${JOB_ID}..."
timeout 900 bash -c "
    until STATUS=\$(${hub_oc} exec deployment/fulfillment-grpc-server -n ${INSTALLER_NAMESPACE} -- \
        sh -c \"curl -sk http://osac-aap:80/api/controller/v2/jobs/${JOB_ID}/ \
        -H 'Authorization: Bearer ${AAP_TOKEN}'\" 2>/dev/null | jq -r '.status') && \
        [[ \"\${STATUS}\" == 'successful' || \"\${STATUS}\" == 'failed' ]]; do
        sleep 10
    done
    [[ \"\${STATUS}\" == 'successful' ]]
" || { echo "AAP config-as-code job failed or timed out"; exit 1; }

echo "Remote cluster setup complete"
