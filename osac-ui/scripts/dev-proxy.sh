#!/usr/bin/env bash
# Keep the client secret out of shell traces.
set +x
set -euo pipefail

get_secret_key() {
  local ui_namespace="$1" key="$2" encoded
  encoded="$(oc -n "$ui_namespace" get secret osac-ui-backend-credentials -o "jsonpath={.data.$key}")" || return 1
  [[ -n "$encoded" ]] || return 1
  printf '%s' "$encoded" | base64 --decode
}

load_oc_credentials() {
  local ui_namespace="${OSAC_UI_NAMESPACE:-osac}"
  local issuer="${OIDC_ISSUER_URL:-}"
  local client_id="${OIDC_SERVICE_CLIENT_ID:-}"
  local client_secret="${OIDC_SERVICE_CLIENT_SECRET:-}"

  command -v oc >/dev/null 2>&1 || return 1

  if [[ -z "$issuer" ]]; then
    issuer="$(oc -n "$ui_namespace" get configmap osac-ui -o jsonpath='{.data.OIDC_ISSUER_URL}')" || return 1
  fi
  if [[ -z "$client_id" ]]; then
    client_id="$(get_secret_key "$ui_namespace" client-id)" || return 1
  fi
  if [[ -z "$client_secret" ]]; then
    client_secret="$(get_secret_key "$ui_namespace" client-secret)" || return 1
  fi
  [[ -n "$issuer" && -n "$client_id" && -n "$client_secret" ]] || return 1

  export OIDC_ISSUER_URL="$issuer"
  export OIDC_SERVICE_CLIENT_ID="$client_id"
  export OIDC_SERVICE_CLIENT_SECRET="$client_secret"
  printf 'dev:proxy: loaded OIDC settings from OpenShift project %s\n' "$ui_namespace" >&2
}

if [[ -z "${OIDC_ISSUER_URL:-}" || -z "${OIDC_SERVICE_CLIENT_ID:-}" || -z "${OIDC_SERVICE_CLIENT_SECRET:-}" ]]; then
  if ! load_oc_credentials; then
    printf '%s\n' 'dev:proxy: could not load missing OIDC settings with oc; using the current environment.' >&2
    printf 'dev:proxy: for protected reflection, check oc login and access to the osac-ui ConfigMap and osac-ui-backend-credentials Secret in project %s.\n' "${OSAC_UI_NAMESPACE:-osac}" >&2
  fi
fi

if [[ "${OIDC_ISSUER_URL:-}" == *'.svc.cluster.local'* ]]; then
  printf '%s\n' 'dev:proxy: OIDC_ISSUER_URL uses cluster DNS; this host must reach that issuer for OIDC discovery.' >&2
fi

export BASE_UI_URL="${BASE_UI_URL:-http://localhost:5173}"

cd "$(dirname "${BASH_SOURCE[0]}")/../proxy"
exec nodemon --watch 'proxy/**/*' --exec 'go run' main.go --signal SIGTERM
