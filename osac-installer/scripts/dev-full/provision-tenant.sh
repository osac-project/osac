#!/usr/bin/env bash
# dev-full: provision a ready-to-use tenant so the browser "create a VM" flow works.
#
# Why this exists (the chain of facts that makes it necessary):
#   - Networking resources (VirtualNetwork/Subnet/SecurityGroup) cannot live in the
#     reserved 'shared'/'system' tenants, so a real tenant is required.
#   - A regular user's tenant is derived from the Keycloak *organization* claim
#     (groups map to tenants only for service accounts). With no organization the
#     claim is empty and the API returns "failed to determine assignable tenants".
#   - JIT provisioning creates the *user record* on first authenticated call, but it
#     requires the DB tenant to already exist ("tenant '<t>' doesn't exist" otherwise).
#   - The Tenants API is gRPC-only (not exposed over REST), so the DB tenant is
#     created with a throwaway in-cluster grpcurl pod. The Fulfillment token is
#     stored in a temporary Secret and exposed to grpcurl through an environment
#     reference. This keeps the host dependency-free: no host grpcurl is needed.
#
# What this does (all idempotent -- safe to re-run):
#   1. Create the DB tenant via the private gRPC Tenants API. Creating the tenant
#      auto-provisions its default VirtualNetwork + Subnet + SecurityGroup (tenant
#      onboarding), so no network seeding is needed here.
#   2. Create a matching, enabled Keycloak organization.
#   3. Add the dev users (default tenant1_user, tenant1_admin) as organization
#      members so their tokens carry the organization claim and they can log in to
#      the UI and manage resources in the tenant.
#
# Requires KUBECONFIG to point at the kind cluster (set by the Makefile).
#
# Usage: provision-tenant.sh [osac-namespace]
#   Env overrides: TENANT, TENANT_USERS (comma-separated), KC_NS, KC_REALM,
#                  INTERNAL_SVC, INTERNAL_PORT, GRPCURL_IMAGE, FULFILLMENT_CA_FILE

set -euo pipefail

NS="${1:-${NS:-osac}}"
TENANT="${TENANT:-tenant1}"
TENANT_USERS="${TENANT_USERS:-tenant1_user,tenant1_admin}"
KC_NS="${KC_NS:-keycloak}"
KC_REALM="${KC_REALM:-osac}"
INTERNAL_SVC="${INTERNAL_SVC:-fulfillment-internal-api}"
INTERNAL_PORT="${INTERNAL_PORT:-8001}"
GRPCURL_IMAGE="${GRPCURL_IMAGE:-docker.io/fullstorydev/grpcurl:latest}"
AUTH_CA_FILE="${FULFILLMENT_CA_FILE:-}"
TEMP_AUTH_CA_FILE=""
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../lib.sh"

log()  { echo "[+] $*"; }
warn() { echo "[!] $*" >&2; }

log "Provisioning tenant '${TENANT}' in namespace '${NS}'..."

PF_PID=""
POD_NAME=""
TOKEN_SECRET_NAME=""
delete_token_secret() {
  [[ -z "${TOKEN_SECRET_NAME}" ]] && return 0
  if kubectl -n "${NS}" delete secret "${TOKEN_SECRET_NAME}" --ignore-not-found >/dev/null 2>&1; then
    TOKEN_SECRET_NAME=""
  else
    return 1
  fi
}
cleanup() {
  if [[ -n "${POD_NAME}" ]]; then
    kubectl -n "${NS}" delete pod "${POD_NAME}" --ignore-not-found >/dev/null 2>&1 || true
  fi
  if ! delete_token_secret; then
    warn "failed to delete temporary token Secret ${TOKEN_SECRET_NAME} during exit cleanup"
  fi
  if [[ -n "${TEMP_AUTH_CA_FILE}" ]]; then
    rm -f "${TEMP_AUTH_CA_FILE}"
  fi
  if [[ -n "${PF_PID}" ]]; then
    kill "${PF_PID}" 2>/dev/null || true
    wait "${PF_PID}" 2>/dev/null || true
  fi
}
trap cleanup EXIT

# ── 1. Create the DB tenant via the private gRPC Tenants API ────────────────────
# Tenants is gRPC-only. Run grpcurl in-cluster so no host grpcurl is required.
# Store the short-lived Fulfillment token in a temporary Secret; grpcurl expands
# its Secret-backed environment variable at request time. -insecure preserves
# the existing TLS behavior against the internal CA.
if [[ -z "${AUTH_CA_FILE}" ]]; then
  TEMP_AUTH_CA_FILE="$(mktemp)"
  AUTH_CA_FILE="${TEMP_AUTH_CA_FILE}"
  kubectl -n "${NS}" get configmap ca-bundle -o jsonpath='{.data.bundle\.pem}' > "${AUTH_CA_FILE}"
  [[ -s "${AUTH_CA_FILE}" ]] || { warn "ca-bundle ConfigMap in ${NS} has no bundle.pem"; exit 1; }
