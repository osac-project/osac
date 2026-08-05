# BCM Inventory Integration

Import bare metal servers managed by NVIDIA Base Command Manager (BCM) as
Assisted Installer agents so they can be used for cluster-as-a-service via OSAC.

This integration was built and tested against **BCM 11** (Rocky Linux 9).

## Overview

BCM acts as the inventory source of truth for bare metal hardware. This
integration reads device information from BCM's JSON API, discovers BMC
system IDs by matching boot MAC addresses, and creates BareMetalHost CRs
so that Metal3/Ironic can boot the nodes with a discovery ISO. Once the nodes
register as Agents, the playbook labels them with resource class and rack
metadata from BCM.

```text
BCM (Lite Nodes)
      |
      | 1. Fetch inventory via JSON API (mTLS)
      v
playbook_osac_import_bcm_agents.yml
      |
      | 2. Query BMC endpoints, match systems by MAC
      | 3. Create BMC secrets + BareMetalHost CRs
      v
Metal3 / Ironic
      |
      | 4. Boot nodes with discovery ISO via virtual media
      v
Assisted Installer Agents
      |
      | 5. Label agents with resource_class, rack metadata
      v
OSAC (cluster provisioning)
```

## Device Model

Nodes are registered in BCM as **Lite Nodes** rather than Physical Nodes. This
means BCM provides inventory and monitoring without managing the OS — OpenShift
handles provisioning via RHCOS.

Each Lite Node has:
- **Hostname and MAC** for identification
- **BMC interface** pointing to the BMC endpoint (e.g., `rf0` for Redfish,
  `ipmi0` for IPMI, `drac0` for iDRAC, `ilo0` for iLO)
- **BMC credentials** (`bmcSettings`) for BMC authentication
- **Notes field** containing `resource_class=<value>` for hardware classification

## Supported BMC Types

| BCM Interface | BMC Type | Protocol | Discovery |
|---------------|----------|----------|-----------|
| `rf0` | Redfish | `redfish-virtualmedia+https://` | MAC-based system discovery |
| `drac0` | iDRAC | `idrac-virtualmedia+https://` | MAC-based system discovery |
| `ilo0` | iLO | `ilo5-virtualmedia+https://` | MAC-based system discovery |
| `ipmi0` | IPMI | `ipmi://` | Static URL (no discovery needed) |

Redfish-compatible types (redfish, drac, ilo) query the BMC's
`/redfish/v1/Systems/` and `/EthernetInterfaces` endpoints to find the
correct system UUID by matching boot MAC addresses. IPMI uses a simple
`ipmi://<bmc_ip>` URL with no discovery step.

## Prerequisites

- BCM head node with JSON API accessible on port 8081
- Client certificate and key for BCM mTLS authentication
- BMC endpoints accessible from the cluster
- OpenShift cluster with:
  - Baremetal Operator (BMO) configured with `watchAllNamespaces: true`
  - Assisted service installed
  - Agent namespace created (default: `hardware-inventory`)
  - Pull secret in the agent namespace

## Adding Nodes to BCM

Use the `bcm_add_lite_nodes.py` script to register bare metal machines as
Lite Nodes in BCM:

```bash
python3 hack/bcm_add_lite_nodes.py \
  --url https://bcm-head:8081 \
  --cert /path/to/cert.pem \
  --key /path/to/key.pem \
  --inventory samples/bcm_lite_nodes_inventory.yml
```

### Inventory File Format

```yaml
network: internalnet

nodes:
  - hostname: node001
    ip: 10.141.0.10
    mac: "52:54:00:4B:5D:71"
    resource_class: gpu-large
    bmc:
      ip: 10.141.0.1
      username: root
      password: "<from-vault-or-secret-manager>"
      interface_name: rf0

  - hostname: node002
    ip: 10.141.0.11
    mac: "52:54:00:B6:E8:33"
    resource_class: default
    bmc:
      ip: 10.141.0.1
      username: root
      password: "<from-vault-or-secret-manager>"
      interface_name: rf0
```

| Field | Description |
|-------|-------------|
| `network` | BCM network name for interfaces (default: `internalnet`) |
| `hostname` | Node hostname in BCM |
| `ip` | Node's own IP on the internal network |
| `mac` | Boot NIC MAC address |
| `resource_class` | Hardware classification (stored in BCM notes field) |
| `bmc.ip` | BMC endpoint address |
| `bmc.username` | BMC username |
| `bmc.password` | BMC password |
| `bmc.interface_name` | BMC interface name in BCM (determines BMC type, default: `rf0`) |

The script is idempotent — it skips nodes that already exist in BCM.

## Running the Import Playbook

### Locally

