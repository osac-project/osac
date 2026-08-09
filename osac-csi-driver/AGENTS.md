# osac-csi-driver

CSI meta-driver that presents a single CSI identity (`csi.osac.openshift.io`) to Kubernetes and routes storage requests to vendor-specific CSI drivers (NetApp Trident, VAST Data, Pure Storage) based on storage tier resolution from the OSAC [fulfillment service](../fulfillment-service/). Runs as two plugins: a controller (Deployment) handling volume lifecycle via the fulfillment service, and a node plugin (DaemonSet) routing mount operations to vendor CSI sockets.

## Critical Rules

- **Never edit** `go.sum` — regenerate with `go mod tidy`
- **Always `go mod tidy`** before committing
- **Commit message format**: `OSAC-XXXXX: description of change` (Red Hat Jira key prefix)
- **AI attribution**: Use `Assisted-by: Claude Code <noreply@anthropic.com>` (not Co-Authored-By)
- **Sign-off required**: `git commit -s` for DCO compliance
- **Always `make lint`** after changing any Go code — fix all issues before proceeding
- Run `make lint test` before committing
- **Container build context**: The `Containerfile` must be built from the **mono-repo root** (not `osac-csi-driver/`), because it needs `go.work` and sibling module `go.mod` files

## Dev Environment

