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

// NATGatewayReconciler runs the Fabric create_nat_gateway and
// delete_nat_gateway tasks (contract §4.10) once the VirtualNetwork is Ready
// and the ExternalIP is ALLOCATED. A NATGateway records only the Fabric Manager.
// See natgateway_contract.go for the dependency snapshot and result checks.
type NATGatewayReconciler struct {
	Client              client.Client
	Engine              *networktask.Engine
	NetworkingNamespace string
}

// Reconcile advances the create or delete of a NATGateway.
func (r *NATGatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileNetworkResource(ctx, r.Client, r.Engine, req.NamespacedName, networkingHandlers[*v1alpha1.NATGateway]{
		kind:      kindNATGateway,
		finalizer: osacNATGatewayFinalizer,
		newObject: func() *v1alpha1.NATGateway { return &v1alpha1.NATGateway{} },
		status:    func(o *v1alpha1.NATGateway) any { return &o.Status },
		create:    r.create,
		remove:    r.remove,
		stateTransition: func(o *v1alpha1.NATGateway, before, after networkFeedbackState) {
			stampTransition(o, &o.Status.StateTransitionTime, before, after)
		},
	})
}

// allocated reports whether an ExternalIP is ALLOCATED: Ready with an address.
func allocated(eip *v1alpha1.ExternalIP) bool {
	return eip != nil && isReady(eip) && eip.Status.Address != ""
}

func msgWaitingForAllocation(id string) string {
	return "waiting for ExternalIP " + id + " to be ALLOCATED"
}

func (r *NATGatewayReconciler) create(ctx context.Context, natgw *v1alpha1.NATGateway) (ctrl.Result, error) {
	if !r.Engine.ProvisioningEnabled() {
		networktask.SetFailed(natgw, networktask.MessageNetworkingDisabled)
		return ctrl.Result{}, nil
	}
	var vn *v1alpha1.VirtualNetwork
	var eip *v1alpha1.ExternalIP
	var dependencies natGatewayContext
	if natgw.Status.TaskInput == nil {
		var err error
		vn, err = findVirtualNetwork(ctx, r.Client, natgw.Namespace, natgw.Spec.VirtualNetwork)
		if err != nil {
			return ctrl.Result{}, err
		}
		if vn == nil || !isReady(vn) {
			networktask.SetProgressing(natgw, msgWaitingForParent(kindVirtualNetwork, natgw.Spec.VirtualNetwork))
			return waitResult(), nil
		}
		eip, err = findExternalIP(ctx, r.Client, natgw.Namespace, natgw.Spec.ExternalIP)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !allocated(eip) {
			networktask.SetProgressing(natgw, msgWaitingForAllocation(natgw.Spec.ExternalIP))
			return waitResult(), nil
		}
		// Not pending: the checks above already waited for Ready and ALLOCATED,
		// and create runs only without a deletionTimestamp.
		dependencies, _, err = buildNATGatewayContext(natgw, vn, eip)
		if err != nil {
			networktask.SetFailed(natgw, err.Error())
			return ctrl.Result{}, nil
		}
		copyManagers(natgw, vn.Status.NetworkManagers, true)
	}
	input, store, err := inputOf(natgw, func() (map[string]any, error) {
		resource, err := networktask.Snapshot(natgw, kindNATGateway, natgw.Spec)
		if err != nil {
			return nil, err
		}
		vnSnapshot, err := parentSnapshot(vn, kindVirtualNetwork, vn.Spec, "ipv4Cidr", nil)
		if err != nil {
			return nil, err
		}
		input := networktask.NewInput(natgw, kindNATGateway, resource)
		input["virtual_network"] = vnSnapshot
		input["external_ip"] = map[string]any{"id": natgw.Spec.ExternalIP, fieldAddress: eip.Status.Address}
		input[inputNATGatewayContext], err = natGatewayContextVars(dependencies)
		if err != nil {
			return nil, err
		}
		return input, nil
	}, nil)
	if err != nil {
		return ctrl.Result{}, err
	}
	req := taskRequest(natgw, kindNATGateway, networktask.CreateNATGateway, v1alpha1.NetworkJobManagerFabric, input)
	req.BeforeLaunch = store
	req.Accept = func(ctx context.Context, result networktask.Result) error {
		if err := networktask.AcceptNoOutput(ctx, result); err != nil {
			return err
		}
		return r.acceptDependencies(ctx, natgw, input)
	}
	out, err := r.Engine.Run(ctx, natgw, req)
	if err != nil {
		return ctrl.Result{}, err
	}
	if out.Succeeded() {
		networktask.SetSucceeded(natgw)
		return ctrl.Result{}, nil
	}
	reportOutcome(natgw, out)
	return outcomeResult(out), nil
}

func (r *NATGatewayReconciler) remove(ctx context.Context, natgw *v1alpha1.NATGateway) (bool, ctrl.Result, error) {
	input := cleanupInput(natgw)
	return runCleanup(ctx, r.Engine, natgw, kindNATGateway,
		cleanupTask{create: networktask.CreateNATGateway, manager: v1alpha1.NetworkJobManagerFabric,
			request: func() networktask.Request {
				return taskRequest(natgw, kindNATGateway, networktask.DeleteNATGateway, v1alpha1.NetworkJobManagerFabric, input)
			}},
	)
}

// SetupWithManager registers the controller. VirtualNetwork and ExternalIP
// events re-queue the NATGateways that use them.
func (r *NATGatewayReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return errors.New("local manager is nil")
	}
	ns := r.NetworkingNamespace
	inNamespace := builder.WithPredicates(NetworkingNamespacePredicate(ns))
	natList := func() client.ObjectList { return &v1alpha1.NATGatewayList{} }
	return ctrl.NewControllerManagedBy(localMgr).
		Named("natgateway").
		For(&v1alpha1.NATGateway{}, inNamespace).
		Watches(&v1alpha1.VirtualNetwork{}, enqueueWhere(r.Client, ns, natList, func(obj, trigger client.Object) bool {
			return obj.(*v1alpha1.NATGateway).Spec.VirtualNetwork == triggerID(trigger, kindVirtualNetwork)
		}), inNamespace).
		Watches(&v1alpha1.ExternalIP{}, enqueueWhere(r.Client, ns, natList, func(obj, trigger client.Object) bool {
			return obj.(*v1alpha1.NATGateway).Spec.ExternalIP == triggerID(trigger, kindExternalIP)
		}), inNamespace).
		Complete(r)
}
