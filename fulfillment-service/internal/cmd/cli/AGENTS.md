# OSAC CLI

Never edit this file except for minor corrections. Do not append new content.

Fulfillment's Cobra CLI. Read the [CLI UX guidelines](CLI_UX.md) for command
design; the parent service guide owns request-path tracing and deployed tests.

## Command conventions

- Keep commands non-interactive and scriptable by default; reuse existing
  verbs and registration in `root_cmd.go`.
- Write command and flag help using Markdown.
- Use `{{ bt }}` for inline code and `{{ bt 3 }}` for fenced code blocks.
- Do not end `shortHelp` with a period.
- Prefix flag help with a short italicized type, followed by ` - `.
- Wrap private-API commands with `help.MarkPrivateAPI`.
- Add every private command to the corresponding `privateNames` annotation test.

## Validation

Run from `fulfillment-service/`, not this nested CLI directory:

```bash
go build ./cmd/fulfillment-service ./cmd/osac
uv run dev.py lint
ginkgo run --timeout 20m -r internal
```

Keep parsing, registration and help tests beside the command. The recursive
unit run includes CLI packages; deployed API/CLI behavior uses the parent's
`it/` harness, not unit doubles.
