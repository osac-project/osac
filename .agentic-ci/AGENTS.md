# Agentic CI sandbox validation

Never edit this file except for minor corrections. Do not append new content.

Follow the repository root instructions. This overlay supplies early Autofix
feedback; ordinary GitHub CI remains authoritative.

## Helper contract

- Source `.agentic-ci/changed.sh` and call `changed` from the repository root.
  Change directory only after selection or in a subshell.
- Preserve missing-base fallback, pathspecs and ignored-file behavior; consume
  the full tracked/untracked stream.
- Declare precise sandbox exceptions in `config.yml`.

## Local validation

From the repository root, after selector, overlay or regression changes:

```bash
uv run --no-project --with 'pyyaml>=6.0' python tools/test/agentic-ci-validation-smoke.py
bash tools/test/component-hooks-smoke.sh
```

These smoke checks do not replace the production scenarios below. Workflow
changes also require actionlint and the applicable root pre-commit checks.

## Touched-area map

| Area | Required validation | Command / boundary |
|---|---|---|
| Overlay selectors, shared change detection, setup or skip declarations | [DEV] Unit plus production component integration | Run the local smoke checks above, then the [production scenarios](../docs/INTEGRATION-TESTING.md#agentic-ci-sandbox) |
| Dependency discard paths | [DEV] Production component integration | Follow the [profiling and restoration requirements](../docs/INTEGRATION-TESTING.md#agentic-ci-sandbox) |
| Regression workflow or path filter | [DEV] Unit and workflow validation | Run the local checks and actionlint; retain [PR and merge-group reporting](../.github/AGENTS.md#component-hooks-and-sandbox-regression-smoke-tests) |

See [testing boundaries and production evidence](../docs/INTEGRATION-TESTING.md#agentic-ci-sandbox).
