# OSAC CLI

This directory is part of the fulfillment-service component, not an isolated
project. Read [`../../../../AGENTS.md`](../../../../AGENTS.md),
[`../../../AGENTS.md`](../../../AGENTS.md), and
[`.claude/rules/cli-ux.md`](.claude/rules/cli-ux.md).

The CLI is tenant-facing, not a Kubernetes administration interface. Users
must not need Kubernetes knowledge. Follow kubectl conventions by default;
consult comparable cloud CLIs when kubectl has no equivalent. Keep commands
non-interactive and scriptable.

- Write command and flag help using Markdown.
- Use `{{ bt }}` for inline code and `{{ bt 3 }}` for fenced code blocks.
- Do not end `shortHelp` with a period.
- Prefix flag help with a short italicized type, followed by ` - `.
- Wrap private-API commands with `help.MarkPrivateAPI`.
- Add every private command to the corresponding `privateNames` annotation test.
- Follow nearby command structure and tests.
