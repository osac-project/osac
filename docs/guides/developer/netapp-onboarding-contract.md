# NetApp registration and onboarding contract

This contract connects backend/tier registration to prepared-SVM tenant
onboarding for the developer preview. Registration, the operator request
mapping, preparation lookup/waiting, and Fulfillment storage-status projection are implemented on the
`OSAC-5813-netapp-backend-tiers` branch. The receiving AAP role is developed separately in
[PR #1580](https://github.com/osac-project/osac/pull/1580); deployed native
resource lifecycle and FC qualification remain joint work.

The working MVP profile is a shared cluster management endpoint with separate
discovery and tenant/backend Trident accounts. This is the agreed preview profile; the actual account's permissions still
need qualification to establish restriction to its tenant's SVM. Backend credentials
serve discovery; the preparation Secret supplies the account for tenant volume
operations. Both accounts must work through the registered cluster endpoint.

For administrator requests and TLS preparation, see
[NetApp backend and tier registration](../admin/netapp-backend-tiers.md).
For administrator-driven acceptance steps and their implementation gates, see
[manual NetApp validation](../admin/netapp-manual-validation.md).

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
      labels:
        osac.openshift.io/tenant-id: tenant-a
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
      tenant_preparation:
        tenant_id: tenant-a
        source:
          namespace: osac-storage
          name: admin-chosen-preparation
          uid: source-secret-uid
          resource_version: "42"
        provider_config:
          username: trident-tenant-a
          password: "<from the protected preparation Secret>"
          svm: customer_acme
```

The operator deduplicates connections and protected password resolution by
backend ID. `max_iops` reaches Ansible as a number. If the ONTAP extension is
absent, `provider_config` is omitted; an explicitly supplied zero is preserved.
`encryption_enabled` is always included for ONTAP, including `false`.
Existing provider payloads omit these additions unless used.

The common connection retains discovery credentials. Its optional
`tenant_preparation` carries the separately resolved tenant credentials and
provider fields, once per backend. Existing providers omit this key. The
existing dispatcher filters complete connection entries by backend ID, so this
addition needs no dispatcher schema change. Prepared credentials are not new
backend fields. Keep credential-bearing tasks under `no_log`; never log or
return either password. Credential-bearing AAP launch failures redact response
bodies, which could otherwise echo submitted passwords.

Sender implementation and tests:

- [Tier resolution](../../../osac-operator/internal/controller/storage_tier_definitions.go)
- [Request serialization](../../../osac-operator/pkg/provisioning/aap_provider.go)
- [Payload tests](../../../osac-operator/pkg/provisioning/aap_provider_test.go)
- [Controller tests](../../../osac-operator/internal/controller/storage_controller_test.go)

## Tenant preparation on the hub

The CSP admin prepares each tenant's SVM and account, then creates one Kubernetes
Opaque Secret per tenant/backend in the hub's `osac-storage` namespace. All tiers
on that backend share the same preparation. The installer creates this namespace
and retains it on uninstall; restrict access to trusted administrators and the
operator. OSAC Secrets Service preparation sourcing is deferred. The discovery
password reference used by registration is unchanged.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: acme-netapp-primary-info  # Admin-selected; no naming formula.
  namespace: osac-storage
  labels:
    osac.openshift.io/storage-backend: netapp-primary  # Backend metadata.name.
    osac.openshift.io/tenant: acme                     # Tenant resource name.
type: Opaque
stringData:
  username: trident_acme
  password: "<tenant-account-password>"
  svm: customer_acme  # Explicit prepared SVM; no required name.
```

Only those two association labels are required. The backend label contains its
registered **name**, while tier associations and AAP connection keys retain the
backend **ID**. The operator resolves name to ID from the catalog internally.
OSAC enforces Tenant ID == Tenant resource name; neither is the Kubernetes UID.
Admins can therefore prepare this Secret before creating the intended Tenant.
Each matching pair must have exactly one source; duplicates block dispatch.
Old purpose/backend-ID/tenant-ID labels alone do not select a preparation.

This source is not the native Trident credentials Secret. The operator passes
its contents and source metadata to AAP in `tenant_preparation`; AAP creates a
separate credentials Secret and TBC in the actual VM hosting cluster's Trident
namespace. This supports a separate hub/VMaaS cluster. Keep preparation selector
labels off AAP output Secrets. Never substitute discovery credentials for the
supplied tenant account or modify/delete the admin source.

```text
CSP admin: prepare SVM/account and hub Secret (before or after Tenant creation)
  -> normal Tenant onboarding reaches Ready
  -> operator polls for the matching tenant/backend-name labels
  -> operator passes source + ID-keyed backend/tier settings to AAP
  -> backend setup validates prepared SVM/account and requested policies
  -> native setup creates target credentials Secret, TBC and StorageClasses
  -> successful current-configuration jobs and classes establish storage readiness
```

| Input / boundary | Source or remaining qualification |
|---|---|
| Preparation | Hub `osac-storage`; independent of the workload Trident namespace |
| AAP payload | Layout above; the namespace/name change does not alter its schema |
| Runtime endpoint | Registered `StorageBackend.spec.endpoint`; no preparation `management_endpoint` field for this profile |
| Runtime permissions | Qualify the separate tenant account through the shared cluster endpoint; distinct usernames alone do not restrict access |
| TLS trust | Approved bundle for every HTTPS caller; AAP execution-environment and Trident delivery remain installation/role integration work |
| Native placement | AAP must use the actual VM hosting cluster, including dedicated VMaaS; capture-only testing does not verify this |

Will's receiving role consumes `tenant_preparation.provider_config`, including
the explicit SVM and credentials, and derives the address from the registered
backend. Use compatible operator/role revisions when testing; registration and
source selection alone do not establish working native provisioning.

NetApp's
[Trident SAN configuration guidance](https://docs.netapp.com/us-en/trident-2510/trident-use/ontap-san-examples.html)
requires an SVM management address for `vsadmin`, and the cluster management
address for `admin`. The working profile therefore requires cluster-endpoint
compatible runtime credentials. It is not compatible with substituting an
ordinary SVM-scoped account at that address. Separate SVM management endpoints
remain an alternative if the team changes the chosen account model; that would
add an endpoint to the preparation and operator/AAP mapping.

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
best-effort resolution policy. Missing-backend skipping remains a catalog-resolution follow-up. Preparation
lookup now requires exactly one non-deleting Opaque Secret with matching
tenant-name/backend-name labels and non-empty `username`, `password`, `svm`. The operator reads the
hub API directly, and polls at `OSAC_AAP_STATUS_POLL_INTERVAL` (default 30s), so
creating a matching Secret resumes work without a Tenant edit. The namespace
default is `osac-storage`; the operator Helm deployment explicitly sets
`OSAC_STORAGE_PREPARATION_NAMESPACE` to that value. A custom deployment can
override it, provided the namespace exists and the operator can read its Secrets.
`OSAC_STORAGE_CONFIG_NAMESPACE` remains the legacy provider-output namespace;
it is unrelated to NetApp preparation selection.

Missing, ambiguous, invalid or unreadable preparation sets both storage
conditions false. The input Secret is never patched or deleted. For prepared
ONTAP, backend readiness requires a successful setup job for the current
configuration; cluster readiness also requires successful native configuration
and all tier classes. Older successful jobs without a matching configuration
version do not satisfy these checks. AAP must only report native success after
Trident confirms a current-generation bound backend. The operator does not
independently watch TBCs. Missing classes trigger an idempotent repair job.

Configuration versions include tenant generation, tier settings and source
UID/SVM, and exclude credentials. Changing only a preparation password does not
start a rotation job; future provisioning or repair jobs use their newly read
snapshot. Rotation remains deferred. Deletion retains its finalizer if previous
prepared work exists but the catalog needed to route cleanup cannot be recovered.
Sender/controller tests do not prove native lifecycle support.

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
credential-update behavior, tier guards and catalog cleanup. ERROR/manual retry and configured CA-pool injection also passed a deployed
read-only ONTAP check on edge-17. A separate operator-to-real-AAP capture verified
prepared values without ONTAP requests or native resource creation; that run used
the earlier source labels/namespace and must be repeated for this revision. OIDC/JWKS and Vault were HTTP
protocol fixtures; ONTAP trust used a supplied public certificate with normal
hostname/expiry verification. This is component registration evidence.
OSAC Secrets Service production sourcing, AAP/Trident trust delivery, native
lifecycle and FC VM acceptance still need their respective integration checks.
