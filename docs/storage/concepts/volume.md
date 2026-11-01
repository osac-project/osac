# Volume

> **Status: Dev Preview.** The volume tracking described here is under
> development and lands as part of the Dev Preview. See
> [guides/volume-lifecycle.md](../guides/volume-lifecycle.md) for the
> lifecycle semantics.

A volume is a unit of persistent block storage created for a ComputeInstance
disk. When a tenant selects a [StorageTier](storagetier.md) for a boot disk or
an additional disk, the disk is backed by a volume provisioned from the
tier's resolved [StorageClass](storageclass.md).

In the Dev Preview, ComputeInstance disks are tracked as Volume records in
the Storage API:

- A Volume record is created for each requested disk (boot and additional)
  before the ComputeInstance is provisioned, and the volume identifiers are
  passed to the workload cluster's DataVolumes.
- Each disk's volume is visible on the ComputeInstance as a read-only
  correlation.
- Volume records are removed with their ComputeInstance, and deletion
  completes only after the records are cleaned up. A disk attached to a
  ComputeInstance cannot be deleted independently.

Until the volume tracking lands, disks are provisioned directly as KubeVirt
DataVolumes from the tier's resolved StorageClass, with no Volume records.

Standalone volume provisioning, dynamic attach/detach, resize, snapshots, and
backups are not supported in this phase.

## See also

- [StorageTier](storagetier.md): how a disk selects its storage
- [Volume lifecycle](../guides/volume-lifecycle.md): Dev Preview semantics
