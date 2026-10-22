#!/usr/bin/env bash
# Fetch a short-lived Fulfillment access token from the configured Keycloak realm.
get_fulfillment_access_token() {
  local issuer_url="${FULFILLMENT_ISSUER_URL:-}"
  local client_id="${FULFILLMENT_CLIENT_ID:-}"
  local client_secret_file="${FULFILLMENT_CLIENT_SECRET_FILE:-}"
  local ca_file="${CA_FILE:-/etc/ca-bundle/bundle.pem}"
  if [[ "${issuer_url}" != https://* || -z "${client_id}" || -z "${client_secret_file}" \
    || ! -s "${client_secret_file}" || ! -r "${ca_file}" ]]; then
    echo "ERROR: Fulfillment service-account credentials are unavailable." >&2
    return 1
  fi

  local token_url="${issuer_url%/}/protocol/openid-connect/token"
  local response
  if ! response=$(curl --silent --show-error --fail --cacert "${ca_file}" \
    --connect-timeout 5 --max-time 30 \
    --request POST \
    --data-urlencode 'grant_type=client_credentials' \
    --data-urlencode "client_id=${client_id}" \
    --data-urlencode "client_secret@${client_secret_file}" \
    "${token_url}"); then
    echo "ERROR: Fulfillment token request failed." >&2
    return 1
  fi

  local access_token
  if ! access_token=$(printf '%s' "${response}" | python3 -c '
import json
import sys

try:
    token = json.load(sys.stdin).get("access_token")
except (json.JSONDecodeError, AttributeError):
    sys.exit(1)

if not isinstance(token, str) or not token:
    sys.exit(1)

sys.stdout.write(token)
'); then
    echo "ERROR: Fulfillment token response was invalid." >&2
    return 1
  fi

  printf '%s\n' "${access_token}"
}
