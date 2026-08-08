# E2E readiness gate (OSAC-3370)

Expensive e2e (`e2e-vmaas-full-install`, `e2e-bmaas-full-install`, `e2e-caas-full-install`) **fails** the cheap readiness job until an unlock signal is present. Goal: save runner bandwidth until CodeRabbit is happy.

## Signals (any one is enough)

| Signal | Notes |
|--------|--------|
| `coderabbitai[bot]` `APPROVED` on head | Primary. **Starts** e2e (`e2e-on-approval`). Blocked while any human still has outstanding `CHANGES_REQUESTED`. |
| `lgtm` label | Alternate. **Starts** e2e (`e2e-on-label`). Must be **fresh** for the head (labeled at/after this head’s first `pull_request` CI run). Cleared on new pushes when stale. |
| `e2e-ready` label | Quiet override (same freshness / start / cleanup as `lgtm`). Prefer `lgtm` or CodeRabbit in normal flow. |

**Human `APPROVED` reviews do not unlock expensive e2e.**

`/test` and `/retest` only **rerun** workflows; they do not apply unlock labels.
If readiness already failed, a rerun still fails the gate until a signal is present.

Non-`pull_request` events (schedule, `workflow_dispatch`, `merge_group`) skip the gate.

Fork PRs: readiness is a **cost** gate only. Secrets / cluster e2e still need
`ok-to-test` (or org membership) via `authorize-fork-pr` — same as before.

## Author flow

1. Open PR → cheap `e2e-readiness` fails; no heavy runners.
2. When ready: CodeRabbit `APPROVED` on current head, **or** apply `lgtm`.
3. Push more commits → unlock labels removed if stale → gate fails until CodeRabbit re-approves or `lgtm` is re-applied.

## Smoke checklist

Use after landing `osac-test-infra` (`check-e2e-readiness`) **then** the caller change in this repo:

- [ ] Open a PR with a code change (not docs-only).
- [ ] Confirm `e2e-readiness` fails quickly (no CR / `lgtm`).
- [ ] Confirm no EC2 / full-install reusable jobs start while unreadiness fails.
- [ ] CodeRabbit `APPROVED` on head → `e2e-on-approval` starts e2e.
- [ ] Apply `lgtm` → `e2e-on-label` starts e2e.
- [ ] Push a new commit → unlock labels removed when stale → readiness fails again.
- [ ] Optional: schedule / `workflow_dispatch` still runs without the label.

## Dependency

Callers use:

`uses: osac-project/osac-test-infra/.github/actions/check-e2e-readiness@main`

Merge the `osac-test-infra` PR that adds this action before merging the caller change here.
