# NetApp backend and tier registration

The Developer Preview uses an administrator-created, shared Trident backend on
one VM hosting cluster. OSAC registers its reference and creates tenant-specific
StorageClasses selecting its prepared virtual pools. The administrator owns the
SVM, TridentBackendConfig (TBC), credentials, TLS trust, QoS and encryption settings.

Registration and the operator's shared-profile payload are implemented on the
feature branch. The matching AAP receiver is separate work; native onboarding
requires that receiver. See the [onboarding interface](../developer/netapp-onboarding-contract.md)
and [manual validation guide](netapp-manual-validation.md).

## Before registration

The Cloud Infrastructure Admin prepares:

- Trident and its CRDs on the actual VM hosting cluster, with the required FC
  worker/HBA, zoning and multipath configuration.
- An ONTAP SVM and native credentials, then a TBC using `ontap-san` and
  `sanType: fcp`. Its management endpoint, account permissions and server
  certificate trust belong in the native Trident configuration.
- Named virtual pools and their performance/encryption defaults. For example,
  a pool labelled `performance: extreme` uses the administrator's adaptive QoS
  policy. Pool labels select storage; they do not authorize tenants.

A TBC is a namespaced Kubernetes object. Its `metadata.name` can differ from
Trident's native `backendName`. OSAC records the TBC namespace/name; AAP must
resolve the bound native backend name when generating StorageClasses.
See NetApp's [TBC lifecycle](https://docs.netapp.com/us-en/trident-2510/trident-use/backend-kubectl.html)
and [SAN configuration examples](https://docs.netapp.com/us-en/trident-2510/trident-use/ontap-san-examples.html).

Backend registration still requires the existing HTTPS endpoint, username and
exactly one password source. These compatibility fields remain stored, but the
shared-profile operator does not resolve or send them to AAP. Trident uses the
credentials and endpoint already configured in the customer-managed TBC.
Fulfillment and AAP make no direct ONTAP connection.

## 1. Create the backend password reference

Use an OSAC Secrets Service VALUE Secret for the password, or supply an inline
password through the existing API. VALUE means a single non-empty byte value
under `data["value"]`. It is different from a native Kubernetes Secret.

After private API login, the CLI can read exact password bytes from stdin:

```bash
osac --tenant shared create secret --name netapp-catalog-password --type value \
  --from-file=value=- < /path/to/protected/password
```

Record the returned OSAC Secret ID. Registration checks reference visibility,
existence, type and lifecycle; it does not fetch the protected password or test
it against ONTAP. Shared/system Secret read restrictions remain unchanged.
The shared-profile onboarding path does not consume this password.

## 2. Register the shared backend

Edit the [backend example](examples/netapp-backend.yaml). Its new section is:

```yaml
ontap:
  profiles:
    - name: shared-fc
      shared:
        tbcNamespace: trident
        tbcName: customer-fc
```

This is `spec.ontap`, beside `provider`, `endpoint` and `credentials`. It selects
one existing TBC in the VM hosting cluster chosen by deployment configuration;
it does not select a cluster. Profile names must be unique DNS labels. The API
supports shared profiles now; a dedicated mode requires a later typed branch.

```bash
osac create -f netapp-backend.yaml
osac get storagebackend netapp-primary -o json
```

Record the returned backend ID. A valid request persists as READY without an
ONTAP request. **READY means accepted catalog configuration.** It does not
prove the TBC exists, its credentials work, or FC disk I/O is available.

## 3. Register tiers selecting prepared pools

Edit the [tier example](examples/netapp-tier.yaml) with that backend ID:

```yaml
backends:
  - backendId: registered-backend-id
    encryptionEnabled: true
    ontap:
      profile: shared-fc
      poolSelector:
        performance: extreme
```

```bash
osac create -f netapp-tier.yaml
osac get osac.private.v1.StorageTier netapp-block-extreme -o json
```

ONTAP requires one existing backend and BLOCK protocol. A shared association
must name a registered profile and supply a non-empty pool selector. Generic
bandwidth limits and `ontap.maxIops` must be zero/absent: the prepared virtual
pool supplies its native QoS. Selector entries are literal label equalities,
not arbitrary Trident expressions.

`encryptionEnabled` remains the existing advertised tier setting. The admin must
ensure the selected pool's native encryption configuration matches that claim.
OSAC stores and forwards the boolean; it does not change the TBC, choose NAE/NVE,
or verify encryption on the array. For encrypted offerings, prepare and qualify
an encrypted pool before advertising the tier.

A second tier may select another pool on the same profile. Both use the same
customer TBC. The public tenant tier API omits backend/provider details; the
fully qualified private type above shows them.

## 4. Onboard a tenant

Normal Tenant creation starts identity onboarding. Once the Tenant is Ready,
the storage operator resolves the catalog and dispatches the two storage stages:

1. AAP observes the referenced TBC through the hosting cluster's Kubernetes API.
2. AAP creates tenant/tier StorageClasses selecting that bound backend and pools.

The shared path needs **no per-tenant preparation Secret**. Successful jobs for
the current configuration and observed classes establish the separate
`StorageBackendReady` and `ClusterStorageReady` conditions. AAP's receiver must
implement these steps; sender capture alone does not establish readiness.
Offboarding removes only OSAC-owned tenant configuration under the existing
lifecycle guards. Customer-managed TBCs, Secrets, SVMs and policies remain.

## Updates and failures

Descriptions and credentials remain editable. Provider, ONTAP endpoint and
profile definitions are immutable; changing those bindings requires a replacement
backend. ONTAP tier backend/protocol/QoS/encryption bindings likewise require a
replacement tier. Existing update-mask and optimistic-lock behavior applies.
An accepted backend Update clears a historical probe ERROR/message.

| Failure | Result / action |
|---|---|
| Malformed endpoint/profile/selector, missing credentials, invalid password reference | API rejects without saving; correct the input |
| Unknown profile, empty selector or nonzero shared IOPS cap | API rejects the tier; select an existing profile/pool |
| Unreachable array, invalid native password or untrusted ONTAP certificate | Catalog registration still succeeds; inspect TBC/Trident status on the hosting cluster |
| Missing/unbound TBC or failed AAP/class creation | Tenant storage stays not ready; correct native preparation or receiver configuration and retry |

## Compatibility and verification limits

Legacy ONTAP backends without profiles keep the earlier prepared-SVM path;
existing providers retain their payloads. New shared-profile requests require
matching API/operator/AAP versions; do not send them to the old prepared-SVM role.

Local API/database and operator tests cover registration, payloads and lifecycle
gates. The edge-17 shared-path test uses real Kubernetes/AAP with catalog fixtures
and a capture-only playbook: two pool selections pass, credentials/preparation are
absent, and no ONTAP requests or Trident creations occur. Native TBC observation,
StorageClass creation, encrypted FC volumes and VM I/O remain joint qualification.
