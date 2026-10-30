# StorageClasses and labels

OSAC resolves each [StorageTier](storagetier.md) to a Kubernetes
StorageClass on the target cluster where tenant workloads run. StorageClasses
are the concrete provisioning handle: when a tenant selects a tier for a disk,
the workload's volume is created from the resolved StorageClass.

OSAC identifies the StorageClasses it manages or consumes with two labels:

| Label | Meaning |
|---|---|
| `osac.openshift.io/tenant` | Which tenant the class serves |
| `osac.openshift.io/storage-tier` | Which tier the class fulfills |

The `tenant` label **value** depends on the tenancy model:

- **Dedicated** (`1 OSAC tenant = 1 storage tenant`): the class serves one
  OSAC tenant, and the value is that tenant's name.
- **Shared** (`many OSAC tenants = 1 storage tenant`): the class serves all
  tenants on a shared storage system, and the value is the constant
  `csp-managed`.

See [tenancy/dedicated.md](../tenancy/dedicated.md) and
[tenancy/shared.md](../tenancy/shared.md) for what each model implies.

Example of a shared, CSP-managed class for a `gold` tier:

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: ontap-gold
  labels:
    osac.openshift.io/tenant: csp-managed
    osac.openshift.io/storage-tier: gold
provisioner: csi.trident.netapp.io
# ...driver-specific parameters
```

## Resolution rules

During tenant onboarding and workload provisioning, OSAC lists the
StorageClasses on the target cluster and resolves each configured tier:

```
 list classes on the target cluster, per tier label
              │
   ┌──────────┼───────────────┬─────────────────┐
   ▼          ▼               ▼                 ▼
 exactly    no match      more than one    tier not configured
 one match                 match             in OSAC
   │          │               │                 │
   ▼          ▼               ▼                 ▼
 resolved   unresolved    ambiguous         ignored
 (recorded   (onboarding   (onboarding       (no effect on
  on tenant   incomplete)   incomplete)       readiness)
  status)
```

- **Exactly one** matching class per tier: the tier resolves, and the class
  name is recorded on the tenant status.
- **No** matching class: the tier is unresolved; onboarding stays incomplete.
- **More than one** matching class: the tier is ambiguous; onboarding stays
  incomplete, because OSAC cannot choose between duplicates.
- Classes for **unconfigured** tiers (no matching StorageTier in OSAC) are
  ignored and do not affect readiness.

Resolution is by labels only. OSAC does not inspect provisioners, parameters,
or the health of the backing storage.

## Ownership of StorageClasses

Who creates, updates, and deletes StorageClasses depends on the ownership
model:

- **OSAC-managed**: OSAC creates tenant-specific classes during tenant
  onboarding and deletes them when the tenant is removed.
- **CSP-managed**: the Cloud Provider Admin creates and manages the classes;
  OSAC only reads them. OSAC never creates, updates, or deletes CSP-owned
  classes, including during tenant teardown.

See [ownership/osac-managed.md](../ownership/osac-managed.md) and
[ownership/csp-managed.md](../ownership/csp-managed.md).

## See also

- [Readiness and conditions](readiness.md): how resolution results surface
  as tenant status
- [Tenant storage onboarding](../guides/tenant-onboarding.md): the
  end-to-end flow
