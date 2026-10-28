# Tenant storage onboarding

What happens to a tenant's storage, from onboarding through teardown, in both
ownership models. For provider-specific setup steps, see the
[profile guides](../README.md#reading-paths).

## Onboarding sequence

```
CSP admin registers StorageBackend + StorageTiers
                │
                ▼
Tenant is created in OSAC
                │
                ▼
      ┌─────────────────────────────────┐
      │  OSAC-managed                   │  CSP-managed
      │  1. provision tenant creds      │  1. list shared classes on the
      │     (hub Secret)                │     target cluster
      │  2. create tenant classes       │  2. match one class per
      │     on the target cluster       │     configured tier
      │  3. verify classes              │  3. set StorageReady
      └─────────────────────────────────┘
                │
                ▼
Tenant status: conditions + resolved {name, tier} classes
                │
                ▼
Tenant users select tiers per disk when creating ComputeInstances
```

## What tenants see

Storage tiers are offered by name. A tenant user selecting a tier for a boot
disk or an additional disk gets a volume from the tier's resolved
StorageClass. The selection rules and defaults (request, catalog item,
template) are documented in
[Storage tier selection](../../guides/developer/computeinstance-guide.md#storage-tier-selection).

A tier cannot be selected until the administrator has configured it in OSAC.
If a tenant's storage onboarding is incomplete, a requested tier may have no
resolved StorageClass; workload disk provisioning then fails with an
actionable error. Hard-blocking ComputeInstance creation on completed
onboarding is in development.

## Checking readiness

Readiness is recorded on the Tenant resource in the management cluster:

```shell
# Conditions (mode-appropriate: StorageReady for CSP-managed;
# StorageBackendReady and ClusterStorageReady for OSAC-managed)
oc get tenant <tenant-name> -o jsonpath='{.status.conditions}'

# Resolved tier-to-class mapping
oc get tenant <tenant-name> -o jsonpath='{.status.storageClasses}'
```

Warning events on the Tenant (for example, duplicate-class detection) carry
the same details as the condition messages:

```shell
oc describe tenant <tenant-name>
```

For the meaning of each condition and its reasons, see
[concepts/readiness.md](../concepts/readiness.md).

## Adding a tier later

New tiers can be configured after a tenant is onboarded:

- OSAC reconciles the new tier on its next pass and records its resolved
  class in the tenant status.
- The latched `StorageReady` milestone is not reset by tier
  additions.
- A workload requesting the new tier uses its current resolution at
  provisioning time.

## Offboarding

When a tenant is deleted:

- **OSAC-managed:** OSAC tears down the tenant's StorageClasses and storage
  credentials.
- **CSP-managed:** OSAC removes its own records only. CSP-owned
  StorageClasses, CSI configuration, and the shared storage system are
  preserved.

## Troubleshooting

| Symptom | Likely cause | Where to look |
|---|---|---|
| Tenant onboarding incomplete (`NotFound`) | A configured tier has no matching StorageClass on the target cluster | `oc get tenant <name> -o jsonpath='{.status.conditions}'`; check the tier's class labels |
| Condition `False` with `MultipleFound` | More than one class matches a tier | Condition message names the duplicates; remove or relabel one |
| Readiness not evaluated; operator logs show target access errors | The operator's configured target is unreachable or access is missing | Operator logs; verify the target access configuration (the operator fails closed and never queries the local cluster as a fallback) |
| Tier not selectable by a tenant user | The tier is not configured in OSAC | `osac get storagetier` |
| Workload creation fails on a tier that previously worked | The tier's class changed or was removed after onboarding | Re-check `status.storageClasses`; provisioning uses the current resolved classes |

For NetApp-specific diagnostics, see
[guides/csp-managed-netapp-shared.md](csp-managed-netapp-shared.md).

## See also

- [Readiness and conditions](../concepts/readiness.md)
- [StorageClasses and labels](../concepts/storageclass.md)
- [Responsibility matrix](../architecture/responsibility-matrix.md)
