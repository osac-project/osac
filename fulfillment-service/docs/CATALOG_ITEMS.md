# Catalog Items

## Overview

OSAC uses a three-level hierarchy to provision infrastructure resources:

```
Template (private API)
    ↓  referenced by
Catalog Item (private API, published to public API)
    ↓  used by
Resource (public API — cluster or compute instance)
```

**Templates** are infrastructure blueprints managed through the private (admin) API. They define
the underlying provisioning logic: node set structures, host types, and spec defaults (pull secret,
SSH key, version, network CIDRs). Templates are not visible to end users.

**Catalog items** reference a template and add a curation layer on top. Using `field_definitions`,
the admin controls which resource spec fields are exposed to end users, locks fields to fixed
values, sets defaults, and optionally validates user input with JSON Schema. Once published
(`published: true`), catalog items become visible through the public API.

**Resources** (clusters, compute instances) are what end users create. A user can create a resource
in two ways:

- `osac create cluster --template <id>` — direct template access, no field restrictions. Supports
  `--template-parameter` to pass custom values (e.g., `vpc_id`, `vlan`) that AAP uses for
  provisioning.
- `osac create cluster --catalog-item <id>` — the server resolves the template from the catalog
  item and applies `field_definitions` to enforce defaults and validation. Use `--set` to pass
  template parameters and spec field overrides (e.g., `--set template_parameters.vpc_id=vpc-123`).

Both templates and catalog items are created and managed by the cloud provider admin through the private
API. The distinction is not one of roles but of purpose: templates define what infrastructure
exists and are deeply linked to Ansible roles, catalog items define what users see. Unlike templates, which are private-only, catalog items span both APIs — the admin manages them via the private API, and once published they become visible
to end users via the public API (list, inspect, and use when creating resources).

### What field_definitions can do

Catalog items control **both typed spec fields and template parameters**:

- **Typed spec fields** (e.g., `pull_secret`, `ssh_public_key`, `version_name`, `network.pod_cidr`,
  `node_sets.<name>.size`) — fields defined in the resource protobuf spec. Simple scalar fields have
  dedicated CLI flags (`--pull-secret`, `--ssh-public-key`, `--pod-cidr`); nested or map-like fields
  such as `node_sets.*` or `network.*` are only settable via `--set` or YAML, because expressing them as
  first-class CLI arguments is awkward.
- **Template parameters** (e.g., `template_parameters.vpc_id`, `template_parameters.vlan`) —
  custom provisioning parameters forwarded to AAP as Ansible extra variables. Their names must
  already exist on the referenced template; a catalog item cannot invent new ones.

How catalog items interact with those parameters:

1. **Not listed in `field_definitions`** — the user cannot set them (any value sent is rejected).
   The server then applies the **template** default, if the parameter is optional
   (`required: false`) and template defines one. If it is optional with no default, it is omitted. If it is
   `required: true`, creation always fails when the parameter is not listed — template `default` is
   ignored for required parameters, so the catalog item must list them (with a catalog-item
   `default`, or `editable: true` so the user supplies the value).
2. **Listed in `field_definitions`** — the catalog item controls the value:
   - `editable: false` + `default` → locked to the catalog-item default (user cannot override).
   - `editable: true` + `default` → user may override; otherwise the catalog-item default is used.
   - `editable: true` without `default` → user **must** provide a value. The template default is
     **not** used as a fallback once the parameter is listed here.

In short: omit a parameter from `field_definitions` to keep the template's optional default; list
it to expose, lock, or require a catalog-item-controlled value.

## Creating Catalog Items

Catalog items are created using `osac create -f` with a YAML file. The file must include an `@type`
field that identifies the catalog item type.

> **Note:** Unlike other resources (clusters, compute instances), catalog items do not have a
> dedicated `osac create <type>` subcommand. The `field_definitions` structure — a list of entries
> each with `path`, `editable`, `default`, and `validation_schema` — is too complex to express as
> CLI flags. Use `osac create -f` with a YAML file instead.

### ClusterTemplates

