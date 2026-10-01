# Fulfillment service

gRPC and REST APIs, persistence, authorization, resource lifecycle, and the
`osac` CLI.

This component is part of the OSAC monorepo, not an isolated project. Its APIs,
generated artifacts, deployment configuration, and runtime behavior may affect
other components. Apply the repository-wide rules in
[`../AGENTS.md`](../AGENTS.md), consider downstream consumers before changing
behavior, and follow the instructions for every affected component.

## Code style

- Write clear, idiomatic Go consistent with the project's architecture, conventions, and surrounding code.
- Look for opportunities to reuse existing code and patterns rather than duplicating logic.
- Keep related code together, favoring existing packages and files where they fit naturally.
- Favor simplicity, readability, and maintainability over unnecessary abstractions.

## Required context

Before changing this component, identify the documents relevant to the change
below, then read and follow them. These documents are authoritative for their
respective areas.

- API or proto work: [`docs/API.md`](docs/API.md) and [`docs/CLEANAPI.md`](docs/CLEANAPI.md)
- Proto changes: [`../proto/AGENTS.md`](../proto/AGENTS.md)
- API or CLI request input changes:
  [`docs/REQUEST_PATH_TRACING.md`](docs/REQUEST_PATH_TRACING.md). Trace the path
  from the user-facing entry point through routing, filtering, and
  transformation layers to the handler.
- Authentication/authorization: [`docs/AUTH.md`](docs/AUTH.md)
- Database or request lifecycle: [`docs/CODEWALK.md`](docs/CODEWALK.md)
- Deployment and local setup: [`docs/INSTALL.md`](docs/INSTALL.md) and [`README.md`](README.md)
- CLI-specific conventions: [`internal/cmd/cli/AGENTS.md`](internal/cmd/cli/AGENTS.md)

## Invariants

- Do not hand-edit `../proto/public/`, `../proto/gen/`, `*_mock.go`, or `go.sum`.
- Express field and cross-field validation with proto validation annotations when possible, not duplicated Go checks.
- Base resource messages follow the custom `OSAC_OBJECT_SHAPE` rule. An intentional exception requires `// buf:lint:ignore OSAC_OBJECT_SHAPE` directly above the message.
- Update validation operates on the stored object after applying the update mask, not on the partial request alone.
- Public servers wrap private servers and add tenant/auth behavior; preserve that boundary.
- Existing database migrations are immutable. Add a new numbered migration instead of changing an applied one.
- Tenant authorization and attribution must remain enforced on every public resource path.

## Generated files

Run from `fulfillment-service/` when applicable:

```bash
make -C ../proto generate # After proto changes.
make -C ../proto lint
go generate ./... # After changes affecting mocks or other generated Go files.
go mod tidy # After module dependency changes.
```

## Validation

Run these checks from `fulfillment-service/` as applicable.

### Local checks

```bash
uv run dev.py lint # Lint Go and Protobuf.
uv run ruff check # Lint Python.
helm lint charts/service -f charts/service/ci-values.yaml
helm template test charts/service -f charts/service/ci-values.yaml
go build ./cmd/fulfillment-service ./cmd/osac
ginkgo run --timeout 10m internal/servers # Focused server unit tests.
ginkgo run --timeout 20m -r internal # All unit suites. Slow, run only when necessary.
```

### Integration tests

Integration tests run in a local Kind Kubernetes cluster. The installer test
target builds, loads, and deploys the current service image.

See the [fulfillment-service test tiers and coverage notes](../docs/INTEGRATION-TESTING.md#fulfillment-service).

It reuses the existing cluster and database. For a full suite run, use a fresh
environment unless the user agrees to reuse the database. See `README.md` for
prerequisites and host entries.

The `it/` suite includes CLI workflows that exercise only Fulfillment Service
APIs. Its harness builds the CLI from this checkout and runs it against the
deployed service. Catalog Item API behavior, CLI creation, and the ClusterOrder
release image written by Fulfillment are checked in `it/`. Keep cross-component
provisioning journeys under `tests/e2e/`.

The MCP SDK spec in `it/` starts its HTTP handler in-process and calls the
deployed public Fulfillment API with each user's token. It covers public API
authorization and persistence, while chart renders cover opt-in deployment
shape. It does not exercise a deployed MCP route or TLS handshake.

To prepare a fresh environment, recreate the dedicated `osac-dev` Kind
cluster. Collect useful diagnostics before deleting it.

```bash
export KUBECONFIG="$HOME/.kube/osac-dev-kind.kubeconfig"
make -C ../osac-installer install-infra PLATFORM=kind PROFILE=dev NS=osac
make -C ../osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment
```

If a fresh environment is needed, confirm that `osac-dev` is a disposable test cluster before deleting it with `kind delete cluster --name osac-dev`.

If setup fails with `failed to lookup host '...'`, read [README.md — Running integration tests](README.md#running-integration-tests) for host entries.

## Writing tests

- Prefer Ginkgo/Gomega, following existing test conventions. Use `DescribeTable` with named `Entry` cases when setup and assertions are shared.
- Extend existing suites and reuse their fixtures, mocks, and harness setup. The `internal/servers` suite provides database/transaction setup; `it/` provides deployed-service clients and a CLI harness.
- Keep cases independent, register cleanup with `DeferCleanup`, and use bounded `Eventually` assertions for asynchronous behavior instead of sleeps.
- Test observable behavior, including relevant error paths and tenant isolation where applicable. Deployed Fulfillment-only API/CLI workflows belong in `it/`; cross-component provisioning belongs in `tests/e2e/`.
- Consult [Fulfillment test tiers and coverage](../docs/INTEGRATION-TESTING.md#fulfillment-service) when choosing coverage for a change.
