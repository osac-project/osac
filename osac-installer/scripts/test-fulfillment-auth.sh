#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

mkdir -p "${TMP_DIR}/bin"
cat > "${TMP_DIR}/bin/curl" <<'MOCK_CURL'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$@" > "${CURL_ARGS_FILE}"
for arg in "$@"; do
  if [[ "${arg}" == client_secret@* ]]; then
    secret_file="${arg#client_secret@}"
    printf '%s' "${secret_file}" > "${CURL_SECRET_PATH_FILE}"
    cat "${secret_file}" > "${CURL_CAPTURED_SECRET_FILE}"
  fi
done

if [[ "${CURL_EXIT_CODE:-0}" != 0 ]]; then
  exit "${CURL_EXIT_CODE}"
fi
cat "${CURL_RESPONSE_FILE}"
MOCK_CURL
chmod +x "${TMP_DIR}/bin/curl"

cat > "${TMP_DIR}/bin/kubectl" <<'MOCK_KUBECTL'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >> "${KUBECTL_ARGS_FILE}"
case "$*" in
  *"get configmap -l osac.openshift.io/fulfillment-auth-config=true -o name"*)
    case "${MOCK_AUTH_CONFIG_MAP_COUNT:-1}" in
      0) ;;
      1) printf '%s\n' 'configmap/fulfillment-auth' ;;
      2) printf '%s\n' 'configmap/fulfillment-auth' 'configmap/stale-fulfillment-auth' ;;
    esac
    ;;
  *"get configmap/fulfillment-auth -o jsonpath="*)
    printf '%s\t%s\t%s\t%s\t%s' \
      "${MOCK_ISSUER_URL}" "${MOCK_CLIENT_ID_SECRET_NAME}" "${MOCK_CLIENT_ID_SECRET_KEY}" \
      "${MOCK_CLIENT_SECRET_SECRET_NAME}" "${MOCK_CLIENT_SECRET_SECRET_KEY}"
    ;;
  *"get configmap ca-bundle"*)
    printf '%s\n' '-----BEGIN CERTIFICATE-----' 'mock-ca' '-----END CERTIFICATE-----'
    ;;
  *"get secret ${MOCK_CLIENT_ID_SECRET_NAME}"*|*"get secret ${MOCK_CLIENT_SECRET_SECRET_NAME}"*)
    if [[ "$*" == *"${MOCK_CLIENT_ID_SECRET_KEY}"* ]]; then
      printf '%s' "${MOCK_CLIENT_ID_B64}"
    elif [[ "$*" == *"${MOCK_CLIENT_SECRET_SECRET_KEY}"* ]]; then
      printf '%s' "${MOCK_CLIENT_SECRET_B64}"
    fi
    ;;
  *"get secret keycloak-admin-credentials"*)
    printf '%s' 'cGFzc3dvcmQ='
    ;;
  *"port-forward"*)
    printf '%s\n' "$$" >> "${PORT_FORWARD_PIDS_FILE}"
    exec /bin/sleep 300
    ;;
  *"create token admin"*)
    echo "admin service-account token must not be used" >&2
    exit 1
    ;;
esac
MOCK_KUBECTL
chmod +x "${TMP_DIR}/bin/kubectl"

cat > "${TMP_DIR}/bin/sleep" <<'MOCK_SLEEP'
#!/usr/bin/env bash
exit 0
MOCK_SLEEP
chmod +x "${TMP_DIR}/bin/sleep"

