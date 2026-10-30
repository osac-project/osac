# Networking architecture decisions

Read this summary before planning, implementing, or reviewing networking work,
including IP, MAC, or network-attachment data on any provisioned resource.

## Authoritative sources and implementation status

This summary was checked against the accepted designs on `enhancement-proposals`
`main` on 2026-09-30 and updated with the managed OOB model proposed by OSAC-6121 on
2026-10-19. Read the applicable full designs before finalizing scope:

- [Unified networking, OSAC-1433](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1433-unified-networking/design.md)
- [Default networking, OSAC-1433](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1433-default-networking/design.md)
- [VMaaS networking, OSAC-1435](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1435-vmaas-networking/design.md)
- [CaaS networking, OSAC-1436](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1436-caas-networking/design.md)
- [BMaaS networking, OSAC-1437](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1437-bmaas-networking/design.md)
- [VMaaS multi-NIC capability proposal, OSAC-6121](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-6121-vmaas-multi-nic/design.md) (proposed extension; check acceptance status before treating it as shipped)

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
7. **Workload attachments:** Bare-metal instances and clusters retain at most
   one tenant attachment. VMaaS retains the single-NIC limit when the
   provider NetworkClass supports_multi_nic capability is false or unset.
   Proposed OSAC-6121 allows up to eight VM attachments when enabled, with
   exactly one primary on multi-attachment VMs. Its managed first-cut path
   permits the primary workload VirtualNetwork plus the deployment's one
   provider-managed shared OOB VirtualNetwork; arbitrary cross-VirtualNetwork
   requests remain unsupported. The provider creates the networks and VM, and
   the tenant consumes the VM. Attachment membership, Subnet references,
   primary selection, and OOB static address are fixed at creation. Existing
   SecurityGroup update behavior remains.
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
| ComputeInstance | network_attachments, ComputeNetworkAttachment: subnet, security groups, optional primary, optional static IPv4 for provider-managed OOB | False/unset capability: maximum one, implicitly primary. Proposed true capability: maximum eight, exactly one explicit primary for multiple entries; managed OOB uses the configured primary role and the one shared OOB role. |
| Cluster | `network_attachment`, ClusterNetworkAttachment: subnet, security groups | Singular; no primary field |
| BaremetalInstance | `network_attachments`, BareMetalNetworkAttachment: subnet, security groups, interface, optional primary | Maximum one entry; primary omitted or true accepted, false rejected |

VMaaS reports per-attachment Subnet and assigned IPv4 details through
`ComputeInstanceStatus.compute_network_attachment_statuses`, with `subnet_ref`
and `ip_address`. The proposed OSAC-6121 extension reports one entry per
attached Subnet and includes the provider-configured OOB address. KubeVirt
feedback population is tracked by
[OSAC-2479](https://redhat.atlassian.net/browse/OSAC-2479).

For ordinary tenant VM creation, an omitted attachment requests tenant defaults;
a supplied attachment resolves missing fields individually. The managed OOB flow
is provider-created and supplies its complete primary and OOB attachment set.
Consult the unified/default designs for validation of tenant ownership,
Subnet/SecurityGroup alignment, readiness, and BMaaS interface selection. Do not
interchange these message types.

For the proposed VMaaS extension, the provider-managed path has one primary
workload VirtualNetwork and may attach the deployment's single shared OOB
VirtualNetwork as a secondary. This is the only supported cross-VN case in the
increment. The primary role resolves to the CUDN plus EVPN path; the OOB role
resolves to a localnet K8s manager through NetworkClass network_types. The
provider creates both VirtualNetworks, their Subnets, and the ComputeInstance.
Tenants consume the VM and cannot choose another provider network.

The primary attachment controls VM placement, default route, and inbound
ExternalIPAttachment DNAT target (its discovered IP). NATGateway egress remains
scoped to the primary VirtualNetwork. The OOB attachment has a provider-reserved
static IPv4 address injected into the guest and does not replace the primary
default route. The operator's existing eight-entry ceiling is retained, and
all attachments are create-time-only.

The localnet K8s manager creates the Localnet CUDN and namespace-local NAD from
SubnetNetworkBinding. The VM reconciler consumes the generic binding contract
and does not branch on manager names. After the fabric manager configures the
switch VLAN, it publishes versioned output through the existing OSAC-4291
fabric-output ConfigMap pattern; the localnet manager consumes the validated
physical-network name, VLAN ID, and optional MTU before creating the CUDN. The
shared OOB CUDN selects only VM namespaces carrying the binding's OOB label.
The manager capability remains false until this handoff, NAD reachability,
static-IP template path, and per-attachment feedback are deployed and tested.
See the [OSAC-6121 design](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-6121-vmaas-multi-nic/design.md)
and the [OpenShift 4.19 multiple networks guide](https://docs.redhat.com/en/documentation/openshift_container_platform/4.19/html-single/multiple_networks/multiple_networks).
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
SecurityGroup policy, IPv6, or an upstream-router/BGP-peer configuration API.
The OSAC-1433 design's AAP input and attachment result contract specifies the
controller-supplied dependency snapshot and same-job-result validation.
AgentlessNet implements it under OSAC-6308. Netris must adopt the attachment
v1 contract before advertising it; that work is tracked by OSAC-6638. The
OSAC-6308 deployed E2E coverage uses a test-only static route and does not
validate upstream BGP advertisement.

## AgentlessNet NATGateway implementation status

OSAC-6202 adds the independent `natGatewayContractVersion: v1` manager
registration and controller/AAP contract. The NATGateway controller resolves
the current ready VirtualNetwork and allocated ExternalIP, submits their
identity, tenant and address/CIDR snapshot, then accepts only a matching
same-job `nat_gateway.ensure` or `nat_gateway.delete` result. Unversioned
managers keep their existing lifecycle. Netris adoption is tracked by OSAC-6694.

AgentlessNet stores NATGateway ownership in schema-v1 SQLite. AAP translates the
immutable VirtualNetwork IPv4 CIDR with a constant namespace rule pair: a saved
transit `/31` exemption followed by explicit SNAT on the namespace uplink.
Subnet lifecycle does not inspect or reconcile NATGateway state. Scoped host
forwarding and a host SNAT-bypass rule are installed before announcing the
ExternalIP `/32` through local FRR. Delete withdraws the route before removing
SNAT, the transit exemption, forwarding, bypass and matching reply-destination
conntrack entries; ExternalIP and VirtualNetwork cleanup remain blocked until
saved NATGateway state is removed. The current per-Subnet source behavior is
superseded by OSAC-6202's VN-wide SNAT plan; the accepted design amendment must
be published through the enhancement-proposals design workflow. Deployed BGP
convergence and packet delivery remain QE coverage under OSAC-5533.
