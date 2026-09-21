# Feedback Workflow

Determine which component this PR concerns — `fulfillment-service/`,
`osac-operator/`, `osac-aap/`, or `osac-installer/` — from the PR's
changed files, then follow that component's workflow below. If it spans
multiple components, address comments for each component using that
component's validation commands.

## fulfillment-service

Read and execute `.ai-workflows/bugfix/skills/feedback.md`.

All artifact paths (`.artifacts/bugfix/{issue}/`) should use `.ai-bot/`
instead.

Review comments are already provided in the task above (use source 1: task
file).

### Session Context Recovery

Before making changes, read `.ai-bot/session-context.md` if it exists. This
file contains the reasoning behind the original implementation (root cause,
design decisions, test strategy). Use it to avoid contradicting prior decisions
unless the reviewer explicitly asks for a different approach.

### Addressing Comments

For each review comment:

1. Read the comment carefully. Understand what is being asked.
2. If the comment requests a code change, implement it.
3. If the comment asks a question, answer it in `.ai-bot/comment-responses.json`
   and make any related code changes.
4. If you disagree with a suggestion, explain why in the comment response but
   still implement it unless doing so would introduce a correctness bug.

### Validation After Changes

After addressing all comments, run the full validation sequence (from
`fulfillment-service/`):

1. `gofmt -s -w .` then `git diff --exit-code` (formatting clean)
2. `buf lint` (if any proto files changed)
3. `ginkgo run -r internal` (unit tests pass)
4. `go build ./cmd/fulfillment-service && go build ./cmd/osac` (both binaries compile)

### Comment Responses

Write a JSON file to `.ai-bot/comment-responses.json` mapping each comment ID
to a short summary of what you did. The bot uses this to post descriptive
replies on the PR instead of generic messages.

### Iteration Cap

Maximum 2 fix-test cycles per feedback round. If tests still fail after 2
attempts, document the failure in the comment response and let the reviewer
decide.

## osac-operator

You are addressing PR review feedback. Your scope is narrow: read the
review comments, make targeted changes, verify correctness, and update
session artifacts for the next round.

Read and execute `.ai-workflows/bugfix/skills/feedback.md` with these
settings:
  - All artifact paths (`.artifacts/bugfix/{issue}/`) should use
    `.ai-bot/` instead.
  - Write comment response summaries to `.ai-bot/comment-responses.json`.
  - Update `.ai-bot/session-context.md` with a new feedback round section.

### Verification

After making changes (from `osac-operator/`):

1. If you touched `osac-operator/api/v1alpha1/*_types.go`, run
   `cd osac-operator && make manifests generate`.
2. If you touched `osac-operator/go.mod`, run `cd osac-operator && go mod tidy`.
3. Run `cd osac-operator && make test` — all tests must pass.
4. Run `cd osac-operator && make lint` — fix all reported issues.

### Guidelines

- Read `.ai-bot/session-context.md` and `.ai-bot/implementation-notes.md`
  before making changes — understand the original design decisions.
- Do not revert intentional decisions without cause. If the original
  session rejected an approach for documented reasons, explain the
  rationale to the reviewer rather than blindly adopting their suggestion.
- Keep changes focused. Address the review comments — do not refactor
  surrounding code or fix unrelated issues.
- Record declined suggestions in the session context so the next round
  does not re-evaluate the same trade-off.

## osac-aap

Read and execute `.ai-workflows/bugfix/skills/feedback.md` with the
settings below.

All artifact paths (`.artifacts/bugfix/{issue}/`) should use `.ai-bot/`
instead.

### Settings

```yaml
lint_command: cd osac-aap && uv run ansible-lint
```

### Session Context Recovery

Before making changes, read `.ai-bot/session-context.md` and
`.ai-bot/implementation-notes.md` if they exist. Also read
`.ai-bot/root-cause.md` for the original root cause analysis.

### Addressing Comments

For each comment, determine:
- Is it a code change request, test addition, explanation, or design
  challenge?
- Does it conflict with the original design decisions?
- Does it involve cross-repo coordination (osac-operator,
  fulfillment-service)?

Apply changes following the Ansible conventions:
- FQCN for all modules
- `name:` on every task
- Underscores in role names
- Include `osac.service.common` for remote K8s operations

If a reviewer suggests something that contradicts the project conventions
(e.g., using bare module names, hyphenated role names), explain the
convention rather than adopting the suggestion.

### Validation After Changes

After addressing all comments, run the full validation sequence (from
`osac-aap/`):

```bash
cd osac-aap

# Mandatory
uv run ansible-lint

# Syntax check modified playbooks
ansible-playbook --syntax-check playbook_osac_<modified>.yml

# Pre-commit
pre-commit run --all-files

# Helm (if charts/ changed)
helm lint charts/aap/
```

### Comment Responses

Write `.ai-bot/comment-responses.json` mapping each comment ID to a
short response summary.

### Session Context Update

Append a feedback round section to `.ai-bot/session-context.md`:

```markdown
## Feedback Round N
**Comments addressed**: [@reviewer on file:line, ...]
**Changes made**:
- [Description] (file:line) — [why this approach]
**Suggestions declined**:
- [@reviewer on file:line]: [reason]
**Tests updated**: [list changes or "no test changes needed"]
```

### Common Review Feedback Patterns

| Feedback | Action |
|----------|--------|
| "Use FQCN" | Replace bare module with `ansible.builtin.*` or `kubernetes.core.*` |
| "Missing task name" | Add descriptive `name:` field |
| "Use underscores" | Rename to underscores in role dir, `meta/osac.yaml`, and strategy |
| "Missing kubeconfig" | Add `osac.service.common` include before remote K8s ops |
| "Update meta/osac.yaml" | Ensure the identity field matches role directory name |
| "Cross-repo needed" | Document in PR description, do not attempt changes in other repos |
| "Add to ansible-lint-ignore" | Only if genuinely unavoidable; explain in PR comment |
| "Stale vendor" | Flag for human review; do not modify `vendor/` automatically |

## osac-installer

Read and execute `.ai-workflows/bugfix/skills/feedback.md` with the
following repo-specific context.

All artifact paths (`.artifacts/bugfix/{issue}/`) should use `.ai-bot/`
instead.

### Session Context Recovery

Read `.ai-bot/session-context.md` and `.ai-bot/implementation-notes.md`
to understand the prior session's decisions and changes.

### Feedback Handling Rules

1. **Mono-repo boundaries**: If feedback asks you to change a file in a sibling
   component directory (e.g. `osac-operator/`), make the change there and land
   one PR at the `osac` repo root. **osac-ui** is external (OCI chart).

2. **Values consistency**: If feedback applies to one values file, check
   whether other values files (development, vmaas-ci, caas-ci)
   need the same change. Call this out in your response.

### Validation After Changes

After addressing all review comments, run the full validation suite (from
`osac-installer/`):

```bash
cd osac-installer
yamllint --strict .
pre-commit run --all-files
make helm-lint
make helm-validate
```

See `osac-installer/Makefile` for the underlying helm lint/template commands
these targets execute.

### Comment Responses

Write `.ai-bot/comment-responses.json` with per-comment response
summaries matching the comment IDs from the task file.

### Session Context Update

Update `.ai-bot/session-context.md` with a summary of this feedback
round (what changed, what was kept, why).
