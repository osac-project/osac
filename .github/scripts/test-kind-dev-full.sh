#!/usr/bin/env bash
set -euo pipefail

OSAC_NAMESPACE="${OSAC_NAMESPACE:-osac}"
KEYCLOAK_NAMESPACE="${KEYCLOAK_NAMESPACE:-keycloak}"
ROLLOUT_TIMEOUT="${ROLLOUT_TIMEOUT:-5m}"
KEYCLOAK_URL="${KEYCLOAK_URL:-https://keycloak.osac.localhost:8443}"
FULFILLMENT_API_URL="${FULFILLMENT_API_URL:-https://fulfillment-api.osac.localhost:8443/api/fulfillment/v1/tenants}"
ADMIN_USERNAME="${OSAC_ADMIN_USERNAME:-tenant1_admin}"
EXPECTED_TENANT="${OSAC_EXPECTED_TENANT:-tenant1}"

for command in kubectl curl jq base64; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "Required command not found: ${command}" >&2
    exit 1
  fi
done

wait_for_deployment() {
  local namespace="$1"
  local deployment="$2"

  printf 'Waiting for deployment %s/%s ...\n' "${namespace}" "${deployment}"
  kubectl --namespace "${namespace}" rollout status "deployment/${deployment}" \
    --timeout="${ROLLOUT_TIMEOUT}"
}

# Fulfillment API server path, operator, AWX web/task, Keycloak, and UI.
wait_for_deployment "${OSAC_NAMESPACE}" fulfillment-ingress-proxy
wait_for_deployment "${OSAC_NAMESPACE}" fulfillment-rest-gateway
wait_for_deployment "${OSAC_NAMESPACE}" fulfillment-grpc-server
wait_for_deployment "${OSAC_NAMESPACE}" osac-operator
wait_for_deployment "${OSAC_NAMESPACE}" awx-web
wait_for_deployment "${OSAC_NAMESPACE}" awx-task
wait_for_deployment "${KEYCLOAK_NAMESPACE}" keycloak-service
wait_for_deployment "${OSAC_NAMESPACE}" osac-ui

printf 'Checking the OSAC UI Gateway route ...\n'
if ! curl --silent --show-error --fail \
  --connect-timeout 10 --max-time 30 --retry 5 --retry-connrefused --retry-delay 2 --retry-max-time 60 \
  --output /dev/null \
  "http://ui.osac.localhost:8080/"; then
  echo "The UI is not reachable through its Kind Gateway route." >&2
  exit 1
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT
ca_bundle="${tmp_dir}/ca-bundle.pem"

kubectl --namespace "${OSAC_NAMESPACE}" get configmap ca-bundle \
  -o jsonpath='{.data.bundle\.pem}' > "${ca_bundle}"
if [[ ! -s "${ca_bundle}" ]]; then
  echo "The ${OSAC_NAMESPACE}/ca-bundle ConfigMap has no bundle.pem." >&2
  exit 1
fi

password_b64="$(kubectl --namespace "${KEYCLOAK_NAMESPACE}" get secret keycloak-admin-credentials \
  -o jsonpath='{.data.default-user-password}')"
if [[ -z "${password_b64}" ]]; then
  echo "Keycloak default-user-password is missing." >&2
  exit 1
fi
admin_password="$(printf '%s' "${password_b64}" | base64 --decode)"

printf 'Authenticating as %s through Keycloak ...\n' "${ADMIN_USERNAME}"
token_response="$(curl --silent --show-error --fail-with-body \
  --connect-timeout 10 --max-time 30 --cacert "${ca_bundle}" \
  --request POST "${KEYCLOAK_URL}/realms/osac/protocol/openid-connect/token" \
  --data-urlencode 'grant_type=password' \
  --data-urlencode 'client_id=osac-cli' \
  --data-urlencode "username=${ADMIN_USERNAME}" \
  --data-urlencode "password=${admin_password}" \
  --data-urlencode 'scope=openid organization')"
access_token="$(jq -er '.access_token | select(type == "string" and length > 0)' <<<"${token_response}")"

printf 'Requesting tenant list from Fulfillment API ...\n'
tenant_response="$(curl --silent --show-error --fail-with-body \
  --connect-timeout 10 --max-time 30 --retry 5 --retry-connrefused --retry-delay 2 --retry-max-time 60 \
  --cacert "${ca_bundle}" \
  --header "Authorization: Bearer ${access_token}" \
  "${FULFILLMENT_API_URL}")"

if ! jq -e --arg expected_tenant "${EXPECTED_TENANT}" \
  '(.items | type == "array") and any(.items[]; .metadata.name == $expected_tenant)' \
  <<<"${tenant_response}" >/dev/null; then
  echo "Authenticated tenant list did not contain the expected '${EXPECTED_TENANT}' tenant." >&2
  exit 1
fi

tenant_count="$(jq -er '.items | length' <<<"${tenant_response}")"
printf 'Authenticated tenant list succeeded (%s tenant(s); includes %s).\n' \
  "${tenant_count}" "${EXPECTED_TENANT}"
