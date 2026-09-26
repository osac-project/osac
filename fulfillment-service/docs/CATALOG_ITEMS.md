# Catalog Items

## Overview

Users can create a resource directly from a Template or through a Catalog Item that references one:

```
Template → Catalog Item → Resource
Template ───────────────→ Resource (direct API creation)
```

**Templates** describe how OSAC provisions a cluster, virtual machine (VM), or bare metal instance. For
example, a template can specify the number and type of machines in a cluster, a VM's boot-disk size,
or the hardware type for a bare metal instance. Templates can also define inputs that users provide
when creating a resource; these are called template parameters. Users can list and inspect the
templates available to them.

**Catalog items** are curated offerings for creating resources. Each references a template and can
fix selected inputs or provide defaults that users may change. Policies for these inputs appear in
`fields` and `template_parameters`. Inputs without policies keep their normal creation behavior.
For example, a Linux VM offering can fix its disk image while letting users choose an instance type
and boot-disk size.

Cloud Provider Admins manage offerings shared across organizations (tenants). Tenant Admins manage
offerings for their own organization. Tenant and project access rules determine which catalog items
you can read, including unpublished items.

Setting `published: true` makes an item available for provisioning. No role can use an unpublished
item to create resources.

**Resources** are the clusters, compute instances, and bare metal instances that users create.
All three can be created from a catalog item or directly from a template through the API.

Catalog policies apply only during creation. The resource keeps its template and resolved
configuration, so later catalog edits affect only future provisioning. Unpublishing or deleting
the catalog does not affect existing resources. After creation, normal resource lifecycle rules
apply; the catalog no longer enforces its locked values.

## Creating Catalog Items

Catalog items are created using `osac create -f` with a YAML file. The file must include an `@type`
field that identifies the catalog item type. Use YAML to configure field policies. Dedicated
catalog-item create subcommands support basic metadata, template selection, and publication.

### ClusterTemplates

Cluster Catalog Items are based on Cluster Templates. Templates are imported periodically via an
Ansible job; you can list the ones available in your environment:

```bash
osac get clustertemplates
osac get clustertemplates <id> -o yaml
osac get hosttypes
osac get clusterversions
```

For the cluster example below, assume the administrator has installed a `sandbox` template and
its provisioning workflow. It defines optional `vpc_id` and `vlan` parameters with defaults. Its
`fc430` HostType (hardware type) must already exist in the shared tenant. A node set groups machines
of the same hardware type; the `workers` node set below starts with one `fc430` machine:

```yaml
'@type': type.googleapis.com/osac.private.v1.ClusterTemplate
id: osac.templates.sandbox
metadata:
  name: sandbox
  tenant: shared
title: Sandbox Cluster
description: Small sandbox cluster template with networking parameters.
node_sets:
  workers:
    host_type:
      name: fc430
      shared: true
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

This offering fixes the OpenShift version and VLAN. Users can change the default worker count and
pod CIDR, and supply their own pull secret and SSH key. The shared ClusterVersion `4-17-0` must
already exist, be enabled, and be usable for new clusters.

```yaml
'@type': type.googleapis.com/osac.public.v1.ClusterCatalogItem
metadata:
  name: sandbox
title: Sandbox Cluster
description: Small development cluster.
template:
  name: sandbox
  shared: true
published: true
fields:
  version:
    locked:
      name: 4-17-0
      shared: true
  node_sets:
    editable:
      default_value:
        items:
          workers:
            host_type:
              name: fc430
              shared: true
            size: 1
  network:
    pod_cidr:
      editable:
        default_value: 10.128.0.0/14
template_parameters:
  vpc_id:
    editable:
      default_value:
        '@type': type.googleapis.com/google.protobuf.StringValue
        value: vpc-sandbox-01
  vlan:
    locked:
      '@type': type.googleapis.com/google.protobuf.Int64Value
      value: "100"
