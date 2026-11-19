# osac-aap: New Ticket Bugfix Workflow

## Overview

Multi-phase bugfix workflow for Ansible automation code. This repo uses
`ansible-lint` and existing unit/integration suites. Select relevant checks
for the changed behavior; integration tests require a Kind cluster.

## Workflow

Read and execute `.ai-workflows/bugfix/skills/unattended.md` with the
settings below.

### Settings

```
branch: stay on the current branch (already created by the orchestration system)
lint_command: uv run ansible-lint
```

### Artifact Paths

Create separate reports only when requested; preserve outputs required by the
calling workflow at its specified paths and formats.

## Phase Details

### Phase 1: Diagnose

1. Read the ticket description from `.ai-bot/issue.md`
2. Identify which files are involved:
   - Playbooks: `playbook_osac_*.yml` at repo root
   - Template roles: `collections/ansible_collections/osac/templates/roles/`
   - Service roles: `collections/ansible_collections/osac/service/roles/`
   - Workflow playbooks: `collections/ansible_collections/osac/workflows/playbooks/`
   - Filter plugins: `collections/ansible_collections/osac/service/plugins/filter/`
   - Custom modules: `collections/ansible_collections/osac/service/plugins/modules/`
3. Understand the data flow: osac-operator -> osac_job_vars -> playbook ->
   implementation_strategy -> template role -> K8s resources
4. Check `.claude/rules/playbook-patterns.md` and `.claude/rules/networking-cudn.md`
   for domain-specific patterns

### Phase 2: Fix

1. Make the code change following all conventions from `.ai-bot/instructions.md`
2. Key rules to remember:
   - FQCN for all modules (`ansible.builtin.*`, `kubernetes.core.*`)
   - Every task needs a `name:` field
   - Underscores in role names, `implementation_strategy`, and `fabric_manager`/`k8s_manager`
   - Include `osac.service.common` before remote K8s operations
3. If adding a new template role, create `meta/osac.yaml`

Add meaningful behavior or regression coverage where needed, reusing existing
unit or integration suites. Do not require a new test for every change or a
separate artifact explaining routine decisions.

### Phase 3: Validate

Select relevant existing checks for the changed behavior:

```bash
# 1. Lint (mandatory -- must pass)
uv run ansible-lint

# 2. Syntax check any modified playbooks
ansible-playbook --syntax-check playbook_osac_<modified>.yml

# 3. Pre-commit hooks
pre-commit run --files <changed-files>

# 4. Helm lint (only if charts/ changed)
helm lint charts/aap/
```

Fix attributable lint failures and report unrelated findings. Common issues:
- Missing FQCN: use `ansible.builtin.<module>` not bare `<module>`
- Missing task name: add `name:` to every task
- Role name with hyphens: use underscores

If the fix touches `collections/ansible_collections/osac/workflows/` or
`collections/ansible_collections/osac/service/roles/`, inspect the relevant integration targets in
`tests/integration/targets/`. Source review is not test execution; report
unavailable runtime checks honestly.

### Phase 4: Self-Review

Review the diff against:
- All conventions in `.ai-bot/instructions.md`
- Patterns documented in `.claude/rules/playbook-patterns.md`
- The `.ansible-lint.yml` skip/warn lists (don't introduce new violations)
- The `.ansible-lint-ignore` file (don't add to it without justification)
- Cross-repo impact (does this change require coordinated changes in
  osac-operator or fulfillment-service?)


### Phase 5: Document

Summarize the changes, relevant checks and limitations concisely. Note affected
component contracts when relevant. Separate reports are optional unless
requested; preserve caller-required outputs at their specified paths and formats.
