/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
)

// ExternalIPReconciler runs the Fabric create_external_ip and
// delete_external_ip tasks (contract §4.10). It writes status.address only
// from an accepted result; the API state is ALLOCATED exactly when the
// address is set and the ExternalIP is not being deleted.
type ExternalIPReconciler struct {
	Client              client.Client
	Engine              *networktask.Engine
	NetworkingNamespace string
}

// Reconcile advances the create or delete of an ExternalIP.
func (r *ExternalIPReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileNetworkResource(ctx, r.Client, r.Engine, req.NamespacedName, networkingHandlers[*v1alpha1.ExternalIP]{
		kind:      kindExternalIP,
		finalizer: osacExternalIPFinalizer,
		newObject: func() *v1alpha1.ExternalIP { return &v1alpha1.ExternalIP{} },
		status:    func(o *v1alpha1.ExternalIP) any { return &o.Status },
		create:    r.create,
		remove:    r.remove,
		stateTransition: func(o *v1alpha1.ExternalIP, before, after networkFeedbackState) {
			stampTransition(o, &o.Status.StateTransitionTime, before, after)
		},
	})
}

func (r *ExternalIPReconciler) create(ctx context.Context, eip *v1alpha1.ExternalIP) (ctrl.Result, error) {
	if !r.Engine.ProvisioningEnabled() {
		// ExternalIPs never exist with provider networking disabled.
		networktask.SetFailed(eip, networktask.MessageNetworkingDisabled)
		return ctrl.Result{}, nil
	}
	pool, err := findExternalIPPool(ctx, r.Client, eip.Namespace, eip.Spec.Pool)
	if err != nil {
		return ctrl.Result{}, err
	}
	if eip.Status.TaskInput == nil {
		if pool == nil || !isReady(pool) {
			networktask.SetProgressing(eip, msgWaitingForParent(kindExternalIPPool, eip.Spec.Pool))
			return waitResult(), nil
		}
		copyManagers(eip, pool.Status.NetworkManagers, true)
	}
	input, store, err := inputOf(eip, func() (map[string]any, error) {
		resource, err := networktask.Snapshot(eip, kindExternalIP, eip.Spec, "pool")
		if err != nil {
			return nil, err
		}
		poolSnapshot, err := parentSnapshot(pool, kindExternalIPPool, pool.Spec, "cidrs", nil)
		if err != nil {
			return nil, err
		}
		input := networktask.NewInput(eip, kindExternalIP, resource)
		input["external_ip_pool"] = poolSnapshot
		return input, nil
	}, nil)
	if err != nil {
		return ctrl.Result{}, err
	}
	req := taskRequest(eip, kindExternalIP, networktask.CreateExternalIP, v1alpha1.NetworkJobManagerFabric, input)
	req.BeforeLaunch = store
	req.Accept = func(_ context.Context, result networktask.Result) error {
		address, err := networktask.ParseExternalIP(result, poolCIDR(input))
		if err != nil {
			return err
		}
		eip.Status.Address = address
		return nil
	}
	out, err := r.Engine.Run(ctx, eip, req)
	if err != nil {
		return ctrl.Result{}, err
	}
	if out.Succeeded() {
		networktask.SetSucceeded(eip)
		return ctrl.Result{}, nil
	}
	reportOutcome(eip, out)
	return outcomeResult(out), nil
}

// poolCIDR is the pool CIDR of the stored input (external_ip_pool.spec.cidrs[0]).
func poolCIDR(input map[string]any) string {
	pool, _ := input["external_ip_pool"].(map[string]any)
	spec, _ := pool["spec"].(map[string]any)
	cidrs, _ := spec["cidrs"].([]any)
	if len(cidrs) == 0 {
		return ""
	}
	cidr, _ := cidrs[0].(string)
	return cidr
}

func (r *ExternalIPReconciler) remove(ctx context.Context, eip *v1alpha1.ExternalIP) (bool, ctrl.Result, error) {
	if id := eip.Labels[osacExternalIPIDLabel]; id != "" {
		attached, err := anyItem(ctx, r.Client, &v1alpha1.ExternalIPAttachmentList{}, eip.Namespace, func(o client.Object) bool {
			return o.(*v1alpha1.ExternalIPAttachment).Spec.ExternalIP == id
		})
		if err != nil || attached {
			return false, waitResult(), err
		}
		used, err := anyItem(ctx, r.Client, &v1alpha1.NATGatewayList{}, eip.Namespace, func(o client.Object) bool {
			return o.(*v1alpha1.NATGateway).Spec.ExternalIP == id
		})
		if err != nil || used {
			return false, waitResult(), err
		}
	}
	input := cleanupInput(eip)
	return runCleanup(ctx, r.Engine, eip, kindExternalIP,
		cleanupTask{create: networktask.CreateExternalIP, manager: v1alpha1.NetworkJobManagerFabric,
			request: func() networktask.Request {
				return taskRequest(eip, kindExternalIP, networktask.DeleteExternalIP, v1alpha1.NetworkJobManagerFabric, input)
			}},
	)
}

// SetupWithManager registers the controller. Pool events re-queue its
// ExternalIPs; ExternalIPAttachment and NATGateway events re-queue their ExternalIP.
func (r *ExternalIPReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return errors.New("local manager is nil")
	}
	ns := r.NetworkingNamespace
	inNamespace := builder.WithPredicates(NetworkingNamespacePredicate(ns))
	eipList := func() client.ObjectList { return &v1alpha1.ExternalIPList{} }
	return ctrl.NewControllerManagedBy(localMgr).
		Named("externalip").
		For(&v1alpha1.ExternalIP{}, inNamespace).
		Watches(&v1alpha1.ExternalIPPool{}, enqueueWhere(r.Client, ns, eipList, func(obj, trigger client.Object) bool {
			return obj.(*v1alpha1.ExternalIP).Spec.Pool == triggerID(trigger, kindExternalIPPool)
		}), inNamespace).
		Watches(&v1alpha1.ExternalIPAttachment{}, enqueueWhere(r.Client, ns, eipList, func(obj, trigger client.Object) bool {
			return triggerID(obj, kindExternalIP) == trigger.(*v1alpha1.ExternalIPAttachment).Spec.ExternalIP
		}), inNamespace).
		Watches(&v1alpha1.NATGateway{}, enqueueWhere(r.Client, ns, eipList, func(obj, trigger client.Object) bool {
			return triggerID(obj, kindExternalIP) == trigger.(*v1alpha1.NATGateway).Spec.ExternalIP
		}), inNamespace).
		Complete(r)
}