```

The `vpc_id` and `vlan` policies use parameters declared by the template shown above. See
[Template parameter policies](#template-parameter-policies) for default and required-value behavior.

### ComputeInstanceTemplates

Compute Instance Catalog Items are based on Compute Instance Templates. List and inspect the
ones available in your environment:

```bash
osac get computeinstancetemplates
osac get computeinstancetemplates osac.templates.ocp_virt_vm -o yaml
osac get instancetypes
osac get diskimages
osac get storagetiers
```

### ComputeInstanceCatalogItem

This offering uses the shared `osac.templates.ocp_virt_vm` template and fixes the image to Fedora.
Users can change the default instance type and 80 GiB boot-disk size. They can also supply their own
SSH key, which has no catalog policy.

Before creating the offering, the template, shared `u1-small` InstanceType, and shared `fedora`
DiskImage must exist.

```yaml
'@type': type.googleapis.com/osac.public.v1.ComputeInstanceCatalogItem
metadata:
  name: standard-vm
title: Standard Virtual Machine
description: Fedora virtual machine with customizable compute resources and boot-disk size.
template:
  id: osac.templates.ocp_virt_vm
  shared: true
published: true
fields:
  disk_image:
    locked:
      name: fedora
      shared: true
  instance_type:
    editable:
      default_value:
        name: u1-small
        shared: true
  boot_disk:
    size_gib:
      editable:
        default_value: 80
```

Instance types define CPU, memory, and optional GPU resources. If `u1-small` is not already
available, a platform administrator can create it through the private API, for example:

```yaml
'@type': type.googleapis.com/osac.private.v1.InstanceType
metadata:
  name: u1-small
  tenant: shared
spec:
  cores: 2
  memory_gib: 2
  state: INSTANCE_TYPE_STATE_ACTIVE
```

DiskImages must also be created before they can be referenced. A shared Fedora image could be:

```yaml
'@type': type.googleapis.com/osac.private.v1.DiskImage
metadata:
  name: fedora
  tenant: shared
spec:
  source_type: SOURCE_TYPE_REGISTRY
  source_ref: "quay.io/containerdisks/fedora:41"
  guest_os_family: GUEST_OS_FAMILY_LINUX
  architecture:
    - ARCHITECTURE_AMD64
  lifecycle: DISK_IMAGE_LIFECYCLE_AVAILABLE
