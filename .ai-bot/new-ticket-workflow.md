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

Keep routine output to a concise summary of changes, relevant checks and
limitations. Create separate reports only when requested; preserve outputs
required by the calling workflow at its specified paths and formats.

### Repo-Specific Test Commands

Use these exact commands (from `fulfillment-service/`) during the test phase:

```bash
cd fulfillment-service

# Focused existing unit tests for the changed behavior
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

Select relevant formatting, lint, build and focused unit checks from
`fulfillment-service/AGENTS.md`. Use the full suite only when needed for the
changed behavior. Summarize the checks performed and any limitations or
unrelated failures. Session/review reports are optional; preserve caller-required
outputs at their specified paths and formats.

## osac-operator

IMPORTANT: You will not commit changes — the orchestration system
commits after your session ends. Treat the end of your session as
the "before committing" checkpoint. Stay on the current branch
(already created by the orchestration system).

Execute the following bugfix workflow phases in order.
Each phase is defined in the corresponding skill file.

1. Read and execute `.ai-workflows/bugfix/skills/assess.md`
   The bug report is in `.ai-bot/issue.md`. Clarify ambiguous scope before
   making destructive or unrelated changes.

2. Read and execute `.ai-workflows/bugfix/skills/diagnose.md`
   Read `osac-operator/.claude/rules/controller-patterns.md` and
   `osac-operator/.claude/rules/common-pitfalls.md` before diagnosing — many
   bugs in this codebase fall into the documented pitfall categories.

3. Read and execute `.ai-workflows/bugfix/skills/fix.md`
   Implement the minimal fix.
   - If you touch `osac-operator/api/v1alpha1/*_types.go`, run
     `cd osac-operator && make manifests generate` immediately.
   - If you touch `osac-operator/go.mod`, run `cd osac-operator && go mod tidy`
     immediately.
   - Add meaningful regression coverage only where needed, using existing suites.

4. Select relevant existing checks from `osac-operator/AGENTS.md`, using
   focused tests for the changed behavior. Use full suites only when needed.

5. Review the diff for correctness, scope and safety. Fix attributable issues;
   report unrelated failures or unavailable checks rather than repeating a
   fixed test/review loop.

6. Summarize the changes, checks and limitations concisely in the PR response.
   Separate diagnosis, verification, review and session reports are optional
   unless requested by the task or required by the calling workflow. Preserve
   its output paths and formats.
