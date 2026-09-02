# osac/ Skill Parity Checklist

Verifies that a developer working from a fresh, **standalone `osac/`-only
clone** — with no `osac-workspace` sibling and no `~/.osac-ai-skills` — gets
full AI-skill parity with the former `osac-workspace` experience, across all
three harnesses (Claude Code, Cursor, Gemini CLI).

Run this checklist before telling developers they no longer need
`osac-workspace` **for skills**.

## Scope & Announcement Boundary

This checklist covers **skill parity only**. Passing it satisfies exit
criterion **(a)** of the dedicated-AI-skills-repo ADR (new skills repo live and
vendored by `osac/`'s bootstrap) — see
[ADR 0001](https://github.com/osac-project/osac-workspace/blob/main/decisions/0001-dedicated-ai-skills-repo.md),
Consequences.

A green checklist means it is safe to announce: *developers no longer need
`osac-workspace` for skills specifically.* It is **not** a full `osac-workspace`
decommission announcement. Per ADR Decision item 5, the following remain a
separate, later effort and are **out of scope** here:

- (b) root-context (`AGENTS.md` / `CLAUDE.md`) reconciliation
- (c) PR-dashboard rehoming
- (d) dev-container tooling decision
- (e) `reference/` docs placement
- (f) `decisions/` placement

The standalone remote/Jira-credential resolution paths were fixed in
[OSAC-4005](https://redhat.atlassian.net/browse/OSAC-4005); Part A re-verifies
that fix still holds rather than assuming it.

## Preconditions

1. A fresh clone of `osac/` (a fork or a direct clone), on `main`, with **no
   `osac-workspace` parent directory**.
2. `~/.osac-ai-skills` and `~/.ai-workflows` may or may not be present — the
   automated checks pin an empty `HOME` so resolution is forced onto the
   repo-local `./.osac-ai-skills`, exercising the true standalone path.
3. Run bootstrap once:
   ```bash
   tools/bootstrap.sh          # or: tools/bootstrap.sh --no-fork  (read-only/CI)
   ```

## Part A — Automated checks

Run the parity smoke test from the repo root:

```bash
bash tools/test/skill-parity-smoke.sh
```

It must end with `All skill parity smoke tests passed.` It asserts, on an
isolated standalone-clone fixture (empty `HOME`, no `osac-workspace` sibling):

| # | Check | Contract |
|---|-------|----------|
| A1 | Standalone precondition | Repo-local `.osac-ai-skills` vendor present; clone not nested inside an `osac-workspace`-shaped parent. |
| A2 | `create-pr` / `osac-release` remote resolution | The documented dual-path snippet resolves `resolve-remotes.sh` from `./.osac-ai-skills` and it exits 0 with `UPSTREAM_REMOTE` set (OSAC-4005). |
| A3 | `jira-task-management` credential resolution | The documented snippet resolves and sources `jira-safe-create.sh` from `./.osac-ai-skills`, defining its helper functions (OSAC-4005). |
| A4 | Cross-harness discovery | After linking, `.claude/skills`, `.cursor/skills`, and `.gemini/skills` each resolve every vendored OSAC skill (plus ai-workflow skills). |
| A5 | Discovery integrity | `link-agent-skills.sh --verify` passes. |

## Part B — Manual per-harness attestation

Part A is harness-agnostic (it verifies each harness's discovery wiring). The
checks below require actually invoking a skill inside each harness and can only
be attested by a human with that harness installed. From the fresh standalone
clone, for each harness:

1. Confirm the harness lists OSAC skills (e.g. `create-pr`, `osac-feature`).
2. Invoke one non-mutating skill and confirm it loads and runs.
3. Invoke one of `create-pr`, `osac-release`, or `jira-task-management` far
   enough to exercise its remote/Jira-credential resolution (it must resolve
   `resolve-remotes.sh` / `jira-safe-create.sh` without an `osac-workspace`
   sibling). Abort before any push/create — resolution success is the check.

| Harness | Skills listed | Skill loads & runs | Tool/credential resolution (OSAC-4005) | Attested by / date |
|---------|:-------------:|:------------------:|:--------------------------------------:|--------------------|
| Claude Code | ☐ | ☐ | ☐ | |
| Cursor | ☐ | ☐ | ☐ | |
| Gemini CLI | ☐ | ☐ | ☐ | |

> Command syntax varies by harness; skill discovery is wired identically by
> `tools/bootstrap.sh`. A harness that is not installed locally must be attested
> by a developer who has it.

## Skill inventory

Skills expected to be discoverable after bootstrap. OSAC-native skills are
vendored from
[`osac-ai-skills`](https://github.com/osac-project/osac-ai-skills); workflow
skills come from [`flightctl/ai-workflows`](https://github.com/flightctl/ai-workflows).
The authoritative list is what `link-agent-skills.sh --verify` enforces (check
A5) — this table is the human-readable reference.

- **OSAC-native (`osac-ai-skills`):** `browser-demo-recording`,
  `capture-tasks-from-meeting-notes`, `create-pr`, `design-review`,
  `generate-status-report`, `github-actions-workflows`, `jira-task-management`,
  `milestone-scope`, `osac-cluster`, `osac-demo-recording`, `osac-feature`,
  `osac-release`, `performance-review`, `ponytail-review`, `prd-review`,
  `pre-pr-review`, `presentation`, `quick-fix`, `release-plan`, `report-bug`,
  `review-gate`, `security-review`, `test-plan-review`, `test-plan-score`.
- **Workflow (`ai-workflows`):** `bugfix`, `design`, `e2e`, `implement`, `prd`.

## Validation record

Record each full run used to support a parity announcement:

| Date | Commit | Part A result | Part B (harnesses attested) | Notes |
|------|--------|---------------|-----------------------------|-------|
|      |        |               |                             |       |
