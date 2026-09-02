#!/usr/bin/env bash
# dev-full: seed a starter catalog (disk image, instance types, template, catalog
# item) into fulfillment-service. These are shared/global catalog objects.
#
# Uses the private REST API (/api/private/v1/...) exposed on the internal API
# service (TLS, port 8001), reached via a local port-forward. Auth is the 'admin'
# ServiceAccount bearer token. Payloads follow the CURRENT proto schema — notably:
#   - ComputeInstanceTemplate.spec_defaults references an instance_type + disk_image
#     (the old inline cores/memory/image fields were removed)
#   - CatalogItem.template is a reference object ({id}), not a bare string
# The default NetworkClass is created by the chart's create-network-class hook
# (networkClass.enabled=true). Per-tenant networking (VirtualNetwork/Subnet/
# SecurityGroup) is auto-provisioned by tenant onboarding (see provision-tenant.sh),
# so it is NOT seeded here.
#
# Requires KUBECONFIG to point at the kind cluster (set by the Makefile).
#
# Usage: seed-catalog.sh [osac-namespace]

set -euo pipefail

NS="${1:-${NS:-osac}}"
INTERNAL_SVC="${INTERNAL_SVC:-fulfillment-internal-api}"
INTERNAL_PORT="${INTERNAL_PORT:-8001}"
LOCAL_PORT="${LOCAL_PORT:-8001}"

log()  { echo "[+] $*"; }
warn() { echo "[!] $*" >&2; }

# has_id <json> — succeeds if the response body carries an "id".
has_id() { python3 -c "import json,sys; d=json.load(sys.stdin); sys.exit(0 if 'id' in d else 1)" 2>/dev/null; }
# get <json> <key-path> — python-style .get chain, prints '' on miss.
jget() { python3 -c "import json,sys; d=json.load(sys.stdin); print(${1})" 2>/dev/null || true; }

log "Seeding catalog into '${NS}' via ${INTERNAL_SVC}:${INTERNAL_PORT}..."

admin_token=$(kubectl -n "${NS}" create token admin)

# Port-forward the internal API.
kubectl -n "${NS}" port-forward "svc/${INTERNAL_SVC}" "${LOCAL_PORT}:${INTERNAL_PORT}" >/dev/null 2>&1 &
pf_pid=$!
trap 'kill "${pf_pid}" 2>/dev/null || true; wait "${pf_pid}" 2>/dev/null || true' EXIT
sleep 3

API="https://localhost:${LOCAL_PORT}/api/private/v1"
CURL=(curl -skS -H "Authorization: Bearer ${admin_token}" -H "Content-Type: application/json")

post() {  # post <path> <json-body>  -> prints response body
  "${CURL[@]}" -X POST "${API}/$1" -d "$2"
}

# ── DiskImage ─────────────────────────────────────────────────────────────────
resp=$(post disk_images '{
  "metadata": {"name": "fedora", "tenant": "shared"},
  "spec": {
    "source_type": "SOURCE_TYPE_REGISTRY",
    "source_ref": "quay.io/containerdisks/fedora:latest",
    "guest_os_family": "GUEST_OS_FAMILY_LINUX",
    "architecture": ["ARCHITECTURE_AMD64"],
    "lifecycle": "DISK_IMAGE_LIFECYCLE_AVAILABLE"
  }
}')
echo "$resp" | has_id && log "  disk-image: fedora" || warn "  disk-image fedora failed (may already exist)"

# ── InstanceTypes ─────────────────────────────────────────────────────────────
# name:cores:memory_gib:description
for entry in \
  "u1-small:2:4:2 cores, 4 GiB RAM" \
  "u1-medium:4:8:4 cores, 8 GiB RAM" \
  "u1-large:8:16:8 cores, 16 GiB RAM"; do
  it_name="${entry%%:*}"; rest="${entry#*:}"
  it_cores="${rest%%:*}"; rest="${rest#*:}"
  it_mem="${rest%%:*}"; it_desc="${rest#*:}"
  resp=$(post instance_types "{
    \"metadata\": {\"name\": \"${it_name}\"},
    \"spec\": {\"cores\": ${it_cores}, \"memory_gib\": ${it_mem}, \"description\": \"${it_desc}\", \"state\": \"INSTANCE_TYPE_STATE_ACTIVE\"}
  }")
  echo "$resp" | has_id && log "  instance-type: ${it_name} (${it_desc})" || warn "  instance-type ${it_name} failed (may already exist)"
done

# ── ComputeInstanceTemplate ───────────────────────────────────────────────────
# spec_defaults now references an instance_type + disk_image (inline cores/memory/
# image were removed from the schema).
resp=$(post compute_instance_templates '{
  "id": "osac.templates.ocp_virt_vm",
  "title": "Virtual Machine Template (Linux and Windows)",
  "description": "VM template for OpenShift Virtualization supporting Linux and Windows guests.",
  "spec_defaults": {
    "boot_disk": {"size_gib": 10},
    "run_strategy": "Always",
    "instance_type": {"name": "u1-medium"},
    "disk_image": {"name": "fedora"}
  },
  "parameters": [
    {
      "name": "exposed_ports",
      "title": "Exposed Ports",
      "description": "Ports to expose (e.g. 22/tcp,80/tcp)",
      "type": "string",
      "required": false
    }
  ]
}')
echo "$resp" | has_id && log "  template: osac.templates.ocp_virt_vm" || warn "  template failed (may already exist)"

# ── CatalogItem ───────────────────────────────────────────────────────────────
# template is now a reference object, not a bare string.
resp=$(post compute_instance_catalog_items '{
  "metadata": {"name": "linux-vm"},
  "title": "Linux Virtual Machine",
  "description": "Fedora-based virtual machine with KVM acceleration. Default: 4 cores, 8 GiB RAM, 10 GiB disk.",
  "template": {"id": "osac.templates.ocp_virt_vm"},
  "published": true,
  "tenant": ""
}')
echo "$resp" | has_id && log "  catalog-item: linux-vm" || warn "  catalog-item failed (may already exist)"

# ── Networking is NOT seeded here ──────────────────────────────────────────────
# The default VirtualNetwork + Subnet + SecurityGroup are auto-provisioned per
# tenant by tenant onboarding when the tenant is created (see provision-tenant.sh),
# using the default NetworkClass onboarding defaults. Seeding them here would place
# them in the reserved 'shared' tenant, which the API rejects. The NetworkClass
# itself is created by the chart's create-network-class hook (networkClass.enabled).

log "Catalog seeded — ready to create compute instances"
