# Finalize BareMetal and CaaS SSH-Key Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove inline BareMetalInstance and Cluster SSH-key inputs and carry a
user-owned registered SSH-key Secret reference through CaaS, BMaaS, InfraEnv,
and AAP provisioning.

**Architecture:** Fulfillment validates and canonicalizes `SecretReference`
values, while ClusterOrder stores only the canonical Secret ID. The operator
resolves key bytes at the InfraEnv and AAP launch boundaries and passes the
same ID to child BareMetalInstances; downstream provider-specific raw-key
fields remain unchanged.

**Tech Stack:** Protocol Buffers/CleanAPI, Go 1.25, PostgreSQL migrations,
controller-runtime/Kubernetes CRDs, Ansible, Cobra, Ginkgo/Gomega, pytest, and
Ruff.

**Spec:**
`docs/superpowers/specs/2026-10-19-ssh-key-baremetal-caas-migration-design.md`

## Global Constraints

- Keep commit `4d43771e6` intact and add implementation commits on top.
- Never create or own an SSH-key Secret on a tenant's behalf.
- Do not block normal Secret deletion or rotate access on existing nodes.
- Never persist key bytes in ClusterOrder or include them in logs/conditions.
- Keep provider-facing BMFO `sshPublicKey`, InfraEnv `sshAuthorizedKey`, and
  internal AAP `ssh_public_key` materialization fields.
- Do not update `osac-ui`; its independently selected protobuf baseline stays
  unchanged.
- Edit only private proto sources; generate public and Go artifacts with
  `make -C proto generate`.
- Generate operator deepcopy/CRD/Helm CRD artifacts; never hand-edit them.
- A shared catalog's `fields.ssh_key.editable {}` means caller-required because
  it has no default; shared catalogs cannot lock or default tenant references.
- Migrate integration and E2E code, but defer deployed/complex test execution
  to PR CI.
- Preserve tenant and owner-reference annotations on all resources.
- Every implementation commit is signed off, uses the `OSAC-5429:` prefix,
  includes `Assisted-by: OpenCode <noreply@opencode.ai>`, and keeps lines within
  80 characters.

## Review Focus

- An unrelated Cluster update after key deletion must retain the stale ID and
  succeed; only an explicit `spec.ssh_key` replacement is revalidated (Task 2).
- A controller-authorized Secret read must reject a cross-tenant Secret, and a
  child BMI in a non-default project must retain the Cluster's canonical
  project selector while matching the ClusterOrder ID (Tasks 4-5).
- Recreating a Secret under the same name must require and accept the new
  canonical ID without resolving the deleted old ID (Tasks 2 and 3).
- Key bytes must occur only in the AAP launch variable and InfraEnv/BMFO
  materialization, never ClusterOrder, status messages, or logs (Tasks 3-5).
- Shared catalogs must reject locked/default SSH-key references while accepting
  an editable caller-supplied reference and rejecting omission (Task 2).

## File and Interface Map

- `proto/private/osac/private/v1/*_type.proto` owns API field numbers,
  reservations, references, and catalog policies.
- `fulfillment-service/internal/servers/ssh_validation.go` owns reusable
  OpenSSH-key and Secret-reference validation used by BMI and Cluster servers.
- `fulfillment-service/internal/servers/private_clusters_server.go` owns
  Cluster create/update canonicalization and mutable replacement semantics.
- `fulfillment-service/internal/servers/*catalog_item_policies.go` owns
  locked/editable/default reference policy behavior.
- `fulfillment-service/internal/database/migrations/129_*.up.sql` owns legacy
  JSON cleanup and insert/reference-change race protection.
- `fulfillment-service/internal/controllers/cluster/` projects the canonical
  Secret ID into ClusterOrder.
- `osac-operator/api/v1alpha1/clusterorder_types.go` owns the ID-only CRD
  contract.
- `osac-operator/internal/controller/sshkey/` owns operator-side Secret reads,
  tenant/type/data checks, and byte extraction.
- `osac-operator/pkg/provisioning/provision_lifecycle.go` owns the optional
  trigger-preparation hook used to inject AAP variables only when a job starts.
- `osac-operator/internal/controller/clusterorder_controller.go` owns AAP key
  resolution and recoverable provisioning conditions.
- `osac-operator/internal/controller/baremetalworker/` owns InfraEnv key
  materialization and reference-only child BMI requests.
- `osac-aap/.../cluster_settings/` consumes the trusted launch variable and
  retains provider-default fallback.
- `tests/e2e/core/` owns reusable Secret registration and CLI request helpers;
  service-tier tests own journey assertions.

---

### Task 1: Add the Reference Contracts Without Removing Compatibility Yet

