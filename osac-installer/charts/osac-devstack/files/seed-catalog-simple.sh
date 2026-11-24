#!/usr/bin/env bash
# Seed the minimal dev-full catalog through the private REST API.
#
# The hook runs inside the cluster, but the private API uses TLS. A local
# port-forward lets curl authenticate with the short-lived Keycloak
# service-account token without requiring grpcurl in the hook image.
set -euo pipefail

NS="${1:-${NS:-osac}}"
INTERNAL_SVC="${INTERNAL_SVC:-fulfillment-internal-api}"
INTERNAL_PORT="${INTERNAL_PORT:-8001}"
LOCAL_PORT="${LOCAL_PORT:-8001}"
CA_FILE="${CA_FILE:-/etc/ca-bundle/bundle.pem}"
export DEV_FEDORA_IMAGE_NAME="${DEV_FEDORA_IMAGE_NAME:-fedora}"
export DEV_FEDORA_IMAGE_SOURCE="${DEV_FEDORA_IMAGE_SOURCE:-quay.io/containerdisks/fedora:latest}"
export DEV_FEDORA_IMAGE_ARCHITECTURES="${DEV_FEDORA_IMAGE_ARCHITECTURES:-[\"ARCHITECTURE_AMD64\",\"ARCHITECTURE_ARM64\"]}"
catalog_disk_update_mask=""
# Preserve ordinary Linux dev-full's existing create-only seeding behavior.
# An explicitly selected development image must also reach existing catalogs.
if [[ "${DEV_FEDORA_IMAGE_NAME}" != fedora \
  || "${DEV_FEDORA_IMAGE_SOURCE}" != quay.io/containerdisks/fedora:latest ]]; then
  catalog_disk_update_mask=fields.disk_image
fi

log() { echo "[+] $*"; }

source /scripts/fulfillment-auth.sh
AUTH_TOKEN="$(get_fulfillment_access_token)"

# The REST gateway encodes gRPC AlreadyExists as status code 6 in its JSON body.
is_already_exists() {
  grep -Eq '"code"[[:space:]]*:[[:space:]]*6([[:space:]]*[,}])' "${response_file}"
}

response_file=$(mktemp)

# cleanup removes the response file and stops the port-forward.
cleanup() {
  rm -f "${response_file}"
  kill "${pf_pid:-}" 2>/dev/null || true
  wait "${pf_pid:-}" 2>/dev/null || true
}
trap cleanup EXIT

kubectl -n "${NS}" port-forward "svc/${INTERNAL_SVC}" "${LOCAL_PORT}:${INTERNAL_PORT}" >/dev/null 2>&1 &
pf_pid=$!

INTERNAL_HOST="${INTERNAL_SVC}.${NS}.svc.cluster.local"
API="https://${INTERNAL_HOST}:${LOCAL_PORT}/api/private/v1"
CURL=(curl --cacert "${CA_FILE}" --resolve "${INTERNAL_HOST}:${LOCAL_PORT}:127.0.0.1" --noproxy "${INTERNAL_HOST}")
api_ready=false
for _ in $(seq 1 60); do
  if ! kill -0 "${pf_pid}" 2>/dev/null; then
    echo 'Catalog seed port-forward failed' >&2
    exit 1
  fi
  if "${CURL[@]}" --silent --show-error --fail \
    --connect-timeout 1 --max-time 5 \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    -o /dev/null "${API}/disk_images"; then
    api_ready=true
    break
  fi
  sleep 5
done

if [[ "${api_ready}" != true ]]; then
  echo 'Timed out waiting for a healthy fulfillment API' >&2
  exit 1
fi

# post creates one catalog resource and accepts only gRPC AlreadyExists conflicts.
post() {
  local path="$1"
  local description="$2"
  local payload="$3"
  local update_mask="${4:-}"
  local status

  if ! status=$("${CURL[@]}" --silent --show-error \
    --connect-timeout 3 --max-time 30 -o "${response_file}" -w '%{http_code}' \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    -H 'Content-Type: application/json' \
    -X POST "${API}/${path}" -d "${payload}"); then
    echo "Failed to create ${description}: request to ${path} did not complete" >&2
    return 1
  fi

  case "${status}" in
    2*) log "${description}: created" ;;
    409)
      if is_already_exists; then
        if [[ -n "${update_mask}" ]]; then
          update_catalog_disk_default "${payload}"
        else
          log "${description}: already exists"
        fi
      else
        echo "Failed to create ${description}: API returned HTTP ${status}" >&2
        return 1
      fi
      ;;
    *)
      echo "Failed to create ${description}: API returned HTTP ${status}" >&2
      return 1
      ;;
  esac
}

