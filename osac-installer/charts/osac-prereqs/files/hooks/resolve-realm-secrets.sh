#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${KEYCLOAK_NAMESPACE:-keycloak}"
SECRET_NAME="keycloak-client-secrets"
RAW_REALM="${REALM_RAW_PATH:-/realm-raw/realm.json}"
RESOLVED_REALM="${REALM_OUTPUT_PATH:-/realm/realm.json}"

echo "Resolving Keycloak realm secrets..."

if ! oc get secret "${SECRET_NAME}" -n "${NAMESPACE}"; then
    echo "Generating osac-controller/osac-admin client secrets..."
    oc create secret generic "${SECRET_NAME}" -n "${NAMESPACE}" \
        --from-literal=osac-controller="$(openssl rand -base64 18)" \
        --from-literal=osac-admin="$(openssl rand -base64 18)"
fi

CONTROLLER_SECRET=$(oc get secret "${SECRET_NAME}" -n "${NAMESPACE}" -o jsonpath='{.data.osac-controller}' | base64 -d)
ADMIN_SECRET=$(oc get secret "${SECRET_NAME}" -n "${NAMESPACE}" -o jsonpath='{.data.osac-admin}' | base64 -d)

[[ -n "${CONTROLLER_SECRET}" ]] || { echo "ERROR: ${SECRET_NAME} missing osac-controller key" >&2; exit 1; }
[[ -n "${ADMIN_SECRET}" ]] || { echo "ERROR: ${SECRET_NAME} missing osac-admin key" >&2; exit 1; }

sed \
    -e "s#__OSAC_CONTROLLER_CLIENT_SECRET__#${CONTROLLER_SECRET}#" \
    -e "s#__OSAC_ADMIN_CLIENT_SECRET__#${ADMIN_SECRET}#" \
    "${RAW_REALM}" > "${RESOLVED_REALM}"

echo "Realm secrets resolved -> ${RESOLVED_REALM}"