**Files:**
- Modify: `proto/private/osac/private/v1/cluster_type.proto`
- Modify: `proto/private/osac/private/v1/cluster_template_type.proto`
- Modify: `proto/private/osac/private/v1/cluster_catalog_item_type.proto`
- Modify: `osac-operator/api/v1alpha1/clusterorder_types.go`
- Modify: `fulfillment-service/internal/validation/bmaas_ssh_key_reference_contract_test.go`
- Generated: `proto/public/osac/public/v1/*.proto`
- Generated: `proto/gen/osac/{private,public}/v1/*.{pb.go}`
- Generated: `osac-operator/api/v1alpha1/zz_generated.deepcopy.go`
- Generated: `osac-operator/config/crd/bases/osac.openshift.io_clusterorders.yaml`
- Generated: `osac-operator/charts/operator-crds/templates/osac.openshift.io_clusterorders.yaml`

**Interfaces:**
- Produces: `ClusterSpec.ssh_key` as `SecretReference` field 13.
- Produces: `ClusterTemplateSpecDefaults.ssh_key` as `SecretReference` field 7.
- Produces: `ClusterCatalogItemFields.ssh_key` as
  `GlobalSecretReferenceFieldPolicy` field 8.
- Produces: `FulfillmentSecretReference{ID string}` and
  `ClusterOrderSpec.SSHKey *FulfillmentSecretReference` serialized as
  `spec.sshKey.id`.
- Temporarily retains old inline fields until Task 8 so intermediate commits
  compile and remain testable.

- [ ] **Step 1: Add failing descriptor and CRD contract tests**

  Extend the validation test to assert the three Cluster `ssh_key` descriptors
  use the exact types and field numbers above. Add an API/CRD test asserting an
  ID-only ClusterOrder reference serializes as `{"sshKey":{"id":"key-id"}}`
  and does not contain public-key bytes.

- [ ] **Step 2: Run the contract tests and verify they fail**

  Run: `go test ./fulfillment-service/internal/validation ./osac-operator/api/v1alpha1`

  Expected: FAIL because Cluster `ssh_key` and ClusterOrder `sshKey` do not
  exist.

- [ ] **Step 3: Add the additive private proto and CRD fields**

  Add the fields with the exact numbers and types from **Interfaces**. Define
  `FulfillmentSecretReference.ID` with minimum length one and leave
  `ClusterOrderSpec.SSHKey` mutable and optional.

- [ ] **Step 4: Regenerate protobuf and operator artifacts**

  Run: `make -C proto generate`

  Run from `osac-operator/`: `make manifests generate helm-crds check-helm-crds`

  Expected: generated public/Go proto, deepcopy, CRD, and Helm CRD diffs match
  the private sources; no `osac-ui` files change.

- [ ] **Step 5: Run contract validation**

  Run: `make -C proto lint`

  Run: `go test ./fulfillment-service/internal/validation ./osac-operator/api/v1alpha1`

  Expected: PASS.

- [ ] **Step 6: Commit the additive contracts**

  Commit: `OSAC-5429: add Cluster SSH key reference contracts`

### Task 2: Implement Fulfillment Cluster Validation and Catalog Policies

**Files:**
- Modify: `fulfillment-service/internal/servers/ssh_validation.go`
- Modify: `fulfillment-service/internal/servers/private_baremetal_instances_server.go`
- Modify: `fulfillment-service/internal/servers/private_clusters_server.go`
- Modify: `fulfillment-service/internal/servers/cluster_catalog_item_policies.go`
- Modify: `fulfillment-service/internal/servers/catalog_item_field_policies.go`
- Modify: `fulfillment-service/internal/utils/cluster_spec_defaults.go`
- Modify: `fulfillment-service/internal/cmd/service/start/grpcserver/register_servers.go`
- Modify: `fulfillment-service/internal/cmd/service/start/grpcserver/reference_validator_setup.go`
- Test: `fulfillment-service/internal/servers/private_clusters_server_test.go`
- Test: `fulfillment-service/internal/servers/private_cluster_catalog_items_server_test.go`
- Test: `fulfillment-service/internal/servers/private_baremetal_instances_server_test.go`
- Test: `fulfillment-service/internal/utils/cluster_spec_defaults_test.go`
- Test: `fulfillment-service/internal/cmd/service/start/grpcserver/reference_lookups_test.go`

**Interfaces:**
- Produces:
  `validateAndCanonicalizeSSHKeyReference(ctx, logger, secretsDAO, secretStore,
  metadata, ref, field) (*privatev1.SecretReference, error)`.
- Produces: `PrivateClustersServerBuilder.SetSecretStore(vault.SecretStore)`.
- Consumes: Cluster `ssh_key` and global-secret field policy from Task 1.
- Catalog semantics: an editable policy with no default rejects an omitted
  caller value as required; shared policy lock/default values are invalid.

- [ ] **Step 1: Write failing Secret-reference validation tests**

  Add table-driven create tests for canonicalization by name and ID, empty
  identifier, shared reference, project mismatch, missing/deleted Secret,
  wrong type, missing/empty `public_key`, and malformed OpenSSH data. Assert
  errors identify `spec.ssh_key` but never include key bytes.

