# Networking

OSAC gives every tenant private, isolated networks for its virtual machines
(VMaaS), bare-metal servers (BMaaS), and clusters (CaaS), and controlled
access to and from the outside world. Tenants work with one simple set of
networking resources; the cloud provider decides, once at installation, which
networking technology implements them.

This page is a plain-language overview for product and project managers. It
describes the accepted target design (the
[enhancement proposals](#further-reading)), not what a particular release
delivers. For what a given release supports, see
[Supported configurations](../guides/installation/customer-install-guide.md#9-supported-configurations)
in the installation guide. Terms are explained in the [glossary](#glossary).

## How to read this page

A networking setup is described by three choices the provider makes at
installation:

```
A networking setup = FABRIC MANAGER   x  KUBERNETES MANAGER  x  ENABLED SERVICES
                     (netris)            (cudn_evpn)            (VMaaS)
                     (agentless_net)     (none)                 (BMaaS)
                     (provider's own)                           (CaaS)
```

- **Fabric Manager** (required): the plug-in that configures the provider's
  physical network: switches, routing, NAT, external addresses, and bare-metal
  switch ports.
- **Kubernetes Manager** (optional): the OSAC plug-in that connects VMs on the
  OpenShift management cluster to the same networks. Needed only for VMs.
- **Enabled services**: which of VMaaS, BMaaS, and CaaS the deployment offers.

The tenant never sees these choices. The same tenant resources work the same
way on every supported combination.

## What a tenant gets

| Resource | What it is | Cloud analogy |
|---|---|---|
| VirtualNetwork | A private, isolated IPv4 network | VPC |
| Subnet | An address range inside a VirtualNetwork; VMs, servers, and cluster nodes can share one | Subnet |
| SecurityGroup | Firewall rules: which traffic may enter and leave a workload | Security group |
| ExternalIP | An address from a provider-owned pool, outside the tenant network | Elastic / floating IP |
| ExternalIPAttachment | Inbound access: maps an ExternalIP to one workload | Floating IP association |
| NATGateway | Outbound access: lets a whole VirtualNetwork reach outside through one ExternalIP | NAT gateway |

Key behaviors:

- **Ready on day one.** When a tenant is created, OSAC automatically creates a
  default VirtualNetwork, Subnet, SecurityGroup, and NATGateway. A tenant can
  start workloads without any networking setup. The provider chooses the
  default firewall rules once, for all tenants (the shipped default allows
  all outbound traffic and no inbound traffic).
- **Isolation.** Different VirtualNetworks, even of the same tenant, cannot
  reach each other privately, and may reuse the same address ranges.
- **Addresses are automatic.** Workloads get their address by DHCP; tenants
  never assign IP addresses by hand.
- **Inbound and outbound are separate.** A NATGateway gives outbound access
  only; inbound access needs an ExternalIP attached to the workload. A tenant
  can also ask OSAC to create and clean up that ExternalIP automatically when
  creating a workload.
- **"External" does not mean "internet".** An ExternalIP is reachable wherever
  the provider routes its pool: the internet, a corporate network, or a
  private backbone.
- **Create and delete, not edit.** Networking resources are fixed once created
  and are replaced by deleting and recreating them. The one exception is
  SecurityGroup rules, which can be replaced in place; the new rules reach
  every workload that uses the group without recreating it.
- **Firewall rules depend on the workload.** SecurityGroups are always
  enforced on VMs. On bare-metal servers and cluster workers they are
  enforced only if the Fabric Manager supports it; with Netris and
  AgentlessNet they are not, so those servers are isolated by their network
  but not filtered.
- **Safe ordering.** OSAC refuses to create something before what it depends
  on is ready, and refuses to delete something still in use, with a clear
  message.

## How it works

```
 Tenant (UI / CLI / API)
          |
          v
 +---------------------+     stores resources, checks rules,
 | Fulfillment service |     creates one object per resource
 +---------------------+
          |
          v
 +---------------------+     runs each step as an automation job
 |    OSAC operator    |---------------------------------+
 +---------------------+                                 |
          |                                              |
          v                                              v
 +----------------------------+          +-------------------------------+
 | Kubernetes Manager         |          | Fabric Manager                |
 | (OSAC: cudn_evpn)          |          | (Netris, AgentlessNet, or the |
 | VM networking on the       |          |  provider's own)              |
 | OpenShift hub              |          | switches, routing, NAT,       |
 +----------------------------+          | external IPs, server ports    |
                                         +-------------------------------+
```

- **One contract, many backends.** OSAC defines a fixed set of tasks a Fabric
  Manager must implement (create a network, attach a server, allocate an
  external address, and so on). Any backend that implements them plugs in
  without changes to OSAC. A provider can write its own Fabric Manager for
  networking equipment OSAC does not ship.
- **Defined by tests.** The design defines a conformance test suite with one
  test profile per service (VMs, bare metal, clusters). The provider runs it
  against its Fabric Manager and reports the result; a service's networking
  is supported on a backend only with a passing report for that service.
  Tests of traffic behavior itself (isolation, firewall enforcement) are a
  planned follow-up.
- **Chosen once.** The managers, the enabled services, the default network
  ranges and firewall rules, and how each Subnet's addresses are shared
  between VMs, servers, and cluster ingress are set at installation. Changing
  them later means deleting every tenant and workload first; switching
  provider networking on or off needs a reinstall.

## The backends

### Netris (Fabric Manager, shipped with OSAC)

For data centers whose switches are managed by a
[Netris](https://www.netris.io/) controller. Netris builds the tenant
networks on the switches using EVPN-VXLAN, a standard way to run many
isolated networks over one physical fabric. Dedicated Netris gateway servers
(SoftGates) at the edge of the fabric handle NAT and external addresses.

- Works for VMs (together with `cudn_evpn`), bare-metal servers, and clusters.
- Lets VMs and bare-metal servers share one Subnet.
- The only OSAC-shipped backend that supports Ethernet east-west domains
  ([FabricDomain](#high-performance-east-west-networking)).
- Firewall rules are not applied to bare-metal servers or cluster workers;
  they are applied to VMs by the Kubernetes Manager.
- The provider prepares the Netris controller, switches, gateways, and
  upstream routing before installing OSAC.

### AgentlessNet (Fabric Manager)

For providers with standard managed switches and no SDN controller. OSAC
configures the switches directly (Cumulus Linux is the validated platform)
and uses a Linux "net node" server for routing, DHCP, and NAT. Each Subnet
becomes a VLAN, and each VirtualNetwork becomes an isolated routing domain on
the net node. Nothing extra runs on the switches, hence "agentless".

- Designed for bare-metal servers.
- No VMs: it cannot pair with a Kubernetes Manager.
- Clusters and east-west domains are not covered by its design yet.
- Firewall rules are not enforced; traffic within a VirtualNetwork is
  allowed.
- Limited by the VLAN range: at most about 4,000 Subnets per fabric.

### The provider's own Fabric Manager

A provider can implement the OSAC Fabric Manager contract for its own
networking stack, package it as an automation collection, and register it.
It supports whatever services it passes the conformance suite for. To run VMs
it must support EVPN-VXLAN so it can pair with `cudn_evpn`.

### cudn_evpn (Kubernetes Manager, shipped with OSAC)

Connects VMs running on the OpenShift management cluster to the tenant's
networks. It uses the OVN-Kubernetes networking built into OpenShift to
extend each tenant Subnet from the switches onto the cluster nodes over
EVPN-VXLAN, so VMs, bare-metal servers, and the outside world see one
network.

- Required to run VMs.
- Applies SecurityGroups to VMs as Kubernetes network policies.
- Requires BGP peering between the cluster nodes and the fabric, and an
  OSAC release whose management cluster runs OpenShift 4.22 or later.
- Requires a Fabric Manager that supports EVPN-VXLAN (today, Netris).
- Once it is selected, no SecurityGroup in the deployment can have an
  ICMP-only rule (Kubernetes network policies cannot express one); tenants
  use the "all protocols" rule, which includes ICMP.

### Provider networking disabled

A provider can install OSAC without any provider networking. Tenants can
still create networking resources (so their tooling works), but OSAC
configures nothing on the network: there are no ExternalIPs, no NAT, and no
isolation guarantees. VMs use the cluster's default network, bare-metal
servers stay on the provider's provisioning network, and the provider makes
cluster endpoints reachable by its own means. This choice is made at
installation and cannot change later.

## What works with what

| | Netris + cudn_evpn | Netris alone | AgentlessNet | Provider's own | Networking disabled |
|---|---|---|---|---|---|
| VMs (VMaaS) | Yes | No | No | If EVPN + cudn_evpn | Yes, without tenant networking |
| Bare-metal servers (BMaaS) | Yes | Yes | Yes | If conformant | Yes, provisioning network only |
| Clusters (CaaS) | Yes | Yes | Not designed yet | If conformant | Yes, endpoints by provider |
| VMs and servers on one Subnet | Yes | n/a | n/a | If EVPN + cudn_evpn | No |
| ExternalIPs and NAT | Yes | Yes | Yes | If conformant | No |
| Firewall rules on VMs | Yes | n/a | n/a | If cudn_evpn | No |
| Firewall rules on bare metal and cluster workers | No | No | No | Provider-specific | No |
| East-west domains (FabricDomain) | Ethernet | Ethernet | No | If it declares it | No |

The AgentlessNet column reflects its design; its implementation is still
under review.

Two rules apply to every setup:

- CaaS requires BMaaS, because cluster workers are bare-metal servers.
- Enable every service the provider will offer, and select `cudn_evpn` if it
  may ever offer VMs, before the first tenant is created. Every tenant gets
  a default network, so adding BMaaS, CaaS, or VMs later is not supported
  without deleting all tenants.

## Networking per service

**Virtual machines.** A VM connects to one Subnet and gets an address from
the VM part of that Subnet's range. It keeps the same address across restarts
and live migration. Its SecurityGroups are enforced on the cluster. Inbound
access is through an ExternalIP, outbound through the VirtualNetwork's
NATGateway. In this phase of the design, VMs can use only the first Subnet of
each VirtualNetwork; bare-metal servers can use any Subnet.

**Bare-metal servers.** OSAC installs the server on the provider's
provisioning network, then moves the server's chosen network port onto the
tenant Subnet and reboots it so it picks up its tenant address. On deletion,
the port returns to the provisioning network before the server is cleaned
and released. The tenant can choose which data port to use; the management
port is never moved.

**Clusters.** Cluster workers are bare-metal servers on the tenant Subnet; the
control plane runs on the provider's management cluster. With provider
networking enabled, every cluster from a standard template automatically
gets two ExternalIPs, one for its API and one for its applications
(ingress), plus DNS names in the provider's configured zone
(`api.<cluster>.<domain>`, `api-int.<cluster>.<domain>`, and
`*.apps.<cluster>.<domain>`). A cluster's VirtualNetwork needs a NATGateway
so the workers can reach the control plane; the default VirtualNetwork
already has one. OSAC also adds a cluster SecurityGroup that allows traffic
from the cluster's Subnet, all outbound traffic, and web traffic (ports 80
and 443) to the applications; like other rules, it is not enforced on
bare-metal workers with Netris. Some provider-specific cluster templates
(for example NVIDIA NICo) keep their own endpoint setup.

## External connectivity

- The provider creates one or more **ExternalIP pools** from address ranges it
  owns and routes to its fabric. Pools are shared by all tenants; tenants see
  only how many addresses are left.
- A tenant picks a pool, never a specific address.
- **Inbound** = ExternalIP + ExternalIPAttachment to a VM, server, or cluster
  endpoint. All ports pass through; the workload's SecurityGroups decide what
  is allowed. On bare metal with Netris or AgentlessNet nothing filters it,
  so every port of the server is reachable wherever the pool is routed.
- **Outbound** = one NATGateway per VirtualNetwork, using one ExternalIP.
- ExternalIPs and NATGateways are the networking resources that are metered
  for billing.
- When the pools run out, new ExternalIPs, new tenants (whose default
  NATGateway needs one), new clusters, and VMs or servers that ask for
  automatic external access fail until the provider adds a pool. A tenant
  whose setup failed this way is deleted and recreated by the provider; it
  does not recover on its own.

## High-performance east-west networking

AI workloads need fast server-to-server links in addition to the normal
network. A **FabricDomain** groups a tenant's bare-metal servers into an
isolated domain on a high-performance fabric.

- **Ethernet (for example RoCE / Spectrum-X):** designed, using Netris.
- **InfiniBand and NVLink:** the API leaves room for them; not designed yet.
- Bare-metal servers only; not VMs and not cluster workers.
- The tenant lists the servers explicitly; changing the list means deleting
  and recreating the domain.

See [Ethernet FabricDomains](../guides/admin/fabric-domains.md).

## Out of scope

The following are outside the accepted networking design:

- IPv6 and dual-stack.
- Disconnected (air-gapped) deployments.
- More than one network interface per VM, server, or cluster; NIC bonding and
  VLAN trunking for tenants.
- Connecting two VirtualNetworks to each other (peering).
- Tenant-managed DNS zones, load balancers, and internet gateways.
- Networking quotas and bandwidth metering.
- DNS names for individual VMs and bare-metal servers (only clusters get
  names).
- Per-tenant default networks or firewall rules; defaults are the same for
  every tenant.
- Network technologies other than EVPN-VXLAN for VMs, and Kubernetes
  Managers written by providers.
- More than one management cluster (hub), or VMs on a separate cluster.
- Changing the managers, enabled services, or defaults while tenants exist.

## Glossary

- **BGP:** the routing protocol networks use to tell each other which
  addresses they can reach.
- **DHCP:** the standard way a machine receives its IP address
  automatically.
- **EVPN-VXLAN:** a standard way to run many isolated tenant networks over
  one shared physical network.
- **Hub (management cluster):** the provider's OpenShift cluster that runs
  OSAC itself and the tenants' VMs.
- **Kubernetes network policy:** a firewall rule applied by OpenShift to
  traffic of the workloads it runs.
- **NAT:** translating private addresses to an external one; outbound NAT
  lets private workloads reach outside, inbound NAT maps an external address
  to one workload.
- **SDN controller:** software that configures a whole fleet of network
  switches centrally (for example Netris).
- **VLAN:** a simple way to split one physical network into separate
  segments, limited to about 4,000 segments.

## Further reading

Designs (enhancement proposals):

- [Unified networking](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1433-unified-networking/design.md)
- [Default networking](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1433-default-networking/design.md)
- [Network manager integration contract](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1433-network-manager-integration-contract-networking/design.md)
- [VMaaS networking](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1435-vmaas-networking/design.md),
  [CaaS networking](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1436-caas-networking/design.md),
  [BMaaS networking](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1437-bmaas-networking/design.md)
- [Netris Fabric Manager](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-2434-netris-fabric-manager-networking/design.md),
  [AgentlessNet Fabric Manager](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-3664-agentless-vlan-fabric-manager/design.md),
  [CUDN EVPN Kubernetes Manager](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-4291-cudn-evpn-k8s-manager-phase-1-networking/design.md)
- [Multi-fabric east-west networking](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1382-multi-fabric-east-west-networking/design.md)

In this repository:

- [Installing with provider networking](../guides/installation/customer-install-guide.md#44-installing-with-provider-networking)
- [Tenant networking guide](../guides/developer/networking-guide.md)
- [Ethernet FabricDomains](../guides/admin/fabric-domains.md)
- [Networking architecture decisions](../agent-context/networking-decisions.md)
- [Lab: CUDN with BGP and VRF-Lite](setup-bpg-vrf-lite/README.md)
