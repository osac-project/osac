# Project context for coding agents

These tracked documents hold OSAC context used by coding, planning, and review
agents. Root and component `AGENTS.md` files specify when each document must be
read. No bootstrap, tool-specific rules, or skill invocation is needed to read them.

Codex and Cursor read `AGENTS.md` directly. Root `CLAUDE.md` and `GEMINI.md`
import that same file without duplicating guidance. The Claude import also
supports installations without native `AGENTS.md` discovery. Follow the root
instructions to read applicable component `AGENTS.md` files before editing.

| When | Required context |
|------|------------------|
| Networking planning, implementation, or review, including resource IP/MAC data | [Networking decisions](networking-decisions.md) and the applicable accepted designs |
| Installer values/schema changes or planning their delivery | [Enclave Wizard pipeline](enclave-wizard-pipeline.md) |
| Requirements/design drafting, decomposition, or review | [Feature dimensions](osac-dimensions.md) and [review patterns](review-patterns.md) |

## Ownership and maintenance

OSAC owns these project-specific references alongside its implementation.
`osac-project/osac-ai-skills` owns reusable skills, workflow templates, and
bootstrap fan-out. Its `.design/context/*.md` files retain the workflow paths
as forwarding documents to this directory; maintain the full content here.
The forwarding documents instruct skills to read the local OSAC document,
with a GitHub fallback for standalone skill/evaluation workspaces.

Update context in the same PR as a changed contract or workflow. When accepted
designs evolve in `enhancement-proposals`, update the affected summary here
and verify its source links. Accepted designs define target behavior; check
the current API, CRDs, and implementation before claiming the behavior ships.
Identify an intentional revision or extension explicitly when changing a design.

Keep essential rules and reading triggers in `AGENTS.md`; keep detailed
explanations here. Preserve existing skill entry paths when moving a reference,
update affected skills and their versions in `osac-ai-skills`, and validate
fan-out and reference resolution together before retiring an old source.

## Guidance retired from shared Claude rules

| Former rule | Current source |
|-------------|----------------|
| `architecture-patterns.md` | Root `AGENTS.md` tenant invariants, [cross-component architecture](../ARCHITECTURE.md), component API/auth docs, and [networking decisions](networking-decisions.md) |
| `dev-conventions.md` | Root `AGENTS.md` Git and Jira conventions |
| `networking-design-alignment.md` | Root networking reading trigger and [networking decisions](networking-decisions.md) |
| `request-path-tracing.md` | Fulfillment `AGENTS.md` and [request-path tracing](../../fulfillment-service/docs/REQUEST_PATH_TRACING.md) |
| CLI `cli-ux.md` | CLI `AGENTS.md` and [CLI UX guidelines](../../fulfillment-service/internal/cmd/cli/CLI_UX.md) |
