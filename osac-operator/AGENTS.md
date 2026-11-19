# OSAC operator

Never edit this file except for minor corrections. Do not append new content.

Kubernetes controllers for OSAC resources, provisioning through AAP, feedback
to the fulfillment service, and the KubeVirt console proxy.

## Required context

Read the applicable references before changing behavior:

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
- `pkg/provisioning` and `pkg/aap` have external consumers, including the bare-metal fulfillment operator; interface changes are cross-component changes.
- Networking manager tasks run only through `internal/networktask` (see its `doc.go`): one generic job template per task, `status.jobs` entries, typed result validation.
- When debugging operators, check for stale `vendor/` dependencies and cached images before rebuilding.
- Worker recovery retains attempted or uncertain Create intent, including
  legacy state and empty List results; do not equate missing evidence with
  deletion. Only an invocation entering with explicit Reserved intent may
  re-arm its own definitively rejected Create; later rejection cannot erase
  earlier uncertainty.
- Preserve optimistic worker checkpoints, authoritative identity/tenant/owner
  checks before destruction, UID-safe resumes and strict Agent association.
  Keep per-NodeSet capacity, retry clocks and continuous-readiness accounting.
- Observe once per worker invocation. Rejected-create checkpoints atomically
  test UID and the complete selected worker without overwriting unrelated
  workers, conditions or metadata; do not erase prior uncertain outcomes.
- Cleanup waits for owner-driven detach; never force CAP-Agent/Machine hooks
  or NodePool replicas. Archived-Cluster ownership lookup remains unresolved.
- Failed ExternalIP release retains the object, address and finalizer for retry;
  dispatch allocation with the Fulfillment API ID.
- Reject networking results when dependency identity or tenant snapshots change.
  ComputeInstance feedback preserves disk correlation/registration lists while
  excluding them from outgoing update masks.
- LVMS resumes preserve authoritative volume identity across stale snapshots,
  status conflicts and terminating-resource replacement; absence of TopoLVM
  must not prevent the Volume controller from becoming ready.

## Generated files

- After changing `api/v1alpha1/*_types.go`, run `make manifests generate`.
- Then run `make helm-crds` to synchronize `config/crd/` with `charts/operator-crds/`; use `make check-helm-crds` to verify the result.
- From `osac-operator/`, regenerate Fulfillment protos through
  `make -C ../proto generate` (see [proto instructions](../proto/AGENTS.md)).
  Do not run a separate component-local protobuf generator.
- Never hand-edit `config/crd/`, `zz_generated.deepcopy.go`, or `go.sum`; run `go mod tidy` for module changes.

<a id="integration-testing"></a>

## Validation

See [suite boundaries and coverage gaps](../docs/INTEGRATION-TESTING.md#osac-operator).

Shared goldens bind physical NetworkAttachment apply/delete/`query_dhcp_lease`,
the `dhcp_lease` result and ClusterOrder ingress reservation. Keep
`internal/controller/networking_wire_golden_test.go`,
`internal/networktask/wire_golden_test.go`, AAP's
`tests/unit/test_network_task_wire_contract.py` and
`../tests/contract/network-task` synchronized in one PR; run `make test` from
`osac-operator/`. DHCP validation uses the interface MAC and physical subnet
range. Overlay delete/detach, other deletes, SecurityGroup, FabricDomain and
ingress release remain unbound; see [wire boundaries](../docs/testing/osac-operator.md#coverage-boundaries).

### Local build and checks

Run from `osac-operator/`, using Go from `go.mod` and Helm for chart checks:

```bash
make fmt
make vet
make lint
make helm-lint
make check-helm-crds
make test
make build
```

`make test` prepares Envtest and regenerates manifests/DeepCopy code; review
generated changes. `make build` also runs that unit-test path before building
the manager and console proxy, so it is not a compile-only shortcut.

### Select coverage by boundary

Controller regressions reuse `internal/controller/suite_test.go` and real
Envtest API-server writes for status/version behavior. Fake updates do not
establish conflicts or watch delivery.

All commands below also run from `osac-operator/`. Integration targets require
the current image/manifests deployed into an explicitly selected Kind cluster.

| Touched area | Required tier and command |
|---|---|
| Go/Helm changes | Static: `make fmt lint helm-lint check-helm-crds` |
| Helpers, reconciliation, finalizers, status, CRDs, worker recovery or LVMS | Unit/Envtest/Contract: `make test` |
| Networking task input/result contracts | `make test`; synchronize shared `../tests/contract/network-task` goldens with AAP in the same PR; see `internal/networktask/doc.go` for the bound task surface |
| Deployment, watches, RBAC, console proxy, networking or Helm wiring | Deploy current image/manifests, then `make integration-tests` against Kind |
| Provider-disabled ComputeInstance launches | `make test` and `make -C ../osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=operator` (standalone integration target does not select this phase) |
| ComputeInstance disk handoff through Fulfillment/operator | `make -C ../osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=compute-storage` |
| Fulfillment TLS and trust rotation | `make test`, Kind integration, and `make -C ../osac-installer fulfillment-trust-render-test` |
| Generated CRDs/manifests | `make manifests generate helm-crds check-helm-crds`, then applicable Envtest/Kind suite |
| AAP, provisioning, KubeVirt or Fulfillment boundary | Qualifying real-boundary Contract/E2E; report missing coverage via the linked suite guide |

Worker persistence uses the shared Controller Envtest Suite, labelled
`baremetalworker`; keep dependency doubles and explicit CR fixture writes in
`*_test.go`, not importable packages. Kubernetes/etcd and OSAC CRDs are real;
Fulfillment responses, ignition HTTP and Agent/InfraEnv progression are
controlled. Keep the minimal InfraEnv/ClusterDeployment and Agent/NodePool CRDs
consumed by the suite.

For focused worker controller coverage, with `KUBEBUILDER_ASSETS` pointing to
the Envtest binaries prepared by the Makefile:

```bash
go test ./internal/controller -count=1 -ginkgo.label-filter=baremetalworker
```

These focused pure-helper checks do not replace controller Envtest:

```bash
go test ./internal/controller/baremetalworker -count=1
go test -race ./internal/controller/baremetalworker -count=1
```

Local doubles and Kind worker RBAC checks do not establish real Fulfillment/
Postgres uniqueness or recovery, Agent-watch delivery, Assisted Service or
provider deletion. These remain [Contract gaps](../docs/INTEGRATION-TESTING.md#osac-operator);
deployed lifecycle, CAP-Agent/drain and hardware checks are QE E2E.
LVMS fixture CRDs/status do not prove device provisioning, mounting or CSI I/O.

The console proxy integration tests are under `test/integration/`; E2E suites
are under [`../tests/e2e/`](../tests/e2e/).
