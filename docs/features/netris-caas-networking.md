# Netris CaaS Networking

This document describes how network traffic flows through the OSAC
infrastructure when using Netris as the network backend. It covers the
physical underlay (switches, softgates, uplinks), the logical overlay (VPCs,
NAT rules), and the packet-level path for each traffic direction.

## Netris Component Roles

The Netris fabric has three component types, each with a distinct role in the
data plane:

| Component | What it is | Data-plane role |
|-----------|-----------|-----------------|
| **Switch** (leaf/spine) | Physical network switch running a Netris agent | L2/L3 forwarding for all traffic (east-west and north-south), VXLAN encapsulation, EVPN, anycast gateway, VRF isolation, ACL enforcement. Does NOT perform NAT. |
| **SoftGate** | x86 server at the fabric edge running Netris gateway software | All NAT (SNAT/DNAT), L4 load balancing, BGP peering with upstream routers, default route injection into VPCs. The internet gateway. |
| **Controller** | Centralized management server | Pure control plane. Translates intent (VPC, NAT rule, server cluster) into device configs pushed to switches and softgates via gRPC. Not in the data path. |

## Physical Underlay

The physical fabric follows a spine-leaf Clos topology. SoftGates sit at the
top, connecting to the upstream router (internet) and the spine layer.
Servers connect to leaf switches via bonded VPC NICs for redundancy. The
Netris Controller manages all devices but is not in the data path.

```mermaid
%%{init: {'theme': 'dark', 'themeVariables': {'lineColor': '#ff9933'}, 'flowchart': {'defaultRenderer': 'elk'}}}%%
graph TB
    internet((Internet))
    upstream[Upstream Router]

    sg1[SoftGate 1 - NAT / BGP]
    sg2[SoftGate 2 - NAT / BGP]

    subgraph spine [Spine Layer]
        spine1[Spine 1]
        spine2[Spine 2]
    end

    subgraph leaf [Leaf Layer - VXLAN / EVPN / VRF]
        leaf1[Leaf 1]
        leaf2[Leaf 2]
    end

    subgraph srv1 [Server 1]
        bond1[Bond]
    end

    subgraph srv2 [Server 2]
        bond2[Bond]
    end

    subgraph srv3 [Server 3]
        bond3[Bond]
    end

    internet --- upstream
    upstream --- sg1
    upstream --- sg2
    sg1 --- spine1
    sg1 --- spine2
    sg2 --- spine1
    sg2 --- spine2
    spine1 --- leaf1
    spine1 --- leaf2
    spine2 --- leaf1
    spine2 --- leaf2
    leaf1 --- bond1
    leaf2 --- bond1
    leaf1 --- bond2
    leaf2 --- bond2
    leaf1 --- bond3
    leaf2 --- bond3
```

Each server has two VPC NICs bonded across both leaf switches. Spine
switches form a full mesh between all leafs. SoftGates peer with the
upstream router via BGP, advertising public IPs (/32) for DNAT and receiving
default routes for outbound traffic.

## Logical Overlay

The overlay runs on top of the physical fabric using VXLAN tunnels and VRFs
on the leaf switches. Each hosted cluster gets its own VPC (isolated VRF).
The SoftGate is the gateway between VPCs and the internet.

```mermaid
%%{init: {'theme': 'dark', 'themeVariables': {'lineColor': '#ff9933'}, 'flowchart': {'defaultRenderer': 'elk'}}}%%
graph TB
    internet((Internet))

    mgmt_snat[Mgmt SNAT]
    mgmt_api_dnat[Mgmt API DNAT :6443]
    hosted_api_dnat[Hosted Cluster API DNAT :6443]
    cluster_snat[Cluster SNAT]
    ingress_dnat[Hosted Cluster Ingress DNAT :80/:443]

    subgraph mgmt_vpc [Management VPC]
        mgmt_gw[Anycast Gateway]
        mgmt_metallb[MetalLB VIP]

        subgraph mgmt_cluster [Management Cluster]
            mce[MCE]
            hypershift[HyperShift]
            aap[AAP]
            mgmt_metallb_op[MetalLB Operator]
            storage_op[Storage Operator]
            hosted_apiserver[Hosted kube-apiserver]
        end

        mgmt_apiserver_lb[Mgmt kube-apiserver]
    end

    subgraph cluster_vpc [Cluster VPC - 192.168.101.0/24]
        gw[Anycast Gateway 192.168.101.1]
        metallb[MetalLB VIP 192.168.101.28]
        node1[Worker 1 - 192.168.101.2]
        node2[Worker 2 - 192.168.101.3]
        node3[Worker 3 - 192.168.101.4]
    end

    internet --- mgmt_snat
    internet --- mgmt_api_dnat
    internet --- hosted_api_dnat
    internet --- cluster_snat
    internet --- ingress_dnat

    mgmt_snat --- mgmt_gw
    mgmt_api_dnat --- mgmt_apiserver_lb
    hosted_api_dnat --- mgmt_metallb
    mgmt_metallb --- hosted_apiserver
    mgmt_gw --- mgmt_cluster

    ingress_dnat --- metallb
    cluster_snat --- gw
    gw --- node1
    gw --- node2
    gw --- node3
    metallb --- node1
    metallb --- node2
    metallb --- node3
```

