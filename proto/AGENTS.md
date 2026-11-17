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

## Generation and validation

Use Go from `go.mod`, uv, Buf and protoc. Run from `proto/`:

```bash
make generate
make lint
go build ./...
```

- `make generate` invokes fulfillment's `dev.py` cleanapi toolchain to generate
  public protos, then `buf generate` for Go. Do not run the latter alone.
- `make lint` invokes fulfillment's proto lint path and custom lint plugin.
- Commit source and both generated trees; CI checks their freshness.
- Generated Go compilation does not establish consumer compatibility; test
  affected servers and clients. UI bindings use a separately authorized
  [API baseline](../osac-ui/AGENTS.md#generated-api-bindings).

## API semantics

- Preserve wire compatibility when retiring or replacing fields.
- For API shape, annotations, visibility and stored-object/update-mask validation,
  follow [fulfillment invariants](../fulfillment-service/AGENTS.md#invariants),
  [API.md](../fulfillment-service/docs/API.md) and
  [CLEANAPI.md](../fulfillment-service/docs/CLEANAPI.md).
- Generator and lint-plugin implementations/tests belong to fulfillment;
  `tests/osac/tests/v1/` holds test contracts.
