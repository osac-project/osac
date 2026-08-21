# New Ticket Workflow

Execute the following workflow phases in order. This is an
infrastructure/deployment repo -- there are no unit tests. Validation
is structural (YAML lint, pre-commit, Helm lint, Helm template render).

1. **Read and execute .ai-workflows/bugfix/skills/assess.md**
   The bug report is in `.ai-bot/issue.md`. Identify which files are
   affected (Helm charts, values files, scripts, prerequisites).
   Do not ask clarifying questions -- make reasonable assumptions.

2. **Read and execute .ai-workflows/bugfix/skills/diagnose.md**
   Write your root cause analysis to `.ai-bot/diagnosis.md`.

3. **Read and execute .ai-workflows/bugfix/skills/fix.md**
   Implement the minimal fix. Key constraints:
   - Do not edit component code under sibling mono-repo directories (e.g.
     `../fulfillment-service/`) from an installer-only change — use one `osac` PR.

4. **Validate changes**
   Run all validation commands from the installer root in sequence. If any
   fail, revise your fix and revalidate (up to 5 iterations):

   ```bash
   yamllint --strict .
   pre-commit run --all-files
   make helm-lint
   make helm-validate
   ```

5. **Read and execute .ai-workflows/bugfix/skills/review.md**
   Self-review for schema consistency, values file alignment, namespace
   references. If issues found, correct them, revalidate, and re-review
   (up to 4 iterations).

6. **Read and execute .ai-workflows/bugfix/skills/pr.md**
   Write PR description to `.ai-bot/pr.md` with root cause and affected
   components.
