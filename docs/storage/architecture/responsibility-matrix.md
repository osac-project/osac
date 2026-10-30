# Responsibility matrix

Who creates, reads, updates, and deletes each storage resource, side by side
for the two ownership models. "CSP" is the Cloud Provider Admin; "OSAC" is
the platform (Fulfillment API, operator, automation provider).

## Resource ownership

| Resource | OSAC-managed | CSP-managed |
|---|---|---|
| Storage system (array, SVMs, pools) | CSP-owned; OSAC provisions tenants on it via API | CSP-owned; OSAC never touches it |
| Vendor CSI driver | Provider-dependent: installed by the provider automation for VAST, otherwise prepared externally | Installed and operated by the CSP |
| CSI backend config (e.g., TBC) | Per-tenant, provisioned by OSAC | Shared, configured by the CSP |
| StorageBackend record | Created by CSP admin; includes endpoint + credentials | Created by CSP admin; `cspManaged: true`, endpoint/credentials optional |
| Management credentials | Required; used by OSAC; rotated through OSAC | Optional; never used by OSAC; rotated outside OSAC |
| Hub Secret (tenant storage credentials) | Created by OSAC | Not required |
| StorageClasses on target | Created and deleted by OSAC, per tenant | Created and managed by the CSP; read-only for OSAC |
| StorageTier records | Created by CSP admin | Created by CSP admin |
| Tenant resources (namespace, CRs) | Created by OSAC | Created by OSAC |

## Actions by lifecycle phase

| Phase | OSAC-managed | CSP-managed |
|---|---|---|
| **Setup** | CSP admin registers backend (endpoint + credentials) and tiers; CSP prepares the array | CSP prepares array, CSI driver, backend config, and labeled classes; CSP admin registers backend (`cspManaged: true`) and tiers |
| **Tenant onboarding** | OSAC provisions tenant credentials (Secret), creates tenant classes, verifies classes | OSAC lists the shared classes, verifies one per tier, sets `StorageReady` |
| **Workload provisioning** | OSAC resolves the requested tier's class; JIT provisioning may run through provider automation | OSAC resolves the requested tier's class from the CSP-provided classes; no provider dispatch |
| **Credential rotation** | Update the backend or Secret through OSAC; next reconciliation uses the new value | Entirely outside OSAC |
| **Tenant teardown** | OSAC deletes tenant classes and credentials | OSAC removes its own records only; CSP-owned classes and CSI config are preserved |
| **Class changes** | OSAC reconciles its own classes | CSP applies changes; OSAC re-resolves on its next reconciliation |

## Guarantees

- In CSP-managed mode, OSAC never creates, updates, or deletes CSP-owned
  resources (StorageClasses, CSI configuration, the shared backend, or the
  storage system), including during tenant teardown.
- CSP-managed readiness never depends on hub Secrets, backend credentials, or
  storage-provider automation jobs.
- The operator checks its explicitly configured target cluster and fails
  closed on access problems; it never falls back to the local cluster.

## See also

- [Ownership models](../ownership/osac-managed.md)
- [Storage control plane](control-plane.md)
