# Cross-Component Docs

This directory contains concise, hand-maintained guidance for architecture and
conventions that span component or repository boundaries. Component `AGENTS.md`
files own component-scoped agent rules and invariants and route readers to the
referenced README and documentation for architecture, setup, and detailed
conventions. Root `AGENTS.md` owns cross-component flow and dependency
guidance; [`ARCHITECTURE.md`](ARCHITECTURE.md) and
[`CONVENTIONS.md`](CONVENTIONS.md) provide the detailed cross-component
reference.

Also in this directory:

- [codex-getting-started.md](codex-getting-started.md) — onboarding OpenAI
  Codex against an OSAC checkout (install, `/import`, permissions, hook trust,
  MCP, and workflow differences).

Regeneration tooling for these documents is unconfirmed. Any future
regeneration must preserve the cross-component-only scope rather than replace
these files with an unfiltered snapshot.
