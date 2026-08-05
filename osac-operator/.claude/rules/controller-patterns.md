# Controller Development Patterns

## Dual-Controller Pattern

Each resource has two controllers:

```text
Resource Controller                    Feedback Controller
- Provisions via AAP                   - Syncs CR state → fulfillment-service
- Manages finalizers and deletion       - Converts K8s Phase → proto State
- Updates Phase, Conditions, etc.       - Sends Signal RPC on deletion
```

| File Pattern | Purpose |
|---|---|
| `{resource}_controller.go` | Provisioning, lifecycle |
| `{resource}_feedback_controller.go` | Sync to fulfillment-service |

**Why?** Resource controller handles infra lifecycle; feedback controller handles fulfillment-service integration. Operator works even if fulfillment-service is down.

## Reconciliation Pattern

```go
func (r *SubnetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    subnet := &v1alpha1.Subnet{}
    if err := r.Client.Get(ctx, req.NamespacedName, subnet); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // Skip if unmanaged
    if subnet.Annotations[osacManagementStateAnnotation] == ManagementStateUnmanaged {
        return ctrl.Result{}, nil
    }

    // Save old status, compare after reconcile, update only if changed
    oldStatus := subnet.Status.DeepCopy()
    var res ctrl.Result
    var err error
    if subnet.DeletionTimestamp.IsZero() {
        res, err = r.handleUpdate(ctx, subnet)
    } else {
        res, err = r.handleDelete(ctx, subnet)
    }
    if !equality.Semantic.DeepEqual(subnet.Status, *oldStatus) {
        if err := r.Status().Update(ctx, subnet); err != nil {
            return res, err
        }
    }
    return res, err
}
```

Key rules:
- Always `client.IgnoreNotFound(err)` — resource may be deleted between list and get
- Save old status and compare before updating (avoids reconciliation loops)
- Separate `handleUpdate` and `handleDelete`

## Finalizer Management

- Each controller has its own finalizer: `osac.openshift.io/{resource}-finalizer`
- Feedback controllers: `osac.openshift.io/{resource}-feedback-finalizer`
- Remove finalizer only after cleanup fully completes
- Multiple finalizers coexist — each controller manages its own

## AAP Integration

Networking controllers use `provisioning.RunProvisioningLifecycle()` with callbacks:
- `OnBeforeProvision` — validate preconditions
- `OnSuccess` — extract outputs, set Phase to Ready
- `OnFailed` — set Phase to Failed

Template naming: `{prefix}-{action}-{kind}` (e.g., `osac-create-subnet`).
Prefix configurable via `OSAC_AAP_TEMPLATE_PREFIX`.

## Feedback Controller

Syncs K8s Phase → proto State:

| K8s Phase | Proto State |
|---|---|
| Progressing | PENDING |
| Ready | READY |
| Failed | FAILED |
| Deleting | DELETING |
| (deletion failed) | DELETE_FAILED |

Handle NotFound during deletion gracefully — fulfillment-service may archive before finalizer removed.

## CRD Type Definition

```go
type SubnetSpec struct {
    // +kubebuilder:validation:Required
    VirtualNetwork string `json:"virtualNetwork"`
    // +kubebuilder:validation:Pattern=`^([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}$`
    IPv4CIDR string `json:"ipv4CIDR,omitempty"`
}
type SubnetStatus struct {
    Phase            SubnetPhase `json:"phase,omitempty"`
    BackendNetworkID string      `json:"backendNetworkID,omitempty"`
    Conditions       []Condition `json:"conditions,omitempty"`
    JobHistory       []JobRecord `json:"jobHistory,omitempty"`
}
// +kubebuilder:validation:Enum=Progressing;Ready;Failed;Deleting
type SubnetPhase string
```

Common kubebuilder markers: `+kubebuilder:validation:Required`, `Pattern`, `Enum`, `Minimum`, `+kubebuilder:printcolumn`.
