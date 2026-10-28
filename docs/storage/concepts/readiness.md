# Readiness and conditions

OSAC reports storage readiness on the Tenant resource in the management
cluster. Conditions distinguish "storage is ready" from "storage could not be
checked", and they are the primary signal for troubleshooting onboarding
problems.

> **Note:** Readiness checks verify **StorageClass presence** on the target
> cluster (see [storageclass.md](storageclass.md)). They do not validate the
> transport (Fibre Channel fabric, iSCSI network, Ethernet), the CSI driver's
> health, or the backing storage system. Transport and driver health remain
> the storage operator's responsibility.

## Conditions by ownership mode

| Condition | Mode | Meaning |
|---|---|---|
| `StorageBackendReady` | OSAC-managed | The tenant's backend credentials have been provisioned (the hub Secret exists). |
| `ClusterStorageReady` | OSAC-managed | StorageClasses are installed on the target cluster for this tenant. |
| `StorageReady` | CSP-managed | All expected CSP-managed classes for the configured tiers exist on the target cluster. |

> **Status: Dev Preview.** `StorageReady` is the newer, latched
> onboarding signal introduced with CSP-managed storage. The
> OSAC-managed conditions above are unchanged, and unifying readiness
> reporting across modes is planned.

## StorageReady semantics

`StorageReady` is a **latched initial-onboarding milestone**:

```
 onboarding              every configured tier has
   not yet                exactly one matching class
   complete   ──────────────────────────────────►  True (latched)
     │                                             │
     │ NotFound (a tier has no class)              │ stays True when
     │ MultipleFound (a tier has 2+ classes)       │ tiers are added
     ▼                                             ▼
   False   ◄── re-checked until resolved           milestone stays True
```

- It becomes `True` when every initially configured CSP-managed
  [StorageTier](storagetier.md) resolves to exactly one matching
  StorageClass on the target cluster.
- It **stays** `True` afterwards. Adding a tier later, or a class changing,
  does not reset the milestone.
- Latching means the condition proves the *initial* onboarding succeeded.
  It does not prove that every class remains present forever. Workload provisioning
  consumes the tenant's current resolved classes; strict gating of
  ComputeInstance creation on this condition, with a live check of the
  requested tier, is in development.

When the check completes but storage is not ready, the condition is `False`
with one of these reasons:

| Reason | Meaning |
|---|---|
| `NotFound` | At least one configured tier has no matching StorageClass. |
| `MultipleFound` | At least one configured tier matches more than one StorageClass. |

The condition message names the affected tier and, for duplicates, the
matching class names. Kubernetes events on the Tenant record the same details.

If OSAC cannot query the target cluster at all (for example, the target
access is missing or the API is unreachable), the check has not completed.
This is reported separately from a confirmed missing class.

## Inspecting readiness

Readiness is recorded on the operator's Tenant custom resource in the
management cluster:

```shell
oc get tenant <tenant-name> -o jsonpath='{.status.conditions}'
```

Resolved tier-to-class mappings are also recorded on the tenant status:

```shell
oc get tenant <tenant-name> -o jsonpath='{.status.storageClasses}'
# e.g. [{"name":"ontap-gold","tier":"gold"},{"name":"ontap-silver","tier":"silver"}]
```

Tenant users do not read these conditions through a public API today; storage
readiness is surfaced to administrators on the management cluster.

## See also

- [Tenant storage onboarding](../guides/tenant-onboarding.md)
- [Storage control plane](../architecture/control-plane.md): which component
  computes and projects readiness
