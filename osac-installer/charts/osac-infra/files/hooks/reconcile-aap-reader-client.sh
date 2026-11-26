#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=ensure-aap-reader-secret.sh
source "$(dirname "${BASH_SOURCE[0]}")/ensure-aap-reader-secret.sh"

KEYCLOAK_URL=https://keycloak:443
KEYCLOAK_CA_FILE=/etc/keycloak/ca/ca.crt
KEYCLOAK_ADMIN_DIR=/etc/keycloak/admin
REALM_ADMIN_URL="${KEYCLOAK_URL}/admin/realms/osac"
CLIENTS_URL="${REALM_ADMIN_URL}/clients"
CLIENT_ID=osac-aap-secret-reader
REALM_RAW_PATH="${REALM_RAW_PATH:-/realm-raw/realm.json}"

keycloak_curl() {
    curl --fail --silent --show-error --cacert "${KEYCLOAK_CA_FILE}" \
        --connect-timeout 10 --max-time 30 "$@"
}

reader_secret=$(ensure_aap_reader_secret keycloak)
client_definition=$(python3 -c '
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    client = next(c for c in json.load(source)["clients"] if c["clientId"] == sys.argv[2])
json.dump(client, sys.stdout)
' "${REALM_RAW_PATH}" "${CLIENT_ID}")

admin_token=$(
    keycloak_curl "${KEYCLOAK_URL}/realms/master/protocol/openid-connect/token" \
        --data 'grant_type=password' --data 'client_id=admin-cli' \
        --data-urlencode "username@${KEYCLOAK_ADMIN_DIR}/admin-username" \
        --data-urlencode "password@${KEYCLOAK_ADMIN_DIR}/admin-password" \
        | python3 -c 'import json, sys; print(json.load(sys.stdin)["access_token"])'
)
keycloak_admin() {
    keycloak_curl --header @<(printf 'Authorization: Bearer %s\n' "${admin_token}") "$@"
}

client_uuid=$(
    keycloak_admin --get "${CLIENTS_URL}" --data-urlencode "clientId=${CLIENT_ID}" --data 'exact=true' \
        | python3 -c 'import json, sys; print(next((c["id"] for c in json.load(sys.stdin) if c["clientId"] == sys.argv[1]), ""))' "${CLIENT_ID}"
)
method=POST
url="${CLIENTS_URL}"
if [[ -n "${client_uuid}" ]]; then
    method=PUT
    url="${CLIENTS_URL}/${client_uuid}"
fi

echo "Reconciling Keycloak client ${CLIENT_ID}..."
printf '%s' "${reader_secret}" | python3 -c '
import json
import sys

client = json.loads(sys.argv[1])
client["secret"] = sys.stdin.read()
json.dump(client, sys.stdout)
' "${client_definition}" | keycloak_admin --request "${method}" "${url}" \
    --header 'Content-Type: application/json' --data-binary @- >/dev/null

# Creation applies the scope lists from the representation. Keycloak's client
# update API ignores those lists, so reconcile existing associations separately.
if [[ -n "${client_uuid}" ]]; then
    realm_scopes=$(keycloak_admin "${REALM_ADMIN_URL}/client-scopes")
    default_scopes=$(keycloak_admin "${url}/default-client-scopes")
    optional_scopes=$(keycloak_admin "${url}/optional-client-scopes")
    scope_changes=$(python3 -c '
import json
import sys

client, realm, defaults, optional = map(json.loads, sys.argv[1:])
scope_ids = {scope["name"]: scope["id"] for scope in realm}
removals, additions = [], []
for kind, desired, scopes in (
    ("default", client["defaultClientScopes"], defaults),
    ("optional", client["optionalClientScopes"], optional),
):
    current = {scope["name"]: scope["id"] for scope in scopes}
    for name in sorted(current.keys() - set(desired)):
        removals.append(f"DELETE {kind} {current[name]}")
    for name in sorted(set(desired) - current.keys()):
        if name not in scope_ids:
            sys.exit(f"Required AAP reader client scope {name} is missing from the realm.")
        additions.append(f"PUT {kind} {scope_ids[name]}")
print("\n".join(removals + additions))
' "${client_definition}" "${realm_scopes}" "${default_scopes}" "${optional_scopes}")
    if [[ -n "${scope_changes}" ]]; then
        while read -r method kind scope_id; do
            keycloak_admin --request "${method}" "${url}/${kind}-client-scopes/${scope_id}" >/dev/null
        done <<< "${scope_changes}"
    fi
fi
