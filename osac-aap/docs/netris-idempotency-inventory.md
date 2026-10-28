# OSAC-4923 — Netris AAP inventory / operation matrix

Source of truth for in-scope idempotency behavior. SG rule prune and
attached-subnet ACL scoping are **out of scope** (see Deferred).

## Deferred (not OSAC-4923)

| Ticket | Work |
|--------|------|
| [OSAC-4888](https://redhat.atlassian.net/browse/OSAC-4888) | SecurityGroup ACL desired-state reconcile and stale-rule prune |
| [OSAC-4925](https://redhat.atlassian.net/browse/OSAC-4925) | ACL fan-out scoped to `status.attachedSubnetCIDRs`; remove VN-wide / `0.0.0.0/0` defaults |
| [OSAC-4936](https://redhat.atlassian.net/browse/OSAC-4936) | Fulfillment Update/FieldMask immutability enforcement |

## Conventions

- **Ownership key**: Netris object `name` (OSAC CR name, or `{name}-snat` / `{name}-dnat` for NAT).
- **Create path is update path**: operator re-triggers create on config version bump.
- **Mutable-field decision (AC4)**: in-scope VPC / V-Net / IPAM / NAT fields are
  **create-only**. On name collision with immutable drift, fail clearly — do
  **not** update-in-place and do **not** delete-and-recreate. SecurityGroup rule
  content reconcile remains deferred (OSAC-4888 / upcoming SG refactor).
- **Immutable mismatch**: if name exists and immutable fields differ → fail clearly; never delete+recreate.
- **Default guard**: VirtualNetwork, Subnet, and NATGateway delete playbooks
  refuse `osac.openshift.io/default=true` even if a job extra variable tries
  to bypass the guard. SecurityGroup behavior is outside this ticket.

## Matrix

| Playbook | Task file | Owned Netris objects | Mutable (4923) | Immutable | Create recovery | Delete recovery |
|----------|-----------|----------------------|----------------|-----------|-----------------|-----------------|
| `playbook_osac_create_virtual_network.yml` | `create_virtual_network.yaml` | VPC, IPAM allocation | — | VPC tenant; allocation prefix, tenant, VPC | 2nd run no-op if match; fail on drift or unverifiable fields; partial: create missing VPC then allocation | — |
| `playbook_osac_delete_virtual_network.yml` | `delete_virtual_network.yaml` | VPC, IPAM allocation | — | — | — | Delete-if-absent; verify tenant/prefix/VPC ownership; still find allocation if VPC was already deleted; refuse default label |
| `playbook_osac_create_subnet.yml` | `create_subnet.yaml` | IPAM subnet, V-Net | — | IPAM prefix, tenant, site, purpose, VPC; V-Net VPC, tenant, site | 2nd run no-op if match; fail on drift or unverifiable fields; partial: create missing IPAM subnet or V-Net | — |
| `playbook_osac_delete_subnet.yml` | `delete_subnet.yaml` | IPAM subnet, V-Net | — | — | — | Delete-if-absent; verify tenant/site/VPC and prefix/purpose where available; refuse default label |
| `playbook_osac_create_security_group.yml` | `create_security_group.yaml` | ACL rules (`{sg}-ingress-*`, `{sg}-egress-*`) | — (rule reconcile deferred) | — | 2nd run no-op if ACL names exist (name-skip) | — |
| `playbook_osac_delete_security_group.yml` | `delete_security_group.yaml` | ACL rules | — | — | — | Delete-if-absent for known names; refuse default; orphan prune → **4888** |
| `playbook_osac_create_nat_gateway.yml` | `create_nat_gateway.yaml` | SNAT (`{name}-snat`) | — | action, VPC, site, protocol/state, source/destination, SNAT target/pool | 2nd run no-op if match; fail on drift or unverifiable fields | — |
| `playbook_osac_delete_nat_gateway.yml` | `delete_nat_gateway.yaml` | SNAT | — | — | — | Delete-if-absent; verify action/site/VPC/source and SNAT target when ExternalIP remains available; refuse default label |
| `playbook_osac_create_external_ip.yml` | `create_external_ip.yaml` | IPAM /32 subnet | — | address in requested pool; tenant; purpose | Reuse only if existing /32 is in the requested pool and tenant/purpose match | — |
| `playbook_osac_delete_external_ip.yml` | `delete_external_ip.yaml` | IPAM /32 | — | — | — | Delete-if-absent; verify tenant/site/purpose and annotated prefix when available |
| `playbook_osac_create_external_ip_pool.yml` | `create_external_ip_pool.yaml` | IPAM allocation + common subnet | — | prefix, tenant; common subnet site/purpose | Get-or-create; fail on drift or unverifiable fields | — |
| `playbook_osac_delete_external_ip_pool.yml` | `delete_external_ip_pool.yaml` | allocation + common | — | — | — | Delete-if-absent; verify prefix, tenant, and common-subnet site/purpose before each delete |
| `playbook_osac_attach_external_ip.yml` | `attach_external_ip.yaml` | DNAT (`{name}-dnat`) | — (target immutable in 4923) | action, VPC, site, dnatToIP, destinationAddress | 2nd run no-op if match; fail on drift or unverifiable fields | — |
| `playbook_osac_detach_external_ip.yml` | `detach_external_ip.yaml` | DNAT | — | — | — | Delete-if-absent; verify action/site/VPC/target and external address when available |
| `playbook_osac_move_network_attachment.yml` | `move_network_attachment.yaml` | V-Net port membership | port on from/to V-Net | — | Detach/attach idempotent; interrupted move recovers; post-conditions asserted | N/A |

## Controller helpers

| Role | Create behavior (4923) |
|------|------------------------|
| `netris.controller.vpc` | Skip if name exists and tenant matches; read detail when tenant is omitted; fail if tenant differs or remains unverifiable |
| `netris.controller.ipam` (allocation/subnet) | Skip if name exists and expected prefix/tenant/site/purpose/VPC fields match; fail if fields differ or are unverifiable |
| `netris.controller.vnet` | Skip if name exists and expected VPC/tenant/site fields match; read detail when list summary omits fields |
| `netris.controller.nat` | Skip if name exists and immutable NAT fields match; read detail for omitted fields; fail on drift or unverifiable fields |

## Failure diagnostics

Top-level Netris create, delete, attach, detach, and move jobs include the OSAC
resource kind/name, operation, failed Ansible task, HTTP status when present,
and a bounded sanitized error summary in AAP output. Cookie and common
credential values are redacted. OSAC resource status and Fulfillment error
propagation are unchanged by this work.
| `netris.controller.acl` | Unchanged name-skip (prune → OSAC-4888) |
