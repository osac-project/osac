# Dedicated tenancy

> **Status: GA.** This is the tenancy model of the existing OSAC-managed
> path.

In the dedicated model, each OSAC tenant maps to its own storage-side tenant:

```
OSAC tenant "alpha"  -->  storage tenant (own credentials, own classes)
OSAC tenant "beta"   -->  storage tenant (own credentials, own classes)
```

Concretely:

- Each tenant gets its own storage credentials, provisioned by OSAC and kept
  in a tenant-scoped hub Secret.
- StorageClasses are tenant-specific and labeled with the tenant's name:
  `osac.openshift.io/tenant=<tenant-name>`.
- Isolation is enforced by the storage system's own tenant boundary:
  separate accounts, virtual machines, or pools, depending on the provider.

Dedicated tenancy is the default for
[OSAC-managed storage](../ownership/osac-managed.md): provisioning per-tenant
resources is exactly what the OSAC-managed automation does.

A dedicated CSP-managed profile (the CSP pre-provisions per-tenant storage
tenants and classes) is a possible future combination; it is not implemented.

## See also

- [Shared tenancy](shared.md): the contrasting model
- [StorageClasses and labels](../concepts/storageclass.md)
