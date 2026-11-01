# Networking architecture decisions

Read this summary before planning, implementing, or reviewing networking work,
including IP, MAC, or network-attachment data on any provisioned resource.

## Authoritative sources and implementation status

This summary was checked against the accepted designs on `enhancement-proposals`
`main` on 2026-10-28 (BMaaS notes aligned with PR #357 / OSAC-6817). Shared
networking doc refresh remains under OSAC-6812. Read the applicable full designs
before finalizing scope:

- [Unified networking, OSAC-1433](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1433-unified-networking/design.md)
- [Default networking, OSAC-1433](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1433-default-networking/design.md)
- [VMaaS networking, OSAC-1435](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1435-vmaas-networking/design.md)
- [CaaS networking, OSAC-1436](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1436-caas-networking/design.md)
- [BMaaS networking, OSAC-1437](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1437-bmaas-networking/design.md)

A bootstrap-created `enhancement-proposals/enhancements/` checkout can provide
these documents locally; verify its revision before relying on it. They remain
available through GitHub in a fresh OSAC clone.

These decisions describe the accepted target contract. They do not assert that
every constraint is enforced by the current code. Check [proto sources](../../proto/private/),
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
   feedback paths. For BMaaS, the Fabric Manager owns lease lookup; the networking
   controller runs `query_dhcp_lease` by the selected NIC MAC after the handoff
   reboot, and BMF mirrors the result without calling managers. CaaS delegates
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

## EVPN handoff implementation status

The EVPN handoff amends contract design §4.8–§4.10; this implementation
follows it, and carries `l3Vni` in the stored parent snapshot (pending the
matching EP amendment):

- Fabric `create_virtual_network` with `network_type: evpn` returns
  `{l3_vni: <int>, backend_id?}`. OSAC records it in
  `VirtualNetwork.status.l3Vni` and puts it in the stored `virtual_network`
  snapshot of every Subnet and NetworkAttachment task as `status.l3Vni`.
- Fabric `create_subnet` returns `network_segment: {type: evpn, l2_vni}`,
  recorded in `Subnet.status.networkSegment {type, l2Vni}` and passed to
  Kubernetes tasks as the launch key `network_segment`.
- OSAC rejects a VNI a live resource holds ("VNI already in use") and retries
  while only a deleting resource holds it.
- OVN-Kubernetes serves the VM gateway (`defaultGatewayIPs: [gateway_ipv4]`,
  symmetric IRB into the IP-VRF). The Fabric need not serve it; the Netris
  V-Net is also configured to serve it. `cudn_evpn` sets
  `infrastructureSubnets: [gateway/32, network+2/32]`, route targets
  `0:<l2_vni>` / `0:<l3_vni>` and MTU 1500.
- A VM on a `cudn_evpn` network can ping the node that hosts it, and nothing on
  the node serves it. OVN-Kubernetes treats every EVPN network as advertised and
  SNATs traffic toward the cluster node IPs to the network's management-port
  masquerade IP, so it enters the host through the management port, inside the
  network's VRF. Measured on the single-node lab: the node answers ICMP echo;
  TCP to listening 22 gets a RST from the node kernel (6443 and 10250 are
  refused the same way), which fits `net.ipv4.tcp_l3mdev_accept=0`: sockets in
  the default VRF do not match traffic that arrives on a VRF device; anything
  beyond the node stops at the VRF's `unreachable` default. So isolation of node
  services rests on that kernel default staying 0, not on an OSAC rule. UDP
  matches: with `net.ipv4.udp_l3mdev_accept=0`, dnsmasq listening on 53 did not
  answer and the node sent port-unreachable. Not measured: another node's
  address. No E2E checks any of this; add a check when needed.

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

## Implementation notes

The code implements the network manager integration contract
([OSAC-1433 contract](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1433-network-manager-integration-contract-networking/design.md))
as follows:

- **Generic network task:** every manager task (`create_subnet`,
  `apply_network_attachment`, `query_dhcp_lease`, `create_external_ip`, ...)
  runs as the AAP job template `<prefix>-<task with dashes>` (for example
  `osac-create-subnet`). All of them run
  [`playbook_osac_network_task.yml`](../../osac-aap/playbook_osac_network_task.yml),
  which calls the manager's `ansible_role` with `osac_job_vars` (`task`,
  `manager`, the resource snapshot) and returns `osac_result`/`osac_error`.
  The operator side is [`internal/networktask`](../../osac-operator/internal/networktask/doc.go).
  There is no dispatcher and no per-manager or per-kind playbook.
- **Manager registrations:** each manager registers through an immutable
  ConfigMap in the operator namespace labelled
  `osac.openshift.io/network-fabric-manager` or
  `osac.openshift.io/network-k8s-manager` (`name`, `ansible_role`, optional
  `network_types` and `capabilities`, which apply to both manager types). The
  operator discovers registrations and derives NetworkClass capabilities from
  them. Manager-specific registration content and provider behavior remain with
  the owning teams.
