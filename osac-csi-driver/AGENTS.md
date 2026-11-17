# OSAC CSI driver

Never edit this file except for minor corrections. Do not append new content.

CSI meta-driver that delegates fulfillment-owned volume lifecycle to the
fulfillment service and routes vendor operations through CSI plugins.

## Required context

Read the applicable references before changing behavior:

- Component setup: [`README.md`](README.md)
- Vendor prerequisites: [`charts/csi-backends/README.md`](charts/csi-backends/README.md)
- Fulfillment API changes: [`../fulfillment-service/AGENTS.md`](../fulfillment-service/AGENTS.md)

## Invariants

- `osac.backend` is the routing key for vendor controller and node operations; preserve its meaning across both plugins.
- Keep fulfillment-owned create/delete lifecycle separate from vendor-proxied attach, detach, and mount operations.
- A vendor-controller endpoint of `none` means attach and detach are intentional no-ops.
- Fulfillment OAuth client ID, secret file, and issuer URL are required together.

## Integration Testing

See [suite boundaries and coverage gaps](../docs/INTEGRATION-TESTING.md#osac-csi-driver).

| Touched area | Required validation | Command / follow-up |
|---|---|---|
| Request/response mapping, validation, or driver helpers | Unit | `make test` |
| CSI controller/node routing or CSI protocol behavior | Unit (CSI sanity) | `make test` |
| Fulfillment private Volume API contract | Contract or E2E | Real Volume lifecycle coverage remains a [tracked gap](https://redhat.atlassian.net/browse/OSAC-4845) |
| Vendor attach, detach, mount, or storage lifecycle | Component integration or E2E | Required suite is currently unavailable; track [OSAC-4845](https://redhat.atlassian.net/browse/OSAC-4845) |
| Helm/deployment changes | Helm validation plus applicable E2E | `helm lint charts/csi-driver`; `helm lint charts/csi-backends` |

CSI sanity uses a fake vendor and fulfillment stub; it does not cover a real backend.

## Generated files

- Fulfillment types come from shared `../proto/gen/`; follow [proto instructions](../proto/AGENTS.md).
- Keep dependency changes in `go.mod`/`go.sum` synchronized through `go mod tidy`.

## Validation

### Local build and checks

From `osac-csi-driver/`, use Go from `go.mod` and Helm for chart checks:

```bash
make fmt
make vet
make lint
make test                  # Unit and CSI sanity tests
make build
helm lint charts/csi-driver
helm lint charts/csi-backends
```

### Container and deployed checks

- For container/deployment inputs, run
  `make image-build IMG=<registry>/osac-csi-driver:<tag>` from the same directory.
  The Makefile supplies the monorepo root context.
- For [E2E suites](../tests/e2e/), select the real storage backend and its
  deployment prerequisites; CSI sanity does not prove attach/detach/mount behavior.
