# ADR 0002: Generate the schemas from the protos with protoc-gen-openapiv2

Status: Accepted — 2026-11-13

## Decision

Resource and data source schemas and models are generated from the public
proto definitions by `tools/codegen/generate.sh`:

    protos
      | buf + protoc-gen-openapiv2 (grpc-gateway)
      v
    Swagger 2.0
      | swagger2openapi
      v
    OpenAPI 3
      | tfplugingen-openapi + per-service config
      v
    provider code specification
      | tfplugingen-framework
      v
    internal/gen/*_gen.go

## Why generate at all

- The schemas mirror the API objects; hand-writing them means hand-syncing
  them on every API change.
- The generated models, nested object types and `attr.Value` plumbing are
  most of the boilerplate of a hand-written resource.

## Why openapiv2: three criteria

The proto-to-OpenAPI step must deliver three things:

1. `response_body: "object"` honored: responses must be the resource, not
   the RPC envelope, or the generated Go types collide and don't compile.
2. Proto field names: snake_case, matching both the Terraform idiom and the
   names the gateway puts on the wire (ADR 0001).
3. Enums as strings: they become `stringvalidator.OneOf`, so bad values
   fail at plan time instead of apply time.

Only `protoc-gen-openapiv2` passes all three today.

## Alternatives rejected

**protoc-gen-openapi (gnostic)**

- x ignores `response_body`: envelopes leak in, generated code doesn't
  compile
- x enums as numbers

**protoc-gen-connect-openapi (connectrpc)**

- x optional fields become `oneOf: [schema, null]`, and tfplugingen-openapi
  doesn't look through it: enum validators are lost
- + best raw OpenAPI document (docs, client generation), could run
  alongside

**protoc-gen-openapiv3 (grpc-gateway successor) — rejected for now**

- x camelCase property names only, no proto-names option
- x proto oneofs become oneOf composition, which tfplugingen skips entirely
- x alpha, output unstable

## Costs

- Emits Swagger 2.0, hence the swagger2openapi conversion step.
- Ignores `google.api.field_behavior`: output-only fields such as `status`
  generate as plain optional attributes instead of computed ones.
- `Any` values (`template_parameters`) and proto oneofs (`user_data_source`)
  generate only partially usable shapes.

## Revisit when

- protoc-gen-openapiv3 grows a proto-names option and maps oneofs — then
  re-evaluate it against the three criteria.
