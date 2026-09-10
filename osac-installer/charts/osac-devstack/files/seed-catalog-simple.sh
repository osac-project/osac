#!/usr/bin/env bash
# Seed the OSAC catalog via the private REST API.
#
# Creates disk images, instance types, templates, and catalog items for dev-full.
# Values are hardcoded from catalog-seed.yaml for simplicity.
#
# Usage: seed-catalog-simple.sh <internal-api-service> <internal-api-port>

set -euo pipefail

SERVICE="${1:-fulfillment-internal-api}"
PORT="${2:-8001}"
NS="${NS:-osac}"
ADMIN_TOKEN="${ADMIN_TOKEN:-$(kubectl -n "${NS}" create token admin)}"

log() { echo "[+] $*"; }

# The internal API is TLS-enabled. The certificate is signed by the dev CA, so
# curl skips verification while still using TLS for the connection.
API="https://${SERVICE}.${NS}.svc.cluster.local:${PORT}/api/private/v1"
CURL=(curl -skS \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  -H "Content-Type: application/json")

# Create a resource, treating only a 409 response as an idempotent success.
# Validation, authentication, and transport errors must fail the hook instead
# of being reported as successful seeding.
create_or_skip() {
  local label="$1"
  local path="$2"
  local data="$3"
  local response
  local status
  local body

  if ! response=$("${CURL[@]}" -X POST "${API}/${path}" -d "${data}" -w $'\n%{http_code}' 2>&1); then
    printf '%s\n' "${response}" >&2
    return 1
  fi

  status="${response##*$'\n'}"
  body="${response%$'\n'*}"
  case "${status}" in
    2??)
      log "  ${label}"
      return 0
      ;;
    409)
    log "  ${label} (already exists)"
    return 0
      ;;
    *)
      printf 'HTTP %s: %s\n' "${status}" "${body}" >&2
      return 1
      ;;
  esac
}

log "Seeding catalog into '${NS}'..."

# Disk images
log "Creating disk images..."
create_or_skip "disk-image: fedora" \
  "disk_images" \
  '{"metadata":{"name":"fedora","tenant":"shared"},"spec":{"source_type":"SOURCE_TYPE_REGISTRY","source_ref":"quay.io/containerdisks/fedora:latest","guest_os_family":"GUEST_OS_FAMILY_LINUX","architecture":["ARCHITECTURE_AMD64"],"lifecycle":"DISK_IMAGE_LIFECYCLE_AVAILABLE"}}'

# Instance types
log "Creating instance types..."
for it in \
  "u1-small:2:4:2 cores, 4 GiB RAM" \
  "u1-medium:4:8:4 cores, 8 GiB RAM" \
  "u1-large:8:16:8 cores, 16 GiB RAM"; do
  IFS=: read -r name cores memGib desc <<<"$it"
  create_or_skip "instance-type: ${name} (${cores} cores, ${memGib} GiB RAM)" \
    "instance_types" \
    "{\"metadata\":{\"name\":\"${name}\"},\"spec\":{\"cores\":${cores},\"memory_gib\":${memGib},\"description\":\"${desc}\",\"state\":\"INSTANCE_TYPE_STATE_ACTIVE\"}}"
done

# Templates
log "Creating templates..."
create_or_skip "template: osac.templates.ocp_virt_vm" \
  "compute_instance_templates" \
  '{"id":"osac.templates.ocp_virt_vm","title":"Virtual Machine Template (Linux and Windows)","description":"VM template for OpenShift Virtualization supporting Linux and Windows guests.","spec_defaults":{"boot_disk":{"size_gib":10},"run_strategy":"Always","instance_type":{"name":"u1-medium"},"disk_image":{"name":"fedora"}},"parameters":[{"name":"exposed_ports","title":"Exposed Ports","description":"Ports to expose (e.g. 22/tcp,80/tcp)","type":"string","required":false}]}'

# Catalog items
log "Creating catalog items..."
create_or_skip "catalog-item: linux-vm" \
  "compute_instance_catalog_items" \
  '{"metadata":{"name":"linux-vm"},"title":"Linux Virtual Machine","description":"Fedora-based VM with KVM acceleration. Default: 4 cores, 8 GiB RAM, 10 GiB disk.","template":{"id":"osac.templates.ocp_virt_vm"},"published":true}'

log "Catalog seeded — ready to create compute instances"