# Existing installations already have linux-vm. Update just its disk default;
# the original DiskImage and disks of existing VMs remain separate resources.
update_catalog_disk_default() {
  local payload="$1" item_id patch
  "${CURL[@]}" --silent --show-error --fail --connect-timeout 3 --max-time 30 \
    -H "Authorization: Bearer ${AUTH_TOKEN}" --get \
    --data-urlencode 'filter=this.metadata.name == "linux-vm"' \
    -o "${response_file}" "${API}/compute_instance_catalog_items"
  item_id=$(python3 - "${response_file}" <<'PY'
import json
import re
import sys

items = json.load(open(sys.argv[1])).get("items", [])
if len(items) != 1 or not re.fullmatch(r"[a-zA-Z0-9-]+", items[0].get("id", "")):
    sys.exit("Expected exactly one linux-vm catalog item with a valid ID")
print(items[0]["id"])
PY
)
  patch=$(python3 - "${response_file}" "${payload}" <<'PY'
import json
import sys

current = json.load(open(sys.argv[1]))["items"][0]
desired = json.loads(sys.argv[2])
print(json.dumps({"metadata": {"version": current["metadata"].get("version", 0)},
                  "fields": {"disk_image": desired["fields"]["disk_image"]}}))
PY
)
  "${CURL[@]}" --silent --show-error --fail --connect-timeout 3 --max-time 30 \
    -H "Authorization: Bearer ${AUTH_TOKEN}" -H 'Content-Type: application/json' \
    -X PATCH -d "${patch}" -o "${response_file}" \
    "${API}/compute_instance_catalog_items/${item_id}?update_mask=fields.disk_image&lock=true"
  log 'compute instance catalog item linux-vm: disk default updated'
}

log "Seeding catalog into '${NS}'..."

post disk_images "disk image ${DEV_FEDORA_IMAGE_NAME}" "$(python3 - <<'PY'
import json
import os

print(json.dumps({
    "metadata": {"name": os.environ["DEV_FEDORA_IMAGE_NAME"], "tenant": "shared"},
    "spec": {
        "source_type": "SOURCE_TYPE_REGISTRY",
        "source_ref": os.environ["DEV_FEDORA_IMAGE_SOURCE"],
        "guest_os_family": "GUEST_OS_FAMILY_LINUX",
        "architecture": json.loads(os.environ["DEV_FEDORA_IMAGE_ARCHITECTURES"]),
        "lifecycle": "DISK_IMAGE_LIFECYCLE_AVAILABLE",
    },
}))
PY
)"

for entry in \
  'u1-small:2:4:2 cores, 4 GiB RAM' \
  'u1-medium:4:8:4 cores, 8 GiB RAM' \
  'u1-large:8:16:8 cores, 16 GiB RAM'; do
  name="${entry%%:*}"
  remainder="${entry#*:}"
  vcpus="${remainder%%:*}"
  remainder="${remainder#*:}"
  memory_gib="${remainder%%:*}"
  description="${remainder#*:}"

  post instance_types "instance type ${name}" "{
    \"metadata\": {\"name\": \"${name}\"},
    \"spec\": {
      \"vcpus\": ${vcpus},
      \"memory_gib\": ${memory_gib},
      \"description\": \"${description}\",
      \"state\": \"INSTANCE_TYPE_STATE_ACTIVE\"
    }
  }"
done

post compute_instance_templates 'compute instance template osac.templates.ocp_virt_vm' "$(python3 - <<'PY'
import json
import os

print(json.dumps({
    "id": "osac.templates.ocp_virt_vm",
    "title": "Virtual Machine Template (Linux and Windows)",
    "description": "VM template for OpenShift Virtualization supporting Linux and Windows guests.",
    "spec_defaults": {
        "boot_disk": {"size_gib": 10},
        "run_strategy": "Always",
        "instance_type": {"name": "u1-medium"},
        "disk_image": {"name": os.environ["DEV_FEDORA_IMAGE_NAME"]},
    },
}))
PY
)"

post compute_instance_catalog_items 'compute instance catalog item linux-vm' "$(python3 - <<'PY'
import json
import os

print(json.dumps({
    "metadata": {"name": "linux-vm"},
    "title": "Linux Virtual Machine",
    "description": "Fedora-based virtual machine with KVM acceleration.",
    "template": {"id": "osac.templates.ocp_virt_vm"},
    "published": True,
    "fields": {"disk_image": {"editable": {"default_value": {
        "name": os.environ["DEV_FEDORA_IMAGE_NAME"], "shared": True,
    }}}},
}))
PY
)" "${catalog_disk_update_mask}"

log 'Catalog seeded — ready to create compute instances'