The management VPC and each cluster VPC are separate VRFs on the same
physical switches. The SoftGate bridges them to the internet:

- **SNAT** on both VPCs gives their nodes outbound internet access
- **API DNAT** on the management VPC routes `:6443` to the kube-apiserver LB
  on the hosting cluster (because HyperShift runs the apiserver there)
- **Ingress DNAT** on the cluster VPC routes `:80/:443` to the MetalLB VIP
  on the worker nodes (because the ingress controller runs there)

Note the anycast gateway and MetalLB VIP serve different roles in the
cluster VPC. The **anycast gateway** (`192.168.101.1`) is the default route
for egress — worker nodes send all outbound traffic through it, and the
leaf switch forwards it to the SoftGate for SNAT. The **MetalLB VIP**
(`192.168.101.28`) handles ingress — it is an L2 address advertised via ARP
directly on a worker node's NIC, so DNAT'd packets from the SoftGate reach
it without passing through the gateway.

### How the components interact

**Switches** (spine and leaf layers) carry all traffic through the fabric —
both east-west (server-to-server) and north-south (server-to-SoftGate-to-internet).
They use VXLAN overlays with BGP EVPN for MAC/IP learning. Each VPC maps to
a VRF on the switches, providing routing table isolation. V-Nets within a
VPC get unique VXLAN Network Identifiers (VNIs). Leaf switches also run
anycast gateways — every leaf hosting a VPC advertises the same gateway IP
and MAC, so traffic is routed at the nearest leaf without tromboning.

**SoftGates** connect to the leaf switches and to the upstream router. They
are the only path to the internet. When a NAT rule is created for a VPC, the
SoftGate injects a default route (`0.0.0.0/0`) into that VPC's VRF on the
switches, steering all outbound traffic through itself. SoftGates also
advertise public IPs (DNAT destinations) as /32 routes to the upstream
router via BGP, attracting inbound traffic. All NAT translation happens on
the SoftGate — switches never rewrite packet headers.

**Controller** pushes configuration to switches and SoftGates via gRPC. If
the controller goes down, devices continue forwarding with their last-known
config — only new changes stop.

## Overlay: VPCs and Per-Cluster Resources

Each hosted cluster operates across two VPCs and a management network:

| Plane | Purpose | Scope |
|-------|---------|-------|
| **Management VPC** | Hosts the hosting cluster control plane (MCE, HyperShift, kube-apiserver pods). Shared across all hosted clusters. | One per site |
| **Per-cluster VPC** | Dedicated L3 network for each hosted cluster's worker nodes. Auto-created when a Netris server cluster is provisioned. Maps to a VRF + VNI on the switches. | One per hosted cluster |
| **Management network** | Out-of-band access to bare-metal servers via their management NIC (DHCP). Used for SSH-based NMState configuration. Not managed by Netris. | Flat, site-wide |

### Per-cluster resources created by Netris

| Resource | Name | Where it lives |
|----------|------|----------------|
| Server cluster | `<cluster-name>` | Controller config, pushed to switches |
| VPC (VRF + VNI) | `<cluster-name>` (auto-created) | Switch VRF tables |
| Anycast gateway | `192.168.101.1` on every leaf with cluster members | Switch SVIs |
| SNAT rule | `<cluster-name>-snat` | SoftGate NAT table |
| API DNAT rule | `<cluster-name>-api-dnat` | SoftGate NAT table |
| Ingress HTTP DNAT | `<cluster-name>-ingress-http-dnat` | SoftGate NAT table |
| Ingress HTTPS DNAT | `<cluster-name>-ingress-https-dnat` | SoftGate NAT table |
| DNS A records | `api.`, `api-int.`, `*.apps.` | Route53 |

Each cluster consumes **3 public IPs** from the Netris IPAM NAT pool: 1 for
SNAT, 1 for the API DNAT, and 1 for the ingress DNAT.

### Bare-metal server NIC layout

Each worker node has two types of network interfaces:

| Interface | Example | IP assignment | Connected to |
|-----------|---------|---------------|-------------|
| Management NIC | `eno1` | DHCP (unchanged from PXE boot) | Management network (not Netris-managed) |
| VPC NIC(s) | `ens1f0np0`, `ens1f1np1` | Static (first NIC only) | Leaf switch port (Netris-managed, VXLAN access port) |

The routing table on each node:

