# Common Pitfalls

## 1. Forgetting to regenerate after CRD changes
Always run `make manifests generate` after modifying `api/v1alpha1/*.go`.
CRD YAML in `config/crd/` and DeepCopy in `zz_generated.deepcopy.go` are generated — never edit directly.

## 2. Status update loops
Save `oldStatus := subnet.Status.DeepCopy()` before reconciliation.
Use `equality.Semantic.DeepEqual` to compare. Only call `r.Status().Update()` if changed.

## 3. Finalizer removal timing
Remove finalizer only after cleanup fully completes. For feedback controllers: after syncing deletion state.
Handle errors during cleanup — retry on next reconcile, don't remove prematurely.

## 4. AAP job polling inefficiency
Use `StatusPollInterval` (default: 30s). Only poll active jobs.
Return `ctrl.Result{RequeueAfter: interval}` for delayed reconciliation.

## 5. NotFound errors during deletion
Feedback controllers may see NotFound if fulfillment-service archives before finalizer removed.
Handle gracefully: check `DeletionTimestamp` + `codes.NotFound`, then remove finalizer.

## 6. Parent resource lookup failures
If parent not found (e.g., Subnet → VirtualNetwork), requeue with delay.
Don't set Phase to Failed — parent may be created soon. Use conditions for transient errors.

## 7. Vendor directory confusion
Operator uses Go modules, not vendoring. Delete `vendor/` if it exists. Use `go mod tidy`.

## 8. Integration test failures
- Update `kustomization.yaml` after renaming/deleting manifests
- Clean up test clusters: `kind delete cluster --name osac`
- Run `make test-kustomize` before committing manifest changes

## 9. gRPC client version mismatches
Update module version in `buf.gen.yaml`, never edit proto files directly.
Run `buf generate` to regenerate `internal/api/`. Commit generated files.

## 10. AAP template naming
Template names: `{prefix}-{action}-{resource-kind}` (e.g., `osac-create-subnet`).
Per-resource overrides (e.g., `OSAC_CLUSTER_AAP_PROVISION_TEMPLATE`) take precedence.
