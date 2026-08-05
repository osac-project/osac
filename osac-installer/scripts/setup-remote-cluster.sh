#!/usr/bin/env bash
# CI-only: assumes a fresh remote cluster, not idempotent.

set -o nounset
set -o errexit
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

HUB_KUBECONFIG=${HUB_KUBECONFIG:?"HUB_KUBECONFIG must be set"}
REMOTE_KUBECONFIG=${REMOTE_KUBECONFIG:?"REMOTE_KUBECONFIG must be set"}
REMOTE_API_ADDRESS=${REMOTE_API_ADDRESS:?"REMOTE_API_ADDRESS must be set (e.g. https://192.168.128.10:6443)"}
INSTALLER_NAMESPACE=${INSTALLER_NAMESPACE:-"osac"}

hub="--kubeconfig ${HUB_KUBECONFIG}"
remote="--kubeconfig ${REMOTE_KUBECONFIG}"

SKIP_PREREQUISITES=${SKIP_PREREQUISITES:-"false"}

if [[ "${SKIP_PREREQUISITES}" != "true" ]]; then

OCP_VERSION=$(oc ${remote} version -o json | jq -r '.openshiftVersion' | cut -d. -f1-2)

# Remote cluster: install prerequisites

# LVMS
cat <<EOF | oc ${remote} apply -f -
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
retry_until 180 5 "oc ${remote} get crd lvmclusters.lvm.topolvm.io 2>/dev/null" || { echo "Timed out waiting for LVMS CRD"; exit 1; }
echo "Waiting for LVMS operator webhook..."
retry_until 180 5 "oc ${remote} rollout status deployment/lvms-operator -n openshift-storage --timeout=5s 2>/dev/null" || { echo "Timed out waiting for LVMS operator"; exit 1; }

cat <<EOF | oc ${remote} apply -f -
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
retry_until 300 5 "oc ${remote} get sc lvms-vg1 2>/dev/null" || { echo "Timed out waiting for LVMS StorageClass"; exit 1; }
oc ${remote} annotate sc lvms-vg1 storageclass.kubernetes.io/is-default-class=true --overwrite

# CNV operator
cat <<EOF | oc ${remote} apply -f -
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
retry_until 300 10 "oc ${remote} get crd hyperconvergeds.hco.kubevirt.io 2>/dev/null" || { echo "Timed out waiting for CNV CRD"; exit 1; }

# HyperConverged instance
cat <<EOF | oc ${remote} apply -f -
apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: openshift-cnv
EOF
echo "Waiting for CNV to be available..."
retry_until 600 15 "oc ${remote} get hyperconverged kubevirt-hyperconverged -n openshift-cnv \
    -o jsonpath='{.status.conditions[?(@.type==\"Available\")].status}' 2>/dev/null | grep -q True" || { echo "Timed out waiting for CNV to be available"; exit 1; }
echo "CNV ready"

else
echo "Skipping prerequisites (LVMS, CNV) -- SKIP_PREREQUISITES=true"
fi

# Remote cluster: prepare for OSAC

REMOTE_STORAGE_CLASS=${REMOTE_STORAGE_CLASS:-"lvms-vg1"}

oc ${remote} create namespace ${INSTALLER_NAMESPACE}
oc ${remote} label sc "${REMOTE_STORAGE_CLASS}" "osac.openshift.io/tenant=${INSTALLER_NAMESPACE}" --overwrite
oc ${remote} create serviceaccount osac-remote-access -n ${INSTALLER_NAMESPACE}
oc ${remote} adm policy add-cluster-role-to-user cluster-admin \
    "system:serviceaccount:${INSTALLER_NAMESPACE}:osac-remote-access"

REMOTE_TOKEN=$(oc ${remote} create token osac-remote-access -n ${INSTALLER_NAMESPACE} --duration=8760h)

REMOTE_KUBECONFIG_FILE=$(mktemp)
cat > "${REMOTE_KUBECONFIG_FILE}" <<EOF
apiVersion: v1
kind: Config
clusters:
- cluster:
    insecure-skip-tls-verify: true
    server: ${REMOTE_API_ADDRESS}
  name: remote
