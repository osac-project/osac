# bare-metal-fulfillment-operator

Kubernetes operator for managing bare-metal host pools in the OSAC project. Defines BareMetalPool and BareMetalInstance CRDs. Integrates with OpenStack inventory systems and Ironic for power management. Defines Profiles which run Ansible playbooks for additional configuration on the CRDs. Includes Helm charts for deployment.

## Critical Rules

- **Always `make manifests generate`** after modifying CRD types in `api/v1alpha1/*_types.go`
- **Always `make helm-crds`** after regenerating CRDs (or run `make check-helm-crds` to verify sync)
- **Never edit** `config/crd/`, `zz_generated.deepcopy.go` — these are generated
- **Always `go mod tidy`** before committing
- Run `make lint test` before committing

## Dev Environment

**Language**: Go (see `go.mod`) | **Framework**: controller-runtime (Kubebuilder v4) | **Build tool**: Make | **Container tool**: Podman (default) | **Test framework**: Ginkgo v2 + Gomega | **Linter**: golangci-lint (see `Makefile`)

```bash
make build                     # Build manager binary
make test                      # Unit tests (uses envtest for K8s API simulation)
make test-e2e                  # E2E tests (auto-creates/tears down Kind cluster bare-metal-fulfillment-operator-test-e2e)
make setup-test-e2e            # Manually create Kind cluster for e2e
make cleanup-test-e2e          # Manually delete Kind cluster
make lint                      # golangci-lint
make lint-fix                  # golangci-lint run --fix
make lint-config               # Verify golangci-lint config
make fmt && make vet           # Format and vet

make manifests                 # Generate CRD manifests + RBAC via controller-gen
make generate                  # Generate DeepCopy methods via controller-gen
make helm-crds                 # Sync CRDs to operator-crds chart + operator manifests to operator chart
make check-helm-crds           # Runs helm-crds then verifies no drift (may modify generated files)

make run                       # Run controller locally
make install                   # Install CRDs via kustomize
make deploy IMG=<registry>/bare-metal-fulfillment-operator:tag
make undeploy                  # Remove operator from cluster
make uninstall                 # Remove CRDs from cluster

make image-build IMG=<registry>/bare-metal-fulfillment-operator:tag  # Build with Podman
make image-push IMG=<registry>/bare-metal-fulfillment-operator:tag
make docker-buildx PLATFORMS=linux/amd64,linux/arm64  # Multi-arch build
make build-installer IMG=...   # Generate dist/install.yaml (consolidated manifest)

pre-commit run --all-files     # Run all pre-commit hooks
```

## Repository Structure

```text
bare-metal-fulfillment-operator/
├── api/v1alpha1/              # CRD type definitions (BareMetalPool, BareMetalInstance)
├── cmd/
│   ├── main.go                # Operator entry point
│   └── main_test.go           # Entry point tests
├── internal/
│   ├── controller/            # Reconciliation logic (pool + instance controllers)
│   ├── helpers/               # Utility functions
│   ├── inventory/             # BareMetalInstance's host inventory abstraction (pluggable backend interface)
│   ├── management/            # BareMetalInstance's host management (power control)
│   ├── profile/               # Profile configuration handling
│   └── shared/                # Shared utilities
├── charts/
│   ├── operator/              # Operator Helm chart (deployment, RBAC, service)
│   └── operator-crds/         # CRD-only Helm chart
├── config/
│   ├── crd/                   # Generated CRD manifests (DO NOT EDIT)
│   ├── default/               # Kustomize default overlay
│   ├── manager/               # Manager deployment manifest
│   ├── rbac/                  # Generated RBAC rules
│   ├── manifests/             # Additional operator manifests
│   ├── network-policy/        # Network policy definitions
│   ├── prometheus/            # Prometheus monitoring config
│   ├── samples/               # Example CRs
│   └── scorecard/             # Operator scorecard config
├── hack/
│   ├── sync-helm-crds.py      # Sync CRDs to Helm chart
│   └── sync-helm-operator.py  # Sync operator manifests to Helm chart
├── test/
│   ├── crds/                  # Test CRD fixtures (e.g., metal3.io BareMetalHost)
│   ├── e2e/                   # End-to-end tests
│   └── utils/                 # Test utilities
├── Makefile                   # Build, test, lint, deploy, Helm targets
├── go.mod                     # Go 1.26, controller-runtime, gophercloud
└── .golangci.yml              # Linter configuration
```

