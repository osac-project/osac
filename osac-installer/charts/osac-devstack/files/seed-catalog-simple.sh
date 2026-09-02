#!/usr/bin/env bash
# Seed the OSAC catalog via the private gRPC API
#
# Creates disk images, instance types, templates, and catalog items for dev-full.
# Values are hardcoded from catalog-seed.yaml for simplicity.
#
# Usage: seed-catalog-simple.sh <internal-api-service> <internal-api-port>

set -euo pipefail

SERVICE="${1:-fulfillment-internal-api}"
PORT="${2:-8001}"
NS="${NS:-osac}"

log() { echo "[+] $*"; }

# Helper: gRPC call via grpcurl
grpc_call() {
  local method="$1"
  local data="$2"
  grpcurl -plaintext -d "${data}" "${SERVICE}.${NS}.svc.cluster.local:${PORT}" "${method}"
}

log "Seeding catalog into '${NS}'..."

# Disk images
log "Creating disk images..."
grpc_call "osac.private.v1.DiskImages/Create" \
  '{"object":{"metadata":{"name":"fedora"},"spec":{"sourceType":"SOURCE_TYPE_REGISTRY","sourceRef":"quay.io/containerdisks/fedora:latest","architecture":["amd64","arm64"]}}}' \
  >/dev/null 2>&1 || log "  disk-image: fedora (already exists)"
log "  disk-image: fedora"

# Instance types
log "Creating instance types..."
for it in \
  "u1-small:2:4:2 cores, 4 GiB RAM" \
  "u1-medium:4:8:4 cores, 8 GiB RAM" \
  "u1-large:8:16:8 cores, 16 GiB RAM"; do
  IFS=: read -r name cores memGib desc <<<"$it"
  grpc_call "osac.private.v1.ComputeInstanceTypes/Create" \
    "{\"object\":{\"metadata\":{\"name\":\"${name}\"},\"spec\":{\"cores\":${cores},\"memoryGib\":${memGib},\"description\":\"${desc}\"}}}" \
    >/dev/null 2>&1 || log "  instance-type: ${name} (already exists)"
  log "  instance-type: ${name} (${cores} cores, ${memGib} GiB RAM)"
done

# Templates
log "Creating templates..."
grpc_call "osac.private.v1.ComputeInstanceTemplates/Create" \
  '{"object":{"metadata":{"name":"osac.templates.ocp_virt_vm"},"spec":{"title":"Virtual Machine Template (Linux and Windows)"}}}' \
  >/dev/null 2>&1 || log "  template: osac.templates.ocp_virt_vm (already exists)"
log "  template: osac.templates.ocp_virt_vm"

# Catalog items
log "Creating catalog items..."
grpc_call "osac.private.v1.CatalogItems/Create" \
  '{"object":{"metadata":{"name":"linux-vm"},"spec":{"title":"Linux Virtual Machine","description":"Fedora-based VM with KVM acceleration","templateId":"osac.templates.ocp_virt_vm","published":true}}}' \
  >/dev/null 2>&1 || log "  catalog-item: linux-vm (already exists)"
log "  catalog-item: linux-vm"

log "Catalog seeded — ready to create compute instances"
