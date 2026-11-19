# Bare-metal fulfillment operator

Never edit this file except for minor corrections. Do not append new content.

Kubernetes controllers for `BareMetalPool` and `BareMetalInstance` resources,
including inventory allocation, power management, and profile workflows.

## Required context

Read the applicable references before changing behavior:

- Component setup: [`README.md`](README.md)
- Controller lifecycle examples: `internal/controller/`
- Cross-component deployment contracts: [`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md)

## Invariants

- Preserve the pool-to-instance ownership, finalizer, provisioning, and status lifecycle.
- Keep `internal/inventory/` allocation separate from `internal/management/`
  power-management abstractions; controllers use their client interfaces.
- BareMetalInstance host teardown waits while the OSAC networking cleanup
  finalizer is present. Leave ExternalIP and ExternalIPAttachment untouched;
  BMF owns NetworkAttachment teardown after that finalizer clears.

## Generated files

- After changing `api/v1alpha1/*_types.go`, run `make manifests generate`.
- Then run `make helm-crds` to synchronize the CRD and operator Helm charts; use `make check-helm-crds` to verify synchronization.
- Never hand-edit `config/crd/` or `zz_generated.deepcopy.go`.
- After dependency changes, run `go mod tidy` and commit the resulting `go.mod` and `go.sum` changes.

## Integration Testing

See [suite boundaries and coverage gaps](../docs/INTEGRATION-TESTING.md#bare-metal-fulfillment-operator).

| Touched area | Required validation | Command / follow-up |
|---|---|---|
| Pure inventory, selection, validation, or client logic | Unit | `make test` |
| Reconciliation, finalizers, allocation, or status transitions | Envtest | `make test` |
| Controller deployment, CRDs, pool flows, or Kubernetes wiring | Component integration | Deploy current image/manifests, then `make integration-tests`; [installer alternative](../docs/INTEGRATION-TESTING.md#bare-metal-fulfillment-operator) |
| Networking teardown ownership | Kind component integration | [Isolated installer boundary](../docs/INTEGRATION-TESTING.md#net-clean-08-deployed-bmf-ownership-boundary) |
| Metal3, BCM, Ironic, BMC, power, or hardware semantics | Contract or real-provider integration | Follow the owning [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) task |
| Generated CRDs or Helm CRDs | Envtest plus Kind | `make manifests generate helm-crds check-helm-crds`, then the required test command |

## Validation

### Local build and checks

From `bare-metal-fulfillment-operator/`, use Go from `go.mod` and Helm for chart
checks. `make test` obtains Envtest assets and regenerates manifests/DeepCopy
code; review any resulting diff.

```bash
make fmt && make vet
make lint
make test
make helm-lint
make check-helm-crds
make build
```

### Deployed checks

Run `make integration-tests` from the same directory after deploying the current
image and CRDs into a selected Kind cluster. The Ginkgo suite lives under
`test/integration/`; it does not supply real hardware/provider behavior.
Installer orchestration is owned by
[`../osac-installer/AGENTS.md`](../osac-installer/AGENTS.md).
