# OSAC Templates Ansible Collection

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

Base templates for OSAC (Open Sovereign AI Cloud) Fulfillment Services solution,
providing automated provisioning of OpenShift clusters and virtual machines on OpenShift
Virtualization.

## Overview

The `osac.templates` collection provides ready-to-use templates for deploying
infrastructure on OpenShift through the OSAC fulfillment service system. Templates are
defined as Ansible roles with metadata that enables self-service provisioning via the
OSAC [fulfillment service](https://github.com/osac-project/fulfillment-service).

## Features

- **Cluster Templates**: Deploy complete OpenShift clusters with customizable configurations
- **VM Templates**: Provision virtual machines on OpenShift Virtualization with cloud-init support
- **Storage Provider Roles**: Provision storage backends for OSAC tenants and manage K8s StorageClasses

## Installation

This collection is maintained as part of the [osac-aap](https://github.com/osac-project/osac) mono-repo (under `osac-aap/`) and is automatically available when working within that repository.

### For Development

When working within the `osac-aap/` directory of the mono-repo, the collection is automatically available from the `collections/` directory. No installation is required.

### For System-wide Installation

To install the collection system-wide from source:

```bash
git clone https://github.com/osac-project/osac
cd osac/osac-aap/collections/ansible_collections/osac/templates
ansible-galaxy collection build
ansible-galaxy collection install osac-templates-*.tar.gz
```

## Available Templates

### Cluster Templates

#### `ocp_small`
Minimal OpenShift cluster configuration.

**Default Configuration:**
- 2 nodes
- Resource class: fc430

**Required Parameters:**
- `pull_secret`: Red Hat pull secret for OpenShift installation
- `ssh_public_key`: SSH public key for node access

### VM Templates

#### `ocp_virt_vm`
Virtual machine template for OpenShift Virtualization: **Linux and Windows** guests use the same template ID. Linux is the default. Windows is selected when any of the following is true (in order): role var `guest_os_family: windows` (e.g. extra vars), annotation `osac.openshift.io/guest-os-family: windows` on the `ComputeInstance`, or `spec.image.sourceRef` contains the substring `containerdisks/windows` (an informal convention in some community Windows disk images — not a Microsoft-official image catalog). **Windows requires** `spec.image.sourceRef` to point at **your** Windows container disk (golden image or registry path you maintain); this template does not ship a default. Windows uses sysprep / CloudBase-Init paths, Hyper-V domain defaults, and matching delete cleanup.

**Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `guest_os_family` | string | `linux` | `linux` or `windows` — overrides inference when set before the role runs |
| `exposed_ports` | string | "22/tcp" (linux) / "3389/tcp" (windows) | Comma-separated ports (e.g., "22/tcp,80/tcp") |

The following are read from the `ComputeInstance` spec:

| Spec Field | Description |
|-----------|-------------|
| `spec.cores` | Number of CPU cores |
| `spec.memoryGiB` | Memory allocation in GiB |
| `spec.bootDisk.sizeGiB` | Root disk size in GiB |
| `spec.image.sourceRef` | Container disk image |
| `spec.runStrategy` | VM run strategy (Always, Halted, etc.) |
| `spec.sshKey` | SSH public key for VM access |
| `spec.userDataSecretRef.name` | Secret containing cloud-init user data |
| `spec.additionalDisks` | Additional data disks |

**Windows-specific behavior** (when guest OS resolves to `windows`): hostname via sysprep unattend.xml (15-character NetBIOS limit), enhanced Hyper-V enlightenments and clock policy, SATA sysprep CD-ROM, optional CloudBase-Init user-data secret, 900s ready wait.

## Usage

Templates are deployed through the OSAC fulfillment service, not directly via
ansible-playbook. The OSAC orchestrator handles template selection, parameter
validation, and lifecycle management.

## Template Development

### Creating a New Cluster Template

1. Create a new role directory under `roles/`:
   ```bash
   mkdir -p roles/my_cluster_template/{tasks,defaults,meta}
   ```

2. Define template metadata, spec defaults, and parameters in `roles/my_cluster_template/meta/osac.yaml`:
   ```yaml
   title: My Cluster Template
   description: Description of what this template provides

   template_type: cluster

   default_node_request:
   - resourceClass: fc430
     numberOfNodes: 2
   allowed_resource_classes: []

   spec_defaults:
     release_image: "quay.io/openshift-release-dev/ocp-release:4.17.0-multi"

   parameters:
     - name: my_param
       title: My Parameter
       description: What this parameter controls
       type: string
       required: true
   ```

3. Implement provisioning tasks in `roles/my_cluster_template/tasks/install.yaml`
4. Implement cleanup tasks in `roles/my_cluster_template/tasks/delete.yaml`

### Creating a New ComputeInstance Template

ComputeInstance templates define all metadata, spec defaults, and parameters in a
single file: `meta/osac.yaml`.

1. Create a new role directory under `roles/`:
   ```bash
   mkdir -p roles/my_vm_template/{tasks,meta}
   ```

2. Define template metadata, spec defaults, and parameters in `roles/my_vm_template/meta/osac.yaml`:
   ```yaml
   title: My VM Template
   description: Description of what this template provides
   template_type: compute_instance

   spec_defaults:
     # cores/memory_gib are reserved (removed); instance_type is the sole,
     # mandatory way to size a ComputeInstance. Set spec_defaults.instance_type
     # here to give the template a default, or omit it to require callers to
     # always pass instance_type explicitly.
     boot_disk:
       size_gib: 10
     image:
       source_type: registry
       source_ref: "quay.io/containerdisks/fedora:latest"
     run_strategy: "Always"

   parameters:
     - name: my_param
       title: My Parameter
       description: What this parameter controls
       type: string
       required: false
       default: "some_default"
       validation:
         pattern: '^[a-z]+$'
   ```

3. Implement provisioning tasks in `roles/my_vm_template/tasks/create.yaml`
4. Implement cleanup tasks in `roles/my_vm_template/tasks/delete.yaml`

See roles/ocp_virt_vm for more examples

### Creating a New Storage Provider Role

Storage provider roles use a tier-based dispatch model: osac-operator resolves storage tiers
from the Tier API and passes them via the `osac_job_vars.storage_tier_definitions`
extra_var, each tier declares its provider, and the service-layer dispatcher
(`osac.service.storage_provider`) groups tiers by provider and dispatches to each provider's
template role with the filtered tier subset.

1. Create role structure. The role **MUST** be named `{provider}_storage` (e.g.,
   `vast_storage`, `netapp_storage`). This naming convention is enforced by the
   dispatcher at `osac.service.storage_provider`, which constructs the role name
   dynamically: `osac.templates.{{ provider }}_storage`. Using a different naming
   pattern will cause a runtime role-not-found error.
   ```bash
   mkdir -p roles/my_provider_storage/{tasks,defaults,meta}
   ```

2. Define storage provider metadata in `roles/my_provider_storage/meta/osac.yaml`:
   ```yaml
   title: My Storage Provider
   template_type: storage_provider
   implementation_strategy: my_provider
   capabilities:
     supports_nfs: true
     provisioning_targets:
       - vmaas
   ```

3. Implement the three required action task files. Each receives `_provider_tiers`
   (filtered tier subset for this provider) and `_provisioning_target` via `vars:`
   from the dispatcher:
   - `tasks/setup.yaml` — provision storage backend resources per tier.
     Must set `storage_provider_tenant_config` output fact (dict).
   - `tasks/ensure_storage_class.yaml` — JIT K8s StorageClass provisioning per tier.
     Must set `storage_provider_storage_class_names` (list of SC names, one per tier).
     Must apply EP #26 labels (`osac.openshift.io/tenant`, `osac.openshift.io/storage-tier`).
   - `tasks/teardown.yaml` — cleanup in reverse dependency order.

4. Add the provider name to the hardcoded allowlist in
   `osac.service.storage_provider/tasks/main.yaml`. The allowlist is hardcoded
   (not a variable) to prevent override via extra_vars.

5. **Credential isolation:** Admin credentials (e.g., VAST VMS admin) must NEVER
   appear in tenant-namespace K8s Secrets. Provider roles must create per-tenant
   data-plane credentials and use only those in CSI Secrets. Admin credentials
   arrive via the `storage_provider_backend_connections` extra_var (resolved by
   osac-operator from the Tier API and keyed by `backend_id`) — there is no
   env-var or K8s Secret fallback for admin credentials.

6. **Provisioning targets:** Each provider handles both VMaaS and CaaS provisioning
   targets via the `_provisioning_target` parameter. Currently supported: `vmaas`.
   CaaS targets (`hcp_control_plane`, `hcp_worker_root`, `hcp_data_plane`) are
   defined in the enum but not yet implemented.

**CaaS provisioning targets:** `hcp_control_plane`, `hcp_worker_root`, and
`hcp_data_plane` are defined in the provisioning target enum but not yet implemented.
When implemented, `hcp_data_plane` will support provisioning multiple StorageClasses
into the guest HCP cluster (e.g., separate tiers for databases and general workloads).
Currently, all CaaS targets return an explicit "not yet implemented" error.

**Configuration:** Storage tiers arrive via the `osac_job_vars.storage_tier_definitions`
extra_var, populated by osac-operator from the Tier API:

```json
[
  {"name": "default", "protocol": "nfs", "provider": "vast", "backend_id": "be-001",
   "qos_limits": {"static_limits": {"max_reads_bw_mbps": 100, "max_writes_bw_mbps": 100}}},
  {"name": "high-performance", "protocol": "block", "provider": "vast", "backend_id": "be-001",
   "qos_limits": {"static_limits": {"max_reads_bw_mbps": 500, "max_writes_bw_mbps": 500}}, "quota_bytes": 500}
]
```

Backend connection details (endpoint, username, password) are resolved separately and
passed via the `storage_provider_backend_connections` extra_var, keyed by `backend_id`.

**Tier fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | DNS-label tier name (used in StorageClass naming) |
| `protocol` | yes | `nfs` or `block` |
| `provider` | yes | Provider name (e.g., `vast`) |
| `backend_id` | provider-dependent | Key into `storage_provider_backend_connections` for this tier's backend credentials (required by providers that resolve credentials this way, e.g. VAST) |
| `qos_policy` | n/a — derived, ignored if set by the caller | QoS policy name (creates STATIC mode policy on VMS), always derived from the tier name (`<name>-qos`). Any caller-supplied value is discarded, never used |
| `qos_limits` | no | Dict merged into QoS POST body (e.g., `static_limits`, `static_total_limits`). A tier opts out of `qos_policy` derivation unless `qos_limits.static_limits` sets a positive `max_reads_bw_mbps` or `max_writes_bw_mbps` |
| `quota_bytes` | no | Hard quota in bytes for the tier's view |

**QoS limits:** When a tier has a derived `qos_policy` (see above), the role creates a QoS
policy via REST API (`POST /api/qospolicies/`). The `qos_limits` dict is merged directly into
the POST body, so its keys must match VMS API fields. Real VMS rejects STATIC mode without at
least one limit — always include `qos_limits.static_limits` for a tier that should get QoS
enforcement.

**Dispatcher pattern:** `osac.service.storage_provider` validates inputs (tier list,
provider allowlist, protocol allowlist, provisioning target enum, max tier count) then
dispatches to `osac.templates.{provider}_storage` via a `_dispatch_provider.yaml`
wrapper (Ansible does not support `loop:` on `include_role`).

**VAST provider specifics:** The `vast_storage` role creates per-tenant VMS managers
(TENANT_ADMIN user type) with random passwords via REST API (`POST /api/managers/`).
The vendored `vastdata.vms` collection has no module for managers, roles, QoS policies,
or API tokens — these 4 resources use `ansible.builtin.uri` directly. Per-tenant
credentials are stored in a hub-cluster Secret and used in CSI Secrets. Password
rotation requires teardown + re-provisioning.

**Block encryption:** When `spec.blockEncryptionPassphrase` is provided in the Tenant CR
event payload, the passphrase is persisted to the hub Secret during `setup` and used by
`ensure_storage_class` to populate the CSI Secret's `passphrase` field and set
`hostEncryption: "true"` on block-protocol StorageClasses. On CSI Secret recreation,
`ensure_storage_class` reads the passphrase from the hub Secret so it survives the
original event. NFS encryption is managed at the VAST cluster level and is not
controlled per-StorageClass.

**Legacy StorageClass migration:** Tenants provisioned before the multi-tier refactor
have single-tier StorageClasses (e.g., `vast-nfs-{tenant}`). The refactored code creates
new multi-tier-named StorageClasses (e.g., `vast-nfs-{tenant}-default`) alongside legacy
ones. Existing PVCs continue to reference legacy names; new workloads use tier-specific
names.

## Architecture

Templates integrate with OSAC through a well-defined interface:

### Cluster Template Lifecycle
1. OSAC creates a dedicated namespace for the cluster
2. Template receives `cluster_order` and `template_parameters` variables
3. OSAC delegates to template id role for provisioning
4. OSAC monitors cluster status and provides kubeconfig access
5. On deletion, template cleans up all resources

### ComputeInstance Template Lifecycle
1. OSAC creates a dedicated namespace for the ComputeInstance
2. OSAC receives `compute_instance` and `template_parameters` variables
3. OSAC delegeates to template id role for provisioning
4. OSAC assigns floating IP and configures port forwarding
5. On deletion, template removes all resources in order

### Storage Provider Lifecycle
1. Operator creates Org CR, triggering `{{ aap_prefix }}-create-org` AAP job
2. `setup` provisions VAST resources per tier (tenant, views, quotas)
3. Per-tenant VAST user created with random password (admin creds never leave AAP)
4. Tenant config + per-tenant credentials persisted to hub-cluster K8s Secret
5. VIP pool is pre-configured globally by infra admins (not provisioned per-tenant)
6. At VM creation, JIT `ensure_storage_class` checks if all tiers' StorageClasses exist (short-circuit)
7. If absent, reads per-tenant creds from hub Secret, creates CSI Secret with tenant creds + per-tier StorageClasses
8. On tenant deletion (`{{ aap_prefix }}-delete-org`), `teardown` reads stored config, deletes per-tier resources + per-tenant user
9. Teardown validates provider type, handles legacy single-tier Secrets, gates hub Secret deletion on cleanup success

## Dependencies

### Runtime Dependencies
- `osac.service` collection (for cluster templates and storage provider dispatcher)
- `kubernetes.core` collection (for VM templates and storage K8s resource management)
- `vastdata.vms` collection v1.2.0 (for VAST storage provider, vendored)
- `osac.esi` collection (for floating IP management)

### Environment Requirements
- OpenShift 4.x cluster with cluster-admin access
- OpenShift Virtualization operator (for VM templates)
- OSAC orchestrator and fulfillment service

## Contributing

Contributions are welcome! Please ensure all templates:
- Include comprehensive `meta/osac.yaml` metadata
- Define parameters in `meta/osac.yaml`
- Implement both create and delete operations
- Follow Ansible best practices
- Include descriptive variable names and comments

## License

Apache License 2.0 - See [LICENSE](LICENSE) for details.

## Support

- **Issues**: https://github.com/osac-project/osac/issues
- **Documentation**: https://github.com/osac-project/osac
- **Repository**: https://github.com/osac-project/osac

## Author

Jason Kary <jkary@redhat.com>