export CURL_ARGS_FILE="${TMP_DIR}/curl-args"
export CURL_SECRET_PATH_FILE="${TMP_DIR}/curl-secret-path"
export CURL_CAPTURED_SECRET_FILE="${TMP_DIR}/curl-secret"
export CURL_RESPONSE_FILE="${TMP_DIR}/response.json"
export KUBECTL_ARGS_FILE="${TMP_DIR}/kubectl-args"
export PORT_FORWARD_PIDS_FILE="${TMP_DIR}/port-forward-pids"
: > "${PORT_FORWARD_PIDS_FILE}"
export MOCK_ISSUER_URL="https://keycloak.example.test/realms/osac"
export MOCK_CLIENT_ID_SECRET_NAME="custom-fulfillment-credentials"
export MOCK_CLIENT_ID_SECRET_KEY="custom-client-id"
export MOCK_CLIENT_SECRET_SECRET_NAME="custom-fulfillment-client-secret"
export MOCK_CLIENT_SECRET_SECRET_KEY="custom-client-secret"
export MOCK_CLIENT_ID_B64="$(printf '%s' 'fulfillment-client' | python3 -c 'import base64,sys; print(base64.b64encode(sys.stdin.buffer.read()).decode())')"
export MOCK_CLIENT_SECRET_B64="$(printf '%s' 'secret with & special/chars' | python3 -c 'import base64,sys; print(base64.b64encode(sys.stdin.buffer.read()).decode())')"
export PATH="${TMP_DIR}/bin:${PATH}"

source "${SCRIPT_DIR}/lib.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_eq() {
  local expected="$1" actual="$2" description="$3"
  [[ "${actual}" == "${expected}" ]] || fail "${description}: expected '${expected}', got '${actual}'"
}

assert_not_contains() {
  local needle="$1" file="$2" description="$3"
  if grep -Fq -- "${needle}" "${file}"; then
    fail "${description}: found sensitive value in ${file}"
  fi
}

assert_port_forwards_stopped() {
  local pid
  while IFS= read -r pid; do
    [[ -z "${pid}" ]] && continue
    if kill -0 "${pid}" 2>/dev/null; then
      kill "${pid}" 2>/dev/null || true
      fail "port-forward mock process ${pid} remains after caller exit"
    fi
  done < "${PORT_FORWARD_PIDS_FILE}"
}

issuer_url="https://keycloak.example.test/realms/osac/"
client_id="fulfillment-client"
client_secret='secret with & special/chars'
access_token='test-access-token'

printf '{"access_token":"%s","token_type":"Bearer"}\n' "${access_token}" > "${CURL_RESPONSE_FILE}"
token="$(get_fulfillment_access_token "${issuer_url}" "${client_id}" "${client_secret}" \
  --cacert "${TMP_DIR}/ca.pem")"
assert_eq "${access_token}" "${token}" "token exchange returns the access token"
grep -Fxq 'https://keycloak.example.test/realms/osac/protocol/openid-connect/token' "${CURL_ARGS_FILE}" \
  || fail "token exchange uses the realm token endpoint"
grep -Fxq 'grant_type=client_credentials' "${CURL_ARGS_FILE}" \
  || fail "token exchange requests the client-credentials grant"
grep -Fxq "client_id=${client_id}" "${CURL_ARGS_FILE}" \
  || fail "token exchange supplies the client id"
grep -Fxq -- '--data-urlencode' "${CURL_ARGS_FILE}" \
  || fail "token exchange URL-encodes its form fields"
grep -Fxq -- '--cacert' "${CURL_ARGS_FILE}" \
  || fail "token exchange preserves caller TLS options"
grep -Fxq "${TMP_DIR}/ca.pem" "${CURL_ARGS_FILE}" \
  || fail "token exchange passes through the configured CA file"
assert_eq "${client_secret}" "$(cat "${CURL_CAPTURED_SECRET_FILE}")" "curl reads the secret from a temporary file"
assert_not_contains "${client_secret}" "${CURL_ARGS_FILE}" "curl arguments"
secret_file="$(cat "${CURL_SECRET_PATH_FILE}")"
[[ ! -e "${secret_file}" ]] || fail "temporary client secret file is removed after the request"

rm -f "${CURL_ARGS_FILE}"
if get_fulfillment_access_token "http://keycloak.example.test/realms/osac" "${client_id}" "${client_secret}" \
  >"${TMP_DIR}/insecure-stdout" 2>"${TMP_DIR}/insecure-stderr"; then
  fail "non-HTTPS issuer URL is rejected"
fi
[[ ! -e "${CURL_ARGS_FILE}" ]] || fail "non-HTTPS issuer URL does not call curl"
assert_not_contains "${client_secret}" "${TMP_DIR}/insecure-stderr" "non-HTTPS issuer diagnostics"

