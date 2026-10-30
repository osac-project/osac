# StorageTier

A StorageTier is the tenant-facing storage offering. It advertises a class of
storage service as a name tenants can select when creating workloads, without
exposing backend details. Tiers are admin-managed catalog resources: the
Cloud Provider Admin controls which tiers exist, and tenant users select them
by name.

```
StorageBackend "netapp-prod"        StorageBackend "vast-prod"
        │                                   │
        │ associated via                    │ associated via
        ▼                                   ▼
StorageTier "gold"                  StorageTier "silver"
  protocol: BLOCK                    protocol: BLOCK
  QoS: 500/200 MB/s                  QoS: 250/100 MB/s
                    │
                    │ selected by name, per disk
                    ▼
        ComputeInstance boot disk / additional disks
```

## Fields

| Field | Description |
|---|---|
| `description` | Free-text description of the advertised service level. |
| `protocol` | Storage protocol: `NFS` (file-based) or `BLOCK` (volume-based). Always explicit. |
| `backends[]` | Associations to backends that compose the tier. |

Each backend association records:

| Field | Description |
|---|---|
| `backendId` | The backend the tier is served from. |
| `maxReadBandwidthMBs` | Maximum read bandwidth in MB/s. |
| `maxWriteBandwidthMBs` | Maximum write bandwidth in MB/s. |
| `encryptionEnabled` | Whether data at rest is encrypted on this backend. |

> **Note:** These settings describe the tier's advertised service level.
> Whether they are enforced on the backing storage depends on the ownership
> model and the provider integration. OSAC-managed provisioning applies
> them where the provider role supports it (for example, QoS policies for
> VAST NFS tiers). CSP-managed storage is prepared entirely by the CSP;
> OSAC applies nothing.

> **Note on protocol:** the tier protocol distinguishes file-based (`NFS`)
> from volume-based (`BLOCK`) access. Transport-level details (Fibre
> Channel, iSCSI, NVMe/TCP, Ethernet) are provider data-plane choices and
> are not part of the OSAC API. See
> [providers/netapp.md](../providers/netapp.md) for supported transports.

## Status

| State | Meaning |
|---|---|
| `ACTIVE` | The tier is available for tenants to select. |

## Tier availability

Tiers are deployment-wide catalog resources. Every tier configured in OSAC is
offered to tenants; a tenant cannot select a tier until it has been configured
by the administrator. When a tenant is onboarded, OSAC reconciles storage for
*all* configured tiers (see [readiness](readiness.md)).

## Managing tiers

```shell
osac create storagetier --name gold \
  --backend-id <backend-id> \
  --protocol BLOCK \
  --max-read-bandwidth-mbs 500 \
  --max-write-bandwidth-mbs 200

osac describe storagetier gold
osac get storagetier
```

## Selecting a tier

Tenant users select a tier per disk when creating a ComputeInstance:
`boot_disk.storage_tier` for the boot disk and
`additional_disks[i].storage_tier` for each additional disk. Boot and
additional disks can use different tiers. See
[Storage tier selection](../../guides/developer/computeinstance-guide.md#storage-tier-selection)
for the full resolution order, including catalog-item and template defaults.

## See also

- [StorageBackend](storagebackend.md): what a tier references
- [StorageClasses and labels](storageclass.md): how a tier resolves to a
  concrete StorageClass
- [Readiness and conditions](readiness.md): tier readiness at tenant
  onboarding
