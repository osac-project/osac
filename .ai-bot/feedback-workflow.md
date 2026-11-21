# Feedback Workflow

Determine which component this PR concerns — `fulfillment-service/` or
`osac-operator/` — from the PR's changed files, then follow that
component's workflow below. If it spans both, address comments for each
component using that component's validation commands.

## fulfillment-service

Read and execute `.ai-workflows/bugfix/skills/feedback.md`.

Create separate reports only when requested; preserve outputs required by the
calling workflow at its specified paths and formats. Keep response JSON concise.

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
3. If the comment asks a question, answer it concisely; change code only when
   warranted.
4. If you disagree with a suggestion, explain the technical reason and seek
   clarification rather than implementing a change you consider unnecessary.

### Validation After Changes

Select relevant formatting, lint, build and focused test checks from
`fulfillment-service/AGENTS.md`. Use full suites only when needed for the
changed behavior. Reply with a concise summary of changes, checks and
limitations. Report unrelated failures; no fixed iteration count is required.
Session/review reports are optional unless required by the calling workflow,
whose output paths and formats must be preserved.

## osac-operator

You are addressing PR review feedback. Your scope is narrow: read the
review comments, make targeted changes and verify the changed behavior.

Use `.ai-workflows/bugfix/skills/feedback.md` for relevant feedback handling;
create separate reports only when requested. Preserve caller-required outputs
at their specified paths and formats, with concise PR text and response JSON.

### Verification

After making changes (from `osac-operator/`):

1. If you touched `osac-operator/api/v1alpha1/*_types.go`, run
   `cd osac-operator && make manifests generate`.
2. If you touched `osac-operator/go.mod`, run `cd osac-operator && go mod tidy`.
3. Run relevant existing tests for the changed behavior; use the full
   `make test` suite only when needed.
4. Run applicable lint checks and report unrelated failures.

### Guidelines

- Use existing task context when needed to understand earlier design decisions.
- Do not revert intentional decisions without cause. If the original
  session rejected an approach for documented reasons, explain the
  rationale to the reviewer rather than blindly adopting their suggestion.
- Keep changes focused. Address the review comments — do not refactor
  surrounding code or fix unrelated issues.
- Explain declined suggestions concisely in the review response.
