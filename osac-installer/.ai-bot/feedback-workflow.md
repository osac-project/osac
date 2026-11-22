# Feedback Workflow

Read and execute .ai-workflows/bugfix/skills/feedback.md with
the following repo-specific context.

## Context Recovery

Use existing task context when needed to understand earlier decisions.

## Feedback Handling Rules

1. **Mono-repo boundaries**: If feedback asks you to change a file in a sibling
   component directory (e.g. `../osac-operator/`), make the change there and land
   one PR at the `osac` repo root. **osac-ui** is external (OCI chart).

2. **Values consistency**: If feedback applies to one values file, check
   whether other values files (development, vmaas-ci, caas-ci)
   need the same change. Call this out in your response.

## Post-Change Validation

Select relevant existing checks from the installer root for the changed behavior:

```bash
yamllint --strict .
pre-commit run --files <changed-files>
make helm-lint
make helm-validate
```

See `Makefile` for the underlying helm lint/template commands these targets execute.

## Response

Summarize changes, relevant checks and limitations concisely. Report unrelated
failures without expanding the fix. Session reports are optional unless
requested; preserve caller-required outputs at their specified paths and formats.
Keep PR text and response JSON concise.
