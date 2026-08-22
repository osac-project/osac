# E2E readiness gate (OSAC-3370)

Expensive e2e (`e2e-vmaas-full-install`, `e2e-bmaas-full-install`, `e2e-caas-full-install`) **fails** the cheap readiness job until an unlock signal is present. Goal: save runner bandwidth until CodeRabbit is happy.

Action + `/e2e-ready` + label cleanup live in [osac-test-infra](https://github.com/osac-project/osac-test-infra) (`check-e2e-readiness@main`). This repo is the caller.

## Signals (any one is enough)

| Signal | Notes |
|--------|--------|
| `coderabbitai[bot]` `APPROVED` on exact HEAD | Primary. **Starts** e2e. Same-repo: `e2e-on-approval` (`pull_request_review`). Fork: that event has a read-only token / no secrets, so `e2e-on-approval` only hands off; `e2e-on-approval-fork.yml` (`workflow_run`, YAML from default branch) verifies APPROVED on exact HEAD and reruns. Blocked while any human still has outstanding `CHANGES_REQUESTED`. Abbreviated SHA does not count. The replay workflow must already be on `main` (this PR cannot replay itself before merge). |
| `lgtm` label | Alternate. **Starts** e2e (`e2e-on-label`). Prow removes the label on new pushes. |
| `e2e-ready` via `/e2e-ready` | Quiet override. Must be applied by `github-actions[bot]` (slash command). Manual UI labels are rejected. Cleanup removes the label on push. |

**Human `APPROVED` reviews do not unlock expensive e2e.**

`/test` and `/retest` only **rerun** workflows; they do not apply unlock labels.
If readiness already failed, a rerun still fails the gate until a signal is present.

Non-`pull_request` events (schedule, `workflow_dispatch`, `merge_group`) skip the gate.

Fork PRs: readiness is a **cost** gate only. Secrets / cluster e2e still need
`ok-to-test` (or org membership) via `authorize-fork-pr` — same as before.
CodeRabbit APPROVED **does** auto-start fork e2e after this workflow is on
`main` (`workflow_run` replay). `/ok-to-test` is still required for secrets,
not for the cost unlock.

## Author flow

1. Open PR → cheap `e2e-readiness` fails; no heavy runners.
2. When ready: CodeRabbit `APPROVED` on current head, **or** `/lgtm`, **or** `/e2e-ready`.
3. Push more commits → `lgtm` (Prow) and `e2e-ready` (cleanup) drop → gate fails until CodeRabbit re-approves or a label is re-applied.

## Smoke checklist

- [ ] Open a PR with a code change (not docs-only).
- [ ] Confirm `e2e-readiness` fails quickly (no CR / `lgtm` / `/e2e-ready`).
- [ ] Confirm no EC2 / full-install reusable jobs start while unreadiness fails.
- [ ] CodeRabbit `APPROVED` on exact head → e2e starts (same-repo: `e2e-on-approval`; fork: `e2e-on-approval-fork` after the handoff run completes).
- [ ] Apply `lgtm` → `e2e-on-label` starts e2e.
- [ ] `/e2e-ready` (bot-applied) unlocks; a manual `e2e-ready` label does not.
- [ ] Push a new commit → unlock labels removed → readiness fails again.
- [ ] Optional: schedule / `workflow_dispatch` still runs without the label.
