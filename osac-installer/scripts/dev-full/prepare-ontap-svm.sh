#!/usr/bin/env bash
# dev-full / local testing helper: create or update one or more NetApp ONTAP
# tenant preparation Secrets (osac.openshift.io/secret-type=storage-tenant-preparation)
# on the hub cluster for osac-operator to discover and inject into AAP jobs.
#
# Supports both single-tenant and batch (--map) modes, auto-resolves the
# immutable osac.openshift.io/tenant-id label from the hub Tenant CR when
# --tenant-id is omitted, and optionally resolves --backend <name> to its
# Fulfillment StorageBackend ID via the internal REST API.
#
# Credentials are read from ONTAP_SVM_PASSWORD, stdin (--password-stdin),
# an interactive prompt, or generated ephemerally (--generate-password for
# local mock testing) and piped to `kubectl apply -f -` via stdin so passwords
# never appear in process arguments or on disk.
#
# Examples:
#   # Single tenant with auto-resolved tenant-id and generated test password:
#   ./prepare-ontap-svm.sh \
#     --tenant tenant1 \
#     --backend ontap-primary \
#     --backend-id ontap-be-1 \
#     --svm svm_tenant1 \
#     --username trident_tenant1 \
#     --generate-password
#
#   # Batch-prepare multiple tenant SVMs on the same backend:
#   ./prepare-ontap-svm.sh \
#     --backend ontap-primary \
#     --backend-id ontap-be-1 \
#     --map "tenant1:svm_tenant1:trident_t1" \
#     --map "tenant2:svm_tenant2:trident_t2" \
#     --generate-password

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

NS="${NS:-osac}"
TENANT_NS="${OSAC_TENANT_NAMESPACE:-osac-system}"
PREP_NS="${OSAC_STORAGE_PREPARATION_NAMESPACE:-osac-storage}"
INTERNAL_SVC="${INTERNAL_SVC:-fulfillment-internal-api}"
INTERNAL_PORT="${INTERNAL_PORT:-8001}"
LOCAL_PORT="${LOCAL_PORT:-18001}"

TENANT_NAME=""
TENANT_ID=""
BACKEND_ID=""
BACKEND_NAME=""
SVM_NAME=""
SVM_USER=""
SECRET_NAME=""
GENERATE_PASSWORD="false"
PASSWORD_STDIN="false"
declare -a MAPPINGS=()

# Print command-line usage and supported options to stdout.
usage() {
  cat <<'EOF'
Usage:
  prepare-ontap-svm.sh [options]

Options:
  --tenant <name>             Tenant CR name on the hub cluster
  --tenant-id <id>            Immutable OSAC tenant ID (auto-resolved from Tenant CR if omitted)
  --backend-id <id>           Fulfillment StorageBackend ID
  --backend <name>            Fulfillment StorageBackend name (resolved via internal API if --backend-id omitted)
  --svm <name>                Prepared ONTAP SVM name
  --username <user>           ONTAP runtime username for Trident
  --secret-name <name>        Preparation Secret name (default: ontap-prep-<tenant>-<backend-id>)
  --map <spec>                Batch entry: "<tenant>:<svm>:<username>[:<tenant_id>[:<secret_name>]]" (repeatable)
  --prep-namespace <ns>       Preparation Secret namespace (default: $OSAC_STORAGE_PREPARATION_NAMESPACE or osac-storage)
  --tenant-namespace <ns>     Hub Tenant CR namespace (default: $OSAC_TENANT_NAMESPACE or osac-system)
  --osac-namespace <ns>       OSAC control-plane namespace for API lookup (default: $NS or osac)
  --generate-password         Generate an ephemeral random password via openssl (for local/mock testing)
  --password-stdin            Read the SVM runtime password from stdin
  -h, --help                  Show this help message
EOF
}

# Print an informational status line to stdout.
log() { echo "[+] $*"; }

# Print a warning or error diagnostic line to stderr.
warn() { echo "[!] $*" >&2; }

while [[ $# -gt 0 ]]; do
  case "$1" in
  --tenant)
    TENANT_NAME="${2:?--tenant requires a value}"
    shift 2
    ;;
  --tenant-id)
    TENANT_ID="${2:?--tenant-id requires a value}"
    shift 2
    ;;
  --backend-id)
    BACKEND_ID="${2:?--backend-id requires a value}"
    shift 2
    ;;
  --backend)
    BACKEND_NAME="${2:?--backend requires a value}"
    shift 2
    ;;
  --svm)
    SVM_NAME="${2:?--svm requires a value}"
    shift 2
    ;;
  --username)
    SVM_USER="${2:?--username requires a value}"
    shift 2
    ;;
  --secret-name)
    SECRET_NAME="${2:?--secret-name requires a value}"
    shift 2
    ;;
  --map)
    MAPPINGS+=("${2:?--map requires a value}")
    shift 2
    ;;
  --prep-namespace)
    PREP_NS="${2:?--prep-namespace requires a value}"
    shift 2
    ;;
  --tenant-namespace)
    TENANT_NS="${2:?--tenant-namespace requires a value}"
    shift 2
    ;;
  --osac-namespace)
    NS="${2:?--osac-namespace requires a value}"
    shift 2
    ;;
  --generate-password)
    GENERATE_PASSWORD="true"
    shift
    ;;
  --password-stdin)
    PASSWORD_STDIN="true"
    shift
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  *)
    warn "Unrecognized command-line option."
    usage >&2
    exit 1
    ;;
  esac
