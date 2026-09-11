# Fulfillment service

gRPC and REST APIs, persistence, authorization, resource lifecycle, and the
`osac` CLI.

This component is part of the OSAC monorepo, not an isolated project. Its APIs,
generated artifacts, deployment configuration, and runtime behavior may affect
other components. Apply the repository-wide rules in
[`../AGENTS.md`](../AGENTS.md), consider downstream consumers before changing
behavior, and follow the instructions for every affected component.

## Required context

Before changing this component, identify the documents relevant to the change
below, then read and follow them. These documents are authoritative for their
respective areas.

- API or proto work: [`docs/API.md`](docs/API.md) and [`docs/CLEANAPI.md`](docs/CLEANAPI.md)
- Authentication/authorization: [`docs/AUTH.md`](docs/AUTH.md)
- Database or request lifecycle: [`docs/CODEWALK.md`](docs/CODEWALK.md)
- Deployment and local setup: [`docs/INSTALL.md`](docs/INSTALL.md) and [`README.md`](README.md)
- CLI-specific conventions: [`internal/cmd/cli/AGENTS.md`](internal/cmd/cli/AGENTS.md)

## Invariants

- The proto contract lives in the top-level `proto/` module, not here. `proto/private/` is the API source of truth; `proto/tests/` contains editable test-only definitions.
- Never edit `proto/public/` or `proto/gen/` manually; `proto/tests/` is editable test-proto source. Generated Go is one shared tree at `proto/gen/`, imported by every module as `github.com/osac-project/osac/proto/gen/...`.
- Express field and cross-field validation with proto validation annotations when possible, not duplicated Go checks.
- Base resource messages follow the custom `OSAC_OBJECT_SHAPE` rule. An intentional exception requires `// buf:lint:ignore OSAC_OBJECT_SHAPE` directly above the message.
- Update validation operates on the stored object after applying the update mask, not on the partial request alone.
- Public servers wrap private servers and add tenant/auth behavior; preserve that boundary.
- Existing database migrations are immutable. Add a new numbered migration instead of changing an applied one.
- Tenant authorization and attribution must remain enforced on every public resource path.

## Generated files

- Proto changes are regenerated ONCE, in the top-level `proto/` module: `make -C ../proto generate` (= `uv run dev.py build protos` for `proto/public/` + `buf generate` for `proto/gen/`). `make -C ../proto lint` runs `buf lint`. No more per-consumer `buf generate`.
- Commit the `proto/private/` (or `proto/tests/`) source, the regenerated `proto/public/`, and the regenerated `proto/gen/`. CI (`Check generated code (proto)`) fails the PR if `proto/gen/` or `proto/public/` is stale.
- Test-only proto changes under `proto/tests/` still regenerate `proto/gen/` but not `proto/public/`.
- Run `go generate ./...` here for mocks and other `go:generate` outputs; run `go mod tidy` after module changes.
- Never hand-edit `proto/public/`, `proto/gen/`, `*_mock.go`, or `go.sum`.

## Validation

From `fulfillment-service/`:

```bash
uv run dev.py lint                 # Go/proto lint
uv run ruff check                  # Python lint
helm lint charts/service -f charts/service/ci-values.yaml
helm template test charts/service -f charts/service/ci-values.yaml
go build ./cmd/fulfillment-service ./cmd/osac
ginkgo run -r internal             # Unit tests; excludes it/
ginkgo run internal/servers        # Focused package tests
```

For integration tests, use the installer-owned target with an available Kind
cluster: `make -C ../osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment`.
See `README.md` for required host entries and deployment setup.
