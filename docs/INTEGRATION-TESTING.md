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
| Component integration | `it/`; `make -C ../osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment` | Deployed Fulfillment Service, its database, and the CLI binary built from this checkout. NetworkClass manager-registration and readiness specs run separately with the local OSAC Operator image deployed. | CaaS, VMaaS, BMaaS, and external provider workflows unless a specific test exercises them. |
| E2E | `../tests/e2e/` | Cross-component OSAC user journeys | Depends on the deployed test environment and its configured providers. |

### Coverage notes

- **CLI commands that only call Fulfillment APIs:** Cover them in `fulfillment-service/it/`.
- **Canonical networking Hub routing:** [`fulfillment-service/it/it_networking_hub_placement_test.go`](../fulfillment-service/it/it_networking_hub_placement_test.go) covers VirtualNetwork, Subnet, SecurityGroup, ExternalIPPool, ExternalIP, ExternalIPAttachment, and NATGateway CR placement on the NetworkClass canonical Hub and absence on a valid alternate Hub. It also verifies that SecurityGroup retains its stored Hub assignment and does not create a duplicate CR when the canonical Hub changes. The fixture uses distinct Hub entries and namespaces on the service Kind cluster; it tests Hub entry and namespace routing, not isolation across separate Kubernetes clusters. The unavailable-canonical/no-fallback case remains controller unit coverage because this deployed-service harness cannot isolate or reset the reconcilers' cached Hub resolution between cases.
- **NetworkClass manager registration and capability propagation:** `it_networkclass_manager_capabilities_test.go` creates the NetworkClass first, then adds fabric and Kubernetes manager registrations and verifies the deployed operator persists their capability intersection. The installer target runs this spec separately with the local operator image so the rest of the service-only suite remains isolated from operator reconciliation.
- **NetworkClass manager readiness:** `it_networkclass_manager_readiness_test.go` covers `PENDING → FAILED` while a manager is missing, recovery to `READY` after its ConfigMap registration appears, and the persisted capability intersection.
- **Provisioning journeys that cross into operators or providers:** Keep them in `tests/e2e/` and exercise those boundaries explicitly.
- **Catalog Items:** `it/` checks creation and update behavior, publication visibility, CLI creation, and the ClusterOrder release image written by Fulfillment. Catalog-backed provisioning journeys that exercise other components remain in the CaaS, VMaaS, BMaaS, and reference E2E suites.

## osac-installer