if get_fulfillment_access_token "${issuer_url}" "" "${client_secret}" \
  >"${TMP_DIR}/missing-stdout" 2>"${TMP_DIR}/missing-stderr"; then
  fail "missing client id is rejected"
fi
[[ ! -e "${CURL_ARGS_FILE}" ]] || fail "missing credentials do not call curl"
assert_not_contains "${client_secret}" "${TMP_DIR}/missing-stderr" "missing-credential diagnostics"

printf '{"access_token":"%s","token_type":"Bearer"}\n' "${access_token}" > "${CURL_RESPONSE_FILE}"
rm -f "${KUBECTL_ARGS_FILE}"
touch "${TMP_DIR}/ca.pem"
host_token="$(get_fulfillment_service_account_token osac --cacert "${TMP_DIR}/ca.pem")"
assert_eq "${access_token}" "${host_token}" "host credential resolver returns the service-account access token"
grep -Fq 'get configmap -l osac.openshift.io/fulfillment-auth-config=true' "${KUBECTL_ARGS_FILE}" \
  || fail "host credential resolver finds the configured issuer and credentials by stable label"
grep -Fq 'OSAC_FULFILLMENT_CLIENT_ID_SECRET_NAME' "${KUBECTL_ARGS_FILE}" \
  || fail "host credential resolver reads the configured client-id Secret reference"
grep -Fq 'OSAC_FULFILLMENT_CLIENT_SECRET_SECRET_KEY' "${KUBECTL_ARGS_FILE}" \
  || fail "host credential resolver reads the configured client-secret Secret key"
grep -Fq "get secret ${MOCK_CLIENT_ID_SECRET_NAME}" "${KUBECTL_ARGS_FILE}" \
  || fail "host credential resolver reads the configured credential Secret"
grep -Fq ".data['${MOCK_CLIENT_ID_SECRET_KEY}']" "${KUBECTL_ARGS_FILE}" \
  || fail "host credential resolver reads the configured client-id key"
grep -Fq ".data['${MOCK_CLIENT_SECRET_SECRET_KEY}']" "${KUBECTL_ARGS_FILE}" \
  || fail "host credential resolver reads the configured client-secret key"
grep -Fxq -- '--cacert' "${CURL_ARGS_FILE}" \
  || fail "host credential resolver preserves the caller TLS option"
! grep -Fxq -- '--insecure' "${CURL_ARGS_FILE}" \
  || fail "host credential resolver verifies TLS for the Keycloak exchange"
assert_not_contains "${client_secret}" "${KUBECTL_ARGS_FILE}" "kubectl arguments"
assert_not_contains "${client_secret}" "${CURL_ARGS_FILE}" "host token request arguments"

export MOCK_AUTH_CONFIG_MAP_COUNT=2
rm -f "${CURL_ARGS_FILE}"
if get_fulfillment_service_account_token osac --cacert "${TMP_DIR}/ca.pem" \
  >"${TMP_DIR}/duplicate-configmap-stdout" 2>"${TMP_DIR}/duplicate-configmap-stderr"; then
  fail "multiple Fulfillment auth ConfigMaps are rejected"
fi
grep -Fq 'expected exactly one Fulfillment auth ConfigMap, found 2' "${TMP_DIR}/duplicate-configmap-stderr" \
  || fail "duplicate Fulfillment auth ConfigMaps produce a clear error"
[[ ! -e "${CURL_ARGS_FILE}" ]] || fail "duplicate Fulfillment auth ConfigMaps do not call curl"
unset MOCK_AUTH_CONFIG_MAP_COUNT

export CURL_EXIT_CODE=22
rm -f "${CURL_SECRET_PATH_FILE}"
if get_fulfillment_access_token "${issuer_url}" "${client_id}" "${client_secret}" \
  >"${TMP_DIR}/http-stdout" 2>"${TMP_DIR}/http-stderr"; then
  fail "HTTP failure is rejected"
