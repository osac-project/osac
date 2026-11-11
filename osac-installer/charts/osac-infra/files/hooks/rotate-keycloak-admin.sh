#!/usr/bin/env bash
set -euo pipefail

: "${CURRENT_ADMIN_USERNAME:?CURRENT_ADMIN_USERNAME must be set}"
: "${CURRENT_ADMIN_PASSWORD:?CURRENT_ADMIN_PASSWORD must be set}"
: "${NEW_ADMIN_PASSWORD:?NEW_ADMIN_PASSWORD must be set}"
: "${KEYCLOAK_URL:?KEYCLOAK_URL must be set}"

KEYCLOAK_CA_FILE="${KEYCLOAK_CA_FILE:-/etc/keycloak/ca/ca.crt}"
keycloak_curl() {
    curl --cacert "${KEYCLOAK_CA_FILE}" --fail --silent --show-error "$@"
}

echo "Waiting for Keycloak before rotating the master admin password..."
for attempt in $(seq 1 120); do
    if keycloak_curl "${KEYCLOAK_URL}/realms/master" >/dev/null 2>&1; then
        break
    fi
    if [[ "${attempt}" -eq 120 ]]; then
        echo "ERROR: Keycloak was not ready after 600s" >&2
        exit 1
    fi
    sleep 5
done

request_token() {
    local password=$1
    keycloak_curl -X POST "${KEYCLOAK_URL}/realms/master/protocol/openid-connect/token" \
        -H 'Content-Type: application/x-www-form-urlencoded' \
        -d 'grant_type=password' \
        -d 'client_id=admin-cli' \
        --data-urlencode "username=${CURRENT_ADMIN_USERNAME}" \
        --data-urlencode "password=${password}" \
        | python3 -c 'import json,sys; print(json.load(sys.stdin).get("access_token", ""))'
}

token=""
already_rotated=false
if token=$(request_token "${CURRENT_ADMIN_PASSWORD}" 2>/dev/null) && [[ -n "${token}" ]]; then
    :
elif token=$(request_token "${NEW_ADMIN_PASSWORD}" 2>/dev/null) && [[ -n "${token}" ]]; then
    # A previous attempt may have rotated the account before it could update
    # the Kubernetes Secret. Continue idempotently with the requested password.
    already_rotated=true
else
    echo "ERROR: neither the stored nor requested Keycloak admin password authenticates" >&2
    exit 1
fi

users_json=$(keycloak_curl --get "${KEYCLOAK_URL}/admin/realms/master/users" \
    --data-urlencode "username=${CURRENT_ADMIN_USERNAME}" \
    --data-urlencode 'exact=true' \
    -H "Authorization: Bearer ${token}")
user_id=$(printf '%s' "${users_json}" | python3 -c '
import json
import sys

username = sys.argv[1]
matches = [user for user in json.load(sys.stdin) if user.get("username") == username]
if len(matches) != 1 or not matches[0].get("id"):
    raise SystemExit("expected exactly one Keycloak master admin account")
print(matches[0]["id"])
' "${CURRENT_ADMIN_USERNAME}")

if [[ "${already_rotated}" == false && "${CURRENT_ADMIN_PASSWORD}" != "${NEW_ADMIN_PASSWORD}" ]]; then
    reset_payload=$(python3 -c \
        'import json,sys; print(json.dumps({"type":"password","value":sys.argv[1],"temporary":False}))' \
        "${NEW_ADMIN_PASSWORD}")
    keycloak_curl -X PUT "${KEYCLOAK_URL}/admin/realms/master/users/${user_id}/reset-password" \
        -H "Authorization: Bearer ${token}" \
        -H 'Content-Type: application/json' \
        -d "${reset_payload}" >/dev/null
fi

patch_payload=$(python3 -c '
import base64
import json
import sys

encoded = base64.b64encode(sys.argv[1].encode()).decode()
print(json.dumps({"data": {"admin-password": encoded}}))
' "${NEW_ADMIN_PASSWORD}")
oc patch secret keycloak-admin-credentials -n keycloak --type=merge -p "${patch_payload}" >/dev/null
oc delete secret keycloak-admin-password-rotation -n keycloak --ignore-not-found=true >/dev/null
echo "Keycloak master admin password rotation completed"
