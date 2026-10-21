# OSAC-5429 BareMetal and CaaS SSH-Key Migration

## Intent

Finalize the registered SSH-key migration started by OSAC-5429. Users must
register an `SSH_PUBLIC_KEY` Secret and reference it instead of placing public
key bytes in BareMetalInstance or Cluster API requests. CaaS must carry the
user-owned reference through BMaaS worker creation without creating a Secret on
the user's behalf.

Success means:

- the deprecated BareMetalInstance inline-key API is removed;
- Cluster uses the same registered-key model for both BMaaS and non-BMaaS
  provisioning;
- ClusterOrder stores only the canonical Secret ID;
- AAP, InfraEnv, and BMFO receive resolved bytes only at their existing
  materialization boundaries; and
- affected unit, integration, and end-to-end fixtures use registered Secrets.

The existing commit `4d43771e6` remains intact. This work is added on top.

## Scope

### In scope

- BareMetalInstance and BareMetalInstanceCatalogItem contract cleanup.
- Cluster, ClusterTemplate defaults, and ClusterCatalogItem migration from
  inline values to Secret references.
- Fulfillment validation, canonicalization, persistence protection, catalog
  policy handling, CLI input, and ClusterOrder projection.
- ClusterOrder CRD, operator Secret resolution, child BMI creation, and AAP job
  launch integration.
- Removal of CaaS template-parameter paths that accept inline SSH keys.
- Migration of affected integration and E2E fixtures.
- Focused documentation for the new request flow and deletion behavior.

### Out of scope

- Creating or owning an SSH-key Secret on behalf of a tenant.
- Rotating access on already provisioned nodes.
- Blocking normal Secret deletion.
- Renaming provider-facing fields such as BMFO `sshPublicKey`, InfraEnv
  `sshAuthorizedKey`, or internal AAP `ssh_public_key` variables.
- Implementing the corresponding UI selection workflow. The UI's independently
  selected protobuf baseline remains unchanged in this change.

## API Contracts

Remove the old inline fields and reserve both their protobuf names and numbers:

- `BareMetalInstanceSpec.ssh_public_key`;
- `BareMetalInstanceCatalogItemFields.ssh_public_key`;
- `ClusterSpec.ssh_public_key`;
- `ClusterTemplateSpecDefaults.ssh_public_key`; and
- `ClusterCatalogItemFields.ssh_public_key`.

BareMetalInstance retains its existing `ssh_key` `SecretReference` from the
first OSAC-5429 commit. Cluster gains `ssh_key` using the same registered
Secret-reference model. ClusterTemplate defaults use a Secret reference, and
the BareMetalInstance and Cluster catalog fields use
`SecretReferenceFieldPolicy`.

Shared catalog items may mark the field editable or required, but may not lock
or default a tenant-owned reference. This allows a shared CaaS offering to
require the tenant to select a key without embedding tenant data in the
offering.

Fulfillment resolves references selected by name or ID to their canonical
identity. Validation requires:

- a non-shared Secret in the resource tenant and applicable project;
- `SECRET_TYPE_SSH_PUBLIC_KEY`;
- a non-empty `data["public_key"]`; and
- one valid OpenSSH public key.

`BareMetalInstance.spec.ssh_key` remains immutable. `Cluster.spec.ssh_key`
remains mutable in the same Cluster states in which the current
`ssh_public_key` can be changed. A replacement update validates only the new
reference and does not require the old Secret to exist.

## ClusterOrder and Runtime Data Flow

Replace `ClusterOrder.spec.sshPublicKey` with a typed reference containing the
canonical Fulfillment Secret ID. Public key bytes must not be persisted in the
ClusterOrder.

The data flow is:

```text
Cluster.spec.ssh_key (canonical Secret ID)
  -> ClusterOrder.spec.sshKey.id
     -> ClusterOrder controller resolves bytes for an AAP launch
     -> bare-metal worker controller resolves bytes for InfraEnv
     -> child BareMetalInstance.spec.ssh_key.id
        -> BMaaS controller resolves bytes for BMFO
```

The Fulfillment Cluster reconciler copies only the canonical ID into the
ClusterOrder. The operator verifies that a Secret read with controller
credentials belongs to the Cluster tenant and has the expected type and data
before using it.

The ClusterOrder controller resolves the key only when launching a provisioning
job and passes it as a trigger-only AAP extra variable. A narrow,
backward-compatible provisioning-lifecycle hook supplies these variables
without changing existing provider call sites. The AAP `cluster_settings` role
uses the explicit launch value with `no_log: true`, then retains its existing
provider-default credential fallback when a Cluster does not select a key.

The bare-metal worker controller resolves the same Secret for InfraEnv's
`sshAuthorizedKey` and copies the reference ID into every child BMI. It does
not copy key bytes into the BMI API and does not create a Secret.

## Inline-Path Removal

Remove all user-facing and fallback paths that still accept inline BMI or
Cluster keys:

- BMaaS server validation and catalog string policies;
- BMaaS controller fallback to `spec.ssh_public_key`;
- Cluster server validation and catalog string policies;
- Cluster CLI `--ssh-public-key` and `--ssh-public-key-file`;
- operator fallback from ClusterOrder or `template_parameters` raw values; and
- published CaaS template parameters named `ssh_public_key`.