- [ ] **Step 2: Write failing mutable replacement tests**

  Cover a READY Cluster changing from key ID A to B; deleting A and then
  selecting a newly created same-name key B; clearing the optional reference;
  and an unrelated spec update after A was deleted. Assert only explicit
  `spec.ssh_key` changes resolve the new reference.

- [ ] **Step 3: Write failing default and catalog tests**

  Assert template defaults copy a detached `SecretReference`; private catalog
  lock/default references canonicalize; a shared catalog rejects lock/default;
  `editable {}` accepts a caller reference and rejects omission; and policy
  application does not alias policy/reference messages.

- [ ] **Step 4: Run focused tests and verify they fail**

  Run from `fulfillment-service/`:
  `ginkgo run --focus "SSH key|ssh_key" internal/servers internal/utils`

  Expected: FAIL on missing Cluster validation and policy behavior.

- [ ] **Step 5: Extract the reusable validator and use it for BMI and Cluster**

  Implement the interface above by resolving in tenant/project scope, rejecting
  shared Secrets, loading Vault-backed data, validating type/data/OpenSSH
  syntax, and returning `canonicalSecretReference(secret)`. Refactor the BMI
  method to call it without changing BMI immutability.

- [ ] **Step 6: Integrate Cluster create/update and server construction**

  Add `secretStore` to the builder/server, wire it in `register_servers.go`,
  validate after catalog/template defaults on create, and validate only when
  `spec.ssh_key` is explicitly changed on update. Exclude this custom-scoped
  field from the generic reference interceptor.

- [ ] **Step 7: Implement Cluster defaults and global-reference policies**

  Add `decodeGlobalSecretReferencePolicy` reuse, Cluster policy application,
  private-catalog canonicalization, shared-catalog lock/default rejection, and
  caller-required editable-without-default behavior.

- [ ] **Step 8: Run focused and package tests**

  Run from `fulfillment-service/`:
  `ginkgo run --focus "SSH key|ssh_key" internal/servers internal/utils`

  Run: `go test ./internal/cmd/service/start/grpcserver`

  Expected: PASS, including unrelated-update-after-deletion and same-name/new-ID
  recovery cases.

- [ ] **Step 9: Commit Fulfillment validation and policy behavior**

  Commit: `OSAC-5429: validate Cluster SSH key references`

### Task 3: Persist and Project Canonical Cluster Key IDs

**Files:**
- Create: `fulfillment-service/internal/database/migrations/129_finalize_ssh_key_references.up.sql`
- Create: `fulfillment-service/internal/database/migrations/129_finalize_ssh_key_references_test.go`
- Modify: `fulfillment-service/internal/database/migrations.sha256`
- Modify: `fulfillment-service/internal/controllers/cluster/cluster_reconciler_function.go`
- Test: `fulfillment-service/internal/controllers/cluster/cluster_reconciler_function_test.go`
- Modify: `fulfillment-service/internal/cmd/cli/create/cluster/create_cluster_cmd.go`
- Test: `fulfillment-service/internal/cmd/cli/create/cluster/create_cluster_cmd_test.go`

**Interfaces:**
- Consumes: canonical `ClusterSpec.ssh_key.id` and
  `ClusterOrderSpec.SSHKey` from Tasks 1-2.
- Produces: `ClusterOrder.spec.sshKey.id` with no raw key material.
- Produces: Cluster CLI `--ssh-key NAME` constructing
  `SecretReference{Name: strings.TrimSpace(NAME)}`.
- Produces: a trigger that validates Cluster Secret existence only on insert or
  when the `ssh_key.id` value changes, not on status/unrelated updates.

- [ ] **Step 1: Write failing migration tests**

  Assert migration 129 removes `spec.ssh_public_key` from Cluster/BMI rows and
  `fields.ssh_public_key` from both catalog tables. Assert insert and reference
  change to a missing/deleted Secret fail with `Z0002`, same-reference updates
  after Secret deletion succeed, clearing succeeds, and changing from a
  deleted old ID to a live new ID succeeds. Assert deleting a Secret referenced
  by an active Cluster or SSH-key catalog policy remains allowed.

- [ ] **Step 2: Run the migration test and verify it fails**

  Run from `fulfillment-service/`:
  `ginkgo run --focus "Finalize SSH-key references" internal/database/migrations`

  Expected: FAIL because migration 129 does not exist.

- [ ] **Step 3: Implement migration 129 and update its checksum**

  Use JSONB path deletion for all four legacy table paths. Add a Cluster trigger
  that obtains `FOR SHARE` only for a newly selected non-empty ID and never adds
  Cluster/BMI references to `check_secret_not_in_use`.

  Run: `uv run dev.py update hashes`

