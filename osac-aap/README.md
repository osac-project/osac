# OSAC Ansible Project

This repository contains the Ansible automation layer for
[OSAC (Open Sovereign AI Cloud)](https://github.com/osac-project).
It provides the playbooks, roles, and collections that provision and manage
infrastructure resources — networking, compute, bare-metal hosts, and
OpenShift clusters — when triggered by the
[osac-operator](https://github.com/osac-project/osac-operator) via
Ansible Automation Platform (AAP).

## How it fits into OSAC

OSAC is composed of three main components:

```
User ─► fulfillment-service (API) ─► osac-operator (watches K8s CRs) ─► AAP ─► osac-aap (this repo)
```

1. **[fulfillment-service](https://github.com/osac-project/fulfillment-service)** —
   The backend API that users interact with. It exposes available resource types
   (NetworkClasses, ComputeClasses, ClusterTemplates) that are auto-discovered
   from this repo.
2. **[osac-operator](https://github.com/osac-project/osac-operator)** —
   A Kubernetes operator that watches Custom Resources (VirtualNetwork, Subnet,
   SecurityGroup, ComputeInstance, ClusterOrder, etc.) and triggers AAP job
   templates to provision them.
3. **osac-aap (this repo)** — The Ansible automation that actually creates
   infrastructure. Each playbook receives the full K8s CR as its payload,
   reads an `implementation_strategy` annotation, and dynamically includes the
   matching template role to do the provisioning.

## What it provisions

### Networking

Playbooks for the full VirtualNetwork → Subnet → SecurityGroup lifecycle, with
pluggable backends:

| Implementation | Role | Backend |
|----------------|------|---------|
| `cudn_net` | ClusterUserDefinedNetwork (CUDN) on OpenShift | OVN-Kubernetes |
| `netris` | Netris Controller API | Netris |
| `agentless_net` | UID-keyed VirtualNetwork namespace plus Subnet VLAN, gateway, and per-VirtualNetwork DHCP | AgentlessNet |
| `openstack` | OpenStack Neutron | Neutron |

The `agentless_net` unified-resource path provisions and removes the
VirtualNetwork namespace, transit uplink, forwarding baseline, and child
Subnet VLAN/gateway/DHCP state. Subnet VLANs are added to configured Cumulus
trunks; the role never moves a host access port. SecurityGroup, ExternalIPPool,
ExternalIP, ExternalIPAttachment, NATGateway, workload attachment, and external
routing operations remain unsupported. A Ready Subnet means its VLAN, namespace
gateway interface, and per-VirtualNetwork DHCP service were verified; it does
not claim external reachability or workload attachment.

VirtualNetwork and Subnet provider state use schema v2 in a SQLite database at
`AGENTLESS_NET_STATE_FILE` (default `/etc/osac/agentless_network_state.sqlite3`).
The `virtual_networks` table stores each VirtualNetwork's immutable CR CIDR and
one UID-keyed `/31` transit link carved from that CIDR. The `subnets` table
stores each Kubernetes Subnet UID, its parent Kubernetes UID and tenant, the
global VLAN ID, and the saved gateway/DHCP state. Exact schema-v1 VirtualNetwork
databases migrate additively under the state lock; incompatible development
schemas with extra tables still fail closed.
For Subnet provisioning, osac-operator resolves the parent VirtualNetwork by its
Fulfillment UUID label in the Subnet namespace and sends its Fulfillment UUID,
Kubernetes UID, tenant, and phase in `osac_job_vars.parent_virtual_network`.
AgentlessNet validates that context against the Subnet before changing provider
state, so its AAP job does not need separate Kubernetes API credentials for the
parent UID lookup.
Both addresses are endpoints: the host uses the base address and acts as the
namespace default gateway; the namespace uses the next address. The `/31` link
reserves no network or broadcast address and is not an OSAC Subnet. This
implementation retains the merged VirtualNetwork `/31` and SQLite store. This
differs from the [accepted AgentlessNet design](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-3664-agentless-vlan-fabric-manager/design.md),
which specifies a separate `/30` transit pool and JSON state. The transit route
is intended to take precedence over the host's default route; existing
more-specific host routes that overlap the transit block are rejected.

Host uplink names use the reserved `osacvn` prefix. Two shared host `FORWARD`
rules isolate all such interfaces, keeping firewall rule count independent of
the number of VirtualNetworks.

The state-file flock protects short SQLite transactions. A bounded 256-file
lock pool serializes operations for each resource UID while provider commands
run; Subnet operations use the parent VirtualNetwork UID lock so siblings
cannot overwrite shared DHCP configuration. Hash collisions can serialize
unrelated UIDs. A short firewall lock protects the shared host `FORWARD` rules.
Failed creates retain their allocation for retry. Deletes retain Subnet rows
until DHCP, namespace-interface, trunk-membership, and VLAN cleanup is verified.
The module rejects unsupported database versions, corrupt state, and unsafe
owner or file modes; do not delete the state database to clear an error. The
unified allocator uses its SQLite VLAN pool and does not call the legacy JSON
VLAN allocator. Deployments sharing switches with legacy CaaS workflows must
configure disjoint VLAN pools.

Each AgentlessNet VirtualNetwork and Subnet job reads serialized YAML or JSON from
`AGENTLESS_NET_VN_INVENTORY` in the existing `network-fulfillment-ig` ConfigMap,
which the networking worker imports through `envFrom`. It must describe
exactly one authoritative host under `all.children.net_nodes.hosts`, with
`ansible_host` and `ansible_user`, plus an optional port (default `22`). Do not
put password fields anywhere in the ConfigMap. For example:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: network-fulfillment-ig
  namespace: <aap-worker-namespace>
data:
  AGENTLESS_NET_VN_INVENTORY: |
    all:
      children:
        net_nodes:
          hosts:
            network-node:
              ansible_host: <ssh-host>
              ansible_user: <ssh-user>
              ansible_port: 22
        switches:
          hosts:
            leaf-1:
              ansible_host: <cumulus-host>
              ansible_user: cumulus
              # Optional; omitted values default to cumulus.
              ansible_network_os: cumulus
              trunk_ports: [swp1, swp3]
```

VirtualNetwork-only jobs may omit the `switches` group. Subnet jobs require at
least one Cumulus host with a nonempty list of safe `trunk_ports`. The saved
Subnet VLAN is converged on each declared trunk; the role does not assign host
access ports. Before reserving a new Subnet, the role reads every switch's
`bridge -j vlan show` memberships and NVUE internal and L3-VNI reserved ranges.
The SQLite allocator chooses the lowest available VLAN from 1–4094 after
excluding those observed IDs and every saved allocation. Existing Subnet UIDs
reuse their saved VLAN, including allocations from the former 100–199 range.
The managed-node trunk defaults to `eth1`; SSH host, username, credentials,
physical switch trunks, and the optional SSH port remain infrastructure
configuration.

Configure SSH with an AAP machine credential, a mounted private-key path, or
`AGENTLESS_NET_SSH_PRIVATE_KEY` from the existing `network-fulfillment-ig`
Secret. When the Secret key is used, the role writes it to a mode-0600
temporary file on the worker and removes it after the operation, including
failures. Missing or invalid inventory fails before provider mutation.
Configure nonsecret values through the existing
[AAP instance-group configuration](../osac-installer/docs/network-backend.md#agentlessnet-virtualnetwork-baseline).
Tenant VirtualNetwork and Subnet create commands and API inputs remain
unchanged; VLAN bounds and DHCP-manager selection are internal provider details.

The managed node must be reachable by SSH and provide Python 3, `iproute2`,
`iptables` with conntrack support, `dnsmasq`, `ss`, and privilege escalation.
The role derives its DHCP manager on that node: it reuses the manager identified
by an existing VN-owned service file, otherwise selects running systemd when
available or the dedicated Supervisor daemon when reachable. Missing or
conflicting service-manager state fails before switch changes. No DHCP manager
selector is required in the ConfigMap. The role enables IPv4 forwarding and a
permit-all `FORWARD` policy inside the namespace. It does not change the host
forwarding sysctl; interface-scoped host drops keep traffic isolated between
VirtualNetworks. NAT, SecurityGroups, workload attachment, BGP, and full
external connectivity remain unsupported. A VirtualNetwork without a Ready
Subnet cannot make `DefaultNetworkingReady` true. Replacing a networking
manager requires draining and replacing its resources; changing an existing
VN's backend is unsupported.

The unified SQLite allocator assumes one authoritative allocator per fabric.
Switch discovery avoids VLANs already present on configured switches, but it
does not coordinate atomically with an independently running legacy CaaS JSON
allocator. The legacy CaaS pool and transit-address settings remain in their
existing workflows and do not set unified Subnet VLAN bounds. The SQLite state
path remains available as an advanced `AGENTLESS_NET_STATE_FILE` override.

The older `AGENTLESS_NET_IPAM_STATE_FILE` and
`AGENTLESS_NET_EXTERNAL_INTERFACE` settings remain separate for embedded CaaS
step workflows; they do not configure VirtualNetwork transit links.

Plus MetalLB-based ExternalIPPool / ExternalIP management (`metallb_l2`).

### Compute

- **`ocp_virt_vm`** — Provisions virtual machines on OpenShift Virtualization
  (KubeVirt), with configurable CPU, memory, storage, and network attachments.

### Bare Metal

- **`bm_host_agent_provisioning` / `bm_host_agent_deprovisioning`** — Agent-based
  bare-metal host lifecycle.
- **`bm_private_network` / `bm_host_private_network`** — Private network
  attachment for bare-metal hosts.
- Host lease management and bare-metal provisioning integrations.

### Clusters

- **`ocp_small`**, **`ocp_4_20_ai_maas`**, **`ocp_ci_small`** — OpenShift cluster
  templates with different sizes, authentication methods, and infrastructure
  backends (Netris, agentless_net).
- Multi-step workflow playbooks for hosted cluster create / delete / post-install.

### Local LVMS CSI StorageClasses

Single-node VMaaS development/CI deployments can opt in with
`csi_driver_install_lvms_storage_class_enabled: true`. Tenant Stage 2 creates
`osac-<tenant>-<tier>` using the OSAC CSI provisioner. The default remains
`false`, preserving direct TopoLVM classes; ClusterOrder/CaaS stays on its
legacy storage path.

The selector does not migrate existing classes or volumes. If a same-name
class already exists with incompatible provisioner, parameters, reclaim policy
or binding mode, the role fails before modifying any local classes and explains
the prerequisite. Kubernetes makes these fields immutable. For an existing
development installation, check its PVC/PV dependencies and explicitly remove
and recreate the class before opting in, or keep the legacy selector setting.
The role never deletes a class automatically; existing compatible CSI classes
remain idempotent.

## Architecture

```
osac-aap/
├── playbook_osac_*.yml                     # Top-level playbooks (one per AAP job template)
├── collections/ansible_collections/
│   ├── osac/
│   │   ├── service/                        # Shared utility roles (kubeconfig, finalizer, lease, wait_for, ...)
│   │   ├── templates/                      # Pluggable infrastructure roles with meta/osac.yaml
│   │   ├── workflows/                      # Multi-step orchestration (cluster, compute_instance)
│   │   └── config_as_code/                 # AAP configuration (job templates, inventories, credentials)
│   ├── massopencloud/                      # Bare-metal + MOC workflow steps
│   ├── netris/                             # Netris network backend steps
│   ├── nico/                               # NVIDIA NICo bare-metal backend steps
│   ├── dns/                                # DNS management
│   └── ci/                                 # CI-specific steps
├── vendor/                                 # Vendored Ansible collections
├── tests/                                  # Integration test suites
├── samples/                                # Example payloads
└── pyproject.toml                          # Python dependencies (uv)
```

### Key design pattern

Every template role declares its capabilities in `meta/osac.yaml`. Network
roles identify themselves via `fabric_manager`/`k8s_manager` (the dispatcher's
routing keys); other template types still use `implementation_strategy`:

```yaml
template_type: network
fabric_manager: cudn_net
capabilities:
  supports_ipv4: true
  supports_ipv6: true
  supports_dual_stack: true
```

Network roles declare their dispatcher identity for the operator. The installer
owns NetworkClass creation; `agentless_net` is selected through the installer
[overlay instructions](../osac-installer/docs/network-backend.md#agentlessnet-virtualnetwork-baseline)
and is not published as a ComputeClass. The generic
resource playbooks then include the selected role without changing the API.

## Pre-requisites

This project uses uv to install Ansible and other Python dependencies.

Install all the necessary dependencies by running:

```
uv sync --all-groups
```

Then you can run commands like this:

```
uv run ansible-playbook ...
```

Or you can activate the virtual environment so all commands are in your `$PATH` by default:

```
source .venv/bin/activate
```

## Re-vendor Ansible collections

This repository explicitly vendors the Ansible collections that are used as
dependencies, they are located in `vendor/` directory. You'll need
to re-vendor them after an update of `collections/requirements.yaml`.

First set your environment in order to be able to pull some of the collections
available only in Red Hat Automation Hub:

```
export ANSIBLE_GALAXY_SERVER_LIST=automation_hub,default
export ANSIBLE_GALAXY_SERVER_AUTOMATION_HUB_URL=https://console.redhat.com/api/automation-hub/content/published/
export ANSIBLE_GALAXY_SERVER_AUTOMATION_HUB_AUTH_URL=https://sso.redhat.com/auth/realms/redhat-external/protocol/openid-connect/token
export ANSIBLE_GALAXY_SERVER_DEFAULT_URL=https://galaxy.ansible.com/
export ANSIBLE_GALAXY_SERVER_AUTOMATION_HUB_TOKEN=<Get your token from https://console.redhat.com/ansible/automation-hub/token>
```

Then re-vendor the collections:

```
rm -rf vendor
ansible-galaxy collection install -r collections/requirements.yml
```
