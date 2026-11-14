# Architecture decision records

Why the provider is built the way it is. One decision per file, verdict in
the title, reasons in short bullets.

| ADR | Decision |
|-----|----------|
| [0001](0001-use-the-rest-gateway.md) | Talk to the REST gateway, not gRPC |
| [0002](0002-generate-schemas-from-protos.md) | Generate the schemas from the protos with protoc-gen-openapiv2 |
| [0003](0003-keep-schema-glue-hand-written.md) | Keep the schema glue hand-written in the provider files |

Adding a decision: next number, decision as the title, then `Status`,
`Decision`, `Why`, `Alternatives rejected` and `Costs` sections. Mark
superseded records as such, don't delete them.
