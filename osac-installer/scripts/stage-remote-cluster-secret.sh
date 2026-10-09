#!/usr/bin/env bash
# Create the management-cluster Secret used by the OSAC operator and AAP.

set -euo pipefail

HUB_KUBECONFIG=${HUB_KUBECONFIG:-${KUBECONFIG:-}}
REMOTE_KUBECONFIG=${REMOTE_KUBECONFIG:?REMOTE_KUBECONFIG must be set}
REMOTE_API_ADDRESS=${REMOTE_API_ADDRESS:?REMOTE_API_ADDRESS must be set}
INSTALLER_NAMESPACE=${INSTALLER_NAMESPACE:-osac}
REMOTE_KUBECONFIG_SECRET_NAME=${REMOTE_KUBECONFIG_SECRET_NAME:-osac-remote-kubeconfig}
REMOTE_KUBECONFIG_SECRET_KEY=${REMOTE_KUBECONFIG_SECRET_KEY:-kubeconfig}

if [[ "${REMOTE_API_ADDRESS}" != https://* ]]; then
    echo "ERROR: REMOTE_API_ADDRESS must use https://" >&2
    exit 2
fi
[[ -r "${REMOTE_KUBECONFIG}" ]] || {
    echo "ERROR: REMOTE_KUBECONFIG is not readable: ${REMOTE_KUBECONFIG}" >&2
    exit 2
}

remote_args=(--kubeconfig "${REMOTE_KUBECONFIG}")
hub_oc() {
    if [[ -n "${HUB_KUBECONFIG}" ]]; then
        oc --kubeconfig "${HUB_KUBECONFIG}" "$@"
    else
        oc "$@"
    fi
}

management_api=$(hub_oc config view --minify -o jsonpath='{.clusters[0].cluster.server}')
workload_api=$(oc "${remote_args[@]}" config view --minify -o jsonpath='{.clusters[0].cluster.server}')
if [[ -z "${management_api}" || -z "${workload_api}" || "${management_api}" == "${workload_api}" ]]; then
    echo "ERROR: management and workload kubeconfigs must specify different API server endpoints" >&2
    exit 1
fi
workload_ca_data=$(oc "${remote_args[@]}" config view --minify --raw --flatten \
    -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')
if [[ -z "${workload_ca_data}" ]]; then
    echo "ERROR: REMOTE_KUBECONFIG must provide trusted certificate authority data" >&2
    exit 1
fi

hub_oc get namespace "${INSTALLER_NAMESPACE}" >/dev/null || {
    echo "ERROR: management namespace ${INSTALLER_NAMESPACE} does not exist; install infrastructure before staging remote access" >&2
    exit 1
}

REMOTE_TOKEN=$(oc "${remote_args[@]}" create token osac-remote-access \
    -n "${INSTALLER_NAMESPACE}" --duration=8760h)
if [[ -z "${REMOTE_TOKEN}" ]]; then
    echo "ERROR: failed to create token for ${INSTALLER_NAMESPACE}/osac-remote-access on the workload cluster" >&2
    exit 1
fi

umask 077
REMOTE_KUBECONFIG_FILE=$(mktemp)
chmod 600 "${REMOTE_KUBECONFIG_FILE}"
trap 'rm -f "${REMOTE_KUBECONFIG_FILE}"' EXIT
cat >"${REMOTE_KUBECONFIG_FILE}" <<EOF
apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: ${workload_ca_data}
    server: "${REMOTE_API_ADDRESS}"
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

hub_oc create secret generic "${REMOTE_KUBECONFIG_SECRET_NAME}" \
    --from-file="${REMOTE_KUBECONFIG_SECRET_KEY}=${REMOTE_KUBECONFIG_FILE}" \
    -n "${INSTALLER_NAMESPACE}" --dry-run=client -o yaml |
    hub_oc apply -f -
hub_oc label secret "${REMOTE_KUBECONFIG_SECRET_NAME}" \
    osac.openshift.io/remote-cluster-kubeconfig=true \
    -n "${INSTALLER_NAMESPACE}" --overwrite

echo "Staged remote-cluster Secret ${INSTALLER_NAMESPACE}/${REMOTE_KUBECONFIG_SECRET_NAME}"
