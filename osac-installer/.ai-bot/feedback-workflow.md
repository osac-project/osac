# Feedback Workflow

Read and execute .ai-workflows/bugfix/skills/feedback.md with
the following repo-specific context.

## Context Recovery

Read `.ai-bot/session-context.md` and `.ai-bot/implementation-notes.md`
to understand the prior session's decisions and changes.

## Feedback Handling Rules

1. **Mono-repo boundaries**: If feedback asks you to change a file in a sibling
   component directory (e.g. `../osac-operator/`), make the change there and land
   one PR at the `osac` repo root. **osac-ui** is external (OCI chart).

2. **Values consistency**: If feedback applies to one values file, check
   whether other values files (development, vmaas-ci, caas-ci)
   need the same change. Call this out in your response.

## Post-Change Validation

After addressing all review comments, run the full validation suite from the installer root:

```bash
yamllint --strict .
pre-commit run --all-files
make helm-lint
make helm-validate
```

See `Makefile` for the underlying helm lint/template commands these targets execute.

## Session Artifacts

Update `.ai-bot/session-context.md` with a summary of this feedback
round (what changed, what was kept, why).

Write `.ai-bot/comment-responses.json` with per-comment response
summaries matching the comment IDs from the task file.