```

### Create the catalog item

Save the cluster catalog example as `newclustercatalogitem.yaml`, or the VM catalog example as
`newcomputeinstancecatalogitem.yaml`, then create it:

```bash
osac create -f newclustercatalogitem.yaml
osac create -f newcomputeinstancecatalogitem.yaml
```

The input file can contain multiple documents separated by `---`. Create dependencies first.

### Reference scope

You can reference an object by ID or name. An ID must be visible to you, but does not need `shared`
or `project` selectors. If you supply both an ID and a name, they must identify the same object.

When using a name, set `shared: true` for an object in the shared tenant. Set `project` to reference
an object in another project within the same tenant. See
[Object references](API.md#object-references) for the reference format.

Shared catalogs use shared dependencies. Tenant catalogs can use shared objects or objects from
their own tenant.

Local references follow a stricter rule: they must point to objects in the owner's tenant and
project. This applies to both the catalog and the resource being created. Examples include Secrets,
subnet/security-group attachments, and Bare Metal instance types.

A shared catalog cannot lock or default local references. Use `editable: {}` to let each tenant
supply its own value. StorageTier references use the platform scope.

For ComputeInstance catalog items, `ssh_key` is an optional reference to an SSH public key Secret.
A shared item can leave it editable with no default so each tenant can supply its own key. A
tenant-scoped item can default or lock a key from its own scope. Omitting the key is valid; it only
means SSH public-key access is not configured by the catalog item.

## Field Policies

Each policy selects exactly one behavior. Reference policies carry typed reference objects, as
shown by the locked `disk_image` and editable `instance_type` in the VM example.

| Policy | User supplies a value | User omits the value |
|--------|-----------------------|----------------------|
| `locked: <value>` | Rejected, even if identical to the locked value | Use the locked value |
| `editable` with `default_value` | Use the user value | Use the catalog default |
| `editable: {}` | Use the user value | Follow normal template and system defaulting |
| No policy | Follow normal resource behavior | Follow normal template and system defaulting |

All values must satisfy the resource's normal validation rules, even if the field has no policy.
Creation fails if a required value is still missing after all defaults have been applied.

For scalar fields, explicitly supplying `false`, zero, or an empty string counts as user input.
Likewise, `locked: false` sets a fixed value. Normal validation still applies; for example, a
boot-disk size of zero is invalid.

Policies use the supported fields listed below. They do not support arbitrary paths, JSON Schema,
display names, allowed-value lists, or numeric ranges. Dotted names in the tables describe nested
fields; in YAML, write them as nested mappings.

### Available Fields

**ClusterCatalogItem** policies:

| Field | CLI flag | Description |
|-------|----------|-------------|
| `pull_secret_secret` | `--pull-secret` | Local Secret reference containing container registry credentials |
| `ssh_public_key` | `--ssh-public-key` | SSH public key installed on worker nodes |
| `version` | `--version` | ClusterVersion reference for the OpenShift release |
| `network.pod_cidr` | `--pod-cidr` | Pod network CIDR (system default: `10.128.0.0/14`) |
| `network.service_cidr` | `--service-cidr` | Service network CIDR (system default: `172.30.0.0/16`) |
| `node_sets` | — | Policy for the complete node-set map, including sizes and HostType references |
| `network_attachment` | — | Subnet and security-group attachment |
| `auto_external_ip_attachment` | — | Whether to provision external IP attachments automatically |

**ComputeInstanceCatalogItem** policies:

| Field | Description |
|-------|-------------|
| `ssh_key` | Optional reference to an SSH public key Secret |
| `instance_type` | InstanceType reference defining CPU cores, memory, and optional GPUs |
| `run_strategy` | VM run strategy (`COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS`, `COMPUTE_INSTANCE_RUN_STRATEGY_HALTED`) |
| `user_data` | Cloud-init or ignition user data |
| `disk_image` | DiskImage reference |
| `boot_disk.size_gib` | Boot disk size in GiB |
| `boot_disk.storage_tier` | StorageTier reference for the boot disk |
| `additional_disks` | Policy for the complete list of additional disk configurations |
| `network_attachments` | Policy for the complete list of network attachments |
| `auto_external_ip_attachment` | Whether to provision an external IP attachment automatically |

**BareMetalInstanceCatalogItem** policies:

| Field | Description |
|-------|-------------|
| `instance_type` | Local BareMetalInstanceType reference for host placement; separate from compute InstanceType |
| `disk_image` | DiskImage reference |
| `ssh_public_key` | SSH public key |
| `user_data` | Provisioning user data |
| `run_strategy` | Bare metal run strategy |
| `network_attachments` | Policy for the complete list of network attachments |
| `auto_external_ip_attachment` | Whether to provision an external IP attachment automatically |

### List and node-set policies

List policies put their values under `items`. This example offers a default additional disk that
users can change. The `standard` StorageTier must already exist and be active:

```yaml
fields:
  additional_disks:
    editable:
      default_value:
        items:
          - size_gib: 100
            storage_tier:
              name: standard
```

A nonempty user list replaces the catalog default list. Entries are not merged.

Omitting a list and supplying an empty list have the same effect: the catalog's locked value or
default is used. An empty user list cannot override a locked list. Without a catalog value, normal
resource behavior applies, including compute default-network selection.

A catalog cannot set an empty locked or default network-attachment list. The `items` wrapper is
only for policies; resource lists do not use it.

Cluster `node_sets` policies apply to the complete map, also under `items`. If the user supplies a
nonempty map, omitted template node sets are not added. Each supplied node set needs a positive size.

For a node set that exists in the template, users can omit the HostType to inherit it. They cannot
choose a different HostType for that node set. New node sets are allowed if they specify a valid
HostType.

When the user omits the map or supplies an empty one, the catalog's locked map or default is used.
If neither is set, the template's node sets are used.

### Template parameter policies

Template parameters are custom provisioning inputs passed to Ansible Automation Platform (AAP) as
Ansible extra variables.
Each policy uses a parameter name, such as `vpc_id` or `vlan` in the cluster example.

The template declares which parameters exist, their types, any defaults, and whether they are
required. A catalog cannot add new parameters. Its values must use the declared type URL and be
valid for that type, as shown by the `@type` and `value` entries in the cluster example.

Policies support scalar types such as `StringValue` and `BoolValue`, plus `Timestamp` and `Duration`.
A `google.protobuf.Value` parameter can still be used, but cannot have a catalog policy.

Locked and editable policies work as they do for resource fields. If an editable parameter has no
catalog default, it uses the template default when available. Users can also supply parameters
that have no policy.

Required parameters are checked after catalog and template defaults have been applied. They need
no catalog policy if the user or template supplies a value.

## Creating Resources from Catalog Items

Once a catalog item is published, users create resources from it using `--catalog-item`. The VM
offering above leaves the boot-disk StorageTier ungoverned, so choose an active tier when creating
each VM:

```bash
osac create computeinstance --catalog-item <standard-vm-id> \
  --boot-disk-storage-tier <active-tier-name>