Touched-area requirements: [component guide](../osac-installer/AGENTS.md#integration-testing).

The `make fulfillment-trust-render-test` Helm contract renders the production
umbrella chart with trust enabled and disabled. It asserts the operator trust
reconciler gate and checks that CA mounts and verified
curl commands remain present in both states. It checks rendered manifests only;
it does not start the hooks or prove a deployed fulfillment endpoint accepts
the certificate. The Kind
`SUITE=fulfillment` target exercises deployed startup and API behavior, subject
to the profile's configured CA and enabled services.

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
- **Fulfillment client TLS:** `cmd/verified_fulfillment_test.go` probes local TLS
  valid/invalid CA and hostname cases, bundle rotation and last-client retention.
  The production render contract covers mounts/arguments; Kind runs with the
  trust gate disabled. Neither proves a deployed fulfillment TLS endpoint.
- **Controller reconciliation, finalizers, status and CRDs:** Drive the public
  reconciler against real API persistence in the existing Controller Suite.
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
| Unit / isolated role transform ([DEV]) | `uv run --group development ansible-playbook collections/ansible_collections/osac/service/roles/hosted_cluster/tests/test.yml` | Executable NodePool definition transforms: distinct NodeSet names and selectors for the same hardware profile, independent replica counts and scale-up | No Kubernetes resources are created. This is not component-integration or deployed AAP/provider coverage; those gaps remain owned by [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843). |
| Component integration | `tests/integration/`; `make test` (creates Kind, runs playbooks, and tears it down) | Ansible roles/playbooks against real Kind APIs, including a second isolated API for storage target routing, plus CRDs, leases, finalizers, and test-runner pod | AAP, OpenStack, KubeVirt/RHACM, and other provider APIs are not generally real; the VMS storage target uses a mock server. |
| Unit | `tests/unit/test_agentless_network_state.py`, `tests/unit/test_agentless_net_network.py`; included by `uv run pytest tests/unit` | UID/CIDR allocation, locked SQLite state, per-UID operations, real reconciliation/verification, and safe command/module failures | `ip`, `iptables`, the network namespace, and AAP are mocked; this does not prove provider execution. |
| Contract | `tests/integration/targets/agentless_net_stub/tasks/baseline.yml`; from `tests/integration/` run `ansible-playbook targets/agentless_net_stub/tasks/baseline.yml -e '@common_vars.yml'` | Fresh Ansible processes run `files/validate_vn_inventory.yml`: real environment lookup, YAML parsing, password rejection, and host registration | AAP, SSH, and Linux provider operations are omitted. |
| Component integration (focused) | A target under `tests/integration/targets/`; run the corresponding playbook from `tests/integration/` | The specific role workflow and its documented fixtures | Only the dependencies declared by that target; inspect its setup and overrides before claiming a real boundary. |
| Contract | No dedicated contract suite; use the qualifying [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) task for AAP/provider coverage | No AAP or provider endpoint is exercised as a contract | The Kind API, mock VMS server, and fixture-driven provider behavior do not prove an AAP or provider contract. |
| E2E | Cross-component OSAC E2E suites | Complete fulfillment and provisioning flows | Depends on the deployed AAP and provider environment. |

### Build/package validation

Run `make execution-environment-build` when the execution-environment definition
or dependencies change. This validates image assembly and packaging; run the
applicable integration tests separately to validate workflow behavior.

### Coverage notes

- **Filters, variable transforms, and isolated plugin logic:** Include invalid input and default handling.
- **AgentlessNet provider state and command helpers:** Unit coverage proves allocation, locking, retry retention, reconciliation/verification with mocked Linux commands, and worker template rendering. The real Ansible inventory contract covers the input boundary; neither suite proves deployed AAP/SSH networking or packet isolation.
- **Ansible roles, workflow tasks, hooks, leases, finalizers, or Kubernetes resources:** The test must exercise the role/playbook through Ansible against Kind.
- **Template publishing TLS:** The `test_cert_validation` play in `collections/ansible_collections/osac/service/roles/publish_templates/tests/test.yml` runs the real role against an untrusted local HTTPS endpoint and asserts certificate rejection before any authenticated HTTP request. The endpoint is a test double; it does not prove a deployed AAP or fulfillment boundary.
- **Execution-environment definition or dependency inputs:** Image success does not prove the workflow boundary.
- **AAP, OpenStack, KubeVirt/RHACM, or provider provisioning:** Kind-only tests with mocks cannot claim provider coverage.
- **Storage-provider behavior:** The mock VMS server validates role logic, not the provider API.
- **Split-cluster Tenant StorageClass routing:** The storage target-routing integration test exercises the Tenant create/delete playbooks against separate management and workload Kind APIs. It does not verify Tenant status resolution or a deployed AAP/provider lifecycle; that cross-component journey remains QE coverage tracked by [OSAC-4850](https://redhat.atlassian.net/browse/OSAC-4850).

### Coverage gaps

AgentlessNet VN create/retry/delete through deployed AAP/SSH and isolation of
overlapping VNs remain QE coverage gaps. No qualifying VN runner is committed;
the user-run deployed checks remain deferred. Unit, Envtest, and inventory
Contract cases belong to [DEV OSAC-5529](https://redhat.atlassian.net/browse/OSAC-5529).
The specific VN QE owner remains unresolved under
[Feature OSAC-3664](https://redhat.atlassian.net/browse/OSAC-3664); track provider
coverage through [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) /
[OSAC-4850](https://redhat.atlassian.net/browse/OSAC-4850).

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
- **Fulfillment client TLS:** `metering-service/cmd/metering-service/verified_fulfillment_test.go` probes a local TLS endpoint with valid and invalid CA/hostname cases and checks bundle rotation and last-client retention. The production render check covers the CA mount and gate; the deployed Watch contract remains with [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843).
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

CaaS sanity retains the single-nodepool lifecycle and fast deletion feedback
scenario. Two-node-set isolation runs in
`caas/regression/test_cluster_node_sets.py`; explicit version resolution and
invalid-version rejection are consolidated in
`caas/regression/test_cluster_version.py`.

Both `test_cluster_create` and `test_cluster_create_with_two_node_sets` use the
same natural teardown assertions. The two-node-set scenario additionally
checks ready worker aggregates, installed Agents, and per-NodeSet
NodePool isolation. Unit regressions additionally cover distinct NodeSets
sharing one BMIT; that same-profile case is not exercised by this deployed
scenario. Run that [QE] E2E with
`uv run pytest -n 0 tests/e2e/caas/regression/test_cluster_node_sets.py::test_cluster_create_with_two_node_sets`;
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