done

if [[ ${#MAPPINGS[@]} -eq 0 ]]; then
  if [[ -z "${TENANT_NAME}" || -z "${SVM_NAME}" || -z "${SVM_USER}" ]]; then
    warn "Specify either --map <tenant>:<svm>:<username> or all of --tenant, --svm, and --username."
    usage >&2
    exit 1
  fi
  MAPPINGS+=("${TENANT_NAME}:${SVM_NAME}:${SVM_USER}:${TENANT_ID}:${SECRET_NAME}")
fi

# Fetch the StorageBackend list JSON from the Fulfillment internal REST API.
fetch_storage_backends_json() {
  local pf_pid=""
  local temp_ca=""
  local token_header=""
  local svc_host="${INTERNAL_SVC}.${NS}.svc.cluster.local"

  # shellcheck source=/dev/null
  source "${SCRIPT_DIR}/../lib.sh"

  temp_ca="$(mktemp)"
  token_header="$(mktemp)"
  chmod 600 "${temp_ca}" "${token_header}"

  kubectl -n "${NS}" get configmap ca-bundle -o jsonpath='{.data.bundle\.pem}' >"${temp_ca}" 2>/dev/null || true
  if [[ ! -s "${temp_ca}" ]]; then
    rm -f "${temp_ca}" "${token_header}"
    warn "ca-bundle ConfigMap has no bundle.pem; pass --backend and --backend-id directly."
    return 0
  fi

  local token=""
  if ! token="$(get_fulfillment_service_account_token "${NS}" --cacert "${temp_ca}" 2>/dev/null)" || [[ -z "${token}" ]]; then
    rm -f "${temp_ca}" "${token_header}"
    warn "Fulfillment authentication failed; pass --backend and --backend-id directly."
    return 0
  fi
  printf 'Authorization: Bearer %s\n' "${token}" >"${token_header}"
  unset token

  kubectl -n "${NS}" port-forward "svc/${INTERNAL_SVC}" "${LOCAL_PORT}:${INTERNAL_PORT}" >/dev/null 2>&1 &
  pf_pid=$!
  sleep 2

  local resp=""
  resp="$(curl -s --cacert "${temp_ca}" \
    --resolve "${svc_host}:${LOCAL_PORT}:127.0.0.1" \
    --header "@${token_header}" -H "Content-Type: application/json" \
    "https://${svc_host}:${LOCAL_PORT}/api/private/v1/storage_backends" 2>/dev/null || true)"

  kill "${pf_pid}" 2>/dev/null || true
  wait "${pf_pid}" 2>/dev/null || true
  rm -f "${temp_ca}" "${token_header}"

  printf '%s' "${resp}"
}

# Resolve a StorageBackend name to its Fulfillment ID via the internal REST API.
resolve_backend_id_by_name() {
  local backend_name="$1"
  local resp=""
  resp="$(fetch_storage_backends_json)"
  printf '%s' "${resp}" | jq -r --arg name "${backend_name}" \
    '.items[]? | select(.metadata.name == $name) | .id' | head -n 1
}

# Resolve a StorageBackend ID to its Fulfillment name via the internal REST API.
resolve_backend_name_by_id() {
  local backend_id="$1"
  local resp=""
  resp="$(fetch_storage_backends_json)"
  printf '%s' "${resp}" | jq -r --arg id "${backend_id}" \
    '.items[]? | select(.id == $id) | .metadata.name' | head -n 1
}

if [[ -z "${BACKEND_ID}" && -n "${BACKEND_NAME}" ]]; then
  log "Resolving StorageBackend ID via Fulfillment internal API..."
  BACKEND_ID="$(resolve_backend_id_by_name "${BACKEND_NAME}" 2>/dev/null)"
  BACKEND_ID="${BACKEND_ID:-${BACKEND_NAME}}"
  log "Resolved StorageBackend ID."
elif [[ -n "${BACKEND_ID}" && -z "${BACKEND_NAME}" ]]; then
  log "Resolving StorageBackend name via Fulfillment internal API..."
  BACKEND_NAME="$(resolve_backend_name_by_id "${BACKEND_ID}")"
  if [[ -z "${BACKEND_NAME}" ]]; then
    warn "Could not resolve StorageBackend name for ID '${BACKEND_ID}'; pass --backend <name> explicitly."
    exit 1
  fi
  log "Resolved StorageBackend name."
fi

if [[ -z "${BACKEND_ID}" || -z "${BACKEND_NAME}" ]]; then
  warn "--backend <name> (and optionally --backend-id <id>) is required."
  usage >&2
  exit 1
fi

# Resolve the immutable osac.openshift.io/tenant-id label from a hub Tenant CR,
# falling back to the Tenant CR name when the label is absent.
resolve_tenant_id() {
  local t_name="$1"
  local tid=""
  if ! kubectl -n "${TENANT_NS}" get tenant.osac.openshift.io "${t_name}" >/dev/null 2>&1; then
    warn "Target Tenant CR '${t_name}' not found in namespace '${TENANT_NS}'."
    warn "Create the Tenant first or pass --tenant-id explicitly."
    return 1
  fi
  tid="$(kubectl -n "${TENANT_NS}" get tenant.osac.openshift.io "${t_name}" \
    -o jsonpath='{.metadata.labels.osac\.openshift\.io/tenant-id}' 2>/dev/null || true)"
  tid="${tid:-${t_name}}"
  printf '%s' "${tid}"
}

STDIN_PASSWORD=""
if [[ "${PASSWORD_STDIN}" == "true" ]]; then
  IFS= read -r STDIN_PASSWORD
  if [[ -z "${STDIN_PASSWORD}" ]]; then
    warn "No password received on stdin."
    exit 1
  fi
fi

# Ensure preparation namespace exists on the hub cluster.
if ! kubectl get namespace "${PREP_NS}" >/dev/null 2>&1; then
  log "Creating preparation namespace..."
  kubectl create namespace "${PREP_NS}" >/dev/null
fi

prepared_count=0
for entry in "${MAPPINGS[@]}"; do
  IFS=':' read -r t_name svm_name svm_user explicit_tid explicit_sec <<<"${entry}"
  if [[ -z "${t_name}" || -z "${svm_name}" || -z "${svm_user}" ]]; then
    warn "Invalid --map entry. Expected '<tenant>:<svm>:<username>[:<tenant_id>[:<secret_name>]]'."
    exit 1
  fi

  resolved_tid="${explicit_tid:-}"
  if [[ -z "${resolved_tid}" ]]; then
    resolved_tid="$(resolve_tenant_id "${t_name}")"
  fi

  sec_name="${explicit_sec:-}"
  if [[ -z "${sec_name}" ]]; then
    safe_backend="$(printf '%s' "${BACKEND_ID}" | tr '[:upper:]_' '[:lower:]-' | tr -cd 'a-z0-9-')"
    sec_name="ontap-prep-${t_name}-${safe_backend}"
  fi

  svm_password=""
  if [[ -n "${ONTAP_SVM_PASSWORD:-}" ]]; then
    svm_password="${ONTAP_SVM_PASSWORD}"
  elif [[ "${PASSWORD_STDIN}" == "true" ]]; then
    svm_password="${STDIN_PASSWORD}"
  elif [[ "${GENERATE_PASSWORD}" == "true" ]]; then
    svm_password="$(openssl rand -hex 16)"
  else
    read -r -s -p "ONTAP runtime password: " svm_password </dev/tty
    echo "" >&2
    if [[ -z "${svm_password}" ]]; then
      warn "Empty password supplied; aborting."
      exit 1
    fi
  fi

  jq -n \
    --arg name "${sec_name}" \
    --arg ns "${PREP_NS}" \
    --arg backend_name "${BACKEND_NAME}" \
    --arg backend_id "${BACKEND_ID}" \
    --arg tenant_id "${resolved_tid}" \
    --arg tenant_name "${t_name}" \
    --arg username "${svm_user}" \
    --arg password "${svm_password}" \
    --arg svm "${svm_name}" \
    '{
      apiVersion: "v1",
      kind: "Secret",
      metadata: {
        name: $name,
        namespace: $ns,
        labels: {
          "osac.openshift.io/secret-type": "storage-tenant-preparation",
          "osac.openshift.io/storage-backend": $backend_name,
          "osac.openshift.io/storage-backend-id": $backend_id,
          "osac.openshift.io/tenant-id": $tenant_id,
          "osac.openshift.io/tenant": $tenant_name
        }
      },
      type: "Opaque",
      stringData: {
        username: $username,
        password: $password,
        svm: $svm
      }
    }' | kubectl apply -f - >/dev/null

  unset svm_password
  prepared_count=$((prepared_count + 1))
done
log "Prepared ${prepared_count} ONTAP tenant preparation Secret(s)."
