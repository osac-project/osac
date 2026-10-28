# Networking architecture decisions

Read this summary before planning, implementing, or reviewing networking work,
including IP, MAC, or network-attachment data on any provisioned resource.

## Authoritative sources and implementation status

This summary was checked against the accepted designs on `enhancement-proposals`
`main` on 2026-09-30. Read the applicable full designs before finalizing scope:

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

## Accepted contract

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
   permitted; they do not create a tenant Update/Patch API. The temporary
   OSAC-5679 exception exposes `SecurityGroups.Update` for replacing the full
   ingress and egress rule lists only; the public server fixes the update mask
   so the group ID, name, parent VirtualNetwork, metadata, and status remain
   unchanged. This is a scoped extension to the immutable target contract in
   the OSAC-1433 unified networking design. Provider-side rule reconciliation
   limitations remain tracked separately in OSAC-4888.
7. **One tenant attachment:** Each VM, bare-metal instance, or cluster has at most
   one tenant network attachment. Its networking is fixed at creation. Preserve
   the resource-specific message types and compatibility rules below.
8. **DHCP and discovery:** Workloads receive tenant-network addresses through
   DHCP. Discover assigned addresses through the service-specific status and
   feedback paths. BMaaS queries fabric DHCP leases by NIC MAC; CaaS delegates
   host provisioning, port moves, and IP discovery to BMaaS. Check the per-service
   designs for timing and API/ingress VIP allocation.
9. **Pluggable managers:** Managers register type/capabilities through ConfigMaps
   and supply Ansible roles. Adding a manager for existing capabilities does not
   require API changes. Capability types are fixed by the operator; a new
   capability requires an operator change.
10. **Default networking:** Tenant onboarding provisions a VirtualNetwork, IPv4
    Subnet, SecurityGroup, and NATGateway through the normal API/reconcile path.
    `DefaultNetworkingReady` gates tenant readiness. Missing attachment fields
    resolve from ready tenant defaults without replacing supplied values.
11. **Dependency readiness:** Fulfillment validates referenced resources are
    ready at creation. Internal/default/auto-provisioning flows observe the same
    readiness gates and create dependencies in order. Delete is allowed only
    when no active resource references the target, subject to the accepted
    auto-created resource cleanup rules.

## Resource-specific attachment contracts

| Resource | Attachment field and type | Accepted cardinality and primary behavior |
|----------|---------------------------|-------------------------------------------|
| ComputeInstance | `network_attachments`, ComputeNetworkAttachment: subnet, security groups | Plural field retained for compatibility, maximum one entry; no primary field |
| Cluster | `network_attachment`, ClusterNetworkAttachment: subnet, security groups | Singular; no primary field |
| BaremetalInstance | `network_attachments`, BareMetalNetworkAttachment: subnet, security groups, interface, optional primary | Maximum one entry; primary omitted or true accepted, false rejected |

VMaaS reports per-attachment Subnet and assigned IPv4 details through
`ComputeInstanceStatus.compute_network_attachment_statuses`, with `subnet_ref`
and `ip_address`. The current single-attachment boundary permits at most one
status entry; KubeVirt feedback population is tracked by
[OSAC-2479](https://redhat.atlassian.net/browse/OSAC-2479).

An omitted attachment requests tenant defaults; a supplied attachment resolves
missing fields individually. Consult the unified/default designs for validation
of tenant ownership, subnet/security-group VirtualNetwork alignment, readiness,
and BMaaS interface selection. Do not interchange these message types.

## Auto-created resource deletion

Use phased reconciliation with a wait between dependent removals:

1. Delete auto-created ExternalIPAttachments and wait for their finalizers.
2. Delete auto-created ExternalIPs and wait for their removal.
3. Complete parent cleanup and remove its finalizer.

Find auto-created attachments through `osac.openshift.io/auto-created` and
orphaned ExternalIPs through `osac.openshift.io/auto-created-for`, as specified
by the unified design. Manually created external resources and tenant-shared
default networking persist when a workload is deleted. Follow the full design's
reverse-reference guards when deleting shared resources or replacing NetworkClass.

## AgentlessNet ExternalIP implementation status

The OSAC-6307 AgentlessNet v1 implementation supplies the resolved ExternalIP
pool context to AAP. AAP reserves the UID-owned address and returns it in the
successful `external_ip.allocate` job artifact at `osac_result.data.address`.
The operator validates the current job result and pool, then publishes the
address in `ExternalIP.status.address`; AAP does not query the ExternalIPPool
or write the allocated-address annotation for this v1 flow. For both allocation
and release, it validates the result against the resource and pool snapshots in
the same AAP job's submitted `extra_vars`. Release continues to use the v1
identity envelope with empty `data`.

This implementation updates the allocation handoff in the accepted unified
networking design, which now documents the same-job artifact and pool snapshot
contract. Deployed AgentlessNet ExternalIP E2E validation is tracked with
OSAC-6308. Netris ExternalIP adoption follows the same contract: the OSAC-6376
Netris v1 implementation dispatches by the
`osac.openshift.io/externalip-contract-version` annotation. v1 resources run the
`osac.templates.netris_external_ip` module, which reserves a UID-owned canonical
IPv4 /32 nat reservation by Kubernetes UID and returns the exact `osac_result`
v1 artifact at `data.address`; release removes that reservation and reports
success only after confirmed absence. Netris v1 jobs do not query Kubernetes or
mutate ExternalIP annotations or status; the operator owns status, finalizers,
and capacity. Unversioned Netris resources retain the legacy name-owned
annotation handoff until its removal (OSAC-6384), and the operator chart
defaults the netris manager registration to the v1 contract while the manager
itself stays disabled until an operator enables it.

## AgentlessNet ExternalIPAttachment implementation status

The OSAC-6308 AgentlessNet v1 attachment flow is independently versioned by
`externalIPAttachmentContractVersion`; it does not inherit the ExternalIP
contract version. The ExternalIPAttachment controller resolves the ready
ExternalIP, BareMetalInstance, primary Subnet, and VirtualNetwork, then sends
their resource identities, tenant ownership, addresses, and readiness in the
same AAP job input as the attachment CR. AAP does not query Kubernetes or write
CR status. It reserves attachment state in the existing AgentlessNet SQLite
schema-v1 database, installs inbound whole-address DNAT and scoped forwarding,
and announces the ExternalIP `/32` through local FRR. The controller validates
the returned artifact against the submitted input and current resource snapshot
before marking the attachment Ready; detach uses saved provider state and keeps
finalizers until its artifact is accepted.

This first delivery supports BMaaS BareMetalInstance targets. It does not add
SecurityGroup policy, NATGateway SNAT, IPv6, or an upstream-router/BGP-peer
configuration API. The OSAC-1433 design's AAP input and attachment result
contract specifies the controller-supplied dependency snapshot and
same-job-result validation. AgentlessNet implements it under OSAC-6308. Netris
must adopt the attachment v1 contract before advertising it; that work is
tracked by OSAC-6638. The OSAC-6308 deployed E2E coverage uses a test-only static
route and does not validate upstream BGP advertisement.
