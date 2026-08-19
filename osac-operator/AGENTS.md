# AGENTS.md

## Project Overview

OSAC operator is a Kubernetes operator that reconciles infrastructure resources for the [OSAC](https://github.com/osac-project) project. It integrates with the [fulfillment service](../fulfillment-service/), a sibling component in this repository, and Ansible Automation Platform to provision OpenShift clusters and compute instances with networking. Includes a console proxy (aggregated API server) for KubeVirt VM console/VNC access via WebSocket.

### Resources Managed

- **ClusterOrder** (`cord`) — OpenShift clusters via Hosted Control Planes
- **ComputeInstance** (`ci`) — virtual machines via KubeVirt
- **Tenant** — namespace and OVN-Kubernetes UserDefinedNetwork for isolation
- **VirtualNetwork** (`vnet`) — cloud VPC with IPv4/IPv6 CIDR blocks
- **Subnet** (`subnet`) — subnet within a VirtualNetwork
- **SecurityGroup** (`sg`) — network security rules
- **ExternalIPPool** (`extippool`) — external IP pool
- **ExternalIP** — external IP allocated from ExternalIPPool
- **ExternalIPAttachment** — attachment of ExternalIP to ComputeInstance
- **NATGateway** — outbound SNAT for a VirtualNetwork

## Critical Rules

- **Always `make manifests generate`** after modifying CRD types in `api/v1alpha1/*_types.go`
- **Always `make helm-crds`** after regenerating CRDs (runs `make manifests` + `hack/sync-helm-crds.py`) to sync Helm charts (or run `make check-helm-crds` to verify sync)
- **Never edit** `config/crd/`, `zz_generated.deepcopy.go`, `internal/api/`, or `go.sum` — all generated (regenerate `go.sum` with `go mod tidy`)
- **Always `buf generate`** after updating the module version in `buf.gen.yaml` (check the file for the current pinned version)
- **Commit message format**: `OSAC-XXXXX: description of change` (Red Hat Jira key prefix)
- **AI attribution**: Use `Assisted-by: Claude Code <noreply@anthropic.com>` (not Co-Authored-By)
- **Sign-off required**: `git commit -s` for DCO compliance
- **Always `make lint`** after changing any Go code — fix all issues before proceeding
- Run `make lint test` before committing

## Development Commands

```bash
# Build and test (requires Go 1.26.3)
make build                    # Build manager + console-proxy (runs 'make test' first, then builds)
make test                     # Unit tests only (excludes e2e)
make test-integration         # Runs test + test-kustomize + test-smoke
make test-kustomize           # Validate all kustomization.yaml configs (catches missing files)
make test-smoke               # Create kind cluster 'osac-test', apply CRDs + samples, verify ComputeInstance
make test-integration-kind    # Go integration tests against a running kind cluster
make lint                     # golangci-lint v2.12.1 (strict — always run before commit)
make fmt                      # go fmt + goimports
make vet                      # go vet

# Code generation
make manifests                # Generate CRD manifests + RBAC
make generate                 # Generate DeepCopy
buf generate                  # Generate gRPC client from Buf Schema Registry

# Local development
make install                  # Install CRDs into cluster
make run                      # Run controller locally
make uninstall                # Remove CRDs

# Container builds (multi-arch via Containerfile, UBI10 base)
make image-build IMG=<registry>/osac-operator:tag
make image-push IMG=<registry>/osac-operator:tag
make deploy IMG=<registry>/osac-operator:tag
make undeploy

# Helm
make helm-crds                # Regenerate manifests + sync CRDs to charts/operator-crds/
make check-helm-crds          # Verify CRD sync (CI enforces)
```

## Architecture

### Dual-Controller Pattern

Each resource has a **resource controller** (provisions via AAP, manages finalizers) and a **feedback controller** (syncs state to fulfillment-service via gRPC). See `.claude/rules/controller-patterns.md` for reconciliation, finalizer, and AAP integration patterns.

`internal/controller/storage_controller.go` (`StorageReconciler`) is an exception to this pattern: it reconciles `Tenant` resources to provision/deprovision tenant storage (StorageClass discovery, CaaS backend and cluster storage lifecycle) rather than owning its own CRD or following the AAP-based provisioning flow.

### Provisioning

ClusterOrder and ComputeInstance controllers use direct AAP REST API integration via the `ProvisioningProvider` interface (`pkg/provisioning/provider.go` and `pkg/aap/client.go`). Networking controllers (VirtualNetwork, Subnet, SecurityGroup) and the ExternalIP family of controllers also use this pattern. Subnet uses `pkg/provisioning`'s multi-target APIs (`RunMultiTargetProvisioningLifecycle`, `RunMultiTargetDeprovisioningLifecycle`) to dispatch fabric and k8s manager jobs in parallel when a NetworkClass has both; `JobTarget`/`DeprovisionTarget.AbsorbsLegacyHistory` marks the one target that inherits pre-existing, untargeted job history. `pkg/provisioning`, `pkg/aap`, and `pkg/dispatcher` are public packages consumed outside this repo (e.g., `bare-metal-fulfillment-operator`) — changes to their interfaces can impact those consumers. `pkg/dispatcher.NetworkClassClient` is the seam other operators implement to resolve a NetworkClass's fabric/k8s managers without depending on this repo's `internal/api` gRPC client; `internal/dispatcheradapter` is this repo's own implementation, wrapping the generated `privatev1.NetworkClassesClient`. Management-state annotation (`osac.openshift.io/management-state = Unmanaged`) is checked by every resource controller except `tenant_controller.go` to skip reconciliation.

### Multi-cluster

Built on controller-runtime and multicluster-runtime (see `go.mod` for current versions). Hub cluster runs the operator; remote cluster hosts Tenant/ComputeInstance resources (via `OSAC_REMOTE_CLUSTER_KUBECONFIG`).

### Console Proxy

Aggregated API server in `cmd/console-proxy/` provides KubeVirt VM console/VNC access via WebSocket. TLS configured via `OSAC_CONSOLE_PROXY_TLS_CERT_FILE` and `OSAC_CONSOLE_PROXY_TLS_KEY_FILE`. Server implementation in `internal/consoleproxy/` (auth, config resolver, discovery, server, subresource handlers). Tested in `test/integration/console_proxy_test.go`.

### gRPC Client

Consumes private fulfillment-service API. Generated in `internal/api/` from Buf Schema Registry (module version pinned in `buf.gen.yaml`). Update version there and run `buf generate` when API changes. Auth via `OSAC_FULFILLMENT_TOKEN_FILE`.

## File Organization

```text
api/v1alpha1/              # CRD type definitions
cmd/
  main.go                  # Operator entry point
  console-proxy/           # Console proxy aggregated API server
pkg/
  aap/                     # AAP REST API client (public package)
  provisioning/            # ProvisioningProvider abstraction
  dispatcher/              # NetworkClass -> manager resolution (public package)
internal/
  api/                     # Generated gRPC client (DO NOT EDIT)
  controller/              # Reconciliation logic
    {resource}_controller.go           # Provisioning controller
    {resource}_feedback_controller.go  # Feedback controller
  consoleproxy/            # Console proxy server implementation (auth, config, handlers)
  dispatcheradapter/       # Adapts internal/api's gRPC client to pkg/dispatcher.NetworkClassClient
  migrations/              # Data migrations (e.g., migrate_subnetrefs.go)
helpers/                   # Utility functions (at project root, not under internal/)
config/
  crd/                     # Generated CRD manifests (DO NOT EDIT)
  rbac/                    # Generated RBAC rules
  samples/                 # Example CRs and config Secret
  testing/                 # Testing configurations
    default/               # Default testing kustomization
    console-proxy/         # Console proxy testing kustomization
charts/
  operator/                # Helm chart for operator
  operator-crds/           # Helm chart for CRDs
test/integration/          # Integration tests (console_proxy_test.go, integration_suite_test.go, networking_test.go)
hack/sync-helm-crds.py     # Script invoked by `make helm-crds` to sync CRDs to Helm charts
```

## Testing

- **Unit tests**: Ginkgo + Gomega with `envtest` (real etcd + kube-apiserver)
- **Integration**: `make test-kustomize` (manifest validation) + `make test-smoke` (kind cluster)
- **Go integration tests**: `test/integration/` (console_proxy_test.go, integration_suite_test.go, networking_test.go) — run against an already-running kind cluster via `make test-integration-kind` (`go test ./test/integration/ -v -ginkgo.v`)
- **E2E tests**: pytest-based, live in the separate `osac-test-infra` repo; triggered by the root-level `.github/workflows/e2e-vmaas-full-install.yml`, which builds and deploys both `osac-operator` and `fulfillment-service` together
- Kind cluster defaults to `osac` (`KIND_CLUSTER_NAME` in Makefile line 81), but smoke tests create `osac-test`
- Clean up: `kind delete cluster --name osac-test`
- `test-kustomize` catches missing files in kustomization.yaml — always run before committing manifest changes

## Code Quality

- **golangci-lint** (see `Makefile` for pinned version) configured in `.golangci.yml`: dupl, errcheck, ginkgolinter, goconst, gocyclo, govet, ineffassign, lll, misspell, prealloc, revive, staticcheck, unconvert, unused
- Formatters: gofmt, goimports
- Pre-commit hooks (trailing-whitespace, check-merge-conflict, end-of-file-fixer, yamllint --strict, detect-private-key, plus an `osac-operator-golangci-lint` hook scoped to this component's `.go` files) are defined in the root-level `.pre-commit-config.yaml` — there is no `osac-operator`-local pre-commit/yamllint config anymore.
- Run manually from the repo root: `pre-commit run --all-files`
- **CRITICAL**: Always run `make lint` before committing — CI enforces strict linting

## Automation Hooks

Hooks are configured in `.claude/settings.json` and run automatically during agent sessions:

- **CRD type changes** (`PostToolUse`): When `*_types.go` is edited, `make manifests generate` runs automatically.
- **Go module changes** (`PostToolUse`): When `go.mod` is edited, `go mod tidy` runs automatically.
- **Pre-PR** (`PreToolUse`): `make fmt` (fails if files changed — commit fixes first), `make lint`, and `make test` run before `gh pr create`.

## Detailed Rules (auto-loaded from `.claude/rules/`)

- **`controller-patterns.md`** — Dual-controller, reconciliation, finalizer, AAP, feedback, CRD type patterns
- **`common-pitfalls.md`** — 10 common issues: regen, status loops, finalizers, AAP polling, NotFound, etc.
- **`common-tasks.md`** — Adding CRDs/fields, cross-repo change order, RBAC, debugging
- **`configuration.md`** — Environment variables for AAP, gRPC, namespaces, controller flags

## PR Checklist

- [ ] `make manifests generate` if types changed
- [ ] `make helm-crds` run after CRD regeneration
- [ ] `make lint` passes (enforced by CI)
- [ ] `make test` passes
- [ ] `make test-kustomize` passes (catches missing files)
- [ ] CRD changes tested against a cluster
- [ ] Cross-repo dependencies documented in PR description
- [ ] PR title includes Jira key (e.g., "OSAC-12345: fix subnet race")
- [ ] Commits signed off (`git commit -s`) and include AI attribution if applicable

## CI Workflows

- **build-image.yaml**: Runs `make test`, `make test-kustomize`, `make test-smoke`, then builds and pushes container + manifest container
- **check-generated-code.yaml** (repo root, matrixed across components): Validates `buf generate` output unchanged (ensures gRPC client is up-to-date)
- **helm-lint.yaml**: Checks CRD sync (`hack/sync-helm-crds.py`) and lints Helm charts
- **e2e-vmaas-full-install.yml** (repo root, shared with fulfillment-service): builds both components and runs E2E tests in a VMaaS environment

## Security

- **Containers**: UBI10 base images (see `Containerfile` for pinned tags), non-root USER 1001
- **Secrets**: AAP credentials and fulfillment-service tokens via Secret (`config/samples/osac-config-secret.yaml`)
- **TLS**: Console proxy uses `OSAC_CONSOLE_PROXY_TLS_CERT_FILE` and `OSAC_CONSOLE_PROXY_TLS_KEY_FILE`
- **gRPC auth**: Token file via `OSAC_FULFILLMENT_TOKEN_FILE`
- **AAP TLS verification**: Configurable via `OSAC_AAP_INSECURE_SKIP_VERIFY` (defaults false)
- **Pre-commit**: `detect-private-key` hook prevents credential leaks

## Links

- [Kubebuilder Book](https://book.kubebuilder.io/)
- [controller-runtime](https://pkg.go.dev/sigs.k8s.io/controller-runtime)
- [OSAC Project](https://github.com/osac-project)
