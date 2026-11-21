# AAP Configuration

AAP workers read runtime configuration from their instance-group environment.
The storage provider also accepts explicit Ansible variables when invoked directly.

## Block StorageClass filesystem type

New native VAST, LVMS/TopoLVM, and direct Trident block StorageClasses use
`ext4` by default. Set
`OSAC_STORAGE_BLOCK_FSTYPE` in the storage worker environment, or pass
`storage_provider_block_fstype` to Ansible. The explicit variable takes precedence.
The value must be a nonempty string; it is passed unchanged, without trimming or
an allowlist. Select a filesystem supported by the target CSI driver.

The setting is consumed only when creating a new block class. NFS, teardown,
and requests that need no new block class do not validate it. Existing classes
retain their filesystem parameters, including an absent parameter; changing the
setting does not migrate classes, PVCs, PVs, or data. Use a separately named class
for future claims and plan workload/data migration separately.

This addresses missing PV filesystem metadata when kubelet uses
`ReadWriteOnceWithFSType` and the native node driver does not advertise
`VOLUME_MOUNT_GROUP`. A driver's internal filesystem default does not by itself
populate the PV's `spec.csi.fsType`. The pinned installer TopoLVM chart 17.2.0
(node 0.41.1), VAST v2.6.8, and Trident v25.10.0 default-policy sources support
this configuration. Native OLM channels and preinstalled drivers are not fixed
to those versions: check the installed CSIDriver policy, node capabilities,
provisioner arguments, and PV metadata before diagnosing a deployment.

OSAC CSI classes are exempt from this specific missing-fsType ownership defect:
the public CSI chart sets `fsGroupPolicy: File`, which allows kubelet fsGroup
handling without an fsType. This includes the pre-existing bare `netapp` and
`pure` dispatch routes, OSAC CSI aliases, and optional local CSI classes created
by `csi_driver_install`. Those local classes use `csi.osac.openshift.io`, not
native `topolvm.io`. They do not consume this filesystem setting and retain
their original provisioning and reconciliation behavior. The `netapp-storage`
provider instead dispatches to the native Trident `netapp_storage` role. File policy
is not a guarantee against unrelated filesystem or permissions problems.

After creating missing VAST block classes, the provider checks that all requested
block class names exist before returning them. If a class disappeared, it fails
with the missing names; it does not recreate existing classes or retry. This is
a point-in-time completion check, not a guarantee against deletion after the
check. All-existing and NFS-only creation paths do not perform this extra check.

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
