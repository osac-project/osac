# Netris Integration

This document covers the Netris network class integration for provisioning
hosted OpenShift clusters with bare-metal infrastructure managed by a Netris
controller.

## Overview

The Netris network class (`network_class: netris`) provisions cluster
networking through the Netris controller API. It manages:

- **Server clusters** — groups bare-metal servers into a Netris server cluster
  with a dedicated VPC
- **NAT rules** — SNAT for outbound internet access, DNAT for API and ingress
  endpoints
- **IPAM** — dynamically allocates public IPs from the Netris NAT pool
- **NMState** — configures VPC network interfaces on bare-metal servers via SSH
- **DNS** — creates A records for API and ingress endpoints (AWS Route53)
- **MetalLB** — configures ingress load balancing on the managed cluster

The project also supports an ESI network class (`network_class: esi`,
`network_steps_collection: osac.steps`) as an alternative backend
using the `osac.esi` collection.

## Architecture

### Pluggable Network Classes

The network backend is selected by two variables (set via environment or extra
vars):

| Variable | Netris value | Default (ESI) |
|----------|-------------|---------------|
| `network_class` | `netris` | `esi` |
| `network_steps_collection` | `netris.steps` | `osac.steps` |

The generic `osac.service` roles delegate to the network-specific
implementation at runtime:

```
osac.service.cluster_infra
  -> {{ network_steps_collection }}.cluster_infra (create/delete)

osac.service.external_access
  -> {{ network_steps_collection }}.external_access (create/delete)
```

### Ansible Collections

| Collection | Purpose |
|------------|---------|
| `netris.controller` | Low-level Netris API roles: `auth`, `server_cluster`, `server_cluster_template`, `nat`, `ipam`, `l4lb`, `vpc`, `vnet`, `acl` |
| `netris.steps` | High-level orchestration: `cluster_infra` and `external_access` roles that compose `netris.controller` roles |
| `osac.service` | Generic service roles that delegate to `netris.steps` |
| `osac.templates` | Template roles including `netris` (Netris network class for OSAC networking API) |

### Template Integration

The Netris network class is not tied to a specific template. It plugs into any
template that uses the `osac.service.cluster_infra` and
`osac.service.external_access` roles. The standard hosted cluster template is
`ocp_small` (`collections/ansible_collections/osac/templates/roles/ocp_small/`),
which orchestrates the following steps — each overridable via
`install_step_*_override` variables:

1. Pre-install hook (noop by default)
2. Create hosted cluster (`osac.service.hosted_cluster`)
3. Create cluster infrastructure (`osac.service.cluster_infra`)
4. Configure external access (`osac.service.external_access`)
5. Retrieve kubeconfig (`osac.service.retrieve_kubeconfig`)
6. Post-install hook (noop by default)

Steps 3 and 4 are where the network class takes effect — they delegate to the
`netris.steps` collection when `network_class: netris` is set.

### Cluster Create Flow

```
playbook_osac_create_hosted_cluster.yml
  |
  +-- pre-tasks: cluster_settings, extract_template_info, working namespace
  |
  +-- template install tasks (ocp_small):
      |
      +-- 1. Create HostedCluster + NodePool CRs
      |
      +-- 2. Cluster Infrastructure (netris.steps.cluster_infra.create)
      |       - Wait for agents from previous runs to be removed
      |       - Select and label available Agent CRs
      |       - Read server cluster template for NIC mapping
      |       - Create Netris server cluster (creates VPC)
      |       - Allocate 1 NAT IP from IPAM -> create SNAT rule
      |       - Create NMStateConfig CRs (static IPs on VPC NICs)
      |       - Patch InfraEnv nmStateConfigLabelSelector
      |       - Attach and approve agents
      |
      +-- 3. External Access (netris.steps.external_access.create)
      |       - Wait for kube-apiserver LoadBalancer to get internal IP
      |       - Allocate 2 NAT IPs from IPAM (API + ingress)
      |       - Create DNAT rule: public IP:6443 -> internal API
      |       - Create DNS A records (api, api-int, *.apps)
      |       - Wait for DNS propagation
      |       - Retrieve managed cluster kubeconfig
      |       - Wait for network cluster operator
      |       - Configure MetalLB on managed cluster
      |       - Create DNAT rules for ingress HTTP (:80) and HTTPS (:443)
      |
      +-- 4. Retrieve kubeconfig
      +-- 5. Wait for ClusterOperators
```

### Cluster Delete Flow

Reverse order:

1. Delete HostedCluster CR, wait for agents to detach
2. **External access cleanup** — delete DNAT rules (ingress HTTPS, HTTP, API),
   delete legacy L4LB/DNAT rules, delete DNS records
3. **Infrastructure cleanup** — delete NMStateConfig CRs, remove InfraEnv
   label selector, detach and unlabel agents, delete SNAT rule, delete server
   cluster, delete VPC

### SSH Topology

NMState configuration is applied to live servers via a two-hop SSH connection:

```
local -> bastion -> bare-metal server (management interface)
```

## Prerequisites

