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

# Create all catalog items at once
for f in examples/catalog-items/*.yaml; do osac create -f "$f"; done
```

**Note:** `osac create -f` is **not idempotent** — it will fail if the resource
already exists.

### Using catalog items

After loading catalog items, use `--catalog-item` to create resources from them:

```bash
# Create a cluster (CIDR fields have defaults, so no extra flags needed)
osac create cluster --catalog-item simple-ocp-4-17-cluster

# Create a VM (network attachment is required — depends on your tenant's subnets)
osac create computeinstance --catalog-item linux-vm --network-attachment subnet=SUBNET_ID
```

**Note:** VM catalog items cannot default the `network_attachments` field because
the valid values depend on the tenant's existing subnets. You must always provide
at least one `--network-attachment` when creating a compute instance.

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

The VM catalog items reference **instance types** (e.g., `u1-medium`) that
must also exist before VMs can be provisioned. On a kind-dev cluster these
are seeded automatically by `kind-dev/setup.sh`. On a fresh cluster
`osac get instancetypes` is empty — create the required instance types first,
or update the `instance_type` default in the YAML files to match your
environment.

Catalog items also assume the relevant **artifacts** are already available:

- **VM images** — container disk images for tenant VMs (e.g.,
  `quay.io/containerdisks/fedora:latest`)
- **OpenShift artifacts** — release images and operators required for
  provisioning tenant clusters


### File format

These YAML files use the protobuf `Any` encoding format required by
`osac create -f`. Each file includes:

- `@type` — protobuf message type (e.g., `type.googleapis.com/osac.private.v1.ClusterCatalogItem`)
- `metadata.name` — unique identifier
- `title` / `description` — human-friendly display text
- `template` — template identifier this catalog item references
- `published` — whether visible in the public API
- `field_definitions` — user-editable fields with `path`, `display_name`,
  `editable`, `default`, and `validation_schema`
