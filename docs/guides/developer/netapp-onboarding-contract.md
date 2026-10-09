# NetApp registration and onboarding contract

This contract connects backend/tier registration to prepared-SVM tenant
onboarding for the developer preview. Registration, the operator request
mapping, and Fulfillment storage-status projection are implemented on the
`OSAC-5813-netapp-backend-tiers` branch. The receiving AAP role and native
resource lifecycle remain onboarding implementation work.

For administrator requests and TLS preparation, see
[NetApp backend and tier registration](../admin/netapp-backend-tiers.md).

## Registration output

`StorageBackendSpec` keeps its existing fields. Use `provider: ontap`, the
cluster management HTTPS endpoint, and discovery credentials. A successful
registration stores `READY` only after seven bounded authenticated GETs.
This certifies management discovery; it does not certify a tenant SVM, native
Trident installation, FC zoning, usable capacity, or VM disk access.

The private tier association supports:

```yaml
backendId: registered-backend-id
encryptionEnabled: true
ontap:
  maxIops: "5000"
```

`ontap` is the selected `provider_qos` oneof branch; ProtoJSON has no
`providerQos` wrapper. IOPS is an int64, represented as a decimal string in
ProtoJSON. The accepted range is 0–2147483647. Absent/zero adds no OSAC cap;
it does not guarantee capacity or a minimum performance level. ONTAP requires
one existing backend, BLOCK protocol, and zero generic bandwidth limits.
Tier backend/protocol/QoS/encryption bindings are immutable for this preview.

## Operator request received by AAP

The existing provisioning request contains the following additional tier
settings. All identifiers and credentials below are illustrative.

```yaml
osac_job_vars:
  resource:
    metadata:
      name: tenant-a
      namespace: osac-system
      uid: tenant-cr-uid
  storage_tier_definitions:
    - name: fast
      protocol: block
      provider: ontap
      backend_id: backend-id
      encryption_enabled: true
      qos_limits:
        static_limits:
          max_reads_bw_mbps: 0
          max_writes_bw_mbps: 0
        provider_config:
          max_iops: 5000
  storage_backend_connections:
    backend-id:
      endpoint: https://cluster-mgmt.example.com
      username: osac-discovery
      password: "<resolved only during provisioning>"
```

The operator deduplicates connections and protected password resolution by
backend ID. `max_iops` reaches Ansible as a number. If the ONTAP extension is
absent, `provider_config` is omitted; an explicitly supplied zero is preserved.
`encryption_enabled` is always included for ONTAP, including `false`.
Existing provider payloads omit these additions unless used.

The common connection contains discovery credentials only. Prepared-SVM
credentials, CA material, topology, policy names and assignments are not
new backend fields or new operator extra_vars. Keep credential-bearing
tasks under `no_log`; never return or log this connection/password.

Sender implementation and tests:

- [Tier resolution](../../../osac-operator/internal/controller/storage_tier_definitions.go)
- [Request serialization](../../../osac-operator/pkg/provisioning/aap_provider.go)
- [Payload tests](../../../osac-operator/pkg/provisioning/aap_provider_test.go)
- [Controller tests](../../../osac-operator/internal/controller/storage_controller_test.go)

## Inputs the onboarding role must combine

The selected MVP uses named preassignment. The infrastructure administrator
prepares the SVM and runtime credential Secret before tenant onboarding.
Names use exact UTF-8 backend ID and immutable tenant/tier metadata names:

```text
assignment_key = lowercase_hex(sha256(backend_id + "|" + tenant_name))[:16]
tier_key = lowercase_hex(sha256(tier_name))[:16]
SVM = osac-<assignment_key>-svm
runtime Secret = osac-<assignment_key>-credentials
capped-tier policy = osac-<assignment_key>-<tier_key>-qos
TBC = ontap-<assignment_key>
StorageClass = ontap-<assignment_key>-<tier_key>
```

These names are a lookup convention, not ownership proof or ONTAP requirements.
For `backend-id`, `tenant-a` and `fast`, the keys are `79457936f209a67b` and
`115dc3606fbf8691` respectively. Check full identities before adoption.

| Input | Owner/source | Role responsibility |
|---|---|---|
| Tenant metadata name, namespace and full CR UID | Operator resource | Preserve exact ownership identity through setup, retry and delete |
| Backend ID, tier intent and discovery connection | Operator request | Select `ontap`; validate requested settings and discover the named SVM |
| SVM UUID, management endpoint, FC prerequisites and native policies | Prepared ONTAP resources and discovery | Match full identity, endpoint rule, usable capacity and policy/encryption feasibility |
| Runtime `username`, `password`, PEM `ca.crt` | Prepared Kubernetes Secret on the native Trident cluster | Inspect/claim protected source; use its credential reference for native provisioning |
| Native Trident namespace and Kubernetes connection | Role/deployment configuration | Locate source/TBC on the correct hosting target; preserve that placement for deletion |

