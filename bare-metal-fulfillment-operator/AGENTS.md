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
- Keep inventory allocation and power-management abstractions separate.
- Preserve tenant isolation metadata on tenant-scoped resources and avoid credentials in logs or samples.
- BareMetalInstance host teardown waits while the OSAC networking cleanup
  finalizer is present. Leave ExternalIP and ExternalIPAttachment untouched.

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

Kind tests require the current operator deployment and simulate provider transitions.

## Validation

From `bare-metal-fulfillment-operator/`:

```bash
make fmt && make vet
make lint
make test
make helm-lint
make check-helm-crds
make integration-tests       # Requires a pre-existing Kind cluster
```

The integration tests are under `test/integration/`. Installer orchestration,
when needed, is owned by [`../osac-installer/AGENTS.md`](../osac-installer/AGENTS.md).
