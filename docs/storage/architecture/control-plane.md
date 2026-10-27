# Storage control plane

How OSAC's components implement storage, from registration to a tenant's
disk.

## Components

| Component | Role in storage |
|---|---|
| Fulfillment API | Stores StorageBackends, StorageTiers, and tenant records; serves them to the operator and CLI. |
| osac-operator | Reconciles per-tenant storage: resolves tiers to StorageClasses, computes readiness conditions, and dispatches provisioning. |
| Automation provider (AAP) | Executes provider integrations for OSAC-managed storage: backend provisioning, tenant credentials, StorageClass creation and teardown. |
| Target cluster | Where tenant workloads run; hosts the StorageClasses and CSI driver, and provisions the actual volumes. |

## Data flow

```
              management cluster                         target cluster
  ┌──────────────────────────────────────────┐    ┌───────────────────────────┐
  │                                          │    │                           │
  │  Cloud Provider Admin                    │    │  Vendor CSI driver        │
  │    │                                     │    │  (e.g., Trident)          │
  │    v                                     │    │    │                      │
  │  Fulfillment API                         │    │    v                      │
  │    StorageBackend, StorageTier           │    │  StorageClasses           │
  │         │                                │    │  (labeled per tenancy)    │
  │         v                                │    │        ▲                  │
  │  Tenant CR ──► osac-operator ◄───────────┼────┼────────┘                  │
  │  (conditions,   StorageReconciler        │    │  (read-only listing;      │
  │   resolved       lists classes,          │    │   OSAC-managed also       │
  │   classes)       computes readiness)     │    │   creates/deletes)        │
  │                     │                    │    │                           │
  │                     v                    │    │                           │
  │  AAP (OSAC-managed only):                │    │  ComputeInstance disks    │
  │  backend + class provisioning/teardown   │    │  provisioned from the     │
  │                                          │    │  resolved classes         │
  └──────────────────────────────────────────┘    └───────────────────────────┘
```

## Reconciliation

The operator's storage reconciler runs for each tenant:

1. **Collect tier definitions.** All configured StorageTiers are listed and
   partitioned by their backend's ownership mode into OSAC-managed and
   CSP-managed sets. Backend management credentials are resolved only for
   OSAC-managed backends.
2. **Resolve StorageClasses.** The reconciler lists StorageClasses on the
   operator's explicitly configured target cluster (with no
   local-cluster fallback) and matches them against the tier sets using the
   [label contract](../concepts/storageclass.md). CSP-managed tiers match
   classes labeled `osac.openshift.io/tenant=csp-managed`; OSAC-managed tiers
   match classes labeled with the tenant's name.
3. **Update status.** Resolved `{name, tier}` pairs are stored on the Tenant
   status, and the mode-appropriate conditions are set (see
   [readiness](../concepts/readiness.md)).
4. **Dispatch provisioning (OSAC-managed only).** OSAC-managed tiers and
   backend connections are sent to the automation provider. CSP-managed
   tiers are excluded from provider dispatch and from credential forwarding.

StorageClass changes on the target cluster re-trigger reconciliation for the
affected tenants.

## Status projection

The operator's Tenant conditions are projected into the Fulfillment API's
private tenant status. Readiness is computed once, by the operator; the
Fulfillment projection does not recalculate it. There is no public
tenant-status API for storage readiness today.

## Volume creation

When a ComputeInstance is created, each disk's selected tier is resolved
against the current StorageClass mapping and the resulting class is used for
the disk's volume. See [guides/volume-lifecycle.md](../guides/volume-lifecycle.md).

## See also

- [Responsibility matrix](responsibility-matrix.md)
- [Tenant storage onboarding](../guides/tenant-onboarding.md)
