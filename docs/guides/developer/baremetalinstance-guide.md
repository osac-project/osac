# Provisioning a Bare Metal Instance with a DiskImage

This guide shows how to publish a QCOW2 disk artifact, register it as a
`DiskImage`, and select that `DiskImage` when creating a `BareMetalInstance`.
It uses the OSAC CLI for all OSAC operations.

## Prerequisites

- The `osac` CLI is installed and authenticated.
- A tenant has a published BareMetalInstance CatalogItem and the caller can
  create BareMetalInstances from it.
- A registry that the provisioning environment can reach contains (or can
  contain) the disk artifact. Configure registry credentials in the
  provisioning environment when the registry is private.
- An existing whole-disk QCOW2 image is available locally. OSAC bare metal
  provisioning requires an image with a complete partition table, all required
  partitions, and a bootloader; root-partition-only images are not supported.

## 1. Publish the disk artifact

`DiskImage.spec.source_ref` identifies an OCI registry artifact. It is a
reference only: registering a DiskImage does not fetch or inspect the image
artifact. Ensure the registry, artifact, architecture, and credentials are
usable by the provisioning backend before offering the image to users.

Publish a QCOW2 artifact with metadata identifying the disk format and platform:

```bash
export REGISTRY="registry.example.com"
export REPOSITORY="osac/rhel9"
export TAG="9.4"
export QCOW2="./rhel-9.4-x86_64.qcow2"

oras push -a disktype=qcow2 --artifact-platform linux/amd64 \
  "$REGISTRY/$REPOSITORY:$TAG" "$QCOW2"
```

