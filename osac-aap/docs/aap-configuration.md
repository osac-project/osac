# AAP Configuration

## Block StorageClass filesystem type

The storage provider uses `ext4` for `csi.storage.k8s.io/fstype` on new VAST
block and LVMS StorageClasses. Set `OSAC_STORAGE_BLOCK_FSTYPE` in the
storage-operations worker environment to override it, or pass
`storage_provider_block_fstype` when invoking `osac.service.storage_provider`.
An explicit Ansible variable takes precedence over the worker environment.
An explicit override must be nonempty when creating block classes and is
passed unchanged, without a filesystem allowlist or normalization. Choose a
value appropriate for your CSI driver. NFS classes and teardown operations do
not consume this setting.

This setting does not migrate existing StorageClasses or volumes. LVMS skips
existing class names; VAST skips provisioning when all requested classes
already exist and creates only missing block classes on partial requests.
If only NFS classes are missing, VAST does not validate the block filesystem
setting or resubmit existing block classes. Changing the setting does not
update an existing class's immutable filesystem parameters.

## Hosted cluster DNS

The CaaS endpoint workflow publishes API and ingress records under
`HOSTED_CLUSTER_BASE_DOMAIN`. The domain must also be listed in
`EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS`.

`DNS_ZONE` selects the Route 53 hosted zone used for those records. It must be
authoritative for the published names. Its default is
`EXTERNAL_ACCESS_BASE_DOMAIN`; set it explicitly when that default is not the
right zone for `HOSTED_CLUSTER_BASE_DOMAIN`. An explicitly configured parent
zone is preserved and can contain the hosted-cluster domain.
