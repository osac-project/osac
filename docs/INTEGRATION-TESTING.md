# Integration testing

Use the touched-area map in the affected component's `AGENTS.md` to select
validation. Read the corresponding section below for suite boundaries and gaps.
Commands and inline paths in each component section are relative to that
component's directory unless explicitly marked as repository-root commands.

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

## fulfillment-service

Touched-area requirements: [component guide](../fulfillment-service/AGENTS.md#integration-tests).

### Test tiers and commands

| Tier | Location / command | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | `internal/`; `ginkgo run -r internal` | Fulfillment logic and adapters covered by package tests | External services are mocked where the package tests use mocks. |
| Component integration | `it/`; `make -C ../osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment` | Deployed Fulfillment Service, its database, and the CLI built from this checkout. The NetworkClass manager-registration spec runs separately with the local OSAC Operator image deployed. | CaaS, VMaaS, BMaaS, and external provider workflows unless a specific test exercises them. |
| E2E | `../tests/e2e/` | Cross-component OSAC user journeys | Depends on the deployed test environment and its configured providers. |

### Coverage notes

- **CLI commands that only call Fulfillment APIs:** Cover them in `fulfillment-service/it/`.
- **Canonical networking Hub routing:** [`fulfillment-service/it/it_networking_hub_placement_test.go`](../fulfillment-service/it/it_networking_hub_placement_test.go) covers VirtualNetwork, Subnet, SecurityGroup, ExternalIPPool, ExternalIP, ExternalIPAttachment, and NATGateway CR placement on the NetworkClass canonical Hub and absence on a valid alternate Hub. It also verifies that SecurityGroup retains its stored Hub assignment and does not create a duplicate CR when the canonical Hub changes. The fixture uses distinct Hub entries and namespaces on the service Kind cluster; it tests Hub entry and namespace routing, not isolation across separate Kubernetes clusters. The unavailable-canonical/no-fallback case remains controller unit coverage because this deployed-service harness cannot isolate or reset the reconcilers' cached Hub resolution between cases.
- **NetworkClass manager registration and capability propagation:** `it_networkclass_manager_capabilities_test.go` creates the NetworkClass first, then adds fabric and Kubernetes manager registrations and verifies the deployed operator persists their capability intersection. The installer target runs this spec separately with the local operator image so the rest of the service-only suite remains isolated from operator reconciliation.
- **Provisioning journeys that cross into operators or providers:** Keep them in `tests/e2e/` and exercise those boundaries explicitly.
- **Catalog Items:** `it/` checks creation and update behavior, publication visibility, CLI creation, and the ClusterOrder release image written by Fulfillment. Catalog-backed provisioning journeys that exercise other components remain in the CaaS, VMaaS, BMaaS, and reference E2E suites.

## osac-operator

Touched-area requirements: [component guide](../osac-operator/AGENTS.md#integration-testing).

### Test tiers and commands

| Tier | Location / command | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | Co-located `*_test.go`; `make test` | Controller helpers, validation, provisioning state logic | External APIs and providers are mocked. |
| Envtest | Controller tests under `internal/controller/*_envtest_test.go`; run by `make test` | Kubernetes API server, etcd, loaded CRDs, and in-process reconciliation | The controller is not deployed to Kind; provisioning uses controllable or noop providers. |
| Component integration | `test/integration/`; deploy the current operator into a Kind cluster, then run `make integration-tests` | Installed operator, Kubernetes API, CRDs, controller-manager, console proxy, networking behavior, and startup with the Volume controller enabled but no LVMS endpoint or TopoLVM CRD | AAP/provider provisioning and external infrastructure are not real in the current suite; the LVMS-disabled case does not exercise LogicalVolume provisioning. Some tests remove finalizers to bypass external-provider boundaries. |
| Component integration (CI) | `make -C osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=operator` (from repository root) | The thin Kind deployment used by the PR workflow | The same external-provider limitations as the local Kind suite. |
| Component integration / fulfillment-worker contract (opt-in) | `test/integration/caas/`; `make sim-up`, then `make test-integration-caas` | Dedicated marked Kind API, real fulfillment gRPC/Postgres/Keycloak/controller/hub, fulfillment-created ClusterOrder, host-built worker reconciler, real BMI persistence and duplicate-name check | Network readiness/NetworkClass prerequisite and discovery ignition simulated; no running operator/BMF, Agent binding, AAP, hardware, public auth, deployed metrics HTTP or guest install. See [usage](../osac-operator/test/integration/caas/README.md). |
| Contract | `test/contract/`; included by `make test` | Helm chart RBAC templates against the operator permission contract | No deployed operator or external provider is exercised. |
| E2E | `../tests/e2e/` | Cross-component fulfillment journeys | Depends on the deployed test environment and its configured providers. |

### Coverage notes

- **Pure helpers, validation, or state calculations:** Add error and edge-case coverage.
- **Controller reconciliation, finalizers, status, or CRD interactions:** The envtest suite must exercise the changed lifecycle through the public reconciler behavior.
- **LVMS Volume lifecycle:** `lvms_vendor_provisioner_envtest_test.go` exercises RWO/RWOP provisioning, API-generated LogicalVolume names, persisted name/UID resumes, replacement while an old CR is terminating, and deletion through the public Volume reconciler. Stale parent snapshots are injected to verify authoritative reads prevent another create after context persistence. `volume_controller_test.go` injects status conflicts to verify newer vendor context, deletion and replacement UIDs are preserved. Kubernetes and etcd are real; the cached snapshots and TopoLVM status are simulated, and the TopoLVM CRD is a minimal fixture. Default-device-class selection, LVMD provisioning, and CSI mounting remain real-provider/E2E coverage under [OSAC-3711](https://redhat.atlassian.net/browse/OSAC-3711).
- **Controller deployment, watches, RBAC, console proxy, networking, or Helm wiring:** Unit/envtest coverage alone does not prove deployed wiring.
- **AAP, dispatcher, provisioning-provider, KubeVirt, or fulfillment boundary:** A controllable provider in envtest is not coverage of the real provider boundary.
- **Generated CRDs or manifests:** Do not hand-edit generated output.
- **Bare-metal worker identity ([DEV], Unit/Envtest):** Unit tests in `internal/controller/baremetalworker/nodesets_test.go` verify bounded opaque names, reservation before external creation, stale-snapshot resumes, retry backoff, and logical NodeSet-based scale-down, including multiple NodeSets sharing a hardware profile. NodePool capacity/readiness and feedback unit tests verify independent counts and exact map-key attribution without instance-type fallback. The acceptance envtest suite verifies persisted BMI name/ID references, interrupted-create recovery, existing-worker reuse, independent request scaling/reordering, and recovery before cluster finalization. Missing-name references are rejected without guessing from worker names; finalization retains the worker and finalizer for reference repair. Kubernetes/etcd and generated CRDs are real; fulfillment and Agent state are simulated. This does not replace deployed fulfillment/BMaaS, provider, or hardware coverage; those boundary gaps remain owned by [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843).

- **Bare-metal worker Agent convergence ([DEV], Unit/Envtest):** `internal/controller/baremetalworker/agent_reconcile_test.go` and `correlation_test.go` cover all eligible worker phases and protected states, early capacity/stale-ignition observation, unique MAC matching, authoritative binding isolation/conflicts, registration timeout, interrupted status recovery, ReadySince retention, and transition event/metric counts. The `reconcileAgent` scenarios in `acceptance/reconciler_test.go` invoke public `Reconcile` manually and read back real persisted phases/counts after Installed-condition changes or Agent removal. They also verify bound-Agent/stale-Waiting recovery without another BMI or Agent patch, lifecycle-state preservation while provider deletion is pending, and ambiguous binding refusal. Kubernetes/etcd and CRDs are real; fulfillment and Agent state are simulated. These tests do not run a manager or establish watch delivery/restart latency, deployed Assisted Service, or hardware behavior; provider gaps remain under [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843).

- **Unified bare-metal worker convergence ([DEV], Unit/Envtest):** The `worker_*_test.go` and BMI identity suites under `internal/controller/baremetalworker/` cover invocation-local BMI/Agent read reuse, pure per-worker identity/phase projection, guarded latest-status merges, authoritative reservation/capacity checks, interruption after a successful create, concurrent foreign-reference interruption, one fresh ownership/existence Get per destructive attempt, and finalizer retention for concurrently appended workers. `acceptance/worker_reconcile_test.go` drives public `Reconcile` through real CRD status persistence and optimistic-lock conflicts. It checks successful-create ID-write recovery without another create, authoritative NotFound replacement, list-omission fallback, unknown List/Get errors and service-unavailable requeues, repair before InfraEnv gates, rejection of stale-failure persistence before recording a recreated UID, preservation of concurrently appended status and latest aggregate counts, and finalization recovery without resetting history or allocating capacity. The older rebuild pipeline is removed; its mixed/protected worker, MAC fallback, and clock/history cases are covered by the combined observation tests. Explicit successful reconciliations establish the convergence assertions, not fallback polling. Kubernetes/etcd and generated CRDs are real; fulfillment, ignition and Agent status are simulated. These cases do not establish watch delivery, real fulfillment/BMaaS wire behavior, deployed Assisted Service, or hardware provisioning; missing boundary coverage remains owned by [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843).

Focused commands from `osac-operator/` (also included by `make test`):

```bash
go test ./internal/controller/baremetalworker -count=1
go test -race ./internal/controller/baremetalworker -count=1
KUBEBUILDER_ASSETS="$PWD/bin/k8s/1.31.0-linux-amd64" \
  go test ./internal/controller/baremetalworker/acceptance -count=1
```

The focused Envtest command requires the existing Kubernetes 1.31.0 binaries at
that path; `make test` obtains the platform-appropriate assets through setup-envtest.
Allocation convergence uses explicit finalizer, observation-repair, reservation,
and single-Create checkpoints. R01 Unit cases and the R01-E1 acceptance spec
assert stable opaque names, authoritative stale-snapshot resumes, independent
NodeSets sharing a type, one Create and durable ID per invocation, waiting-retry
selection, a single failed-worker retry Delete (including errors), stale-ignition
repair before UID advancement/deletion, and no hot loop for unchanged capacity. Legacy stage fixtures drive
finite sequences (bounded by `16 + 8*N` for ready fixture dependencies), assert
persisted progress at each allocation checkpoint and at most one Create per
call, and return errors/dependency/backoff results unchanged. This is a fixture
termination bound, not a production latency promise. R01-E2 Envtest cases
exercise a committed Create/lost transport acknowledgement, restart, delayed
List visibility (including the AlreadyExists re-list), same-name recovery and
rejection of foreign, ambiguous or deleting recovery candidates. The fake's
scoped-name uniqueness is independent of ID, but does not establish the real
fulfillment/Postgres contract.

The [connected worker suite](../osac-operator/test/integration/caas/README.md)
includes R01-C1, a [DEV] component-integration case with real fulfillment,
Postgres uniqueness and Kubernetes, simulated ignition/networking readiness,
and no hardware or manager watches. It deliberately loses one successful real
Create response, restarts the reconciler, omits one List to force a real
AlreadyExists re-list, and counts one persisted owned incarnation per reserved
name. Connected fixtures use explicit calls within the same `16 + 8*N` bound.
**Execution evidence:** R01 Unit/Envtest cases executed/passed; R01-C1 is
implemented and compile-checked, not executed against a real backend. The
connected command fails before fixtures because `hack/sim.env` is absent; the
explicit sim kubeconfig endpoint also refuses connections. Do not count this
preflight failure or fake uniqueness as passed real-boundary coverage. Restore
an approved marked sim environment and run the focused R01-C1 case, then
`make test-integration-caas` twice. Deployed provider/hardware gaps remain
[OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843).
Phase repair/demotion is persisted at its own successful explicit reconciliation;
aggregate summaries can follow on a fresh invocation, not fallback polling.
Pending retry deadlines can still return before final Agent convergence.
A failed retry Delete publishes failure summaries, preserves the slot and stops
further capacity actions; protected lifecycle fixtures assert no resurrection
from installed Agents. No controller watch, RBAC, or deployment wiring changes
are involved.

### Coverage gaps

The current component integration suite does not exercise real AAP,
OpenStack, KubeVirt, or hardware provisioning. Changes to those boundaries
must be covered by a contract or real-provider suite owned by the relevant
[OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) follow-up task; they cannot be marked covered by envtest alone.

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
| Unit / isolated role transform ([DEV]) | `uv run --group development ansible-playbook collections/ansible_collections/osac/service/roles/hosted_cluster/tests/test.yml` | Executable NodePool definition transforms: distinct NodeSet names and selectors for the same hardware profile, independent replica counts and scale-up | No Kubernetes resources are created. This is not component-integration or deployed AAP/provider coverage; those gaps remain owned by [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843). |
| Component integration | `tests/integration/`; `make test` (creates Kind, runs playbooks, and tears it down) | Ansible roles/playbooks against a real Kind API, CRDs, leases, finalizers, and test-runner pod | AAP, OpenStack, KubeVirt/RHACM, and other provider APIs are not generally real; the VMS storage target uses a mock server. |
| Component integration (focused) | A target under `tests/integration/targets/`; run the corresponding playbook from `tests/integration/` | The specific role workflow and its documented fixtures | Only the dependencies declared by that target; inspect its setup and overrides before claiming a real boundary. |
| Contract | No dedicated contract suite; use the qualifying [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) task for AAP/provider coverage | No AAP or provider endpoint is exercised as a contract | The Kind API, mock VMS server, and fixture-driven provider behavior do not prove an AAP or provider contract. |
| E2E | Cross-component OSAC E2E suites | Complete fulfillment and provisioning flows | Depends on the deployed AAP and provider environment. |

### Build/package validation

Run `make execution-environment-build` when the execution-environment definition
or dependencies change. This validates image assembly and packaging; run the
applicable integration tests separately to validate workflow behavior.

### Coverage notes

- **Filters, variable transforms, and isolated plugin logic:** Include invalid input and default handling.
- **Ansible roles, workflow tasks, hooks, leases, finalizers, or Kubernetes resources:** The test must exercise the role/playbook through Ansible against Kind.
- **Execution-environment definition or dependency inputs:** Image success does not prove the workflow boundary.
- **AAP, OpenStack, KubeVirt/RHACM, or provider provisioning:** Kind-only tests with mocks cannot claim provider coverage.
- **Storage-provider behavior:** The mock VMS server validates role logic, not the provider API.

### Coverage gaps

The integration harness still has provider-dependent scenarios that cannot run
without AAP or additional infrastructure. Changes to provisioning behavior
must identify the real or contract boundary explicitly and link any missing
coverage to [OSAC-4850](https://redhat.atlassian.net/browse/OSAC-4850) or the relevant [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) follow-up.

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
| Unit | Co-located Ginkgo tests in `schema/`, `metering-service/`, and `adapters/`; `make test` | Schema, mapping, runner, retry, ordering, and adapter behavior in-process | Kafka, fulfillment Watch, and most external services are mocked. |
| Component integration (database) | `metering-service/internal/projection/postgres_test.go`; included by `make test` | A real PostgreSQL testcontainer, schema, persistence, versioning, and queries | Kafka and fulfillment event delivery are not exercised. `SKIP_DB_TESTS` disables this tier. |
| Component integration | No dedicated real-Kafka component suite currently exists | — | Kafka, CloudEvents delivery, fulfillment Watch, offset commits, retries, and DLQ behavior are currently tested with mocks. |
| Contract | No dedicated contract suite; track [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843)/[OSAC-4846](https://redhat.atlassian.net/browse/OSAC-4846) for fulfillment Watch and Kafka boundaries | No deployed Watch or Kafka protocol endpoint is exercised | Mock streams and Kafka mocks are used. |
| E2E | Cross-component OSAC metering/E2E deployment | The deployed metering pipeline and its configured Kafka/provider dependencies | Depends on the installer environment and enabled metering path. |

### Coverage notes

- **Event schema or transition mapping:** Schema changes affect `schema/`, `metering-service/`, and `adapters/`.
- **Projection/database code:** Do not set `SKIP_DB_TESTS` when validating database behavior.
- **Kafka producer/consumer, CloudEvents transport, offsets, retries, or DLQ:** Mock Kafka tests alone do not prove the pipeline boundary.
- **Fulfillment Watch or gRPC event ingestion:** Mock streams validate local handling, not the wire contract.
- **Provider adapters:** The shared runner must remain the owner of ordering, retry, deduplication, and DLQ behavior.

### Coverage gaps

There is no component-level suite that runs the full fulfillment Watch → Kafka
→ CloudEvents pipeline. Changes to that path must not claim integration
coverage from mock-based tests; add or extend the real-Kafka coverage under
[OSAC-4846](https://redhat.atlassian.net/browse/OSAC-4846).

## tests/e2e

Touched-area requirements: [component guide](../tests/e2e/AGENTS.md#touched-area-map).

| Tier | Location / command | Exercises for real | Faked or omitted |
|---|---|---|---|
| E2E (VMaaS regression) | From the repository root: `uv run pytest tests/e2e/vmaas/regression/test_compute_instance_instance_type.py` | InstanceType resize through CLI/API, CatalogItem provisioning, and Kubernetes/KubeVirt resources | Requires a configured single-node VMaaS environment; no services are mocked. |
| Unit ([DEV], CaaS teardown) | From the repository root: `uv run pytest -n 0 tests/unit/test_caas_teardown_order.py tests/unit/test_cluster_deletion_polling.py tests/unit/test_caas_deletion_diagnostics.py tests/unit/test_caas_worker_bmi_visibility.py tests/unit/test_caas_two_node_sets.py tests/unit/test_caas_selector_contracts.py` | Read-only wait logic, exact-resource NotFound, ordered worker/parent/dependent waits, shared single/two-node-set budgets, stage-specific safe failures, snapshot throttling and sanitization, worker ownership checks, NodeSet selectors with shared BMITs, and shared-only BMIT reference expectations | API/client responses and time are mocked. No deployed controllers, fulfillment, AAP, provider, or metering is exercised. |
| E2E ([QE], focused bare-metal CaaS lifecycle) | From the repository root: `uv run pytest -n 0 tests/e2e/caas/sanity/test_cluster_create.py::test_cluster_create --junitxml=/tmp/test-output/caas-bm-teardown-junit.xml` | CLI/API/database, Kubernetes, OSAC operators, AAP, HyperShift/CAPI/CAP-Agent, Assisted Service, provider-backed virtual BMHs, and Kafka/metering; creation, guest readiness, scale events, natural worker/parent teardown, independent InfraEnv GC, fulfillment removal, and deleted events | Requires the compatible deployed CaaS profile; no mocked completion or workaround-enabled deletion wait. Virtual BMHs do not prove physical-hardware coverage. Guest LVMS device readiness, PVC/CSI mount, and application I/O are not established by this lifecycle test. |

Resize lifecycle tests expect `RestartRequired`. Multi-node live hot-plug
coverage is tracked under
[OSAC-5335](https://redhat.atlassian.net/browse/OSAC-5335).

### Focused CaaS natural-teardown boundary

Both `test_cluster_create` and `test_cluster_create_with_two_node_sets` use the
same natural teardown assertions. The two-node-set scenario additionally
checks ready worker aggregates, installed Agents, and per-NodeSet
NodePool isolation. Unit regressions additionally cover distinct NodeSets
sharing one BMIT; that same-profile case is not exercised by this deployed
scenario. Run that [QE] E2E with
`uv run pytest -n 0 tests/e2e/caas/sanity/test_cluster_create.py::test_cluster_create_with_two_node_sets`;
it requires the same source-pinned environment described below and enough
available BMHs for both worker sets.

The deletion request triggers the test-owned worker BMI wait (480 attempts at
five-second intervals). Worker ownership is verified through both fulfillment
and Kubernetes tenant/owner annotations before those IDs enter the deletion
assertions. Only after all verified BMIs disappear does the
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
