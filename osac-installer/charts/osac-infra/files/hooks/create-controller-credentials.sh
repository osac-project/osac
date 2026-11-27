#!/usr/bin/env bash
set -euo pipefail

KEYCLOAK_NAMESPACE="${KEYCLOAK_NAMESPACE:-keycloak}"
OSAC_NAMESPACE="${OSAC_NAMESPACE:?OSAC_NAMESPACE is required}"
SECRET_NAME="keycloak-client-secrets"
CRED_SECRET_NAME="fulfillment-controller-credentials"

echo "Creating ${CRED_SECRET_NAME} in ${OSAC_NAMESPACE}..."

oc create namespace "${OSAC_NAMESPACE}" --dry-run=client -o yaml | oc apply -f -

echo "Reading osac-controller secret from ${SECRET_NAME}..."
CLIENT_SECRET=$(oc get secret "${SECRET_NAME}" -n "${KEYCLOAK_NAMESPACE}" \
    -o jsonpath='{.data.osac-controller}' | base64 -d)

[[ -n "${CLIENT_SECRET}" ]] || {
    echo "ERROR: Could not read osac-controller from ${SECRET_NAME} in ${KEYCLOAK_NAMESPACE}" >&2
    exit 1
}

oc create secret generic "${CRED_SECRET_NAME}" \
    --from-literal=client-id=osac-controller \
    --from-literal=client-secret="${CLIENT_SECRET}" \
    -n "${OSAC_NAMESPACE}" \
    --dry-run=client -o yaml | oc apply -f -

echo "${CRED_SECRET_NAME} created in ${OSAC_NAMESPACE}"

# Publish the dedicated credential for the AAP bootstrap pod. The job runner
# receives its own runtime file from AAP Controller.
AAP_READER_SECRET=$(mktemp)
trap 'unlink "${AAP_READER_SECRET}"' EXIT
oc get secret "${SECRET_NAME}" -n "${KEYCLOAK_NAMESPACE}" \
    -o jsonpath='{.data.osac-aap-secret-reader}' | base64 -d >"${AAP_READER_SECRET}"
[[ -s "${AAP_READER_SECRET}" ]] || {
    echo "ERROR: ${SECRET_NAME} is missing the AAP Secret reader credential" >&2
    exit 1
}
oc create secret generic aap-fulfillment-secret-reader -n "${OSAC_NAMESPACE}" \
    --from-file=client-secret="${AAP_READER_SECRET}" --dry-run=client -o yaml \
    | oc apply --server-side --field-manager=osac-infra -f -
