# Merge Plan — OSAC-1330 Type-Safe Resource References

## Step 1: Merge osac-test-infra PR #296

- Status: OPEN, MERGEABLE, needs review approval
- Contains: E2E test updates for typed resource references

## Step 2: Revert temporary changes on osac PR #85

| # | File(s) | Change |
|---|---------|--------|
| 1 | `e2e-bmaas-full-install.yml` | `htayrie-rh/osac-test-infra` → `osac-project/osac-test-infra`, `feat/OSAC-1330-typed-resource-refs` → `main` |
| 2 | `e2e-caas-full-install.yml` | same |
| 3 | `e2e-vmaas-full-install.yml` | same |
| 4 | `osac-operator/buf.gen.yaml` | `directory: ../fulfillment-service/proto/private` → `module: buf.build/osac-project/private-api:v0.0.79` |
| 5 | `osac-operator/internal/api/**/*.pb.go` (~60 files) | `git checkout origin/main -- osac-operator/internal/api/` |

**Keep** (not reverted):
- `osac-operator/internal/controller/feedback_controller.go`
- `osac-operator/internal/controller/externalipattachment_feedback_controller.go`
- All 6 `*_feedback_controller_test.go` files

## Step 3: Push & CI

- Operator tests will fail (expected — generated code won't match controller changes until BSR publish)
- All other checks should pass

## Step 4: Merge osac PR #85

## Step 5: Follow-up PR

- Publish new BSR version
- Bump `buf.gen.yaml` to new version
- `buf generate` to regenerate operator proto code
- Operator compiles and tests pass again

## Step 6: Cleanup

- Pop stash (`RoleReference` move) onto a branch for OSAC-3662
- `FOR_SHARE_LOCK_CONTENTION.md` remains untracked
