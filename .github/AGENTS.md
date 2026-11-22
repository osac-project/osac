# .github/ Agent Context

Never edit this file except for minor corrections. Do not append new content.

GitHub workflow orchestration, path selection, release automation and merge gates.

## CI, E2E and merge rules

Read [CI.md](CI.md) before changing workflows, filters or policy.

- Preserve required `CI`, `E2E`, `jira-reference`, exact-head approval and merge
  queue gates. New cheap checks need a filter, a `ci.yml` job and `CI.needs`.
- Keep workflow names synchronized with `workflow_run` lists and policy
  `workflow:` dispatch references. Reusable checks hold no path logic;
  `workflows/ci.yml` selects through `filters/ci-filters.yml`. Test-matrix
  changes must agree with `ci-tests.yml` and `workflows/tests.yml`.
- E2E decision workflows must not execute untrusted PR code. Test only the
  approved commit; forks require a human with write access. Preserve external
  contributor `ok-to-test` authorization and independent readiness gates.
- Keep `tests/e2e/**` in the `e2e-suite` filter, not an ignore list.
- Pipeline/security/ownership changes require infrastructure approval per
  `CODEOWNERS`; only UI lint/typecheck workflows also permit UI approval.
  Dependency-bot policy must never auto-approve workflow changes.
- CaaS PR/queue selection stays `caas/sanity` with `-m sanity`; periodics may
  run regression and reference suites. Never widen an empty tier or append
  references to sanity. Readiness and fork authorization are independent.

## Validation

Select relevant checks from the repository root: actionlint for changed
workflows, applicable pre-commit hooks, and inspection of affected selection,
matrix and required-check reporting. Preserve the separate PR-range secret scan
in `workflows/pre-commit.yaml` with its base-owned configuration.

Local checks do not establish hosted scheduling, publication or deployed E2E
success. Report those limitations when relevant; do not add a new test harness
for routine workflow edits.

## Release safety

- Use provisional `sha-*` images until all required gates pass. Failed runs
  must not publish release-looking nightly tags.
- Only manual dispatch may set `skip_unit`, `skip_integration` or `skip_e2e`;
  report each in the tag message and success notification. Scheduled nightlies
  skip none; security tests are never skippable.
- Push permanent `<component>/vX.Y.Z` tags with real actor credentials, not
  `GITHUB_TOKEN`, which suppresses downstream push-triggered workflows.
  Verify every component publish workflow started and succeeded; a tag alone
  is not publication evidence.
