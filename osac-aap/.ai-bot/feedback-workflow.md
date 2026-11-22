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

Create separate reports only when requested; preserve outputs required by the
calling workflow at its specified paths and formats.

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

Select relevant existing checks and report unavailable or unrelated failures.

```bash
# Mandatory
uv run ansible-lint

# Syntax check modified playbooks
ansible-playbook --syntax-check playbook_osac_<modified>.yml

# Pre-commit
pre-commit run --files <changed-files>

# Helm (if charts/ changed)
helm lint charts/aap/
```

### Step 6: Respond

Summarize changes, relevant checks and limitations concisely, including the
reason for declined suggestions. Session reports are optional unless requested;
preserve caller-required outputs at their specified paths and formats. Keep PR
text and response JSON concise.

## Common Review Feedback Patterns

These are frequently seen in osac-aap reviews and how to handle them:

| Feedback | Action |
|----------|--------|
| "Use FQCN" | Replace bare module with `ansible.builtin.*` or `kubernetes.core.*` |
| "Missing task name" | Add descriptive `name:` field |
| "Use underscores" | Rename to underscores in role dir, `meta/osac.yaml`, and strategy |
| "Missing kubeconfig" | Add `osac.service.common` include before remote K8s ops |
| "Update meta/osac.yaml" | Ensure the identity field (`fabric_manager`/`k8s_manager` for network roles, `implementation_strategy` otherwise) matches role directory name |
| "Cross-repo needed" | Document in PR description, do not attempt changes in other repos |
| "Add to ansible-lint-ignore" | Only if genuinely unavoidable; explain in PR comment |
| "Stale vendor" | Flag for human review; do not modify `vendor/` automatically |
