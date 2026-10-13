#!/usr/bin/env bash
set -euo pipefail

KEYCLOAK_NAMESPACE="${KEYCLOAK_NAMESPACE:-keycloak}"
OSAC_NAMESPACE="${OSAC_NAMESPACE:?OSAC_NAMESPACE is required}"
SECRET_NAME="keycloak-client-secrets"
CRED_SECRET_NAME="osac-bmf-secret-api-credentials"

TMPDIR_CREDS="$(mktemp -d)"
trap 'rm -rf "${TMPDIR_CREDS}"' EXIT

echo "Updating ${CRED_SECRET_NAME} in ${OSAC_NAMESPACE}..."
echo "Reading osac-bmf secret from ${SECRET_NAME}..."
oc get secret "${SECRET_NAME}" -n "${KEYCLOAK_NAMESPACE}" \
    -o jsonpath='{.data.osac-bmf}' | base64 -d > "${TMPDIR_CREDS}/client-secret"
chmod 600 "${TMPDIR_CREDS}/client-secret"

[[ -s "${TMPDIR_CREDS}/client-secret" ]] || {
    echo "ERROR: Could not read osac-bmf from ${SECRET_NAME} in ${KEYCLOAK_NAMESPACE}" >&2
    exit 1
}

printf '%s' 'osac-bmf' > "${TMPDIR_CREDS}/client-id"
chmod 600 "${TMPDIR_CREDS}/client-id"

oc create secret generic "${CRED_SECRET_NAME}" \
    --from-file=client-id="${TMPDIR_CREDS}/client-id" \
    --from-file=client-secret="${TMPDIR_CREDS}/client-secret" \
    -n "${OSAC_NAMESPACE}" \
    --dry-run=client -o yaml | oc apply -f -

echo "${CRED_SECRET_NAME} updated in ${OSAC_NAMESPACE}"