```

Replace `<standard-vm-id>` with the ID from the create output or catalog listing. The compute
instance command currently expects an ID for `--catalog-item`. These VM commands use the tenant's
default subnet. If there is no ready default subnet, add `--network-attachment subnet=SUBNET_ID`.

Users can provide spec fields via CLI flags and `--set`. For the VM example, increase the boot disk
to 100 GiB and supply your own SSH key:

```bash
osac create computeinstance --catalog-item <standard-vm-id> \
  --name my-vm \
  --boot-disk-size 100 \
  --boot-disk-storage-tier <active-tier-name> \
  --ssh-public-key "$(cat ~/.ssh/id_ed25519.pub)"
```

The VM uses the locked Fedora image and the default `u1-small` instance type. The user-supplied disk
size overrides the catalog's 80 GiB default. The SSH key is accepted even though it has no policy.

Do not supply locked inputs. Even supplying the same Fedora image fails with `InvalidArgument`.
The commands below show both cases. They assume `another-image` also exists in the shared tenant
and is visible to you:

```bash
# The catalog fixes the image, so neither command can override it:
osac create computeinstance --catalog-item <standard-vm-id> \
  --disk-image another-image --set disk_image.shared=true
osac create computeinstance --catalog-item <standard-vm-id> \
  --disk-image fedora --set disk_image.shared=true
# Error includes: disk_image: field is not editable
```

For the cluster example, supply a pull secret and SSH key and optionally override the pod CIDR:

```bash
osac create secret --name cluster-pull-secret \
  --from-file=.dockerconfigjson=pull-secret.json

osac create cluster --catalog-item sandbox \
  --name my-cluster \
  --pull-secret cluster-pull-secret \
  --ssh-public-key "$(cat ~/.ssh/id_ed25519.pub)" \
  --pod-cidr "10.128.0.0/14"
```

Do not supply `--version` for this offering: the catalog fixes it to `4-17-0`.

Use `--set` to pass template parameters or override editable or ungoverned spec fields. Each `--set`
takes a single `KEY=VALUE` pair (split on the first `=`):

```bash
osac create cluster --catalog-item sandbox \
  --set template_parameters.vpc_id=vpc-staging-02 \
  --pull-secret cluster-pull-secret
```

The CLI chooses the parameter type from the value: StringValue, Int64Value, DoubleValue, or
BoolValue. Use resource YAML if the template needs another type or the CLI would choose the wrong
one. For catalog-based creation, use `--set`. The `--template-parameter` and
`--template-parameter-file` flags are for direct-template creation.

Non-editable parameters are rejected by the server:

```bash
# This fails because vlan is locked in the catalog item:
osac create cluster --catalog-item sandbox \
  --set template_parameters.vlan=5000
# Error includes: template_parameters.vlan: field is not editable
```

### Shared pull secrets in cluster templates

Platform administrators can create a Vault-backed pull Secret in the `shared` tenant and reference
it from a shared cluster template:

```bash
osac --tenant shared create secret --name shared-pull-secret \
  --from-file=.dockerconfigjson=pull-secret.json
```

The template's default reference is:

```yaml
spec_defaults:
  pull_secret_secret:
    name: shared-pull-secret
