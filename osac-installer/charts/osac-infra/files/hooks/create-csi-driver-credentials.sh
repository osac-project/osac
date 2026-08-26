#!/usr/bin/env bash
set -euo pipefail

KEYCLOAK_NAMESPACE="${KEYCLOAK_NAMESPACE:-keycloak}"
OSAC_NAMESPACE="${OSAC_NAMESPACE:?OSAC_NAMESPACE is required}"
SECRET_NAME="keycloak-client-secrets"
CRED_SECRET_NAME="osac-csi-driver-credentials"

TMPDIR_CREDS="$(mktemp -d)"
trap 'rm -rf "${TMPDIR_CREDS}"' EXIT

echo "Creating ${CRED_SECRET_NAME} in ${OSAC_NAMESPACE}..."

oc create namespace "${OSAC_NAMESPACE}" --dry-run=client -o yaml | oc apply -f -
oc label namespace "${OSAC_NAMESPACE}" app.kubernetes.io/managed-by=Helm --overwrite 2>/dev/null || true
oc annotate namespace "${OSAC_NAMESPACE}" meta.helm.sh/release-name="${HELM_RELEASE_NAME}" --overwrite 2>/dev/null || true
oc annotate namespace "${OSAC_NAMESPACE}" meta.helm.sh/release-namespace="${HELM_RELEASE_NAMESPACE}" --overwrite 2>/dev/null || true

echo "Reading osac-csi-driver secret from ${SECRET_NAME}..."
oc get secret "${SECRET_NAME}" -n "${KEYCLOAK_NAMESPACE}" \
    -o jsonpath='{.data.osac-csi-driver}' | base64 -d > "${TMPDIR_CREDS}/client-secret"
chmod 600 "${TMPDIR_CREDS}/client-secret"

[[ -s "${TMPDIR_CREDS}/client-secret" ]] || {
    echo "ERROR: Could not read osac-csi-driver from ${SECRET_NAME} in ${KEYCLOAK_NAMESPACE}" >&2
    exit 1
}

printf '%s' 'osac-csi-driver' > "${TMPDIR_CREDS}/client-id"

oc create secret generic "${CRED_SECRET_NAME}" \
    --from-file=client-id="${TMPDIR_CREDS}/client-id" \
    --from-file=client-secret="${TMPDIR_CREDS}/client-secret" \
    -n "${OSAC_NAMESPACE}" \
    --dry-run=client -o yaml | oc apply -f -

echo "${CRED_SECRET_NAME} created in ${OSAC_NAMESPACE}"
