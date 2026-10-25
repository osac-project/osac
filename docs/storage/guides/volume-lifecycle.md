# ComputeInstance volume lifecycle

> **Status: Dev Preview.** Volume lifecycle management is under development;
> semantics below reflect the current phase.

Volumes are managed as part of their parent ComputeInstance:

- **Creation:** a volume is created and tracked for each requested disk
  (boot and additional), provisioned from the tier's resolved StorageClass
  through the Storage API.
- **Reporting:** volume identifiers and their parent attachment are reported
  in ComputeInstance status; the workload cluster's DataVolumes reference the
  resulting volume IDs.
- **Deletion:** volumes are deleted with their ComputeInstance. A volume
  attached to a ComputeInstance cannot be deleted independently.

Not supported in this phase:

- Standalone volume provisioning (Volume-as-a-Service)
- Dynamic volume attach and detach
- Volume resize, migration, snapshots, and backups

This page will be expanded as the volume lifecycle work lands. See also
[concepts/volume.md](../concepts/volume.md).
