# New Ticket Workflow

Execute the following workflow phases in order. This is an
infrastructure/deployment component. Select relevant existing YAML, lint,
render or script checks for the changed behavior.

1. **Read and execute .ai-workflows/bugfix/skills/assess.md**
   The bug report is in `.ai-bot/issue.md`. Identify which files are
   affected (Helm charts, values files, scripts, prerequisites).
   Clarify ambiguous scope before destructive or unrelated changes.

2. **Read and execute .ai-workflows/bugfix/skills/diagnose.md**
   Identify the root cause without requiring a separate diagnosis artifact.

3. **Read and execute .ai-workflows/bugfix/skills/fix.md**
   Implement the minimal fix. Key constraints:
   - Do not edit component code under sibling mono-repo directories (e.g.
     `../fulfillment-service/`) from an installer-only change — use one `osac` PR.

4. **Validate changes**
   Select relevant existing checks from the installer root; use broader
   validation only when needed. Available commands include:

   ```bash
   yamllint --strict .
   pre-commit run --files <changed-files>
   make helm-lint
   make helm-validate
   ```

5. **Read and execute .ai-workflows/bugfix/skills/review.md**
   Self-review for schema consistency, values file alignment, namespace
   references. Correct attributable issues and rerun relevant checks;
   report unrelated failures or unavailable environments.

6. **Read and execute .ai-workflows/bugfix/skills/pr.md**
   Summarize changes, checks and limitations concisely. Separate reports are
   optional unless requested; preserve caller-required outputs at their specified
   paths and formats.