- [ ] **Step 4: Write failing projection and CLI tests**

  Assert Cluster reconciliation writes only `SSHKey.ID`; nil stays omitted; a
  changed canonical ID updates the CR spec; `--ssh-key tenant-login` builds the
  expected reference; and old CLI flags remain only until Task 8.

- [ ] **Step 5: Implement projection and additive CLI support**

  Replace the Cluster reconciler's raw-value projection with ID-only reference
  projection. Add `runner.args.sshKey`, the `--ssh-key` flag/help, and reference
  construction while retaining old flags temporarily for a green intermediate
  commit.

- [ ] **Step 6: Run focused persistence, controller, and CLI tests**

  Run from `fulfillment-service/`:
  `ginkgo run --focus "Finalize SSH-key references" internal/database/migrations`

  Run: `go test ./internal/controllers/cluster ./internal/cmd/cli/create/cluster`

  Expected: PASS and no key bytes in serialized ClusterOrder fixtures.

- [ ] **Step 7: Commit persistence, projection, and CLI support**

  Commit: `OSAC-5429: project Cluster SSH key Secret IDs`

### Task 4: Resolve Cluster Keys Only When Launching AAP Jobs

**Files:**
- Create: `osac-operator/internal/controller/sshkey/resolver.go`
- Create: `osac-operator/internal/controller/sshkey/resolver_test.go`
- Modify: `osac-operator/pkg/provisioning/provision_lifecycle.go`
- Test: `osac-operator/pkg/provisioning/provision_lifecycle_test.go`
- Modify: `osac-operator/api/v1alpha1/conditions.go`
- Modify: `osac-operator/internal/controller/clusterorder_controller.go`
- Test: `osac-operator/internal/controller/clusterorder_controller_test.go`
- Test: `osac-operator/internal/controller/clusterorder_envtest_test.go`
- Modify: `osac-operator/cmd/main.go`

**Interfaces:**
- Produces `sshkey.SecretsClient`, matching generated
  `privatev1.SecretsClient.Get`.
- Produces:
  `sshkey.Resolve(ctx, client, id, expectedTenant) (string, error)`.
- Produces:
  `provisioning.PrepareTriggerFunc func() (map[string]any, bool, time.Duration)`.
- Produces `RunProvisioningLifecycleWithPrepareTrigger(...)`, with the existing
  `RunProvisioningLifecycle(...)` delegating with a nil hook.
- Produces AAP launch variable `ssh_public_key` only when a non-empty Cluster
  reference resolves successfully.

- [ ] **Step 1: Write failing resolver tests**

  Assert valid Vault-returned data resolves; nil object, missing/deleted object,
  wrong type, empty `public_key`, and tenant mismatch fail; gRPC errors retain
  their status; and no returned error contains the key bytes.

- [ ] **Step 2: Write failing trigger-preparation lifecycle tests**

  Assert preparation runs only for a new trigger, not Skip/Poll/Backoff;
  prepared variables reach `TriggerProvisionWithExtraVars`; `ready=false`
  requeues without launching; and existing lifecycle callers behave unchanged.

- [ ] **Step 3: Run focused package tests and verify they fail**

  Run from `osac-operator/`:
  `go test ./internal/controller/sshkey ./pkg/provisioning -count=1`

  Expected: FAIL because the resolver and exported lifecycle variant are absent.

- [ ] **Step 4: Implement the resolver and lifecycle variant**

  `Resolve` fetches by ID, checks deletion, exact tenant, type
  `SECRET_TYPE_SSH_PUBLIC_KEY`, and non-empty `data["public_key"]`, then returns
  bytes as a string. The lifecycle variant passes its hook directly to
  `runLifecycleCore`; preserve the original function as a compatibility wrapper.

- [ ] **Step 5: Write failing ClusterOrder launch tests**

  Assert the reconciler resolves against the
  `osac.openshift.io/tenant` annotation, launches with exactly the top-level
  `ssh_public_key` variable, leaves the serialized resource ID-only, and never
  resolves during poll. For missing/wrong-tenant data, assert no launch,
  `Progressing=True`, reason `SSHKeyResolutionPending`, a bounded requeue, and a
  message containing only the Secret ID.

- [ ] **Step 6: Wire ClusterOrder reconciliation and production clients**

  Add a `SecretsClient sshkey.SecretsClient` field/constructor parameter, wire
  `privatev1.NewSecretsClient(grpcConn)` in `cmd/main.go`, and use the
  trigger-preparation variant. A nil `spec.sshKey` returns ready with no extra
  variable so provider-default behavior remains available.

- [ ] **Step 7: Run focused operator tests**

  Run from `osac-operator/`:
  `go test ./internal/controller/sshkey ./pkg/provisioning -count=1`

  Run:
  `go test ./internal/controller -count=1 -ginkgo.focus="SSHKey|SSH key"`

  Expected: PASS; no launch occurs for an unresolved/cross-tenant key.

