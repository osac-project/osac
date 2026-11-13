# ADR 0001: Talk to the REST gateway, not gRPC

Status: Accepted — 2026-11-13

## Decision

The provider calls the REST gateway (`/api/fulfillment/v1/...`) with a plain
`net/http` client. It does not use the gRPC API or the generated Go clients
of the `proto` module.

## Why

- The gateway serves `/api/*` on the same hostname and port as the gRPC API,
  so the `osac login` session works unchanged: same server address, same CA
  bundle, same token renewal (`internal/osaccli`).
- The gateway marshals protojson with proto field names, and the generated
  schemas use the same names, so responses convert to state and plans
  convert to request bodies with no per-resource field mapping
  (`internal/provider/convert.go`).
- No dependency on the `proto` module: the provider tracks the public API
  through the generated schemas only.

## Costs

- REST path naming is inconsistent between services
  (`baremetal_instances` but `bare_metal_instance_types`) — OSAC-7379.
- The gateway drops create/update validation warnings, such as deprecated
  disk image notices — OSAC-7380.
- Updates need an explicit `updateMask` query parameter; deletes are async
  and need polling (`internal/provider/settle.go`).

## Alternatives rejected

**gRPC API with the generated proto clients** — the first POC approach.

- x two generated type systems (proto and schema) to reconcile per resource
- x drags the `proto` module and its generated tree into the provider
