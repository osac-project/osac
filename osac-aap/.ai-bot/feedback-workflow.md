# osac-aap: PR Feedback Workflow

## Overview

Address PR review feedback on Ansible automation code. This workflow handles
reviewer comments, implements changes, validates with ansible-lint, and
updates session artifacts for multi-round review continuity.

## Workflow

Read and execute `.ai-workflows/bugfix/skills/feedback.md` with the
settings below.

### Settings

```yaml
lint_command: uv run ansible-lint
```

### Artifact Paths

All artifact paths (`.artifacts/bugfix/{issue}/`) should use `.ai-bot/`
instead.

## Process

### Step 1: Gather Review Comments

Retrieve comments from the PR. Sources (in priority order):
1. Task file with structured comments (if provided by orchestrator)
2. PR number via `gh api repos/{owner}/{repo}/pulls/{pr_number}/comments`
3. User-provided context

### Step 2: Recover Prior Context

Read these files from `.ai-bot/` if they exist:
- `session-context.md` -- original implementation decisions
- `implementation-notes.md` -- file-by-file rationale
- `root-cause.md` -- original root cause analysis

### Step 3: Understand the Feedback

For each comment, determine:
- Is it a code change request, test addition, explanation, or design
  challenge?
- Does it conflict with the original design decisions?
- Does it involve cross-repo coordination (osac-operator,
  fulfillment-service)?

### Step 4: Implement Changes

Apply changes following the Ansible conventions:
- FQCN for all modules
- `name:` on every task
- Underscores in role names
- Include `osac.service.common` for remote K8s operations

If a reviewer suggests something that contradicts the project conventions
(e.g., using bare module names, hyphenated role names), explain the
convention rather than adopting the suggestion.

### Step 5: Validate

```bash
# Mandatory
uv run ansible-lint

# Syntax check modified playbooks
ansible-playbook --syntax-check playbook_osac_<modified>.yml

# Pre-commit
pre-commit run --all-files

# Helm (if charts/ changed)
helm lint charts/aap/
```

### Step 6: Update Session Context

Append a feedback round section to `.ai-bot/session-context.md`:

```markdown
## Feedback Round N
**Comments addressed**: [@reviewer on file:line, ...]
**Changes made**:
- [Description] (file:line) -- [why this approach]
**Suggestions declined**:
- [@reviewer on file:line]: [reason]
**Tests updated**: [list changes or "no test changes needed"]
```

### Step 7: Write Comment Responses

Write `.ai-bot/comment-responses.json` mapping each comment to a response:

```json
[
  {"comment_id": 123, "response": "Applied FQCN as suggested."},
  {"comment_id": 456, "response": "Kept underscore naming per project convention."}
]
```

## Common Review Feedback Patterns

These are frequently seen in osac-aap reviews and how to handle them:

| Feedback | Action |
|----------|--------|
| "Use FQCN" | Replace bare module with `ansible.builtin.*` or `kubernetes.core.*` |
| "Missing task name" | Add descriptive `name:` field |
| "Use underscores" | Rename to underscores in role dir, `meta/osac.yaml`, and strategy |
| "Missing kubeconfig" | Add `osac.service.common` include before remote K8s ops |
| "Update meta/osac.yaml" | Ensure `implementation_strategy` matches role directory name |
| "Cross-repo needed" | Document in PR description, do not attempt changes in other repos |
| "Add to ansible-lint-ignore" | Only if genuinely unavoidable; explain in PR comment |
| "Stale vendor" | Flag for human review; do not modify `vendor/` automatically |
