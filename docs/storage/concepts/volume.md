# Volume

> **Status: Dev Preview.** Volume lifecycle management is under development.
> See [guides/volume-lifecycle.md](../guides/volume-lifecycle.md) for current
> semantics.

A volume is a unit of persistent block storage created for a ComputeInstance
disk. When a tenant selects a [StorageTier](storagetier.md) for a boot disk or
an additional disk, the disk is backed by a volume provisioned from the
tier's resolved [StorageClass](storageclass.md).

In the current Dev Preview, volumes are managed as part of their parent
ComputeInstance:

- A volume is created and tracked for each requested disk.
- Volume identifiers and their parent attachment are reported in
  ComputeInstance status.
- Volumes are deleted with their ComputeInstance; a volume attached to a
  ComputeInstance cannot be deleted independently.

Standalone volume provisioning, dynamic attach/detach, resize, snapshots, and
backups are not supported in this phase.

## See also

- [StorageTier](storagetier.md): how a disk selects its storage
- [Volume lifecycle](../guides/volume-lifecycle.md): Dev Preview semantics