```

If neither the user nor the catalog supplies a pull Secret, the cluster inherits the template's
reference. The fulfillment controller retrieves the Secret data during provisioning.

Tenant users can use the shared credential through the template. They cannot read or change the
shared Secret through the Secrets API. This is a special case: a tenant cannot directly supply a
shared Secret reference, and a catalog cannot lock or default a shared Secret for a tenant resource.

### Server-side processing

When the server processes the request, it:

1. Looks up the catalog item and verifies it is published and not deleted.
2. Resolves the template referenced by the catalog item and sets the resource's `spec.template`.
3. Rejects supplied locked inputs and applies the locked values. For editable inputs, user values
   take priority over catalog defaults. Ungoverned inputs follow their normal behavior.
4. Applies template defaults to remaining omitted inputs, then normal resource and system defaults.
5. Validates the final configuration and references before saving the resource.

A dry run applies defaults and validates the resource without saving it or creating automatic
networking resources. The response shows the resulting configuration. Normal creation also returns
any supported deprecation warnings.

Choose exactly one source in a creation request: `spec.catalog_item` or `spec.template`. Direct
template creation uses normal validation and skips catalog policies. The cluster and compute CLI
commands support `--template`, though the option currently emits a deprecation warning:

```bash
osac create cluster --template sandbox
osac create computeinstance --template osac.templates.ocp_virt_vm
```

The bare metal CLI subcommand requires `--catalog-item`. To create directly from a template, use
the API or `osac create -f` with `spec.template`.

API clients request dry-run mode with the HTTP header `X-Dry-Run: true` or gRPC metadata
`x-dry-run: true`.

## Managing Catalog Items

### List

```bash
osac get clustercatalogitems
osac get computeinstancecatalogitems
osac get baremetalinstancecatalogitems
```

These lists include unpublished items you have access to. To browse published offerings, use:

```bash
osac get clustercatalogitems --filter 'this.published'
osac get computeinstancecatalogitems --filter 'this.published'
osac get baremetalinstancecatalogitems --filter 'this.published'
```

### Inspect

```bash
osac get clustercatalogitems <id> -o yaml
osac get computeinstancecatalogitems <id> -o yaml
osac get baremetalinstancecatalogitems <id> -o yaml
```

### Update

Edit a catalog item interactively (opens in `$EDITOR`):

```bash
osac edit clustercatalogitems <id>
osac edit computeinstancecatalogitems <id>
osac edit baremetalinstancecatalogitems <id>
```

You can change `title`, `description`, `published`, `fields`, and `template_parameters`. You cannot
switch the catalog to another template. Removing a policy restores normal creation behavior for
that field. See [API updates](API.md#update) for how to update selected fields through the API.

Creating a catalog, changing its policies, or publishing it checks that its values and dependencies
are usable. Policy updates also check the policies left unchanged by the update.

Updates that only unpublish an item or change its title or description skip these dependency checks.
An admin can therefore withdraw an offering even if a dependency is no longer usable. All edits
affect only future provisioning.

### Delete

```bash
osac delete clustercatalogitems <id>
osac delete computeinstancecatalogitems <id>
osac delete baremetalinstancecatalogitems <id>
```

You can delete a catalog even if resources were created from it. Those resources keep their
template and configuration. Their `spec.catalog_item` records which catalog was used and cannot
be changed, even after that catalog is deleted.

The catalog protects its template and objects referenced by locked values or defaults from deletion.
This applies to both published and unpublished catalogs. Remove the policy reference, or delete
the catalog, before deleting a dependency. Other resources may still prevent the dependency's deletion.

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
| List bare metal instance catalog items | `GET` | `/api/fulfillment/v1/baremetal_instance_catalog_items` |
| Get bare metal instance catalog item | `GET` | `/api/fulfillment/v1/baremetal_instance_catalog_items/{id}` |
| Create bare metal instance catalog item | `POST` | `/api/fulfillment/v1/baremetal_instance_catalog_items` |
| Update bare metal instance catalog item | `PATCH` | `/api/fulfillment/v1/baremetal_instance_catalog_items/{id}` |
| Delete bare metal instance catalog item | `DELETE` | `/api/fulfillment/v1/baremetal_instance_catalog_items/{id}` |

See [Filter expressions](FILTER.md) for filtering and ordering list results.