```bash
ansible-playbook playbook_osac_import_bcm_agents.yml \
  -e bcm_api_url=https://bcm-head:8081 \
  -e bcm_cert_path=/path/to/cert.pem \
  -e bcm_key_path=/path/to/key.pem \
  -e hosted_cluster_default_infraenv=infraenv
```

### Via AAP

The playbook is configured in AAP config-as-code with:
- Job template: `osac-import-bcm-agents`
- Schedule: every 10 minutes (disabled by default)
- Instance group: `cluster-fulfillment-ig` with `bcm-certs` secret mounted

To enable:

1. Create the `bcm-certs` secret in the AAP namespace with `tls.crt` and `tls.key`
2. Set `OSAC_IMPORT_BCM_AGENTS_ENABLED=true` in the `config-as-code-ig` secret
3. Set `BCM_API_URL` in the `cluster-fulfillment-ig` secret
4. (Lab/debug only) If BCM uses self-signed certificates, set
   `BCM_VALIDATE_CERTS=false` in the `cluster-fulfillment-ig` secret.
   **Not recommended for production** — configure proper CA certificates instead.
5. (Lab/debug only) If BMCs use self-signed certificates, set
   `BCM_DISABLE_BMC_CERT_VERIFICATION=true` in the `cluster-fulfillment-ig`
   secret. **Not recommended for production.**
6. Run config-as-code to apply the schedule

## Configuration

All configuration is in `group_vars/all/bcm.yaml`:

| Variable | Default | Description |
|----------|---------|-------------|
| `bcm_api_url` | (env: `BCM_API_URL`) | BCM API endpoint |
| `bcm_cert_path` | `/var/secrets/bcm-certs/tls.crt` | Client certificate path |
| `bcm_key_path` | `/var/secrets/bcm-certs/tls.key` | Client key path |
| `bcm_validate_certs` | `false` | Verify BCM TLS certificates |
| `bcm_agent_namespace` | `hardware-inventory` | Namespace for BMH/Agent CRs |
| `bcm_infraenv_name` | `infraenv` | InfraEnv CR name |
| `bcm_pull_secret_name` | `pull-secret` | Pull secret for InfraEnv |
| `bcm_disable_bmc_cert_verification` | `true` | Skip BMC TLS verification |
| `bcm_managed_by_label` | `osac.openshift.io/managed-by=import-bcm-agents` | Label selector for managed resources |
| `bcm_agent_resource_class_label` | `osac.openshift.io/resource_class` | Agent resource class label key |
| `bcm_agent_rack_label` | `osac.openshift.io/rack` | Agent rack label key |

## How It Works

The playbook runs in 5 phases:

### Phase 1: Fetch inventory from BCM

Queries the BCM JSON API via mTLS to get:
- All devices (filtered to `LiteNode` type)
- Rack information

Validates that BCM returns a list (not an error dict). For each Lite Node,
extracts hostname, boot MAC, BMC IP, BMC type, BMC credentials, resource
class (from notes field), and rack position. Hostnames are sanitized for
use as Kubernetes resource names.

### Phase 2: Discover BMC system paths

For Redfish-compatible BMC types (redfish, drac, ilo):
1. Queries `/redfish/v1/Systems/` on each BMC endpoint
2. Fetches the `/EthernetInterfaces` collection for each system
3. Fetches individual EthernetInterface resources to read the `MACAddress` property
4. Matches boot MACs to Redfish system paths

For IPMI BMC types, builds a simple `ipmi://<bmc_ip>` URL with no discovery.

BMC queries use `ignore_errors` so one unreachable BMC does not abort the
entire import. Failed queries are logged as warnings with the error message.

### Phase 3: Reconcile Kubernetes resources

- Ensures the InfraEnv CR exists
- Creates/updates BMC secrets with credentials from BCM
- Creates/updates BareMetalHost CRs with BMC URLs
- Servers with no BMC URL (unsupported type or failed discovery) are skipped
  with a warning

### Phase 4: Clean up stale resources

Finds BareMetalHost CRs labeled `managed-by=import-bcm-agents` that no longer
correspond to a BCM Lite Node, and deletes the Agent, BMH, and BMC secret.

Stale detection compares against all BCM server names (not just those with
resolved BMC URLs), so a temporary BMC outage does not cause deletion of
a server's resources.

### Phase 5: Wait for agents and apply labels

Waits for each node to boot the discovery ISO and register as an Agent (up to
15 minutes per node). Then labels agents with `resource_class` and `rack`
metadata from BCM. See [CaaS Agent Binding Labels](agent-labels.md) for the
full label reference.

## Removing Nodes

Remove the Lite Node from BCM (via `cmsh` or the API). On the next playbook
run, the stale BMH, Agent, and BMC secret will be automatically cleaned up.
