# .github/ Agent Context

This area is part of the OSAC monorepo, not an isolated project. Its workflow
and automation changes may affect other components. Apply the repository-wide
rules in [`../AGENTS.md`](../AGENTS.md), consider downstream effects, and
follow the instructions for every affected component.

## CI, E2E and merge rules

Read [`.github/CI.md`](CI.md) before changing a workflow. In short: three required checks (`CI`, `E2E`, `jira-reference`)
plus a native approval on the head commit, and the merge queue.

- `workflows/ci.yml` runs the cheap checks as reusable workflows (`helm-lint.yaml`, `build-image.yaml`, ...) and as
  one matrix of unit and integration test legs (`ci-tests.yml` lists them, `workflows/tests.yml` runs a leg), and ends
  in the job `CI`. A leg that is not needed has no job, so the pull request shows no skipped row for it. The reusable workflows hold no path logic; `ci.yml`'s `plan` job reads
  `filters/ci-filters.yml`. A new check needs a filter, a job in `ci.yml` and an entry in the `needs` of `CI`.
- `workflows/e2e-pr.yml` (a thin caller of the shared osac-ci workflows, pinned to a release commit) decides, from the default branch with the repository token, when the full-install E2E starts for a
  pull request and keeps the single `E2E` check up to date (plus one line per suite that runs, named by its `check` in `e2e-policy.yml`, such as `E2E / VMaaS Full Install`). The lines are check runs of the OSAC CI app (a token minted from the organization secrets `OSAC_CI_APP_ID` and `OSAC_CI_APP_PRIVATE_KEY`, with the checks permission), so the page draws them with a spinner; `E2E` is required from that app only: approved on the exact head commit by a person with write
  access or CodeRabbit (only a person with write access for a pull request from a fork), a suite affected (`e2e-policy.yml`), and no cheap check already failed. It does not wait for `CI`: the E2E runs in parallel with it and both are required for the merge. It checks out no pull request code (only the policy and
  filters of the default branch), and the suites it calls test the approved commit. `workflows/e2e-queue.yml` runs the
  suites for a merge queue entry. `workflows/e2e-review-relay.yml` only finishes, so a review wakes `e2e-pr.yml`. `workflows/e2e-comment.yml` posts the placeholder of the control comment when a pull request opens (its finished run is deleted by `e2e-pr.yml`). The same run turns on auto-merge for a pull request that has an approval that counts, so it adds no check.
- The `e2e-<suite>-full-install.yml` workflows hold one job each, no gating; they run on a schedule (every 5 hours), by hand
  and when called.
- `workflow_run` lists workflows by exact name (`CI`, `E2E review relay`, `E2E comment` in `e2e-pr.yml`): keep the list in step when one is
  renamed.
- Path filters: `tests/e2e/**` is the full-install suite and must still make the E2E apply (`e2e-suite` filter). Do not fold
  `tests/e2e` back into an ignore list.
- No Prow labels (`/lgtm`, `/approve`, `/hold`, `/e2e-ready`, `/ok-to-test`) are read. Prow still sets `jira/valid-reference`
  (the `jira-reference` check reads it) and runs the cherry-pick plugin.
- The one comment command is `/test` (alone: the suites the change needs; `/test vmaas caas`: those suites), read by
  `e2e-pr.yml` through the shared decision for members of the organization. The old slash-command workflow (`/test <workflow>`,
  `/retest`, `/cancel`, `optional-workflows.yml`) is gone: re-run a check from its page, and start Netris with `/test netris`.
  A pull request from someone outside the organization needs the `ok-to-test` label added by a person with triage access.

## Dependency updates and who may change the pipeline

