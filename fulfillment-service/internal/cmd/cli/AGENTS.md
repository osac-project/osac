# OSAC CLI

Never edit this file except for minor corrections. Do not append new content.

This directory is part of the fulfillment-service component, not an isolated
project. Read [`../../../../AGENTS.md`](../../../../AGENTS.md) and
[`../../../AGENTS.md`](../../../AGENTS.md). When designing or changing commands,
read the [CLI UX guidelines](CLI_UX.md).

## Command conventions

- Write command and flag help using Markdown.
- Use `{{ bt }}` for inline code and `{{ bt 3 }}` for fenced code blocks.
- Do not end `shortHelp` with a period.
- Prefix flag help with a short italicized type, followed by ` - `.
- Wrap private-API commands with `help.MarkPrivateAPI`.
- Add every private command to the corresponding `privateNames` annotation test.

## Common tasks

### Add or extend a command

- Follow existing verbs and registration in `root_cmd.go` rather than adding
  a synonym. Keep the command non-interactive and scriptable by default, as
  described in `CLI_UX.md`.
- Start with the corresponding verb package, such as `get/` or `create/`.
  Inspect shared lookup, rendering and help helpers before adding a new path.
  Keep human-readable and structured output behavior consistent with nearby
  commands; do not expose credentials through generic object rendering.
- Trace flags and input through routing/filtering/transformation to the server
  using the parent component's request-path guide. A flag accepted by Cobra
  is not proof that the handler receives or applies it.
- Add regression tests beside the command for parsing, registration and help.
  Use the existing `it/` CLI harness from the parent component for deployed
  behavior against a running service.

## Validation

Run from `fulfillment-service/`, not this nested CLI directory:

```bash
go build ./cmd/fulfillment-service ./cmd/osac
uv run dev.py lint
ginkgo run --timeout 20m -r internal
```

The recursive unit run includes CLI packages; use it when changes span command
registration, help or shared input handling. Follow the parent guide for
focused server tests and deployed API/CLI integration checks. Unit success
does not prove authentication or lifecycle behavior against a running service.
