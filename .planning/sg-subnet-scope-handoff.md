# SecurityGroup Scope Change: VirtualNetwork → Subnet

## Handoff for Implementation Session

Work from `/home/dmanor/dev/osac-project/osac-workspace/osac` (mono-repo) and
`/home/dmanor/dev/osac-project/osac-workspace/enhancement-proposals` (design docs).
Read component AGENTS.md/CLAUDE.md before editing.

---

## Problem

SecurityGroup is currently scoped to VirtualNetwork, but the fabric manager (Netris)
enforces ACL rules per network segment (which maps to an OSAC Subnet, not VirtualNetwork).
When a VNet CIDR (e.g., `10.100.0.0/16`) is wider than its subnets (`10.100.1.0/24`,
`10.100.2.0/24`), Netris rejects the ACL rule because `/16` isn't in its IPAM. Multi-subnet
VirtualNetworks are broken for SecurityGroup provisioning.

The current `create_security_group.yaml` AAP role uses `_sg_vn_cidr` (the VirtualNetwork
CIDR) as `acl_dst_prefix` at line 61. This is wrong — it should use the Subnet CIDR.

## Solution

Move SecurityGroup to be a child of Subnet instead of VirtualNetwork. The ACL rules use
the Subnet CIDR as `dst_prefix`.

Current resource hierarchy:
```
VirtualNetwork
  ├── Subnet
  ├── SecurityGroup  ← currently here
  └── NATGateway
```

Target:
```
VirtualNetwork
  ├── Subnet
  │     └── SecurityGroup  ← moved here
  └── NATGateway
```

## Changes Required (in dependency order)

### 1. Proto (fulfillment-service)

**Private proto** — `fulfillment-service/proto/private/osac/private/v1/security_group_type.proto`:
- Add `import "osac/private/v1/subnet_type.proto";`
- Add `SubnetLocalReference subnet = N;` to `SecurityGroupSpec` (pick next available field number)
- Mark `virtual_network` as deprecated: `VirtualNetworkLocalReference virtual_network = 1 [deprecated = true];`
- Update all comments: "scoped to a Subnet" instead of "scoped to a VirtualNetwork"
- Update `owner-reference` annotation docs: owner is the Subnet's parent VN (for GC)

**Public proto** — `fulfillment-service/proto/public/osac/public/v1/security_group_type.proto`:
- Same changes as private proto

**Then run:**
```bash
cd fulfillment-service
uv run dev.py lint proto && buf generate
```

### 2. CLI (fulfillment-service)

**File:** `fulfillment-service/internal/cmd/cli/create/securitygroup/create_securitygroup_cmd.go`
- Add `--subnet` flag (required)
- Keep `--virtual-network` as deprecated alias (resolve to subnet's VN internally)
- Update help text and examples at lines ~304-310
- When `--subnet` is used, auto-resolve the parent VN for the owner-reference annotation

### 3. Server Validation (fulfillment-service)

**File:** `fulfillment-service/internal/servers/private_security_groups_server.go`
- `validateSecurityGroup` (line ~234): Accept `subnet` field. If only `virtual_network` is set
  (backward compat), resolve to the VN's first subnet (fail if VN has multiple subnets — user must
  specify which subnet).
- `validateVirtualNetworkReference` (line ~269): Replace with `validateSubnetReference` — validate
  subnet exists, is READY, and set the owner-reference to the subnet's parent VN.
- Update immutability validation (`validateImmutableFieldsSecurityGroup`) — `subnet` is immutable.

**File:** `fulfillment-service/internal/servers/private_compute_instances_server.go`
- `validateNetworkReferencesState` (line ~1012): Currently checks "SecurityGroups belong to the same
  VirtualNetwork as their attachment's Subnet." Change to: "SecurityGroups reference the same Subnet
  as the attachment." This is simpler — direct Subnet ID comparison.

**File:** `fulfillment-service/internal/servers/private_baremetal_instances_server.go`
- `validateNetworkAttachments`: Same change — SG must reference the same subnet as the attachment.