- [ ] **Step 8: Commit trigger-only AAP resolution**

  Commit: `OSAC-5429: resolve Cluster SSH keys for AAP launches`

### Task 5: Pass the Reference Through CaaS Bare-Metal Workers

**Files:**
- Modify: `osac-operator/internal/controller/baremetalworker/reconciler.go`
- Modify: `osac-operator/internal/controller/baremetalworker/infraenv.go`
- Modify: `osac-operator/internal/controller/baremetalworker/bmi.go`
- Modify: `osac-operator/internal/controller/baremetalworker/fulfillment.go`
- Test: `osac-operator/internal/controller/baremetalworker/infraenv_test.go`
- Test: `osac-operator/internal/controller/baremetalworker/bmi_reconcile_test.go`
- Test: `osac-operator/internal/controller/baremetalworker/fulfillment_test.go`
- Test: `osac-operator/internal/controller/baremetalworker_reconciler_test.go`
- Test: `osac-operator/internal/controller/baremetalworker_lifecycle_test.go`
- Modify: `osac-operator/cmd/main.go`

**Interfaces:**
- Consumes: `sshkey.SecretsClient` and `sshkey.Resolve` from Task 4.
- Produces: InfraEnv `spec.sshAuthorizedKey` from resolved bytes.
- Produces: child BMI `spec.ssh_key` cloned from the canonical Cluster
  reference after its ID is checked against ClusterOrder; this preserves a
  non-default project selector without copying key bytes into ClusterOrder.
- Produces: `InfraEnvReady=False` with reason `SSHKeyResolutionPending` when
  resolution fails.

- [ ] **Step 1: Write failing worker tests**

  Replace raw-key fixtures with `SSHKey.ID`. Assert InfraEnv receives the
  resolved key, child BMI requests contain the same ID and canonical Cluster
  project with no inline field, nil references omit both values, an ID mismatch
  between Cluster and ClusterOrder prevents Create, and Secret calls use a
  bounded gRPC deadline.

- [ ] **Step 2: Add failure/recovery tests**

  Assert missing/deleted/wrong-tenant Secrets create no InfraEnv or BMI, retain
  existing workers, set the recoverable InfraEnv condition without bytes, and
  requeue. Replace the ClusterOrder ID and assert the next reconcile resumes.

- [ ] **Step 3: Run focused tests and verify they fail**

  Run from `osac-operator/`:
  `go test ./internal/controller/baremetalworker -count=1`

  Expected: FAIL because workers still read/copy `SSHPublicKey`.

- [ ] **Step 4: Inject the generated Secrets client**

  Add `sshkey.SecretsClient` to `NewReconciler`, use the same per-call timeout
  and transport classification conventions as other Fulfillment calls, and wire
  it from the shared gRPC connection in `cmd/main.go`.

- [ ] **Step 5: Replace raw fallback with reference resolution**

  Delete `resolveSSHPublicKey` and all `template_parameters.ssh_public_key`
  fallback. Resolve bytes immediately before creating a missing InfraEnv and
  validate the reference again immediately before each new BMI Create attempt.
  Pass bytes only to the InfraEnv builder; build BMIs from
  `proto.Clone(cluster.Spec.SSHKey)` after checking that its ID equals
  `co.Spec.SSHKey.ID`. Observation, recovery, and cleanup of existing workers
  must not depend on the Secret remaining readable.

- [ ] **Step 6: Run worker unit and envtest coverage**

  Run from `osac-operator/`:
  `go test ./internal/controller/baremetalworker -count=1`

  Run:
  `go test ./internal/controller -count=1 -ginkgo.label-filter=baremetalworker`

  Expected: PASS, including tenant safety and replacement recovery.

- [ ] **Step 7: Commit the CaaS worker reference flow**

  Commit: `OSAC-5429: pass SSH key references to BMaaS workers`

### Task 6: Remove Raw CaaS Inputs From AAP Templates

**Files:**
- Modify: `osac-aap/collections/ansible_collections/osac/service/roles/cluster_settings/tasks/main.yml`
- Modify: `osac-aap/collections/ansible_collections/osac/service/roles/cluster_settings/tests/test.yml`
- Modify: `osac-aap/collections/ansible_collections/osac/templates/roles/ocp_ci_small/meta/osac.yaml`
- Modify: `osac-aap/collections/ansible_collections/osac/templates/roles/ocp_4_20_small_nico/meta/osac.yaml`
- Modify: `osac-aap/tests/integration/fixtures/clusterorder-test.yaml`
- Modify: `osac-aap/tests/integration/fixtures/clusterorder-ci-test.yaml`

**Interfaces:**
- Consumes: top-level AAP variable `ssh_public_key` from Task 4.
- Produces: `cluster_settings_ssh_public_key`, then materializes it into the
  internal `cluster_settings_template_parameters.ssh_public_key` consumed by
  existing provider roles.
