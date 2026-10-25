# How CI works

Three checks must pass before a pull request merges, and one approval must be on the commit that merges.

| Check | What it is | Where |
|---|---|---|
| `CI` | Everything cheap: linters, unit and integration tests, builds, chart checks, generated-code checks. Only the parts your change touches run. | `workflows/ci.yml` |
| `E2E` | The full-install end-to-end suites on a real cluster, about 100 minutes. | `workflows/e2e-pr.yml`, `workflows/e2e-queue.yml` |
| `jira-reference` | The title names a Jira issue (`OSAC-1234:`) or says `NO-ISSUE:`. | `workflows/jira-reference.yml` |

The approval is a normal GitHub review from someone with write access (and from a code owner when you touch a path
that has one in `CODEOWNERS`: the CI definition under `.github/`, and any folder a team has claimed). A new commit drops it; a clean rebase or "Update branch" keeps it. The approval is also
what starts the E2E: it runs in parallel with `CI` and does not wait for it, and both must pass. When the
checks are green and the approval is there, the pull request goes into the merge queue by itself: the approval turns on
auto-merge (`e2e-pr.yml`, job `Auto-merge`). To keep one pull request from merging, choose "Disable auto-merge" on it (it stays off) or
convert it to a draft. The queue runs `CI` and
`E2E` again on the result of merging, so what lands is what was tested.

## Optional: BMaaS Netris

