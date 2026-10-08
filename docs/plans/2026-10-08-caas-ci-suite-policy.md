# CaaS CI Suite Policy Implementation Plan

> **REQUIRED SUB-SKILL:** Use the executing-plans skill to implement this plan task-by-task.

**Goal:** Run only CaaS sanity in PRs and merge queue; run sanity, regression, and applicable references in periodics.

**Architecture:** Select suites explicitly in the two full-install callers. Keep manual overrides, but force test-infra PR-associated dispatches to sanity regardless of tier labels. The reusable runner must neither append references to sanity nor fall back from an empty tier to the complete suite.

**Tech Stack:** GitHub Actions YAML, Bash, pytest, PyYAML.

## Approved design and alternatives

Use caller-owned policy rather than event-dependent reusable defaults: callers know the trigger intent, and nightly/release callers already explicitly request `caas`. Directory selection and the sanity marker together prevent unintended collection. Periodics select `caas` with `sanity or regression or requires_caas or reference_common`. Manual runs default to sanity and retain suite/filter/marker overrides, except dispatches carrying a PR number, which remain sanity-only. Readiness, authorization, deployment and scheduling are unchanged. Netris/agentless workflows are provisioning workflows, not consumers of this pytest selector, and remain unchanged.

## Task 1: Selection contract tests (Unit/Contract, [DEV])

Create `tests/unit/test_caas_ci_suite_policy.py` in osac. Read the actual caller YAML, evaluate its limited suite-selection expressions, and check PR, merge_group, schedule, manual defaults and overrides. With `OSAC_TEST_INFRA_DIR` pointing at the companion checkout, check its caller and execute the actual runner's selection Bash against temporary tier directories. Assert no sanity reference expansion and failure on missing tiers. Run `OSAC_TEST_INFRA_DIR=<checkout> uv run pytest -n 0 tests/unit/test_caas_ci_suite_policy.py` from osac and observe expected failures before editing workflows. No cluster, APIs, providers or controllers run in this tier; it proves selection, not deployed E2E behavior.

## Task 2: Workflow changes

Modify osac `.github/workflows/e2e-caas-full-install.yml` and test-infra `.github/workflows/e2e-caas-full-install-caller.yml` for explicit trigger policy. Remove the now-unneeded label resolver job and its gate dependencies; select sanity directly for PR-associated dispatches. Modify test-infra `.github/workflows/e2e-caas-full-install.yml` to fail closed on empty requested tiers and only append references for full/regression selection. Re-run the contract tests until green. Update each `.github/AGENTS.md` plus osac `docs/INTEGRATION-TESTING.md` to document selection and test command.

## Task 3: Validation

Run focused tests with both repositories, Ruff on the new Python test, and pre-commit on changed files in each repository. Set `METERING_ADAPTER_URL=http://127.0.0.1:1` only for collection (no service calls are made). Collect `tests/e2e/caas/sanity -m sanity` and `tests/e2e/caas tests/e2e/references -m 'sanity or regression or requires_caas or reference_common'` from osac. Collection is not deployed E2E coverage: [QE] live execution needs provisioned CaaS, hub credentials, AAP, virtual BMHs, metering and cluster images. No live run is requested; report that boundary rather than claiming it passed. Review both diffs and preserve unrelated dirty checkouts. Commit and push both feature branches to contributor forks without opening PRs. Keep osac's temporary test-infra fork/branch references in a separate commit so they can be removed after test-infra merges.
