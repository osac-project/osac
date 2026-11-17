# AGENTS.md — `proto/`

Never edit this file except for minor corrections. Do not append new content.

Shared Protobuf sources and the single generated Go tree consumed across the
monorepo (`github.com/osac-project/osac/proto/gen/osac/<layer>/v1`).

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

Use Go from `go.mod`, uv, Buf and protoc; the fulfillment development toolchain
owns the cleanapi and lint plugins. Run commands from `proto/`:

```bash
make generate
make lint
go build ./...
```

1. Edit files under `private/` (or `tests/` for test-only types).
2. From this directory: `make generate`
   - regenerates `public/` from `private/` (via `fulfillment-service/dev.py`,
     which owns the cleanapi toolchain), then
   - regenerates the Go tree under `gen/` (`buf generate`).
3. `make lint` runs the fulfillment dev.py proto lint path, including `buf lint`.
   Its `buf-plugin-osac-lint` plugin is built from
   `fulfillment-service/cmd/buf-plugin-osac-lint`.
4. Commit **all** of: the `private/` (or `tests/`) source, the regenerated
   `public/`, and the regenerated `gen/`. CI (`Check generated code (proto)`)
   fails the PR if `gen/` or `public/` is stale.

Building this module checks generated Go compilation, not compatibility of
every consumer. Validate affected consumers after API changes. Do not invoke
`buf generate` alone as a substitute for the ordered private-to-public step.

## API semantics

For API shape, validation, visibility, or update-mask changes, read
[API.md](../fulfillment-service/docs/API.md) and
[CLEANAPI.md](../fulfillment-service/docs/CLEANAPI.md) before editing sources.

## Common Tasks

### Add a field or change validation

- Edit the owning resource in `private/osac/private/v1/`. Follow neighboring
  field numbering, visibility and validation annotations; preserve protobuf
  wire compatibility when retiring or replacing fields.
- Keep field/cross-field validation in proto annotations where supported.
  Resource messages must satisfy `OSAC_OBJECT_SHAPE`; use the documented
  explicit lint exception only for an intentional non-resource shape.
- For update behavior, inspect the fulfillment server's mask application and
  stored-object validation as well as the request message. A partial update
  request is not the complete resource being validated.
- Run the ordered generation/lint/build sequence above and inspect both the
  public API projection and Go diff. Exercise affected server and consumer
  tests; generated compilation alone does not establish semantic compatibility.

### Change visibility or generator behavior

- Read the linked `CLEANAPI.md` before changing private annotations. Verify
  that the generated public tree excludes private-only elements without
  breaking public service references; do not repair the public output by hand.
- Generator implementations and their tests live in `../fulfillment-service/`;
  `tests/osac/tests/v1/` supplies test-only contracts. Follow that component's
  checks when the generator or lint plugin changes.
- The shared Go tree does not update the UI's separately pinned TypeScript
  baseline. Follow the explicit authorization rule in
  [the UI guide](../osac-ui/AGENTS.md#generated-api-bindings).
