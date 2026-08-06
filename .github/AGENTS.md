# .github/ Agent Context

## E2E readiness gate (OSAC-3370)

Full-install e2e (`e2e-vmaas-full-install`, `e2e-bmaas-full-install`, `e2e-caas-full-install`) does **not** auto-spend runners on every PR push. Cheap `e2e-readiness` job **fails closed** until unlocked.

**Allow when any of:**
- `lgtm` label present (Prow removes on push)
- `e2e-ready` label applied by `github-actions[bot]` via `/e2e-ready` (cleanup removes on push; manual UI labels are rejected)
- `coderabbitai[bot]` `APPROVED` on the **exact current HEAD** (blocked while a human still has `CHANGES_REQUESTED`). Auto-start: same-repo via `e2e-on-approval`; forks via `e2e-on-approval-fork` (`workflow_run` replay — default-branch YAML, write token). `lgtm` / `/e2e-ready` still work.

Human GitHub `APPROVED` does **not** unlock. Fork PRs still need `ok-to-test` (or org membership) for secrets/cluster — readiness is cost-only.

Cheap checks stay ungated. Schedules / `workflow_dispatch` / `merge_group` skip the readiness job.

Details + smoke checklist: [`.github/e2e-readiness.md`](e2e-readiness.md).