**File:** `fulfillment-service/internal/servers/default_networking_provisioner.go`
- `createDefaultSecurityGroup` (line ~317): Takes `vnID` currently. Change to take `subnetID` — the
  default SG references the default subnet, not the default VN. The owner-reference annotation stays
  as the VN ID (for GC cascade).

### 4. DB Migration (fulfillment-service)

**New migration** in `fulfillment-service/internal/database/migrations/`:
- Backfill: for each existing SG, look up the VN's first subnet and set `data->'spec'->'subnet'`
- Update the `check_subnet_not_in_use` trigger (migration 52) to also check SecurityGroups
  referencing the subnet (prevent deleting a subnet with active SGs)
- Do NOT remove the `virtual_network` field from existing records (backward compat)

### 5. Operator (osac-operator)

**File:** `osac-operator/api/v1alpha1/securitygroup_types.go`
- Add `Subnet string` field to `SecurityGroupSpec` (alongside `VirtualNetwork`)
- `make manifests generate helm-crds`

**File:** `osac-operator/internal/controller/securitygroup_controller.go`
- `handleUpdate` (line ~159): Currently resolves parent VirtualNetwork by UUID label to get
  `ImplementationStrategy`. Change to: resolve Subnet by UUID label → get Subnet's
  `spec.virtualNetwork` → resolve VN → get `ImplementationStrategy` from VN spec.
- Pass Subnet CIDR (not VN CIDR) to the AAP job via the CR annotations.
- Add precondition: requeue until Subnet is Ready (like NATGateway waits for VN Ready).

### 6. AAP (osac-aap)

**File:** `osac-aap/playbook_osac_create_security_group.yml`
- Extract `subnet` ref from CR spec instead of `virtual_network`

**File:** `osac-aap/collections/ansible_collections/osac/templates/roles/netris/tasks/create_security_group.yaml`
- Line 8: `sg_virtual_network` → `sg_subnet` (read from `security_group.spec.subnet`)
- Lines 20-44: Resolve Subnet CR (not VN CR) to get the CIDR. Look up the parent VN from the
  Subnet's `spec.virtualNetwork` field to get the VPC name (VPC name == VN name).
- Line 61: `acl_dst_prefix` uses the **Subnet CIDR** (not VN CIDR)

**File:** `osac-aap/collections/ansible_collections/osac/templates/roles/netris/tasks/delete_security_group.yaml`
- Same changes if it references VN

### 7. Design Doc Changes (enhancement-proposals)

**Repo:** `/home/dmanor/dev/osac-project/osac-workspace/enhancement-proposals`
**PR branch:** `fix/OSAC-3210-bm-only-caas-design` on fork `danmanor`
**Push to fork, not origin.**

**File:** `enhancements/OSAC-1433-unified-networking/design.md`
- Update resource hierarchy diagram: SG is child of Subnet (not VN)
- Update dispatcher routing table: SG dispatched per Subnet (resolve implementation strategy from
  Subnet → VN → NetworkClass)

**File:** `enhancements/OSAC-1437-bmaas-networking/design.md`
- Update validation section: SG must reference the same subnet as the BMI network attachment
- Update example CLI commands

**File:** `enhancements/OSAC-1433-default-networking/design.md`
- Default SG creation references default subnet, not default VN
- Update the "Default Networking at Tenant Onboarding" flow

**File:** `enhancements/OSAC-1436-caas-networking/design.md`
- Cluster validation: SGs belong to same subnet as `network_attachment`

## Backward Compatibility

- **Proto**: Add `subnet` field, keep `virtual_network` as deprecated. Server accepts either:
  - If `subnet` is set → use it
  - If only `virtual_network` is set → resolve to VN's first subnet. If VN has multiple subnets,
    return `InvalidArgument: virtual_network has multiple subnets; use subnet field to specify which`
- **CLI**: Keep `--virtual-network` as deprecated alias for one release. If used with a multi-subnet
  VN, fail with a helpful error.
- **Existing DB records**: Migration backfills `subnet` from the VN's first subnet. For VNs with
  multiple subnets, the migration assigns the SG to the first subnet (alphabetical by name).
- **K8s CRs**: The operator accepts both `spec.virtualNetwork` and `spec.subnet`. If only VN is set,
  resolves to first subnet.