Cluster Catalog Items are based on Cluster Templates. Templates are imported periodically via an
Ansible job; you can list the ones available in your environment:

```bash
$ osac get clustertemplates
TENANT  DELETING  PROJECT  ID                                  NAME  TITLE
shared  -         -        osac.templates.ocp_4_20_ai_maas     -     OpenShift AI 4.20 Cluster with MaaS
shared  -         -        osac.templates.ocp_4_20_small_nico  -     OpenShift 4.20 Cluster on NICo Bare Metal
shared  -         -        osac.templates.ocp_ci_small         -     CI OpenShift Cluster
shared  -         -        osac.templates.ocp_small            -     OpenShift Small Cluster
```

Example cluster template that the catalog item below references. It defines optional `vpc_id` and `vlan`
parameters (with defaults) that the catalog item can expose or lock:

```yaml
'@type': type.googleapis.com/osac.public.v1.ClusterTemplate
id: osac.templates.sandbox
metadata:
  name: sandbox
title: Sandbox Cluster
description: Small sandbox cluster template with networking parameters.
node_sets:
  workers:
    host_type: fc430
    size: 1
parameters:
  - name: vpc_id
    title: VPC ID
    description: Virtual private cloud identifier for provisioning.
    required: false
    type: type.googleapis.com/google.protobuf.StringValue
    default:
      '@type': type.googleapis.com/google.protobuf.StringValue
      value: vpc-sandbox-default
  - name: vlan
    title: VLAN ID
    description: VLAN used for the cluster network.
    required: false
    type: type.googleapis.com/google.protobuf.Int64Value
    default:
      '@type': type.googleapis.com/google.protobuf.Int64Value
      value: "100"
```

### ClusterCatalogItem

```yaml
'@type': type.googleapis.com/osac.public.v1.ClusterCatalogItem
metadata:
  name: sandbox
title: Sandbox Cluster
description: Small development cluster.
template: "osac.templates.sandbox"
published: true
field_definitions:
  # Typed proto fields
  - path: ssh_public_key
    display_name: SSH Public Key
    editable: false
    default: "ssh-ed25519 AAAA..."
  - path: node_sets.workers.size
    display_name: Workers Count
    editable: false
    default: 1
  - path: node_sets.workers.host_type
    display_name: Workers Resource Class
    editable: true
    default: "fc430"
  - path: version_name
    display_name: Version
    editable: false
    default: "4-17-0"  # must match an existing, enabled ClusterVersion metadata.name
  - path: network.pod_cidr
    display_name: Pod CIDR
    editable: true
    default: "10.128.0.0/14"
    validation_schema: '{"type":"string","pattern":"^[0-9./]+$"}'
  # Template parameters — forwarded to AAP/Ansible
  - path: template_parameters.vpc_id
    display_name: VPC ID
    editable: true
    default: "vpc-sandbox-01"
  - path: template_parameters.vlan
    display_name: VLAN ID
    editable: false
    default: 100
    validation_schema: '{"type":"number","minimum":1,"maximum":4094}'
```

> Cluster templates and catalog items reference host types in `node_sets.*.host_type`. Those host
> types must already exist (`osac get hosttypes`).

Only list `template_parameters.*` paths that exist on the referenced template (e.g., VPC ID, VLAN ID). Omit a parameter to
keep the template's optional default; list it to expose, lock, or require a value. Required
template parameters must be listed — see the rules above.

### ComputeInstanceTemplates

Compute Instance Catalog Items are based on Compute Instance Templates, you can list the ones available in your environment:

```bash
$ osac get computeinstancetemplates
ID                          NAME  TITLE
osac.templates.ocp_virt_vm  -     Virtual Machine Template (Linux and Windows)
```

### ComputeInstanceCatalogItem

