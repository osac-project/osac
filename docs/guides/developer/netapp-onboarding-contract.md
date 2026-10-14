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
cluster management HTTPS endpoint, and discovery credentials. Seven bounded
authenticated GETs set `READY` on success or `ERROR` with `status.message` on
connection failure. Both outcomes persist the desired configuration. Every
explicit Update, including an unchanged save, reruns discovery; success clears
the error. There is no automatic backend reconciliation. Invalid requests and
unauthorized Secret reads still fail without saving. The probe uses the existing
Fulfillment CA pool and verifies the registered endpoint's certificate.
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

The common connection currently contains discovery credentials only. Prepared-SVM
credentials are not new backend fields. The next operator/AAP input revision
below is still pending; it is not included in this implemented payload. Keep credential-bearing
tasks under `no_log`; never return or log this connection/password.

Sender implementation and tests:

- [Tier resolution](../../../osac-operator/internal/controller/storage_tier_definitions.go)
- [Request serialization](../../../osac-operator/pkg/provisioning/aap_provider.go)
- [Payload tests](../../../osac-operator/pkg/provisioning/aap_provider_test.go)
- [Controller tests](../../../osac-operator/internal/controller/storage_controller_test.go)

## Tenant preparation: updated direction, implementation pending

The CSP admin prepares an SVM and its account. For the preview, the latest
direction uses one administrator-created Kubernetes Opaque Secret per immutable
OSAC tenant ID and backend ID, shared by every tier on that backend. Tenant
preparation through OSAC Secrets Service is deferred. The discovery password
reference used by registration is unchanged.

The proposed administrator input is:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: acme-netapp-primary-info  # Admin-selected; no naming formula.
  namespace: trident
  labels:
    osac.openshift.io/secret-type: storage-tenant-preparation
    osac.openshift.io/storage-backend-id: backend-id
    osac.openshift.io/tenant-id: immutable-osac-tenant-id
    osac.openshift.io/tenant: acme
type: Opaque
stringData:
  username: trident_acme
  password: "<tenant-runtime-password>"
  svm: customer_acme             # Explicit prepared SVM; no required name.
```

This is the preparation input, not the native Trident credentials Secret. The
operator is to select exactly one protected input for the tenant/backend and
pass its information to AAP. AAP creates the native credentials Secret and TBC
on the actual VM hosting cluster. Runtime credentials must never be replaced
with the cluster discovery account. Source presence alone is not proof of
successful onboarding.

```text
Admin prepares SVM/account + labelled input Secret
  -> Tenant CR is Ready
  -> operator waits for matching preparation
  -> operator supplies preparation + backend/tier settings to AAP
  -> AAP validates it and creates runtime Secret, TBC and StorageClasses
  -> storage conditions report the result
```

Agree these remaining inputs with the receiving implementation before enabling
this flow:

| Item | Proposed resolution / remaining check |
|---|---|
| Source cluster | Protected `trident` namespace on the operator/hub cluster; confirm this separately from the runtime Trident cluster, including dedicated VMaaS |
| Tenant identity | Use the immutable OSAC tenant ID, obtained from the Tenant API; do not substitute the Kubernetes CR UID or reuse a previous tenant's assignment |
| AAP payload | Add a per-backend generic preparation input, preserving source identity; agree its exact layout with the role owner before changing sender/receiver |
| Runtime management endpoint | Keep cluster discovery on `StorageBackend.spec.endpoint`; require an explicit SVM endpoint in preparation if the runtime account cannot use that address |
| Certificate trust | Installation supplies an approved CA bundle to each HTTPS caller; AAP execution-environment and Trident projection remain to wire and qualify |

Will's current role derives Trident's address from the registered backend.
That is not yet a qualified account/endpoint profile. NetApp's
[Trident SAN configuration guidance](https://docs.netapp.com/us-en/trident-2510/trident-use/ontap-san-examples.html)
requires an SVM management address for `vsadmin`, and the cluster management
address for `admin`. The minimal three-field Secret above therefore does not
establish that a separate runtime endpoint is unnecessary. Restoring
`management_endpoint` is the proposed solution for SVM-scoped access.

The native TBC uses `ontap-san`, `sanType: fcp`, `useREST: true`, the validated
runtime endpoint/SVM and `deletionPolicy: delete`. An IP data LIF is omitted
for FC. Native driver/fabric qualification is separate. Proposed trust wiring:
mount the approved PEM CA bundle in the AAP execution environment and use it
for verified ONTAP requests; AAP also base64-encodes that PEM bundle into TBC
`spec.trustedCACertificate` for the preview's Trident REST connection.
The bundle's deployment source and delivery into that environment still need
agreement with the installation/role owners.
The TBC credentials Secret reference alone does not supply certificate trust.

Prepared source Secrets have no Tenant garbage collection owner reference.
Retries and cleanup must check full tenant/backend/source identity and retain
administrator-prepared SVMs and accounts. The earlier computed SVM/Secret naming
convention is superseded by the explicit SVM and association labels above.

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

Tenant storage setup now waits and requeues on catalog resolution errors,
backends in ERROR and unresolved ONTAP discovery credentials. It publishes
`ConfigurationUnavailable` on both storage conditions and launches no new
storage provisioning job until resolution succeeds. CaaS cleanup still runs
before that gate. Legacy compute/deletion callers retain their existing
best-effort resolution policy. Missing-backend skipping and the new preparation
lookup/readiness integration remain onboarding follow-ups; sender tests do not
prove native lifecycle support.

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
VALUE password lookup, tier round trips, public filtering, the earlier rejected
credential-update behavior, tier guards and catalog cleanup. The new ERROR/manual
retry semantics and configured CA-pool injection have local fixture coverage;
they have not been rerun against the array. OIDC/JWKS and Vault were HTTP
protocol fixtures; ONTAP trust used a supplied public certificate with normal
hostname/expiry verification. This is component registration evidence.
Production identity/Vault/CA deployment, receiving AAP behavior, native lifecycle
and FC VM acceptance still need their respective integration checks.