fi
[[ -r "${AUTH_CA_FILE}" ]] || { warn "Fulfillment issuer CA bundle is not readable"; exit 1; }
fulfillment_token="$(get_fulfillment_service_account_token "${NS}" --cacert "${AUTH_CA_FILE}")"
pod="osac-provision-tenant-$$"
POD_NAME="${pod}"
TOKEN_SECRET_NAME="osac-provision-tenant-token-$$"
kubectl -n "${NS}" delete pod "${pod}" --ignore-not-found >/dev/null 2>&1 || true
kubectl -n "${NS}" delete secret "${TOKEN_SECRET_NAME}" --ignore-not-found >/dev/null 2>&1 || true
printf '%s' "${fulfillment_token}" \
  | kubectl -n "${NS}" create secret generic "${TOKEN_SECRET_NAME}" \
      --from-file=token=/dev/stdin --dry-run=client -o yaml \
  | kubectl -n "${NS}" apply -f - >/dev/null
unset fulfillment_token
export POD_NAME TOKEN_SECRET_NAME GRPCURL_IMAGE NS TENANT INTERNAL_SVC INTERNAL_PORT
pod_manifest=$(python3 - <<'PY'
import json
import os

namespace = os.environ["NS"]
pod_name = os.environ["POD_NAME"]
payload = {"object": {"metadata": {"name": os.environ["TENANT"]}}}
container_args = [
    "-expand-headers",
    "-insecure",
    "-H", "authorization: Bearer ${FULFILLMENT_TOKEN}",
    "-d", json.dumps(payload),
    f"{os.environ['INTERNAL_SVC']}:{os.environ['INTERNAL_PORT']}",
    "osac.private.v1.Tenants/Create",
]
manifest = {
    "apiVersion": "v1",
    "kind": "Pod",
    "metadata": {"name": pod_name, "namespace": namespace},
    "spec": {
        "restartPolicy": "Never",
        "automountServiceAccountToken": False,
        "containers": [{
            "name": "grpcurl",
            "image": os.environ["GRPCURL_IMAGE"],
            "command": ["grpcurl"],
            "args": container_args,
            "env": [{"name": "FULFILLMENT_TOKEN", "valueFrom": {
                "secretKeyRef": {"name": os.environ["TOKEN_SECRET_NAME"], "key": "token"},
            }}],
        }],
    },
}
print(json.dumps(manifest))
PY
)
printf '%s\n' "${pod_manifest}" | kubectl -n "${NS}" apply -f - >/dev/null

# Wait for the one-shot pod to finish (Succeeded or Failed), then read its output.
phase=""
for _ in $(seq 1 60); do
  phase=$(kubectl -n "${NS}" get pod "${pod}" -o jsonpath='{.status.phase}' 2>/dev/null || true)
  [[ "${phase}" == "Succeeded" || "${phase}" == "Failed" ]] && break
  sleep 2
done
out=$(kubectl -n "${NS}" logs "${pod}" 2>/dev/null || true)
kubectl -n "${NS}" delete pod "${pod}" --ignore-not-found >/dev/null 2>&1 || true
POD_NAME=""
if ! delete_token_secret; then
  warn "failed to delete temporary token Secret ${TOKEN_SECRET_NAME}; exit cleanup will retry"
fi

if [[ "${phase}" == "Succeeded" ]]; then
  log "  tenant '${TENANT}' created (default network auto-provisioned)"
elif echo "${out}" | grep -qi "AlreadyExists"; then
  log "  tenant '${TENANT}' already exists"
else
  warn "  tenant create did not succeed (phase=${phase:-unknown}):"
  echo "${out}" | sed 's/^/      /' >&2
  exit 1
fi

