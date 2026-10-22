# Agentic CI sandbox validation

Follow the repository root instructions. This overlay supplies early Autofix
feedback; ordinary GitHub CI remains authoritative.

## Touched-area map

| Area | Required validation | Command / boundary |
|---|---|---|
| Overlay selectors, shared change detection, setup or skip declarations | [DEV] Unit plus production component integration | Run `uv run --no-project --with 'pyyaml>=6.0' python tools/test/agentic-ci-validation-smoke.py` from the repository root, then the [production scenarios](../docs/INTEGRATION-TESTING.md#agentic-ci-sandbox) |
| Dependency discard paths | [DEV] Production component integration | Follow the [profiling and restoration requirements](../docs/INTEGRATION-TESTING.md#agentic-ci-sandbox) |
| Regression workflow or path filter | [DEV] Unit and workflow validation | Run the regression suite above, `bash tools/test/component-hooks-smoke.sh`, and actionlint; retain [PR and merge-group reporting](../.github/AGENTS.md#component-hooks-and-sandbox-regression-smoke-tests) |

Source `changed.sh` before changing directory. Preserve missing-base fallback,
pathspecs, and ignored-file behavior; consume the complete tracked/untracked stream.
See [testing boundaries and production evidence](../docs/INTEGRATION-TESTING.md#agentic-ci-sandbox).
Declare precise sandbox exceptions in `config.yml`.
