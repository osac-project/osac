# Creating a VM with OSAC

This guide walks through creating a ComputeInstance (virtual machine) using the OSAC CLI
or the gRPC / REST API.
It assumes you already have a Tenant in `Ready` state (see [Tenant Setup Guide](tenant-setup.md))
and networking resources set up (see [Networking Guide](networking-guide.md)).
For per-disk storage tier selection, see [Storage tier selection](#storage-tier-selection).
To change a VM's InstanceType after creation, see [Resizing a VM](computeinstance-resize-guide.md).

## Contents

- [Prerequisites](#prerequisites)
- [Storage tier selection](#storage-tier-selection)
- [Step 1: Find a Compute Instance Catalog Item](#step-1-find-a-compute-instance-catalog-item)
- [Step 2: Choose an instance type](#step-2-choose-an-instance-type)
- [Register an SSH key](#register-an-ssh-key)
- [Step 3: Create the ComputeInstance](#step-3-create-the-computeinstance)
- [Step 4: Monitor the instance](#step-4-monitor-the-instance)
- [Step 5: Access the console](#step-5-access-the-console)

---

## Prerequisites

**CLI users:**

- The `osac` CLI is installed and configured (API endpoint, authentication token).

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

- A Tenant is in `Ready` state (see [Tenant Setup Guide](tenant-setup.md)).
- Networking resources (VirtualNetwork, Subnet, and optionally SecurityGroups) are created
  and in `READY` state (see [Networking Guide](networking-guide.md)).
- At least one compute instance catalog item is published and available
  (see [Compute Instance Catalog Items Guide](computeinstance-catalogitem-guide.md)).
- At least one instance type is in `ACTIVE` state
  (see [Managing Instance Types](instancetype-guide.md)).
- At least one storage tier is available to the tenant. Each disk must receive
  a tier directly or through a catalog item or template default.

---

## Storage tier selection

Storage tiers let a cloud provider offer different storage characteristics to
ComputeInstance disks. A VM can use one tier for its boot disk and different
tiers for its additional disks. For example, a database VM might use a
performance tier for its boot disk and an archival tier for its data disk.

This section covers selecting an available storage tier for a ComputeInstance.
Creating and activating `StorageTier` resources and making them available
through a tenant's resolved StorageClasses are provider administration tasks
outside this guide.

The setting is per disk. In the API, `storage_tier` is a
`StorageTierReference`; examples use the tier's `name`.

### Tier resolution

The fulfillment service resolves each disk independently using this order:

| Priority | Source | Fields |
|----------|--------|--------|
| 1 | User request | `boot_disk.storage_tier` or `additional_disks[i].storage_tier` |
| 2 | ComputeInstanceCatalogItem default | `boot_disk.storage_tier` or the complete `additional_disks` list |
| 3 | ComputeInstanceTemplate default | `spec_defaults.boot_disk.storage_tier` |

An explicit user value takes precedence over a catalog or template default.
Catalog item policies still apply: a non-editable field rejects a user value,
while an editable field with a default accepts an override. For
`additional_disks`, omit the field to use the Catalog Item default. If the
Catalog Item has an editable default and you want no additional disks, set
`additional_disks` to `{"items": []}` to opt out. See the [CLI and API examples
below](#select-tiers-when-creating-a-vm) for these request shapes.

Template defaults apply when the user omits a value and the CatalogItem has not
already supplied one. However, an editable CatalogItem field without a default
must be provided by the user. The current template default model supports a
boot disk default; additional disks are defaulted as a complete list by a
catalog item.

Every disk that exists after resolution must have both a positive size and a
non-empty storage tier. Boot and additional disks can use different tiers.

### Configure defaults

#### ComputeInstance template

Set the boot disk default in the template's `spec_defaults`. A template
default is a fallback, so a user value or catalog item default can replace it.

```yaml
spec_defaults:
  boot_disk:
    size_gib: 20
    storage_tier:
      name: standard
```

The template does not define a default for the `additional_disks` array. Use a
catalog item when an offering should include additional disks by default.

#### ComputeInstance catalog item

Configure `boot_disk.storage_tier` and `additional_disks` field definitions
in the [Compute Instance Catalog Items Guide](computeinstance-catalogitem-guide.md).
That guide contains the CatalogItem-specific syntax and explains how editable
fields, defaults, and complete additional-disk lists work.

### Select tiers when creating a VM

The CLI accepts a tier name for the boot disk and for every additional disk:

```bash
osac create computeinstance \
  --name <computeinstance-name> \
  --template <template-name> \
  --instance-type <instance-type-name> \
  --disk-image <disk-image-name> \
  --boot-disk-size 20 \
  --boot-disk-storage-tier fast \
  --additional-disk size=200,storage-tier=archive
```

Repeat `--additional-disk` for more disks. The CLI requires
`storage-tier=<name>` in every disk specification. CatalogItems can define a
default list for `additional_disks`. If the CatalogItem used to create the
ComputeInstance has this default, pass `--additional-disk ""` to opt out and
create the instance without additional disks. The field must be editable for
this override to be accepted.
The boot disk tier may be omitted when the applicable Catalog Item policy does
not require a user value and a Catalog Item or Template default supplies it. A
template default does not satisfy an editable CatalogItem field without a
CatalogItem default.

The equivalent API shape is shown below. To opt out of a Catalog Item disk
default, send `"additional_disks": {"items": []}`.

```json
{
  "spec": {
    "boot_disk": {
      "size_gib": 20,
      "storage_tier": {"name": "fast"}
    },
    "additional_disks": {
      "items": [
        {
          "size_gib": 200,
          "storage_tier": {"name": "archive"}
        }
      ]
    }
  }
}
```

After creation, the selected tier is part of the disk configuration and
cannot be changed. To use another tier, create a new ComputeInstance.

### Validation and troubleshooting

#### Missing tier during request validation

If no source supplies a tier, creation fails with `INVALID_ARGUMENT`. Typical
messages are:

```text
boot_disk.storage_tier is required
additional_disks[0].storage_tier is required
```

Check the request and then inspect the catalog item with:

```bash
osac get computeinstancecatalogitems <name-or-id> -o yaml
```

Verify that the catalog item has the correct path and default, or add the tier
to the request. If the catalog item defines the path as editable without a
default, the user must provide the value; the template default is not used as
a substitute for that required catalog field.

#### Field rejected by a catalog item

If the request contains a storage-tier field path that the CatalogItem does
not list in its field definitions, the server rejects the request with:

```text
fields not allowed by catalog item: boot_disk.storage_tier
```

Add the path to the catalog item, or remove the user-provided field and provide
the value through a catalog or template default. If the field is locked, the
request must not include an override.

#### Tier unavailable for a tenant during provisioning

AAP resolves the requested tier to a tenant-specific StorageClass during
provisioning. If no matching StorageClass is available, the provisioning
condition includes this exact error line:

```text
Storage tier "archive" is not available for tenant "<tenant>" (resolving additional disk 1).
```

The complete error also lists the available tiers after this sentence.

Inspect the ComputeInstance status and the operator events:

```bash
kubectl describe computeinstance <computeinstance-name>
kubectl get events --sort-by=.lastTimestamp
```

If the message says that no resolved storage tiers are available, check the
Tenant `ClusterStorageReady` condition and the StorageClasses resolved for
that tenant. A provider administrator must create or repair the tenant's
storage configuration before retrying the VM.

#### Tier cannot be changed

The boot disk and additional disk configurations are immutable after creation.
An update that changes a disk's tier is rejected with an error containing
`disk is immutable` or `additional disks are immutable`. Create a replacement
ComputeInstance when the workload needs a different tier.

---

## Step 1: Find a Compute Instance Catalog Item

Catalog items are curated infrastructure offerings that define defaults and constraints for
compute instances. List the available catalog items:

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

To inspect a catalog item's details (defaults, editable fields):

**CLI:**

```bash
osac get computeinstancecatalogitems <id> -o yaml
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  -d '{"id": "<id>"}' \
  $OSAC_API osac.public.v1.ComputeInstanceCatalogItems/Get
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  "https://$OSAC_API/api/fulfillment/v1/compute_instance_catalog_items/<id>"
```

Note the **NAME** or **ID** of the catalog item you want to use.

> **Note:** Catalog items are created by platform admins. If no catalog items are available,
> contact your platform administrator. For details on catalog item lifecycle management and
> how field definitions control VM creation, see the
> [Compute Instance Catalog Items Guide](computeinstance-catalogitem-guide.md).

---

## Step 2: Choose an instance type

List the available instance types to find the compute configuration for your VM:

**CLI:**

```bash
osac get instancetype
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  $OSAC_API osac.public.v1.InstanceTypes/List
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  "https://$OSAC_API/api/fulfillment/v1/instance_types"
```

The output shows each type's name, cores, memory, state, and description.
Choose an ACTIVE instance type that meets your workload requirements.

Note the **NAME** of the instance type you want to use.

> **Note:** This step is optional if the catalog item already defines a default value for
> `instance_type`. You can check the catalog item's `field_definitions` to see
> whether this field has a non-editable default.

For details on instance type lifecycle and management, see
[Managing Instance Types](instancetype-guide.md).

---

## Register an SSH key

ComputeInstances can use a registered SSH public key stored in a tenant-scoped Secret. Create the
Secret before creating the VM:

```bash
osac create secret \
  --name <ssh-key-name> \
  --type ssh-public-key \
  --from-file public_key=$HOME/.ssh/id_ed25519.pub
```

The Secret must contain the OpenSSH public key under the `public_key` data entry. List Secrets with
`osac get secrets`, inspect one with `osac get secret <name-or-id> -o yaml`, and delete one you no
longer need with `osac delete secret <name-or-id>`. API clients manage the same Secret through
`osac.public.v1.Secrets` or `POST /api/fulfillment/v1/secrets`, using type
`SECRET_TYPE_SSH_PUBLIC_KEY` and the `public_key` data entry.

The SSH key is optional. When used, OSAC configures it on the VM's first boot through cloud-init.
Use it with cloud-init-compatible images; it is not supported with Windows disk images or
ignition-based user data. The `ssh_key` reference cannot be changed after the ComputeInstance is
created. API requests can refer to the Secret by name or ID.

---

## Step 3: Create the ComputeInstance

Create the ComputeInstance using the CLI or API. Use the catalog item and specify
networking via the subnet (and optionally security groups).

The catalog item's `field_definitions` determine which fields you can set. Fields marked
as `editable: true` accept your values; fields marked as `editable: false`
are locked to the catalog item's defaults and cannot be overridden. Inspect the catalog
item's field definitions beforehand to see which fields apply (see
[How field definitions shape VM creation](computeinstance-catalogitem-guide.md#how-field-definitions-shape-vm-creation)).

**CLI:**

```bash
osac create computeinstance \
  --name <computeinstance_name> \
  --catalog-item <catalog-item-name-or-id> \
  --instance-type <instance-type-name> \
  --image quay.io/containerdisks/fedora:latest \
  --ssh-key <ssh-key-name> \
  --boot-disk-size 10 \
  --boot-disk-storage-tier <boot-tier> \
  --additional-disk size=50,storage-tier=<data-tier> \
  --network-attachment subnet=<subnet-id> \
  --run-strategy Always \
  --user-data '#cloud-config
user: <username>
password: <password>
chpasswd:
  expire: false'
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" -d '{
  "object": {
    "metadata": {
      "name": "<computeinstance_name>"
    },
    "spec": {
      "catalog_item": "<catalog-item-name-or-id>",
      "instance_type": "<instance-type-name>",
      "image": {
        "source_type": "registry",
        "source_ref": "quay.io/containerdisks/fedora:latest"
      },
      "ssh_key": {"name": "<ssh-key-name>"},
      "boot_disk": {
        "size_gib": 10,
        "storage_tier": {"name": "<boot-tier>"}
      },
      "additional_disks": {
        "items": [{"size_gib": 50, "storage_tier": {"name": "<data-tier>"}}]
      },
      "network_attachments": [
        {"subnet": "<subnet-id>"}
      ],
      "run_strategy": "Always",
      "user_data": "#cloud-config\nuser: <username>\npassword: <password>\nchpasswd:\n  expire: false"
    }
  }
}' $OSAC_API osac.public.v1.ComputeInstances/Create
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{
  "metadata": {
    "name": "<computeinstance_name>"
  },
  "spec": {
    "catalog_item": "<catalog-item-name-or-id>",
    "instance_type": "<instance-type-name>",
    "image": {
      "source_type": "registry",
      "source_ref": "quay.io/containerdisks/fedora:latest"
    },
    "ssh_key": {"name": "<ssh-key-name>"},
    "boot_disk": {
      "size_gib": 10,
      "storage_tier": {"name": "<boot-tier>"}
    },
    "additional_disks": {
      "items": [{"size_gib": 50, "storage_tier": {"name": "<data-tier>"}}]
    },
    "network_attachments": [
      {"subnet": "<subnet-id>"}
    ],
    "run_strategy": "Always",
    "user_data": "#cloud-config\nuser: <username>\npassword: <password>\nchpasswd:\n  expire: false"
  }
}' "https://$OSAC_API/api/fulfillment/v1/compute_instances"
```

> **Note:** The `--ssh-key` flag refers to a registered SSH public key Secret by name. The
> `--user-data` field uses cloud-init format. The example
> above creates a user and disables password expiry, allowing you to log in via the VM
> console. Adjust the username and password as needed.
>
> The `--network-attachment` flag uses the format
> `subnet=<subnet-id>[,security-groups=<sg-id1>,<sg-id2>]`. Security groups are optional.
> The flag can be repeated for multiple NICs. In the API, use an array of
> `network_attachments` objects, each with a `subnet` field and an optional
> `security_groups` array. See the
> [Networking Guide](networking-guide.md) for details on creating subnets and security
> groups.

To attach security groups via the API, add them to each network attachment:

```json
"network_attachments": [
  {
    "subnet": "<subnet-id>",
    "security_groups": ["<securitygroup-id-1>", "<securitygroup-id-2>"]
  }
]
```

### Alternative: Create from a YAML file

You can also create a ComputeInstance from a YAML file with `osac create -f`. Create a file
(e.g., `my-vm.yaml`) with the following content:

```yaml
'@type': type.googleapis.com/osac.public.v1.ComputeInstance
metadata:
  name: <computeinstance_name>
spec:
  catalog_item: <catalog-item-name-or-id>
  instance_type: <instance-type-name>
  image:
    source_ref: quay.io/containerdisks/fedora:latest
    source_type: registry
  ssh_key:
    name: <ssh-key-name>
  boot_disk:
    size_gib: 10
    storage_tier:
      name: <boot-tier>
  additional_disks:
    items:
      - size_gib: 50
        storage_tier:
          name: <data-tier>
  network_attachments:
    - subnet: <subnet-id>
  run_strategy: Always
  user_data: |
    #cloud-config
    user: <username>
    password: <password>
    chpasswd:
      expire: false
```

To attach security groups in the YAML, add them to the network attachment:

```yaml
  network_attachments:
    - subnet: <subnet-id>
      security_groups:
        - <securitygroup-id>
```

Create the instance:

```bash
osac create -f my-vm.yaml
```

---

## Step 4: Monitor the instance

Track the ComputeInstance status:

**CLI:**

```bash
osac get computeinstance
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  $OSAC_API osac.public.v1.ComputeInstances/List
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  "https://$OSAC_API/api/fulfillment/v1/compute_instances"
```

Wait for the ComputeInstance state to reach `RUNNING`.

To get a specific instance by ID:

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  -d '{"id": "<computeinstance-id>"}' \
  $OSAC_API osac.public.v1.ComputeInstances/Get
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  "https://$OSAC_API/api/fulfillment/v1/compute_instances/<computeinstance-id>"
```

---

## Step 5: Access the console

Once the ComputeInstance is running, attach to its serial console:

```bash
osac console serial computeinstance <computeinstance-name-or-id>
```

Log in with the credentials defined in `user_data`.

> **Tip:** To exit the console, press `Ctrl+]`.

To connect via VNC instead:

```bash
osac console vnc computeinstance <computeinstance-name-or-id>
```

> **Note:** Console access is only available through the `osac` CLI, which
> establishes a bidirectional streaming connection via the
> `osac.public.v1.ConsoleProxy/Connect` gRPC stream. There is no REST
> equivalent for interactive console sessions.
