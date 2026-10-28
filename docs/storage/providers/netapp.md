# NetApp

> **Status: Dev Preview (CSP-managed).** NetApp is the provider of the
> CSP-managed Dev Preview profile, and NetApp backends must be
> CSP-managed: the API rejects `cspManaged: false`, including when the
> field is omitted, since the default is `false`. OSAC-managed NetApp
> support is planned.

NetApp storage is integrated through ONTAP and the Trident CSI driver:

```
NetApp ONTAP cluster
  └── storage virtual machine (SVM)
        └── virtual storage pools  <->  OSAC StorageTiers
                    |
                    v
Trident CSI driver (on the VMaaS target cluster)
  ├── backend config (TridentBackendConfig) pointing at the SVM
  └── StorageClasses labeled per the OSAC contract
```

## Requirements

Preparing NetApp storage for OSAC involves:

1. **ONTAP cluster** with a storage virtual machine (SVM) serving the tenants
   that will consume storage. In the shared-tenancy profile, one SVM serves
   all tenants (see [tenancy/shared.md](../tenancy/shared.md)).
2. **Virtual storage pools** on the SVM that back the offered tiers. Each
   pool's service characteristics (for example, media type and QoS) map to an
   OSAC StorageTier.
3. **Transport** between the VMaaS target cluster's hosts and the SVM:
   Fibre Channel or iSCSI for block volumes, Ethernet for NFS. Zoning,
   networking, and multipathing are configured and validated by the storage
   operator. OSAC does not validate the transport (see below).
4. **Trident CSI operator** installed and operated on the VMaaS target
   cluster. OSAC does not install or operate Trident.
5. **TridentBackendConfig** connecting Trident to the SVM and its pools. In
   the shared-tenancy profile, one shared backend config serves all tenants.
6. **StorageClasses**: one per offered tier, carrying the OSAC labels (see
   [StorageClass contract](#storageclass-contract)) and the
   `csi.trident.netapp.io` provisioner.

Refer to the
[NetApp Trident documentation](https://docs.netapp.com/us-en/trident/index.html)
for installation and configuration steps; Trident installation and operation
belong to the ownership model chosen for the deployment (see
[ownership/](../ownership/)).

## Protocols and data plane

The OSAC API distinguishes only `NFS` (file) and `BLOCK` (volume) at the
tier level. The concrete transport is a Trident driver choice on the target
cluster:

| OSAC tier protocol | Trident driver | Transports |
|---|---|---|
| `BLOCK` | `ontap-san` | Fibre Channel, iSCSI |
| `BLOCK` | `ontap-san-economy` | iSCSI |

The supported NetApp profile provides block storage only. NFS-backed tiers
are not supported for NetApp in this release, even though the tier
protocol exists in the API.

OSAC's readiness check verifies **StorageClass presence only**. It does not
validate Fibre Channel zoning, iSCSI networking, the SVM's health, or
Trident's operational state. Transport validation remains with the storage
operator.

## StorageClass contract

OSAC resolves NetApp-backed tiers by label, regardless of the driver or
parameters. For the shared-tenancy CSP-managed profile, each class carries:

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: ontap-gold
  labels:
    osac.openshift.io/tenant: csp-managed
    osac.openshift.io/storage-tier: gold
provisioner: csi.trident.netapp.io
parameters:
  backendType: ontap-san
  # ... Trident parameters selecting the SVM's pool for this tier
```

Exactly one class must exist per configured tier. See
[concepts/storageclass.md](../concepts/storageclass.md) for the resolution
rules and the full label contract.

## Dev Preview boundaries

- **Ownership:** CSP-managed only. OSAC-managed NetApp is planned.
- **Profile:** VMaaS. CaaS and BMaaS consumption is planned.
- **Tenancy:** shared SVM. Dedicated-SVM profiles are planned.

See [guides/csp-managed-netapp-shared.md](../guides/csp-managed-netapp-shared.md)
for the end-to-end profile guide.