- **Capability types:** the operator fixes them. `eastWestEthernet` (Fabric
  Manager only) means the manager implements the FabricDomain tasks.
  `security_group_icmp` (any manager) means its `create_security_group` accepts
  `icmp` rules. The operator records `status.securityGroupIcmp` on the
  NetworkClass, true only when every resolved manager declares it, and
  fulfillment copies it to `status.security_group_icmp`. SecurityGroup create
  and rule update reject `PROTOCOL_ICMP` with InvalidArgument when the value is
  false, or unset with provider networking enabled. `cudn_evpn` does not declare
  it because NetworkPolicy cannot match ICMP; `all` includes ICMP. Netris
  declares it although its SecurityGroup tasks are no-ops. The value is derived,
  so it has no Helm value. A class whose default rules use `icmp` without the
  capability fails resolution.
- **DNS:** OSAC has no per-resource DNS. VMs get the cluster DNS that
  OVN-Kubernetes DHCP gives on the primary UDN. Bare metal on Netris gets an
  installation-level resolver (default `8.8.8.8`) from the Netris default DHCP
  option set, a provider install step, not OSAC code; bare metal without
  internet needs no DNS reachability. The settings `CUDN_EVPN_DNS_RESOLVERS`,
  `NETRIS_DNS_RESOLVERS`, `NETRIS_DNS_SNAT_CIDR` and the Netris per-VN DNS SNAT
  are removed.
- **NetworkClass and NetworkAttachment CRDs:** the fulfillment reconciler
  creates the single NetworkClass CR, which resolves its managers from the
  registrations. BMaaS (BMF) and VMaaS create a NetworkAttachment per
  workload attachment; the operator networking controller runs its tasks.
- **Status:** networking CRs have no `status.phase`. They report the `Ready`
  condition (reasons `Progressing`, `Succeeded`, `Failed`, `Updating`,
  `ProvisioningDisabled`), one `status.jobs` entry per task run (manager,
  attempt, state, AAP job ID, message) and `status.taskInput`, the input
  captured once at the first job and reused by retries and cleanup. Feedback
  controllers map `Ready` to the private state; there is no `DELETE_FAILED`
  (cleanup tasks retry until they succeed).
- **Provider networking disabled:** with
  `global.networking.provisioningEnabled: false` no networking job runs;
  the operator does not read manager registration ConfigMaps. It marks the
  NetworkClass Ready with reason `ProvisioningDisabled` and leaves
  `status.networkManagers`, `status.networkType` and `status.securityGroupIcmp`
  empty, and ICMP rules are stored. The configured
  manager names remain part of the NetworkClass spec but are not resolved;
  VirtualNetwork, Subnet, SecurityGroup, ExternalIPPool and FabricDomain
  become Ready with reason `ProvisioningDisabled`; workloads create no
  NetworkAttachment, and ExternalIP, ExternalIPAttachment and NATGateway are
  not offered. The fulfillment service refuses to start when the stored value
  changes.
- **CaaS workers:** worker BareMetalInstances live in tenant `system` and
  record their Cluster in status. The agent registration timeout (30 minutes)
  starts at the latest of the attempt start, the BMI network-ready time and
  the management publication, so network setup time does not count against it.

## AgentlessNet NATGateway implementation status

The NATGateway controller runs `create_nat_gateway` and `delete_nat_gateway`
through the generic network task for every manager. It stores the identity,
tenant and address/CIDR snapshot of the ready VirtualNetwork and allocated
ExternalIP as `nat_gateway_context`, and accepts a result only while they still
match. `osac.templates.agentless_net` reads that input without querying
Kubernetes. There is no per-manager `natGatewayContractVersion`.

AgentlessNet stores NATGateway ownership in schema-v1 SQLite. AAP translates the
immutable VirtualNetwork IPv4 CIDR with a constant namespace rule pair: a saved
transit `/31` exemption followed by explicit SNAT on the namespace uplink.
Subnet lifecycle does not inspect or reconcile NATGateway state. Scoped host
forwarding and a host SNAT-bypass rule are installed before announcing the
ExternalIP `/32` through local FRR. Delete withdraws the route before removing
SNAT, the transit exemption, forwarding, bypass and matching reply-destination
conntrack entries; ExternalIP and VirtualNetwork cleanup remain blocked until
saved NATGateway state is removed. The accepted design amendment for VN-wide
SNAT must be published through the enhancement-proposals design workflow.
Deployed BGP convergence and packet delivery remain QE coverage under OSAC-5533.

The other AgentlessNet tasks (VirtualNetwork, Subnet, ExternalIPPool, ExternalIP,
ExternalIPAttachment) still read the per-kind playbook inputs, and AgentlessNet
has no valid manager registration. NATGateway therefore cannot run on a deployed
AgentlessNet until the role adopts the contract.
