# Shared tenancy

> **Status: Dev Preview.** Shared tenancy is used by the CSP-managed NetApp
> profile; see [guides/csp-managed-netapp-shared.md](../guides/csp-managed-netapp-shared.md).

In the shared model, many OSAC tenants map to a single storage-side tenant:

```
OSAC tenant "alpha" --\
OSAC tenant "beta" ---->  one shared storage tenant (e.g., one SVM)
OSAC tenant "gamma" --/        |
                               +-- one shared StorageClass per tier,
                                   labeled osac.openshift.io/tenant=csp-managed
```

Concretely:

- One storage system partition (for example, one NetApp SVM) serves all
  tenants on it.
- One shared StorageClass per tier carries the label value `csp-managed`
  instead of a tenant name. Every tenant on the shared system resolves the
  same class for a given tier.
- The storage operator (the CSP) is responsible for enforcing tenant
  isolation *within* the shared storage tenant, for example through the
  provider's own quota and access controls.

> **Important:** The `osac.openshift.io/tenant=csp-managed` label is a
> **resolution convention**, not a security boundary. It tells OSAC which
> shared classes to consume for tier resolution; it does not isolate tenants
> from each other. Isolation within the shared storage tenant is the storage
> operator's responsibility.

Shared tenancy is used with [CSP-managed storage](../ownership/csp-managed.md):
the CSP prepares the shared system and classes once, and every onboarded
tenant consumes them. Combined with OSAC-managed storage, shared classes
would need to be provisioned per tenant differently and are not supported in
this phase.

## See also

- [Dedicated tenancy](dedicated.md): the contrasting model
- [StorageClasses and labels](../concepts/storageclass.md)
