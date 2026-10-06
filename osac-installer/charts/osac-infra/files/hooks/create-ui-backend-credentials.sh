#!/usr/bin/env bash
set -euo pipefail

KEYCLOAK_NAMESPACE="${KEYCLOAK_NAMESPACE:-keycloak}"
OSAC_NAMESPACE="${OSAC_NAMESPACE:?OSAC_NAMESPACE is required}"
SECRET_NAME="keycloak-client-secrets"
CRED_SECRET_NAME="osac-ui-backend-credentials"

TMPDIR_CREDS="$(mktemp -d)"
trap 'rm -rf "${TMPDIR_CREDS}"' EXIT

echo "Updating ${CRED_SECRET_NAME} in ${OSAC_NAMESPACE}..."

echo "Reading osac-ui-backend secret from ${SECRET_NAME}..."
oc get secret "${SECRET_NAME}" -n "${KEYCLOAK_NAMESPACE}" \
    -o jsonpath='{.data.osac-ui-backend}' | base64 -d > "${TMPDIR_CREDS}/client-secret"
chmod 600 "${TMPDIR_CREDS}/client-secret"

[[ -s "${TMPDIR_CREDS}/client-secret" ]] || {
    echo "ERROR: Could not read osac-ui-backend from ${SECRET_NAME} in ${KEYCLOAK_NAMESPACE}" >&2
    exit 1
}

oc create secret generic "${CRED_SECRET_NAME}" \
    --from-literal=client-id=osac-ui-backend \
    --from-file=client-secret="${TMPDIR_CREDS}/client-secret" \
    -n "${OSAC_NAMESPACE}" \
    --dry-run=client -o yaml | oc apply -f -

echo "${CRED_SECRET_NAME} updated in ${OSAC_NAMESPACE}"
