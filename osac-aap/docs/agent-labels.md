# CaaS Agent Binding Labels

Assisted Installer Agent CRs must carry specific Kubernetes labels for OSAC
cluster provisioning and scale-out to work. This document is the canonical
reference for those labels.

## Required Labels

### Import-Time Labels

These labels must be present on every Agent CR **at import time** for CaaS
cluster provisioning and scale-out. Missing any applicable label causes
provisioning to fail silently or stall indefinitely.

| Label Key | Operator Action | Required For |
|-----------|-----------------|--------------|
| `osac.openshift.io/resource_class` | Set by import path | Agent selection and NodePool matching |

### Controller-Managed Labels

These labels are set automatically by provisioning automation. Operators
should **not** set them manually.

| Label Key | Set By | Required For |
|-----------|--------|--------------|
| `osac.openshift.io/clusterorder` | `select_and_label_new_agents` | HyperShift agent-to-NodePool binding |

### `osac.openshift.io/resource_class`

Classifies the server's hardware type (e.g., `fc430`, `gpu-large`). Used by
`select_and_label_new_agents` to find available agents matching the
ClusterOrder's `nodeRequests[].resourceClass`, and by NodePool
`agentLabelSelector` to bind agents to the correct pool.

**Set by:** Each import path derives this label from its backend-specific source:
- BMH import (`playbook_osac_import_agents.yml`) - from `server.resource_class`
- BCM import (`playbook_osac_import_bcm_agents.yml`) - from BCM notes field
- NICo - from NICo instance type

**Defined in:** `group_vars/all/osac_common_labels.yaml` as `agent_resource_class_label`

**If missing:** `select_and_label_new_agents` finds zero matching agents.
Provisioning fails with _"0 agents were added to the cluster instead of N"_.
No NodePool binding ever happens.

### `osac.openshift.io/clusterorder`

Binds an agent to a specific ClusterOrder so that HyperShift's CAPI provider
can select it via the NodePool's `agentLabelSelector`.

**Set by:** The provisioning automation (`select_and_label_new_agents.yml`)
sets this label automatically when a cluster is created or scaled out.
Operators do **not** need to set this label manually.

**Defined in:** `group_vars/all/osac_common_labels.yaml` as `cluster_order_label`

**If missing:** HyperShift CAPI never selects the agent. The NodePool stays
at `NoSuitableAgents` indefinitely.

## Backend-Specific Required Labels

These labels are required only when using a specific network backend.

| Label Key | Operator Action | Backend | Required For |
|-----------|-----------------|---------|--------------|
| `netris.server/name` | Set via inventory | Netris | Netris server cluster creation |

### `netris.server/name`

Maps the agent to its corresponding Netris server entry. Only required when
using the Netris network backend. Not needed for NICo, ESI, or other backends.

**Set by:** The BMH import playbook (`playbook_osac_import_agents.yml`) from
`server.netris_server_name` in the inventory file.

**Defined in:** `group_vars/all/netris.yaml` as `netris_agent_server_name_label`

**If missing (Netris backend):** The Netris server cluster is created with an
empty server list. Workers are never added to the network segment.

**If missing (non-Netris backend):** No impact. This label is only checked by
the Netris integration.

## Optional Labels

These labels are set by specific import paths for metadata purposes. They are
not required for provisioning but may be useful for operations and debugging.

| Label Key | Set By | Purpose |
|-----------|--------|---------|
| `osac.openshift.io/rack` | BCM import | Rack position from BCM inventory |
| `osac.openshift.io/managed-by` | Import playbooks | Tracks which import playbook manages the BMH/Agent lifecycle |

> **Note:** `osac.openshift.io/host_uuid` is an **annotation**, not a label,
> despite the misleading Ansible variable name `agent_host_uuid_label` in
> `osac_common_labels.yaml`. It is set under `metadata.annotations` by the ESI
> filter plugin and read from `metadata.annotations` by the manage_agents and
> cluster_infra roles. To verify it, check `metadata.annotations`, not
> `metadata.labels`.

## Invalid Label Keys (Common Mistake)

Upstream assisted-install documentation uses an
`inventory.agent-install.openshift.io/extra-labels/` mechanism to set labels
on Agent CRs. **Do not use this mechanism for OSAC labels.** Label keys
containing multiple slashes are rejected by the Kubernetes API server.

### What fails

Keys like these are invalid Kubernetes label keys:

```text
inventory.agent-install.openshift.io/extra-labels/osac.openshift.io/resource_class=fc430
```

The Kubernetes API server rejects the resource update (on the BMH, InfraEnv,
or Agent CR - whichever carries the invalid key) because the label key
`inventory.agent-install.openshift.io/extra-labels/osac.openshift.io/resource_class`
contains more than one slash. A valid label key has at most one slash
separating a DNS prefix from the name segment (e.g., `osac.openshift.io/resource_class`).

### Symptoms

- Agent stays in `Pending` with no binding-related error
- NodePool shows `NoSuitableAgents`
- No clear error message pointing to the label key format

### Correct Approach

Apply import-time OSAC labels directly on the Agent CR, either through the
server inventory file (recommended) or by patching the Agent CR after
registration. Do not manually set controller-managed labels like `clusterorder`.

**Via inventory file (BMH import):**

```yaml
servers:
  - name: node001
    bmc_url: "redfish-virtualmedia+https://192.168.1.21:8000/redfish/v1/Systems/<uuid>"
    bmc_username: "<BMC_USERNAME>"
    bmc_password: "<BMC_PASSWORD>"
    boot_mac: "AA:BB:CC:DD:EE:01"
    netris_server_name: "server-01"
    resource_class: "fc430"
```

The import playbook reads `resource_class` and `netris_server_name` from the
inventory and applies them as properly-formatted Kubernetes labels. See
[Bare Metal Agent Import](import-agents.md) for the full inventory format.

**Manual patching (import-time labels only, for debugging or one-off):**

```bash
oc label agent/<agent-name> -n hardware-inventory \
  osac.openshift.io/resource_class=fc430 \
  netris.server/name=server-01
```

## Verifying Agent Labels

Check that an agent has the required labels before provisioning:

```bash
# List all agents with their resource class
oc get agent -n hardware-inventory \
  -L osac.openshift.io/resource_class \
  -L osac.openshift.io/clusterorder

# Check a specific agent's labels
oc get agent <agent-name> -n hardware-inventory -o json | \
  jq '.metadata.labels'

# Find agents available for a specific resource class (not yet bound)
oc get agent -n hardware-inventory \
  -l "osac.openshift.io/resource_class=fc430,!osac.openshift.io/clusterorder"
```

## Label Lifecycle

```text
1. Server imported (BMH/BCM/NICo playbook)
   -> Agent CR created with: resource_class, netris.server/name (if Netris)

2. ClusterOrder submitted, provisioning starts
   -> select_and_label_new_agents adds: clusterorder
   -> Agent approved for NodePool binding

3. Cluster deleted or agent removed
   -> detach_and_unlabel_all_removed_agents removes: clusterorder
   -> Agent returns to the available pool
```

## Backend-Specific Details

Each import backend has its own documentation covering the full agent import
workflow:

- [Bare Metal Agent Import (BMH)](import-agents.md) - BareMetalHost-based import with Ironic
- [BCM Inventory Integration](bcm-inventory-integration.md) - NVIDIA Base Command Manager import
- [NICo Integration](nico-integration.md) - NVIDIA NICo bare metal provisioning
- [Netris Integration](netris-integration.md) - Netris network backend (requires `netris.server/name`)
