# New Ticket Workflow

Determine which component this ticket concerns — `fulfillment-service/`,
`osac-operator/`, `osac-aap/`, or `osac-installer/` — from its description
and the files involved, then follow that component's workflow below. If it
spans multiple components, complete each workflow independently and validate
each before writing the PR description.

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

## osac-aap

Multi-phase bugfix workflow for Ansible automation code. This component uses
`ansible-lint` as its primary validation gate — there are no compiled
artifacts or unit test frameworks. Integration tests exist but require a
kind cluster and are heavy-weight.

Read and execute `.ai-workflows/bugfix/skills/unattended.md` with these
settings:

- **branch**: Stay on the current branch (already created by the orchestration
  system — do not create a new branch).
- **lint_command**: `cd osac-aap && uv run ansible-lint`
- **max_retries**: 3

All artifact paths (`.artifacts/bugfix/{issue}/`) should use `.ai-bot/`
instead. Write the PR description to `.ai-bot/pr.md`.

### Phase 1: Diagnose

1. Read the ticket description from `.ai-bot/issue.md`
2. Identify which files are involved:
   - Playbooks: `osac-aap/playbook_osac_*.yml` at component root
   - Template roles: `osac-aap/collections/ansible_collections/osac/templates/roles/`
   - Service roles: `osac-aap/collections/ansible_collections/osac/service/roles/`
   - Workflow playbooks: `osac-aap/collections/ansible_collections/osac/workflows/playbooks/`
   - Filter plugins: `osac-aap/collections/ansible_collections/osac/service/plugins/filter/`
   - Custom modules: `osac-aap/collections/ansible_collections/osac/service/plugins/modules/`
3. Understand the data flow: osac-operator -> osac_job_vars -> playbook ->
   implementation_strategy -> template role -> K8s resources
4. Check `osac-aap/.claude/rules/playbook-patterns.md` and
   `osac-aap/.claude/rules/networking-cudn.md` for domain-specific patterns
5. Write root cause analysis to `.ai-bot/root-cause.md`

### Phase 2: Fix

1. Make the code change following all conventions from the osac-aap section
   of `.ai-bot/instructions.md`
2. Key rules to remember:
   - FQCN for all modules (`ansible.builtin.*`, `kubernetes.core.*`)
   - Every task needs a `name:` field
   - Underscores in role names, `implementation_strategy`, and
     `fabric_manager`/`k8s_manager`
   - Include `osac.service.common` before remote K8s operations
3. If adding a new template role, create `meta/osac.yaml`
4. Write implementation notes to `.ai-bot/implementation-notes.md`

**Test file exception**: This is an Ansible project without a traditional
unit test framework. If the fix touches workflow logic that has integration
test coverage (check `osac-aap/tests/integration/targets/`), update the
relevant baseline or override test. If no matching integration test target
exists, document why tests were not added in implementation notes.

### Phase 3: Validate

Run in this order (from `osac-aap/`):

```bash
cd osac-aap

# 1. Lint (mandatory — must pass)
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

If the fix touches `osac-aap/collections/ansible_collections/osac/workflows/`
or `osac-aap/collections/ansible_collections/osac/service/roles/`, also verify
integration tests still pass conceptually by reviewing the relevant test
targets in `osac-aap/tests/integration/targets/`.

### Phase 4: Self-Review

Review the diff against:
- All conventions in the osac-aap section of `.ai-bot/instructions.md`
- Patterns documented in `osac-aap/.claude/rules/playbook-patterns.md`
- The `osac-aap/.ansible-lint.yml` skip/warn lists (don't introduce new
  violations)
- The `osac-aap/.ansible-lint-ignore` file (don't add to it without
  justification)
- Cross-repo impact (does this change require coordinated changes in
  osac-operator or fulfillment-service?)

Write review findings to `.ai-bot/review.md`.

### Phase 5: Document

Write PR description to `.ai-bot/pr.md` including:
- What changed and why
- Which collections/roles were modified
- Cross-repo dependencies (if any)
- How to test (e.g., which integration test target exercises this code)

Write session context to `.ai-bot/session-context.md`.

## osac-installer

Infrastructure/deployment bugfix workflow. This is a structural-validation
repo — there are no unit tests. Validation is YAML lint, pre-commit, Helm
lint, and Helm template render.

Execute the following workflow phases in order.

1. **Read and execute `.ai-workflows/bugfix/skills/assess.md`**
   The bug report is in `.ai-bot/issue.md`. Identify which files are
   affected (Helm charts, values files, scripts, prerequisites).
   Do not ask clarifying questions — make reasonable assumptions.

2. **Read and execute `.ai-workflows/bugfix/skills/diagnose.md`**
   Write your root cause analysis to `.ai-bot/diagnosis.md`.

3. **Read and execute `.ai-workflows/bugfix/skills/fix.md`**
   Implement the minimal fix. Key constraints:
   - Do not edit component code under sibling mono-repo directories (e.g.
     `fulfillment-service/`) from an installer-only change — use one `osac` PR.

4. **Validate changes**
   Run all validation commands from the `osac-installer/` directory in
   sequence. If any fail, revise your fix and revalidate (up to 5 iterations):

   ```bash
   cd osac-installer
   yamllint --strict .
   pre-commit run --all-files
   make helm-lint
   make helm-validate
   ```

5. **Read and execute `.ai-workflows/bugfix/skills/review.md`**
   Self-review for schema consistency, values file alignment, namespace
   references. If issues found, correct them, revalidate, and re-review
   (up to 4 iterations).

6. **Read and execute `.ai-workflows/bugfix/skills/pr.md`**
   Write PR description to `.ai-bot/pr.md` with root cause and affected
   components.
