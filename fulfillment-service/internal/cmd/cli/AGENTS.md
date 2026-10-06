# OSAC CLI

This directory is part of the fulfillment-service component, not an isolated
project. Read [`../../../../AGENTS.md`](../../../../AGENTS.md) and
[`../../../AGENTS.md`](../../../AGENTS.md). When designing or changing commands,
read the [CLI UX guidelines](CLI_UX.md).

- Write command and flag help using Markdown.
- Use `{{ bt }}` for inline code and `{{ bt 3 }}` for fenced code blocks.
- Do not end `shortHelp` with a period.
- Prefix flag help with a short italicized type, followed by ` - `.
- Wrap private-API commands with `help.MarkPrivateAPI`.
- Add every private command to the corresponding `privateNames` annotation test.
- Follow nearby command structure and tests.