# ── 1b. Ensure subnet target namespace(s) for legacy standalone setup ────────────
# Each Subnet gets its own k8s namespace named after the Subnet CR: the osac-operator
# ComputeInstance controller creates/looks for the KubeVirt VM in that
# subnet-target namespace (osac.openshift.io/subnet-target-namespace annotation),
# NOT in the tenant namespace. Current dev-full installs enable Subnet AAP
# provisioning, which creates the namespace and API-only CUDN object. This host
# helper retains an idempotent namespace ensure for older local clusters; it does
# not create a CUDN or replace the normal AAP provisioning path. Kind VMs use the
# default pod network + cluster-wide l2bridge binding, with no CUDN dataplane.
#
# Subnets are scoped to a tenant by the osac.openshift.io/tenant annotation (not a
# label), and onboarding creates them asynchronously, so poll for the tenant's
# subnet(s) to appear before ensuring their namespaces.
subnets_for_tenant() {
  kubectl -n "${NS}" get subnets -o json 2>/dev/null | python3 -c "
import json,sys
for s in json.load(sys.stdin).get('items', []):
    if s.get('metadata', {}).get('annotations', {}).get('osac.openshift.io/tenant') == '${TENANT}':
        print(s['metadata']['name'])
" 2>/dev/null || true
}
onboarded_subnets=""
for _ in $(seq 1 30); do
  onboarded_subnets=$(subnets_for_tenant)
  [[ -n "${onboarded_subnets}" ]] && break
  sleep 2
done
if [[ -z "${onboarded_subnets}" ]]; then
  warn "  no subnet found for tenant '${TENANT}' yet — VM provisioning will hang until its subnet namespace exists"
else
  for sn in ${onboarded_subnets}; do
    kubectl create namespace "${sn}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
    log "  subnet target namespace ready: ${sn}"
  done
fi

# ── 2 & 3. Keycloak organization + membership ───────────────────────────────────
# Port-forward Keycloak and drive its admin API to create an enabled organization
# matching the tenant and add the dev users as members.
admin_pw=$(kubectl -n "${KC_NS}" get secret keycloak-admin-credentials \
  -o jsonpath='{.data.admin-password}' | base64 -d)

kubectl -n "${KC_NS}" port-forward svc/keycloak 18443:443 >/dev/null 2>&1 &
PF_PID=$!
sleep 3

KC="https://localhost:18443"
kc_token=$(curl -sk -X POST "${KC}/realms/master/protocol/openid-connect/token" \
  -d client_id=admin-cli -d username=admin -d "password=${admin_pw}" \
  -d grant_type=password \
  | python3 -c "import json,sys; print(json.load(sys.stdin)['access_token'])")

KCURL=(curl -skS -H "Authorization: Bearer ${kc_token}" -H "Content-Type: application/json")

# find_org_id <name> — prints the organization id, or '' if absent.
find_org_id() {
  "${KCURL[@]}" "${KC}/admin/realms/${KC_REALM}/organizations?exact=true&search=$1" \
    | python3 -c "import json,sys; print(next((o['id'] for o in json.load(sys.stdin) if o.get('name')=='$1'), ''))" 2>/dev/null || true
}

# Create the organization (idempotent: 201 created, 409 already exists both OK).
code=$("${KCURL[@]}" -o /dev/null -w "%{http_code}" \
  -X POST "${KC}/admin/realms/${KC_REALM}/organizations" \
  -d "{\"name\":\"${TENANT}\",\"alias\":\"${TENANT}\",\"enabled\":true,\"domains\":[{\"name\":\"${TENANT}.localhost\",\"verified\":true}]}")
case "${code}" in
  201) log "  organization '${TENANT}' created" ;;
  409) log "  organization '${TENANT}' already exists" ;;
  *)   warn "  organization create returned HTTP ${code}" ;;
esac

org_id=$(find_org_id "${TENANT}")
if [[ -z "${org_id}" ]]; then
  warn "  could not resolve organization id for '${TENANT}' — skipping membership"
  exit 1
fi

# Add each dev user as an organization member (idempotent).
IFS=',' read -ra users <<< "${TENANT_USERS}"
for u in "${users[@]}"; do
  u="${u// /}"
  [[ -z "${u}" ]] && continue
  uid=$("${KCURL[@]}" "${KC}/admin/realms/${KC_REALM}/users?username=${u}&exact=true" \
    | python3 -c "import json,sys; d=json.load(sys.stdin); print(d[0]['id'] if d else '')" 2>/dev/null || true)
  if [[ -z "${uid}" ]]; then
    warn "    user '${u}' not found — skipping"
    continue
  fi
  code=$("${KCURL[@]}" -o /dev/null -w "%{http_code}" \
    -X POST "${KC}/admin/realms/${KC_REALM}/organizations/${org_id}/members" -d "\"${uid}\"")
  case "${code}" in
    201) log "    member added: ${u}" ;;
    409) log "    member already present: ${u}" ;;
    *)   warn "    adding member '${u}' returned HTTP ${code}" ;;
  esac
done

log "Tenant '${TENANT}' ready — log in as one of: ${TENANT_USERS} (password: keycloak default-user-password)"
