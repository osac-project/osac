# Managing Compute Instance Catalog Items

This guide covers catalog items for compute instances: what they are, how
admins create and manage them, and how users create VMs from them. It explains
the relationship between a catalog item's `field_definitions` and the
arguments accepted when creating a ComputeInstance.

Each step shows the `osac` CLI command and the equivalent gRPC / REST API
calls so that the same guide works for both CLI users and application
developers integrating with OSAC's API.

## Contents

- [Overview](#overview)
- [Who does what](#who-does-what)
- [Prerequisites](#prerequisites)
- [Browsing catalog items](#browsing-catalog-items)
  - [List catalog items](#list-catalog-items)
  - [Inspect a catalog item](#inspect-a-catalog-item)
- [Admin operations](#admin-operations)
  - [Create a catalog item](#create-a-catalog-item)
  - [Publish a catalog item](#publish-a-catalog-item)
  - [Update a catalog item](#update-a-catalog-item)
  - [Unpublish a catalog item](#unpublish-a-catalog-item)
  - [Delete a catalog item](#delete-a-catalog-item)
- [How field definitions shape VM creation](#how-field-definitions-shape-vm-creation)
  - [Available paths](#available-paths)
  - [Editable vs non-editable fields](#editable-vs-non-editable-fields)
  - [Defaults and validation](#defaults-and-validation)
  - [Instance type](#instance-type)
  - [Server-side processing](#server-side-processing)
- [User operations](#user-operations)
  - [Create a VM from a catalog item](#create-a-vm-from-a-catalog-item)
- [Troubleshooting](#troubleshooting)

---

## Overview

OSAC uses a three-level hierarchy to provision compute instances:

```text
Template (admin-only)
    ↓  referenced by
Catalog Item (admin-managed, published to users)
    ↓  used by
ComputeInstance (user-created VM)
```

**Templates** are infrastructure blueprints managed through the admin API.
They define the underlying provisioning logic. Templates are not visible to
end users.

**Catalog items** reference a template and add a curation layer. Using
`field_definitions`, the admin controls which ComputeInstance spec fields
users can set, locks fields to fixed values, provides defaults, and
optionally validates user input with JSON Schema. Once published
(`published: true`), catalog items become visible through the public API.

**ComputeInstances** are the VMs that users create. When a user creates a VM
with `--catalog-item` (CLI) or `catalog_item` (API), the server resolves the
underlying template from the catalog item and enforces the catalog item's
`field_definitions` against the user's request.

Storage tier selection is part of the disk contract: every boot disk and every
additional disk must resolve to a storage tier. See [Storage tier selection in
the ComputeInstance Guide](computeinstance-guide.md#storage-tier-selection)
for the precedence rules and provisioning troubleshooting.

| Resource | Purpose | Managed by |
|----------|---------|------------|
| **ComputeInstanceTemplate** | Infrastructure blueprint for VM provisioning | Cloud Provider Admin |
| **ComputeInstanceCatalogItem** | Curated offering with field restrictions and defaults | Cloud Provider Admin |
| **ComputeInstance** | The running VM | Organization User |

---

## Who does what

| Persona | Actions |
|---------|---------|
| **Cloud Provider Admin** | Creates templates and catalog items. Publishes, updates, unpublishes, and deletes catalog items. Defines which fields users can control via `field_definitions`. |
| **Organization User** | Lists published catalog items. Creates VMs using `--catalog-item` (CLI) or `catalog_item` (API). Provides values for editable fields and accepts defaults for the rest. |

Organization Users cannot create, modify, or delete catalog items.

---

## Prerequisites

**CLI users:**

- The `osac` CLI is installed and configured (API endpoint, authentication
  token).

**API users:**

- `grpcurl` (for gRPC) or `curl` (for REST) is installed.
- Set the following environment variables for the examples in this guide:

  ```bash
  export OSAC_API="<api-endpoint>"    # host:port (e.g., fulfillment-api.osac.svc:443)
  export TOKEN="<authentication-token>"

  # Skip TLS verification in development environments:
  # export GRPCURL_FLAGS="-insecure"
  # export CURL_FLAGS="-k"
  ```

**Both:**

- For admin operations: access to the private admin API.
- For user operations: a Tenant in `Ready` state (see
  [Tenant Setup Guide](tenant-setup.md)).

---

## Browsing catalog items

The following operations are available to both Cloud Provider Admins and
Organization Users.

### List catalog items

List all published catalog items:

**CLI:**

```bash
osac get computeinstancecatalogitems
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  $OSAC_API osac.public.v1.ComputeInstanceCatalogItems/List
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  "https://$OSAC_API/api/fulfillment/v1/compute_instance_catalog_items"
```

The CLI renders the output as a table:

| ID | NAME | TITLE | PUBLISHED |
|----|------|-------|-----------|

The gRPC and REST commands return JSON responses containing the same
fields.

> **Note:** The public API only returns published catalog items. Admins using
> the private API see all catalog items regardless of publish state.

### Inspect a catalog item

View the full details of a catalog item, including its `field_definitions`:

**CLI:**

```bash
osac get computeinstancecatalogitems <name-or-id> -o yaml
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  -d '{"id": "<catalog-item-id>"}' \
  $OSAC_API osac.public.v1.ComputeInstanceCatalogItems/Get
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  "https://$OSAC_API/api/fulfillment/v1/compute_instance_catalog_items/<catalog-item-id>"
```

The output shows the catalog item's metadata, title, description, template
reference, publish state, and the complete list of field definitions with
their paths, editability, defaults, and validation schemas.

---

## Admin operations

### Create a catalog item

Catalog items are created using `osac create -f` with a YAML file. The
`field_definitions` structure is too complex for CLI flags, so a file is
required.

> **Note:** Catalog item management (create, update, publish, unpublish,
> delete) uses the **private** admin API (`osac.private.v1`). Template
> discovery below uses the **public** read-only API since templates are
> readable by all authenticated users.

First, find the available compute instance templates:

**CLI:**

```bash
osac get computeinstancetemplates
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  $OSAC_API osac.public.v1.ComputeInstanceTemplates/List
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  "https://$OSAC_API/api/fulfillment/v1/compute_instance_templates"
```

Create a YAML file (e.g., `standard-vm-catalog-item.yaml`):

```yaml
'@type': type.googleapis.com/osac.public.v1.ComputeInstanceCatalogItem
metadata:
  name: standard-linux-vm
title: Standard Linux VM
description: |
  General-purpose Linux virtual machine with sensible defaults.
  Users can choose a registered SSH key, instance type, and boot disk size.
template: "osac.templates.ocp_virt_vm"
published: false
field_definitions:
  - path: ssh_key
    display_name: SSH Key
    editable: true
  - path: instance_type
    display_name: Instance Type
    editable: true
    default: "standard-4-16"
  - path: image.source_type
    display_name: Image Source Type
    editable: false
    default: "registry"
  - path: image.source_ref
    display_name: Image
    editable: true
    default: "quay.io/containerdisks/fedora:latest"
  - path: boot_disk.size_gib
    display_name: Boot Disk Size (GiB)
    editable: true
    default: 20
    validation_schema: '{"type":"number","minimum":10,"maximum":500}'
  - path: boot_disk.storage_tier
    display_name: Boot Disk Storage Tier
    editable: true
    default:
      name: standard
  - path: additional_disks
    display_name: Additional Disks
    editable: true
    default:
      - size_gib: 200
        storage_tier:
          name: archive
  - path: run_strategy
    display_name: Run Strategy
    editable: false
    default: "Always"
  - path: user_data
    display_name: Cloud-init User Data
    editable: true
```

**CLI:**

```bash
osac create -f standard-vm-catalog-item.yaml
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" -d '{
  "object": {
    "metadata": {
      "name": "standard-linux-vm"
    },
    "title": "Standard Linux VM",
    "description": "General-purpose Linux virtual machine with sensible defaults.\nUsers can choose a registered SSH key, instance type, and boot disk size.",
    "template": "osac.templates.ocp_virt_vm",
    "published": false,
    "field_definitions": [
      {"path": "ssh_key", "display_name": "SSH Key", "editable": true},
      {"path": "instance_type", "display_name": "Instance Type", "editable": true, "default": "standard-4-16"},
      {"path": "image.source_type", "display_name": "Image Source Type", "editable": false, "default": "registry"},
      {"path": "image.source_ref", "display_name": "Image", "editable": true, "default": "quay.io/containerdisks/fedora:latest"},
      {"path": "boot_disk.size_gib", "display_name": "Boot Disk Size (GiB)", "editable": true, "default": 20, "validation_schema": "{\"type\":\"number\",\"minimum\":10,\"maximum\":500}"},
      {"path": "boot_disk.storage_tier", "display_name": "Boot Disk Storage Tier", "editable": true, "default": {"name": "standard"}},
      {"path": "additional_disks", "display_name": "Additional Disks", "editable": true, "default": [{"size_gib": 200, "storage_tier": {"name": "archive"}}]},
      {"path": "run_strategy", "display_name": "Run Strategy", "editable": false, "default": "Always"},
      {"path": "user_data", "display_name": "Cloud-init User Data", "editable": true}
    ]
  }
}' $OSAC_API osac.private.v1.ComputeInstanceCatalogItems/Create
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{
  "metadata": {
    "name": "standard-linux-vm"
  },
  "title": "Standard Linux VM",
  "description": "General-purpose Linux virtual machine with sensible defaults.\nUsers can choose a registered SSH key, instance type, and boot disk size.",
  "template": "osac.templates.ocp_virt_vm",
  "published": false,
  "field_definitions": [
    {"path": "ssh_key", "display_name": "SSH Key", "editable": true},
    {"path": "instance_type", "display_name": "Instance Type", "editable": true, "default": "standard-4-16"},
    {"path": "image.source_type", "display_name": "Image Source Type", "editable": false, "default": "registry"},
    {"path": "image.source_ref", "display_name": "Image", "editable": true, "default": "quay.io/containerdisks/fedora:latest"},
    {"path": "boot_disk.size_gib", "display_name": "Boot Disk Size (GiB)", "editable": true, "default": 20, "validation_schema": "{\"type\":\"number\",\"minimum\":10,\"maximum\":500}"},
    {"path": "boot_disk.storage_tier", "display_name": "Boot Disk Storage Tier", "editable": true, "default": {"name": "standard"}},
    {"path": "additional_disks", "display_name": "Additional Disks", "editable": true, "default": [{"size_gib": 200, "storage_tier": {"name": "archive"}}]},
    {"path": "run_strategy", "display_name": "Run Strategy", "editable": false, "default": "Always"},
    {"path": "user_data", "display_name": "Cloud-init User Data", "editable": true}
  ]
}' "https://$OSAC_API/api/private/v1/compute_instance_catalog_items"
```

The catalog item is created with `published: false`. It is not visible to
users until you publish it.

> **Note:** The YAML file can contain multiple documents separated by `---`,
> allowing you to create several catalog items in one command.

---

### Publish a catalog item

Publishing makes the catalog item visible to users in the public API. Edit
the catalog item and set `published: true`:

**CLI:**

```bash
osac edit computeinstancecatalogitems standard-linux-vm
```

In the editor, change `published` from `false` to `true`, then save and
exit.

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" -d '{
  "object": {
    "id": "<catalog-item-id>",
    "published": true
  },
  "update_mask": {
    "paths": ["published"]
  }
}' $OSAC_API osac.private.v1.ComputeInstanceCatalogItems/Update
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -X PATCH -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{
  "published": true
}' "https://$OSAC_API/api/private/v1/compute_instance_catalog_items/<catalog-item-id>?update_mask=published"
```

---

### Update a catalog item

To modify a catalog item's title, description, field definitions, or
template:

**CLI:**

```bash
osac edit computeinstancecatalogitems standard-linux-vm
```

This opens the catalog item in `$EDITOR`. Make your changes, save, and exit.

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" -d '{
  "object": {
    "id": "<catalog-item-id>",
    "title": "Updated Title",
    "description": "Updated description."
  },
  "update_mask": {
    "paths": ["title", "description"]
  }
}' $OSAC_API osac.private.v1.ComputeInstanceCatalogItems/Update
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -X PATCH -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{
  "title": "Updated Title",
  "description": "Updated description."
}' "https://$OSAC_API/api/private/v1/compute_instance_catalog_items/<catalog-item-id>?update_mask=title,description"
```

Changes to `field_definitions` take effect for new VM requests immediately.
Existing VMs are not affected — the catalog item reference on a
ComputeInstance is immutable after creation.

---

### Unpublish a catalog item

To hide a catalog item from users without deleting it, edit it and set
`published: false`:

**CLI:**

```bash
osac edit computeinstancecatalogitems standard-linux-vm
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" -d '{
  "object": {
    "id": "<catalog-item-id>",
    "published": false
  },
  "update_mask": {
    "paths": ["published"]
  }
}' $OSAC_API osac.private.v1.ComputeInstanceCatalogItems/Update
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -X PATCH -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{
  "published": false
}' "https://$OSAC_API/api/private/v1/compute_instance_catalog_items/<catalog-item-id>?update_mask=published"
```

Unpublished catalog items are no longer returned by the public API and cannot
be used for new VM creation. Existing VMs that reference the catalog item
continue to work normally.

> **Note:** Existing VMs that reference an unpublished catalog item continue
> to run normally. Users can view the catalog item details on their existing
> ComputeInstance resources, but the catalog item itself is no longer returned
> by the public List or Get endpoints.

---

### Delete a catalog item

**CLI:**

```bash
osac delete computeinstancecatalogitems standard-linux-vm
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  -d '{"id": "<catalog-item-id>"}' \
  $OSAC_API osac.private.v1.ComputeInstanceCatalogItems/Delete
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -X DELETE -H "Authorization: Bearer $TOKEN" \
  "https://$OSAC_API/api/private/v1/compute_instance_catalog_items/<catalog-item-id>"
```

---

## How field definitions shape VM creation

Each entry in `field_definitions` controls a single field on the
ComputeInstance spec. Together, they define the contract between the catalog
item and the user: which fields the user can set, which are locked, what
defaults apply, and what validation constraints exist.

### Available paths

The following paths can be used in `field_definitions`. These correspond
to fields in the ComputeInstance spec. Any path not in this list is ignored
when applying `field_definitions`.

| Path | CLI flag | API field | Description |
|------|----------|-----------|-------------|
| `instance_type` | `--instance-type` | `spec.instance_type` | Named instance type (e.g., `standard-4-16`) |
| `ssh_key` | `--ssh-key` | `spec.ssh_key` | Tenant-scoped SSH public key Secret reference |
| `run_strategy` | `--run-strategy` | `spec.run_strategy` | VM run strategy (`Always`, `Halted`, etc.) |
| `user_data` | `--user-data` | `spec.user_data` | Cloud-init or ignition user data |
| `image.source_type` | (part of `--image`) | `spec.image.source_type` | Image source type (e.g., `registry`) |
| `image.source_ref` | `--image` | `spec.image.source_ref` | Image reference (e.g., OCI image URL) |
| `boot_disk.size_gib` | `--boot-disk-size` | `spec.boot_disk.size_gib` | Boot disk size in GiB |
| `boot_disk.storage_tier` | `--boot-disk-storage-tier` | `spec.boot_disk.storage_tier` | Boot disk storage tier reference |
| `additional_disks` | `--additional-disk` | `spec.additional_disks` | Complete list of additional disk configurations, including each disk's storage tier |
| `network_attachments` | `--network-attachment` | `spec.network_attachments` | Network attachments (subnet + security groups per NIC) |

The optional `ssh_key` is a tenant-scoped Secret reference, not inline
public-key data. A shared catalog item must not lock or default a tenant's key.
List `ssh_key` as editable without a default so each tenant user can choose
a key from their own scope. If they omit it, the optional field remains unset.
A tenant-scoped catalog item may lock or default only a key from its own tenant
and project. Users provide the key with `--ssh-key` or `spec.ssh_key`; see
[Register an SSH key](computeinstance-guide.md#register-an-ssh-key).

These paths use dot notation for nested fields. For example,
`boot_disk.size_gib` refers to the `size_gib` field inside the `boot_disk`
object in the ComputeInstance spec.

---

### Editable vs non-editable fields

Each field definition has an `editable` flag that controls whether the user
can provide their own value:

| `editable` | User provides value | Result |
|------------|---------------------|--------|
| `false` | Yes (via CLI flag or API field) | Request is rejected with `field '<path>' is not editable` |
| `false` | No | Catalog item default is applied |
| `true` | Yes | User's value is accepted (validated against `validation_schema` if present) |
| `true` | No | Default is applied if set; otherwise the field stays unset if optional, or required-field validation fails |

Non-editable fields enforce consistency. For example, locking `run_strategy`
to `Always` ensures all VMs from this catalog item are always running,
regardless of what the user passes on the CLI or in the API request.

Editable fields give users flexibility. For example, making `instance_type`
editable lets each user choose a compute configuration.

---

### Defaults and validation

**Defaults**: Every non-editable field must have a `default`. Editable
fields may also have a default. If an editable optional field has no default
and the user omits it, it remains unset. Required fields fail validation when
omitted. The ComputeInstance `ssh_key` is optional.

**Validation**: Editable fields can include a `validation_schema` — a JSON
Schema (draft 2020-12) string. The server validates the user's value against
this schema. Non-editable fields do not need validation since their value
always comes from the default.

Example — constrain boot disk size to between 10 and 500 GiB:

```yaml
- path: boot_disk.size_gib
  display_name: Boot Disk Size (GiB)
  editable: true
  default: 20
  validation_schema: '{"type":"number","minimum":10,"maximum":500}'
```

Example — restrict image references to a specific registry:

```yaml
- path: image.source_ref
  display_name: Image
  editable: true
  default: "quay.io/containerdisks/fedora:latest"
  validation_schema: '{"type":"string","pattern":"^quay\\.io/containerdisks/"}'
```

For storage tier field-definition syntax, see the examples in the catalog item
creation section above. The [ComputeInstance Guide's storage tier defaults](computeinstance-guide.md#configure-defaults)
documents precedence and the complete-list behavior for `additional_disks`.

---

### Instance type

Catalog items use the `instance_type` path to control which compute
configuration users select. Instance types are named bundles of cores and
memory managed by the admin (see
[Managing Instance Types](instancetype-guide.md)).

If the catalog item defines `instance_type` with a default value, the server
validates that the referenced instance type exists and is not OBSOLETE. If
it is DEPRECATED, the server returns a warning.

---

### Server-side processing

When a user creates a ComputeInstance with `--catalog-item` (CLI) or
`catalog_item` (API), the server processes the request in this order:

1. **Lookup**: Finds the catalog item by name or ID.
2. **Access check**: Verifies the catalog item is published and not deleted.
3. **Template resolution**: Sets the ComputeInstance's `template` to the
   template referenced by the catalog item.
4. **Apply field definitions**: For each field definition:
   - **Non-editable**: rejects a user-provided value; otherwise applies the default.
   - **Editable with user value**: validates against `validation_schema` if
     present.
   - **Editable without user value**: applies the default.
5. **Validate**: Validates the resulting spec, including the required storage
   tier on every disk, instance type state, and network attachments, then
   creates the resource.

Fields not covered by any field definition pass through unchanged — the user
can set them freely via CLI flags or API fields.

---

## User operations

### Create a VM from a catalog item

List the available catalog items to find one that fits your workload:

**CLI:**

```bash
osac get computeinstancecatalogitems
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  $OSAC_API osac.public.v1.ComputeInstanceCatalogItems/List
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  "https://$OSAC_API/api/fulfillment/v1/compute_instance_catalog_items"
```

Inspect the catalog item to see which fields you can customize:

**CLI:**

```bash
osac get computeinstancecatalogitems <name-or-id> -o yaml
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  -d '{"id": "<catalog-item-id>"}' \
  $OSAC_API osac.public.v1.ComputeInstanceCatalogItems/Get
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  "https://$OSAC_API/api/fulfillment/v1/compute_instance_catalog_items/<catalog-item-id>"
```

In the output, review the `field_definitions`:
- Fields with `editable: true` can be set via CLI flags or API fields.
- Fields with `editable: false` are locked to the catalog item's default —
  you cannot override them.
- Fields with a `validation_schema` must match the specified constraints.

Create a VM, providing values for the editable fields:

**CLI:**

```bash
osac create computeinstance \
  --name my-vm \
  --catalog-item standard-linux-vm \
  --instance-type standard-4-16 \
  --ssh-key <ssh-key-name> \
  --boot-disk-size 40 \
  --network-attachment subnet=<subnet-id> \
  --user-data '#cloud-config
user: myuser
password: <password>
chpasswd:
  expire: false'
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" -d '{
  "object": {
    "metadata": {
      "name": "my-vm"
    },
    "spec": {
      "catalog_item": "standard-linux-vm",
      "instance_type": "standard-4-16",
      "ssh_key": {"name": "<ssh-key-name>"},
      "boot_disk": {"size_gib": 40},
      "network_attachments": [{"subnet": "<subnet-id>"}],
      "user_data": "#cloud-config\nuser: myuser\npassword: <password>\nchpasswd:\n  expire: false"
    }
  }
}' $OSAC_API osac.public.v1.ComputeInstances/Create
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{
  "metadata": {
    "name": "my-vm"
  },
  "spec": {
    "catalog_item": "standard-linux-vm",
    "instance_type": "standard-4-16",
    "ssh_key": {"name": "<ssh-key-name>"},
    "boot_disk": {"size_gib": 40},
    "network_attachments": [{"subnet": "<subnet-id>"}],
    "user_data": "#cloud-config\nuser: myuser\npassword: <password>\nchpasswd:\n  expire: false"
  }
}' "https://$OSAC_API/api/fulfillment/v1/compute_instances"
```

The server applies the catalog item's field definitions to your request.
Non-editable fields reject user-provided values and use the catalog item's
defaults. Editable fields accept your values, falling back to catalog item
defaults for anything you omit. The boot disk tier must be present in the
request, in the catalog item, or in the template's boot-disk defaults. Each
additional disk must include a storage tier in the request or in the catalog
item's complete `additional_disks` default.

For the full VM creation walkthrough, see the
[ComputeInstance Guide](computeinstance-guide.md).

---

## Troubleshooting

### Catalog item not found

```text
Error: catalog item 'my-catalog-item' is not published (404 Not Found)
```

The catalog item either does not exist or is not published. Ask your platform
administrator to verify the catalog item exists and has `published: true`.

### CLI flag or API field ignored for a non-editable field

If you pass a CLI flag or API field for a field that the catalog item marks as
non-editable, the server rejects the request with an error such as
`field 'boot_disk.storage_tier' is not editable`. Inspect the catalog item's
field definitions to see which fields are locked:

```bash
osac get computeinstancecatalogitems <name-or-id> -o yaml
```

### Validation failed for a field

```text
Error: validation failed for field 'boot_disk.size_gib': ...
```

The value you provided does not match the catalog item's `validation_schema`
for that field. Inspect the catalog item to see the constraints:

```bash
osac get computeinstancecatalogitems <name-or-id> -o yaml
```

Look at the `validation_schema` for the field mentioned in the error to
understand the allowed values.

### Required field missing

If a required ComputeInstance field has no default and is omitted, creation
fails. `ssh_key` is optional, so an editable `ssh_key` policy without a default
leaves it unset when the user omits it. To use an SSH key, supply
`--ssh-key <ssh-key-name>` or `spec.ssh_key`.

### Storage tier missing or unavailable

If a catalog item does not define `boot_disk.storage_tier` or
`additional_disks` correctly, the ComputeInstance may fail tier validation or
provisioning. Inspect the catalog item's field definitions, then follow the
[ComputeInstance Guide's storage tier troubleshooting](computeinstance-guide.md#validation-and-troubleshooting)
for the exact validation errors, provisioning message, and recovery steps.

### Instance type is obsolete

```text
Error: instance type 'standard-4-16' in field_definitions is obsolete and cannot be used
```

The catalog item's default instance type has been obsoleted. Update the
catalog item to reference an ACTIVE instance type:

```bash
osac edit computeinstancecatalogitems <name-or-id>
```

List the available instance types to find a replacement:

```bash
osac get instancetype
```
