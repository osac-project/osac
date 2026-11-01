# Integration testing

Use the touched-area map in the affected component's `AGENTS.md` to select
validation. Read the corresponding section below for suite boundaries and gaps.
Commands and inline paths in each component section are relative to that
component's directory unless explicitly marked as repository-root commands.

## Contents

- [Shared tiers and boundaries](#shared-tiers-and-boundaries)
- [Planning evidence](#planning-evidence)
- [Release automation](#release-automation)
- [fulfillment-service](#fulfillment-service)
- [osac-installer](#osac-installer)
- [osac-operator](#osac-operator)
- [bare-metal-fulfillment-operator](#bare-metal-fulfillment-operator)
- [osac-aap](#osac-aap)
- [osac-csi-driver](#osac-csi-driver)
- [osac-metering](#osac-metering)
- [tests/e2e](#testse2e)
- [Multi-branch test isolation](MULTI-BRANCH-ISOLATION.md)

## Shared tiers and boundaries

Use these tier names consistently:

- **Unit** — one package or function with external dependencies mocked.
- **Envtest** — a real Kubernetes API server and etcd process with the
  component's controllers or providers driven in-process; this is not a Kind
  integration test.
- **Component integration** — the component runs against a real Kind,
  testcontainer, broker, database, or protocol endpoint as documented by the
  component.
- **Contract** — a focused test of a boundary between two components or
  between a component and a provider.
- **E2E** — a cross-component user journey through the deployed OSAC stack.

Component-specific test labels in a matrix are subtiers of one of these canonical
tiers. The matrix must make that mapping explicit; a local label does not add
another tier or satisfy a Contract requirement by itself.

A lower tier does not satisfy a higher-tier requirement. Every component
integration section must disclose which dependencies are real and which are
faked or stubbed. If the required boundary has no qualifying suite, record
the gap and link the owning follow-up task; do not describe a lower-tier or
stub-only test as coverage of that boundary.

When a suite, command, or dependency boundary changes, update this guide and
its component's touched-area map in the same change. Link follow-up tickets
with their Jira URLs.

Build/package validation checks image assembly and dependencies. It is separate
from the test tiers and does not replace the applicable integration tests.

### Work ownership

Use the test tier to route implementation work. Unit, Envtest,
component-integration, and Contract tests for changed code belong to the owning
`[DEV]` story. Deployed cross-component user journeys belong to `[QE]` stories.
The reviewed test plan must classify each case by tier and owner; do not copy a
component-integration case into a QE story or treat an E2E case as covered by a
lower-tier test.

## Planning evidence

Design test plans must include a coverage matrix derived from the affected
components' touched-area maps and the actual test infrastructure. Use one row
per behavior and required boundary; a cross-component case may need several
rows. Include unit-only changes with their applicable tier rather than requiring
integration tests for every change.

Each row must identify:

- The owning component, touched behavior, and requirement/interface references.
- The required tier and the boundary whose behavior the test proves.
- The test-case IDs and existing suite/file to extend, or a clearly marked
  proposed location for new coverage.
- The execution command, working directory, and environment prerequisites.
- Which services, APIs, databases, providers, and controllers run for real,
  and which are simulated, mocked, or omitted.
- Any unavailable suite or unresolved infrastructure prerequisite, with the
  owning follow-up's Jira URL. If no owner or ticket exists, report that as
  unresolved; do not invent a ticket or claim the boundary is covered.

Verify existing suite paths and commands against the repository. Mark proposed
commands as proposed; where execution is not yet defined, record the gap
instead of supplying a plausible command. Name a specific tier and harness
rather than leaving alternatives such as "envtest or Kind" or "Cypress or
equivalent". Describe the running dependencies: a fixture-based test is not
evidence of a deployed boundary merely because it is labelled integration.

For timing or asynchronous behavior, identify the trigger, observable result,
measurement interval, and pass/fail bound. State how the test isolates the path
being measured from fallback polling, periodic resync, or mocked completion.
Flag contradictory or unspecified source behavior instead of inventing an
expected result.

Decomposition must carry the applicable matrix evidence into each
implementation or QE task's testing approach, including case IDs and
unresolved gaps. A task must remain actionable when ingested independently of
the feature test plan. Keep new suite/infrastructure work explicit in the
decomposition.

Before reporting a plan or decomposition ready, check every touched area against
its required boundary. Distinguish "has a planned test case" from "has an
identified execution path" and from "execution passed". Report missing or
wrong-tier coverage as unresolved even when all requirement and interface IDs
have mappings. A documented provider gap does not require real hardware for
an unrelated status-projection change; scope the test to the changed behavior.

### Evidence checks before completing a phase

Apply these checks during generation and self-review, then correct the artifacts
before reporting the phase complete:

| Claim | Evidence required |
|---|---|
| Integration coverage | Identify the exercised boundary and running dependencies. Lint, typechecking, collection, and schema-generation checks are static/build validation, not integration tests. |
| Envtest coverage | Envtest provides a Kubernetes API server and etcd. It does not provide fulfillment-service, PostgreSQL, or provider controllers; name and start those separately when the test requires them. |
| Cross-component coverage | A fake endpoint proves the caller's handling of that double, not the receiving service's persistence or reconciliation. Separate those cases and choose the harness for each. |
| Runnable test | Cite the repository file defining the command and the suite it runs. Replace vague instructions such as "run focused integration tests" with that command, or record execution as blocked pending an explicitly proposed harness. |
| Source contradiction or missing requirement | Cite the exact source file, section, and passage. Re-read the authoritative PRD/design before declaring a blocker; distinguish stale Jira text from a contradiction in those documents. |

For an existing approved design, retain its explicit assertions (including
negative, exhaustive, lifecycle, and timing assertions) or record why an
assertion cannot be planned. Do not replace them with a generic compatibility
check simply because it shares the same requirement or interface ID.

After decomposition, reconcile the test plan and tasks in both directions. If a
task restores a missing assertion or corrects a boundary, update the
corresponding test case and coverage row in the same phase. Do not report a
complete mapping while leaving the plan and tasks with different expected
behavior. Preserve earlier versions separately when conducting an evaluation.

Report behavioral coverage, execution readiness, and test execution results
separately. A case with a proposed harness or unresolved command is planned but
not execution-ready; static checks passing does not change that status.

## Implementation evidence

Every change must classify its touched behaviors against the tier boundaries
above and, when the affected component has one, its touched-area map. Changes
that do not alter runtime behavior need no integration coverage; record that
classification explicitly. Classify behavior-changing generated config against
the touched-area map.

Before opening a pull request:

- Every required integration row must have an executed, passing test through
  the named boundary. Unit tests and mocks do not satisfy a real component
  boundary; a documented provider gap does not make a lower-tier test
  coverage of that boundary.
- Cite the executed command and working directory per row. Lint, typecheck,
  collection, and schema-generation checks are build validation, not
  integration evidence.
- Missing or wrong-tier coverage is reported as a gap, never as covered. Link
  it to the owning follow-up ticket using its Jira URL when one exists. If no
  owner or ticket exists, report the gap as unresolved; do not invent a ticket.
- The PR description or review report must state, per touched boundary: the
  exercised boundary, running dependencies, executed command, and result — or
  report the gap as unresolved with its owning ticket URL when one exists. If
  there is no owner or ticket, state that explicitly; never claim the boundary
  is covered.

## Agentic CI sandbox

The repository overlay provides early Autofix feedback. It does not replace
authoritative GitHub CI, component integration, or deployed E2E gates. Its
[touched-area map](../.agentic-ci/AGENTS.md) owns validation of the overlay itself.
All cases below belong to [DEV]
[OSAC-6172](https://redhat.atlassian.net/browse/OSAC-6172).

| Behavior | Tier | Execution and boundary |
|---|---|---|
| API/schema dependency selection, large tracked/untracked streams, Git fallbacks, UI/E2E routing, and failure propagation | Unit | Use the [canonical local command](../.agentic-ci/AGENTS.md#touched-area-map); real temporary Git repositories and Bash execute the actual YAML validation bodies, while expensive commands are recording doubles |
| Production setup and API/schema/UI/E2E scenarios | Component integration | Use the production Autofix image and OpenShell runner with the candidate overlay; actual toolchains, allowed registries, lint, supported unit tests, and pytest collection run. No OSAC deployment, container services, Kubernetes API, or provider is started |
| Deliberate API test failure | Component integration | Add a failing unit test in an isolated production scenario, verify the go-test validation record fails, and run a clean scenario afterward |
| Dependency copy-back and optional discard | Component integration | Measure three paired trials on the same production image; add exact generated paths only for median savings of at least 10% and five seconds, then verify restoration on reuse. The current overlay discards only `osac-ui/node_modules` and `osac-aap/.venv`. |

Full UI validation remains intentional. E2E collection creates an external venv
and installs root dependencies once, only when selected. The overlay's
[skips](../.agentic-ci/config.yml) specify container, Envtest, network, live-service
and metering-execution exceptions. Collection proves import readiness for
selected cases; deployed journeys and metering coverage require their own gates.
Selected/deselected counts are revision-specific evidence, not fixed thresholds.

Production evidence must identify the candidate/base commits, image digest,
runner revision, effective merged profile, and selected toolchain versions.
Verify both project-selected interpreters (fulfillment 3.14 and AAP 3.13) with
Python downloads disabled. Use a fresh setup and select the candidate as the
overlay source: the runner reads it before the agent runs. Local/Podman runners
ignore profile overlays and cannot establish this acceptance boundary.

Attach `_run/sandbox-setup.json`, `_run/sandbox-validation.json`, and
`_run/toolchains.json`, along with module-selection logs, UI results, E2E counts,
and copy-back measurements. Include `_run/sandbox-discard.json` when paths are
added. A successful overall agent exit does not imply successful validation;
inspect required records for failure, timeout, error, or not-run status. Label
the intentional negative scenario separately. Missing production execution or
profiling evidence remains an acceptance gap under
[OSAC-6172](https://redhat.atlassian.net/browse/OSAC-6172).

## Release automation

Touched-area requirements: [workflow guide](../.github/AGENTS.md#release-safety).

Run from the repository root with PyYAML 6.0.3, Bash, and jq installed:

```sh
python3 -m unittest discover -s .github/scripts -p test_umbrella_release.py -v
```

This is **[DEV] Unit** coverage. Synthetic Helm archives exercise the package
inventory and release-note helpers. The tests execute the workflow's actual
notification Bash with GitHub CLI and Slack requests captured by local fakes;
the `uv` fake uses the current Python interpreter without fetching dependencies.
Cases cover reused subchart versions, CRDs, standalone CSI charts, fallback
metadata, failed publication, nightly/skip/cancellation conditions, and the
actual chart release step outcome when its job allows errors. Existing release
tests also cover stable Latest selection and check that the workflow retains
its immutable-version guard.

These tests do not validate hosted GitHub scheduling, real Slack delivery or
rendering, or Konflux build/policy/staging operations. Those boundaries require
an authorized hosted run; local mocks do not establish that coverage.

## fulfillment-service

Touched-area requirements: [component guide](../fulfillment-service/AGENTS.md#integration-tests).

### Test tiers and commands

| Tier | Location / command | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | `internal/`; `ginkgo run -r internal` | Fulfillment logic and adapters covered by package tests | External services are mocked where the package tests use mocks. |
| Component integration | `it/`; `make -C ../osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment` | Deployed Fulfillment Service, its database, and the CLI binary built from this checkout. The specs labelled `networking-operator` run in a second phase with the local OSAC Operator image deployed and `global.networking.provisioningEnabled=false`; the first phase excludes them. | CaaS, VMaaS, BMaaS, and external provider workflows unless a specific test exercises them. |
| Component integration ([DEV] OSAC-6553) | `it/it_service_accounts*.go`; focused command in [service-account coverage](#coverage-notes) | Deployed Fulfillment API, PostgreSQL, Keycloak, and the configured runtime IDP principal for CRUD, tenant isolation, PENDING-to-READY credential Secret provisioning with client ID and secret retrieval, and client lifecycle | Failure tests run the service and reconciler in process and inject Keycloak transport failures; external provider provisioning is out of scope. |
| Component integration ([DEV] OSAC-6307) | `internal/servers` and `internal/database/migrations/126_hold_external_ip_capacity_until_archive_test.go`; from `fulfillment-service/`, run `go test ./internal/servers ./internal/database/migrations -timeout 20m` | Fulfillment server logic and PostgreSQL test container, including archive-time capacity and exclusivity for terminating consumers | Kubernetes and provider cleanup are not run; release completion is exercised by operator tests with provider fixtures. |
| E2E | `../tests/e2e/` | Cross-component OSAC user journeys | Depends on the deployed test environment and its configured providers. |

### Fulfillment integration timings

The `SUITE=fulfillment` installer target uses Ginkgo's native `--json-report`
flag when `FULFILLMENT_IT_REPORT_DIR` is set. The default is empty: local runs
neither generate nor clear reports. To opt in from the repository root, run:

```bash
make -C osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment FULFILLMENT_IT_REPORT_DIR=../fulfillment-service/logs/timings
```

Relative directories resolve from `osac-installer/`. CI sets an absolute
`FULFILLMENT_IT_REPORT_DIR` under `fulfillment-service/logs/timings/`, which its
existing `logs` artifact includes. The three phases write `service-only.json`,
`networking-operator.json`, and `bmaas-disabled.json`. Only the
networking-operator phase deploys the OSAC Operator.
When reporting is enabled, those three prior files are cleared before the run
so a reused local directory cannot retain stale reports for unreached phases.
A missing report does not imply a passing phase. Use the originating Actions
run to identify the tested revision.

These reports are for maintainers investigating CI runtime or comparing fixture
costs before and after an optimization. Download the artifact and inspect its
native Ginkgo suite and spec reports. Total runtime, status, attempts, seed,
suite configuration, and node events support analysis outside the test suite.
For example, list executed specs from a downloaded report with:

```bash
jq '.[] | .SpecReports[]
    | select(.LeafNodeType == "It" and .State != "skipped" and .State != "pending")
    | {name: ((.ContainerHierarchyTexts + [.LeafNodeText]) | join(" ")),
       seconds: (.RunTime / 1000000000)}' service-only.json
```

Native reports include captured output, failures, and report entries, and
should be handled as CI diagnostic logs. They are not sanitized timing-only
summaries. Ginkgo documents the format in
[machine-readable reporting](https://onsi.github.io/ginkgo/#generating-machine-readable-reports).

Fixtures called inside an `It` remain part of its body; node timings do not
separate that fixture work. Build, deployment and test compilation time are
outside suite runtime; use Actions step timestamps and build logs for those
costs. Runtime savings require comparable deployed runs and are not established
by unit tests.

Run `go test ./it -run TestCatalogItemDeletionProbe` from `fulfillment-service/`
for unit coverage of deletion observation, signal throttling, cancellation, and
concurrent archival using callback doubles. Catalog cleanup observes every
200ms, keeps its five-minute bound, and signals no more often than every two
seconds; it waits for archival before removing dependencies. The complete
installer command in the tier table remains the component-integration check.

### Coverage notes

- **StorageBackend ownership ([DEV] [OSAC-6868](https://redhat.atlassian.net/browse/OSAC-6868)):** `internal/validation/storage_backend_ownership_test.go` covers explicit and omitted ownership through the real validation interceptor. `internal/servers/private_storage_backends_server_test.go` combines that interceptor with the real private server and PostgreSQL, then reads in a separate transaction to verify rejection leaves no backend and supported configurations persist. Run `go test ./internal/validation -count=1` and `go test ./internal/servers -count=1 -timeout=10m -ginkgo.focus='Storage backend ownership|Private storage backends server'` from `fulfillment-service/`. Tenancy and attribution are suite doubles; these tests do not exercise a deployed service. `it/it_storage_backend_ownership_test.go` covers the deployed private API/CLI and database boundary through the Fulfillment installer target above. No operator, AAP, or storage-array provisioning is exercised.

- **Tenant storage onboarding projection ([OSAC-6575](https://redhat.atlassian.net/browse/OSAC-6575), DEV):** `internal/controllers/tenant/tenant_storage_onboarding_test.go` covers exact operator condition projection, missing/unknown evidence, source isolation, timestamps, and unchanged observations through the reconciler with mocked hub and Kubernetes clients. `it/it_tenant_storage_onboarding_test.go` exercises operator Tenant CR status to explicit private Tenant `Signal` to persisted private API status using real Kubernetes, the deployed Fulfillment controller/API, and PostgreSQL. [OSAC-6684](https://redhat.atlassian.net/browse/OSAC-6684) adds repeated reads and signals with unchanged storage evidence; a changed compute-readiness reason in the same response confirms the later hub observation occurred. Run the Fulfillment installer target above, or `ginkgo run --timeout 20m --focus 'Tenant storage onboarding feedback' it` against a compatible deployed environment. The fixture supplies the operator condition; it does not prove automatic feedback-watch delivery, storage readiness calculation ([OSAC-6447](https://redhat.atlassian.net/browse/OSAC-6447)), AAP execution, remote cluster access, real storage-provider behavior, or VM consumption ([OSAC-6596](https://redhat.atlassian.net/browse/OSAC-6596)).
- **Service-account API and Keycloak lifecycle ([DEV] OSAC-6553):** `it_service_accounts_test.go` covers the deployed gRPC and REST APIs, tenant-admin authorization and tenant isolation, dry-run behavior, maximum tenant/client-ID lengths, the PENDING-to-READY transition, credential Secret retrieval of both client ID and secret, and deletion finalizer/retry behavior. Service-account creation does not assign roles; support for service-account subjects in RoleBindings is tracked by [OSAC-6554](https://redhat.atlassian.net/browse/OSAC-6554). The suite uses the deployed Fulfillment API, PostgreSQL, Keycloak, and the runtime principal mounted for the gRPC server. `it_service_accounts_failures_test.go` runs the service and reconciler in process against real PostgreSQL and Keycloak. It injects failures in the client-registration POST to `/clients-registrations/default` and registration DELETE; its `commit` case uses a deferred PostgreSQL constraint trigger to reject persistence. These failure cases run the reconciler directly rather than through the deployed controller loop. From `fulfillment-service/`, after deploying a compatible image and configuring the required Kind service hostnames and trusted TLS bundle, run `GOTOOLCHAIN=go1.26.3 go test ./it -timeout 75m -run '^TestIntegration$' -args -ginkgo.focus='Service-account infrastructure' -ginkgo.v -ginkgo.no-color`. The suite does not cover secret rotation ([OSAC-6555](https://redhat.atlassian.net/browse/OSAC-6555)) or deployed cross-component journeys ([OSAC-6557](https://redhat.atlassian.net/browse/OSAC-6557)).
- **BareMetalInstanceType architecture:** Unit validation is in `internal/validation/baremetal_hardware_spec_contract_test.go`. PostgreSQL component-integration tests cover Create validation/persistence in `internal/servers/baremetal_instance_type_architecture_test.go` and canonicalization, preservation, idempotence and rollback in `internal/database/migrations/125_canonicalize_bare_metal_type_architectures_test.go`. Create tests use in-process handlers with mocked tenancy/attribution.
- **CLI commands that only call Fulfillment APIs:** Cover them in `fulfillment-service/it/`.
- **RoleBinding authorization ([OSAC-6146](https://redhat.atlassian.net/browse/OSAC-6146), DEV):** `it_role_binding_reconciler_test.go` checks Keycloak role grant/removal and reader-role behavior; `it_role_binding_authorization_test.go` checks tenant-admin-only denial, `admins` access after fresh login, and denial after both grants are removed. Run the full focused coverage from `fulfillment-service/` with `ginkgo run --timeout 20m --focus 'Role binding reconciler|grants system-wide tenant listing while a tenant admin is in admins' it`. The reconciler group includes reader-role cases that create cluster, catalog, and network fixtures. These use deployed Fulfillment and Keycloak. Existing-token revocation is tracked in [OSAC-6375](https://redhat.atlassian.net/browse/OSAC-6375).
- **Cluster reserved-tenant placement ([DEV]):** `internal/servers/private_clusters_server_test.go` and `clusters_server_test.go` cover explicit shared placement, administrator default-to-shared, no persistence/request mutation, shared catalog selection by a real tenant, forbidden tenant moves, and DAO-seeded legacy shared Cluster read/status/delete. Run `ginkgo run --timeout 10m internal/servers` from `fulfillment-service/`; PostgreSQL is real, while attribution and surrounding suite tenancy are doubles (the reserved-placement cases use real administrator tenancy logic). `it/it_public_clusters_test.go` adds authenticated public administrator rejection and an admin List proving no persisted result; ordinary user creation remains tenant-owned. Run the Fulfillment installer target above for this deployed API/database/identity boundary. Neither suite proves worker/provider teardown: the existing tenant-owned CaaS journey below belongs to [QE]; see the [operator coverage notes](#osac-operator) for deployed worker coverage gaps. Existing shared Clusters are not migrated automatically; retain supported read/status/delete for cleanup, then recreate in a real tenant rather than moving live objects across tenants.
- **Reserved metadata on the public API ([OSAC-6394](https://redhat.atlassian.net/browse/OSAC-6394), [OSAC-6395](https://redhat.atlassian.net/browse/OSAC-6395), [OSAC-6396](https://redhat.atlassian.net/browse/OSAC-6396)):** `internal/servers/generic_mapper_test.go` covers case-sensitive matching of `admin.osac.openshift.io` and dot-delimited subdomains, recursive public projection, and rejection of explicit reserved input. `internal/servers/generic_server_test.go` covers empty-map clearing, private update behavior, stale `lock=true` rejection, and a deterministic PostgreSQL row-lock case: a private transaction updates reserved labels and annotations, a public update waits on that row, and PostgreSQL confirms the blocked update before the writer commits. The public update then retains the latest reserved entries while applying the ordinary map replacement. Run the focused unit cases from `fulfillment-service/` with `ginkgo run --no-color --focus 'Generic mapper|preserves reserved metadata from the locked object on public updates|rejects a stale version when public update locking is requested|preserves the latest reserved metadata after waiting for a concurrent update|preserves reserved metadata while merging nonempty unmasked labels' internal/servers`. The suite uses an ephemeral real PostgreSQL container; it does not exercise a deployed Fulfillment service or gateway.
  `fulfillment-service/it/it_labels_test.go` and `it_annotations_test.go` cover public Create/Update rejection, public whole-map replacement, omission preservation, and filtered public responses alongside private administrator reads of stored metadata. Run them through `make -C ../osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment`. These cases use the deployed Fulfillment gRPC APIs, PostgreSQL, and the test identities; they do not exercise REST/Connect clients or cluster provisioning. Related test-plan cases: TC-IS3-01, TC-IS3-02, TC-IS4-01, TC-IS4-03, TC-IS5-01, and TC-IS5-02.
- **Shared metadata integration fixtures ([OSAC-6398](https://redhat.atlassian.net/browse/OSAC-6398)):** `it/it_metadata_test.go` creates a tenant-scoped ComputeInstance using the shared network, storage, and template fixtures, writes reserved labels and annotations through the private administrator API, and checks Tenant Admin and Tenant User reads through REST and CLI. A user from the `sales` tenant verifies isolation. The fixture snapshots the complete private object before and after the write and confirms reserved values remain stored while tenant and any existing owner-reference annotation remain unchanged. The fake-client helper test seeds an owner-reference annotation to verify map preservation. The language-neutral QE contract is a same-tenant Tenant Admin and Tenant User, a second-tenant user, one catalog-backed ComputeInstance with a tenant network attachment and `HALTED` run strategy, unique reserved and ordinary markers in both maps, and deletion of the instance before its dependencies; the Python runner recreates this contract with its own fixtures.

  Run the deployed case with `make -C ../osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment`, or against an already deployed compatible Kind environment with `ginkgo run --timeout 20m --focus 'reserved metadata privacy fixtures' it` from `fulfillment-service/`. The deployed Fulfillment service, PostgreSQL, Keycloak authentication, REST gateway, and CLI are real. Test fixtures seed networking readiness and storage/template dependencies; no VM, cluster, AAP, or external provider provisioning runs. Cleanup is registered as resources are created, so Ginkgo removes the ComputeInstance before its dependencies.
- **Metadata contract matrix ([OSAC-6399](https://redhat.atlassian.net/browse/OSAC-6399), [OSAC-6401](https://redhat.atlassian.net/browse/OSAC-6401), DEV):** `it/it_metadata_fixtures_test.go` creates and cleans up workload and storage/administration fixtures. `it/it_metadata_test.go` exercises private metadata replacement across the resource matrix and selected tenant REST/CLI projection and filter behavior. `it/it_public_volumes_test.go` covers public Volume rejection and preservation; `it/it_self_subject_access_reviews_test.go` covers ephemeral review metadata. Server unit tests cover map-level updates for DiskImage and InstanceType, and DAO tests cover reserved metadata map filters. Run the Fulfillment installer target above. These tests use deployed Fulfillment, PostgreSQL, and Keycloak; they do not provision VMs, clusters, or provider-backed storage.
- **DiskImage API-only workflows ([DEV]):** `it_disk_images_api_test.go` covers public CRUD for provider admin/shared, tenant-admin, and tenant-user images; deprecation/obsolescence timestamps, default-list visibility, explicit lifecycle filtering, reactivation, template-default name resolution, and template-bound deletion protection. `it_disk_images_tenant_isolation_test.go` covers tenant visibility and access refusal. `it_disk_image_lifecycle_test.go` covers deprecated-image warnings and obsolete-image rejection without ComputeInstance persistence. `it_public_clusters_test.go` checks the selected shared ClusterVersion's missing-image `FailedPrecondition`, version identity, reason, and absence of a persisted cluster. `it_bare_metal_instance_catalog_items_test.go` rejects foreign-tenant image names with `NotFound` and no persisted catalog item.
  These component-integration cases use real public/private Fulfillment APIs, authentication, and PostgreSQL. The ComputeInstance fixture simulates network readiness and storage-provider configuration; no VM, cluster, AAP, or bare-metal provider lifecycle is exercised. Run the documented Fulfillment installer target above; against an already deployed compatible Kind environment, run `ginkgo run --timeout 20m --focus 'DiskImage API|DiskImage tenant isolation|DiskImage lifecycle|Public clusters rejects a shared ClusterVersion|Bare Metal Instance Catalog Items rejects a cross-tenant DiskImage' it` from `fulfillment-service/`.
  VM image projection/running and deletion protection through deployed workload teardown remain in `tests/e2e/vmaas/regression/test_compute_instance_disk_image.py`. BMH image selection and deprecated-image provisioning remain in `tests/e2e/bmaas/sanity/test_baremetal_instance_lifecycle.py`; DiskImage-backed CaaS provisioning remains in the CaaS E2Es.
- **VMaaS ComputeInstance network-attachment Create contract ([OSAC-5562](https://redhat.atlassian.net/browse/OSAC-5562), [OSAC-5563](https://redhat.atlassian.net/browse/OSAC-5563), DEV):** `fulfillment-service/it/it_compute_subnet_test.go` exercises the public API's one-attachment limit, no-persistence errors for missing tenant defaults and missing SecurityGroups on a non-default VirtualNetwork, and persistence of an explicitly specified attachment. `internal/servers/private_compute_instances_server_test.go` covers successful field-by-field tenant-default completion. This is Fulfillment Service and database coverage; provider provisioning remains E2E coverage.
- **Omitted network-attachment defaulting ([OSAC-6480](https://redhat.atlassian.net/browse/OSAC-6480), DEV):** `it_compute_subnet_test.go` (`uses tenant defaults when network_attachments is omitted`), `it_baremetal_instance_lifecycle_test.go` (`Creates a BareMetalInstance with omitted networking and verifies fields`), and `it_public_clusters_network_attachment_test.go` (`Uses tenant defaults when network_attachment is omitted`) create each resource through the public API with the attachment field omitted and assert Create and public Get return one resolved attachment carrying the tenant default fixture's exact Subnet and SecurityGroup IDs; the bare-metal case additionally asserts interface `data-0` from the first fabric port. Tenant default networking fixtures are ensured inside each spec with `ensureTenantDefaultNetworkingFixture`/`getTenantDefaultNetworkFixture`; fixtures simulate network readiness. Partial-attachment defaulting (supplied values survive field-level completion) is unit coverage: `internal/servers/private_baremetal_instances_server_test.go`'s `Default network_attachments population` suite proves omitted, empty-list, and partial attachments fill the missing subnet, empty security groups, and the first-fabric-port interface, and `internal/servers/private_compute_instances_server_test.go` proves partial defaults plus partial user input satisfy validation; Cluster default resolution has no dedicated partial-input unit spec and is covered by the omission case above. Run through the documented Fulfillment installer target (`SUITE=fulfillment`). This is deployed Fulfillment Service, database, and authenticated-client coverage; provider provisioning remains E2E coverage.
- **VMaaS ComputeInstance public update contracts ([OSAC-6617](https://redhat.atlassian.net/browse/OSAC-6617), DEV):** `fulfillment-service/it/it_compute_instance_network_attachment_status_test.go` extends the attachment-feedback case with a real metadata edit, asserts attachment status in the update response and a subsequent public Get, and rejects explicit `status.state` and attachment-status masks alongside their normalized spellings without persistence. `internal/servers/compute_instances_server_test.go` covers status-path rejection, omitted-mask inference, explicit-mask clearing, targeted map replacement, and a real two-transaction reproduction in which concurrent private status feedback survives a maskless public update. The servers suite uses real PostgreSQL row locks and the metering trigger, while suite tenancy and attribution are doubles; the deployed case uses real Fulfillment gRPC APIs, PostgreSQL, and test identities. Run `ginkgo run --timeout 10m internal/servers` from `fulfillment-service/` and the documented Fulfillment installer target above; against an already deployed compatible Kind environment, run `ginkgo run --timeout 20m --focus 'Compute instance network attachment status' it` from `fulfillment-service/`. Neither suite provisions a real VM; provider feedback remains E2E coverage.
- **MCP server:** `fulfillment-service/it/it_mcp_server_test.go` uses a real MCP
  SDK client and an in-process MCP handler; its exchanger forwards a caller API
  token to deployed Fulfillment and PostgreSQL. It covers five-tool discovery,
  connection summaries, tenant visibility, creator attribution, asynchronous
  create acceptance with the resource ID and version, rejected arbitrary
  service/method/payload arguments, and rejected writes without persisted
  mutations. `it_mcp_discovery_test.go` creates
  Projects and Secret references for two tenants, verifies caller-scoped
  Project and Secret-reference listing, confirms Secret-reference results
  contain only reference metadata, and checks list-only Secret Get rejection
  through the shared safe-error contract. `it_mcp_deployed_test.go` runs in the
  Fulfillment Kind CI job with `MCP_TEST_ENABLED=true` and real Keycloak; it
  checks TLS metadata and valid calls, plus rejection of the Fulfillment API
  audience, a wrong issuer, an expired token, and a wrong resource. It also
  verifies token-exchange rejection, changed tenant permissions, and retryable
  unavailability while Keycloak is stopped; cleanup restores changed client
  settings and replica counts. `internal/auth/auth_jwt_validator_test.go`
  covers expired and untrusted-issuer tokens; MCP ingress tests cover wrong
  resource audiences. `mcpserver` unit tests cover 4xx/429/5xx and transport
  exchange failures, typed registration, caller/permission mismatch, safe
  errors, and connection checks. Helm lint and rendering cover opt-in templates
  and schema; ordinary
  profile renders exercise the disabled default. Provider provisioning, VM
  readiness, and external MCP-host interoperability remain outside these tests.
- **ComputeInstance deletion feedback with stale references ([OSAC-6513](https://redhat.atlassian.net/browse/OSAC-6513), DEV):** `internal/references/reference_validator_test.go` covers status-only, spec, mixed, and empty masks at Unit tier. `it/it_compute_instance_update_test.go` holds a deleting ComputeInstance, removes its referenced user-data Secret, then checks that status feedback persists through the deployed private Fulfillment API and PostgreSQL while spec, mixed, and empty-mask updates still reject the missing reference. Run it with the Fulfillment installer target above; against an already deployed compatible Kind environment, run `ginkgo run --timeout 20m --focus 'persists deletion feedback after a referenced Secret disappears' it` from `fulfillment-service/`. The suite omits the operator, AAP, and VM provider, so it does not prove dependent network teardown.
- **ExternalIP allocation capacity ([DEV] OSAC-6307):** `internal/servers/private_external_ips_server_test.go` verifies capacity remains held while the ExternalIP has a finalizer and returns after archive. Migration 125 backfills pending deletions and checks exactly-once release in PostgreSQL. Application and database checks keep terminating consumers exclusive until archive. Provider cleanup is covered by operator tests with fixtures.
- **Canonical networking Hub routing:** [`fulfillment-service/it/it_networking_hub_placement_test.go`](../fulfillment-service/it/it_networking_hub_placement_test.go) covers VirtualNetwork, Subnet, SecurityGroup, ExternalIPPool, ExternalIP, ExternalIPAttachment, and NATGateway CR placement on the NetworkClass canonical Hub and absence on a valid alternate Hub. It also verifies that two separately provisioned network-attached Cluster resources sharing a subnet use the same Hub, repeated reconciliation does not create duplicate ClusterOrders, a cross-Hub Cluster create fails without a database row or CR, and SecurityGroup retains its stored Hub assignment when the canonical Hub changes. The fixture uses distinct Hub entries and namespaces on the service Kind cluster; it tests Hub entry and namespace routing, not isolation across separate Kubernetes clusters or real provider provisioning. Unavailable-canonical/no-fallback, ComputeInstance, and BareMetalInstance placement remain controller unit coverage because this deployed-service fixture does not isolate reconcilers between cases or exercise provider workflows.
- **SecurityGroup rule updates ([OSAC-5679](https://redhat.atlassian.net/browse/OSAC-5679), DEV):** `fulfillment-service/it/it_ipv4_networking_contract_test.go` calls the public Update API as a tenant user, verifies ingress and egress persistence while name and parent VirtualNetwork remain unchanged, and waits for Fulfillment to patch the corresponding SecurityGroup CR. This proves the public authorization and Fulfillment-to-Kubernetes API write path; operator/provider reaction to the changed CR remains outside this component suite.
- **ComputeInstance tenant-scoped SSH keys and shared Secret creation ([DEV]):** `fulfillment-service/it/it_compute_instance_ssh_key_test.go` covers named-project lookup by Secret name and ID, unnamed-project defaults, generic errors when a Secret is hidden, out of scope, missing, or in a project the user cannot access, both CLI flag forms, and a named-project CatalogItem default through the CLI. `fulfillment-service/it/it_compute_instance_catalog_items_test.go` covers the named-project REST default and policy round trip. `fulfillment-service/it/it_public_secrets_test.go` rejects a platform administrator's shared-tenant SSH Secret create and verifies that no Secret persists. Unit coverage checks canonical references, `shared=true` rejection before lookup, old stored references, CLI parsing, and server-side rejection of explicit and default-shared SSH Secret creates before persistence. It also verifies that tenant- and system-scoped SSH keys and other shared Secret types remain allowed. The project-scoped cases use the deployed Fulfillment APIs, PostgreSQL, Keycloak test identities and project grants, and the CLI built from this checkout. The shared Secret create case uses the harness's platform-admin connection. These cases do not exercise the operator, AAP, VM provider, or ComputeInstance CR projection.
- **FabricDomain API authorization, tenant filtering, and CRUD persistence:** [`fulfillment-service/it/it_fabric_domain_tenant_api_test.go`](../fulfillment-service/it/it_fabric_domain_tenant_api_test.go) runs in the component-integration suite. The deployed Fulfillment API, PostgreSQL, and Keycloak identities are real; the operator is disabled, so this test does not claim FabricDomain controller, AAP, or Netris lifecycle coverage. See [FabricDomain integration and lifecycle coverage](../fulfillment-service/it/README.fabric-domain.md).
- **ExternalIP lifecycle races:** [`fulfillment-service/it/it_external_ip_test.go`](../fulfillment-service/it/it_external_ip_test.go) races ExternalIPAttachment creation with parent ExternalIP deletion and verifies one side wins without a dangling reference. It also keeps a parent IP allocated while a dependent attachment is terminating behind a test finalizer, then releases the finalizer and retries deletion. The deferred-attachment case races Cluster readiness with Cluster deletion and verifies retries do not create a late attachment or leave auto-created IPs behind. These are deployed Fulfillment API and database checks; they do not exercise provider detach or release behavior, and they skip in `SUITE=fulfillment`, which deploys with provider networking disabled.
- **Worker `status.cluster` owner-reference attribution ([OSAC-6096](https://redhat.atlassian.net/browse/OSAC-6096), DEV):** `internal/database/migrations/125_add_owner_reference_checks_test.go` runs against PostgreSQL and covers set-once (changing or clearing the `status.cluster` owner reference or `status.node_set` is rejected), live-target (missing, deleting, cross-tenant and cross-project rejected), malformed references, and owner deletion ordering (a cluster archives only after its workers are gone; soft delete stays allowed while workers exist). `internal/database/database_schema_test.go` fails `CheckSchema` for a mapped table missing its `check_owner_reference` triggers; the registry in `database_tool.go` and the table and path mappings in the migration are extended together when a new resource records an owner reference, so a mapped table without its checks fails CI. `internal/servers/worker_attribution_test.go` covers the same contract through the real private and public servers, including the public API dropping user-supplied attribution fields and rejecting them in update masks. `internal/servers/event_publisher_test.go` publishes a worker's change and asserts the event lands on the tenant's Kafka topic carrying the cluster reference, node set and project. Deployed cross-component journeys (worker provisioning through a real cluster) remain E2E coverage; worker controller behavior is covered in the osac-operator suite.
- **Networking design ([OSAC-1433](https://redhat.atlassian.net/browse/OSAC-1433), DEV):** `fulfillment-service/it/it_networking_design_test.go` (label `networking-design`) checks the unified and default networking designs against the deployed Fulfillment private and public APIs and PostgreSQL in the service-only phase: the installer-created NetworkClass (singleton, immutable fields, `address_plan` validation and `FAILED_PRECONDITION` while a VirtualNetwork exists, delete guard, status writes and the READY gate), creation gates (NetworkClass, parent VirtualNetwork, the default Subnet of a tenant's default VirtualNetwork), canonical IPv4 CIDRs, IPv6 rejection, the address plan prefix limit, sibling overlap, pool overlap with VirtualNetworks and the NetworkClass default, pool capacity, deletion guards (children, Tenant), sticky DELETING and the second Delete, status-only private Updates, SecurityGroup per-protocol rule validation and rule-update gates (`TC-SG-ICMP-*` drive the `status.security_group_icmp` gate through the private API in the service-only phase), the provider-networking-disabled ExternalIP, ExternalIPAttachment and NATGateway rejections, `networking_provisioning_enabled` in the private capabilities, and Default Networking order and `DefaultNetworkingReady` reasons. `it_compute_subnet_test.go` (TC-ATTACH-01/02) covers attachment defaulting (`INVALID_ARGUMENT` without a default, `FAILED_PRECONDITION` while it isn't READY) and SecurityGroup selection errors. No operator runs in this phase, so the fixtures report networking status through the private API. `it_networking_operator_test.go` (label `networking-operator`, "Networking with operator") runs in the operator phase: the operator makes the installer's NetworkClass READY with reason `ProvisioningDisabled` without reading the manager registrations, VirtualNetwork, Subnet, SecurityGroup and ExternalIPPool become READY with reason `ProvisioningDisabled` and no job, ExternalIP creates fail with `FAILED_PRECONDITION`, deletes complete, and a new tenant reaches `DefaultNetworkingReady=True/ProvisioningDisabled`. Not covered here: the provider-networking-enabled path (manager tasks through AAP, `Updating`, ExternalIPs, NATGateways, automatic ExternalIPs; the specs that need it skip with a message in this deployment), successful FabricDomain creates (the dev NetworkClass doesn't enable east-west Ethernet), excluded-range rejections (the dev profile sets no `excludedIpv4Cidrs`) and the startup refusals of the installation settings (unit tests in `internal/startup`).
- **Updateless networking API contract ([OSAC-5374](https://redhat.atlassian.net/browse/OSAC-5374), DEV):** `it_updateless_networking_contract_test.go` probes the six public networking services through the tenant API for List/Get/Delete availability and `Unimplemented` Update (`InvalidArgument` for the scoped SecurityGroups rule-only Update, OSAC-5679), the ExternalIPPool private provider lifecycle (create/read/delete on a disposable pool), and the CLI's refusal to edit an immutable networking resource before any write. It exercises the deployed public/private Fulfillment APIs, authentication, and the CLI binary; no operator, AAP, or provider behavior is asserted. The NetworkClass singleton's destructive lifecycle is not exercised here (only one NetworkClass may exist per deployment and it can't be deleted while networking resources exist); it is covered by the `internal/servers` unit suite.
- **Provisioning journeys that cross into operators or providers:** Keep them in `tests/e2e/` and exercise those boundaries explicitly.
- **FabricDomain to Netris lifecycle:** The complete BMaaS E2E journey is tracked by [OSAC-4786](https://redhat.atlassian.net/browse/OSAC-4786). It requires reserved real Netris hosts and owned cleanup; API-only integration cases are not substitutes.
- **Catalog Items:** `it/` checks creation and update behavior, publication visibility, CLI creation, and the ClusterOrder release image written by Fulfillment. Catalog-backed provisioning journeys that exercise other components remain in the CaaS, VMaaS, BMaaS, and reference E2E suites.
- **ComputeInstance additional-disk defaults:** the VMaaS regression test `test_compute_instance_empty_additional_disks_disables_catalog_item_default` covers the API contract for omitted and explicitly empty disk lists through provisioning and the resulting operator resource.

## osac-installer

Touched-area requirements: [component guide](../osac-installer/AGENTS.md#integration-testing).

The `make mce-render-test` Helm contract renders the prerequisite charts from
local sources. It asserts disabled defaults, explicit standalone MCE
enablement, configuration, Assisted image overrides, compatibility RBAC,
explicit disabled-state suppression, enabled empty-override behavior, and the
CaaS profile's explicit enablement with inherited defaults. It does not install
MCE or call an Operator catalog or cluster API.

The `make -C osac-installer kind-runtime-test` unit suite is owned by installer
implementation work. It uses command stubs to check runtime selection, sudo
authentication and credential expiry, Makefile/host setup routing, native
Ginkgo report opt-in and output-path configuration for all three phases, the
operator being deployed only in the networking-operator phase, safe
credential reset, resuming stopped nodes after storage preparation and waiting
for API readiness, Helm 3/4 apply flags for devstack and OSAC releases,
image loading without virtualization prerequisites or
non-portable temporary-file patterns,
TopoLVM chart download when the locked archive is missing, reuse when present,
exclusion from general chart dependency builds,
and install failure before cluster setup when the download fails, and
device-mapper major detection in the selected engine, refusal of failed or
invalid probes, replacement of stale helper permissions or images, and cleanup
capabilities and ordering without running a container engine or
modifying host privileges. It does not exercise real password entry, TopoLVM
devices, or a deployed KubeVirt installation.
The chart dependency cases stub Helm; upstream chart downloads are not
exercised by this unit suite.

The `it-osac-installer` leg runs `scripts/it-networking-checks.py` after
`install-osac` and again after the idempotent upgrade. Against the deployed
Kind release it checks the NetworkClass step Job result and, through a
read-only copy of that Job with its own credentials, the private API NetworkClass
(`fabric_manager`, `k8s_manager`, `spec.defaults`, east-west capabilities,
`spec.address_plan` against the rendered step input and the derived plan) and
its NetworkClass CR (sixteenths, `Ready`), the ValidatingAdmissionPolicy and
binding (a direct VirtualNetwork create by an RBAC-allowed non-OSAC user and by
the cluster admin is rejected with server-side dry run; a ConfigMap create is
not), and `OSAC_PROVISIONING_ENABLED` / `--networking-provisioning-enabled` on
the operator and fulfillment Deployments as rendered from
`global.networking.provisioningEnabled`. Provider networking is disabled in this
leg, so the check also asserts that `status.security_group_icmp` stays unset;
the enabled path's AAP jobs are E2E.

The `make fulfillment-trust-render-test` Helm contract renders the production
umbrella chart with trust enabled and disabled. It asserts the operator trust
reconciler gate and checks that CA mounts and verified
curl commands remain present in both states. It checks rendered manifests only;
it does not start the hooks or prove a deployed fulfillment endpoint accepts
the certificate. The Kind
`SUITE=fulfillment` target exercises deployed startup and API behavior, subject
to the profile's configured CA and enabled services.

### Install preflight (OSAC-6561)

The `osac` chart checks prerequisites while Helm renders it (`templates/preflight/`,
`files/preflight/catalog.yaml`) and publishes the result as JSON
(`files/preflight/report.schema.json`). CI runs the unit and Helm contract tiers
as the `Unit tests (osac-installer)` leg and the Kind tier as the three
`Integration test (osac-installer preflight, Helm …)` legs (3.18.0, 3.22.0 and
4.2.2), all registered in `.github/ci-tests.yml` and run by
`.github/workflows/tests.yml`, so the
scheduled and nightly integration runs include them. None of them goes through
a Makefile target.

| Tier | Command | Real | Faked or not covered |
|---|---|---|---|
| Unit, [DEV] OSAC-6566 | `helm unittest charts/osac` from `osac-installer/` (plugin pinned in the workflow) | Helm rendering of the real chart, the engine, catalog, report and failure message | The cluster: `lookup`, capabilities and the release revision are mocked. The plugin's fake client rejects a LIST of a kind with no objects, so empty-list branches are not covered here. |
| Helm contract, [DEV] OSAC-6562, OSAC-6566 | `uv run --script scripts/test-preflight-render.py` from `osac-installer/` | Offline `helm template` of the umbrella and CSI charts; report schema validation; catalog consistency; the packaged chart archive | No cluster. Asserts the log policy on every hook Job, the CSI hook deadlines, that an offline render never blocks, that `preflight.*` values are closed by the schema, and that `helm package` ships the catalog, schema and preflight templates and no unit tests. |
| Component integration, [DEV] OSAC-6566 | `uv run --script scripts/test-preflight-kind.py` against a Kind context | Helm 3.18.0, 3.22.0 and 4.2.2, a real API server, real `lookup`, release Secrets, hooks and failed-hook logs | A preflight-only copy of the chart (no workloads); the prerequisites are stub CRDs, a Service with an EndpointSlice and Secrets; OpenShift-only checks run as `skipped: not OpenShift`. |

The Kind test covers a healthy install, an unchanged upgrade, a faulted upgrade
that leaves the deployed revision and the last passing report intact, the
install-versus-upgrade severity split, redaction of a database URL, a missing
namespace, four independent faults reported once on a fresh install, and a
failing hook that prints its pod log (with a control without the annotation).
It also covers:

- an installer that cannot read the cluster: with `validation.enabled=false`
  no cluster read happens and the install works; with validation on, Helm's
  own `forbidden` error stops the render before anything is installed;
- the first upgrade of a release made by a chart that had no preflight, with
  `--reuse-values` (the old chart is a stand-in with its own pre-install hook
  and the full defaults minus `preflight`; it is not the published chart);
- installing from the packaged chart archive;
- the report ConfigMap outliving `helm uninstall`, a reinstall next to it, and
  the removal command from the customer guide;
- `--no-hooks`, `--atomic` and `helm rollback`: the render-time gate still
  applies, writes no report without hooks, adds no rollback revision, and a
  rollback does not run it.

It refuses to run on a context that is not a Kind cluster.

Coverage gaps:

- The OpenShift-only checks (`ocp-version`, `aap-operator-ready`,
  `aap-license-secret`) run against a real OpenShift API only in the deployed
  OpenShift lanes. Owner: [OSAC-6568](https://redhat.atlassian.net/browse/OSAC-6568).
- Resource shortage and image-pull failures are not detected before the
  installation starts; they surface at `--wait` time. Later slices of
  [OSAC-6561](https://redhat.atlassian.net/browse/OSAC-6561) cover them.
- Helm 4 `--dry-run=server` (and `helm install --dry-run=server` on Helm 3)
  cannot print a report on a cluster that lacks a CRD the chart renders, even
  in report mode, because Helm maps every rendered kind first. On Helm 3 the
  `helm template --dry-run=server -s templates/preflight/report.yaml` form
  works anywhere; on Helm 4 the failing render's JSON line is the portable
  channel.

### OSAC-5343 deployed enablement coverage

The release E2E path adds these assertions to existing user journeys. The
umbrella chart defaults `global.fulfillmentTrust.enabled=true`, which enables
trust reconciliation for tenant-scoped ClusterOrders. Run release E2E only
after the compatible admission image and tenant CSI trust chart are deployed
and trust identities exist. The
standard dev and CI profiles override this feature to disabled. Set
`OSAC_FULFILLMENT_TRUST_E2E=true` only for the release suite. These tests use
real Fulfillment Service, operator, tenant Kubernetes API, CSI, and (where
enabled) Kafka/metering services; they do not use protocol test doubles. The
test harness may read the hosted-cluster kubeconfig to inspect target state;
the automatic trust path never sends it to an AAP job.

| Case | Tier and owner | Location and command | Required boundary |
|---|---|---|---|
| CaaS trust and metering | E2E, OSAC-5547 | `METERING_ADAPTER_URL=<adapter> OSAC_FULFILLMENT_TRUST_E2E=true uv run pytest -n 0 tests/e2e/caas/sanity/test_cluster_create.py` from repo root | New ClusterOrder, real tenant ConfigMap, verified management clients, event delivery. |
| Tenant CSI rollout | E2E, OSAC-5547 | `OSAC_FULFILLMENT_TRUST_E2E=true uv run pytest -n 0 tests/e2e/storage/test_caas_cluster_storage.py` from repo root | Real tenant CSI Deployment and storage provisioning. |
| VMaaS and BMaaS feedback | E2E, OSAC-5547 | `METERING_ADAPTER_URL=<adapter> OSAC_FULFILLMENT_TRUST_E2E=true uv run pytest -n 0 tests/e2e/vmaas/regression/test_compute_instance_creation.py tests/e2e/bmaas/sanity/test_baremetal_instance_lifecycle.py` from repo root | Deployed resource lifecycle and verified operator connection. |
| Installer hooks and AAP publishing | E2E, OSAC-5547 | `uv run pytest -n 0 tests/e2e/enablement/test_installer_trust.py` from repo root | Deployed Helm post-install hooks and successful AAP template publish. |
| Dev-full UI Gateway reachability | E2E, local dev-full | `OSAC_UI_URL=http://ui.osac.localhost:8080 osac-installer/scripts/test-ui-gateway.sh` from repo root | Browser-reachable `osac-ui` Deployment through the shared Gateway. |
| Overlapping-root rotation | E2E release gate, OSAC-5547 | `OSAC_TRUST_ROTATION_PHASE=overlap OSAC_TRUST_ROTATION_EXPECTED_HASH=<sha256> OSAC_TRUST_LEAF_ENDPOINT=<dns:port> OSAC_TRUST_OLD_ROOT_PEM_PATH=<file> OSAC_TRUST_NEW_ROOT_PEM_PATH=<file> uv run pytest -n 0 tests/e2e/enablement/test_ca_rotation_gate.py` from repo root; repeat with `OSAC_TRUST_ROTATION_PHASE=post-switch` and `OSAC_TRUST_ROTATION_PHASE=final` at the corresponding hash | All selected target hashes and CSI rollouts, operator and metering verified-client metrics, and leaf trust under the expected root before advancing rotation. |

The rotation gate is read-only. The operator or administrator changes the
Bundle sources and serving leaf between runs. A passing overlap run permits
the leaf switch; a passing post-switch run permits old-root removal. The final
run verifies convergence after old-root removal. The suite needs an environment
with a published tenant admission image, a compatible tenant CSI chart and
trust identities, fulfillment trust enabled, and a working external provider.
Until that environment exists, collection is execution readiness only and
does not count as a passed E2E boundary. Deployment and execution are owned by
[OSAC-5547](https://redhat.atlassian.net/browse/OSAC-5547).

## osac-operator

Touched-area requirements: [component guide](../osac-operator/AGENTS.md#integration-testing).

### Test tiers and commands

| Tier | Location / command | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | Co-located `*_test.go`; `make test` | Controller helpers, validation, provisioning state logic | External APIs and providers are mocked. |
| Envtest | Existing Controller Suite in `internal/controller/`; run by `make test` | Kubernetes API server, etcd, loaded CRDs and public in-process reconciliation | Provider/fulfillment responses and external CR progression are test-controlled; this is not a deployed worker. |
| Component integration | `test/integration/`; deploy the current operator into Kind, then `make integration-tests` | Installed operator, Kubernetes API, CRDs, controller-manager, console proxy, networking, worker service-account authorization and startup without LVMS/TopoLVM | AAP/provider provisioning and external infrastructure are not real; some existing tests remove finalizers to bypass external-provider boundaries. |
| Component integration (CI) | `make -C osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=operator` (repository root) | Thin Kind deployment used by the PR workflow | Same external-provider limitations as the local Kind suite. |
| Contract | `test/contract/`; included by `make test` | Helm chart RBAC templates against the operator permission contract | No deployed operator or external provider. |
| E2E | `../tests/e2e/` | Cross-component fulfillment journeys | Depends on the deployed environment and configured providers. |

### Coverage notes

- **Pure helpers, validation, or state calculations:** Include errors and edge cases.
- **ClusterOrder upgrade observation and deadlines ([DEV] OSAC-6265):**
  `test/integration/clusterorder_upgrade_contract_test.go` writes valid and
  invalid control-plane and node-set observation states through the installed
  ClusterOrder status subresource, and reads the three timeout variables from
  the deployed operator. It requires the current CRD and operator deployment;
  it does not exercise Fulfillment or PostgreSQL feedback.
- **Fulfillment client TLS:** `cmd/verified_fulfillment_test.go` probes local TLS
  valid/invalid CA and hostname cases, bundle rotation and last-client retention.
  The production render contract covers mounts/arguments; Kind runs with the
  trust gate disabled. Neither proves a deployed fulfillment TLS endpoint.
- **Fulfillment OAuth:** `cmd/verified_fulfillment_test.go` uses local TLS OIDC
  and gRPC endpoints to verify client-credentials token acquisition and
  authenticated calls in both connection modes. The installer render contract
  checks the Secret references and issuer ConfigMap.
- **VMaaS network-attachment IP feedback and ExternalIP DNAT ([OSAC-2479](https://redhat.atlassian.net/browse/OSAC-2479), [OSAC-2485](https://redhat.atlassian.net/browse/OSAC-2485), DEV):** `test/integration/computeinstance_network_status_test.go` writes and reads `computeNetworkAttachmentStatuses` through the installed ComputeInstance CRD status subresource in Kind. Controller unit tests cover selection of the default KubeVirt interface, Subnet CR name-to-ID resolution, Fulfillment status feedback, and waiting for/using the reported IP as the ExternalIP target. This verifies the operator API and controller contract; the Kind suite does not run a real VMI, AAP workflow, or network fabric, so it makes no claim about provider-level reachability.
- **Cross-cluster fulfillment OAuth:** No deployed suite currently covers this
  boundary. With bundled Keycloak, the dev installer provisions the existing
  `osac-controller` credentials through `osac-infra`, but the local
  `kind-osac-dev` cluster is not a multi-cluster test environment. Add a
  deployed cross-cluster exercise in a supported environment; track this gap
  under [OSAC-5939](https://redhat.atlassian.net/browse/OSAC-5939).
- **Installer schema consumption:** The installer schema exposes the operator
  credential Secret name and key references. Verify that the Enclave plugin
  and Wizard consume the updated schema before treating those controls as
  available there; that pipeline is outside this repository's chart-render
  tests.
- **Controller reconciliation, finalizers, status and CRDs:** Drive the public
  reconciler against real API persistence in the existing Controller Suite.
- **Networking controllers and the network task engine:** `internal/networktask/engine_test.go` and `result_test.go` cover launch, job tracking, retries, backoff, cancellation and typed result validation against an in-memory AAP. Envtest specs in `internal/controller/networking_resources_test.go`, `networking_attachments_test.go`, `computeinstance_networking_test.go` and `clusterorder_networking_test.go` drive the public reconcilers with the in-memory AAP of `networking_harness_test.go`: task order, `status.jobs`, `status.taskInput`, the `Ready` condition, delete order and the provisioning-disabled fallback. `networking_crd_contract_test.go` checks the CRD CEL rules; `networking_feedback_state_test.go` and `workload_networking_feedback_test.go` the feedback mapping. No real AAP job, manager role or fabric runs; that boundary is E2E under [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843).
- **Operator and AAP wire contract (OSAC-6766):** `tests/contract/network-task/` holds golden JSON read by both sides: `inputs/<task>.<manager>.json` is the `osac_job_vars` the operator launches (VirtualNetwork with `network_type`, Subnet with the `network_segment` launch key and `virtual_network.status.l3Vni`, ExternalIPPool, ExternalIP, ExternalIPAttachment, NetworkAttachment on both managers, NATGateway with `nat_gateway_context`) and `results/<task>.<manager>.json` is the job artifacts a manager returns (`l3_vni`, `network_segment.l2_vni`, `external_ip.address`, `vm_attachment`). Operator side: the envtest spec `networking_wire_golden_test.go` ("Operator and AAP wire contract goldens") drives the real reconcilers on the in-memory AAP, compares every launch with its input golden after fixing names and UIDs, and feeds each result golden through the engine and its parser; `internal/networktask/wire_golden_test.go` runs the parsers on the result goldens. Run `make test` from `osac-operator/`; `UPDATE_GOLDEN=1` rewrites the input goldens for review. The AAP half is in the osac-aap section. Shapes are bound at unit and envtest tier, not by a deployed AAP job: no real AAP, role on a cluster or Netris runs here; that stays [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843). **Not bound (no ticket):** all `delete_*` and `detach_*` tasks, `create_security_group`/`delete_security_group`, `query_dhcp_lease` and its `dhcp_lease` result, FabricDomain and cluster-ingress tasks, and physical (bare-metal) interfaces; their inputs and results are covered only by each side's own fixtures.
- **NATGateway dependency snapshot ([DEV] OSAC-6202):** `natgateway_contract_test.go` and the NATGateway specs in `networking_attachments_test.go` cover the `nat_gateway_context` snapshot, tenant and identity rejection without a job, rejection of a result when a dependency changed during the job, the failed-delete finalizer and the input staying the same when a same-VN Subnet is created or deleted. `osac-aap` unit tests cover VN-wide SNAT, the saved transit exemption, ExternalIP consumer exclusivity and cleanup; the `agentless_net_nat_gateway` target runs both tasks through `playbook_osac_network_task.yml` with provider commands replaced by fixtures. AAP, SSH, BGP convergence and packet delivery are not exercised.
- **CSP-managed ComputeInstance storage readiness and tier filtering ([OSAC-6596](https://redhat.atlassian.net/browse/OSAC-6596), DEV):** Covered by `internal/controller/computeinstance_controller_test.go` and `internal/controller/storage_controller_test.go` in `make test` (Envtest with mocked Fulfillment gRPC and the in-memory AAP). Specs drive public `ComputeInstanceReconciler.Reconcile` through initial and day-2 provisioning triggers: requested tiers already mapped in `Tenant.status.storageClasses` (with `StorageReady` unset or `True`) provision without any storage tier catalog, backend, or credential lookup; an unmapped tier, or any tier while `StorageReady=False`, resolves the catalog and stays gated on a catalog or credential failure or on CSP readiness; a tenant without storage status gates on a catalog failure unless the target cluster already has a StorageClass for every requested tier; a CSP-managed backend of any provider, including VAST with supplied credentials, never has its credentials resolved or forwarded and provisions with the supplied StorageClass; and a day-2 job on a running VM is gated on `ConfigurationApplied` without overwriting the VM's `Provisioned` or `Ready` status. Tables of `checkStorageReadiness` and `storageClassesCoverRequestedTiers` decisions cover the tenant storage states. Deployed AAP jobs and KubeVirt VM creation remain E2E coverage.
- **LVMS Volume lifecycle:** `lvms_vendor_provisioner_envtest_test.go` covers
  RWO/RWOP provisioning, generated names, persisted UID resumes, terminating
  resource replacement and deletion. Stale parent snapshots and conflicts in
  `volume_controller_test.go` check authoritative identity preservation.
  Kubernetes/etcd are real; TopoLVM is a minimal CRD with fixture status.
  LVMD/default-device-class provisioning and CSI mounting remain real-provider
  E2E under [OSAC-3711](https://redhat.atlassian.net/browse/OSAC-3711).
- **Controller deployment, watches, RBAC, console proxy, networking and Helm:**
  Unit/Envtest alone cannot prove deployed wiring. The existing Kind suite's
  `baremetalworker_test.go` queries Kubernetes authorization for the installed
  service account's ClusterOrder/status, InfraEnv, Agent, NodePool and Secret
  permissions. This is RBAC coverage, not worker watch delivery or host allocation.
- **Networking on the deployed operator (OSAC-1433, DEV, Kind):** the operator leg
  deploys provider networking disabled (`OSAC_PROVISIONING_ENABLED=false`) without
  the fulfillment service. `networking_crd_validation_test.go` checks the CEL and
  schema rules of the installed NetworkClass, NetworkAttachment and every networking
  CRD (immutability, physical interface name and MAC, ComputeInstance with overlay,
  MAC format, ports per protocol, address-plan sum, FabricDomain servers).
  `networking_networkclass_test.go` (TC-REG-01) resolves the
  chart-rendered and profile-provided manager registrations, also checks, in the leg's
  provisioning-enabled phase (label `networkclass`), the derived
  `status.securityGroupIcmp` for the chart-rendered managers, and sets `Failed`
  with the exact message for missing, duplicate, mutable and invalid registrations,
  re-resolves on registration changes and checks the registrations are immutable.
  `networking_disabled_test.go` checks `Ready=True ProvisioningDisabled` without any
  job (VirtualNetwork, Subnet with its `status.ipam`, SecurityGroup and its rule
  update, ExternalIPPool, FabricDomain), the `Failed` ExternalIP, ExternalIPAttachment,
  NATGateway and NetworkAttachment, and the delete order with operator-released
  finalizers. `networking_admission_policy_test.go` applies the installer's
  ValidatingAdmissionPolicy (rendered by the Makefile into `OSAC_IT_ADMISSION_POLICY`)
  and checks it denies networking writes, OSAC finalizer removal and workload deletes
  to cluster-admin and an RBAC-authorized ServiceAccount, and admits the operator's.
  `networking_rbac_test.go` asks SubjectAccessReviews for the operator's networking
  permissions (NetworkAttachment, NetworkClass, the BareMetalInstance watch,
  registration ConfigMaps). The enabled path (`status.jobs`, AAP launches, results)
  stays Envtest with an in-memory AAP; a real AAP and manager roles are E2E under
  [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843).
- **AAP, network task engine, provisioning, KubeVirt and fulfillment:** A test-local
- **Remote VMaaS cluster `StorageClass` readiness ([OSAC-6576](https://redhat.atlassian.net/browse/OSAC-6576), DEV):**
  `internal/controller/storage_controller_test.go` covers dual-`envtest`
  `MultiClusterWatch` fan-out across two API servers, and
  `test/integration/remote_storage_cluster_test.go` exercises a live second Kind
  cluster (`osac-remote`) for remote `StorageClass` watches, kubeconfig Secret
  rotation, and `Forbidden` RBAC fail-closed recovery.
- **AAP, dispatcher, provisioning, KubeVirt and fulfillment:** A test-local
  dependency double does not prove the receiving service/provider boundary.
- **Generated CRDs/manifests:** Change the source and run the owning generator.

### Bare-metal worker coverage ([DEV])

Worker Unit specs use the Ginkgo BareMetalWorker Suite and descriptive behavior
names; RBAC Contract specs use the Operator Contract Suite. All worker persistence
specs share `internal/controller/suite_test.go` with the other operator tests. They are `internal/controller/baremetalworker_*_test.go`,
labelled `baremetalworker`; there is no separate acceptance suite or reusable
external-environment framework. The fulfillment/ignition doubles are private to
that test binary. Explicit CR fixtures write only the evidence the worker reads.
Retain the minimal InfraEnv/ClusterDeployment and existing Agent/NodePool test
CRDs required by these cases. None of these fixtures runs Assisted Service,
CAP-Agent, HyperShift, BMF, AAP or hardware provisioning.

| Behavior / cases | Unit location under `internal/controller/baremetalworker/` | Existing Controller Suite location under `internal/controller/` | Real boundary and limits |
|---|---|---|---|
| Reservation, identity and allocation checkpoints; R01/R02 | `nodesets_test.go`, `bmi_recovery_test.go`, `bmi_reconcile_test.go`, `worker_capacity_test.go` | `baremetalworker_reconciler_test.go`, `baremetalworker_convergence_test.go` | Real status/optimistic locking; returned IDs, lost acknowledgement, restart, delayed visibility, AlreadyExists recovery and foreign/ambiguous/deleting refusal use test-local API responses, not Postgres. |
| Definitively rejected BMI Create checkpoint ([DEV] [OSAC-6535](https://redhat.atlassian.net/browse/OSAC-6535)) | `bmi_rejection_test.go` | `baremetalworker_convergence_test.go` | Unit owns code classification, exact checkpoint/response verification and event/metric accounting, one-patch/no-post-Create-Get behavior, growing retry backoff and prior unknown/legacy-outcome safety. Envtest proves atomic status JSON Patch UID/whole-worker fencing for target mutation/removal/reordering and same-name replacement; unrelated worker/condition/count/metadata/spec preservation; deletion replay to finalizer removal; minimal persisted Failed/retry/WorkersFailed convergence. Only an invocation transitioning explicit Reserved to Attempted may re-arm its own rejected Create. No local retry or resourceVersion test. Terminal-code rollback remains a service contract, not proved by doubles; real service/DB/provider gaps remain under [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843). No new E2E or sanity expansion. |
| Agent-before-BMI cleanup, retirement and finalization; R03 | `cleanup_test.go`, `retry_test.go`, `worker_teardown_test.go`, `reservation_cleanup_test.go` | `baremetalworker_convergence_test.go`, `baremetalworker_reconciler_test.go` | Real Agent Delete UID-precondition rejection; delayed/lost BMI deletion, once-only retry, fresh destructive reads, unrecorded-ID recovery and bound-worker retention use explicit fixture completion. |
| Independent progress with blocked creation; R04 | `worker_reconcile_test.go`, `worker_capacity_test.go`, `retry_test.go` | `baremetalworker_convergence_test.go`, `baremetalworker_reconciler_test.go` | Real summaries persist before input errors; retirement/cleanup/binding proceed without pull secret, ignition or image; no Create/fetch is authorized by missing inputs. |
| One invocation-local observation and phase projection; R05 | `worker_observation_test.go`, `worker_projection_test.go`, `worker_reconcile_test.go` | `baremetalworker_convergence_test.go` | Lost status after Agent patch recovers without a second patch/Create; demotion/protected history and no pre-bind Ready from stale snapshots. Read budgets exclude fresh destructive authorization. |
| Strict shared association, selector union and UID races; R06 | `correlation_test.go`, `agent_reconcile_test.go`, `worker_projection_test.go` | `baremetalworker_reconciler_test.go`, `baremetalworker_convergence_test.go` | Real Agent UIDs and shared-selector deduplication; ambiguous/foreign/malformed evidence authorizes no patch/delete/readiness; restart reconstructs durable binding. No Assisted Service controller. |
| InfraEnv resource evidence, ownership and stale-artifact repair; R07 | `worker_reconcile_test.go` | `baremetalworker_reconciler_test.go`, `baremetalworker_convergence_test.go` | Real owner UIDs/status; ready condition is output, foreign objects are errors, stale failure persists before replacement UID recording; ignition uses a local HTTP endpoint. |
| Stateless per-call availability and order isolation; R08 | `fulfillment_test.go`, `fulfillment_error_test.go` | `baremetalworker_convergence_test.go` | Real order-scoped unavailable conditions and recovery; injected gRPC codes preserve sentinel/original code and cancellation semantics. No real backend outage. |
| Per-attempt origin and continuous Ready interval; R09 | `agent_reconcile_test.go`, `retry_test.go`, `worker_projection_test.go`, `worker_capacity_test.go` | `baremetalworker_convergence_test.go`, `baremetalworker_reconciler_test.go` | Real optional timestamp persistence, lost-ack restart, one-time backfill and demotion/re-entry; fixed-clock boundaries, not provider timing. Older controllers retain parent-age behavior; downgrade safety is not guaranteed. |
| Intent-derived desired/current/ready and metrics; R10 | `worker_reconcile_test.go`, `metrics_test.go`; parent cases in `../clusterorder_controller_test.go` | `baremetalworker_convergence_test.go` | Real counts, failed/unavailable conditions and per-NodeSet retention. Unit metrics collect exact two-type desired/zero-ready series before reservations. No deployed metrics HTTP guarantee. |
| Tenant/owner isolation and CAP-Agent handoff | `scaling_tenant_safety_test.go`, `agent_handoff_test.go`, `correlation_test.go` | `baremetalworker_tenant_safety_test.go`, `baremetalworker_reconciler_test.go` | Real tenant annotations and exact HostedCluster-derived binding; owner-driven detachment fixture precedes deletion. No deployed CAP-Agent/drain behavior. |

Focused commands from `osac-operator/` (included by `make test`):

```bash
go test ./internal/controller/baremetalworker -count=1
go test -race ./internal/controller/baremetalworker -count=1
KUBEBUILDER_ASSETS="$PWD/bin/k8s/1.31.0-linux-amd64" \
  go test ./internal/controller -count=1 -ginkgo.label-filter=baremetalworker
```

The focused commands use existing Kubernetes 1.31.0 binaries; `make test`
obtains platform-appropriate assets through setup-envtest. Public reconciliation
traces use explicit calls, including separate finalizer/repair/reservation,
single-Create, phase persistence and Agent/BMI absence checkpoints. Legacy
fixture convergence is bounded by `16 + 8*N` with deliberately ready dependencies;
errors and dependency/backoff results propagate unchanged. This is a fixture
termination bound, not a production latency SLA. Retry deadlines advance in the
test instead of sleeping, and protected workers cannot be resurrected by an
Installed Agent. Fulfillment dependency state remains isolated per test.

Reservation cleanup specs in `baremetalworker_convergence_test.go` exercise early
deletion after reservation persistence, both sides of the real optimistic Create-intent race,
restart after intent persistence before the external call, legacy ID-less
retention, and lost acknowledgement cleanup with delayed List visibility.
`reservation_cleanup_test.go` adds Unit checks for the same safety boundaries
and resetting create state only after confirmed old-incarnation cleanup.
Only explicit `Reserved` status is cancellable without provider evidence;
`Attempted` and omitted legacy state stay conservative. Interruption after
intent persistence before Create remains ambiguous and may block deletion:
there is no provider-side atomic resolve/cancel protocol in this change.
Mixed-version execution with an older controller that ignores create state is
not safe; deploy the generated CRD and drain older worker controllers before
using reservation cancellation. The deployed early-delete E2E remains unchanged.

### Networking cleanup lifecycle coverage (NET-CLEAN-07)

The Fulfillment component integration suite installs the current controller
manager and exercises networking cleanup through the Kubernetes API. It deletes
ClusterOrder, ComputeInstance, and BareMetalInstance resources and verifies that
the current and legacy ownership markers lead to the expected networking child
cleanup. The cases verify ExternalIPAttachment deletion before ExternalIP,
workload finalizer retention while test finalizers hold children, preservation
of manual or foreign resources, and BareMetalFulfillment projection of its
finalizer before networking children are created. The BareMetalInstance case
restarts the installed manager while attachment cleanup is held by a test
finalizer, then confirms cleanup resumes for the same attachment after the
finalizer is released. Test finalizers simulate provider cleanup; they do not
exercise a real provider. Additional cases persist ExternalIP Create and
ExternalIPAttachment detach launch claims in the real API, delete those
resources, restart the installed manager, and verify deletion remains held,
Create cannot move the ExternalIP back to Ready, and no job ID is recorded
while the launch claim remains active. Controller tests separately assert the
exact provider invocation count;
the Kind suite does not deploy AAP.

Run the suite in its own Kind cluster when testing in parallel. Use a local-only
Kind config with unique HTTP and HTTPS host ports, and set the HTTPS port to the
same value as `FULFILLMENT_IT_HTTPS_PORT`. Do not check environment-specific Kind
configs into the repository. For example:

```bash
export KIND_CLUSTER_NAME=osac-5749-operator
export KIND_CONFIG="$HOME/.config/kind/osac-5749-operator.yaml" # local config maps HTTP 38080 and HTTPS 38443
export FULFILLMENT_IT_HTTPS_PORT=38443

KUBECONFIG= make -C osac-installer install-infra PLATFORM=kind PROFILE=dev NS=osac
KUBECONFIG= make -C osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=operator
KUBECONFIG= make -C osac-installer uninstall PLATFORM=kind PROFILE=dev NS=osac
```

Run NET-CLEAN-08 in a separate cluster with its own local Kind config,
kubeconfig, and host ports when validating both suites in parallel. Deployed QE
coverage is tracked under
[OSAC-5750](https://redhat.atlassian.net/browse/OSAC-5750); real provider
cleanup coverage is tracked under
[OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843).

### Coverage gaps and ownership

Real private-API fixture validation, fulfillment-generated tenant-owned
ClusterOrders, Postgres scoped-name uniqueness and lost-ack recovery, actual
fulfillment deletion/name reuse, backend outage/authorization, deployed metrics
HTTP and manager Agent-watch delivery require qualifying **[DEV] Contract or
component integration** coverage under
[OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843). Neither the controller
fixture's uniqueness model nor installed RBAC asserts those boundaries. The
current Kind suite has no worker contract fixture exercising them; do not count
Unit/Envtest, compile checks or chart renders as substitutes. Optional same-UID
Agent binding-change Delete resourceVersion coverage (R03-E6) remains proposed,
not a completion gate.

Real Assisted Service artifacts/selectors/binding, AAP/HyperShift/CAP-Agent,
provider/drain and hardware create/scale/delete journeys remain **[QE] E2E**
under the same follow-up. Production archived-Cluster ownership lookup still
needs an approved fix and a dedicated owner/ticket (unresolved). Bound-worker
cleanup waits for owner-driven detachment; no Machine/CAP-Agent hooks or
NodePool replicas are manipulated to force cleanup. These gaps do not require
a new environment for local status-projection or cleanup-policy changes.

## bare-metal-fulfillment-operator

Touched-area requirements: [component guide](../bare-metal-fulfillment-operator/AGENTS.md#integration-testing).

### Test tiers and commands

| Tier | Location / command | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | Co-located `*_test.go`; `make test` | Allocation, lifecycle, client, and provider logic in isolation | Kubernetes and external provider APIs are mocked or intercepted. |
| Envtest | Controller tests under `internal/controller/*_envtest_test.go`; run by `make test` | Kubernetes API server, etcd, OSAC CRDs, and static Metal3 CRDs | Metal3 controller, Ironic/BMC, hardware, and some provider clients are faked. |
| Component integration | `test/integration/`; deploy the current operator into a Kind cluster, then run `make integration-tests` | Deployed operator behavior, CRDs, Kubernetes API, pool/instance flows, and status transitions | The suite creates static `BareMetalHost` state and simulates provider transitions; it does not run a real Metal3 operator, Ironic, BMC, or hardware. |
| Component integration (CI) | `make -C osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=bmf` (from repository root) | The thin Kind deployment used by the PR workflow | Same static Metal3/provider boundary as the local suite. |
| Contract | No dedicated contract suite; follow [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) for qualifying provider-contract coverage | No real external provider boundary is exercised by the current suite | Static Metal3 CRDs, patched status, and simulated provider transitions do not exercise a real Metal3/Ironic/BMC contract. |
| E2E | Cross-component OSAC E2E suites | Fulfillment-to-operator user journeys where the environment provides them | Real hardware and provider availability remain environment-dependent. |

### Coverage notes

- **Pure inventory, selection, validation, or client logic:** Cover success, no-match, and provider-error paths.
- **Reconciliation, finalizers, allocation, or status transitions:** Use the public reconciler behavior and the appropriate CRD fixtures.
- **Controller deployment, CRDs, pool flows, or Kubernetes wiring:** Envtest alone does not prove the deployed controller path.
- **Metal3, BCM, Ironic, BMC, power, or hardware semantics:** Static CRDs and HTTP test doubles do not satisfy a real-boundary requirement.
- **Generated CRDs or Helm CRDs:** Keep generated artifacts synchronized.

### Coverage gaps

The current Kind suite deliberately stops at static Metal3 resources and
simulated provider status. Work that changes the real Metal3/Ironic/BCM/BMC
boundary must add the qualifying coverage under [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) or its contract-test
follow-up; extending the existing static-fixture suite alone is insufficient.

## osac-aap

Touched-area requirements: [component guide](../osac-aap/AGENTS.md#integration-testing).

### Test tiers and commands

| Tier | Location / command | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | `tests/unit/`; `uv run pytest tests/unit` | Filter and isolated plugin behavior | Kubernetes, AAP, cloud, and storage services are mocked or fixture-driven. |
| Unit / local HTTP transport | `tests/unit/test_netris_external_ip_transport.py`; `uv run pytest tests/unit/test_netris_external_ip_transport.py` | Real Ansible HTTP transport and module subprocesses: authenticated GET/POST/DELETE, allocate/release, sanitized 401 errors, and Cookie isolation for same-origin and same-host cross-port redirects, with and without response `Set-Cookie` headers (session and unrelated cookies). The loopback provider (`tests/unit/netris_session_fixture.py`) rejects missing/incorrect sessions. | Synthetic loopback provider only; no Kind, deployed AAP, provider login, or real Netris. Both supplied and response-derived cookies are excluded on redirects; response-cookie refresh is not accepted by this client. POST/DELETE 307/308 remain rejected under the default urllib policy; explicit `all` tests also check method/body/Content-Type preservation. Live provider compatibility and cookie-refresh requirements remain unverified. |
| Unit | `tests/unit/plugins/lookup/test_fulfillment_token.py`; included by `uv run pytest tests/unit` | OIDC discovery validation, Keycloak client-credentials exchange, token caching/refresh, and sanitized failures | Keycloak and Fulfillment are mocked; this does not prove deployed credentials or cross-cluster reachability. |
| Unit | `tests/unit/test_publish_templates_auth_config.py`; included by `uv run pytest tests/unit` | Parsed publisher pod spec, Kubernetes service-account projection, and OAuth Secret/ConfigMap references | No Kubernetes objects are created and no AAP API or external issuer is contacted. |
| Unit / isolated role transform ([DEV]) | `uv run --group development ansible-playbook collections/ansible_collections/osac/service/roles/hosted_cluster/tests/test.yml` | Executable NodePool definition transforms: distinct NodeSet names and selectors for the same hardware profile, independent replica counts and scale-up | No Kubernetes resources are created. This is not component-integration or deployed AAP/provider coverage; those gaps remain owned by [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843). |
| Component integration | `tests/integration/`; `make test` (creates Kind, runs playbooks, and tears it down) | Ansible roles/playbooks against real Kind APIs, including a second isolated API for storage target routing, plus CRDs, leases, finalizers, and test-runner pod | AAP, OpenStack, KubeVirt/RHACM, and other provider APIs are not generally real; the VMS storage target uses a mock server. |
| Unit / isolated role transform (OSAC-6182) | `collections/ansible_collections/osac/templates/roles/ocp_virt_vm/tests/test.yml`; Tests 15–17 run in separate Ansible processes via `make test` and its existing CI job, selected by `test_windows_userdata_sysprep`, `test_windows_no_userdata`, and `test_linux_userdata_cloudinit` | Windows user-data extraction and pre-secrets base spec, no-user-data Windows routing, and Linux cloud-init/virtio routing without sysprep | No Kubernetes Secret operations or Windows guest boot. Linux cloud-init wiring is an offline patch replay; the Kind scenario below owns real sysprep Secret copying and wiring. |
| Unit + Component integration ([DEV] OSAC-6596) | `collections/ansible_collections/osac/service/roles/tenant_storage_class/tests/test.yml` and `tests/integration/targets/compute_instance_create/tasks/csp_storage_tiers.yml` in `make test` | Ansible tier resolution, deduplication, fail-closed pre-finalizer rejection (including whitespace-only StorageClass names), and Kind `DataVolume.spec.pvc.storageClassName` creation | No deployed AAP server or live KubeVirt VM boot ([OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843)). |
| Component integration ([DEV] OSAC-6182) | `tests/integration/targets/compute_instance_create/tasks/sysprep_secret_copy.yml`; included by `make test` | Compute-instance create workflow and real `create_secrets.yaml` tasks against Kind: rejects missing tenant annotations, copies `userdata` unchanged to `Unattend.xml`, and verifies tenant annotation, VM label, and SATA CD-ROM/Secret references in the resulting in-memory VM spec | VM/DataVolume creation and readiness are overridden; no Windows guest boot, deployed AAP/operator, or real-provider lifecycle is exercised. Offline role Test 15 covers only extraction and the pre-secrets base spec. |
| Unit / isolated role ([DEV] OSAC-6682) | `tests/unit/test_tenant_storage_class.py`; included by `uv run pytest tests/unit` | Real Ansible tier selector: exact selection, missing/empty maps, rejection of duplicate requested-tier mappings, unrelated-tier duplicates, and trimmed StorageClass names (whitespace-only names never resolve; padded copies of one name are not ambiguous) | No Kubernetes or provider operations. |
| Component integration ([DEV] OSAC-6682) | `tests/integration/targets/compute_instance_create/tasks/storage_tier_mapping.yml`; included by `make test`; [focused commands and prerequisites](../osac-aap/tests/integration/targets/compute_instance_create/README.md) | Real top-level ComputeInstance playbook, tier selector, Kind StorageClasses and persisted DataVolume specs; boot/additional disk selection, missing/ambiguous map rejection before disk creation, retry identity, unchanged shared classes, and mixed supplied classes with real LVMS JIT class creation | Input is supplied directly, so operator CSP filtering and exclusion of backend credentials remain [OSAC-6596](https://redhat.atlassian.net/browse/OSAC-6596). Fixed synthetic Volume IDs are prerequisite inputs, without corresponding Volume records or propagation assertions. Volume-ID coverage under [OSAC-6682](https://redhat.atlassian.net/browse/OSAC-6682) is deferred to the post-preview Volume API workflow; this target has no dependency on PR #1907. No AAP controller, operator, CDI/KubeVirt reconciliation, Trident, PVC binding, mount or guest I/O. The isolated setup uses minimal VM/DataVolume schemas, not provider admission validation. |
| Component integration (OSAC-1433) | `tests/integration/targets/network_task_playbook/tasks/baseline.yml`; from `tests/integration/` run `uv run ansible-playbook targets/network_task_playbook/tasks/baseline.yml -e '@common_vars.yml'`; also registered in `make test` | The generic playbook contract: the fixed Fabric (21) and Kubernetes (8) task sets, a task of the set run through a real manager role (the Netris SecurityGroup no-op) with no typed output, invalid task/manager/input rejection that runs no manager task, launch extra-var validation, and retryable errors for missing roles, collections or task files. Running every task of each set is covered by `netris_fabric` (Fabric) and `cudn_evpn` (Kubernetes) | No Kubernetes, AAP or backend; each case is a child `ansible-playbook -e @extravars` process, as AAP launches a job. |
| Component integration (OSAC-6766) | `tests/integration/targets/netris_fabric/tasks/baseline.yml`; from `tests/integration/` run `uv run ansible-playbook targets/netris_fabric/tasks/baseline.yml -e '@common_vars.yml'`; also registered in `make test` (creates no cluster objects) | The Netris Fabric Manager (`osac.templates.netris`) through `playbook_osac_network_task.yml`: all 21 Fabric tasks, VirtualNetwork and Subnet results (`backend_id`, `l3_vni`, evpn `network_segment.l2_vni`) and reruns, V-Net gateway and DHCP, `l3Vni` mismatch, physical attachment port move and DHCP lease, ExternalIP/NAT names and addresses, FabricDomain, ordered and absent-tolerant deletes with the VNI freed last, 5xx versus 4xx classification and sanitised messages, tenant isolation, drift, fail-closed owner proof, partial-create and lost-response recovery | Netris is a stateful in-process fake (`tests/integration/fixtures/netris/fake_netris.py`) and the hub kubeconfig is unusable; no AAP, operator, switches or live Netris. Gap: the real Netris controller and the deployed journey stay the lab E2E (QE); the live ExternalIP path remains [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843). |
| Component integration (OSAC-6766) | `tests/integration/targets/cudn_evpn/tasks/baseline.yml`; from `tests/integration/` run `uv run ansible-playbook targets/cudn_evpn/tasks/baseline.yml -e '@common_vars.yml'`; also registered in `make test` | The eight `cudn_evpn` Kubernetes Manager tasks through `playbook_osac_network_task.yml`: namespace, CUDN EVPN spec, NetworkPolicy and attachment results, ownership refusals, VNI validation, idempotent reruns and absent-tolerant deletes against a real Kubernetes API, plus every state of a hub object (a VTEP or CUDN that never gets ready, objects held in deletion, drift, objects of someone else) and API errors and dropped connections | The ClusterUserDefinedNetwork and VTEP CRDs are fakes (accept-anything schema, no status subresource); the play sets the VTEP and CUDN conditions, not OVN-Kubernetes. API faults come from `fixtures/kube/fault_proxy.py`, which forwards everything else to Kind unchanged; timeouts run `files/short_waits.yml`, the generic playbook with the role's waits cut to seconds. No AAP. Not covered: OVN-Kubernetes behavior, real CRD validation, EVPN/DHCP data plane. The target refuses to run if those CRDs already exist. |
| Component contract (OSAC-1433) | `tests/integration/targets/config_as_code_job_templates/tasks/baseline.yml`; also registered in `make test` (no Kind needed) | Rendered config-as-code `controller_templates`: networking templates run the generic playbook in the networking-operations instance group with `ask_labels_on_launch` and timeout 3600 and the launch-policy guard | No AAP controller; the templates are not created. |
| Component contract (OSAC-6766) | `tests/integration/targets/config_as_code_pod_specs/tasks/baseline.yml`; also registered in `make test` (no Kind needed); with `tests/unit/test_config_as_code_cluster_netris_settings.py` and `tests/unit/test_publish_templates_auth_config.py` | Rendered container-group pod specs: cluster fulfillment keeps the CaaS Netris settings (controller URL, certificate validation, CA ConfigMap mount and `NETRIS_CA_PATH`, `protected_linear` strategy, optional `netris-credentials` Secret, AgentlessNet inventory); networking operations carry none of them; the config-as-code Secret takes the three settings from `instanceGroups.networkFulfillment.config` | No AAP controller or Kubernetes; the container groups are not created and no Netris controller is contacted. |
| Component integration (OSAC-1435) | `compute_instance_create`/`compute_instance_delete` (baseline and overrides), `compute_instance_with_gpu_create`, `compute_instance_create:sysprep_secret_copy` in `make test` | `osac_job_vars.vm_network` as the operator sends it: target namespace, VM template `networks`/`interfaces` taken unchanged (enabled-mode `l2bridge` and disabled-mode default pod network with masquerade), the `osac.openshift.io/compute-instance-id` VM label, and a delete without `vm_network` failing before any resource is touched | VM/DataVolume creation is overridden; no KubeVirt VM runs on Kind. |
| Component integration (focused) | A target under `tests/integration/targets/`; run the corresponding playbook from `tests/integration/` | The specific role workflow and its documented fixtures | Only the dependencies declared by that target; inspect its setup and overrides before claiming a real boundary. |
| Contract | No dedicated contract suite; use the qualifying [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) task for AAP/provider coverage | No AAP or provider endpoint is exercised as a contract | The Kind API, mock VMS server, and fixture-driven provider behavior do not prove an AAP or provider contract. |
| E2E | Cross-component OSAC E2E suites | Complete fulfillment and provisioning flows | Depends on the deployed AAP and provider environment. |

### Build/package validation

Run `make execution-environment-build` when the execution-environment definition
or dependencies change. This validates image assembly and packaging; run the
applicable integration tests separately to validate workflow behavior.

### Coverage notes

- **Filters, variable transforms, and isolated plugin logic:** Include invalid input and default handling.
- **AgentlessNet VirtualNetwork/Subnet state and command helpers:** Unit coverage proves the current v1 schema initialization, additive recognition of the earlier v1 consumer-table layouts, fail-closed rejection of incompatible v1/v2 databases, internal allocation and exhaustion, exclusion parsing for bridge memberships and NVUE reserved ranges, parent-lock serialization, retry retention, VLAN interfaces, derived DHCP-manager selection, DHCP rendering/service lifecycle, lease preservation, and diagnostics with mocked provider commands. OSAC-6306 pool unit cases prove immutable identity and idempotent identity-checked deletion. OSAC-6308 attachment unit cases prove the schema-v1 attachment row, identity/retry/target-replacement behavior, dependency deletion guards, and owner-scoped DNAT, forwarding, route, and conntrack cleanup commands. OSAC-6202 NATGateway unit cases prove VN-wide SNAT, transit exemption/order, constant rule count, zero-Subnet readiness, Subnet independence, cleanup retention, explicit SNAT and UID-scoped forwarding/bypass/route cleanup. The existing ExternalIPPool operator controller test verifies pool deprovision waits until child ExternalIP CRs are removed. The operator envtest verifies Subnet parent-context construction and NATGateway desired-configuration stability; the Kind pool target verifies the ExternalIPPool CR's Kubernetes UID, Fulfillment label, tenant, family, and CIDR reach saved state through the existing playbooks. AAP/Kind service-readiness checks do not prove client leases or traffic delivery.
- **Ansible roles, workflow tasks, hooks, leases, finalizers, or Kubernetes resources:** The test must exercise the role/playbook through Ansible against Kind.
- **Netris tenant networking idempotency (OSAC-4923, [DEV]):** Unit tests cover the idempotency filters and response normalization; the `netris_fabric` target covers get-or-create reruns, immutable drift, detail reads, partial-create and lost-response recovery through the generic playbook.
  The fixture proves caller-side playbook behavior; it does not confirm response contracts against a live Netris controller. See `osac-aap/docs/netris-idempotency-inventory.md` for the operation matrix. SecurityGroup stale ACL pruning remains OSAC-4888; broader live-provider coverage remains under OSAC-4843.
- **Netris VirtualNetwork, Subnet and SecurityGroup tasks:** `osac-aap/tests/unit/test_netris_vn_subnet.py` and `test_netris_playbook.py` run the real task files through `playbook_osac_network_task.yml` in child Ansible processes against a stateful in-memory Netris double. They prove contract inputs and results (`backend_id`, `l3_vni`, evpn `network_segment.l2_vni`), final versus retryable errors, get-or-create reruns without writes, V-Net gateway/DHCP planning and update, and absent-object deletes. The double is not a live Netris controller ([OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843)); the deployed journey is the lab E2E.
- **Netris NetworkAttachment tasks:** `osac-aap/tests/unit/test_netris_attachment.py` runs the real task files through the generic networking playbook against the in-memory Netris double. It covers the overlay no-op, the physical port move (EVPN Subnets get DHCP over the physical range before the move), `query_dhcp_lease`, ownership refusals, and absent-object deletes. It does not cover a live Netris, switches, or the bare-metal fulfillment operator wiring; deployed journeys belong to the lab E2E.
- **Netris FabricDomain and cluster ingress tasks, and the VirtualNetwork task without NAT:** `osac-aap/tests/unit/test_netris_fabric_domain.py` runs `create_fabric_domain`, `delete_fabric_domain`, `reserve_cluster_ingress_ip`, `release_cluster_ingress_ip` and `create_virtual_network` (which creates no NAT rule and ignores leftover Netris DNS settings) through `playbook_osac_network_task.yml` against the same in-memory Netris double. They prove the Server Cluster template and membership, the refusal of a cluster in another VPC, the absence of any NAT request from `create_virtual_network`, the final reserve error, and absent-object deletes. The double is not a live Netris controller ([OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843)); the deployed journey is the lab E2E.
- **MetalLB ExternalIP detach annotation cleanup:** The `metallb_l2_detach_annotation` target starts two independent Ansible processes after both have read the same annotation snapshot. It verifies that concurrent detach cleanup succeeds when one process removes the annotation before the other patches it, against a real Kind API. It does not exercise a deployed AAP controller or MetalLB provider.
- **Netris parent-network deletion:** The `netris_fabric` target covers VirtualNetwork, Subnet and ExternalIPPool deletes: absent-tolerant, ordered (the VNI is freed last), 404 tolerated, 400 final, 5xx retryable, and recovery after a lost response ([OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) for a live Netris).
- **Netris ExternalIPPool, ExternalIP, ExternalIPAttachment and NATGateway tasks:** `osac-aap/tests/unit/test_netris_eip_nat.py` runs the real task files through `playbook_osac_network_task.yml` in child Ansible processes against the stateful in-memory Netris double, including the `osac.templates.netris_external_ip` module. They prove contract inputs and the `external_ip.address` result, API-ID-owned /32 reservations (`osac-eip-<ExternalIP id>`), DNAT/SNAT rules in the destination or VirtualNetwork VPC, final versus retryable errors (full pool, objects of another tenant or VPC), get-or-create reruns without writes, absent-object deletes, and zero Kubernetes access. The double is not a live Netris controller ([OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843)); the deployed journey is the lab E2E.
- **Task and Python coverage of the network manager roles (OSAC-6766):** `make test` in `osac-aap/` measures `roles/netris`, `roles/cudn_evpn` and their four plugins and fails on a gap. The Ansible callback `tests/coverage/callback_plugins/osac_task_coverage.py` is enabled through the environment, so the child `ansible-playbook` processes of the targets and of the unit tests inherit it; it records the outcome of every role task, per process, in `tests/coverage/.runs/`. `tests/coverage/role_coverage.py` takes its denominators from the role YAML and reports every task never run, every `when` condition (one per list item) seen on one side only, every `rescue` never entered, and every include of `_fail.yaml` or `_stop.yaml`, `ansible.builtin.fail` and `assert` that never fired or whose `osac_error` no test asserted. A fail path counts as asserted only when a test recorded the classification and a checked message of a run in which it fired; `network_task_playbook/tasks/parse_job.yaml` records that for the targets, so a fail path needs a target case with `sft_message` or `sft_message_search`. The plugins must reach full line and branch coverage with pytest-cov (`tests/coverage/coveragerc`). A `when` that holds several conditions joined by `or` or `and` is measured as one condition. Dead code stays in `tests/coverage/exclusions.yaml`, one line with its proof each; a stale entry fails the check. `make role-coverage` runs only what measures these roles, on a Kind cluster of its own. The `netris.controller` roles these tasks call are reported, not gated.
- **Operator and AAP wire contract (OSAC-6766):** `osac-aap/tests/unit/test_network_task_wire_contract.py` runs the shared input goldens in `tests/contract/network-task/inputs` unchanged through the generic playbook against the in-memory Netris double (VirtualNetwork, Subnet, ExternalIPPool, ExternalIP, attachment, ExternalIPAttachment, NATGateway) and compares the returned `osac_result` with `results/`; it runs the Kubernetes goldens through the `cudn_evpn` planner (the L3 and L2 VNIs of the result goldens must reach the CUDN `ipVRF` and `macVRF`, and the apply result equals its golden) and validates the `nat_gateway_context` golden with the `agentless_net` state module. Run `uv run --all-groups python -m pytest tests/unit/test_network_task_wire_contract.py` from `osac-aap/`. The operator half is in the osac-operator section. The Kubernetes objects and the agentless_net provider commands are not run here (see the `cudn_evpn` and `agentless_net_nat_gateway` targets); no AAP server or live Netris. Kubernetes inputs are checked at `cudn_evpn` planner level, not by running the role. **Not bound (no ticket):** all `delete_*` and `detach_*` tasks, `create_security_group`/`delete_security_group`, `query_dhcp_lease` and its `dhcp_lease` result, FabricDomain and cluster-ingress tasks, and physical (bare-metal) interfaces; their inputs and results are covered only by each side's own fixtures.
- **Template publishing TLS:** The `test_cert_validation` play in `collections/ansible_collections/osac/service/roles/publish_templates/tests/test.yml` runs the real role against an untrusted local HTTPS endpoint and asserts certificate rejection before any authenticated HTTP request. The endpoint is a test double; it does not prove a deployed AAP or fulfillment boundary.
- **Template publishing OAuth:** Unit and local mock-server coverage belong to DEV [OSAC-6084](https://redhat.atlassian.net/browse/OSAC-6084). The token lookup tests validate OIDC discovery, client-credentials exchange, refresh, and sanitized errors; the role fixture verifies Bearer authentication for each published resource type. Neither test proves deployed AAP secret delivery or cross-cluster Keycloak/Fulfillment reachability; QE coverage remains tracked by [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843).
- **Execution-environment definition or dependency inputs:** Image success does not prove the workflow boundary.
- **AAP, OpenStack, KubeVirt/RHACM, or provider provisioning:** Kind-only tests with mocks cannot claim provider coverage.
- **Storage-provider behavior:** The mock VMS server validates role logic, not the provider API.
- **Split-cluster Tenant StorageClass routing:** The storage target-routing integration test exercises the Tenant create/delete playbooks and compute-instance JIT provisioning against separate management and workload Kind APIs. Compute coverage includes unset/empty remote configuration, workload placement, a misplaced management StorageClass, repeated provisioning, and preservation of the supplied kubeconfig. It does not verify Tenant status resolution, VM creation, or a deployed AAP/provider lifecycle; that cross-component journey remains QE coverage tracked by [OSAC-4850](https://redhat.atlassian.net/browse/OSAC-4850).

### Coverage gaps

Subnet lifecycle, real DHCP client leases, same-Subnet L2 traffic, service
restart, retry recovery, `/30` addressing, peer-preserving cleanup, BMI-shaped
access-port attachment, and AAP DHCP lease discovery require the deployed lab
journey owned by the `e2e` branch and tracked by [DEV OSAC-5530](https://redhat.atlassian.net/browse/OSAC-5530)
and [Feature OSAC-1562](https://redhat.atlassian.net/browse/OSAC-1562). The lab
uses deployed Fulfillment/database/operator/AAP/SSH/Linux/dnsmasq/Cumulus VX
components and untagged test clients. It does not establish physical-hardware,
BMI provisioning/reboot/status feedback, ExternalIPAttachment DNAT, NAT, or
full-install CI coverage. Unit and contract tests cover lease lookup and
inventory validation only. ExternalIPAttachment and broader fabric-manager
coverage remain tracked under [Feature OSAC-3664](https://redhat.atlassian.net/browse/OSAC-3664)
and [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) /
[OSAC-4850](https://redhat.atlassian.net/browse/OSAC-4850).

The integration harness still has provider-dependent scenarios that cannot run
without AAP or additional infrastructure. Changes to provisioning behavior
must identify the real or contract boundary explicitly and link any missing
coverage to [OSAC-4850](https://redhat.atlassian.net/browse/OSAC-4850) or the relevant [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) follow-up.

Template publishing OAuth has unit and local mock-server coverage, but no
deployed test currently proves AAP worker secret delivery or connectivity across
the Keycloak, AAP, and Fulfillment boundaries. Track that deployed journey under
[OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843).

## osac-csi-driver

Touched-area requirements: [component guide](../osac-csi-driver/AGENTS.md#integration-testing).

### Test tiers and commands

| Tier | Location / command | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | Co-located Go tests; `make test` | Driver, node/controller mapping, fulfillment client, and validation logic in-process | Fulfillment service and vendor storage endpoints are mocked. |
| Unit (CSI sanity) | `test/sanity/`; included by `make test` | CSI protocol calls over Unix sockets and the meta-driver's routing behavior | The vendor controller/node implementation is `fakeVendor`; fulfillment volume operations use a stub. |
| Component integration | No dedicated real-backend suite currently exists | — | No real storage vendor, attach/detach, mount, or fulfillment deployment is exercised by `make test`. |
| Contract | No dedicated contract suite; track [OSAC-4845](https://redhat.atlassian.net/browse/OSAC-4845) for vendor and fulfillment-boundary coverage | No deployed fulfillment or real vendor endpoint is exercised | Fulfillment and vendor calls use stubs and `fakeVendor`. |
| E2E | `../tests/e2e/storage/` when enabled | Tenant/CaaS storage-controller lifecycle and StorageClass setup | These flows do not currently create a PVC through the OSAC CSI driver or verify CSI `CreateVolume`, node publish/mount, or pod I/O. They depend on the selected storage tier and environment gates. |

### Coverage notes

- **Request/response mapping, validation, or driver helpers:** Cover protocol errors and backend status mapping.
- **CSI controller/node routing or CSI protocol behavior:** The sanity suite is required but remains fake-vendor coverage.
- **Fulfillment private Volume API contract:** A fake generated client does not prove compatibility with the deployed service.
- **Vendor attach, detach, mount, or storage lifecycle:** The fake vendor cannot satisfy a real storage-backend requirement.
- **Helm/deployment changes:** Build an image when container/deployment inputs change.

### Coverage gaps

The current sanity suite intentionally stops at a fake vendor and a fulfillment
stub. Changes to a real storage backend, attach/detach, mount, or deployed
fulfillment boundary require the real-backend coverage tracked by [OSAC-4845](https://redhat.atlassian.net/browse/OSAC-4845);
do not label fake-vendor sanity coverage as component integration coverage.
The current storage E2Es validate orchestration and StorageClass setup, not the
full CSI delivery path from PVC creation through LVMS/TopoLVM to a mounted
workload. That end-to-end user journey still needs an explicitly owned QE test.

## osac-metering

Touched-area requirements: [component guide](../osac-metering/AGENTS.md#integration-testing).

### Test tiers and commands

| Tier | Location / command | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | Co-located tests in `schema/`, `metering-service/` (rules, usage evaluation), and `adapters/`; `make test` | Schema, rule revision parsing and startup checks, usage evaluation (meters, gate semantics, duration arithmetic), adapter translation, runner, retry, ordering | Kafka and PostgreSQL are not required. |
| Component integration (database) | `metering-service/internal/database`; included by `make test` | A real PostgreSQL testcontainer, the event-log schema, idempotent fact writes, ledger and outbox round trips | Kafka is not exercised. |
| Component integration (Kafka and database) | `metering-service/internal/fold` and `metering-service/internal/round`; included by `make test` | A real Kafka broker and PostgreSQL: fold determinism across restarts, duplicates, and shuffles; heartbeat rounds, catch-up and recovery, relay resend with the same IDs | The billing provider and fulfillment are not exercised; events are produced directly. |
| Component integration (combined pipeline) | `metering-service/internal/pipeline`; included by `make test` | A real Kafka broker and PostgreSQL with the fold, round, and relay wired in one process: envelope, per-interval continuation, stop, resize split, delete, and volume billing, asserted on the tenant's heartbeat topic | The billing provider and fulfillment are not exercised; events are produced directly and rounds run at fixed times. |
| Contract | `adapters/m360/contract_test.go`; runs when `M360_TEST_API_URL`, `M360_TEST_API_KEY`, and `M360_TEST_TENANT` are set, skips otherwise | A real M360 tenant: submits positive and negative quantities, duplicate IDs, dated heartbeats, invalid heartbeats, and invoiced-period corrections, and asserts synchronous acceptance or rejection | Reading the tenant's usage totals back is a gap: the adapter has no query path, so rate and deduplication arithmetic is not verified end to end. Deploying the M360 tenant is out of scope for this repository. |
| E2E | Cross-component OSAC metering/E2E deployment | The deployed metering pipeline and its configured Kafka/provider dependencies | Depends on the installer environment and enabled metering path. |
| Helm render and lint | `make -C osac-metering helm-lint` | Chart templates, rule revision 0001, and the admission policy that protects it | Deployed behavior. |
| Helm render | `make -C osac-installer metering-auth-render-test` | Metering Deployment, rules ConfigMap, admission policy and binding, and absence of fulfillment credentials in the vmaas-ci, bmaas-ci, caas-ci, and full-ci profiles | Does not verify a deployed metering pipeline. |

### Coverage notes

- **Event schema or usage evaluation:** Schema changes affect `schema/`, `metering-service/`, and `adapters/`; run the root `make test` after schema changes.
- **Fold, database, rounds, or relay:** Do not skip the real PostgreSQL and Kafka suites; they are the determinism gate for billing.
- **Rule revisions:** `metering-service/internal/rules` checks that shipped revisions parse, use only tracked fields, never change, and that the chart's meters match the ones in code.
- **Provider adapters:** The shared runner must remain the owner of ordering, retry, deduplication, and DLQ behavior; the DLQ is mandatory.

### Coverage gaps

- Deployed cross-component journeys (tenant onboarding creating the three topics) belong to the `[QE]` E2E work; the event-to-heartbeat path on a hub is covered by the metering sanity tier in `tests/e2e/vmaas/sanity/test_metering_billing.py`.
- The M360 contract test needs a real tenant configured per environment; it is skipped when the variables are absent. It verifies submission and synchronous acceptance or rejection; verifying the billed totals needs a usage query path the adapter does not have yet.

## tests/e2e

Touched-area requirements: [component guide](../tests/e2e/AGENTS.md#touched-area-map).

| Tier | Location / command | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit ([DEV], VMaaS deletion-test finalizer retries) | From the repository root: `uv run pytest -n 0 tests/unit/test_vmaas_deletion_finalizer.py` | Test-helper retry policy: reread versions and finalizers after Aborted, preserve concurrent finalizers, stop after bounded attempts, skip no-op updates, and propagate other errors | gRPC responses and time are mocked; no production behavior changes or deployed deletion coverage. |
| Unit ([DEV], VMaaS Netris parallel setup and staged teardown) | From the repository root: `uv run pytest -n 0 tests/unit/test_vmaas_parallel_setup.py tests/unit/test_vmaas_networking_helpers.py tests/unit/test_vmaas_ssh_helper.py` | `run_parallel` semantics (re-raise of `pytest.fail`, all workers finished, first failure in submit order), Subnet and SecurityGroup ordering after the VirtualNetwork, network mapping by submit order, serial VM creates per tenant, the per-namespace SSH helper lock, staged teardown ordering with idempotent finalizers, and the pool fixture check | gRPC, Kubernetes, SSH and time are faked; no deployed run time is measured. |
| E2E (VMaaS deletion feedback, [QE] [OSAC-6513](https://redhat.atlassian.net/browse/OSAC-6513)) | From the repository root: `uv run pytest -n 0 tests/e2e/vmaas/regression/test_compute_instance_disk_image.py -k deletion_feedback_with_removed_disk_image` | Deployed Fulfillment/database accept private DELETING feedback carrying the removed DiskImage reference; the operator, AAP, and VM provider remove the ComputeInstance, permitting explicit SecurityGroup, Subnet, and VirtualNetwork deletion | Requires a configured VMaaS template, instance type, storage tier, and networking provider; no deletion completion is mocked. |
| E2E ([DEV] OSAC-5530 lab journey) | From the repository root: `AAP_PROJECT_GIT_BRANCH=OSAC-5530-subnet-vlan-dhcp AAP_PROJECT_EXPECTED_REVISION=<runtime-sha> bash ./vlan-e2e.sh` | Deployed Fulfillment/database/operator/AAP/SSH/Linux/dnsmasq/Cumulus VX; real DHCP leases from BusyBox clients, gateway pings, same-Subnet L2 across leaves, DHCP restart/renewal, Subnet retry, `/30` single-address allocation, deletion, and cleanup | Uses client containers and virtual switches; it does not establish physical-hardware, BMaaS lease-status, NAT, or full-install CI coverage. A successful run is required to claim deployed behavior. |
| E2E (VMaaS regression) | From the repository root: `uv run pytest tests/e2e/vmaas/regression/test_compute_instance_instance_type.py` | InstanceType resize through CLI/API, CatalogItem provisioning, and Kubernetes/KubeVirt resources | Requires a configured single-node VMaaS environment; no services are mocked. |
| E2E (metering sanity) | From the repository root: `METERING_ADAPTER_URL=<echo adapter URL> uv run pytest tests/e2e/vmaas/sanity/test_metering_billing.py` | The deployed API, Kafka, and metering pipeline through the echo adapter's query API: well-formed heartbeats, per-interval continuation, stop and delete ending billing, and the resize dimension split. It has no ExternalIP case; ExternalIP billing is covered only by the billing files of `tests/e2e/networking/vmaas/`, in the Netris VMaaS job | Requires a deployed cluster with the metering stack, the echo adapter, and `METERING_ADAPTER_URL` set; no component imports or mocks. |
| E2E (VMaaS additional disks) | From the repository root: `uv run pytest tests/e2e/vmaas/regression/test_compute_instance_storage_tier.py -k empty_additional_disks_disables_catalog_item_default` | CatalogItem default application versus an explicit empty `additional_disks` wrapper, checked on the ComputeInstance CR | Requires a configured VMaaS environment with storage tiers, subnet, disk image, instance type, and VM template; no services are mocked. |
| Unit ([DEV], E2E failure reporting) | From the repository root: `uv run pytest -n 0 tests/unit/test_e2e_failure_report.py tests/unit/test_e2e_failure_plugin.py tests/unit/test_e2e_failure_snapshot.py` | Sanitizer, `CommandFailed`/`StuckState`, failure classification, and the reporter plugin through real `python -m pytest` subprocesses: immediate `E2E FAILURE` log line, fsynced `results-<worker>.jsonl`, `-x`, kill, xdist workers, snapshot cap and budget | Cluster, API, Kubernetes, fabric and guests are mocked or absent; snapshot content is not proven against a deployed environment. |
| Unit ([DEV], compute-reference teardown) | From the repository root: `uv run pytest -n 0 tests/unit/test_e2e_helpers.py` | Ordered cleanup at all four compute-reference call sites, exact Kubernetes/API NotFound, bounded polling, and credential-safe command failures | API/client responses and time are mocked; does not prove deployed reconciliation. |
| E2E ([QE], compute references) | From the repository root: `uv run pytest -n 0 tests/e2e/references/test_compute_references.py` | Reference-based ComputeInstance creation and natural cleanup before deleting dependent Secrets/catalog items or releasing module-scoped dependencies | Requires deployed Fulfillment, operator, AAP, KubeVirt, storage, and tenant networking. No forced finalizer removal; local unit success is not evidence of a passing deployed run. |
| E2E ([QE] Netris VMaaS connectivity, [OSAC-4291](https://redhat.atlassian.net/browse/OSAC-4291)) | Manual vmaas-netris suite (`DEPLOY_SUITE=vmaas`), run with `-x`; locally: `uv run pytest -n 0 -x tests/e2e/networking/vmaas/test_vmaas_networking.py` | Real Fulfillment, operator, AAP, KubeVirt, CUDN/EVPN, and Netris; ordered `test_00`..`test_14`: same-Subnet ARP/ICMP, cross-VirtualNetwork and cross-tenant isolation, cluster DNS, SecurityGroup TCP allow/deny with rule updates, NAT egress, manual/automatic ExternalIP ingress for tenant1 and manual ingress for tenant2, attachment delete stopping ingress, and one staged teardown test that checks nothing is left and that none of the suite's ExternalIPs is left and the pool `allocated` count equals the live ExternalIPs on it (an owned pool held by tenant default ExternalIPs is not deleted; any other holder fails). The routed pool is checked once by the `netris_external_path` fixture. **Removed E2E requirements, not covered at an equal tier:** ICMP SecurityGroup rule rejection by NetworkClass capability (the lower tiers are the `TC-SG-ICMP-01` specs in `fulfillment-service/it/it_networking_design_test.go`, the SecurityGroup server specs, and the operator capability spec; the capable branch's READY through AAP is not covered on cudn_evpn) and second-tenant automatic ExternalIP ingress and garbage collection. After the module the job runs the billing files as a second pytest run (next row) | Requires the `vmaas-ci` profile with `values/vmaas-ci/netris.yaml` (`operator.aap.statusPollInterval: 5s`), VM kubeconfig, and `kubectl`; guest commands run over `kubectl exec` in a test-owned SSH helper Pod in each VM namespace (by default the digest-pinned OSAC execution-environment image; override with `OSAC_E2E_SSH_HELPER_IMAGE`, an image with an ssh client that the cluster can pull). Independent resources are created at the same time and every child waits for its parent to be READY in both the CR and the API. The NAT and ExternalIP tests always run and fail with a diagnosis when the external path is not wired; they need the routed pool and pytest on a host that routes it. The tests are numbered and serial. Inter-subnet routing is not covered. Not integration evidence until run against a deployed lab. |
| E2E (ExternalIP and NATGateway billing) | Second pytest run of the Netris VMaaS job, after the module and also with `-x`; it starts even when the module failed, and a failure in either run fails the job; locally: `METERING_ADAPTER_URL=<echo adapter URL> uv run pytest -n 0 -x tests/e2e/networking/vmaas/test_external_ip_billing.py tests/e2e/networking/vmaas/test_external_ip_reattach_billing.py tests/e2e/networking/vmaas/test_nat_gateway_billing.py` | Real Fulfillment, operator, AAP, KubeVirt, Fabric and Kubernetes Managers, Kafka and metering through the echo adapter: an ExternalIP billed through deletion, attach/detach/reattach to a second ComputeInstance with disjoint attached and unattached meters, and NATGateway billing with its two references as dimensions | Requires `global.networking.provisioningEnabled: true` (`values/vmaas-ci/netris.yaml`); with it disabled ExternalIP creation is rejected with `FailedPrecondition`. The tests send no traffic and need no helper Pod. They use the routed pool (`OSAC_EXTERNAL_IP_POOL_CIDR`) and skip, giving no signal, when no echo adapter is found. **Moved to lower tiers:** ExternalIPPool allocation, exhaustion, release and the pool-deletion guards walk a real /30 pool in `Public external IPs server` (`walks a small pool through allocation, exhaustion, release and deletion`, `fulfillment-service/internal/servers/external_ips_server_test.go`, real PostgreSQL with mocked tenancy, no provider), with `TC-OP-03` in `fulfillment-service/it/it_networking_operator_test.go` for pool CR removal (deployed operator, provider networking disabled). Attachment validation, the field path of an unknown target and the same-target duplicate are the `Create through the reference interceptor` specs in `external_ip_attachments_server_test.go` (real reference validator, public server and PostgreSQL) and `TC-DISABLED-02` in `fulfillment-service/it/it_networking_design_test.go` (deployed); the absent public ExternalIP Update is `it_updateless_networking_contract_test.go` (deployed). References by name are `creates an ExternalIP from a shared pool given by name` in `external_ips_server_test.go`, `Create with references by name` in `private_nat_gateways_server_test.go`, the tenant-user by-name pool spec in `fulfillment-service/it/it_networking_tenant_isolation_test.go` (deployed, provider networking disabled; compiled, not yet observed on a deployed run), and the by-name `NATGateways/Create` of `test_vmaas_networking.py`. **Do not cite as coverage:** the specs in `it_external_ip_test.go`, `it_nat_gateway_test.go` and `it_networking_tenant_isolation_test.go` behind `ndSkipUnlessProviderNetworkingEnabled` skip in `SUITE=fulfillment`, which deploys with provider networking disabled. **Not covered by a lower tier:** the three billing journeys above (no suite crosses fulfillment, Kafka and metering for them; the rule-level pieces are `TestExternalIPAllocationDimensions` and `TestNATGatewayAllocation` in `osac-metering/metering-service/internal/usage/engine_test.go`), and the real provider release of pool capacity, which only the `test_14` accounting check sees. ExternalIPPool capacity initialization runs in `tests/e2e/vmaas/regression/external_ip/`. |
| E2E (Netris BMaaS connectivity) | Manual bmaas-netris suite; locally: `uv run pytest -n 0 tests/e2e/networking/bmaas` | Numbered BMI networking lifecycle, including same-subnet ARP/ICMP, cross-subnet routing/isolation, and cross-tenant cases | Requires Netris BMaaS services, BMH SSH endpoints, and `OSAC_BMI_NETWORK_INTERFACE`. The PR job excludes the slower cross-tenant class. The Netris `full` suite ignores this directory: it runs on netris + cudn_evpn, where the API rejects its `PROTOCOL_ICMP` rules. |
| E2E (stretched BMI/ComputeInstance connectivity) | Backend-agnostic test: `uv run pytest -n 0 tests/e2e/networking/stretched`; test-infra selectors: `DEPLOY_SUITE=full TEST_SUITE=stretched` | Creates a BMI and ComputeInstance on one OSAC Subnet and verifies unique addresses plus bidirectional ARP/ICMP | The Netris `/test stretched-netris` workflow provisions `full-ci`, BMaaS SSH access, `OSAC_BMI_NETWORK_INTERFACE`, the VM template, `kubectl` on the pytest host, an SSH helper Pod in the VM namespace for guest commands (`OSAC_E2E_SSH_HELPER_IMAGE`), and CUDN EVPN VTEP/underlay. `DEPLOY_SUITE=full` also includes this test. It is not part of standard VMaaS or BMaaS runs. |
| Unit ([DEV], CaaS teardown) | From the repository root: `uv run pytest -n 0 tests/unit/test_caas_teardown_order.py tests/unit/test_cluster_deletion_polling.py tests/unit/test_caas_deletion_diagnostics.py tests/unit/test_caas_worker_bmi_visibility.py tests/unit/test_caas_two_node_sets.py tests/unit/test_caas_selector_contracts.py` | Read-only wait logic, exact-resource NotFound, ordered worker/parent/dependent waits, shared single/two-node-set budgets, stage-specific safe failures, snapshot throttling and sanitization, worker ownership checks, NodeSet selectors with shared BMITs, and shared-only BMIT reference expectations | API/client responses and time are mocked. No deployed controllers, fulfillment, AAP, provider, or metering is exercised. |
| E2E ([QE], focused bare-metal CaaS lifecycle) | From the repository root: `uv run pytest -n 0 tests/e2e/caas/sanity/test_cluster_create.py::test_cluster_create --junitxml=/tmp/test-output/caas-bm-teardown-junit.xml` | CLI/API/database, Kubernetes, OSAC operators, AAP, HyperShift/CAPI/CAP-Agent, Assisted Service, provider-backed virtual BMHs, and Kafka/metering; catalog-item-authored creation (published ClusterCatalogItem locking version, node sets, and SSH key), guest readiness, scale events, natural worker/parent teardown, independent InfraEnv GC, fulfillment removal, and deleted events | Requires the compatible deployed CaaS profile; no mocked completion or workaround-enabled deletion wait. Virtual BMHs do not prove physical-hardware coverage. Guest LVMS device readiness, PVC/CSI mount, and application I/O are not established by this lifecycle test. |

Resize lifecycle tests expect `RestartRequired`. Multi-node live hot-plug
coverage is tracked under
[OSAC-5335](https://redhat.atlassian.net/browse/OSAC-5335).

### CaaS CI selection boundary

The full-install callers in osac and osac-test-infra select `caas/sanity` with
`-m sanity` for PRs and merge queue. Test-infra dispatches carrying `pr-number`
use the same policy regardless of tier labels or suite/marker overrides.
Periodics select all of `caas` (sanity + regression) plus reference tests marked
`requires_caas` or `reference_common`. Manual dispatches without PR context
default to sanity and retain suite/filter/marker overrides. The reusable runner
never adds references to sanity; an empty requested tier fails rather than
falling back to the whole suite. Nightly/release validation already selects
`caas` explicitly and remains unchanged.

[DEV] Unit/Contract selection checks live in
`tests/unit/test_caas_ci_suite_policy.py`. From the repository root, run
`OSAC_TEST_INFRA_DIR=/path/to/osac-test-infra uv run pytest -n 0 tests/unit/test_caas_ci_suite_policy.py`.
They evaluate actual caller YAML and execute path-selection Bash against
local temporary directories. Companion-repository checks skip when the path
is not set. These checks and pytest collection prove selection only, not
[QE] deployed CaaS success; the live prerequisites below still apply.

### E2E component build-context boundary

The full-install callers hand osac-test-infra's reusable workflow a
`components` list, and it builds each entry as
`podman build -f <containerfile> <context>`. A COPY source outside the declared
`context` is therefore unreadable, and the failure only appears mid-run as
`copier: stat: "<path>": no such file or directory`. The metering echo adapter,
like metering-service, copies `go.work` and every workspace sibling's `go.mod`
from the repository root, so its context is `.` and not `osac-metering`.

[DEV] Unit/Contract checks live in
`tests/unit/test_e2e_component_build_contexts.py`. From the repository root,
run `uv run pytest -n 0 tests/unit/test_e2e_component_build_contexts.py`. They
read every component declaration in
`.github/workflows/e2e-*-full-install.yml` and in
`.github/actions/resolve-e2e-components/action.yaml`, then resolve each
referenced Containerfile's COPY sources against its declared context on disk.
They prove path reachability only: no image is built, so `.containerignore`
allowlists, registry access, and build success are not established.

### Focused CaaS natural-teardown boundary

CaaS sanity retains the single-nodepool lifecycle and fast deletion feedback
scenario and verifies deployed version resolution through the ClusterOrder and
HostedCluster release image. Two-node-set isolation runs in
`caas/regression/test_cluster_node_sets.py`. Invalid-version API validation and
ClusterVersion deletion protection belong to the Fulfillment integration suite
in `fulfillment-service/it/it_public_clusters_test.go`.

Both `test_cluster_create` and `test_cluster_create_with_two_node_sets` use the
same natural teardown assertions. `test_cluster_create` creates its cluster from a
published ClusterCatalogItem; the two-node-set scenario still creates its cluster
directly and additionally
checks ready worker aggregates, installed Agents, and per-NodeSet
NodePool isolation. Unit regressions additionally cover distinct NodeSets
sharing one BMIT; that same-profile case is not exercised by this deployed
scenario. Run that [QE] E2E with
`uv run pytest -n 0 tests/e2e/caas/regression/test_cluster_node_sets.py::test_cluster_create_with_two_node_sets`;
it requires the same source-pinned environment described below and enough
available BMHs for both worker sets.

The deletion request triggers the test-owned worker BMI wait (480 attempts at
five-second intervals). Worker ownership is verified through the fulfillment
BMI's tenant, cluster-order label and owner-reference annotation, plus the
Kubernetes CR's tenant annotation and BMI UUID label, before those IDs enter
the deletion assertions. The API owner-reference annotation is not copied to
the CR. Only after all verified BMIs disappear does the
read-only parent wait observe the exact ClusterOrder NotFound (121 attempts at
ten-second intervals); only after parent removal does the separate InfraEnv GC
wait observe that exact InfraEnv NotFound in the same namespace (60 attempts at
five-second intervals). Empty status, a terminating object, or an API error is
not absence. Parent lookup errors fail fast; InfraEnv lookup errors retain the
existing retry policy but cannot satisfy the absence assertion.

The parent and dependent budgets schedule at most 1,200 and 295 seconds of
sleeps, respectively. Command execution and approximately sixty-second,
monotonic-throttled diagnostic snapshots add time, so these are not strict
elapsed deadlines or production SLAs. Either stage timing out remains a test
failure, with a final sanitized snapshot, even if resources disappear later.
The focused path does not remove lifecycle hooks or finalizers. Other scenarios
that still call the legacy cleanup-enabled `wait_for_cluster_deletion` do not
prove natural teardown. Unit tests and collection do not prove deployed E2E
success; this test-only change adds no Envtest/component-integration/Contract
tier.

Use `tests/e2e/conftest.py` for fixture configuration: hub kubeconfig and
`OSAC_NAMESPACE`, public/private fulfillment endpoints and auth, current CLI and
required utilities, template/release/disk images, pull-secret/SSH-key inputs,
available virtual BareMetalHosts, and healthy Kafka/metering. Verify compatible
source revisions and image digests for OSAC, operators, installer, and the AAP
execution environment/project; AAP must resolve the exact tested revision, not
mutable `main`/`latest`. Preserve JUnit, lifecycle logs, and cleanup evidence
before infrastructure teardown.

Guest LVMS disk prerequisites are a separate storage/infrastructure follow-up;
its infrastructure source revision, owner, and Jira URL remain unresolved. Do
not wipe or reuse the guest OS disk to make the lifecycle test pass.

## Multi-branch test isolation

See [Multi-branch test isolation](MULTI-BRANCH-ISOLATION.md) for detailed
guidance on running integration tests from multiple checkouts simultaneously,
including Kind cluster naming, namespace parameters, port isolation, database
and Kafka separation, cleanup procedures, and a quick-reference checklist.