The prepared runtime Secret carries these annotations:

```yaml
osac.openshift.io/storage-backend: backend-id
osac.openshift.io/tenant: tenant-a
osac.openshift.io/storage-resource-id: prepared-svm-uuid
osac.openshift.io/storage-assignment-state: available
```

Claiming adds `osac.openshift.io/storage-tenant-uid` with the full Tenant CR
UID and changes the assignment state to `claimed` using resource-version
checks. Offboarding records `retained`. The source has no Tenant garbage
collection owner reference. Recreating a tenant name does not authorize reuse
of an old claim; reuse requires explicit infrastructure-admin release.

The source is separate from the Fulfillment discovery VALUE Secret.
Discovery cannot recover an existing SVM password and must never substitute
the discovery account for the runtime account.

AAP must decode source `data["ca.crt"]` to PEM, verify the selected runtime
management endpoint, then encode that PEM once into TBC
`spec.trustedCACertificate`. Referencing `credentials.name` alone does not load
the CA. The planned TBC uses `ontap-san`, `sanType: fcp`, `useREST: true`, the
prepared SVM/runtime management endpoint, and `deletionPolicy: delete`.
An IP data LIF is omitted for FC. Native driver/fabric qualification is separate.

The draft interprets tier encryption as the requested data-at-rest outcome.
The receiver must validate that outcome against prepared capacity before
publishing a class. Registration stores the boolean; it does not prove that
an ONTAP aggregate can satisfy it.

## Readiness and failure contract

The operator's Tenant CR conditions are `StorageBackendReady` and
`ClusterStorageReady`. Fulfillment projects them independently into private
Tenant status. The private enum values are:

```text
COMPUTE_INFRASTRUCTURE_READY = 3  (existing; preserved)
STORAGE_BACKEND_READY = 4
CLUSTER_STORAGE_READY = 5
```

Use current `observedGeneration` values. Missing/stale/unavailable observations
project as unknown; explicit failure takes precedence across hosting hubs.
Reasons/messages are retained. An overall Ready tenant phase alone does not
make its storage ready. The projection neither creates these conditions nor
implements lifecycle gating: the onboarding controller owns both.

One existing resolver behavior needs explicit integration handling:
`resolveTierDefinitions` skips missing backends and failed password resolution,
and `resolveAndInjectTierContext` treats resolution errors as non-fatal.
The ONTAP onboarding path must turn unresolved required configuration into a
failed/not-ready stage. It must not succeed with an empty catalog or fall back
to a default provider or StorageClass. Sender serialization tests do not prove
that lifecycle guarantee.

Persist ownership and partial progress before/after native operations so retries
resume the same assignment. Cleanup checks data dependencies, removes owned
classes/TBC, waits for native backend removal, records retention and clears
storage finalizers only after verification. Keep prepared SVMs, LIFs, accounts,
policies and source Secrets. Cleanup failures retain state/finalizers.
OSAC-owned resources retain the existing `osac.openshift.io/tenant` and
`osac.openshift.io/owner-reference` annotations. Prepared sources and retained
records have no Tenant garbage collection owner reference.

## Joint checks before claiming onboarding support

The receiving AAP role must accept the exact generic payload, including numeric
IOPS, missing/zero caps and explicit false encryption. Test two tiers sharing a
backend, protected runtime credentials/CA, missing preparation, failed resolution,
current-generation readiness, retries and guarded offboarding. Then qualify
native FC VM disk access and tenant isolation with the shared VM consumption
workstream. OSAC CSI integration is outside the preview contract.

Registration qualification has passed through the current candidate CLI and
REST gateway/API on an isolated host setup, with real PostgreSQL, Kafka and
read-only ONTAP discovery. It exercised the linked YAML examples, protected
VALUE password lookup, tier round trips, public filtering, failed credential
update atomicity, tier guards and catalog cleanup. OIDC/JWKS and Vault were HTTP
protocol fixtures; ONTAP trust used a supplied public certificate with normal
hostname/expiry verification. This is component registration evidence.
Production identity/Vault/CA deployment, receiving AAP behavior, native lifecycle
and FC VM acceptance still need their respective integration checks.
