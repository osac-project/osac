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
- `coderabbitai[bot]` `APPROVED` on the **exact current HEAD** (blocked while a human still has `CHANGES_REQUESTED`). Auto-start: same-repo via thin `e2e-on-approval` (`uses` test-infra `e2e-on-label`); forks via thin `e2e-on-approval-fork` (`uses` test-infra fork replay). `fork-handoff` stays a top-level job here so the replay gate can match it. `lgtm` / `/e2e-ready` still work.

Human GitHub `APPROVED` does **not** unlock. `/ok-to-test` is fork **secrets** only (`authorize-fork-pr`); it does not unlock the cost gate. Fork PRs need `/ok-to-test` (or org membership) **and** one of CR / `lgtm` / `/e2e-ready`.

Cheap checks stay ungated. Schedules / `workflow_dispatch` / `merge_group` skip the readiness job.

Path filter skips docs / unit-test-only PRs (`!**/tests/**` and friends). `tests/e2e/**` is the full-install suite and **must** still set `should-run` (`e2e-suite` filter). Do not fold `tests/e2e` back into the ignore list.

Details + smoke checklist: [`.github/e2e-readiness.md`](e2e-readiness.md).

## Release safety

Nightly builds use provisional `sha-*` image tags while all build, unit,
integration, security, and E2E gates run. Promote images to release-looking
nightly tags only after every required gate passes; failed runs must not publish
release-looking tags.