The Cluster CLI accepts `--ssh-key` for a previously registered Secret. AAP and
provider roles may continue using raw-key variable names after the trusted
resolution boundary because those systems require materialized key bytes.

## Persistence and Compatibility

Add a new database migration; do not modify existing migrations. It removes
obsolete inline SSH-key properties from stored BareMetalInstance, Cluster, and
catalog-item JSON. It must not synthesize Secrets or convert bytes into a
tenant resource.

Database validation closes the race between API reference validation and
persistence for Cluster creates and key-reference updates. The persisted
reference is the canonical Secret ID.

This is an intentional pre-GA contract break. Removed wire fields are reserved,
so stale clients fail rather than writing a reused field number. Existing
provisioned instances are unaffected because SSH injection is initial-only. An
existing Cluster that needs later provisioning must select a registered key
through the new mutable field.

## Secret Deletion and Error Handling

SSH-key Secrets retain their normal lifecycle and may be deleted while
referenced. Deletion does not alter existing nodes.

If a Cluster needs the deleted key for an AAP launch, InfraEnv recreation, or
BMaaS scale-up, reconciliation pauses and records a recoverable resolution
failure using existing ClusterOrder or InfraEnv conditions. Conditions and logs
may identify the Secret and failure class, but must never contain key bytes.
Transient API failures are retried through the existing reconciliation
behavior.

Recovery is:

1. register a replacement `SSH_PUBLIC_KEY` Secret;
2. update `Cluster.spec.ssh_key` by name or ID;
3. let Fulfillment canonicalize the replacement ID; and
4. allow reconciliation to resume from the updated ClusterOrder.

Reusing a deleted Secret's name is valid, but the replacement has a new ID.
This behavior is documented briefly; full key rotation remains out of scope.

## Component Changes

### Protobuf and Fulfillment

- Update editable private protobuf sources, then regenerate public and Go
  artifacts once through `make -C proto generate`.
- Remove deprecated BMI compatibility code and tests.
- Add Cluster Secret-reference validation, mutable replacement behavior,
  catalog policy handling, CLI support, and ClusterOrder projection.
- Add migration and trigger coverage.
- Remove obsolete lint exceptions and update developer/catalog documentation.

### Operator

- Add the typed ClusterOrder reference and regenerate deepcopy code, CRDs, and
  Helm CRDs.
- Extend the narrow Fulfillment client seams used by ClusterOrder and
  bare-metal worker reconciliation to read Secrets.
- Resolve and validate the key at AAP and InfraEnv boundaries.
- Build child BMIs with `ssh_key.id` and no inline-key field.

### AAP

- Consume the operator-supplied launch variable before the provider-default
  fallback.
- Remove ClusterOrder and template-parameter raw-key fallback.
- Stop publishing inline SSH-key parameters from CaaS template metadata.
- Keep downstream raw-key role variables where materialized bytes are required.

### Tests and documentation

- Register SSH-key Secrets in shared Go and Python fixtures.
- Replace inline BMI and Cluster request fields with Secret references.
- Update the BareMetalInstance guide, CaaS/catalog documentation, architecture
  notes, and integration-testing touched-area maps where their contracts
  change.

## Test Strategy

### Contract and Fulfillment unit coverage

- Removed fields are absent and their protobuf names/numbers are reserved.
- BMaaS create and catalog flows accept registered references only.
- Cluster create and mutable update support name and ID selection and persist
  canonical IDs.
- Missing, deleted, malformed, wrong-type, shared, cross-tenant, and invalid
  project references are rejected without exposing data.
- Updating from a deleted old reference to a valid replacement succeeds.
- Shared catalog items can require an editable reference but cannot lock or
  default one.
- Database trigger races and legacy JSON cleanup are covered.
- ClusterOrder projection contains only the reference ID.
- CLI tests cover `--ssh-key` and absence of raw/file flags.

### Operator and AAP unit/envtest coverage

- Secret tenant, type, and data validation is enforced.
- InfraEnv receives the resolved key.
- Child BMIs contain only `spec.ssh_key.id`.
- AAP receives the key only in launch variables.
- A missing Secret prevents new work, reports a recoverable condition, and does
  not modify existing workers.
- Selecting a replacement reference resumes reconciliation.
- AAP role tests cover explicit launch input and provider-default fallback with
  no raw ClusterOrder/template-parameter fallback.

### Integration and E2E coverage

- Fulfillment integration fixtures create registered Secrets and use
  references; unrelated fixtures may use `user_data`.
- Direct BMaaS reference E2E creates and selects an SSH-key Secret.
- CaaS helpers register/delete the Secret and pass `--ssh-key`.
- The focused BMaaS-backed CaaS catalog requires an editable reference supplied
  at Cluster creation.
- The CaaS scenario asserts the canonical Cluster reference, ID-only
  ClusterOrder, resolved InfraEnv key, and matching reference-only child BMIs.

Integration and deployed E2E tests are migrated in this change but their
execution is deferred to PR CI. Local validation covers generators, linters,
focused Go tests that do not require deployment, isolated AAP tests, Python unit
tests, Ruff, and E2E collection. The final report lists the CI-deferred commands
explicitly.
