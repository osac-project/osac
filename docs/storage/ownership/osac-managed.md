# OSAC-managed storage

> **Status: GA.** This is the default ownership model. Omitting `cspManaged`
> (or setting it to `false`) selects it.

In the OSAC-managed model, OSAC provisions and manages the storage resources
that tenants consume. The Cloud Provider Admin registers the storage system
and its credentials; OSAC does the rest.

## What OSAC manages

For each tenant using an OSAC-managed backend, OSAC:

- Provisions the tenant's storage credentials on the storage system and
  stores them as a Secret in the management cluster (the
  `StorageBackendReady` signal).
- Creates tenant-specific StorageClasses on the target cluster, labeled with
  the tenant's name (the `ClusterStorageReady` signal). See
  [tenancy/dedicated.md](../tenancy/dedicated.md).
- Tears down these resources when the tenant is deleted.

Provisioning runs through OSAC's automation provider integration; the
operator dispatches the backend and tier definitions to provider roles.

## Requirements

- The StorageBackend **must** include a management `endpoint` and
  `credentials`. See [concepts/storagebackend.md](../concepts/storagebackend.md)
  for the validation rules.
- The management endpoint must be reachable from the management cluster's
  automation infrastructure.
- The credentials must have sufficient rights on the storage system to
  create per-tenant resources.
- CSI driver installation depends on the provider integration. The VAST
  provider automation installs and operates the VAST CSI operator on the
  target cluster as part of tenant provisioning; other provider
  integrations may require the CSI driver to be prepared externally.

## Credentials

Credentials are required and actively used: OSAC resolves them when
provisioning tenant storage. Rotation happens by updating the backend or its
referenced Secret; the next reconciliation uses the new value. See
[concepts/storagebackend.md](../concepts/storagebackend.md#rotation).

## Readiness

OSAC-managed readiness is reported through the `StorageBackendReady` and
`ClusterStorageReady` conditions. See
[concepts/readiness.md](../concepts/readiness.md).

## See also

- [Responsibility matrix](../architecture/responsibility-matrix.md): a
  side-by-side comparison with CSP-managed storage
- [Storage control plane](../architecture/control-plane.md)