## Resources Managed

- **BareMetalPool** — defines host sets (type + replica count) with optional profile; phases: Progressing, Ready, Failed, Deleting
- **BareMetalInstance** — individual bare-metal host with inventory allocation and power lifecycle; phases: Allocating, Progressing, Ready, Failed, Deleting

## Architecture

```text
BareMetalPool CR (spec.hostSets: [{hostType, replicas}])
  ↓ (reconcile)
BareMetalPool Controller
  ↓ (creates)
BareMetalInstance CRs (one per host)
  ↓ (reconcile)
BareMetalInstance Controller
  ↓ (allocates via)
Inventory Client (OpenStack or Metal3)
  ↓ (manages power via)
Management Client (Ironic)
```

### Key Subsystems

| Package | Purpose |
|---------|---------|
| `internal/controller/` | Pool and instance reconciliation (lifecycle, finalizers, status updates) |
| `internal/inventory/` | Host allocation abstraction with pluggable backend interface (OpenStack, Metal3) and in-memory locking |
| `internal/management/` | Power control via OpenStack Ironic integration |
| `internal/profile/` | Profile configuration and parameter injection for Ansible workflows |
| `internal/shared/` | Shared utilities across controllers |

### Helm Charts

Two charts in `charts/`:
- **operator-crds** — CRD definitions only, for installing CRDs independently
- **operator** — operator deployment, RBAC, service account, metrics

CRDs must stay in sync: after `make manifests`, run `make helm-crds` (uses `hack/sync-helm-crds.py` and `hack/sync-helm-operator.py`). CI enforces sync via `make check-helm-crds`.

## CI Workflows

- **build-bmf-image.yaml**: Runs tests, builds + pushes container image and manifests
- **helm-lint.yaml** (repo root, matrixed across components): Checks CRD sync (`make check-helm-crds`) and lints Helm charts
- **pre-commit.yaml** (repo root, shared across components): pre-commit hooks + golangci-lint + gitleaks PR-diff secret scan on PRs
- **publish-charts.yaml** (repo root, shared across components): packages and pushes Helm charts to GHCR on version tags
- **e2e-bmaas-full-install.yml** (repo root, shared with fulfillment-service/osac-operator/osac-aap): builds all four components and runs E2E tests in a BMaaS environment

## Code Quality

- **golangci-lint** (see `Makefile`) with dupl, errcheck, ginkgolinter, goconst, gocyclo, govet, ineffassign, lll, misspell, prealloc, revive, staticcheck, unconvert, unused (see `.golangci.yml`)
- **Pre-commit hooks**: trailing-whitespace, check-merge-conflict, end-of-file-fixer, check-added-large-files, check-case-conflict, check-json, check-symlinks, detect-private-key, yamllint --strict (excludes `config/`), golangci-lint run --fix
- **Tests**: Ginkgo v2 + Gomega with envtest for unit tests; Kind cluster integration tests for e2e (`test/e2e/`)
- **Test coverage**: Unit tests generate `cover.out`

## Container Security

- **Base images**: registry.access.redhat.com/ubi10/go-toolset:1.26 (builder), ubi10-minimal:10.2 (runtime)
- **Multi-stage build**: CGO_ENABLED=0, runs as non-root user 1001
- **Default registry**: ghcr.io/osac-project/bare-metal-fulfillment-operator:latest

## Code Generation Flow

1. Modify `api/v1alpha1/*_types.go` (CRD types)
2. Run `make manifests generate` → generates CRDs in `config/crd/bases/` + DeepCopy methods
3. Run `make helm-crds` → syncs CRDs to `charts/operator-crds/templates/` and operator manifests to `charts/operator/templates/`
4. CI enforces sync via `make check-helm-crds` on PRs

## Test Structure

- `internal/controller/*_test.go` — controller unit tests with envtest
- `internal/controller/*_integration_test.go` — Metal3 integration tests
- `test/e2e/` — end-to-end tests with real Kind cluster (named `bare-metal-fulfillment-operator-test-e2e`)
- `test/utils/` — test utilities
- `test/crds/` — external CRDs (metal3.io_baremetalhosts.yaml)
- **ENVTEST_K8S_VERSION**: Auto-detected from k8s.io/api version in go.mod (e.g., 1.36)