contexts:
- context:
    cluster: remote
    user: osac-remote-access
    namespace: ${INSTALLER_NAMESPACE}
  name: remote
current-context: remote
users:
- name: osac-remote-access
  user:
    token: ${REMOTE_TOKEN}
EOF

# Hub cluster: configure operator for remote cluster

oc ${hub} create secret generic osac-remote-kubeconfig \
    --from-file=kubeconfig="${REMOTE_KUBECONFIG_FILE}" \
    -n ${INSTALLER_NAMESPACE}
oc ${hub} label secret osac-remote-kubeconfig \
    osac.openshift.io/remote-cluster-kubeconfig=true \
    -n ${INSTALLER_NAMESPACE}

rm -f "${REMOTE_KUBECONFIG_FILE}"

oc ${hub} patch deployment osac-operator-controller-manager -n ${INSTALLER_NAMESPACE} --type=strategic -p '{
  "spec": {"template": {"spec": {
    "volumes": [{"name": "remote-kubeconfig", "secret": {"secretName": "osac-remote-kubeconfig"}}],
    "containers": [{"name": "manager",
      "volumeMounts": [{"name": "remote-kubeconfig", "mountPath": "/var/run/secrets/remote", "readOnly": true}],
      "env": [{"name": "OSAC_REMOTE_CLUSTER_KUBECONFIG", "value": "/var/run/secrets/remote/kubeconfig"}]
    }]
  }}}
}'

oc ${hub} patch secret config-as-code-ig -n ${INSTALLER_NAMESPACE} --type=strategic -p "{
  \"stringData\": {
    \"REMOTE_CLUSTER_KUBECONFIG_SECRET_NAME\": \"osac-remote-kubeconfig\",
    \"REMOTE_CLUSTER_KUBECONFIG_SECRET_KEY\": \"kubeconfig\"
  }
}"

# Re-run AAP config-as-code to pick up the remote cluster kubeconfig
AAP_PASSWORD=$(oc ${hub} get secret osac-aap-admin-password -n ${INSTALLER_NAMESPACE} -o jsonpath='{.data.password}' | base64 -d)
[[ -z "${AAP_PASSWORD}" ]] && echo "ERROR: Failed to get AAP password from secret osac-aap-admin-password" && exit 1

AAP_TOKEN=$(oc ${hub} exec deployment/fulfillment-grpc-server -n ${INSTALLER_NAMESPACE} -- \
    sh -c "curl -sf -X POST http://osac-aap:80/api/controller/v2/tokens/ \
    -u admin:${AAP_PASSWORD} -H 'Content-Type: application/json' -d '{}'" | jq -r '.token')
[[ -z "${AAP_TOKEN}" || "${AAP_TOKEN}" == "null" ]] && echo "ERROR: Failed to create AAP token" && exit 1

JOB_ID=$(oc ${hub} exec deployment/fulfillment-grpc-server -n ${INSTALLER_NAMESPACE} -- \
    sh -c "curl -sf -X POST http://osac-aap:80/api/controller/v2/job_templates/osac-config-as-code/launch/ \
    -H 'Authorization: Bearer ${AAP_TOKEN}' -H 'Content-Type: application/json' -d '{}'" | jq -r '.id')
[[ -z "${JOB_ID}" || "${JOB_ID}" == "null" ]] && echo "ERROR: Failed to launch config-as-code job" && exit 1

echo "Waiting for config-as-code job ${JOB_ID}..."
timeout 900 bash -c "
    until STATUS=\$(oc ${hub} exec deployment/fulfillment-grpc-server -n ${INSTALLER_NAMESPACE} -- \
        sh -c \"curl -sk http://osac-aap:80/api/controller/v2/jobs/${JOB_ID}/ \
        -H 'Authorization: Bearer ${AAP_TOKEN}'\" 2>/dev/null | jq -r '.status') && \
        [[ \"\${STATUS}\" == 'successful' || \"\${STATUS}\" == 'failed' ]]; do
        sleep 10
    done
    [[ \"\${STATUS}\" == 'successful' ]]
" || { echo "AAP config-as-code job failed or timed out"; exit 1; }

echo "Remote cluster setup complete"