**Language**: Go 1.26.3 | **Build tool**: Make | **Container tool**: Podman (default) | **Test framework**: standard Go `testing` + [csi-test/v5](https://github.com/kubernetes-csi/csi-test) sanity suite | **Linter**: golangci-lint v2.12.1

```bash
make build                     # Build binary to bin/osac-csi-driver (CGO_ENABLED=0)
make test                      # Unit tests + CSI sanity tests (go test ./... with coverage)
make lint                      # golangci-lint (auto-downloads to bin/)
make lint-fix                  # golangci-lint --fix
make fmt                       # go fmt
make vet                       # go vet
make clean                     # Remove bin/ and cover.out

make image-build IMG=<registry>/osac-csi-driver:tag   # Build with Podman (from mono-repo root)
make image-push IMG=<registry>/osac-csi-driver:tag
```

## Repository Structure

```text
osac-csi-driver/
├── cmd/
│   └── osac-csi-driver/
│       └── main.go              # Entry point, flag parsing, driver bootstrap
├── pkg/
│   ├── driver/
│   │   ├── driver.go            # gRPC server lifecycle, logging interceptor
│   │   ├── controller.go        # ControllerServer (CreateVolume, DeleteVolume, Publish/Unpublish)
│   │   ├── controller_test.go   # Unit tests (mock-based, standard testing pkg)
│   │   ├── identity.go          # IdentityServer (GetPluginInfo, Probe, capabilities)
│   │   └── node.go              # NodeServer (Stage/Unstage, Publish/Unpublish, vendor proxy)
│   ├── fulfillment/
│   │   ├── volume.go            # VolumeClient interface, VolumeInfo, CreateVolumeParams
│   │   ├── controlplane.go      # ControlPlaneClient interface (Publish/Unpublish)
│   │   └── stubs.go             # In-memory stubs for development (no fulfillment-service needed)
│   └── proxy/
│       └── proxy.go             # gRPC connection manager for vendor CSI sockets (lazy, cached)
├── test/
│   └── sanity/
│       ├── sanity_test.go       # CSI sanity test suite (kubernetes-csi/csi-test/v5)
│       ├── fakevendor_test.go   # Fake vendor CSI driver for sanity tests
│       └── testdata/
│           └── secrets.yaml     # Test secrets
├── charts/
│   ├── csi-driver/              # Main Helm chart (controller Deployment + node DaemonSet)
│   └── csi-backends/            # Vendor CSI controller Deployments (Trident, VAST, Pure)
├── Makefile
├── .golangci.yml                # golangci-lint v2 config
├── Containerfile                # Multi-stage build (UBI10, non-root user 1001)
└── go.mod                       # Go 1.26.3
```

## Architecture

```text
Kubernetes PVC
  ↓ (CSI CreateVolume / DeleteVolume)
ControllerServer ──→ fulfillment-service Volume API
                       (policy check, tier resolution, vendor dispatch)
  ↓ (CSI ControllerPublishVolume / ControllerUnpublishVolume)
ControllerServer ──→ fulfillment-service ControlPlane API

  ↓ (CSI NodeStageVolume / NodePublishVolume)
NodeServer ──→ proxy Manager ──→ vendor CSI node plugin
               (routed by "osac.backend" volume context key)
```

The controller plugin **never talks to vendor CSI drivers directly** — all volume lifecycle and publish/unpublish operations go through the fulfillment-service, which handles policy checks, storage tier resolution, and vendor dispatch. Only the node plugin communicates directly with vendor CSI sockets for mount operations.

### Key Subsystems

| Package | Purpose |
|---------|---------|
| `pkg/driver/` | CSI gRPC server (Identity, Controller, Node) implementing the meta-driver pattern |
| `pkg/fulfillment/` | Interfaces and stubs for fulfillment-service volume and control plane operations |
| `pkg/proxy/` | Lazy gRPC connection manager for vendor CSI sockets (unix + TCP) |

### Volume Context Keys

The controller sets these keys in volume context at creation time, consumed by the node plugin:

| Key | Purpose |
|-----|---------|
| `osac.backend` | Routing key — identifies which vendor CSI socket to proxy to |
| `osac.volume-id` | Vendor-side volume ID |
| `osac.protocol` | Storage protocol (e.g., `nfs`) |

### Dual-Plugin Topology

- **Controller plugin** (Deployment): `CreateVolume`, `DeleteVolume`, `ControllerPublishVolume`, `ControllerUnpublishVolume` — delegates all operations to the fulfillment-service volume and control plane APIs
- **Node plugin** (DaemonSet): `NodeStageVolume`, `NodePublishVolume`, `NodeUnstageVolume`, `NodeUnpublishVolume` — routes to vendor node sockets via `osac.backend`; maintains in-memory `volumeBackends` map to track which vendor handled each volume's stage

### Stub Mode

The real gRPC fulfillment client is not yet implemented. If `--fulfillment-endpoint` is not set, the driver uses in-memory stubs (`VolumeStub`, `ControlPlaneStub`) for development. Setting `--fulfillment-endpoint` currently exits with an error.

## Configuration

CLI flags are defined in `cmd/osac-csi-driver/main.go`. Run `go run ./cmd/osac-csi-driver/ --help` for the full list. See `charts/csi-driver/templates/controller-deployment.yaml` and `charts/csi-driver/templates/node-daemonset.yaml` for how flags are configured in production.

## Testing

- **Unit tests**: `pkg/driver/controller_test.go` — standard Go `testing` package with hand-written mocks; covers CreateVolume, DeleteVolume, Publish/Unpublish, polling, idempotency, error handling
- **Sanity tests**: `test/sanity/` — kubernetes-csi/csi-test/v5 compliance suite against the real driver + a fake vendor backend
- **E2E tests**: None in this component (E2E tests live in `osac-test-infra`)
- Run all: `make test`

## Helm Charts

Two charts in `charts/`:

- **csi-driver** — Main chart: CSIDriver resource, controller Deployment (with csi-provisioner + csi-attacher sidecars), node DaemonSet (with csi-node-driver-registrar), RBAC, namespace `osac-csi`. Includes optional vendor node DaemonSets (`vendors.<name>.enabled`)
- **csi-backends** — Vendor CSI controller Deployments (Trident, VAST, Pure) in namespace `osac-csi-backends`. Each vendor is conditionally deployed. See `charts/csi-backends/README.md` for vendor-specific prerequisite setup (secrets, configs)

## CI Workflows

- **publish-csi-driver-image.yaml**: Builds and pushes container image on PRs, main pushes, and `osac-csi-driver/v*` tags
- **helm-lint.yaml** (repo root, matrixed): Lints both `csi-driver` and `csi-backends` charts
- **pre-commit.yaml** (repo root): Pre-commit hooks + golangci-lint + gitleaks
- **publish-charts.yaml** (repo root): Packages and pushes both Helm charts to GHCR OCI registry on image build success

## Code Quality

- **golangci-lint** v2 (`.golangci.yml`): dupl, errcheck, goconst, gocyclo, govet, ineffassign, lll, misspell, prealloc, revive, staticcheck, unconvert, unused
- Formatters: gofmt, goimports
- Pre-commit hooks run from the mono-repo root (no component-local pre-commit config)

## Container Security

- **Base images**: `registry.access.redhat.com/ubi10/go-toolset:1.26` (builder), `ubi10-minimal:10.2` (runtime)
- **Multi-stage build**: CGO_ENABLED=0, runs as non-root user 1001
- **Default registry**: `ghcr.io/osac-project/osac-csi-driver:latest`