### Hosting Cluster

You need `KUBECONFIG` pointing to the hub OpenShift cluster where:

- **Multicluster Engine (MCE)** and **Hosted Control Planes (HyperShift)** are
  installed and configured.
- **Ansible Automation Platform (AAP)** is installed (for production use).
- A **storage provider** (e.g., OpenShift Data Foundation) is available with a
  **default StorageClass** configured.
- A **LoadBalancer ingress service** is available on the hosting cluster so that
  hosted cluster API servers (`kube-apiserver` LoadBalancer services) receive an
  IP. **MetalLB** is one option — if using MetalLB, configure an
  `IPAddressPool` CR (with `autoAssign: true`) and an `L2Advertisement` CR. The
  `IPAddressPool` IP range must be within the hosting cluster's VPC subnet.
- **Agent-based infrastructure** is set up with an `InfraEnv` in the agent
  namespace (defaults configured in `infrastructure.yaml`).
- **Bare-metal servers** must be booted with the InfraEnv discovery
  ISO so that corresponding **Agent CRs** are registered on the hub cluster.
  The servers must be registered in Netris and available (not bound to another
  cluster). Each Agent CR must be labeled with:
  - `osac.openshift.io/resource_class` — the server's resource class
  - `netris.server/name` — the corresponding Netris server name

  See [CaaS Agent Binding Labels](agent-labels.md) for the full label
  reference, including common mistakes to avoid.

### Netris Infrastructure

The following must be in place in your Netris environment before running the
playbooks:

- All bare-metal **servers**, **switches**, **interfaces** and **links** that
  will participate in hosted clusters must be registered in the Netris
  controller.
- The Netris environment must be healthy — the controller can reach the switch
  agents and vice versa. Verify connectivity in the Netris UI.
- Each server must have a **management interface** (configured via
  `mgmt_interface` in the resource class map) connected to a management
  network. This interface is used for SSH access to apply NMState configuration.
- All servers within the same **resource class** must have identical logical
  interface layouts (same NIC names and count). The server cluster template
  mapped in the resource class map defines the expected interface configuration.
- The hosting cluster servers must be deployed in a **dedicated VPC**. Configure
  its ID and name via `NETRIS_MGMT_VPC_ID` / `NETRIS_MGMT_VPC_NAME`.

### Netris Controller Access

A running Netris controller reachable from your workstation. The Netris site
must have:

- Available **servers** matching the Agent CR labels
- A **server cluster template** whose ID is mapped in `NETRIS_RESOURCE_CLASS_MAP`
  for the resource class you plan to use
- A **NAT IP pool** with at least 3 available IPs per cluster (1 for SNAT, 2
  for DNAT — API and ingress)
- A **management VPC** (configured via `NETRIS_MGMT_VPC_ID` /
  `NETRIS_MGMT_VPC_NAME`)

### DNS Provider Access

DNS A records are created for the cluster's API and ingress endpoints under the
domain configured in `EXTERNAL_ACCESS_BASE_DOMAIN`. You can use any domain and
DNS provider you control.

The default configuration uses AWS Route53. If using Route53, set
`AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` env vars with write access to
the hosted zone. If using a different provider, you'll need to adjust the
`external_access` role's DNS tasks accordingly.

### SSH Access

Netris supports non-trivial network topologies (e.g., spine-leaf HA
architectures) that require static networking configuration on the bare-metal
hosts. Since NMStateConfig CRs alone only take effect at boot time, the
playbook also applies the network configuration to live hosts via SSH through a
bastion hop:

```
local -> bastion -> bare-metal server (management interface)
```

Verify you can reach the bastion and that the bastion can reach your servers'
management IPs.

## Configuration

### Environment Variables

All Netris-related settings can be configured via environment variables. These
are resolved in `group_vars/all/configuration.yaml` and `group_vars/all/netris.yaml`.

#### Netris Controller

| Variable | Description | Required |
|----------|-------------|----------|
| `NETRIS_CONTROLLER_URL` | Netris controller API URL | Yes |
| `NETRIS_USERNAME` | Netris API username | Yes |
| `NETRIS_PASSWORD` | Netris API password | Yes |
| `NETRIS_SITE_ID` | Netris site ID (integer) | Yes |
| `NETRIS_TENANT_ID` | Netris tenant ID (integer) | Yes |
| `NETRIS_TENANT_NAME` | Netris tenant name | Yes |
| `NETRIS_MGMT_VPC_ID` | Management VPC ID (for API DNAT rules) | Yes |
| `NETRIS_MGMT_VPC_NAME` | Management VPC name | No |

#### Resource Class Map

| Variable | Description | Required |
|----------|-------------|----------|
| `NETRIS_RESOURCE_CLASS_MAP` | JSON dict mapping resource class names to configuration | Yes |

Each key is a resource class name (e.g., `fc430`); the value is an object with:

```json
{
  "fc430": {
    "server_cluster_template_id": 1,
    "mgmt_interface": "eno1",
    "vpc_interfaces": ["ens1f0np0", "ens1f1np1"]
  }
}
```