- `workflows/bot-approve.yml` approves Dependabot and Konflux pull requests as the OSAC CI app (`osac-ci[bot]`) when no
  person needs to read them (every commit the bot's own, plain and signed; only dependency files changed). It runs every
  15 minutes and looks at every open dependency update, so an approval comes within about a quarter of an hour. It is a
  scheduled run on purpose: a run started by a Dependabot pull request only sees Dependabot's secrets, so the app's key
  would have to be handed to Dependabot too. It is a thin caller of the shared `bot-sweep` workflow of osac-project/osac-ci
  (`osac-ci bot-decide`), pinned to a full commit SHA of a release. The approval is made with a token minted from the
  organization secret `OSAC_CI_APP_PRIVATE_KEY` with the pull-request permission only. For a Konflux pull request the
  sweep also prepares it as the same app, with a token minted for that step (pull-requests, issues and actions write): the
  title prefix, the `jira/valid-reference` label the Jira reference check needs, and the approval of its held workflow
  runs once the bot policy approves the commit (an edit by the workflow's own token starts no workflow; one by the app
  does). It used the secret `MERGE_QUEUE_TOKEN` for this before; the shared sweep still accepts that secret, but this
  workflow no longer passes it.
- Changes to the files that define the checks (`.github/workflows`, `actions`, `scripts`, `filters`, `e2e-policy.yml`,
  `CODEOWNERS`, `.pre-commit-config.yaml`; Markdown in them excepted) need an approval from `@osac-project/wg-infra`
  (see `CODEOWNERS`). Only the osac-ui lint and typecheck workflows are also approvable by the osac-ui maintainers
  (`osac-ui/OWNERS`); every other workflow reports a required check, uses secrets or publishes an image, so it stays with
  wg-infra.

## CaaS suite selection and coverage

The CaaS full-install caller runs only `caas/sanity` with `-m sanity` for PRs
and merge queue. Periodics run `caas` (sanity + regression) and applicable
reference tests with `-m 'sanity or regression or requires_caas or reference_common'`.
Manual dispatch defaults to sanity and permits suite/filter/marker overrides.
The test-infra reusable runner must not append references to sanity or widen
an empty requested tier to the full suite. Readiness and fork authorization
are independent of suite selection.

| Area | Required coverage | Command (repository root) | Boundary |
|---|---|---|---|
| CaaS CI suite policy | [DEV] Unit/Contract selection checks | `OSAC_TEST_INFRA_DIR=/path/to/osac-test-infra uv run pytest -n 0 tests/unit/test_caas_ci_suite_policy.py` | Evaluates caller YAML and executes reusable path-selection Bash locally. No deployed services; companion checks skip if the checkout is not specified. |

## Component-hooks and sandbox regression smoke tests

Follow the [touched-area map and local commands](../.agentic-ci/AGENTS.md).
The workflow stays present on every PR and merge-group run, including unrelated
changes that skip execution.
Its path filter uses one brace-alternation entry because `predicate-quantifier:
every` combines separate entries with AND. Keep overlay/helper, test, and Python
setup paths in that entry. See the [sandbox coverage boundary](../docs/INTEGRATION-TESTING.md#agentic-ci-sandbox).

## Release safety

Nightly builds use provisional `sha-*` image tags while all build, unit,
integration, security, and E2E gates run. Promote images to release-looking
nightly tags only after every required gate passes; failed runs must not publish
release-looking tags. A manual `workflow_dispatch` run may opt out of the
unit, integration, or E2E gate individually via `skip_unit`/
`skip_integration`/`skip_e2e` (each surfaced in the tag message and the
Slack success notification); the scheduled nightly cron never sets any of
them, and security tests are never skippable.

A real, permanent `<component>/vX.Y.Z` tag (release mode's `component_versions`
bump) must be pushed with real actor credentials, not the default
`GITHUB_TOKEN` — GitHub does not fire push-triggered workflows for a ref
created by `GITHUB_TOKEN`, so a component's own image/binary/proto publish
workflow would silently never run even though the tag exists.
Whatever creates such a tag must also verify each of that component's
downstream publish workflows actually started and succeeded before reporting
success; a tag existing is not evidence its publish happened.
