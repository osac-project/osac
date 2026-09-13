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

## osac-operator

Touched-area requirements: [component guide](../osac-operator/AGENTS.md#integration-testing).

### Test tiers and commands

| Tier | Location / command | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | Co-located `*_test.go`; `make test` | Controller helpers, validation, provisioning state logic | External APIs and providers are mocked. |
| Envtest | Controller tests under `internal/controller/*_envtest_test.go`; run by `make test` | Kubernetes API server, etcd, loaded CRDs, and in-process reconciliation | The controller is not deployed to Kind; provisioning uses controllable or noop providers. |
| Component integration | `test/integration/`; deploy the current operator into a Kind cluster, then run `make integration-tests` | Installed operator, Kubernetes API, CRDs, controller-manager, console proxy, and networking behavior | AAP/provider provisioning and external infrastructure are not real in the current suite; some tests remove finalizers to bypass that boundary. |
| Component integration (CI) | `make -C osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=operator` (from repository root) | The thin Kind deployment used by the PR workflow | The same external-provider limitations as the local Kind suite. |
| Contract | `test/contract/`; included by `make test` | Helm chart RBAC templates against the operator permission contract | No deployed operator or external provider is exercised. |
| E2E | `../tests/e2e/` | Cross-component fulfillment journeys | Depends on the deployed test environment and its configured providers. |

### Coverage notes

- **Pure helpers, validation, or state calculations:** Add error and edge-case coverage.
- **Controller reconciliation, finalizers, status, or CRD interactions:** The envtest suite must exercise the changed lifecycle through the public reconciler behavior.
- **Controller deployment, watches, RBAC, console proxy, networking, or Helm wiring:** Unit/envtest coverage alone does not prove deployed wiring.
- **AAP, dispatcher, provisioning-provider, KubeVirt, or fulfillment boundary:** A controllable provider in envtest is not coverage of the real provider boundary.
- **Generated CRDs or manifests:** Do not hand-edit generated output.

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
| E2E | `../tests/e2e/` storage flows when enabled | Deployed storage lifecycle through OSAC and its configured backend | Depends on the selected storage tier and environment gates. |

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