- `server_cluster_template_id` — Netris server cluster template ID
- `mgmt_interface` — management NIC name as seen by the OS
- `vpc_interfaces` — list of data-plane / VPC NIC names

#### SSH Access

| Variable | Description | Required |
|----------|-------------|----------|
| `SERVER_SSH_BASTION_HOST` | Bastion hostname/IP | Yes |
| `SERVER_SSH_BASTION_USER` | Bastion SSH username | Yes |
| `SERVER_SSH_BASTION_KEY` | Bastion SSH private key content (written to file at runtime) | Yes |
| `SERVER_SSH_KEY` | Server SSH private key content (written to file at runtime) | Yes |
| `SERVER_MGMT_ROUTE_DESTINATION` | Management route destination CIDR | Yes |
| `SERVER_MGMT_ROUTE_GATEWAY` | Management route gateway IP | Yes |

#### Network and DNS

| Variable | Description | Default |
|----------|-------------|---------|
| `NETWORK_CLASS` | Network backend to use | `esi` |
| `NETWORK_STEPS_COLLECTION` | Ansible collection for network steps | `osac.steps` |
| `EXTERNAL_ACCESS_BASE_DOMAIN` | Base domain for DNS records | `box.massopen.cloud` |
| `EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS` | Comma-separated list of allowed domains | `box.massopen.cloud` |
| `AWS_ACCESS_KEY_ID` | AWS credentials for Route53 | Yes (if using Route53) |
| `AWS_SECRET_ACCESS_KEY` | AWS credentials for Route53 | Yes (if using Route53) |

#### Cluster Settings

| Variable | Description | Default |
|----------|-------------|---------|
| `KUBECONFIG` | Path to hub cluster kubeconfig | Required |
| `HOSTED_CLUSTER_BASE_DOMAIN` | Base domain for hosted clusters | `box.massopen.cloud` |
| `HOSTED_CLUSTER_CONTROLLER_AVAILABILITY_POLICY` | HA policy for control plane | `HighlyAvailable` |
| `HOSTED_CLUSTER_INFRASTRUCTURE_AVAILABILITY_POLICY` | HA policy for infrastructure | `HighlyAvailable` |

### Group Variables (`group_vars/all/`)

| File | What it controls |
|------|-----------------|
| `configuration.yaml` | Environment-driven defaults: Netris connection, network class, DNS domains, SSH config, availability policies |
| `netris.yaml` | Static Netris defaults: agent server name label, SSH key paths, VPC gateway, MetalLB ingress IP |
| `infrastructure.yaml` | Agent namespace, InfraEnv name, default credentials Secret |
| `osac_common_labels.yaml` | K8s label and finalizer names used by OSAC |
| `manage_agents.yaml` | Idle agents network name |

### Netris Resource Naming

All Netris resources created by the playbooks follow this naming convention:

| Resource | Name |
|----------|------|
| Server cluster | `<cluster-name>` |
| VPC | `<cluster-name>` (created by server cluster) |
| SNAT rule | `<cluster-name>-snat` |
| API DNAT rule | `<cluster-name>-api-dnat` |
| Ingress HTTP DNAT | `<cluster-name>-ingress-http-dnat` |
| Ingress HTTPS DNAT | `<cluster-name>-ingress-https-dnat` |
| DNS records | `api.<cluster-name>.<domain>`, `api-int.<cluster-name>.<domain>`, `*.apps.<cluster-name>.<domain>` |

## Netris Network Class (`netris`)

In addition to the CaaS cluster networking above, the `netris` network class
implements the OSAC Networking API by translating its resources into Netris
primitives:

| OSAC Resource | Netris Primitive | API |
|---------------|-----------------|-----|
| VirtualNetwork | VPC + V-Net | `POST /api/v2/vpc`, `POST /api/v2/vnet` |
| Subnet | IPAM allocation + subnet | `POST /api/v2/ipam/allocation`, `POST /api/v2/ipam/subnet` |
| SecurityGroup | ACL rules | `POST /api/acl` (v1 API) |
| ExternalIPPool | IPAM allocation + subnet (purpose=nat) | `POST /api/v2/ipam/allocation`, `POST /api/v2/ipam/subnet` |
| ExternalIP | IPAM IP allocation | Read from existing IPAM pool |

The template role is at `collections/ansible_collections/osac/templates/roles/netris/`.
It is auto-discovered by the `publish_templates` playbook via `meta/osac.yaml`
and registered as a NetworkClass in the fulfillment service.

### Networking Instance Group

The networking operations jobs run on the `networking-operations-ig` instance
group, which mounts the `network-fulfillment-ig` ConfigMap and Secret for Netris
credentials:

| Resource | Keys |
|----------|------|
| ConfigMap `network-fulfillment-ig` | `NETRIS_CONTROLLER_URL`, `NETRIS_USERNAME`, `NETRIS_SITE_ID`, `NETRIS_TENANT_ID`, `NETRIS_TENANT_NAME` |
| Secret `network-fulfillment-ig` | `NETRIS_PASSWORD` |
