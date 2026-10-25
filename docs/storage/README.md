# Storage

OSAC lets cloud providers offer persistent storage to their tenants for virtual
machines (VMaaS) and clusters (CaaS). Tenants select a storage offering by
name when they create workloads; the provider decides how the underlying
storage is operated.

This area explains the storage concepts, the models that shape a deployment,
and the guides for administering and consuming storage.

> **Note:** The OSAC chart does not deploy a CSI driver itself. Vendor CSI
> drivers (for example, Trident for NetApp) are prepared and operated according
> to the [ownership model](#how-to-read-these-documents) selected for the
> deployment.

## How to read these documents

A storage deployment is described by three independent choices:

```
A storage setup  =  PROVIDER   x   OWNERSHIP        x   TENANCY
                    (netapp)       (csp-managed)       (shared)
                    (vast)         (osac-managed)      (dedicated)
```

- **Provider**: which storage vendor and CSI integration is used
  ([providers/](providers/))
- **Ownership**: who operates the storage: the cloud provider or OSAC
  ([ownership/](ownership/))
- **Tenancy**: how OSAC tenants map to storage-side tenancy
  ([tenancy/](tenancy/))

The concepts underneath (backend, tier, StorageClass, volume, file) are the
same regardless of these choices ([concepts/](concepts/)). A concrete
deployment is a *profile* that combines one choice from each dimension; the
[guides/](guides/) directory contains the profile walkthroughs.

## Status

| Area | Status |
|---|---|
| OSAC-managed storage (backends, tiers, provisioning) | GA |
| CSP-managed NetApp with a shared storage tenant (VMaaS) | Dev Preview |
| ComputeInstance volume lifecycle | Dev Preview |
| CSP-managed storage for providers other than NetApp | Planned |
| Dedicated-SVM CSP-managed profiles | Planned |
| CaaS tenant-cluster storage tiers | Planned |
| OSAC CSI routing meta-driver | Planned |
| File API | Planned |

## Reading paths

**New to OSAC storage**

1. [StorageBackend](concepts/storagebackend.md)
2. [StorageTier](concepts/storagetier.md)
3. [StorageClasses and labels](concepts/storageclass.md)
4. [Ownership models](ownership/osac-managed.md): start with
   [OSAC-managed](ownership/osac-managed.md), then
   [CSP-managed](ownership/csp-managed.md)
5. [Tenant storage onboarding](guides/tenant-onboarding.md)

**Cloud Provider Admin (configuring storage)**

1. [Ownership models](ownership/) and [tenancy models](tenancy/)
2. The provider page for your platform (today: [NetApp](providers/netapp.md))
3. The profile guide for your combination (today:
   [CSP-managed NetApp, shared](guides/csp-managed-netapp-shared.md))

**Tenant user (consuming storage)**

1. [StorageTier](concepts/storagetier.md): what a tier is
2. [Storage tier selection](../guides/developer/computeinstance-guide.md#storage-tier-selection):
   choosing a tier per disk when creating a VM
3. [Readiness and conditions](concepts/readiness.md): how to tell when
   storage is available

## Contents

- [concepts/](concepts/): the storage vocabulary: backend, tier,
  StorageClass, volume, file, readiness
- [ownership/](ownership/): OSAC-managed and CSP-managed ownership models
- [tenancy/](tenancy/): dedicated and shared tenant mapping
- [providers/](providers/): vendor integration requirements
- [architecture/](architecture/): how the OSAC storage control plane works
- [guides/](guides/): lifecycle guides and deployment profiles