The BMaaS Netris end-to-end test (a real Netris lab on an AWS box) is not part of `E2E` and is not
required: the policy marks it `manual_only`, so the decision never chooses it and the `E2E` check does not wait for it.
Start it on a pull request with the comment `/test netris` (any member of the organization, any approval state), or by
checking its box in the E2E comment of the pull request; the suite has its own line, `E2E / BMaaS Netris Full Install`, and
the decision runs it through `workflows/e2e-pr-netris.yml`, which calls `workflows/e2e-bmaas-netris-full-install.yml`. It can also be started from the Actions page
("E2E BMaaS Netris Full Install", Run workflow, with the pull request's number, repository, branch and commit). A label no longer
starts it. A new commit does not cancel a run that is already going.

## A merge queue entry that fails

The merge queue runs `CI` and the E2E suites on the same commit. When something that is required fails there, everything
that still runs for the entry is stopped at once, instead of the entry holding its place in the queue until the slowest
suite has ended (about 100 minutes). The job `result` of `e2e-queue.yml` (a shared workflow of osac-ci) watches the E2E
suites and the other required checks of the commit (`CI`, `jira-reference`; the unit and integration tests of every
component are jobs of the `CI` run). On the first failure it writes the required `E2E` check at once, `Failed: bmaas`
for a suite or `Cancelled: stopped, CI / <job> failed` when another workflow caused it, and cancels every run of the
commit that has not ended. GitHub then removes the entry. The jobs that were cancelled shut their clusters down after
that, which can take a few minutes, without holding the queue. Only the first failure is shown: the legs that were still
running are cancelled, so a second failure shows on the next attempt. Workflows that provide no required check (CodeQL,
dependency review) do not stop an entry. When every suite passed, `result` writes `E2E` as passed.

## Optional: CaaS regression tests on a pull request

A pull request gets the CaaS sanity tests (`tests/e2e/caas/sanity`); the regression tests (`tests/e2e/caas/regression`) run
every 5 hours on `main`. To try a new or changed regression test before it merges, start CaaS with both: check
**CaaS Full Install · sanity + regression** in the E2E comment of the pull request (any member of the organization, any
approval state), or comment `/test caas-full`. It is a second box of the CaaS suite, not a suite of its own: the same job
and the same line, `E2E / CaaS Full Install` (which then says `· full`), so the required `E2E` check counts it exactly
like the CaaS suite, and a CaaS run that passed this way satisfies it. It runs the same tests as the schedule (sanity and
regression, with the CaaS references).

- Asking for the same thing while it runs does nothing. Asking for the other version of CaaS while one runs (the sanity
  box while the full one runs, or the other way round) stops that run and starts the one asked for.
- A suite asked for by name runs again whatever it ended with; the bare `/test` only starts what has not passed.
- The decision never chooses it, so a pull request still gets the CaaS sanity tests alone by default, and the merge queue
  does not run it. VMaaS and BMaaS already run their regression tests on every pull request that needs their suite.
- In the policy it is `e2e-caas-full` (`variant_of: caas`, `tier: full`); the decision starts `e2e-pr-caas.yml` with the input
  `tier` (`full`), and that workflow passes it to the wrapper (`e2e-caas-full-install.yml`, input `tier`).

## Approvals after a rebase, and pull requests from forks

- A clean rebase, or "Update branch", keeps the approval and moves it to the new head. A rebase that needs conflict
  resolution drops it, even when the diff is the same, so the pull request needs a new approval.
- For a pull request from a fork, only a person with write access starts the `E2E`: the approval of CodeRabbit or of the
  OSAC CI bot does not count, because the `E2E` runs the fork's code with the repository's secrets.
- For a pull request from someone outside the organization, a person with triage access or above also adds the
  `ok-to-test` label (before approving, or re-run the failed `E2E` job after), or the suites refuse the fork.

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
| `osac-ui/` | generated TypeScript types, unit tests, lint (the UI and its Go proxy), typecheck |
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
| Waiting for an approval | Ask for a review. CodeRabbit's approval or a person with write access starts the E2E (on a pull request from a fork only a person with write access does); the author cannot approve their own pull request. |
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
signed) and only dependency files changed (a sweep every 15 minutes, run from the osac-ci repository, so the approval
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
  `e2e-queue.yml` (copy the `vmaas` job) and, for pull requests, a run-of-its-own workflow `e2e-pr-<suite>.yml` (copy
  `e2e-pr-vmaas.yml`, see below). The line on the pull request follows that run (waiting for a runner, running, then passed
  or failed, with a link to its log); a suite the policy did not pick does not run and has no line.
- **A suite as a run of its own:** every suite (its policy job names a `workflow:`: `e2e-pr-vmaas.yml`, `e2e-pr-caas.yml`, `e2e-pr-bmaas.yml`,
  `e2e-pr-netris.yml`) is not a job of `e2e-pr.yml`: the decision starts that workflow with `workflow_dispatch` for the approved commit, so a failed suite can be
  run again at once (GitHub's Re-run of its run, or `/test vmaas`) without waiting for the others, and the `E2E` check is
  still the sum of the suites' lines. The workflow takes the inputs `repo`, `sha`, `pr-number` (and ignores `fork-pr-author`
  and `fork-pr-author-association`, which the decision also sends), names its run
  `"E2E suite · <suite id> · #<pr> · <sha>"` (the format is how its end is recognised), starts with the shared `verify` job
  of osac-ci (`suite-inputs.yml`: the commit must be the head of the pull request; the fork author and association come
  from the pull request) and calls the suite's `e2e-<suite>-full-install.yml` with `needs: verify`. Its name goes into the
  `workflow_run` list of `e2e-pr.yml`. The merge queue still runs the suite as a job of `e2e-queue.yml`. To add a suite, copy
  `e2e-pr-vmaas.yml` (CaaS has the `tier` input to copy), add `workflow:` to its policy job, list the workflow's name in
  `e2e-pr.yml` and give `e2e-queue.yml` its job. Step (3) of "Add an E2E suite" above then means the queue job only.
- **The decision and the `E2E` check** (approval, CI result, the wording above) and the queue plan are the shared
  workflows of [osac-project/osac-ci](https://github.com/osac-project/osac-ci); `e2e-pr.yml` and `e2e-queue.yml` here are
  thin callers that keep the triggers and the suite jobs. The bot sweep runs from osac-ci itself (no file here). A member of the organization can start suites by hand with the comment `/test` (the suites the change needs) or
  `/test vmaas caas` (those suites), or from the comment the decision keeps on a pull request written by a member
  (a checkbox per suite; any approval state, a draft and a fork of a member included; a pull
  request by someone outside the organization gets no comment). The rules they apply are one page:
  osac-ci `docs/RULES.md`. The callers pin a full commit of an osac-ci release (the tag in a comment); the Dependabot group
  `osac-ci` opens one pull request per release, reviewed by wg-infra.
- **Emergency:** setting the repository variable `CI_GATING_ENABLED` to `false` makes `CI` run every part.
- `workflow_run` lists workflows by exact name. `e2e-pr.yml` lists `CI`, `E2E review relay`, `E2E comment` and the four `E2E <Suite> PR suite` workflows;
  renaming any of them needs the list changed.
- Changes to `.github/workflows`, `.github/actions`, `.github/scripts`, `.github/filters` and `CODEOWNERS` need a code
  owner's approval.