- Retains: provider default Secret fallback when launch input is absent.

- [ ] **Step 1: Extend the isolated role test with failing precedence cases**

  Assert launch variable beats an injected legacy template value; a legacy
  template value alone is ignored; provider default remains the final fallback;
  node requests remain unchanged; and no assertion/debug output prints bytes.

- [ ] **Step 2: Run the isolated test and verify it fails**

  Run from `osac-aap/`:
  `uv run --group development ansible-playbook collections/ansible_collections/osac/service/roles/cluster_settings/tests/test.yml`

  Expected: FAIL because the role still reads ClusterOrder/template parameters.

- [ ] **Step 3: Change the trusted input and remove published parameters**

  Initialize from `ssh_public_key | default('', true)` with `no_log: true`,
  remove `cluster_order.spec.sshPublicKey` and incoming template-parameter
  fallback, add `no_log` to provider-default key materialization, and remove
  `ssh_public_key` from both template metadata files. Keep the final internal
  merge for downstream role compatibility.

- [ ] **Step 4: Update ClusterOrder AAP fixtures**

  Remove raw SSH keys from fixture `spec`/incoming template parameters and pass
  test launch input separately where required.

- [ ] **Step 5: Run isolated AAP validation**

  Run the isolated playbook from Step 2.

  Run: `uv run ansible-lint collections/ansible_collections/osac/service/roles/cluster_settings`

  Expected: PASS.

- [ ] **Step 6: Commit AAP input cleanup**

  Commit: `OSAC-5429: consume resolved CaaS SSH keys in AAP`

### Task 7: Migrate Integration and E2E Fixtures

**Files:**
- Modify: `fulfillment-service/it/it_baremetal_instance_lifecycle_test.go`
- Modify: `fulfillment-service/it/it_bare_metal_instance_catalog_items_test.go`
- Modify: `fulfillment-service/it/it_cluster_catalog_items_test.go`
- Modify: `tests/e2e/core/osac_cli.py`
- Modify: `tests/e2e/caas/conftest.py`
- Modify: `tests/e2e/caas/sanity/test_cluster_create.py`
- Modify: `tests/e2e/caas/sanity/test_cluster_delete_feedback_light.py`
- Modify: `tests/e2e/caas/regression/test_cluster_node_sets.py`
- Modify: `tests/e2e/caas/regression/test_cluster_version.py`
- Modify: `tests/e2e/storage/test_caas_cluster_storage.py`
- Modify: `tests/e2e/references/test_cluster_baremetal_references.py`
- Modify: affected `tests/unit/test_caas_*.py` and
  `tests/unit/test_cluster_cli_node_sets.py`

**Interfaces:**
- Consumes: CLI `--ssh-key`, registered Secret helper, Cluster `ssh_key`, and
  ID-only ClusterOrder from earlier tasks.
- Produces: `OsacCLI.create_cluster(..., ssh_key: str | None)` and
  `create_cluster_with_catalog_item(..., ssh_key: str | None)`.
- Produces: a session-scoped CaaS `ssh_key_secret` fixture that registers
  `OSAC_SSH_PUBLIC_KEY_PATH` and cleans it up.

- [ ] **Step 1: Migrate CLI-helper unit tests first**

  Update Python unit expectations from `--ssh-public-key-file PATH` to
  `--ssh-key NAME`. Assert neither helper emits old flags.

- [ ] **Step 2: Run Python unit tests and verify they fail**

  Run:
  `uv run pytest -n 0 tests/unit/test_caas_teardown_order.py tests/unit/test_cluster_cli_node_sets.py tests/unit/test_caas_version_selection.py tests/unit/test_caas_two_node_sets.py tests/unit/test_caas_worker_bmi_visibility.py`

  Expected: FAIL until helper signatures and fixtures change.

- [ ] **Step 3: Update shared Python helpers and CaaS fixtures**

  Replace both CLI helper parameters with `ssh_key`. Build the CaaS fixture
  using `registered_ssh_key_secret`, the configured public-key file contents,
  and a unique name; preserve cleanup through the context manager.

- [ ] **Step 4: Migrate the focused CaaS catalog journey**

  Change catalog policy to `ssh_key: {editable: {}}`, pass the Secret name at
  create, and assert Cluster canonical reference, ID-only ClusterOrder,
  InfraEnv's resolved value, and every child BMI's matching `ssh_key.id` with no
  inline key. Keep existing lifecycle, metering, and cleanup assertions.

- [ ] **Step 5: Migrate remaining CaaS and direct-BMaaS E2E callers**

  Pass the fixture's Secret name in CaaS/storage journeys. In the reference
  suite, register a Secret and create the BMI with `ssh_key: {name: ...}`;
  always clean it up after the BMI.

