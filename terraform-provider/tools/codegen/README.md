# Schema code generation

Pipeline that generates the Terraform plugin framework schema and model code
for a public API service from its proto definitions, using existing tools
instead of hand-writing the schemas:

```
proto files
    |  buf + protoc-gen-openapiv2 (tools/codegen/buf.gen.openapi.yaml)
    v
OpenAPI 2 document — matches the REST contract exactly, because the plugin
    |           is the one of the grpc-gateway, which honors the `body` and
    |           `response_body` options of the google.api.http annotations
    |  swagger2openapi (npm)
    v
OpenAPI 3 document
    |  tfplugingen-openapi + generator_config.yml
    v
provider code specification (JSON)
    |  tfplugingen-framework
    v
internal/gen/*_gen.go — schema functions, model structs, custom object types
```

## Usage

```bash
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest
go install github.com/hashicorp/terraform-plugin-codegen-openapi/cmd/tfplugingen-openapi@latest
go install github.com/hashicorp/terraform-plugin-codegen-framework/cmd/tfplugingen-framework@latest

tools/codegen/generate.sh baremetal_instances_service
```

The generator configurations in `configs/` map which REST operations of a
service become the Terraform resource and data sources; add one when
generating a service that isn't covered yet.

## What the generated code covers

- The complete attribute tree with snake_case names (the
  `json_names_for_fields=false` option keeps the original proto field names)
  and the descriptions from the proto comments.
- Proto enums become string attributes with an auto-generated
  `stringvalidator.OneOf` validator listing the enum values.
- Model structs and custom nested object types with all the
  `attr.Value`/`attr.Type` plumbing, which is most of the boilerplate of a
  hand-written resource.
- ProtoJSON-compatible types: 64-bit integers are strings, maps and nested
  objects become the corresponding framework types.

## Caveats

- The generated schema is permissive: everything is `Optional` + `Computed`,
  nothing is `Required`, and there are no cross-field validators, no
  `Sensitive` attributes and no `RequiresReplace` plan modifiers. Those rules
  are policy, not shape; they are applied by hand in the resource and data
  source files. Why hand-written instead of post-processing the
  specification: see
  [`../../docs/adr/0003-keep-schema-glue-hand-written.md`](../../docs/adr/0003-keep-schema-glue-hand-written.md).
- `protoc-gen-openapiv2` doesn't know about `google.api.field_behavior`, so
  output-only fields such as `status` are generated as plain optional
  attributes instead of computed ones. The `Any` values of
  `template_parameters` and oneofs such as `user_data_source` generate only
  partially usable shapes. These are accepted costs of the generator choice:
  see
  [`../../docs/adr/0002-generate-schemas-from-protos.md`](../../docs/adr/0002-generate-schemas-from-protos.md).
- The generated code contains schemas and models only: the CRUD
  implementations, the REST client, the name/identifier reference resolution,
  tenant placement and update masks remain hand-written in
  `internal/provider`.
