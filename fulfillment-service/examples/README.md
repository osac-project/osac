# Examples

Example YAML files for creating OSAC resources via `osac create -f`. Each
subdirectory contains resources of a specific kind that can be loaded into the
fulfillment service.

## Authentication

All examples use the **private API**. Log in with private API access first:

```bash
osac login --private ...
```

Or generate a token and log in for development:

```bash
export TOKEN=$(kubectl create token -n osac client)
osac login --private --token "$TOKEN" ADDRESS
```

## Catalog Items

Directory: [`catalog-items/`](catalog-items/)

Example catalog items for clusters and VMs. Load them with:

```bash
# Create a single catalog item
osac create -f examples/catalog-items/simple-ocp-4-17-cluster.yaml

# Prepare the shared resources used by linux-vm.yaml, in this order
osac --tenant shared create -f examples/catalog-items/00-shared-fedora-disk-image.yaml
osac --tenant shared create -f examples/catalog-items/01-shared-u1-small-instance-type.yaml

# Select your tenant, then create its catalog item
osac tenant YOUR_TENANT
osac create -f examples/catalog-items/linux-vm.yaml
```

**Note:** `osac create -f` is **not idempotent** — it will fail if the resource
already exists.

### Using catalog items

After loading catalog items, use `--catalog-item` to create resources from them:

```bash
# Create a cluster (CIDR fields have defaults, so no extra flags needed)
osac create cluster --catalog-item simple-ocp-4-17-cluster

# Create a 10 GiB VM from the Linux catalog item; use the ID printed at creation
osac create computeinstance --catalog-item CATALOG_ITEM_ID \
  --name my-linux-vm --boot-disk-size 10 \
  --boot-disk-storage-tier TIER_NAME \
  --network-attachment subnet=SUBNET_ID
```

The Linux VM catalog item does not select a StorageTier. List available tiers with
`osac get storagetiers`, then supply an active one with `--boot-disk-storage-tier`
when creating a VM. The installer registers `local` when LVMS is enabled.

**Note:** These VM examples leave `network_attachments` unset because valid
subnets depend on the tenant. Provide at least one `--network-attachment` when
creating a compute instance from them unless the tenant has a ready default
subnet.

### Cluster catalog items

| Example | Template |
|---|---|
| `simple-ocp-4-17-cluster.yaml` — Simple OpenShift 4.17 (fc430) | `osac.templates.ocp_4_17_small` |
| `ocp-4-20-nico-baremetal-cluster.yaml` — OCP 4.20 NICo bare metal | `osac.templates.ocp_4_20_small_nico` |
| `ocp-4-20-ai-maas-cluster.yaml` — OpenShift AI 4.20 with MaaS | `osac.templates.ocp_4_20_ai_maas` |
| `ocp-4-20-openshift-ai-cluster.yaml` — OpenShift AI 4.20 (RHOAI + GPU) | `osac.templates.ocp_4_20_openshift_ai` |
| `ocp-ci-cluster.yaml` — CI OpenShift cluster (unpublished) | `osac.templates.ocp_ci_small` |

### Compute instance (VM) catalog items

All VM examples use `osac.templates.ocp_virt_vm` (single template for Linux
and Windows).

| Example | Description |
|---|---|
| `linux-vm.yaml` | General-purpose Linux VM |
| `linux-vm-gpu.yaml` | GPU-enabled Linux VM |
| `windows-server-vm.yaml` | Windows Server VM |
| `windows-11-vm.yaml` | Windows 11 VM |

### Prerequisites

Each catalog item references a **template** published by the AAP
`osac-publish-templates` job during installation. Templates are defined as
roles in `osac-aap/collections/ansible_collections/osac/templates/roles/`.
List available templates with `osac get clustertemplates` or
`osac get computeinstancetemplates`.

The VM catalog items reference **instance types** (e.g., `u1-small`) that
must exist before the catalog items can be created. The shared `u1-small`
definition for `linux-vm.yaml` is in
[`01-shared-u1-small-instance-type.yaml`](catalog-items/01-shared-u1-small-instance-type.yaml).
Other examples may need different instance types.

The VM catalog items also reference **disk images** (e.g., `fedora`) by name.
DiskImage resources must exist before their catalog items can be created —
they map a human-readable name to a container disk OCI reference. The shared
`fedora` definition for `linux-vm.yaml` is in
[`00-shared-fedora-disk-image.yaml`](catalog-items/00-shared-fedora-disk-image.yaml).
Other examples may need different disk images.

Catalog items also assume the relevant **artifacts** are already available:

- **Container disk images** — OCI artifacts referenced by DiskImage resources
  (e.g., `quay.io/containerdisks/fedora:41`)
- **OpenShift artifacts** — release images and operators required for
  provisioning tenant clusters


### File format

These YAML files use the protobuf `Any` encoding format required by
`osac create -f`. Each file includes:

- `@type` — protobuf message type (e.g., `type.googleapis.com/osac.private.v1.ClusterCatalogItem`)
- `metadata.name` — unique identifier
- `title` / `description` — human-friendly display text
- `template` — typed Template reference (`id` or `name`, plus scope)
- `published` — whether available for provisioning
- `fields` — typed field policies selecting `locked` or `editable`, with an optional
  `editable.default_value`