## Testing

- **Unit tests (fulfillment-service):**
  - SG creation with subnet ref → succeeds, VN auto-resolved for owner-reference
  - SG creation with VN ref (single subnet) → backward compat, subnet auto-resolved
  - SG creation with VN ref (multi subnet) → fails with helpful error
  - CI/BMI validation: SG must reference same subnet as attachment
  - Default SG created with subnet ref
- **Unit tests (osac-operator):**
  - SG controller resolves Subnet → VN → implementation strategy
  - SG controller requeues until Subnet is Ready
- **Integration (lab):**
  - Create VN `10.100.0.0/16` with subnets `demo-a` (`10.100.1.0/24`) and `demo-b` (`10.100.2.0/24`)
  - Create SG on `demo-a` → ACL uses `10.100.1.0/24` as dst_prefix → succeeds
  - Create SG on `demo-b` → ACL uses `10.100.2.0/24` as dst_prefix → succeeds
  - Create BMI with `--network-attachment subnet=demo-a,security-groups=sg-a` → works
  - Create BMI with `--network-attachment subnet=demo-a,security-groups=sg-b` → fails (SG-b is on demo-b)

## Lab Environment

- Server: `ssh root@10.6.76.208` (Red Hat VPN, RDU se-lab)
- OCP: SNO, namespace `osac-e2e-ci`, `KUBECONFIG=/root/.kube/config`
- Netris: nodeport on k3s
- Current workaround: 1:1 VNet/Subnet CIDR mapping (VNet CIDR == Subnet CIDR)

## Key Files

| Component | File | What to change |
|-----------|------|---------------|
| Proto (private) | `fulfillment-service/proto/private/osac/private/v1/security_group_type.proto` | Add `subnet` field, deprecate `virtual_network` |
| Proto (public) | `fulfillment-service/proto/public/osac/public/v1/security_group_type.proto` | Same |
| CLI | `fulfillment-service/internal/cmd/cli/create/securitygroup/create_securitygroup_cmd.go` | `--subnet` flag |
| Server (SG) | `fulfillment-service/internal/servers/private_security_groups_server.go` | Validate subnet |
| Server (CI) | `fulfillment-service/internal/servers/private_compute_instances_server.go` | SG→Subnet validation |
| Server (BMI) | `fulfillment-service/internal/servers/private_baremetal_instances_server.go` | SG→Subnet validation |
| Default networking | `fulfillment-service/internal/servers/default_networking_provisioner.go` | Default SG → subnet |
| DB migration | `fulfillment-service/internal/database/migrations/NNN_*.up.sql` | Backfill subnet field |
| Operator CRD | `osac-operator/api/v1alpha1/securitygroup_types.go` | Add Subnet field |
| Operator controller | `osac-operator/internal/controller/securitygroup_controller.go` | Resolve Subnet |
| AAP playbook | `osac-aap/playbook_osac_create_security_group.yml` | Extract subnet ref |
| AAP role | `osac-aap/collections/ansible_collections/osac/templates/roles/netris/tasks/create_security_group.yaml` | Subnet CIDR for ACL |
| EP unified | `enhancement-proposals/enhancements/OSAC-1433-unified-networking/design.md` | Resource hierarchy |
| EP BMaaS | `enhancement-proposals/enhancements/OSAC-1437-bmaas-networking/design.md` | Validation |
| EP default | `enhancement-proposals/enhancements/OSAC-1433-default-networking/design.md` | Default SG |
| EP CaaS | `enhancement-proposals/enhancements/OSAC-1436-caas-networking/design.md` | Validation |

## Conventions

- Branch: `feat/OSAC-XXXX-sg-subnet-scope`
- Commits: `OSAC-XXXX: description`, signed off (`git commit -s`)
- AI attribution: `Assisted-by: Claude Code <noreply@anthropic.com>`
- Push code to `origin` (osac-project), PR against `main`
- Push design docs to `fork` (danmanor), PR against `main`
- Run `uv run dev.py lint proto && buf generate` after proto changes
- Run `make manifests generate helm-crds` after CRD type changes
- Run `make lint test` before committing in each component
