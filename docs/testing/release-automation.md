# Release automation

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Follow this guide for suite setup and limits; inspect test source for
individual cases. Commands and inline paths are relative to the repository root.

Touched-area requirements: [workflow guide](../../.github/AGENTS.md#release-safety).

Run from the repository root with PyYAML 6.0.3, Bash, and jq installed:

```sh
python3 -m unittest discover -s .github/scripts -p test_umbrella_release.py -v
```

This is **[DEV] Unit** coverage. Synthetic Helm archives exercise the package
inventory and release-note helpers. The tests execute the workflow's actual
notification Bash with GitHub CLI and Slack requests captured by local fakes;
the `uv` fake uses the current Python interpreter without fetching dependencies.
Cases cover reused subchart versions, CRDs, standalone CSI charts, fallback
metadata, failed publication, nightly/skip/cancellation conditions, and the
actual chart release step outcome when its job allows errors. Existing release
tests also cover stable Latest selection and check that the workflow retains
its immutable-version guard.

These tests do not validate hosted GitHub scheduling, real Slack delivery or
rendering, or Konflux build/policy/staging operations. Those boundaries require
an authorized hosted run; local mocks do not establish that coverage.