The `disktype=qcow2` annotation and platform metadata in this command identify
the artifact as a Linux QCOW2 image. The `--artifact-platform` option is
experimental in ORAS 1.3; check the [ORAS push documentation](https://oras.land/docs/commands/oras_push/)
for version-specific behavior. For an immutable artifact selection, record the
manifest digest reported by `oras push` and use
`oci://$REGISTRY/$REPOSITORY@sha256:<digest>` as the source reference. Use a
tag reference when you intend the selected artifact to change if the tag is
updated.

## 2. Register and discover the DiskImage

`BareMetalCPUSpec.architecture` (`spec.hardware.cpu.architecture`) is a string
accepting exactly `amd64`, `arm64`, and `s390x`. Aliases, uppercase, and
surrounding whitespace are rejected. A Cloud Provider Admin can create a type
with labels matching the provider's inventory:

```bash
osac create baremetalinstancetype --name standard-amd64 \
  --cpu-cores 4 --cpu-architecture amd64 --memory-total-gb 16 \
  --cpu-threads-per-core 1 --network-port data-0:fabric:Ethernet:10Gbps \
  --host-label hardware=standard
```

Create a named OSAC DiskImage. The CLI defaults `--source-type` to `registry`
and `--guest-os-family` to `linux`; `--architecture` is required and may be
repeated for a multi-architecture artifact. These flags accept canonical names
case-insensitively. `DiskImageSpec.architecture` is an enum list: raw JSON/YAML
uses `ARCHITECTURE_AMD64`, `ARCHITECTURE_ARM64`, or `ARCHITECTURE_S390X`
(protobuf values 1, 2, 3); human-readable architecture labels are lowercase.

```bash
osac create diskimage \
  --name rhel-9-4-amd64 \
  --source-ref "oci://$REGISTRY/$REPOSITORY:$TAG" \
  --guest-os-family linux \
  --architecture amd64

osac get diskimages
osac get diskimages rhel-9-4-amd64 -o yaml
```

The DiskImage name or ID can be used in a `DiskImageReference`. When a name is
used without an explicit scope, OSAC resolves an image in the instance tenant
first and then the shared tenant. Use the returned ID when a name could be
ambiguous. An image in another tenant is not a valid dependency for the
instance, even if a caller can otherwise discover it.

## 3. Create the BareMetalInstance with the OSAC CLI

A BareMetalInstance must have an effective `spec.disk_image`. Set it with
`--disk-image` or use a CatalogItem that provides a valid
`fields.disk_image` default. The old `spec.image` field is reserved and must
not be sent. `disk_image` is immutable after creation.

Use `--disk-image` with a DiskImage name or ID. The command requires a
CatalogItem; it has no `--image` or `--image-source-type` flags. For this example,
`standard-bare-metal` must select an `amd64` type and permit caller-selected
images. If the image is locked, omit `--disk-image`, even for the same image.

```bash
osac create baremetalinstance \
  --name worker-0 \
  --catalog-item standard-bare-metal \
  --disk-image rhel-9-4-amd64 \
  --ssh-key "$(cat ~/.ssh/id_ed25519.pub)"
```

## Networking

Bare-metal networking follows the accepted
[BMaaS networking design](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1437-bmaas-networking/design.md).
That design is the target contract; it does not assert every step is already
enforced in shipping code. Baseline attachment work is under closed OSAC-1448 /
OSAC-1510; remaining PR #357 delta implementation and deployed verification are
[OSAC-6818](https://redhat.atlassian.net/browse/OSAC-6818) and
[OSAC-6820](https://redhat.atlassian.net/browse/OSAC-6820). Shared networking
resources and gates are described in the
[networking guide](networking-guide.md); this section covers only the
BareMetalInstance lifecycle. Architecture summary:
[Bare Metal Server Fulfillment](../../architecture/bm-server-fulfillment.md).

**Ownership.** The deployment's Fabric Manager owns Fabric operations (port
moves, SecurityGroup binding on the physical port, DHCP, and lease lookup).
The bare-metal-fulfillment-operator orchestrates host inventory, OS
provisioning, NetworkAttachment create/delete, the handoff reboot, repair
marking, and status mirroring. It does **not** call Fabric Managers or switch
or DHCP APIs directly. The osac-operator networking controller runs every
Fabric task on the NetworkAttachment.

**Attachment.** A BareMetalInstance has at most one tenant network attachment.
Omit the attachment to use the tenant's default Subnet and SecurityGroup, or
supply a partial selection; the resolved choice is fixed after creation. When
several tenant-facing interfaces exist, you may select the physical interface
(`interface`); otherwise the platform picks a consistent default. System-only
or unknown interfaces are rejected. The sole attachment is the host's primary
route; there is no separate primary-network choice.

**Create path (provider networking enabled).** The host is imaged on the
provider provisioning network. After OS provisioning, OSAC creates a
NetworkAttachment; the Fabric Manager moves the selected port to the Subnet
segment and takes other fabric ports off the provisioning network. BMF then
requests one handoff reboot. After the handoff, the networking controller runs
`query_dhcp_lease` for the selected NIC MAC and records the tenant address.
Readiness with a tenant IP requires that discovery; a provisioning-network
address is never shown as the tenant address. With no lease after ten minutes,
OSAC warns and keeps querying.

**Automatic external access.** `--external-ip-attachment` (or the equivalent
API field) requests an ExternalIP and ExternalIPAttachment for the server.
Inbound access starts only after the tenant endpoint is ready and a real
address is allocated. Automatic external resources are removed when the
server is deleted.

**Delete and repair.** Deletion returns the host to inventory only after
tenant-network cleanup completes. Fabric ports return to the provisioning
network through the manager. If a FabricDomain still lists the host, BMF
writes a repair mark before release; selection skips marked hosts until a
Cloud Provider Admin clears the mark (see the design support procedures).

**Provider networking disabled.** Host provisioning and deprovisioning stay
available on the provisioning network. OSAC does not move tenant interfaces,
discover tenant addresses, or promise tenant routing or ExternalIP access.
Server readiness then attests OS provisioning only.

Example create with an explicit attachment and automatic external access:

```bash
osac create baremetalinstance \
  --name worker-0 \
  --catalog-item standard-bare-metal \
  --disk-image rhel-9-4-amd64 \
  --network-attachment subnet=<subnet-id>,interface=data-0,security-groups=<sg-id> \
  --external-ip-attachment \
  --ssh-key "$(cat ~/.ssh/id_ed25519.pub)"
```

## Validation, lifecycle, and deletion

OSAC resolves the DiskImage reference before creating the instance. The
following outcomes apply to direct selections and to an effective CatalogItem
default. Reference visibility and image lifecycle checks precede architecture
comparison:

| Situation | Result |
|---|---|
| Reference is missing or empty after CatalogItem defaults | `InvalidArgument` |
| Reference cannot be resolved in the instance or shared tenant | Existing reference error: `InvalidArgument` with a reference-validation field error, or `NotFound` from scoped resolution |
| DiskImage is `AVAILABLE` and architecture-compatible | Create succeeds |
| DiskImage is `DEPRECATED` and architecture-compatible | Create succeeds with a warning |
| DiskImage is `OBSOLETE` | `FailedPrecondition` |
| Delete is requested for a DiskImage still referenced by a BMI or CatalogItem | `FailedPrecondition`; the deletion is blocked |

Deprecated-image warning details are available in the gRPC Create response.
The CLI prints them to stderr as `Warning: ...`; the REST Create endpoint
returns only the BareMetalInstance object and omits warnings. Warnings
describe accepted input; they do not indicate that OSAC inspected or can
retrieve the artifact behind `source_ref`.

## Architecture compatibility

Creating a BareMetalInstance compares the effective type's CPU architecture
with the selected DiskImage's entire architecture list after caller, CatalogItem,
and template selection. A DiskImage that lists multiple CPU architectures is allowed to be used with a type that
matches any of the listed architectures. CaaS requests through private BareMetalInstance Create
receive the same check. This compares declarations; it does not establish image
content correctness, physical inventory compatibility, or VM image compatibility.

| Rejected request | Error | Correction |
|---|---|---|
| Instance Create or concrete CatalogItem Create/Update selects a mismatched pair | `InvalidArgument`, `INCOMPATIBLE_DISK_IMAGE_ARCHITECTURE` | Select a compatible image or type |
| DiskImage Update removes an architecture needed by an active instance or concrete CatalogItem pair | `FailedPrecondition`, `DISK_IMAGE_ARCHITECTURE_IN_USE` | Retain the architecture or replace dependent resources |
| Template Update changes its type incompatibly with an inheriting CatalogItem's image | `FailedPrecondition`, `INCOMPATIBLE_DISK_IMAGE_ARCHITECTURE` | Select a compatible type or replace dependent CatalogItems |

Dependency checks include non-deleted catalogs across tenants including unpublished
CatalogItems.

BareMetalInstanceType hardware and existing instance type/image/template selections
are immutable: replace the resource to change them.

## CatalogItem defaults

CatalogItems express DiskImage policy under `fields.disk_image`; templates do
not supply a BareMetalInstance DiskImage default. A locked policy always
supplies the selected image. An editable policy with a `default_value` supplies
it only when the caller omits `spec.disk_image`.

```yaml
fields:
  disk_image:
    editable:
      default_value:
        name: rhel-9-4-amd64
```

CatalogItem Create and Update validate the selected image. `OBSOLETE` and
out-of-scope images are rejected. A `DEPRECATED` image is accepted with a
warning. See [Catalog items](../../../fulfillment-service/docs/CATALOG_ITEMS.md)
for the complete field-policy rules.
