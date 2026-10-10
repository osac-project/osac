# .github/ Agent Context

This area is part of the OSAC monorepo, not an isolated project. Its workflow
and automation changes may affect other components. Apply the repository-wide
rules in [`../AGENTS.md`](../AGENTS.md), consider downstream effects, and
follow the instructions for every affected component.

## E2E readiness gate (OSAC-3370)

Full-install e2e (`e2e-vmaas-full-install`, `e2e-bmaas-full-install`, `e2e-caas-full-install`) does **not** auto-spend runners on every PR push. Cheap `e2e-readiness` job waits (`ready=false`) until unlocked; required `e2e-*-gate` stays **pending** (not failed). Docs-only PRs skip readiness and the gate reports success.

**Allow when any of:**
- `lgtm` label present, or previously applied (Prow removes on push; prior `/lgtm` still unlocks later SHAs unless a human has `CHANGES_REQUESTED`)
- `e2e-ready` label applied by `github-actions[bot]` via `/e2e-ready` (test-infra slash handler also `workflow_dispatch`es this repo's thin `e2e-on-label`, which `uses` the test-infra reusable; GITHUB_TOKEN cannot trigger `labeled` workflows; cleanup removes on push; manual UI labels are rejected)
- `coderabbitai[bot]` `APPROVED` on the **exact current HEAD** (blocked while a human still has `CHANGES_REQUESTED`). Auto-start: same-repo via thin `e2e-on-approval` (`uses` test-infra `e2e-on-label`); forks via thin `e2e-on-approval-fork` (`uses` test-infra fork replay). `fork-handoff` stays a top-level job here so the replay gate can match it. `lgtm` / `/e2e-ready` still work. test-infra `e2e-start` never POSTs in_progress `e2e-*-gate` Checks API placeholders (they land on auto-queue / cancel-stale / ok-to-test). Required gates stay pending until native full-install jobs report.

Human GitHub `APPROVED` does **not** unlock. `/ok-to-test` is fork **secrets** only (`authorize-fork-pr`); it does not unlock the cost gate. Fork PRs need `/ok-to-test` (or org membership) **and** one of CR / `lgtm` / `/e2e-ready`.

Cheap checks stay ungated. Schedules / `workflow_dispatch` / `merge_group` skip the readiness job.

Path filter skips docs / unit-test-only PRs (`!**/tests/**` and friends). `tests/e2e/**` is the full-install suite and **must** still set `should-run` (`e2e-suite` filter). Do not fold `tests/e2e` back into the ignore list.

Details + smoke checklist: [`.github/e2e-readiness.md`](e2e-readiness.md).

## OSAC CI check

`workflows/osac-ci.yml` posts one `OSAC CI` check on every pull request: what the PR is waiting for, who has to act and
what happens next. It is informational and not a required check. The program, its policy (`policy/osac.yml`) and the
state descriptions live in [osac-project/osac-ci](https://github.com/osac-project/osac-ci); the workflow calls its
`publish` action pinned to a full commit SHA, so the code and policy only change by bumping that pin in a reviewed PR.

- `workflow_run` lists workflows by exact name. When a workflow that reports a required check is added, renamed or
  removed, update the list in `osac-ci.yml`. A missing name only delays an update until the next event or the 10-minute
  sweep.
- The workflow never checks out or runs pull request code and holds the read-only `osac-ci-reader` app credentials.
  Keep it that way: no `actions/checkout` of the PR, no PR text in a script.
- The sweep re-evaluates only the pull requests whose posted verdict is missing, errored, out of date, stuck or old
  (`stale-only`), so it is cheap. The built-in token allows 1,000 requests an hour; see the osac-ci README before
  changing it.
- `workflows/osac-ci-override.yml` answers `/override <full commit sha> <reason>` from a wg-infra member: it waives the
  protected-path approval for that exact commit and records who and why as a check run. The comment is the authority;
  never accept a check run as proof (any workflow can write one). Only the osac-ui lint and typecheck workflows are
  also approvable by the osac-ui maintainers (`osac-ui/OWNERS`); every other workflow reports a required check, uses
  secrets or publishes an image, so it stays with wg-infra.
- Changes to the files that define the checks (`.github/workflows`, `actions`, `scripts`, `filters`, `CODEOWNERS`,
  `.pre-commit-config.yaml`; Markdown in them excepted) need an approval from `@osac-project/wg-infra`. `CODEOWNERS` lists them and the OSAC CI
  policy applies the same list; keep both in step.

## Release safety

Nightly builds use provisional `sha-*` image tags while all build, unit,
integration, security, and E2E gates run. Promote images to release-looking
nightly tags only after every required gate passes; failed runs must not publish
release-looking tags.

A real, permanent `<component>/vX.Y.Z` tag (release mode's `component_versions`
bump) must be pushed with real actor credentials, not the default
`GITHUB_TOKEN` — GitHub does not fire push-triggered workflows for a ref
created by `GITHUB_TOKEN`, so a component's own image/binary/proto publish
workflow would silently never run even though the tag exists.
Whatever creates such a tag must also verify each of that component's
downstream publish workflows actually started and succeeded before reporting
success; a tag existing is not evidence its publish happened.
