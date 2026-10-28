# ComputeInstance volume lifecycle

> **Status: Dev Preview.** The volume tracking described here is under
> development and lands as part of the Dev Preview.

ComputeInstance disks are tracked as Volume records in the Storage API:

- **Creation:** a Volume record is created for each requested disk (boot and
  additional) before the ComputeInstance is provisioned. The record's
  identifier is passed to the workload cluster, where the disk is a KubeVirt
  DataVolume provisioned from the tier's resolved StorageClass and annotated
  with the volume identifier.
- **Reporting:** each disk's volume is visible on the ComputeInstance as a
  read-only correlation.
- **Deletion:** Volume records are removed with their ComputeInstance, and
  deletion completes only after the records are cleaned up. A disk attached
  to a ComputeInstance cannot be deleted independently.

Until the volume tracking lands, disks are provisioned directly as KubeVirt
DataVolumes from the tier's resolved StorageClass, with no Volume records.

The flow from a tenant's tier selection to a running disk:

```
Tenant user creates a ComputeInstance,
selecting a tier per disk            (see "Storage tier selection" in the
        │                             ComputeInstance guide)
        ▼
Tier resolves to a StorageClass      (see StorageClasses and labels)
        ▼
Volume record registered per disk
        ▼
DataVolume created per disk from
that class, annotated with the
volume identifier
        ▼
ComputeInstance deletion removes the
Volume records, then the DataVolumes
```

Not supported in this phase:

- Standalone volume provisioning (Volume-as-a-Service)
- Dynamic volume attach and detach
- Volume resize, migration, snapshots, and backups

See also [concepts/volume.md](../concepts/volume.md).
