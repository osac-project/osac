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
| Controller reconciliation, finalizers, status, or CRD interactions | Envtest | `make test` |
| Controller deployment, watches (including optional TopoLVM watch), RBAC, console proxy, networking, or Helm wiring | Component integration | Deploy current image/manifests, then `make integration-tests`; [installer alternative](../docs/INTEGRATION-TESTING.md#osac-operator) |
| AAP, dispatcher, provisioning-provider, KubeVirt, or fulfillment boundary | Qualifying Contract or E2E | Use a boundary-specific suite; follow [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) when coverage is missing |
| Generated CRDs or manifests | Envtest plus applicable Kind suite | `make manifests generate helm-crds check-helm-crds`, then the required test command |

Envtest runs via `make test`; Kind tests require the current operator deployment.
The LVMS envtest lifecycle cases exercise generated LogicalVolume names,
persisted UID-safe resumes, terminating-resource replacement, and deletion
through the public Volume reconciler for RWO and RWOP. They use a minimal
TopoLVM CRD and simulated status; they also inject stale parent snapshots to
verify authoritative reads preserve the recorded LogicalVolume identity.
Status-conflict cases verify newer vendor context, deletion and replacement
UIDs are not overwritten. These tests do not provision or mount real devices.
The bare-metal worker unit tests cover bounded opaque names, authoritative
status reservations from stale snapshots, retry backoff, and per-NodeSet
capacity selection, including distinct NodeSets sharing a hardware profile. The acceptance envtest suite covers persisted BMI name/ID
references, interrupted creation, existing-worker recovery, independent request
scaling/reordering, recovery of unrecorded BMI IDs before finalization, and
retention of workers/finalizers when the recorded BMI name is missing.
Kubernetes/etcd and the generated CRDs are real; fulfillment and Agents are
simulated, so these cases do not prove deployed BMaaS or hardware behavior.
Agent reconciliation unit tests cover phase convergence/protected workers,
registration timeout, binding conflicts and isolation, interrupted-status recovery,
and transition event/metric counts. The acceptance envtest suite drives public
`Reconcile` manually to verify persisted Ready demotion after Agent disappearance
or Installed-condition changes, bound-Agent status repair without another BMI or
Agent patch, protected Failed/Unbinding/Deleting entries, and ambiguous-match
refusal. These cases do not test manager watch delivery or deployed Assisted Service.
Unified worker convergence is covered by `worker_reconcile_test.go`,
`worker_capacity_test.go`, `worker_teardown_test.go`, `worker_projection_test.go`,
and the BMI identity suites. Unit fault injection covers shared read budgets,
unknown versus absent references, latest-status conflict guards, concurrent
reference interruption, capacity interruption, and one fresh ownership/existence Get per BMI
delete attempt, and finalizer retention for concurrently appended workers.
`acceptance/worker_reconcile_test.go` drives public `Reconcile`
through real status persistence and optimistic-lock conflicts: successful-create
ID write interruption, List omission/NotFound/outages, repair before prerequisite
gates, stale-failure write rejection before InfraEnv UID recording, merged
aggregate counts, and identity-only finalization recovery. Assertions follow
explicit reconciliation calls, not fallback polling. R01 Unit cases and the
R01-E1/E2 acceptance specs additionally assert separate finalizer/repair/reservation
and single-Create checkpoints, stable names across stale parent snapshots,
independent NodeSets sharing one instance type, waiting-retry selection, a
single failed-worker retry Delete (including errors), and no requeue from a
no-op retention ordering. Stale-ignition repair persists without advancing the
InfraEnv UID or deleting the newly failed worker in the same invocation. Legacy fixtures drive finite
multi-reconcile sequences, assert durable progress at each allocation checkpoint,
and propagate errors/backoff rather than polling through them. BMI creates
persist their returned ID before the invocation ends; subsequent invocations
observe all persisted references anew. R01-E2 also injects a successful Create
with a lost transport acknowledgement, restarts the reconciler, hides the BMI
from observation and an AlreadyExists re-list, then recovers the same reserved
name/ID without another successful allocation. Unsafe foreign, ambiguous and
deleting recovery candidates are rejected without changing reservations;
recorded deleting IDs and finalization recovery remain observable. The fake
models tenant/project/name uniqueness independently of ID, not the real DB.
The connected R01-C1 case in `test/integration/caas/worker_test.go` is implemented
and compile-checked, but live execution is blocked until the marked sim backend
and its checkout-specific connection configuration are available. It delegates
Create to the real API before losing one acknowledgement, restarts, forces a
real AlreadyExists re-list, and counts persisted owned incarnations per reserved
name. See the connected suite README for its execution prerequisites and limits.
The Unit/Envtest cases above are [DEV] work, not fulfillment wire,
manager-watch, or deployed-provider coverage; those
remaining boundaries belong to
[OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843).
The Kind suite's LVMS-disabled case verifies the Volume controller remains
ready without the TopoLVM `LogicalVolume` CRD; it does not exercise LVMS
provisioning or the CSI data path.

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
