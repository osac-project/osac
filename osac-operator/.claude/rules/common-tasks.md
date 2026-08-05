# Common Tasks

## Adding a New CRD

1. `kubebuilder create api --group osac.openshift.io --version v1alpha1 --kind MyResource`
2. Define types in `api/v1alpha1/myresource_types.go`
3. `make manifests generate`
4. Create controller in `internal/controller/myresource_controller.go`
5. Register controller in `cmd/main.go`

## Adding a Field to Existing CRD

1. Add field to `api/v1alpha1/{resource}_types.go`
2. `make manifests generate`
3. Update controller logic in `internal/controller/{resource}_controller.go`
4. Update feedback controller if field needs sync to fulfillment-service

## Cross-Repo Change Order

1. **fulfillment-service**: Update proto definitions, regenerate
2. **osac-operator**: Update CRD types, controller logic, `buf generate`
3. **osac-aap**: Update Ansible roles/playbooks
4. **osac-installer**: Update submodules, add RBAC if needed

## RBAC Changes

Add markers to controller, then `make manifests`:
```go
//+kubebuilder:rbac:groups=osac.openshift.io,resources=myresources,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=osac.openshift.io,resources=myresources/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=osac.openshift.io,resources=myresources/finalizers,verbs=update
```
Update osac-installer hub-access Role if fulfillment controller needs access.

## Debugging Controller Issues

```bash
kubectl describe subnet my-subnet -n osac-networking
kubectl logs -n osac-system deployment/osac-operator-controller-manager -f
# Enable debug: add --zap-log-level=debug to config/manager/manager.yaml args
```
