# ADR 0003: Keep the schema glue hand-written in the provider files

Status: Accepted — 2026-11-13

## Decision

Schema rules that OpenAPI can't express — `Required`, `Sensitive`,
`RequiresReplace`, cross-field validators — are applied by hand in the
resource and data source files (`internal/provider`), on top of the
generated schema. There is no specification post-processing step.

## Context: where plan modifiers are lost

- Generated output is uniformly `Optional` + `Computed`: nothing is
  `Required`, no cross-field validators, no `Sensitive`, no
  `RequiresReplace`.
- The loss happens at the OpenAPI step, not in the framework generator: the
  intermediate provider code specification supports plan modifiers and
  validators, with imports and Go code snippets — injecting a
  `RequiresReplace` into the specification JSON works. OpenAPI just has no
  way to derive them, so they never reach the specification.

## Why hand-written glue

- Smaller than a specification enricher would be.
- Sits next to the CRUD logic that depends on it.
- Type-checked by the compiler.
- Regeneration never touches it.

## What the glue does today

- `Secret.type` forces replacement: immutable server-side (OSAC-7403).
- Resource ids use `UseStateForUnknown`, so plans don't show unknown ids.
- Secret `data` entries are marked `Sensitive` (`markSensitive`).
- Data sources accept exactly one of `name` or `id`
  (`stringvalidator.ExactlyOneOf`).

## Alternatives rejected

**Post-process the provider code specification** — inject plan modifiers,
validators and sensitive flags between tfplugingen-openapi and
tfplugingen-framework.

- x a second tool to write, test and maintain
- x the rules live far from the CRUD code that depends on them
- x string-typed: specification snippets aren't compiler-checked
