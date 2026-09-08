# Bare-metal fulfillment operator

Kubernetes controllers for `BareMetalPool` and `BareMetalInstance` resources,
including inventory allocation, power management, and profile workflows.

This component is part of the OSAC monorepo, not an isolated project. Its APIs,
generated artifacts, deployment configuration, and runtime behavior may affect
other components. Apply the repository-wide rules in
[`../AGENTS.md`](../AGENTS.md), consider downstream consumers before changing
behavior, and follow the instructions for every affected component.

## Required context

Before changing this component, identify the documents relevant to the change
below, then read and follow them. These documents are authoritative for their
respective areas.

- Component setup: [`README.md`](README.md)
- Controller lifecycle examples: `internal/controller/`
- Cross-component deployment contracts: [`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md)

## Invariants

- Preserve the pool-to-instance ownership, finalizer, provisioning, and status lifecycle.
- Keep inventory allocation and power-management abstractions separate.
- Preserve tenant isolation metadata on tenant-scoped resources and avoid credentials in logs or samples.
- Check the sibling controller when changing shared reconciliation behavior.

## Generated files

- After changing `api/v1alpha1/*_types.go`, run `make manifests generate`.
- Then run `make helm-crds` to synchronize the CRD and operator Helm charts; use `make check-helm-crds` to verify synchronization.
- Never hand-edit `config/crd/` or `zz_generated.deepcopy.go`.
- After dependency changes, run `go mod tidy` and commit the resulting `go.mod` and `go.sum` changes.

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
