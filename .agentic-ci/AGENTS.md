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

Inspect changed selector and overlay paths and run the relevant existing lint
checks from the repository root. Workflow changes use actionlint and applicable
pre-commit hooks. Preserve path selection, failure propagation and required-check
reporting; do not add meta-tests for routine wiring changes.

<a id="touched-area-map"></a>

## Production boundary

Use the [sandbox guide](../docs/INTEGRATION-TESTING.md#agentic-ci-sandbox) for
production runner boundaries. Run an authorized production scenario when the
changed setup or runtime behavior needs it; report unavailable environments
rather than treating local checks as production evidence.
