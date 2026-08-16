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

REALM_ADMIN_USERNAME="${REALM_ADMIN_USERNAME:-admin}"
REALM_ADMIN_PASSWORD="${REALM_ADMIN_PASSWORD:?REALM_ADMIN_PASSWORD must be set}"

# REALM_ADMIN_USERNAME/PASSWORD are user-supplied Helm values (unlike the
# auto-generated base64 client secrets above) substituted into JSON string
# values, so each needs two escaping passes: first full JSON string escaping
# (via python3's json.dumps, so control characters like tabs/newlines are
# handled correctly, not just backslash/quote) since the raw value lands
# inside a JSON string, then sed replacement-escaping (backslash, ampersand,
# and the s### delimiter) so sed doesn't reinterpret characters introduced
# by the JSON escaping.
json_escape_string() {
    python3 -c "import json,sys; print(json.dumps(sys.argv[1])[1:-1])" "$1"
}

escape_sed_replacement() {
    printf '%s' "$1" | sed -e 's/[\&#]/\\&/g'
}

sed \
    -e "s#__OSAC_CONTROLLER_CLIENT_SECRET__#${CONTROLLER_SECRET}#" \
    -e "s#__OSAC_ADMIN_CLIENT_SECRET__#${ADMIN_SECRET}#" \
    -e "s#__OSAC_REALM_ADMIN_USERNAME__#$(escape_sed_replacement "$(json_escape_string "${REALM_ADMIN_USERNAME}")")#" \
    -e "s#__OSAC_REALM_ADMIN_PASSWORD__#$(escape_sed_replacement "$(json_escape_string "${REALM_ADMIN_PASSWORD}")")#" \
    "${RAW_REALM}" > "${RESOLVED_REALM}"

echo "Realm secrets resolved -> ${RESOLVED_REALM}"
