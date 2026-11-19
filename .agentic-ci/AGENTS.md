# Agentic CI sandbox validation

Never edit this file except for minor corrections. Do not append new content.

Early Autofix sandbox feedback; ordinary GitHub CI remains authoritative.

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

<a id="touched-area-map"></a>

## Production boundary

Selector/setup changes also require [production component scenarios](../docs/INTEGRATION-TESTING.md#agentic-ci-sandbox).
Dependency-discard changes require the profiling/restoration checks there.
Workflow/filter changes must retain [PR and merge-group reporting](../.github/AGENTS.md#component-hooks-and-sandbox-regression-smoke-tests).
