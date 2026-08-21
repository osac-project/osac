@AGENTS.md

## Claude Code Tooling

This is an infrastructure/deployment repository (Helm-based) with no Go code, container builds, or unit tests. All validation is structural (YAML lint, Helm lint/template). Legacy overlay directories under `overlays/` store environment-specific secret files (no kustomization.yaml).

### Read Tool

Use for reading YAML manifests, Helm chart files, and scripts. Primary targets:

- `charts/osac/values.yaml`, `charts/osac/values.schema.json` -- Helm default values and schema
- `values/<env>/values.yaml` -- Helm environment-specific values
- `scripts/*.sh`, `scripts/*.py` -- Automation scripts
- `prerequisites/` -- Operator manifests

### Edit Tool

Use for updating Helm chart files, YAML manifests, shell scripts. Common edits:

- New/changed keys in `charts/osac/values.yaml` -- always add a matching property in `charts/osac/values.schema.json`
- Script logic in `scripts/*.sh` (follow `set -euo pipefail`)

Never use Edit for component code under sibling mono-repo directories (e.g.
`../fulfillment-service/`). Edit those paths from the `osac` repo root in one PR.

### Write Tool

Use sparingly. Most work is editing existing files. Valid use cases:

- New prerequisite manifests in `prerequisites/`
- New Helm values files in `values/<env>/`
- Session artifacts (`.ai-bot/diagnosis.md`, `.ai-bot/pr.md`)

Never use Write for component code under sibling mono-repo directories (e.g.
`../fulfillment-service/`). Edit those paths from the `osac` repo root in one PR.

### Bash Tool

Use for validation commands, Helm operations, git operations. Commands run relative to the installer repo root (cwd). Always specify `-n <namespace>` explicitly in `oc` commands on shared clusters.

Example commands (run from the installer repo root):

```bash
# Validation suite (run in order, all must pass)
yamllint --strict .
pre-commit run --all-files
make helm-lint
make helm-validate

# Git operations (always from installer root)
git status
git add file1 file2
git commit -s -m "OSAC-XXXX: description

Assisted-by: Claude Code <noreply@anthropic.com>"
git push fork <branch>
```