- [ ] **Step 6: Migrate Fulfillment integration fixtures**

  Use registered Secrets for SSH-specific cases and `user_data` for unrelated
  BMI lifecycle fixtures. Replace Cluster catalog raw policies/assertions with
  global Secret-reference policies and canonical ID assertions.

- [ ] **Step 7: Run local Python checks and collection only**

  Run the Python unit command from Step 2.

  Run: `uv run ruff check tests/e2e tests/unit`

  Run: `uv run ruff format --check tests/e2e tests/unit`

  Run:
  `uv run pytest --collect-only tests/e2e/caas tests/e2e/references/test_cluster_baremetal_references.py tests/e2e/storage/test_caas_cluster_storage.py`

  Expected: unit PASS, Ruff PASS, and collection succeeds. Do not execute
  deployed Fulfillment integration or E2E suites locally.

- [ ] **Step 8: Commit migrated integration and E2E code**

  Commit: `OSAC-5429: migrate SSH key integration and E2E fixtures`

### Task 8: Remove Inline Contracts and Finish Documentation

**Files:**
- Modify: `proto/private/osac/private/v1/baremetal_instance_type.proto`
- Modify: `proto/private/osac/private/v1/baremetal_instance_catalog_item_type.proto`
- Modify: `proto/private/osac/private/v1/cluster_type.proto`
- Modify: `proto/private/osac/private/v1/cluster_template_type.proto`
- Modify: `proto/private/osac/private/v1/cluster_catalog_item_type.proto`
- Modify: `fulfillment-service/internal/servers/private_baremetal_instances_server.go`
- Modify: `fulfillment-service/internal/servers/baremetal_instance_catalog_item_policies.go`
- Modify: `fulfillment-service/internal/servers/private_clusters_server.go`
- Modify: `fulfillment-service/internal/servers/cluster_catalog_item_policies.go`
- Modify: `fulfillment-service/internal/controllers/baremetalinstance/ssh_key_resolution.go`
- Modify: `fulfillment-service/internal/controllers/baremetalinstance/baremetalinstance_reconciler_function.go`
- Modify: `fulfillment-service/internal/cmd/cli/create/cluster/create_cluster_cmd.go`
- Modify: `fulfillment-service/.golangci.yml`
- Modify: `osac-operator/api/v1alpha1/clusterorder_types.go`
- Test: `fulfillment-service/internal/controllers/baremetalinstance/baremetalinstance_reconciler_function_test.go`
- Test: `fulfillment-service/internal/servers/baremetal_instances_server_test.go`
- Test: `fulfillment-service/internal/servers/private_baremetal_instances_server_test.go`
- Test: `fulfillment-service/internal/servers/baremetal_instance_catalog_items_server_test.go`
- Test: `fulfillment-service/internal/servers/private_baremetal_instance_catalog_items_server_test.go`
- Test: `fulfillment-service/internal/servers/private_cluster_catalog_items_server_test.go`
- Test: `fulfillment-service/internal/servers/private_clusters_server_test.go`
- Test: `fulfillment-service/internal/servers/catalog_item_validation_test.go`
- Test: `fulfillment-service/internal/servers/catalog_item_field_policies_test.go`
- Test: `fulfillment-service/internal/servers/worker_attribution_test.go`
- Test: `fulfillment-service/internal/servers/event_publisher_test.go`
- Test: `fulfillment-service/internal/cmd/cli/create/fieldutil/fieldutil_test.go`
- Test: `fulfillment-service/internal/cmd/cli/describe/catalogitem/view_test.go`
- Test: `osac-operator/internal/controller/baremetalworker_tenant_safety_test.go`
- Test: remaining exact files reported by the generated-API compiler after
  Task 8 Step 4; add each path to the task checklist before editing it.
- Modify: `docs/guides/developer/baremetalinstance-guide.md`
- Modify: `docs/architecture/cluster-fulfillment.md`
- Modify: `fulfillment-service/docs/CATALOG_ITEMS.md`
- Modify: `docs/INTEGRATION-TESTING.md`
- Modify: `tests/e2e/AGENTS.md`
- Generated: protobuf and operator artifacts from Task 1

**Interfaces:**
- Produces: reserved BMI spec name/number `ssh_public_key`/2.
- Produces: reserved BMI catalog name/number `ssh_public_key`/1.
- Produces: reserved Cluster spec name/number `ssh_public_key`/5.
- Produces: reserved Cluster defaults name/number `ssh_public_key`/2.
- Produces: reserved Cluster catalog name/number `ssh_public_key`/2.
- Removes: ClusterOrder `SSHPublicKey`, raw CLI flags, raw controller fallbacks,
  string catalog policies, and deprecated lint suppression.

- [ ] **Step 1: Change contract tests to require final removal**

  Assert every old descriptor lookup returns nil, every exact number/name is in
  the corresponding reserved range/name set, and all replacement fields remain
  present with the Task 1 numbers.

