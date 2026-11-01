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

	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
)

// FabricDomainReconciler runs the Fabric create_fabric_domain and
// delete_fabric_domain tasks (contract §4.10). It launches only when the
// NetworkClass enables Ethernet east-west; NetworkClass resolution already
// requires the Fabric Manager to declare eastWestEthernet then (contract §4.2).
type FabricDomainReconciler struct {
	Client              client.Client
	Engine              *networktask.Engine
	NetworkingNamespace string
	// Recorder emits the FabricDomain Events; nil disables them.
	Recorder events.EventRecorder

	observability *fabricDomainObservability
}

// Reconcile advances the create or delete of a FabricDomain.
func (r *FabricDomainReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileNetworkResource(ctx, r.Client, r.Engine, req.NamespacedName, networkingHandlers[*v1alpha1.FabricDomain]{
		kind:      kindFabricDomain,
		finalizer: osacFabricDomainFinalizer,
		newObject: func() *v1alpha1.FabricDomain { return &v1alpha1.FabricDomain{} },
		status:    func(o *v1alpha1.FabricDomain) any { return &o.Status },
		create:    r.create,
		remove:    r.remove,

		observeTransition: r.observeTransition,
		observeDeleted:    r.recordFabricDomainDeleted,
	})
}

func (r *FabricDomainReconciler) create(ctx context.Context, fd *v1alpha1.FabricDomain) (ctrl.Result, error) {
	var vn *v1alpha1.VirtualNetwork
	if fd.Status.TaskInput == nil {
		var err error
		vn, err = findVirtualNetwork(ctx, r.Client, fd.Namespace, fd.Spec.VirtualNetwork)
		if err != nil {
			return ctrl.Result{}, err
		}
		if vn == nil || !isReady(vn) {
			networktask.SetProgressing(fd, msgWaitingForParent(kindVirtualNetwork, fd.Spec.VirtualNetwork))
			return waitResult(), nil
		}
		copyManagers(fd, vn.Status.NetworkManagers, false)
		fd.Status.NetworkType = vn.Status.NetworkType
	}
	input, store, err := inputOf(fd, func() (map[string]any, error) {
		resource, err := networktask.Snapshot(fd, kindFabricDomain, fd.Spec, "type", "servers")
		if err != nil {
			return nil, err
		}
		vnSnapshot, err := parentSnapshot(vn, kindVirtualNetwork, vn.Spec, "ipv4Cidr", nil)
		if err != nil {
			return nil, err
		}
		input := networktask.NewInput(fd, kindFabricDomain, resource)
		input["virtual_network"] = vnSnapshot
		return input, nil
	}, nil)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !r.Engine.ProvisioningEnabled() {
		networktask.SetProvisioningDisabled(fd, networktask.CreateFabricDomain)
		return ctrl.Result{}, store()
	}
	if !networktask.Started(fd, networktask.CreateFabricDomain, v1alpha1.NetworkJobManagerFabric) {
		nc, err := readyNetworkClass(ctx, r.Client, fd.Namespace, r.Engine.ProvisioningEnabled())
		if err != nil {
			return ctrl.Result{}, err
		}
		if nc == nil || nc.Spec.EastWestCapabilities == nil || !nc.Spec.EastWestCapabilities.SupportsEastWestEthernet {
			networktask.SetFailed(fd, msgEastWestNotEnabled)
			return ctrl.Result{}, nil
		}
	}
	req := taskRequest(fd, kindFabricDomain, networktask.CreateFabricDomain, v1alpha1.NetworkJobManagerFabric, input)
	req.BeforeLaunch = store
	req.Accept = func(_ context.Context, result networktask.Result) error {
		backendID, err := networktask.ParseBackendID(result)
		if err != nil {
			return err
		}
		if backendID != "" {
			fd.Status.BackendID = backendID
		}
		return nil
	}
	out, err := r.Engine.Run(ctx, fd, req)
	if err != nil {
		return ctrl.Result{}, err
	}
	if out.Succeeded() {
		networktask.SetSucceeded(fd)
		return ctrl.Result{}, nil
	}
	reportOutcome(fd, out)
	return outcomeResult(out), nil
}

func (r *FabricDomainReconciler) remove(ctx context.Context, fd *v1alpha1.FabricDomain) (bool, ctrl.Result, error) {
	input := cleanupInput(fd)
	return runCleanup(ctx, r.Engine, fd, kindFabricDomain,
		cleanupTask{create: networktask.CreateFabricDomain, manager: v1alpha1.NetworkJobManagerFabric,
			request: func() networktask.Request {
				return taskRequest(fd, kindFabricDomain, networktask.DeleteFabricDomain, v1alpha1.NetworkJobManagerFabric, input)
			}},
	)
}

// SetupWithManager registers the controller. VirtualNetwork events re-queue
// its FabricDomains.
func (r *FabricDomainReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return errors.New("local manager is nil")
	}
	if err := r.setupFabricDomainObservability(); err != nil {
		return err
	}
	ns := r.NetworkingNamespace
	inNamespace := builder.WithPredicates(NetworkingNamespacePredicate(ns))
	return ctrl.NewControllerManagedBy(localMgr).
		Named("fabricdomain").
		For(&v1alpha1.FabricDomain{}, inNamespace).
		Watches(&v1alpha1.VirtualNetwork{}, enqueueWhere(r.Client, ns,
			func() client.ObjectList { return &v1alpha1.FabricDomainList{} },
			func(obj, trigger client.Object) bool {
				return obj.(*v1alpha1.FabricDomain).Spec.VirtualNetwork == triggerID(trigger, kindVirtualNetwork)
			}), inNamespace).
		Complete(r)
}
