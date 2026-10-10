# OSAC operator

Kubernetes controllers for OSAC resources, provisioning through AAP, feedback
to the fulfillment service, and the KubeVirt console proxy.

This component is part of the OSAC monorepo, not an isolated project. Its APIs,
generated artifacts, deployment configuration, and runtime behavior may affect
other components. Apply the repository-wide rules in
[`../AGENTS.md`](../AGENTS.md), consider downstream consumers before changing
behavior, and follow the instructions for every affected component.

## Required context

Before changing this component, identify the documents relevant to the change
below, then read and follow them. These documents are authoritative for their
respective areas.

- Component setup and architecture: [`README.md`](README.md)
- Cross-component contracts: [`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md) and [`../docs/CONVENTIONS.md`](../docs/CONVENTIONS.md)
- Controller-specific examples: neighboring files in `internal/controller/`
- API consumer changes: [`../fulfillment-service/AGENTS.md`](../fulfillment-service/AGENTS.md)

## Invariants

- Resource controllers generally own provisioning, finalizers, and lifecycle status; feedback controllers synchronize state with the fulfillment service.
- Every resource controller except `tenant_controller.go` must skip reconciliation when `osac.openshift.io/management-state` is `Unmanaged`.
- `StorageReconciler` is an intentional exception to the dual-controller pattern: it reconciles Tenant storage and does not own a separate CRD.
- Preserve tenant namespace isolation and established predicates when creating or watching resources.
- When changing shared controller behavior, inspect every controller using the same lifecycle.
- `pkg/provisioning`, `pkg/aap`, and `pkg/dispatcher` have external consumers, including the bare-metal fulfillment operator; interface changes are cross-component changes.
- When debugging operators, check for stale `vendor/` dependencies and cached images before rebuilding.
- The fulfillment proto types are the shared top-level `proto/` module, imported as `github.com/osac-project/osac/proto/gen/...`. This component no longer generates its own copy.
- Never put credentials in logs, samples, or manifests.

## Generated files

- After changing `api/v1alpha1/*_types.go`, run `make manifests generate`.
- Then run `make helm-crds` to synchronize `config/crd/` with `charts/operator-crds/`; use `make check-helm-crds` to verify the result.
- Fulfillment proto changes are regenerated once in the shared module: `make -C ../proto generate` (see `proto/AGENTS.md`). Do not regenerate anything proto-related from `osac-operator/`.
- Never hand-edit `config/crd/`, `zz_generated.deepcopy.go`, or `go.sum`; run `go mod tidy` for module changes.

## Integration Testing

See [suite boundaries and coverage gaps](../docs/INTEGRATION-TESTING.md#osac-operator).

| Touched area | Required validation | Command / follow-up |
|---|---|---|
| Pure helpers, validation, or state calculations | Unit | `make test` |
| Fulfillment client CA parsing, TLS verification, or bundle rotation | Unit with local TLS endpoint; component integration for deployment | `make test`, then [installer Kind target](../docs/INTEGRATION-TESTING.md#osac-operator) and `make -C ../osac-installer fulfillment-trust-render-test` |
| Controller reconciliation, finalizers, status, or CRD interactions | Envtest | `make test` |
| Controller deployment, watches (including optional TopoLVM watch), RBAC, console proxy, networking, or Helm wiring | Component integration | Deploy current image/manifests, then `make integration-tests`; [installer alternative](../docs/INTEGRATION-TESTING.md#osac-operator) |
| AAP, dispatcher, provisioning-provider, KubeVirt, or fulfillment boundary | Qualifying Contract or E2E | Use a boundary-specific suite; follow [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) when coverage is missing |
| Generated CRDs or manifests | Envtest plus applicable Kind suite | `make manifests generate helm-crds check-helm-crds`, then the required test command |

Envtest runs via `make test`; Kind tests require the current operator deployment.
The existing Controller Suite (`internal/controller/suite_test.go`) owns all
worker persistence coverage alongside the other operator controllers. Worker
specs are co-located as `internal/controller/baremetalworker_*_test.go`, labelled
`baremetalworker`; there is no separate worker test harness. Dependency doubles
and explicit CR fixture writes live only in `*_test.go`, not importable packages.
Kubernetes/etcd and OSAC CRDs are real; fulfillment responses, ignition HTTP and
external Agent/InfraEnv progression are test-controlled. Keep the minimal
InfraEnv/ClusterDeployment and existing Agent/NodePool test CRDs: the controller
Envtest assertions consume them, not an environment bootstrap.

Worker coverage is owned by **osac-operator [DEV]**:

- Ginkgo Unit specs under `internal/controller/baremetalworker/` use descriptive
  behavior names and cover reservation and identity recovery, one observation per invocation, optimistic status writes,
  authoritative destructive checks, per-NodeSet capacity, retry/cleanup,
  InfraEnv evidence, strict Agent association and CAP-Agent handoff, per-call
  availability classification, fixed attempt/continuous-ready clocks and
  intent-derived counts. `reservation_cleanup_test.go` covers explicit Reserved
  cancellation, both optimistic Create-intent race orderings, attempted/legacy
  empty-List retention, lost acknowledgement recovery and retry-state reset.
  `metrics_test.go` checks the exact two-instance-type
  desired/zero-ready series before reservations without creating missing series
  through metric accessors.
- `baremetalworker_reconciler_test.go` and `baremetalworker_lifecycle_test.go`
  drive public Reconcile through InfraEnv ownership/artifact changes, BMI
  creation/recovery, Agent binding/demotion/handoff, scale-up/down and finalization.
- `baremetalworker_convergence_test.go` preserves R01–R10 persistence and
  fault traces: interrupted/lost Create acknowledgement, delayed List/NotFound/
  outage evidence, conflicts and restart recovery, prerequisite-free progress,
  delayed cleanup and real Agent Delete UID-precondition rejection, selector
  union/ambiguity, stale-ignition classification before UID recording, separate
  order availability, attempt-clock/backfill and continuous readiness, and
  NodeSet-partitioned counts. Reservation cleanup specs add pre-Create
  cancellation/finalization, real create-intent resourceVersion races, restart after persisted intent but
  before the API call, legacy conservatism and lost-ack delayed-List cleanup.
  Each case uses explicit calls; legacy fixture
  convergence is bounded by `16 + 8*N`, not a latency SLA or fallback polling.
- `baremetalworker_tenant_safety_test.go` preserves tenant/owner rejection and
  immutable ownership assertions. The fixture client models scoped-name
  uniqueness but does not establish the real fulfillment/Postgres guarantee.
- `test/integration/baremetalworker_test.go`, run by `make integration-tests`,
  checks the installed service account's worker permissions through real
  Kubernetes authorization. It does not allocate hosts or exercise providers.

Focused validation from `osac-operator/`:

```bash
go test ./internal/controller/baremetalworker -count=1
go test -race ./internal/controller/baremetalworker -count=1
KUBEBUILDER_ASSETS="$PWD/bin/k8s/1.31.0-linux-amd64" \
  go test ./internal/controller -count=1 -ginkgo.label-filter=baremetalworker
```

The complete `make test` runs Unit, the shared Controller Envtest Suite and
Contract checks. `make integration-tests` uses the existing deployed Kind suite;
no extra environment or CaaS-only test command is required. Real fulfillment
API fixture validation, fulfillment-generated ClusterOrders, Postgres
same-name uniqueness/recovery and provider deletion are not established by
these local doubles or the RBAC check. Those **[DEV] Contract** gaps, manager
Agent-watch delivery and real Assisted Service behavior remain under
[OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843); deployed
create/scale/delete, CAP-Agent/drain and hardware journeys remain **[QE] E2E**.
Production archived-Cluster ownership lookup still requires an approved fix
and a dedicated owner/ticket (unresolved); cleanup waits for owner-driven detach
rather than forcing CAP-Agent/Machine hooks or NodePool replicas.

The LVMS envtest lifecycle cases exercise generated LogicalVolume names,
persisted UID-safe resumes, terminating-resource replacement and deletion
through the public Volume reconciler for RWO and RWOP. They use a minimal
TopoLVM CRD and test-controlled status; stale parent snapshots and status
conflicts verify authoritative identity preservation. They do not provision or
mount real devices. The Kind LVMS-disabled case checks that the Volume
controller remains ready without the TopoLVM CRD, not LVMS provisioning or CSI I/O.

## Validation

From `osac-operator/`:

```bash
make fmt
make lint
make test
make helm-lint
make check-helm-crds
make integration-tests       # Requires a pre-existing Kind cluster
```

`make build` also runs the unit-test path. The console proxy integration tests
are under `test/integration/`; E2E suites are under [`../tests/e2e/`](../tests/e2e/).