fi
[[ ! -s "${TMP_DIR}/http-stdout" ]] || fail "HTTP failure does not print a token"
assert_not_contains "${client_secret}" "${TMP_DIR}/http-stderr" "HTTP failure diagnostics"
assert_not_contains "${access_token}" "${TMP_DIR}/http-stderr" "HTTP failure diagnostics"
[[ -s "${CURL_SECRET_PATH_FILE}" ]] || fail "HTTP failure curl call records its temporary secret file"
secret_file="$(cat "${CURL_SECRET_PATH_FILE}")"
[[ ! -e "${secret_file}" ]] || fail "temporary client secret file is removed after HTTP failure"
unset CURL_EXIT_CODE

printf '{not-json}\n' > "${CURL_RESPONSE_FILE}"
if get_fulfillment_access_token "${issuer_url}" "${client_id}" "${client_secret}" \
  >"${TMP_DIR}/invalid-json-stdout" 2>"${TMP_DIR}/invalid-json-stderr"; then
  fail "malformed token response is rejected"
fi
[[ ! -s "${TMP_DIR}/invalid-json-stdout" ]] || fail "malformed response does not print a token"
assert_not_contains "${client_secret}" "${TMP_DIR}/invalid-json-stderr" "malformed-response diagnostics"

printf '{"token_type":"Bearer"}\n' > "${CURL_RESPONSE_FILE}"
if get_fulfillment_access_token "${issuer_url}" "${client_id}" "${client_secret}" \
  >"${TMP_DIR}/missing-token-stdout" 2>"${TMP_DIR}/missing-token-stderr"; then
  fail "response without an access token is rejected"
fi
[[ ! -s "${TMP_DIR}/missing-token-stdout" ]] || fail "response without a token prints nothing"
assert_not_contains "${client_secret}" "${TMP_DIR}/missing-token-stderr" "missing-token diagnostics"

DEVSTACK_AUTH_HELPER="${SCRIPT_DIR}/../charts/osac-devstack/files/fulfillment-auth.sh"
source "${DEVSTACK_AUTH_HELPER}"
FULFILLMENT_ISSUER_URL="${issuer_url}"
FULFILLMENT_CLIENT_ID="${client_id}"
FULFILLMENT_CLIENT_SECRET_FILE="${TMP_DIR}/devstack-client-secret"
CA_FILE="${TMP_DIR}/devstack-ca.pem"
printf '%s' "${client_secret}" > "${FULFILLMENT_CLIENT_SECRET_FILE}"
touch "${CA_FILE}"
printf '{"access_token":"%s","token_type":"Bearer"}\n' "${access_token}" > "${CURL_RESPONSE_FILE}"
export FULFILLMENT_ISSUER_URL FULFILLMENT_CLIENT_ID FULFILLMENT_CLIENT_SECRET_FILE CA_FILE

devstack_token="$(get_fulfillment_access_token)"
assert_eq "${access_token}" "${devstack_token}" "devstack helper returns the access token"
grep -Fxq 'https://keycloak.example.test/realms/osac/protocol/openid-connect/token' "${CURL_ARGS_FILE}" \
  || fail "devstack token exchange uses the realm token endpoint"
grep -Fxq -- "--cacert" "${CURL_ARGS_FILE}" \
  || fail "devstack token exchange verifies the issuer with the CA bundle"
grep -Fxq -- "${CA_FILE}" "${CURL_ARGS_FILE}" \
  || fail "devstack token exchange uses the mounted CA bundle"
assert_eq "${client_secret}" "$(cat "${CURL_CAPTURED_SECRET_FILE}")" "devstack curl reads the mounted secret file"
assert_not_contains "${client_secret}" "${CURL_ARGS_FILE}" "devstack curl arguments"

rm -f "${CURL_ARGS_FILE}"
FULFILLMENT_ISSUER_URL="http://keycloak.example.test/realms/osac"
if get_fulfillment_access_token >"${TMP_DIR}/devstack-insecure-stdout" 2>"${TMP_DIR}/devstack-insecure-stderr"; then
  fail "devstack helper rejects a non-HTTPS issuer URL"
