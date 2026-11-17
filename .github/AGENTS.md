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

Run from the repository root. The reusable `workflows/pre-commit.yaml` runs on
every change and includes these regression checks:

```bash
uv run --no-project --with pyyaml==6.0.3 python -m unittest discover -s .github/scripts -p test_umbrella_release.py -v
uv run --no-project --with pyyaml==6.0.3 python -m unittest discover -s .github/scripts -p test_release_versioning.py -v
```

Preserve `workflows/pre-commit.yaml`'s separate PR-range secret-scan consumer
with base-owned configuration; contribution-level scanning is covered at root.

| Touched area | Additional repository-root validation |
|---|---|
| Workflow/filter/policy | actionlint and applicable pre-commit hooks; inspect required-check reporting |
| CaaS selection | `OSAC_TEST_INFRA_DIR=/path/to/osac-test-infra uv run pytest -n 0 tests/unit/test_caas_ci_suite_policy.py` (companion checks skip without that checkout) |
| Flake reporting | `python3 -m unittest discover -s .github/scripts -p test_flake_report.py` |

Release helper tests use captured publication operations, not live registry or
Konflux evidence; inspect prerequisites and [boundaries](../docs/INTEGRATION-TESTING.md#release-automation).

## Component-hooks and sandbox regression smoke tests

Follow the [local commands and production boundary](../.agentic-ci/AGENTS.md).
The workflow stays present on every PR and merge-group run, including unrelated
changes that skip execution.
Its path filter uses one brace-alternation entry because `predicate-quantifier:
every` combines separate entries with AND. Keep overlay/helper, test, and Python
setup paths in that entry. See the [sandbox coverage boundary](../docs/INTEGRATION-TESTING.md#agentic-ci-sandbox).

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
