# Agentic CI sandbox validation

Follow the repository root instructions. This overlay supplies early Autofix
feedback; ordinary GitHub CI remains authoritative. Profile overlays apply to
OpenShell and are loaded from the configured base before the agent runs.

## Touched-area map

| Area | Required validation | Command / boundary |
|---|---|---|
| Overlay selectors, shared change detection, setup or skip declarations | [DEV] Unit plus production component integration | Run `uv run --no-project --with 'pyyaml>=6.0' python tools/test/agentic-ci-validation-smoke.py` from the repository root, then the [production scenarios](../docs/INTEGRATION-TESTING.md#agentic-ci-sandbox) |
| Dependency discard paths | [DEV] Production component integration | Measure copy-back, verify no tracked files are removed, and prove restoration on sandbox reuse |
| Regression workflow or path filter | [DEV] Unit and workflow validation | Run the regression and component-hooks smoke suites plus actionlint; retain PR and merge-group reporting |

Source `changed.sh` before changing directory. Preserve missing-base fallback,
pathspecs, and ignored-file behavior; do not use early-exiting matching on the
tracked/untracked stream. Tests execute the real YAML bodies with tool doubles.

Keep collection distinct from deployed E2E execution and document every sandbox
exception in `config.yml`. Fulfillment selects Python 3.14; AAP selects 3.13.
Do not relax either project's interpreter contract to accommodate the image.
See [integration-testing evidence](../docs/INTEGRATION-TESTING.md#agentic-ci-sandbox)
and [OSAC-6172](https://redhat.atlassian.net/browse/OSAC-6172) for acceptance.