fi
[[ ! -e "${CURL_ARGS_FILE}" ]] || fail "devstack helper rejects insecure issuer before calling curl"
assert_not_contains "${client_secret}" "${TMP_DIR}/devstack-insecure-stderr" "devstack insecure-issuer diagnostics"

CALLER_BIN="${TMP_DIR}/caller-bin"
mkdir -p "${CALLER_BIN}"
export CALLER_KUBECTL_ARGS_FILE="${TMP_DIR}/caller-kubectl-args"
export CALLER_CURL_ARGS_FILE="${TMP_DIR}/caller-curl-args"
export CALLER_HTTP_HEADERS_FILE="${TMP_DIR}/caller-http-headers"
export CALLER_HEADER_FILE_PATHS_FILE="${TMP_DIR}/caller-header-file-paths"
export CALLER_TOKEN_SECRET_INPUT_FILE="${TMP_DIR}/caller-token-secret-input"
export CALLER_POD_MANIFEST_FILE="${TMP_DIR}/caller-pod-manifest"
export CALLER_ACCESS_TOKEN='host-test-access-token'

cat > "${CALLER_BIN}/kubectl" <<'MOCK_CALLER_KUBECTL'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >> "${CALLER_KUBECTL_ARGS_FILE}"
case "$*" in
  *"port-forward"*)
    printf '%s\n' "$$" >> "${PORT_FORWARD_PIDS_FILE}"
    exec /bin/sleep 300
    ;;
  *"get configmap -l osac.openshift.io/fulfillment-auth-config=true -o name"*)
    printf '%s\n' 'configmap/fulfillment-auth'
    ;;
  *"get configmap/fulfillment-auth -o jsonpath="*)
    printf '%s\t%s\t%s\t%s\t%s' \
      "${MOCK_ISSUER_URL}" "${MOCK_CLIENT_ID_SECRET_NAME}" "${MOCK_CLIENT_ID_SECRET_KEY}" \
      "${MOCK_CLIENT_SECRET_SECRET_NAME}" "${MOCK_CLIENT_SECRET_SECRET_KEY}"
    ;;
  *"get configmap ca-bundle"*)
    printf '%s\n' '-----BEGIN CERTIFICATE-----' 'mock-ca' '-----END CERTIFICATE-----'
    ;;
  *"get secret ${MOCK_CLIENT_ID_SECRET_NAME}"*|*"get secret ${MOCK_CLIENT_SECRET_SECRET_NAME}"*)
    if [[ "$*" == *"${MOCK_CLIENT_ID_SECRET_KEY}"* ]]; then
      printf '%s' "${MOCK_CLIENT_ID_B64}"
    elif [[ "$*" == *"${MOCK_CLIENT_SECRET_SECRET_KEY}"* ]]; then
      printf '%s' "${MOCK_CLIENT_SECRET_B64}"
    fi
    ;;
  *"get secret keycloak-admin-credentials"*)
    printf '%s' 'cGFzc3dvcmQ='
    ;;
  *"create secret generic"*)
    cat > "${CALLER_TOKEN_SECRET_INPUT_FILE}"
    printf 'apiVersion: v1\nkind: Secret\nmetadata:\n  name: mocked-token\n'
    ;;
  *"apply -f -"*)
    input="$(cat)"
    if [[ "${input}" == *'"kind": "Pod"'* ]]; then
      printf '%s\n' "${input}" > "${CALLER_POD_MANIFEST_FILE}"
    fi
    ;;
  *"get pod"*)
    printf '%s' "${CALLER_POD_PHASE:-Succeeded}"
    ;;
  *"logs"*)
    printf '%s' "${CALLER_POD_LOGS:-tenant created}"
    ;;
  *"get subnets"*)
    printf '{"items":[]}'
    ;;
  *"create token admin"*)
    echo "admin service-account token must not be used" >&2
    exit 1
    ;;
esac
MOCK_CALLER_KUBECTL
chmod +x "${CALLER_BIN}/kubectl"

