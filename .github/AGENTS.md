# .github/ Agent Context

Never edit this file except for minor corrections. Do not append new content.

GitHub workflow orchestration, path selection, release automation and merge gates.

## CI, E2E and merge rules

Read [CI.md](CI.md) before changing workflows, filters or policy.

- Preserve required `CI`, `E2E`, `jira-reference`, exact-head approval and merge
  queue gates. New cheap checks need a filter, a `ci.yml` job and `CI.needs`.
- Keep workflow names synchronized with all `workflow_run` lists and policy
  `workflow:` dispatch references when renaming. Reusable checks hold no path
  logic; the plan consumes `filters/ci-filters.yml`.
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
pre-commit run --all-files
```

Preserve `workflows/pre-commit.yaml`'s separate PR-range secret-scan consumer
with base-owned configuration; contribution-level scanning is covered at root.

| Touched area | Additional repository-root validation |
|---|---|
| Workflow/filter/policy | actionlint and applicable pre-commit hooks; inspect required-check reporting |
| CaaS selection | `OSAC_TEST_INFRA_DIR=/path/to/osac-test-infra uv run pytest -n 0 tests/unit/test_caas_ci_suite_policy.py` (companion checks skip without that checkout) |
| Flake reporting | `python3 -m unittest discover -s .github/scripts -p test_flake_report.py` |
| Umbrella publication/notifications | `python3 -m unittest discover -s .github/scripts -p test_umbrella_release.py -v` |
| Release version helpers and consumers | `python3 -m unittest discover -s .github/scripts -p test_release_versioning.py -v` |

Release helper tests use captured publication operations, not live registry or
Konflux evidence; inspect prerequisites and [boundaries](../docs/INTEGRATION-TESTING.md#release-automation).

## Component-hooks and sandbox regression smoke tests

Follow the [touched-area map and local commands](../.agentic-ci/AGENTS.md).
The workflow stays present on every PR and merge-group run, including unrelated
changes that skip execution.
Its path filter uses one brace-alternation entry because `predicate-quantifier:
every` combines separate entries with AND. Keep overlay/helper, test, and Python
setup paths in that entry. See the [sandbox coverage boundary](../docs/INTEGRATION-TESTING.md#agentic-ci-sandbox).

## Common workflow changes

### Add or change a cheap check

- Trace selection from `filters/ci-filters.yml` through the `workflows/ci.yml`
  plan and aggregate job. Test execution belongs in the reusable workflow;
  selection belongs in the caller, not in duplicated path logic.
- For a unit/integration matrix leg, inspect `ci-tests.yml` and
  `workflows/tests.yml` together. Ensure a selected failure reaches `CI` and
  an unrelated change still produces the required aggregate result.
- Use the validation map above for changed helpers and workflow syntax. Check
  both pull-request and merge-group consumers rather than only manual dispatch.

### Rename or extend an E2E workflow

- Read `CI.md`, then update policy dispatch references and `workflow_run`
  names together. A correct YAML file is not enough if the decision workflow
  no longer receives completion events or dispatches the intended suite.
- Keep default-branch decision code separate from approved-head execution.
  Preserve fork authorization, suite tier selection and required reporting;
  do not fix a missing result by weakening readiness or approval gates.

## Release safety

Nightly builds use provisional `sha-*` image tags while all build, unit,
integration, security, and E2E gates run. Promote images to release-looking
nightly tags only after every required gate passes; failed runs must not publish
release-looking tags. A manual `workflow_dispatch` run may opt out of the
unit, integration, or E2E gate individually via `skip_unit`/
`skip_integration`/`skip_e2e` (each surfaced in the tag message and the
success notification); the scheduled nightly cron never sets any of
them, and security tests are never skippable.

A real, permanent `<component>/vX.Y.Z` tag (release mode's `component_versions`
bump) must be pushed with real actor credentials, not the default
`GITHUB_TOKEN` — GitHub does not fire push-triggered workflows for a ref
created by `GITHUB_TOKEN`, so a component's own image/binary/proto publish
workflow would silently never run even though the tag exists.
Whatever creates such a tag must also verify each of that component's
downstream publish workflows actually started and succeeded before reporting
success; a tag existing is not evidence its publish happened.
