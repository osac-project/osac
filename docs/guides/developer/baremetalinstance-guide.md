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

Create a named OSAC DiskImage. The CLI defaults `--source-type` to `registry`
and `--guest-os-family` to `linux`; `--architecture` is required and may be
repeated for a multi-architecture artifact.

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
CatalogItem; it has no `--image` or `--image-source-type` flags.

```bash
osac create baremetalinstance \
  --name worker-0 \
  --catalog-item standard-bare-metal \
  --disk-image rhel-9-4-amd64 \
  --ssh-key "$(cat ~/.ssh/id_ed25519.pub)"
```

## Validation, lifecycle, and deletion

OSAC resolves the DiskImage reference before creating the instance. The
following outcomes apply to direct selections and to an effective CatalogItem
default:

| Situation | Result |
|---|---|
| Reference is missing or empty after CatalogItem defaults | `InvalidArgument` |
| Reference cannot be resolved in the instance or shared tenant | `NotFound` |
| DiskImage is `AVAILABLE` | Create succeeds |
| DiskImage is `DEPRECATED` | Create succeeds with a warning |
| DiskImage is `OBSOLETE` | `FailedPrecondition` |
| Delete is requested for a DiskImage still referenced by a BMI or CatalogItem | `FailedPrecondition`; the deletion is blocked |

Deprecated-image warning details are available in the gRPC Create response.
The REST Create endpoint returns only the BareMetalInstance object, and the
CLI displays a success message without showing warning details. Warnings
describe accepted input; they do not indicate that OSAC inspected or can
retrieve the artifact behind `source_ref`.

A DiskImage cannot be deleted while a BareMetalInstance or BareMetalInstance
CatalogItem still references it. The deletion request returns
`FailedPrecondition`; remove or replace dependent objects before retrying it.

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

## Upgrade and downgrade

This is a breaking field migration. Before upgrading, replace every pending
BareMetalInstance and BareMetalInstanceCatalogItem use of `image` with a
resolvable `disk_image` reference. Existing running or failed instances do not
reconcile merely because the service is upgraded. The current reconciler
injects an image URL for provisioning only after it resolves `disk_image`, so
replace pending legacy objects before upgrading them.

Older OSAC CLI versions must be upgraded because a requested legacy `image` is
not honored. If no CatalogItem policy supplies an effective `disk_image`, the
command fails with `InvalidArgument`. A CatalogItem default can instead allow
the command to succeed with that default image. Upgrade the CLI and CatalogItem
definitions together; mixed-version deployments are not supported.

There is no generic in-place downgrade procedure for this breaking contract.
Before rolling back a release, delete DiskImage-backed BareMetalInstances and
CatalogItems that reference DiskImages. A release-specific, tested rollback
runbook must then reverse the relevant database migration before deploying the
prior service. Do not apply migration SQL manually; follow that runbook for the
exact service and database versions. Recreate the resources as legacy objects
after the rollback if the target release requires `image`.
