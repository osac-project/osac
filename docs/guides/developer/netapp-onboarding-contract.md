# NetApp registration and onboarding interface

The preview uses an administrator-managed shared TridentBackendConfig (TBC).
This document describes the implemented backend/tier API and operator sender,
and the receiving behavior needed from the ONTAP AAP role. The receiving role is
separate work in [PR #1580](https://github.com/osac-project/osac/pull/1580).
Native lifecycle and FC VM qualification require that matching receiver.

For customer actions, see [registration](../admin/netapp-backend-tiers.md) and
[manual validation](../admin/netapp-manual-validation.md).

## Ownership and placement

| Item | Owner / location |
|---|---|
| SVM, FC preparation, native credentials, TLS trust and pool policies | Infrastructure admin; array and VM hosting cluster |
| Shared TBC and its native credentials Secret | Infrastructure admin; Trident namespace on the VM hosting cluster |
| Backend/tier catalog, Tenant CR and AAP jobs | OSAC; hub-side services |
| Tenant/tier StorageClasses | AAP; configured VM hosting cluster |
| Disk/VM lifecycle | Shared VM workflow; outside this registration change |

The existing deployment configuration selects the hub or one dedicated VM
hosting cluster. TBC namespace/name are interpreted on that target; they do not
add a new cluster-routing API. Operator class observation and AAP operations must
use the same target. A configured but unavailable target must not fall back to hub.

## IC-1: Backend API

Keep provider `ontap`, the HTTPS endpoint, username, and exactly one inline
password or OSAC VALUE password reference. These compatibility requirements are
unchanged. Shared onboarding does not resolve or send those credentials.

The private proto adds a typed branch:

```protobuf
oneof provider_config {
  OntapBackendConfig ontap = 5;
}
message OntapBackendConfig { repeated OntapBackendProfile profiles = 1; }
message OntapBackendProfile {
  string name = 1;
  oneof mode { SharedTridentBackend shared = 2; }
}
message SharedTridentBackend {
  string tbc_namespace = 1;
  string tbc_name = 2;
}
```

The `ontap` branch requires provider `ontap`. It contains 1–32 uniquely named
profiles, each with a required shared mode and valid Kubernetes namespace/name.
New ONTAP registrations must provide this branch; profile-less records already
stored remain readable and updatable through the legacy path.
A future dedicated mode can add another typed branch; it is not accepted now.

Create/Update validate local configuration and password-reference metadata,
then persist READY without contacting ONTAP or fetching protected password data.
READY is catalog acceptance, not native health. Update clears historical probe
ERROR/messages. Provider, ONTAP endpoint and profile bindings are immutable.
No backend reconciler or connectivity retry is introduced.

## IC-2: Tier API

The existing `BackendAssociation.provider_qos` oneof's ONTAP message gains
`profile = 2` and `pool_selector = 3`, beside the existing `max_iops = 1`:

```yaml
backendId: registered-backend-id
encryptionEnabled: true
ontap:
  profile: shared-fc
  poolSelector:
    performance: extreme
```

Shared tiers require one ONTAP backend, BLOCK, a known profile and a non-empty
map of literal label equalities. Read/write bandwidth and numeric `max_iops`
must be zero/absent. Reject provider mismatches and selector-expression
injection. Backend/protocol/QoS/encryption bindings remain immutable.

The selected customer pool owns QoS and encryption defaults. Existing
`encryption_enabled` is retained as advertised tier metadata; it must not cause
AAP to change the shared TBC or add a conflicting native encryption override.
The admin qualifies that the advertised outcome matches the chosen pool.

## IC-4: Operator → AAP payload

Two tiers can share one connection; only their pool selections differ:

```yaml
osac_job_vars:
  resource:
    metadata:
      name: tenant-a
      namespace: osac-system
      uid: tenant-cr-uid
      labels:
        osac.openshift.io/tenant-id: tenant-a
  storage_backend_connections:
    backend-id:
      provider_config:
        profiles:
          - name: shared-fc
            shared:
              tbc_namespace: trident
              tbc_name: customer-fc
  storage_tier_definitions:
    - name: fast
      protocol: block
      provider: ontap
      backend_id: backend-id
      encryption_enabled: true
      qos_limits:
        static_limits: {max_reads_bw_mbps: 0, max_writes_bw_mbps: 0}
      provider_config:
        profile: shared-fc
        pool_selector: {performance: extreme}
    - name: standard
      protocol: block
      provider: ontap
      backend_id: backend-id
      encryption_enabled: true
      qos_limits:
        static_limits: {max_reads_bw_mbps: 0, max_writes_bw_mbps: 0}
      provider_config:
        profile: shared-fc
        pool_selector: {performance: value}
```

A shared connection has **no endpoint, username, password or tenant_preparation**.
The operator skips discovery-password resolution and preparation lookup. The
serializer remains generic: a different provider can combine populated common
connection fields with `provider_config`. Legacy payloads retain their fields.

The existing dispatcher must pass these connection/tier dictionaries intact to
the provider role. Provider-specific fields stay under generic `provider_config`;
tier pool selection is top-level on the tier, not inside `qos_limits`.

## IC-5/6: Receiving workflow and lifecycle

These are receiver requirements, not implemented native behavior in this branch:

| Existing action | Shared-profile behavior |
|---|---|
| `setup` | Read selected TBCs through the hosting Kubernetes API; require usable Bound/Success state and the FC driver profile; publish existing provider setup output |
| `ensure_storage_class` | Resolve the TBC's bound native backend name; create/reconcile only tenant/tier classes with exact backend restriction and selected pool labels; publish class names through existing outputs |
| `teardown_cluster_storage` | Apply existing data guards; delete only classes/configuration owned by this tenant/provider |
| `teardown_backend` | Remove only OSAC-owned bookkeeping; retain customer TBC, native Secret, SVM, pools and policies |

No action calls ONTAP directly or creates/patches/deletes a shared TBC or its
credentials. TBC `metadata.name` is not necessarily the native backend name;
use its bound `status.backendInfo.backendName`. Render that exact name safely
in Trident's `storagePools` filter, and render selector-map entries as literal
equalities. A selector alone can match pools on other backends.
[Trident class selection](https://docs.netapp.com/us-en/trident-2510/trident-use/create-stor-class.html),
[TBC binding/status](https://docs.netapp.com/us-en/trident-2510/trident-use/backend-kubectl.html).

Keep the existing setup/class outputs (`storage_provider_tenant_config`,
`storage_provider_storage_class_names` and `tenant_storage_classes`) where their
shared workflows expect them. Preserve full Tenant UID ownership, existing
class-discovery labels and `osac.openshift.io/tenant` /
`osac.openshift.io/owner-reference` annotations on OSAC-owned resources.
Never apply tenant garbage-collection ownership to shared customer resources.

The operator keeps its two storage stages. A successful setup job for the
current configuration gates `StorageBackendReady`; successful class setup and
all expected observed classes gate `ClusterStorageReady`. Merely having a TBC
reference or a Ready Tenant phase cannot satisfy storage readiness. Configuration
versions include profile/TBC routing and pool selectors. Missing profiles or
catalog failures set `ConfigurationUnavailable` without dispatch.

If a class job succeeds but expected classes remain missing, the operator reports
`StorageClassRepairPending` and delays repair using persisted job launch times:
at least two minutes between launches, doubling repeated intervals up to thirty
minutes. It continues checking for late classes during that delay. Changed
configuration is applied immediately; retries do not permanently exhaust.

Fulfillment projects the two storage conditions independently, preserving
reasons/messages and generation checks. The operator does not directly watch
TBC health; AAP must report success only after observing the appropriate native
state. These initial readiness checks are not a continuous array-health monitor.

Deletion runs class cleanup before backend cleanup and retains the storage
finalizer until successful completion. Failed/missing routing remains blocking.
Class deletion must be provider- and full-owner-scoped; another tenant/provider's
classes and the shared TBC must survive.

## Compatibility and checks

Existing ONTAP records without backend profiles retain the legacy prepared-SVM
path, including its hub preparation Secret and credential payload. VAST and other providers keep
their behavior. Do not route a new shared payload to an old prepared-SVM receiver.

Automated coverage includes real API/database validation and persistence,
registered gRPC no-outbound checks, operator Envtest for two tiers/one backend,
blocked premature readiness, retries/configuration changes, staged deletion,
and serializer compatibility. The edge-17 capture-only run used real Kubernetes
and AAP with explicit catalog fixtures: two pool selections delivered, no Secret
reads, no ONTAP calls, no Trident writes. It does not qualify receiver execution,
native FC, encryption, VM disk persistence or shared-worker tenant isolation.

Sender code and tests:

- [Resolution](../../../osac-operator/internal/controller/storage_tier_definitions.go)
- [Serialization](../../../osac-operator/pkg/provisioning/aap_provider.go)
- [Sender tests](../../../osac-operator/pkg/provisioning/aap_provider_test.go)
- [Lifecycle Envtest](../../../osac-operator/internal/controller/storage_shared_trident_test.go)
