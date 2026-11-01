# CSP-managed storage

> **Status: Dev Preview.** Runtime support is currently validated for NetApp
> on the VMaaS profile; see [providers/netapp.md](../providers/netapp.md).
> The API field is provider-neutral, and the onboarding check itself is
> generic. Only NetApp is tested and supported in this release; other
> providers are untested.

In the CSP-managed model, the Cloud Provider Admin (CSP) operates the storage
system and prepares everything tenants need before onboarding. OSAC verifies
that the expected resources exist and consumes them read-only.

Set `cspManaged: true` on the StorageBackend to select this mode. The field
is immutable after creation.

## What the CSP prepares

Before tenants consume CSP-managed storage, the CSP:

- Operates the storage system (for example, a NetApp cluster and its storage
  virtual machines).
- Installs and operates the vendor CSI driver on the target cluster (for
  example, Trident).
- Configures the CSI backend (for example, a shared TridentBackendConfig).
- Creates one StorageClass per offered tier, labeled
  `osac.openshift.io/tenant=csp-managed` and
  `osac.openshift.io/storage-tier=<tier>`. See
  [concepts/storageclass.md](../concepts/storageclass.md) and
  [tenancy/shared.md](../tenancy/shared.md).
- Configures the OSAC operator's access to the target cluster. The operator
  checks the configured target explicitly and fails closed; it never falls
  back to querying the local cluster. (Configuring a *remote* target's
  access for Helm-based deployments is in development; follow the
  installation guide for the current deployment options.)

Provider-specific prerequisites are listed on the provider pages.

## What OSAC does and does not do

OSAC:

- Reads the CSP-provided StorageClasses on the target cluster.
- Resolves each configured tier to exactly one matching class.
- Reports onboarding readiness through the `StorageReady`
  condition (see [concepts/readiness.md](../concepts/readiness.md)).
- Passes the resolved tier-to-class mapping to workload provisioning.

OSAC does **not**:

- Create, update, or delete CSP-owned StorageClasses, CSI configuration, or
  the storage system itself, including during tenant teardown.
- Use backend management credentials, even if supplied. Endpoint and
  credentials are optional on a CSP-managed backend and are never used on the
  CSP-managed runtime path.
- Require a hub Secret or any storage-provider automation job for
  CSP-managed readiness.

This readiness check verifies StorageClass presence only. It does not
validate the transport, the CSI driver's health, or the backing system.

## Supported boundaries (Dev Preview)

- **Provider:** NetApp only. A NetApp backend must be CSP-managed: the API
  rejects `cspManaged: false`, including when the field is omitted, since
  the default is `false`. Other providers can be registered as CSP-managed
  and follow the same generic onboarding check, but are untested and
  unsupported in this release.
- **Profile:** VMaaS (virtual machines). CaaS and BMaaS consumption is
  planned.
- **Tenancy:** shared storage tenant. Dedicated-SVM profiles are planned.

## See also

- [CSP-managed NetApp, shared](../guides/csp-managed-netapp-shared.md): the
  Dev Preview profile guide
- [Responsibility matrix](../architecture/responsibility-matrix.md)
- [StorageBackend](../concepts/storagebackend.md): the `cspManaged` field
