#!/usr/bin/env bash
set -euo pipefail

# Provision a NetBox API token via POST /api/users/tokens/provision/ and write
# it to a Secret in the operator namespace. Idempotent: exits 0 if the Secret
# already exists and its token still authenticates (e.g. on upgrade). A token
# NetBox rejects (401/403 — database reset, revocation) is re-provisioned and
# the Secret is replaced only once the replacement is in hand; any other
# validation failure (NetBox unreachable, 5xx) keeps the Secret and fails the
# Job so its backoff can retry.
#
# Required env vars (set by the Job spec):
#   OPERATOR_NS       — namespace where the operator reads the token Secret
#   SECRET_NAME       — name of the Secret to create
#   NETBOX_SVC        — base URL of the NetBox service
#   NETBOX_USERNAME   — NetBox superuser username
#
# Required volume mount:
#   /credentials/password — superuser password (from osac-infra-netbox-superuser Secret)

PASS="$(cat /credentials/password)"

until curl -sf "${NETBOX_SVC}/login/" > /dev/null; do
  echo "Waiting for NetBox..."
  sleep 5
done

if oc -n "${OPERATOR_NS}" get secret "${SECRET_NAME}" > /dev/null 2>&1; then
  EXISTING="$(oc -n "${OPERATOR_NS}" get secret "${SECRET_NAME}" -o jsonpath='{.data.token}' | base64 -d)"
  STATUS="$(curl -s -o /dev/null -w '%{http_code}' \
    -H "Authorization: Bearer ${EXISTING}" "${NETBOX_SVC}/api/" || true)"
  if [ "${STATUS}" = "200" ]; then
    echo "Secret ${SECRET_NAME} exists and its token authenticates — skipping."
    exit 0
  fi
  if [ "${STATUS}" != "401" ] && [ "${STATUS}" != "403" ]; then
    echo "Cannot validate stored token (HTTP ${STATUS}) — retrying." >&2
    exit 1
  fi
  echo "Stored token rejected (HTTP ${STATUS}) — re-provisioning."
fi

PAYLOAD=$(python3 -c "import json,sys; print(json.dumps({'username':sys.argv[1],'password':sys.argv[2]}))" \
  "${NETBOX_USERNAME}" "${PASS}")

RESP=$(curl -sf -X POST "${NETBOX_SVC}/api/users/tokens/provision/" \
  -H "Content-Type: application/json" \
  --data-raw "${PAYLOAD}")

KEY=$(echo "${RESP}" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['key'])")
TOK=$(echo "${RESP}" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['token'])")
TOKEN="nbt_${KEY}.${TOK}"

# The replacement token is in hand: create or update the Secret without a
# delete window that could leave the operator without a token.
oc -n "${OPERATOR_NS}" create secret generic "${SECRET_NAME}" \
  --from-literal=token="${TOKEN}" --dry-run=client -o yaml \
  | oc -n "${OPERATOR_NS}" apply -f -

echo "Secret ${SECRET_NAME} written to namespace ${OPERATOR_NS}."
