# New Ticket Workflow

Determine which component this ticket concerns — `fulfillment-service/` or
`osac-operator/` — from its description and the files involved, then follow
that component's workflow below. If it spans both, complete each workflow
independently and validate each before writing the PR description.

## fulfillment-service

Read and execute `.ai-workflows/bugfix/skills/unattended.md` with these
settings:

- **branch**: Stay on the current branch (already created by the orchestration
  system -- do not create a new branch).
- **lint_command**: `cd fulfillment-service && gofmt -s -w .`
- **iteration_cap**: Maximum 3 fix-test cycles before escalating.

All artifact paths (`.artifacts/bugfix/{issue}/`) should use `.ai-bot/`
instead. Write the PR description to `.ai-bot/pr.md`.

### Repo-Specific Test Commands

Use these exact commands (from `fulfillment-service/`) during the test phase:

```bash
cd fulfillment-service

# Unit tests (mandatory -- always run)
ginkgo run -r internal

# Focused unit tests (use during iteration to speed up feedback)
ginkgo run -r internal --focus="<test pattern matching the fix area>"
```

Do NOT run integration tests (`ginkgo run it`). They require a kind cluster
with specific `/etc/hosts` entries and are validated separately by CI.

### Repo-Specific Build Commands

```bash
cd fulfillment-service
go build ./cmd/fulfillment-service
go build ./cmd/osac
```

### After Proto Changes

If your fix touches any `fulfillment-service/**/*.proto` file:

```bash
cd fulfillment-service
buf lint
buf generate
```

Then verify the generated code compiles:

```bash
cd fulfillment-service
go build ./cmd/fulfillment-service
```

### After Mock Interface Changes

If your fix modifies an interface that has a `//go:generate mockgen` directive,
regenerate the mock:

```bash
cd fulfillment-service
go generate ./path/to/package/
```

### Final Validation (Before Writing PR Description)

Run these in order (from `fulfillment-service/`). All must pass:

1. `gofmt -s -w .` then `git diff --exit-code` (formatting)
2. `buf lint` (proto linting, if protos changed)
3. `ginkgo run -r internal` (full unit test suite)
4. `go build ./cmd/fulfillment-service && go build ./cmd/osac` (both binaries)

### Session Context

After completing the fix, write a session context summary to
`.ai-bot/session-context.md` covering:

- Root cause summary
- Files changed and why
- Test strategy (what was tested, what was not)
- Risks or areas that need human review

## osac-operator

IMPORTANT: You will not commit changes — the orchestration system
commits after your session ends. Treat the end of your session as
the "before committing" checkpoint. Stay on the current branch
(already created by the orchestration system).

Execute the following bugfix workflow phases in order.
Each phase is defined in the corresponding skill file.

1. Read and execute `.ai-workflows/bugfix/skills/assess.md`
   The bug report is in `.ai-bot/issue.md`. Do not ask clarifying
   questions — make reasonable assumptions where needed.

2. Read and execute `.ai-workflows/bugfix/skills/diagnose.md`
   Write your root cause analysis to `.ai-bot/diagnosis.md`.
   Read `osac-operator/.claude/rules/controller-patterns.md` and
   `osac-operator/.claude/rules/common-pitfalls.md` before diagnosing — many
   bugs in this codebase fall into the documented pitfall categories.

3. Read and execute `.ai-workflows/bugfix/skills/fix.md`
   Implement the minimal fix. Write implementation notes to
   `.ai-bot/implementation-notes.md`.
   - If you touch `osac-operator/api/v1alpha1/*_types.go`, run
     `cd osac-operator && make manifests generate` immediately.
   - If you touch `osac-operator/go.mod`, run `cd osac-operator && go mod tidy`
     immediately.
   - Always add or update unit tests in the same step.

4. Read and execute `.ai-workflows/bugfix/skills/test.md`
   Run the full test suite with `cd osac-operator && make test`. If tests
   fail, revise your fix and retest (up to 5 iterations).
   Write test verification to `.ai-bot/test-verification.md`.

5. Read and execute `.ai-workflows/bugfix/skills/review.md`
   Self-review your changes. If issues are found, correct them,
   retest, and re-review (up to 4 iterations).
   Write review findings to `.ai-bot/review.md`.

6. Run `cd osac-operator && make lint` and fix all reported issues. Repeat
   until it exits cleanly. This is the final gate — lint failures block CI.

7. Write a PR title and description to `.ai-bot/pr.md`.
   Use the `## Title` heading format:

   ```markdown
   ## Title

   OSAC-XXXXX: short description in lowercase

   ## Summary

   ...PR body...

   ## Root Cause

   ...(from .ai-bot/diagnosis.md)...
   ```

8. Write session context to `.ai-bot/session-context.md` for
   continuity if feedback rounds are needed.
