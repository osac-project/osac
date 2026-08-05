# osac-aap: New Ticket Bugfix Workflow

## Overview

Multi-phase bugfix workflow for Ansible automation code. This repo uses
`ansible-lint` as its primary validation gate -- there are no compiled
artifacts or unit test frameworks. Integration tests exist but require a
kind cluster and are heavy-weight.

## Workflow

Read and execute `.ai-workflows/bugfix/skills/unattended.md` with the
settings below.

### Settings

```
branch: stay on the current branch (already created by the orchestration system)
lint_command: uv run ansible-lint
max_retries: 3
```

### Artifact Paths

All artifact paths (`.artifacts/bugfix/{issue}/`) should use `.ai-bot/`
instead. Write the PR description to `.ai-bot/pr.md`.

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
3. Understand the data flow: osac-operator -> EDA event -> playbook ->
   implementation_strategy -> template role -> K8s resources
4. Check `.claude/rules/playbook-patterns.md` and `.claude/rules/networking-cudn.md`
   for domain-specific patterns
5. Write root cause analysis to `.ai-bot/root-cause.md`

### Phase 2: Fix

1. Make the code change following all conventions from `.ai-bot/instructions.md`
2. Key rules to remember:
   - FQCN for all modules (`ansible.builtin.*`, `kubernetes.core.*`)
   - Every task needs a `name:` field
   - Underscores in role names and `implementation_strategy`
   - Include `osac.service.common` before remote K8s operations
3. If adding a new template role, create `meta/osac.yaml`
4. Write implementation notes to `.ai-bot/implementation-notes.md`

**Test file exception**: This is an Ansible project without a traditional
unit test framework. If the fix touches workflow logic that has integration
test coverage (check `tests/integration/targets/`), update the relevant
baseline or override test. If no matching integration test target exists,
document why tests were not added in implementation notes.

### Phase 3: Validate

Run in this order:

```bash
# 1. Lint (mandatory -- must pass)
uv run ansible-lint

# 2. Syntax check any modified playbooks
ansible-playbook --syntax-check playbook_osac_<modified>.yml

# 3. Pre-commit hooks
pre-commit run --all-files

# 4. Helm lint (only if charts/ changed)
helm lint charts/aap/
```

If ansible-lint fails, fix the violations and re-run. Common issues:
- Missing FQCN: use `ansible.builtin.<module>` not bare `<module>`
- Missing task name: add `name:` to every task
- Role name with hyphens: use underscores

If the fix touches `collections/ansible_collections/osac/workflows/` or
`collections/ansible_collections/osac/service/roles/`, also verify
integration tests still pass conceptually by reviewing the relevant test
targets in `tests/integration/targets/`.

### Phase 4: Self-Review

Review the diff against:
- All conventions in `.ai-bot/instructions.md`
- Patterns documented in `.claude/rules/playbook-patterns.md`
- The `.ansible-lint.yml` skip/warn lists (don't introduce new violations)
- The `.ansible-lint-ignore` file (don't add to it without justification)
- Cross-repo impact (does this change require coordinated changes in
  osac-operator or fulfillment-service?)

Write review findings to `.ai-bot/review.md`.

### Phase 5: Document

Write PR description to `.ai-bot/pr.md` including:
- What changed and why
- Which collections/roles were modified
- Cross-repo dependencies (if any)
- How to test (e.g., which integration test target exercises this code)
- Checklist from CLAUDE.md PR Checklist section

Write session context to `.ai-bot/session-context.md`.