- [ ] **Step 2: Run contract tests and verify they fail**

  Run: `go test ./fulfillment-service/internal/validation`

  Expected: FAIL because compatibility fields still exist.

- [ ] **Step 3: Remove and reserve all five protobuf fields**

  Delete old field declarations/comments, add exact number and name
  reservations, and update replacement comments so they no longer mention
  mutual exclusion with removed fields.

- [ ] **Step 4: Regenerate and use compile failures as the residual-work list**

  Run: `make -C proto generate`

  Run: `go test ./fulfillment-service/... ./osac-operator/...`

  Expected initially: compile failures identifying stale generated API uses in
  production/tests; no UI files change.

- [ ] **Step 5: Remove compatibility code and migrate remaining unit fixtures**

  Delete raw server validation/immutability, catalog string policies,
  controller fallback, CLI file reading/help, ClusterOrder raw field, and lint
  suppression. Convert SSH-specific tests to registered refs and unrelated
  fixtures to `user_data`; do not weaken authentication assertions.

- [ ] **Step 6: Regenerate final operator artifacts**

  Run from `osac-operator/`: `make manifests generate helm-crds check-helm-crds`

  Expected: ClusterOrder schemas contain `sshKey.id` and no `sshPublicKey`.

- [ ] **Step 7: Update focused documentation and touched-area maps**

  Document Secret registration/selection, mutable Cluster replacement after
  deletion, initial-only BMI behavior, CaaS ID flow, CI-owned deployed coverage,
  and the focused local commands. Update the E2E text that currently says the
  shared catalog locks an inline key.

- [ ] **Step 8: Run the residual inline-input grep gate**

  Search editable source and tests for `SshPublicKey`, `sshPublicKey`,
  `ssh-public-key-file`, and API-path `ssh_public_key`. Every remaining match
  must be one of: a reserved-name assertion, provider materialization
  (`sshPublicKey`/`sshAuthorizedKey`), internal AAP variable, Secret data key,
  or historical migration test. Remove all other matches.

- [ ] **Step 9: Run local final validation**

  Run: `make -C proto lint`

  Run from `fulfillment-service/`:
  `uv run dev.py lint`

  Run:
  `ginkgo run --timeout 10m internal/servers`

  Run:
  `go test ./internal/controllers/cluster ./internal/controllers/baremetalinstance ./internal/cmd/cli/create/cluster`

  Run from `osac-operator/`:
  `make fmt lint check-helm-crds`

  Run:
  `go test ./internal/controller/sshkey ./internal/controller/baremetalworker ./pkg/provisioning -count=1`

  Run the AAP isolated playbook from Task 6, Python unit/Ruff commands from
  Task 7, and affected E2E collection.

  Expected: all local checks pass.

- [ ] **Step 10: Commit final removal and documentation**

  Commit: `OSAC-5429: remove inline BareMetal and Cluster SSH keys`

### Task 9: Record CI-Deferred Boundary Validation

**Files:**
- Modify only if a validation result requires a correction; otherwise no source
  changes.

**Interfaces:**
- Produces: a final implementation report that separates locally verified
  checks from PR-CI-deferred deployed boundaries.

- [ ] **Step 1: Inspect the complete branch diff and commit stack**

  Resolve fork/upstream roles by URL with
  `.osac-ai-skills/tools/resolve-remotes.sh` (or the bootstrap-installed copy),
  then compare against the merge-base with the detected upstream default
  branch. Confirm the retained `4d43771e6` commit, design/plan commits,
  generated artifacts, and no unrelated or UI changes.

- [ ] **Step 2: Run applicable pre-commit checks over the branch diff**

  Run: `pre-commit run --from-ref <detected-merge-base> --to-ref HEAD`

  Expected: PASS, or document a hook unavailable for an environmental reason.

- [ ] **Step 3: List PR-CI-deferred commands without running them locally**

  Defer and report:

  - Fulfillment Kind integration through
    `make -C osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment`.
  - Operator deployed integration through `make -C osac-operator integration-tests`.
  - AAP Kind integration through `make -C osac-aap test`.
  - Focused deployed CaaS through
    `uv run pytest -n 0 tests/e2e/caas/sanity/test_cluster_create.py::test_cluster_create`.
  - Direct BMaaS reference E2E through
    `uv run pytest -n 0 tests/e2e/references/test_cluster_baremetal_references.py -k baremetal_instance_chain_by_name`.

- [ ] **Step 4: Correct only failures introduced by this branch**

  If final local checks fail, add the smallest test-first correction and commit
  it as `OSAC-5429: fix SSH key migration validation`; otherwise leave the
  worktree clean without an empty commit.

- [ ] **Step 5: Report completion evidence**

  Include commit IDs, local commands/results, E2E collection results, deferred
  CI commands, and any remaining environmental limitation. Do not claim
  deployed integration/E2E passed before PR CI reports it.
