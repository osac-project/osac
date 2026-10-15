#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(CDPATH= cd -- "${SCRIPT_DIR}/../.." && pwd)"
# shellcheck source=../../osac-installer/scripts/lib.sh
source "${REPO_ROOT}/osac-installer/scripts/lib.sh"

OSAC_NAMESPACE="${OSAC_NAMESPACE:-osac}"
KEYCLOAK_NAMESPACE="${KEYCLOAK_NAMESPACE:-keycloak}"
ROLLOUT_TIMEOUT="${ROLLOUT_TIMEOUT:-5m}"
KEYCLOAK_ISSUER_URL="${OSAC_KEYCLOAK_ISSUER_URL:-https://keycloak.osac.localhost:8443/realms/osac}"
FULFILLMENT_GRPC_ADDRESS="${OSAC_FULFILLMENT_GRPC_ADDRESS:-fulfillment-api.osac.localhost:8443}"
UI_URL="${OSAC_UI_URL:-http://ui.osac.localhost:8080/}"
ADMIN_CLIENT_ID="${OSAC_ADMIN_CLIENT_ID:-osac-admin}"
EXPECTED_TENANT="${OSAC_EXPECTED_TENANT:-tenant1}"

for command in kubectl curl grpcurl jq base64 timeout; do
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
  "${UI_URL}"; then
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

client_secret_b64="$(kubectl --namespace "${KEYCLOAK_NAMESPACE}" get secret keycloak-client-secrets \
  -o jsonpath='{.data.osac-admin}')"
if [[ -z "${client_secret_b64}" ]]; then
  echo "Keycloak osac-admin client secret is missing." >&2
  exit 1
fi
client_secret="$(printf '%s' "${client_secret_b64}" | base64 --decode)"

printf 'Authenticating with the %s service account through Keycloak ...\n' "${ADMIN_CLIENT_ID}"
access_token="$(get_fulfillment_access_token "${KEYCLOAK_ISSUER_URL}" \
  "${ADMIN_CLIENT_ID}" "${client_secret}" --cacert "${ca_bundle}")"
unset client_secret client_secret_b64

printf 'Requesting tenant list from Fulfillment gRPC API ...\n'
tenant_response="$(OSAC_ACCESS_TOKEN="${access_token}" timeout 30s grpcurl --expand-headers \
  --cacert "${ca_bundle}" -H 'Authorization: Bearer ${OSAC_ACCESS_TOKEN}' \
  -d '{}' "${FULFILLMENT_GRPC_ADDRESS}" osac.public.v1.Tenants/List)"

if ! jq -e --arg expected_tenant "${EXPECTED_TENANT}" \
  '(.items | type == "array") and any(.items[]; .metadata.name == $expected_tenant)' \
  <<<"${tenant_response}" >/dev/null; then
  echo "Authenticated tenant list did not contain the expected '${EXPECTED_TENANT}' tenant." >&2
  exit 1
fi

tenant_count="$(jq -er '.items | length' <<<"${tenant_response}")"
printf 'Authenticated tenant list succeeded (%s tenant(s); includes %s).\n' \
  "${tenant_count}" "${EXPECTED_TENANT}"
