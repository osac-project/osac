# StorageBackend

A StorageBackend registers a storage system with OSAC. It is the
administration unit that records *what* storage is available and *how* it is
operated. Backends are created and managed by the Cloud Provider Admin through
the private API; tenants never interact with backends directly.

```
StorageBackend "netapp-prod"
   provider:      netapp
   cspManaged:    true | false
   endpoint:      management API (optional in CSP-managed mode)
   credentials:   management user (optional in CSP-managed mode)
        │
        │ referenced by
        ▼
   one or more StorageTiers
```

One backend can back many tiers. For example, a single NetApp cluster with a
shared storage virtual machine (SVM) can expose `gold`, `silver`, and `bronze`
tiers that all reference the same backend.

## Ownership mode

Every backend has an ownership mode, set by the `cspManaged` field:

| Field | `cspManaged: false` (OSAC-managed) | `cspManaged: true` (CSP-managed) |
|---|---|---|
| Who operates the storage | OSAC provisions and manages it | The Cloud Provider Admin operates it |
| `endpoint` | Required | Optional |
| `credentials` | Required | Optional; never used by OSAC even if supplied |
| StorageClass lifecycle | Created and deleted by OSAC | Created and managed by the CSP |

The field is immutable after creation: to change ownership, create a new
backend. The field is provider-neutral in the API, with one exception:
a NetApp backend must set `cspManaged: true`. The API rejects an
OSAC-managed NetApp backend, including when the field is omitted, since
the default is `false`. CSP-managed storage for providers other than
NetApp can be registered but is untested in this release (see
[providers/netapp.md](../providers/netapp.md)).

## Fields

| Field | Description |
|---|---|
| `provider` | Storage provider identifier (for example `netapp`, `vast`). Immutable. |
| `description` | Free-text description. |
| `endpoint` | Management endpoint of the storage system. Required for OSAC-managed backends. |
| `credentials` | Management credentials (see below). Required for OSAC-managed backends. |
| `cspManaged` | Ownership mode. Defaults to `false`. Immutable. |

## Credentials

Credentials authenticate against the storage system's *management* API. They
consist of a `username` and exactly one of:

- `password`: an inline value, or
- `passwordSecret`: a reference to a Secret of type `VALUE` in the same
  tenant, whose `value` data key holds the password.

Validation rules:

- OSAC-managed backends require both `endpoint` and `credentials`.
- CSP-managed backends may omit both. If credentials are supplied, they must
  still be complete and valid, but OSAC never uses them on the CSP-managed
  runtime path.
- `password` and `passwordSecret` are mutually exclusive.

### Rotation

To rotate credentials on an OSAC-managed backend, update the backend (or the
referenced Secret) through the private API. OSAC resolves the current
credential value whenever it provisions storage, so the next reconciliation
uses the new value; already-provisioned resources are not recreated.

For CSP-managed backends, credential management happens entirely outside OSAC.

## Status

| State | Meaning |
|---|---|
| `READY` | The backend is registered and available. |

## Managing backends

```shell
# OSAC-managed backend
osac create storagebackend --name vast-prod \
  --provider vast \
  --endpoint https://storage.example.com:8443 \
  --username osac-provisioner \
  --password '<password>'

# CSP-managed backend (no endpoint or credentials required)
osac create storagebackend --name netapp-prod \
  --provider netapp \
  --csp-managed

osac describe storagebackend netapp-prod
```

Use a dedicated, least-privilege management account for OSAC-managed
backends rather than a shared administrator identity. The example uses a
placeholder password; the API also accepts a Secret reference
(`passwordSecret`) instead of an inline value.

## See also

- [StorageTier](storagetier.md): how tiers reference a backend
- [Ownership models](../ownership/osac-managed.md): what each mode means in
  practice
- [Storage control plane](../architecture/control-plane.md): how backends
  flow through OSAC components
