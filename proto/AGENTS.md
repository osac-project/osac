# AGENTS.md — `proto/`

Shared Protobuf contract for the whole repo. This one module holds
the proto sources **and** the single generated Go tree every other module
imports (`github.com/osac-project/osac/proto/gen/osac/<layer>/v1`). Before this
consolidation each Go module generated its own private copy into its
`internal/api/`, so a proto change had to be regenerated and committed in up to
four places or CI/e2e would fail downstream.

## Layout

- `private/osac/private/v1/*.proto` — **editable source of truth.**
- `public/osac/public/v1/*.proto` — **generated** from `private/` by
  `protoc-gen-cleanapi` (elements marked private are filtered out). Do not edit
  by hand.
- `tests/osac/tests/v1/*.proto` — editable test-only proto source.
- `gen/` — **generated Go**, committed, CI-verified. Never edit by hand. This is
  what every module imports.
- `buf.yaml` / `buf.gen.yaml` / `buf.lock` — buf config for the module.

## Contributing a proto change

1. Edit files under `private/` (or `tests/` for test-only types).
2. From this directory: `make generate`
   - regenerates `public/` from `private/` (via `fulfillment-service/dev.py`,
     which owns the cleanapi toolchain), then
   - regenerates the Go tree under `gen/` (`buf generate`).
3. `make lint` runs `buf lint` (uses the `buf-plugin-osac-lint` plugin built
   from `fulfillment-service/cmd/buf-plugin-osac-lint`).
4. Commit **all** of: the `private/` (or `tests/`) source, the regenerated
   `public/`, and the regenerated `gen/`. CI (`Check generated code (proto)`)
   fails the PR if `gen/` or `public/` is stale.

## Consumers

`go.work` wires this module into the workspace, so `go build ./...` in any
member module resolves `github.com/osac-project/osac/proto` locally. Each
consumer `go.mod` also carries a `replace github.com/osac-project/osac/proto =>
<relative path>` so standalone `go mod tidy` / container builds resolve it
without a published version.
