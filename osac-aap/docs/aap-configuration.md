# AAP Configuration

AAP workers read runtime configuration from their instance-group environment.
The storage provider also accepts explicit Ansible variables when invoked directly.

## Block StorageClass filesystem type

New AAP-created native VAST, LVMS/TopoLVM, and direct Trident block StorageClasses
use `ext4` by default. Set `OSAC_STORAGE_BLOCK_FSTYPE` in the storage worker
environment, or pass `storage_provider_block_fstype` to Ansible. The explicit
variable takes precedence. The value must be a nonempty string supported by the
target driver and is passed unchanged, without trimming or an allowlist.

Only new native block classes consume this setting. Changing it does not alter
existing classes or migrate data. NFS, OSAC CSI File-policy classes (including
optional local CSI classes), and teardown do not consume it.

For deployment configuration, see the
[installer Helm example](../../osac-installer/docs/aap-configuration.md#block-storageclass-filesystem-type).

## Hosted cluster DNS

The CaaS endpoint workflow publishes API and ingress records under
`HOSTED_CLUSTER_BASE_DOMAIN`. The domain must also be listed in
`EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS`.

`DNS_ZONE` selects the Route 53 hosted zone used for those records. It must be
authoritative for the published names. Its default is
`EXTERNAL_ACCESS_BASE_DOMAIN`; set it explicitly when that default is not the
right zone for `HOSTED_CLUSTER_BASE_DOMAIN`. An explicitly configured parent
zone is preserved and can contain the hosted-cluster domain.
