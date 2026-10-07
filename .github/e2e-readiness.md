# E2E readiness gate (OSAC-3370)

Expensive e2e (`e2e-vmaas-full-install`, `e2e-bmaas-full-install`, `e2e-caas-full-install`) **waits** (required `e2e-*-gate` stays pending) until a **cost unlock** is present. Goal: save runner bandwidth until CodeRabbit is happy, without marking the PR e2e red.

Action, `/e2e-ready`, label cleanup, the **e2e-on-label starter**, and **fork CR replay** live in [osac-test-infra](https://github.com/osac-project/osac-test-infra). This repo keeps thin listeners (`e2e-on-label.yml`, `e2e-on-approval.yml`, `e2e-on-approval-fork.yml`) that `uses` those reusables and pass this repo's caller workflow filenames. `fork-handoff` stays a top-level job here (reusable job names are prefixed; the replay gate matches `fork-handoff` exactly).

`GITHUB_TOKEN` cannot start other workflows from a `labeled` event. `/e2e-ready` therefore applies the label **and** starts e2e from the test-infra handler (`workflow_dispatch` of this repo's `e2e-on-label.yml`, which calls the reusable). A human UI `e2e-ready` label still does not start e2e.

## Signals

Cost unlock: any one of CR / `lgtm` / `/e2e-ready`. Fork secrets are a **separate** gate.

| Signal | Cost unlock | Starts expensive e2e | Fork secrets |
|--------|-------------|----------------------|--------------|
| `coderabbitai[bot]` `APPROVED` on exact HEAD | yes. Blocked while a human has outstanding `CHANGES_REQUESTED`. Abbreviated SHA does not count. | yes. Same-repo: `e2e-on-approval`. Fork: `e2e-on-approval` only hands off; `e2e-on-approval-fork.yml` (`workflow_run`, YAML from default branch) verifies APPROVED on exact HEAD and reruns. Replay must already be on `main`. | no |
| `lgtm` (`/lgtm`) | yes. Prow strips the label on push; a prior apply still unlocks later SHAs **unless** a human has outstanding `CHANGES_REQUESTED`. | yes (`e2e-on-label` on apply; later pushes via the PR caller). | no |
| `/e2e-ready` | yes, this SHA. Cleanup strips on push. Must be `github-actions[bot]`; manual UI labels are rejected. | yes (`workflow_dispatch` of `e2e-on-label`). | no |
| `/ok-to-test` | **no** | does not start Full Install once this gate skips unreadied jobs (skipped ≠ failed, so the old rerun-failed path does not spend runners). | **yes** (`authorize-fork-pr`) |
| `/test e2e` / `/retest` | **no** | rerun only. Still waits if not unlocked. | no |
| `/e2e-regression` (or `e2e-regression` label) | **no** | does not start e2e. On the **next** PR full-install run, each suite uses `*/regression` (same job). Immediate: `/test vmaas` / `caas` / `bmaas` after the label is set. | no |
| `/e2e-serial` (or `e2e-serial` label) | **no** | does not start e2e. On the **next** PR full-install run, each suite uses `*/serial` (same job; wins over `e2e-regression` if both are set). Useful today for BMaaS (`bmaas/serial` has tests); VMaaS/CaaS serial dirs are still empty scaffolding. Immediate: `/test bmaas` (or `vmaas` / `caas`) after the label is set. | no |

**Human `APPROVED` reviews do not unlock expensive e2e.** Present `lgtm` and bot `/e2e-ready` still override an outstanding human `CHANGES_REQUESTED`. Historical `lgtm` and CR APPROVED do not.

**Fork recipe:** `/ok-to-test` (or org membership) **and** one of CR / `lgtm` / `/e2e-ready`. Same-repo: cost unlock only.

Unlock replay (`e2e-on-label` / `e2e-on-approval`) looks up the original
`pull_request` run by `head_sha`. Do not scan only the newest 100 runs — this
repo exceeds that in a day, so delayed `/lgtm` would miss the head (see
[osac#550](https://github.com/osac-project/osac/pull/550)).
If that run is still queued (no runner yet), skip replay — the same run will
evaluate current labels when it starts ([osac#538](https://github.com/osac-project/osac/pull/538)
timed out after 3 minutes while `changes` waited 48 minutes).

Non-`pull_request` events (schedule, `workflow_dispatch`, `merge_group`) skip the gate.

## Author flow

1. Open PR (code change) → cheap `e2e-readiness` succeeds with `ready=false`; required `e2e-*-gate` stays **pending**; no heavy runners.
2. Docs-only PR → `e2e-readiness` skipped; `e2e-*-gate` reports success.
3. When ready: CodeRabbit `APPROVED` on current head, **or** `/lgtm`, **or** `/e2e-ready`. Fork PRs also need `/ok-to-test` (or org membership) for secrets.
4. Push more commits → `e2e-ready` drops. `lgtm` label drops too, but a prior `/lgtm` still unlocks unless a human has `CHANGES_REQUESTED`. Gate remains pending if a human has outstanding `CHANGES_REQUESTED`, or if there was never `lgtm` and CodeRabbit has not re-approved.

## Smoke checklist

- [ ] Open a PR with a code change (not docs-only).
- [ ] Confirm `e2e-readiness` finished (`ready=false`; the job is green because it is only a cheap API check, not the merge gate) and required `e2e-*-gate` is **yellow/pending** (no CR / `lgtm` / `/e2e-ready`).
- [ ] Confirm no EC2 / full-install reusable jobs start while `e2e-*-gate` is pending.
- [ ] CodeRabbit `APPROVED` on exact head → e2e starts (same-repo: `e2e-on-approval`; fork: `e2e-on-approval-fork` after the handoff run completes).
- [ ] Apply `lgtm` → `e2e-on-label` starts e2e.
- [ ] `/e2e-ready` (bot-applied) unlocks **and** starts e2e (`e2e-on-label` run appears). A manual `e2e-ready` label does not.
- [ ] `/ok-to-test` alone does not unlock the cost gate. Fork still needs CR / `lgtm` / `/e2e-ready` as well.
- [ ] Push a new commit → `e2e-ready` removed. If the PR had `lgtm` earlier and no human has outstanding `CHANGES_REQUESTED`, e2e still runs; otherwise gate pending again.
- [ ] Optional: schedule / `workflow_dispatch` still runs without the label.
- [ ] Docs-only PR: `e2e-*-gate` succeeds (merge not blocked on e2e).
