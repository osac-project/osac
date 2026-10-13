# Networking architecture decisions

Read this summary before planning, implementing, or reviewing networking work,
including IP, MAC, or network-attachment data on any provisioned resource.

## Authoritative sources and implementation status

This summary was checked against the accepted designs on `enhancement-proposals`
`main` on 2026-10-12. Read the applicable full designs before finalizing scope:

- [Unified networking, OSAC-1433](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1433-unified-networking/design.md)
- [Default networking, OSAC-1433](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1433-default-networking/design.md)
- [VMaaS networking, OSAC-1435](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1435-vmaas-networking/design.md)
- [CaaS networking, OSAC-1436](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1436-caas-networking/design.md)
- [BMaaS networking, OSAC-1437](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1437-bmaas-networking/design.md)

A bootstrap-created `enhancement-proposals/enhancements/` checkout can provide
these documents locally; verify its revision before relying on it. They remain
available through GitHub in a fresh OSAC clone.

These decisions describe the accepted target contract. They do not assert that
every constraint is enforced by the current code. The unified design explicitly
records legacy IPv6/dual-stack fields and allocation paths still present in the
implementation. Check [proto sources](../../proto/private/),
[operator APIs](../../osac-operator/api/), and the affected component's guidance
when assessing current behavior. Report implementation/design gaps explicitly.

When a feature exposes an IP, MAC, or attachment field, check whether an
accepted design already commits to it. For unrelated work, exclude overlapping
scope and cite that design. For an intentional revision or extension, name the
superseded or extended design explicitly.

## Proposed provider-contract revision (PR #357)

The [Unified Networking and Manager Integration revision](https://github.com/osac-project/enhancement-proposals/pull/357)
intentionally extends the accepted baseline below. Until merged, treat it as
desired implementation work, not existing code behavior. Its shared design
defines the cross-service architecture; the VMaaS, BMaaS, and CaaS extension
designs define their own workload fields, lifecycle gates, handoffs, and
teardown flows. The [provider guide](https://github.com/osac-project/osac/pull/1517)
explains the proposed manager onboarding workflow.

- Immutable NetworkDataModel/NetworkManager API registrations replace the
  ConfigMap registry. NetworkClass validates selection without a provider job.
- The fulfillment-service database is authoritative for NetworkData; its
  existing controller path projects each record as a NetworkData CRD in the
  networking hub. NetworkData owner scopes include NetworkClass, VirtualNetwork,
  and Subnet. A Fabric task returns each declared model/owner value that is
  applicable to its owner context and not already stored. A child operation may
  first produce a parent- or profile-owned value; later tasks reuse it.
  Concurrent tasks that can produce the same missing model/owner key must
  converge on one backend allocation and the same immutable value; conflicting
  NetworkData writes are rejected and do not replace the stored value.
- Stable fulfillment-service API IDs identify owners; hub projection UIDs
  fence dispatch, object writes, and original job results. Recreating a hub
  projection preserves allocations and operation history, while deleting and
  recreating the API resource creates a new owner identity. Projection
  retirement alone cannot authorize provider teardown.
- Durable operations track ambiguous launches, accepted results and completed
  persistence separately. Every AAP task is idempotent, including interruption.
- The unified design fixes serialized task inputs and task-specific results;
  provider qualification uses the same producer/consumer fixtures. Pool capacity
  follows its explicit eligible-address rule, independent of vendor allocation.
- Re-enabling after disabled operation uses a guarded maintenance transition:
  drain workloads, account for jobs/provider leftovers, reconcile networking
  readiness, then reopen workload admission. It does not migrate running workloads.

## Accepted contract

This section records the accepted baseline as of 2026-10-12. Its manager
registration mechanism in item 9 is superseded by the proposed provider
contract above; use that revision for the target modular manager design.

1. **Manager responsibilities:** The fabric manager owns physical networking,
   isolation, ACLs, address allocation, DNAT/SNAT, and inter-subnet routing.
   The optional K8s manager connects VM overlays to the fabric. Keep these
   responsibilities separate; check the full design for deployment profiles.
2. **Shared networking resources:** VirtualNetwork, Subnet, SecurityGroup,
   ExternalIP, ExternalIPAttachment, and NATGateway serve VMaaS, CaaS, and BMaaS.
   A Subnet can host VMs, bare-metal servers, and cluster nodes.
3. **Provider configuration:** One provider-owned networking hub and one
   deployment NetworkClass are supported. Tenants do not configure NetworkClass.
   This networking boundary does not define hub behavior for other OSAC areas.
4. **Connected, IPv4 deployments:** Disconnected networking, IPv6, and dual-stack
   are outside the accepted support boundary. Do not infer support from legacy
   fields. Manager registrations and allocation requests must follow the IPv4
   contract in the unified design.
5. **External addressing:** ExternalIP replaces PublicIP; an address external to
   a VirtualNetwork need not be internet-routable. Check migration compatibility
   before renaming existing APIs. ExternalIPAttachment provides inbound DNAT;
   NATGateway provides outbound SNAT.
6. **Create/read/delete operations:** Networking specifications and metadata are
   immutable after creation. Read means List/Get. Replacement requires delete
   and recreate under dependency guards. Internal status/condition updates are
   permitted; they do not create a tenant Update/Patch API.
7. **One tenant attachment:** Each VM, bare-metal instance, or cluster has at most
   one tenant network attachment. Its networking is fixed at creation. Preserve
   the resource-specific message types and compatibility rules below.
8. **DHCP and discovery:** Workloads receive tenant-network addresses through
   DHCP. Discover assigned addresses through the service-specific status and
   feedback paths. BMaaS queries fabric DHCP leases by NIC MAC; CaaS delegates
   host provisioning, port moves, and IP discovery to BMaaS. Check the per-service
   designs for timing and API/ingress VIP allocation.
9. **Manager modularity (accepted baseline):** Managers register type and
   capabilities through ConfigMaps and supply Ansible roles. Adding a manager
   for existing capabilities does not require API changes. Capability types
   are fixed by the operator; a new capability requires an operator change.
   This legacy registration mechanism is superseded by the proposed
   NetworkManager and NetworkDataModel resources described above.
10. **Default networking:** Tenant onboarding provisions a VirtualNetwork, IPv4
    Subnet, SecurityGroup, and NATGateway through the normal API/reconcile path.
    `DefaultNetworkingReady` gates tenant readiness. Missing attachment fields
    resolve from ready tenant defaults without replacing supplied values.
11. **Dependency readiness:** Fulfillment validates referenced resources are
    ready at creation. Internal/default/auto-provisioning flows observe the same
    readiness gates and create dependencies in order. Delete is allowed only
    when no active resource references the target, subject to the accepted
    auto-created resource cleanup rules.

## Service extension boundaries

Each workload extension defines its attachment field, message type,
cardinality, interface selection, request target, readiness/status mapping,
and teardown gate. The shared Unified Networking design defines the common
attachment meaning, default resolution, ownership, and cleanup dependencies.
The VMaaS, CaaS, and BMaaS designs are the source of truth for their exact
workload flows; do not copy those schemas or flows into new shared-networking
work.