cat > "${CALLER_BIN}/curl" <<'MOCK_CALLER_CURL'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$@" >> "${CALLER_CURL_ARGS_FILE}"
args="$*"
while (($#)); do
  if [[ "$1" == "--header" || "$1" == "-H" ]]; then
    shift
    if [[ "${1:-}" == @* ]]; then
      printf '%s\n' "${1#@}" >> "${CALLER_HEADER_FILE_PATHS_FILE}"
      cat "${1#@}" >> "${CALLER_HTTP_HEADERS_FILE}"
    fi
  fi
  shift || true
done

if [[ "${args}" == *"/realms/master/protocol/openid-connect/token"* ]]; then
  printf '{"access_token":"mock-keycloak-admin-token","token_type":"Bearer"}\n'
elif [[ "${args}" == *"/protocol/openid-connect/token"* ]]; then
  printf '{"access_token":"%s","token_type":"Bearer"}\n' "${CALLER_ACCESS_TOKEN}"
elif [[ "${CALLER_API_CURL_FAIL:-false}" == true && "${args}" == *"/api/private/v1/"* ]]; then
  exit 22
elif [[ "${args}" == *"-w %{http_code}"* ]]; then
  printf '201'
elif [[ "${args}" == *"/realms/master/.well-known/openid-configuration"* ]]; then
  printf '{}'
elif [[ "${args}" == *"/organizations?exact=true"* ]]; then
  printf '[{"id":"mock-org","name":"tenant1"}]'
elif [[ "${args}" == *"/users?username="* ]]; then
  printf '[{"id":"mock-user"}]'
elif [[ "${args}" == *"/admin/realms/"* ]]; then
  printf '[{"id":"mock-org","name":"tenant1"}]'
else
  printf '{"id":"mock-object"}'
fi
MOCK_CALLER_CURL
chmod +x "${CALLER_BIN}/curl"

rm -f "${CALLER_KUBECTL_ARGS_FILE}" "${CALLER_CURL_ARGS_FILE}" "${CALLER_HTTP_HEADERS_FILE}" "${CALLER_HEADER_FILE_PATHS_FILE}"
: > "${PORT_FORWARD_PIDS_FILE}"
unset CA_FILE
PATH="${CALLER_BIN}:${TMP_DIR}/bin:${PATH}" \
  "${SCRIPT_DIR}/dev-full/seed-catalog.sh" osac \
  >"${TMP_DIR}/seed-catalog-stdout" 2>"${TMP_DIR}/seed-catalog-stderr"
assert_port_forwards_stopped
grep -Fq "Authorization: Bearer ${CALLER_ACCESS_TOKEN}" "${CALLER_HTTP_HEADERS_FILE}" \
  || fail "host catalog REST calls use the Keycloak service-account bearer"
grep -Fq 'get configmap -l osac.openshift.io/fulfillment-auth-config=true' "${CALLER_KUBECTL_ARGS_FILE}" \
  || fail "host catalog resolves the configured issuer from the labeled Fulfillment ConfigMap"
grep -Fq '.data.OSAC_FULFILLMENT_ISSUER_URL' "${CALLER_KUBECTL_ARGS_FILE}" \
  || fail "host catalog reads the configured issuer key"
grep -Fq "get secret ${MOCK_CLIENT_ID_SECRET_NAME}" "${CALLER_KUBECTL_ARGS_FILE}" \
  || fail "host catalog reads the configured client-id Secret"
grep -Fq ".data['${MOCK_CLIENT_ID_SECRET_KEY}']" "${CALLER_KUBECTL_ARGS_FILE}" \
  || fail "host catalog reads the configured client-id Secret key"
grep -Fq "get secret ${MOCK_CLIENT_SECRET_SECRET_NAME}" "${CALLER_KUBECTL_ARGS_FILE}" \
  || fail "host catalog reads the configured client-secret Secret"
grep -Fq ".data['${MOCK_CLIENT_SECRET_SECRET_KEY}']" "${CALLER_KUBECTL_ARGS_FILE}" \
  || fail "host catalog reads the configured client-secret Secret key"
grep -Fq -- '--cacert' "${CALLER_CURL_ARGS_FILE}" \
  || fail "host catalog token exchange verifies the Keycloak issuer"
! grep -Fq -- '--insecure' "${CALLER_CURL_ARGS_FILE}" \
  || fail "host catalog token exchange does not disable TLS verification"
grep -Fq -- '-skS' "${CALLER_CURL_ARGS_FILE}" \
  || fail "host catalog REST calls preserve their existing TLS option"
grep -Fq 'port-forward svc/fulfillment-internal-api' "${CALLER_KUBECTL_ARGS_FILE}" \
  || fail "host catalog retains its Kubernetes port-forward operation"
! grep -Fq 'create token admin' "${CALLER_KUBECTL_ARGS_FILE}" \
  || fail "host catalog does not mint an admin Kubernetes token"
assert_not_contains "${CALLER_ACCESS_TOKEN}" "${CALLER_CURL_ARGS_FILE}" "host catalog curl arguments"
assert_not_contains "${CALLER_ACCESS_TOKEN}" "${TMP_DIR}/seed-catalog-stdout" "host catalog output"
assert_not_contains "${client_secret}" "${TMP_DIR}/seed-catalog-stdout" "host catalog output"
assert_not_contains "${client_secret}" "${CALLER_CURL_ARGS_FILE}" "host catalog curl arguments"
while IFS= read -r header_file; do
  [[ ! -e "${header_file}" ]] || fail "host catalog removes its temporary Authorization header file"
done < "${CALLER_HEADER_FILE_PATHS_FILE}"

export CALLER_API_CURL_FAIL=true
rm -f "${CALLER_KUBECTL_ARGS_FILE}" "${CALLER_CURL_ARGS_FILE}" "${CALLER_HTTP_HEADERS_FILE}" "${CALLER_HEADER_FILE_PATHS_FILE}"
: > "${PORT_FORWARD_PIDS_FILE}"
if PATH="${CALLER_BIN}:${TMP_DIR}/bin:${PATH}" \
  "${SCRIPT_DIR}/dev-full/seed-catalog.sh" osac \
  >"${TMP_DIR}/seed-catalog-failure-stdout" 2>"${TMP_DIR}/seed-catalog-failure-stderr"; then
  fail "host catalog reports a failed Fulfillment REST request"
fi
assert_port_forwards_stopped
while IFS= read -r header_file; do
  [[ ! -e "${header_file}" ]] || fail "host catalog removes its temporary Authorization header file on failure"
done < "${CALLER_HEADER_FILE_PATHS_FILE}"
assert_not_contains "${CALLER_ACCESS_TOKEN}" "${TMP_DIR}/seed-catalog-failure-stdout" "host catalog failure output"
assert_not_contains "${CALLER_ACCESS_TOKEN}" "${TMP_DIR}/seed-catalog-failure-stderr" "host catalog failure diagnostics"
unset CALLER_API_CURL_FAIL

rm -f "${CALLER_KUBECTL_ARGS_FILE}" "${CALLER_CURL_ARGS_FILE}" "${CALLER_TOKEN_SECRET_INPUT_FILE}" "${CALLER_POD_MANIFEST_FILE}"
: > "${PORT_FORWARD_PIDS_FILE}"
PATH="${CALLER_BIN}:${TMP_DIR}/bin:${PATH}" \
  "${SCRIPT_DIR}/dev-full/provision-tenant.sh" osac \
  >"${TMP_DIR}/provision-tenant-stdout" 2>"${TMP_DIR}/provision-tenant-stderr"
assert_port_forwards_stopped
assert_eq "${CALLER_ACCESS_TOKEN}" "$(cat "${CALLER_TOKEN_SECRET_INPUT_FILE}")" \
  "host tenant provisioning stages its Fulfillment token through stdin"
grep -Fq '"-expand-headers"' "${CALLER_POD_MANIFEST_FILE}" \
  || fail "host tenant grpcurl expands its Secret-backed token environment variable"
grep -Fq 'authorization: Bearer ${FULFILLMENT_TOKEN}' "${CALLER_POD_MANIFEST_FILE}" \
  || fail "host tenant grpcurl keeps the token out of the Pod command arguments"
grep -Fq '"automountServiceAccountToken": false' "${CALLER_POD_MANIFEST_FILE}" \
  || fail "host tenant grpcurl Pod does not mount an unused Kubernetes token"
grep -Fq 'delete secret osac-provision-tenant-token-' "${CALLER_KUBECTL_ARGS_FILE}" \
  || fail "host tenant provisioning cleans up its temporary token Secret"
grep -Fq '.data.OSAC_FULFILLMENT_ISSUER_URL' "${CALLER_KUBECTL_ARGS_FILE}" \
  || fail "host tenant resolves the configured issuer"
grep -Fq "get secret ${MOCK_CLIENT_ID_SECRET_NAME}" "${CALLER_KUBECTL_ARGS_FILE}" \
  || fail "host tenant reads the configured client-id Secret"
grep -Fq ".data['${MOCK_CLIENT_ID_SECRET_KEY}']" "${CALLER_KUBECTL_ARGS_FILE}" \
  || fail "host tenant reads the configured client-id Secret key"
grep -Fq "get secret ${MOCK_CLIENT_SECRET_SECRET_NAME}" "${CALLER_KUBECTL_ARGS_FILE}" \
  || fail "host tenant reads the configured client-secret Secret"
grep -Fq ".data['${MOCK_CLIENT_SECRET_SECRET_KEY}']" "${CALLER_KUBECTL_ARGS_FILE}" \
  || fail "host tenant reads the configured client-secret Secret key"
grep -Fq -- '--cacert' "${CALLER_CURL_ARGS_FILE}" \
  || fail "host tenant token exchange verifies the Keycloak issuer"
! grep -Fq -- '--insecure' "${CALLER_CURL_ARGS_FILE}" \
  || fail "host tenant token exchange does not disable TLS verification"
grep -Fq -- '"-insecure"' "${CALLER_POD_MANIFEST_FILE}" \
  || fail "host tenant grpcurl preserves its existing TLS option"
! grep -Fq 'create token admin' "${CALLER_KUBECTL_ARGS_FILE}" \
  || fail "host tenant provisioning does not mint an admin Kubernetes token"
assert_not_contains "${CALLER_ACCESS_TOKEN}" "${CALLER_POD_MANIFEST_FILE}" "host tenant Pod manifest"
assert_not_contains "${CALLER_ACCESS_TOKEN}" "${TMP_DIR}/provision-tenant-stdout" "host tenant output"
assert_not_contains "${CALLER_ACCESS_TOKEN}" "${CALLER_CURL_ARGS_FILE}" "host tenant curl arguments"
assert_not_contains "${client_secret}" "${CALLER_CURL_ARGS_FILE}" "host tenant curl arguments"

export CALLER_POD_PHASE='Failed'
export CALLER_POD_LOGS='mock grpcurl failure'
rm -f "${CALLER_KUBECTL_ARGS_FILE}" "${CALLER_TOKEN_SECRET_INPUT_FILE}" "${CALLER_POD_MANIFEST_FILE}"
: > "${PORT_FORWARD_PIDS_FILE}"
if PATH="${CALLER_BIN}:${TMP_DIR}/bin:${PATH}" \
  "${SCRIPT_DIR}/dev-full/provision-tenant.sh" osac \
  >"${TMP_DIR}/provision-tenant-failure-stdout" 2>"${TMP_DIR}/provision-tenant-failure-stderr"; then
  fail "host tenant provisioning reports a failed grpcurl Pod"
fi
assert_port_forwards_stopped
grep -Fq 'delete secret osac-provision-tenant-token-' "${CALLER_KUBECTL_ARGS_FILE}" \
  || fail "host tenant provisioning cleans up its temporary Secret on failure"
assert_not_contains "${CALLER_ACCESS_TOKEN}" "${TMP_DIR}/provision-tenant-failure-stdout" "host tenant failure output"
assert_not_contains "${CALLER_ACCESS_TOKEN}" "${TMP_DIR}/provision-tenant-failure-stderr" "host tenant failure diagnostics"

echo "Fulfillment client-credentials helper tests passed."