| Destination | Gateway | Interface | Metric | Effect |
|-------------|---------|-----------|--------|--------|
| `0.0.0.0/0` (default) | VPC anycast gateway (`192.168.101.1`) | VPC NIC | 50 | All data traffic goes through leaf switch into VPC |
| Management CIDR | Management gateway | Mgmt NIC | 100 | Bastion/controller traffic stays on mgmt network |

## Management Plane: NMState Configuration via SSH

Network configuration on live servers is applied through the management
network, not the data plane:

```
Ansible controller
  -> SSH to bastion (management network)
    -> SSH to server mgmt NIC (eno1, DHCP IP)
      -> nmstatectl apply: configures VPC NICs with static IPs and routes
```

This two-hop path ensures the management connection is never disrupted by
the VPC reconfiguration it triggers — the management NIC and routes are left
untouched.

## Network Manager Contract Alignment

The Netris role follows the network manager integration contract in
`enhancement-proposals` (`enhancements/OSAC-1433-network-manager-integration-contract-networking/design.md`):
OSAC sends exactly one `osac_job_vars` envelope per task, and the role
answers with a typed `osac_result` or a sanitized `osac_error`. Until the
shared dispatch lands, the role also accepts the previous raw-CR payload;
`tasks/_contract_input.yaml` is the only file that knows that shape.

| Task | Netris behavior |
|------|-----------------|
| `create_virtual_network` | VPC named after the VirtualNetwork, typed `backend_id` |
| `create_subnet` | V-Net per Subnet, gateway and DHCP from `ipam`, typed `network_segment` when `network_type: evpn` |
| `create_security_group` | Rules are validated exactly; Fabric ACL creation is a documented no-op |
| `apply_network_attachment` | Physical port move; overlay and SecurityGroup bind are documented no-ops |
| `query_dhcp_lease` | One typed `dhcp_lease`; only an active DHCP-sourced binding for the MAC counts, and zero or multiple matches retry |
| `reserve_cluster_ingress_ip` | One `/32` from `ipam.lb_vip_ipv4_cidrs`, typed `cluster_ingress.address` |
| ExternalIP family | Typed `external_ip.address` from the supplied snapshots |
| `create_fabric_domain` | Netris server cluster bound to the VirtualNetwork VPC, typed `backend_id` |

Every contract task publishes `osac_error {message, retryable}`. A
missing or malformed Netris setting (`NETRIS_CONTROLLER_URL`,
`NETRIS_USERNAME`, `NETRIS_PASSWORD`, `NETRIS_BM_PROVISIONING_VNET`,
`NETRIS_MGMT_VPC_ID`) is retryable because an administrator can repair
it; transport failures and HTTP 5xx/408/425/429 retry; other HTTP
statuses and input validation are final. Provider messages are bounded
and redacted, and `ansible_failed_result` is never published, logged, or
kept in a fact.

### SecurityGroup enforcement

Netris v1 ACLs are flat, VPC-scoped prefix rules: each rule needs the
Subnet CIDR it protects, and `create_security_group` runs before any Subnet
exists. Writing a VirtualNetwork-wide rule instead would allow more traffic
than the bound interfaces, which the contract forbids. The Fabric
SecurityGroup create and delete, and the bind in the physical attachment,
are therefore documented no-ops (contract section 4.10).

Consequences for this release:

- VM SecurityGroups are enforced by the Kubernetes Manager (`cudn_evpn`
  NetworkPolicy), which is the enforcement point for VM traffic.
- Bare-metal ports are not filtered by SecurityGroups. The ACL object per
  Subnet is the remaining work, and it belongs with the physical bind.
- The `vm` profile can still pass when the Kubernetes Manager enforces the
  rules; the `baremetal` and `sg` enforcement profiles stay pending.

### Cluster ingress address

`reserve_cluster_ingress_ip` reserves a tenant IPAM `/32` from
`ipam.lb_vip_ipv4_cidrs`, keyed by the Cluster API ID, so a retry returns
the same address and another Cluster on the Subnet gets a different one.
The reservation does not yet attach the address to a VirtualNetwork VPC or
program an EVPN advertisement, and the guest MetalLB L2 announcement path
has not been lab-validated. The `cluster` profile stays pending.

## Conformance Status

| Area | Status |
|------|--------|
| Contract unit tests (`osac-aap/tests/unit`, `-k netris`) | Passing |
| Fake Netris endpoint integration targets | Subnet and parent delete pass; new contract targets not yet added |
| `baremetal` profile | Pending, needs a Netris lab |
| `vm` profile with `netris` + `cudn_evpn` | Pending, needs the shared dispatch and a Netris lab |
| `cluster` profile | Pending, needs the ingress advertisement and a Netris lab |

The repository has no conformance runner for the contract testplan, so lab
runs are recorded manually with their configuration and evidence. A skipped
required run is not a pass.
