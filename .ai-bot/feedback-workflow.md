# Feedback Workflow

Determine which component this PR concerns — `fulfillment-service/` or
`osac-operator/` — from the PR's changed files, then follow that
component's workflow below. If it spans both, address comments for each
component using that component's validation commands.

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
