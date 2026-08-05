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
| `openstack` | OpenStack Neutron | Neutron |

Plus MetalLB-based ExternalIPPool / ExternalIP management (`metallb_l2`).

### Compute

- **`ocp_virt_vm`** — Provisions virtual machines on OpenShift Virtualization
  (KubeVirt), with configurable CPU, memory, storage, and network attachments.

### Bare Metal

- **`bm_host_agent_provisioning` / `bm_host_agent_deprovisioning`** — Agent-based
  bare-metal host lifecycle.
- **`bm_private_network` / `bm_host_private_network`** — Private network
  attachment for bare-metal hosts.
- Host lease management and the
  [ESI (Elastic Secure Infrastructure)](https://esi.readthedocs.org) collection
  (`massopencloud.esi`) for bare-metal provisioning via OpenStack Ironic.

### Clusters

- **`ocp_small`**, **`ocp_4_20_ai_maas`**, **`ocp_ci_small`** — OpenShift cluster
  templates with different sizes, authentication methods, and infrastructure
  backends (ESI, NICo).
- Multi-step workflow playbooks for hosted cluster create / delete / post-install.

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
│   ├── massopencloud/                      # ESI bare-metal + MOC workflow steps
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

Every template role declares its capabilities in `meta/osac.yaml`:

```yaml
template_type: network
implementation_strategy: cudn_net
capabilities:
  supports_ipv4: true
  supports_ipv6: true
  supports_dual_stack: true
```

Running `playbook_osac_config_as_code.yml` publishes these as NetworkClasses /
ComputeClasses that the fulfillment-service auto-discovers, making the system
pluggable — new backends can be added without changing the operator or API.

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
