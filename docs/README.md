# Cross-Component Docs

Hand-trimmed, cross-component-only excerpts of what used to be
`osac-workspace/reference/ARCHITECTURE.md` and `reference/CONVENTIONS.md` —
not raw regenerated codebase-analysis snapshots. Per
[ADR 0001](https://github.com/osac-project/osac-workspace/blob/main/decisions/0001-dedicated-ai-skills-repo.md)'s
exit-criterion (e) (OSAC-4008): these two files ([ARCHITECTURE.md](ARCHITECTURE.md),
[CONVENTIONS.md](CONVENTIONS.md)) were trimmed down to only the content with
no equivalent in any single component's own `AGENTS.md`, and relocated
here, co-located with the code they analyze. The other five `reference/*.md`
siblings (`CONCERNS.md`, `INTEGRATIONS.md`, `STACK.md`, `STRUCTURE.md`,
`TESTING.md`) had zero live consumers and will be deleted (rather than
relocated) as part of the companion `osac-workspace` cleanup PR referenced
above.

`ARCHITECTURE.md` says "Cross-Component" and `CONVENTIONS.md` says
"Cross-Repo Dependencies" — used interchangeably for this directory's
scope (content spanning a component/repo boundary with no home in a
single `AGENTS.md`); this isn't a deliberate scope distinction between
the two files.

- **Component-specific architecture/conventions are authoritative in each
  component's own `AGENTS.md`** — `fulfillment-service/AGENTS.md`'s and
  `osac-operator/AGENTS.md`'s own `## Architecture` sections, not these
  files, are the source of truth for anything scoped to one component.
- **`osac-workspace`'s `reference/ARCHITECTURE.md`/`CONVENTIONS.md` will
  become symlinks into this directory** once `osac-workspace`'s companion
  cleanup PR merges (OSAC-4008 Task 4, sequenced after this PR) — edit
  here, not there, once that lands.
- **Regeneration tooling is unconfirmed.** These files originated from a
  `.planning/codebase/`-tool-generated snapshot (exact tool/command not
  located during OSAC-4008's investigation — TBD, confirm before relying
  on any automated regeneration). Any future regeneration must re-apply
  this trim (cross-component content only) rather than overwrite these
  files wholesale with an unfiltered snapshot.
