# How CI works

Three checks must pass before a pull request merges, and one approval must be on the commit that merges.

| Check | What it is | Where |
|---|---|---|
| `CI` | Everything cheap: linters, unit and integration tests, builds, chart checks, generated-code checks. Only the parts your change touches run. | `workflows/ci.yml` |
| `E2E` | The full-install end-to-end suites on a real cluster, about 100 minutes. | `workflows/e2e-pr.yml`, `workflows/e2e-queue.yml` |
| `jira-reference` | The title names a Jira issue (`OSAC-1234:`) or says `NO-ISSUE:`. | `workflows/jira-reference.yml` |

The approval is a normal GitHub review from someone with write access (and from a code owner when you touch `.github/`,
`proto/` or the installer, see `CODEOWNERS`). A new commit drops it; a clean rebase or "Update branch" keeps it. The approval is also
what starts the E2E: it runs in parallel with `CI` and does not wait for it, and both must pass. When the
checks are green and the approval is there, the author adds the pull request to the merge queue. The queue runs `CI` and
`E2E` again on the result of merging, so what lands is what was tested.

## Which tests a directory needs

A change under a directory runs the parts below, and a part that applies has to pass before `CI` does: there is no way
to merge around it (only an organization admin bypassing the rule). Everything else shows as skipped inside the run. Every
change also runs `pre-commit` and `dependency-review`. `ci-tests.yml` and `filters/ci-filters.yml` are the source.

| Directory | Required parts of `CI` |
|---|---|
| `fulfillment-service/` | Go and proto checks, Python checks, unit tests, integration test, Helm chart lint |
| `osac-operator/` | unit tests and image build, integration test, Helm chart lint and CRD sync |
| `bare-metal-fulfillment-operator/` | unit tests and image build, integration test, Helm chart lint and CRD sync |
| `osac-aap/` | `ansible-lint`, integration test, Helm chart lint |
| `osac-installer/` | integration test, Helm chart lint |
| `osac-metering/` | unit tests (service, adapters, schema), Helm chart lint |
| `osac-ui/` | unit tests, lint (the UI and its Go proxy), typecheck |
| `osac-csi-driver/` | Helm chart lint only (its Go tests are not run in CI yet) |
| `proto/` | generated-code check, and the tests of the components that use it |
| `tests/e2e/` and the E2E workflows | the `E2E` suites the change affects (`e2e-policy.yml`) |

A rule that a change must also add or change tests is not enforced: `CI` checks that the tests which exist pass.

## What you see on a pull request

`CI` is one check. Open it to see which parts ran: a part your change does not touch shows as skipped inside that run,
not as a separate check.

`E2E` is a check, and it always says why it is where it is (with a spinner while it waits or runs):

| The check says | What to do |
|---|---|
| Waiting for an approval | Ask for a review. CodeRabbit's approval or a person with write access starts the E2E; the author cannot approve their own pull request. |
| CI failed | Fix CI and push; the E2E does not start on a commit whose cheap checks have already failed. |
| Changes requested | The reviewer's request has to be resolved first. |
| Draft | Mark the pull request ready. |
| Running | Open the check for the run. |
| Not affected by this change | Docs, tests or other files no suite exercises. Nothing runs. |

The E2E runs once for a commit. To run it again for the same commit, re-run the finished run from the Actions page
(`E2E for pull requests`); a new commit runs it again by itself. A fix pushed after the approval needs a new review,
because a push drops the approval, and then runs the E2E once.

## Dependency updates

Dependabot and Konflux pull requests are approved by the `osac-ci` bot account when every commit is the bot's own (plain,
signed) and only dependency files changed (`workflows/bot-approve.yml`, which looks every 15 minutes, so the approval
comes within about a quarter of an hour). Anything else gets a person.

## Changing the pipeline

- **Which parts run for which paths:** `filters/ci-filters.yml` is the one place. `ci.yml` has a `plan` job that reads it.
- **Add a check:** write it as a reusable workflow (`on: workflow_call`, no path logic inside), add a filter for it to
  `ci-filters.yml`, add a job to `ci.yml` that runs when the filter matched, and add that job to the `needs` of `CI`.
- **Which suites a change needs:** `e2e-policy.yml` (suite, the name of its line on the pull request, and the filters that
  decide it). `e2e-pr.yml` and `e2e-queue.yml` read it; `e2e-pr.yml` reads it from the default branch, never from the pull
  request. A suite whose filters the change does not match does not run and has no line, so a suite for one area (such as
  netris) costs the other pull requests nothing.
- **Add an E2E suite:** (1) an entry in `e2e-policy.yml` with its `suite` id, a `check` name (`E2E / <Suite> Full Install`)
  and its `filters` / `filters_any`; (2) its workflow `e2e-<suite>-full-install.yml`; (3) one job named after the suite id in
  `e2e-pr.yml` and in `e2e-queue.yml` (copy the `vmaas` job). The line on the pull request follows the job of that name
  (waiting for a runner, running, then passed or failed, with a link to its log); a suite the policy did not pick does not
  run and has no line.
- **The decision and the `E2E` check** (approval, CI result, the wording above) live in
  [osac-project/osac-ci](https://github.com/osac-project/osac-ci). The workflows pin its actions to a full commit; bump
  the pin in a reviewed pull request.
- **Emergency:** setting the repository variable `CI_GATING_ENABLED` to `false` makes `CI` run every part.
- `workflow_run` lists workflows by exact name. `e2e-pr.yml` lists `CI` and `E2E review relay`; renaming either needs the
  list changed.
- Changes to `.github/workflows`, `.github/actions`, `.github/scripts`, `.github/filters` and `CODEOWNERS` need a code
  owner's approval.
