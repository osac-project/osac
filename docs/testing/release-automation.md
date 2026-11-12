# Release automation

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Inspect test source for individual cases.

Touched-area requirements: [workflow guide](../../.github/AGENTS.md#release-safety).

This is **[DEV] Unit** coverage: the Python unittest suites under
`.github/scripts/` run the umbrella-release helpers and the workflow's actual
notification Bash against synthetic Helm archives, with GitHub CLI and Slack
requests captured by local fakes. For the invocation and its prerequisites,
see the [release-safety](../../.github/AGENTS.md#release-safety) touched-area
map.

Cases cover package-inventory and release-note edge cases, failed publication,
nightly/skip/cancellation conditions, stable Latest selection, and the
workflow's immutable-version guard.

These tests do not validate hosted GitHub scheduling, real Slack delivery or
rendering, or Konflux build/policy/staging operations. Those boundaries require
an authorized hosted run; local mocks do not establish that coverage.