```yaml
'@type': type.googleapis.com/osac.public.v1.ComputeInstanceCatalogItem
metadata:
  name: standard-vm
title: Standard Virtual Machine
description: General-purpose virtual machine with KubeVirt.
template: "osac.templates.ocp_virt_vm"
published: true
field_definitions:
  - path: ssh_public_key
    display_name: SSH Key
    editable: true
    default: "ssh-ed25519 AAAA..."
  - path: image.source_type
    default: registry
    display_name: Image Source Type
  - path: image.source_ref
    default: quay.io/containerdisks/fedora:latest
    display_name: Image Reference
  - path: boot_disk.size_gib
    default: 20
    display_name: Boot Disk Size (GiB)
    editable: true
  - path: instance_type
    display_name: Instance Type
    editable: true
  - path: run_strategy
    display_name: Run Strategy
    editable: true
  - path: network_attachments
    display_name: Network Attachments
    editable: true
```
> IMPORTANT: Notice that ComputeInstanceCatalogItem uses instance type. So, in order to use it you'll need to have an instance type. For example:

```yaml
'@type': type.googleapis.com/osac.private.v1.InstanceType
id: simple-1-2
metadata:
  creator: system
  name: simple-1-2
  tenant: shared
spec:
  cores: 1
  memory_gib: 2
  state: INSTANCE_TYPE_STATE_ACTIVE
```

### Create the catalog item

```bash
osac create -f newclustercatalogitem.yaml
```

The input file can contain multiple documents separated by `---`.

## Field Definitions

Each entry in `field_definitions` controls a single field on the resource spec:

| Field | Type | Description |
|-------|------|-------------|
| `path` | string | Dot-notation path referencing a field in the resource spec (e.g., `node_sets.workers.size`) |
| `display_name` | string | Human-friendly label for UIs and CLIs |
| `editable` | bool | Whether users can override this field when creating a resource from the catalog item |
| `default` | any | Default value for this field |
| `validation_schema` | string | Optional JSON Schema (draft 2020-12) for validating user-provided values of editable fields |

Non-editable fields are locked to their default value. Editable fields allow users to override the
default, optionally constrained by a `validation_schema`.

### Available Paths

The following paths can be used in `field_definitions`. When a user creates a resource from a
catalog item, the server rejects any spec field not listed in `field_definitions` with an
`InvalidArgument` error.

**ClusterCatalogItem** paths:

| Path | CLI flag | Description |
|------|----------|-------------|
| `pull_secret` | `--pull-secret` | Credentials for container image repositories |
| `ssh_public_key` | `--ssh-public-key` | SSH public key installed on worker nodes |
| `version_name` | `--version` | ClusterVersion name for the OpenShift release |
| `network.pod_cidr` | `--pod-cidr` | Pod network CIDR (default: `10.128.0.0/14`) |
| `network.service_cidr` | `--service-cidr` | Service network CIDR (default: `172.30.0.0/16`) |
| `node_sets.<name>.size` | — | Number of nodes in a named node set (e.g., `node_sets.workers.size`) |
| `node_sets.<name>.host_type` | — | Host type for a named node set. Immutable after creation |
| `template_parameters.<name>` | `--set` | Custom template parameter forwarded to AAP as an Ansible extra variable |

**ComputeInstanceCatalogItem** paths:

| Path | Description |
|------|-------------|
| `ssh_public_key` | SSH public key |
| `instance_type` | Instance Type includes number of CPU cores, memory, etc. |
| `run_strategy` | VM run strategy (e.g., `Always`, `Halted`) |
| `user_data` | Cloud-init or ignition user data |
| `image.source_type` | Image source type (e.g., `registry`) |
| `image.source_ref` | Image reference (e.g., OCI image URL) |
| `boot_disk.size_gib` | Boot disk size in GiB |
| `additional_disks` | Additional disk configurations |
| `network_attachments` | Network attachments (subnet + security groups per NIC) |
| `template_parameters.<name>` | Custom template parameter forwarded to AAP as an Ansible extra variable |

These paths are defined by the resource API. If new fields are added or removed in a future version,
the available paths change accordingly.

## Creating Resources from Catalog Items

Once a catalog item is published, users create resources from it using `--catalog-item`:

```bash
osac create cluster --catalog-item dev-sandbox
```

Users can provide spec fields via CLI flags and `--set`:

```bash
osac create cluster --catalog-item dev-sandbox \
  --name my-cluster \
  --pull-secret "$(cat pull-secret.json)" \
  --ssh-public-key "$(cat ~/.ssh/id_ed25519.pub)" \
  --version "4-17-0" \
  --pod-cidr "10.128.0.0/14"
```

Use `--set` to pass template parameters or override editable spec fields. Each `--set` takes a
single `KEY=VALUE` pair (split on the first `=`):

```bash
osac create cluster --catalog-item rhel-ai-small \
  --set template_parameters.vpc_id=vpc-staging-02 \
  --set template_parameters.ip_block_id=block-789 \
  --pull-secret-file pull-secret.json
```

Non-editable parameters are rejected by the server:

```bash
# This fails because vlan is non-editable in the catalog item:
osac create cluster --catalog-item rhel-ai-small \
  --set template_parameters.vlan=5000
# Error: field 'template_parameters.vlan' is not editable
```

### How CLI flags interact with field_definitions

CLI flags like `--pull-secret` and `--ssh-public-key` set values in the resource spec. However,
the server applies `field_definitions` **after** receiving the request, which means:

- If a field is **not listed** in `field_definitions`, the server **rejects** the request with
  `InvalidArgument`.
- If a field is **non-editable** in the catalog item and the user provides a value, the server
  **rejects** the request with `InvalidArgument`.
- If a field is **editable**, the CLI flag value is accepted and validated against
  `validation_schema` if one is defined.
- If an editable field is **not provided** by the user, the catalog item's default is applied.

For example, given a catalog item with `version_name` set as non-editable with a fixed default,
running `--version "4-18-0"` results in an error — the user cannot override locked fields.

### Server-side processing

When the server processes the request, it:

1. Looks up the catalog item and verifies it is published
2. Sets the resource's `spec.template` to the template referenced by the catalog item
3. Applies `field_definitions`:
   - **Unlisted fields**: any spec field not in `field_definitions` is rejected (`InvalidArgument`)
   - **Non-editable fields**: rejects if the user provided a value; otherwise applies the default
   - **Editable fields with a user value**: validated against `validation_schema` if present
   - **Editable fields without a user value**: the default is applied
4. Validates the resulting spec and creates the resource

## Managing Catalog Items

### List

```bash
osac get clustercatalogitems
osac get computeinstancecatalogitems
```

### Inspect

```bash
osac get clustercatalogitems <id> -o yaml
osac get computeinstancecatalogitems <id> -o yaml
```

### Update

Edit a catalog item interactively (opens in `$EDITOR`):

```bash
osac edit clustercatalogitems <id>
osac edit computeinstancecatalogitems <id>
```

This lets you modify `title`, `description`, `published`, `field_definitions`, and `template`.

### Delete

```bash
osac delete clustercatalogitems <id>
osac delete computeinstancecatalogitems <id>
```

## API Endpoints

| Operation | Method | Endpoint |
|-----------|--------|----------|
| List cluster catalog items | `GET` | `/api/fulfillment/v1/cluster_catalog_items` |
| Get cluster catalog item | `GET` | `/api/fulfillment/v1/cluster_catalog_items/{id}` |
| Create cluster catalog item | `POST` | `/api/fulfillment/v1/cluster_catalog_items` |
| Update cluster catalog item | `PATCH` | `/api/fulfillment/v1/cluster_catalog_items/{id}` |
| Delete cluster catalog item | `DELETE` | `/api/fulfillment/v1/cluster_catalog_items/{id}` |
| List compute instance catalog items | `GET` | `/api/fulfillment/v1/compute_instance_catalog_items` |
| Get compute instance catalog item | `GET` | `/api/fulfillment/v1/compute_instance_catalog_items/{id}` |
| Create compute instance catalog item | `POST` | `/api/fulfillment/v1/compute_instance_catalog_items` |
| Update compute instance catalog item | `PATCH` | `/api/fulfillment/v1/compute_instance_catalog_items/{id}` |
| Delete compute instance catalog item | `DELETE` | `/api/fulfillment/v1/compute_instance_catalog_items/{id}` |

See [Filter expressions](FILTER.md) for filtering and ordering list results.
