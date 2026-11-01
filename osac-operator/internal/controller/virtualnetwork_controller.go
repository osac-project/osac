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

// VirtualNetworkReconciler runs the VirtualNetwork tasks (contract §4.10):
// Fabric create_virtual_network, then Kubernetes create_virtual_network when
// the NetworkClass selects a Kubernetes Manager; delete in reverse order once
// every child is gone.
type VirtualNetworkReconciler struct {
	Client              client.Client
	Engine              *networktask.Engine
	NetworkingNamespace string
}

// Reconcile advances the create or delete of a VirtualNetwork.
func (r *VirtualNetworkReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileNetworkResource(ctx, r.Client, r.Engine, req.NamespacedName, networkingHandlers[*v1alpha1.VirtualNetwork]{
		kind:      kindVirtualNetwork,
		finalizer: osacVirtualNetworkFinalizer,
		newObject: func() *v1alpha1.VirtualNetwork { return &v1alpha1.VirtualNetwork{} },
		status:    func(o *v1alpha1.VirtualNetwork) any { return &o.Status },
		create:    r.create,
		remove:    r.remove,
	})
}

func (r *VirtualNetworkReconciler) create(ctx context.Context, vn *v1alpha1.VirtualNetwork) (ctrl.Result, error) {
	if vn.Status.NetworkManagers == nil {
		nc, err := readyNetworkClass(ctx, r.Client, vn.Namespace, r.Engine.ProvisioningEnabled())
		if err != nil {
			return ctrl.Result{}, err
		}
		if nc == nil {
			networktask.SetProgressing(vn, msgWaitingForNetworkClass)
			return waitResult(), nil
		}
		copyManagers(vn, nc.Status.NetworkManagers, false)
		vn.Status.NetworkType = nc.Status.NetworkType
	}
	input, store, err := inputOf(vn, func() (map[string]any, error) {
		resource, err := networktask.Snapshot(vn, kindVirtualNetwork, vn.Spec, "ipv4Cidr")
		if err != nil {
			return nil, err
		}
		return networktask.NewInput(vn, kindVirtualNetwork, resource), nil
	}, nil)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !r.Engine.ProvisioningEnabled() {
		networktask.SetProvisioningDisabled(vn, networktask.CreateVirtualNetwork)
		return ctrl.Result{}, store()
	}
	out, done, err := runTasks(ctx, r.Engine, vn,
		func() (networktask.Request, bool) {
			req := taskRequest(vn, kindVirtualNetwork, networktask.CreateVirtualNetwork, v1alpha1.NetworkJobManagerFabric, r.fabricVars(vn, input))
			req.BeforeLaunch = store
			// The engine writes these fields with the Succeeded entry.
			req.SerializeVNI = true
			req.Accept = func(ctx context.Context, result networktask.Result) error {
				backendID, l3VNI, err := networktask.ParseVirtualNetworkResult(result, r.networkType(vn))
				if err != nil {
					return err
				}
				if vn.Status.L3VNI != 0 && l3VNI != vn.Status.L3VNI {
					return networktask.Reject("l3_vni changed from the accepted value")
				}
				if l3VNI != 0 && vn.Status.L3VNI == 0 {
					if err := networktask.CheckVNIUnique(ctx, r.Engine.APIReader(), vn.Namespace, string(vn.UID), l3VNI, networktask.VNIKindL3); err != nil {
						return err
					}
				}
				if backendID != "" {
					vn.Status.BackendNetworkID = backendID
				}
				if l3VNI != 0 {
					vn.Status.L3VNI = l3VNI
				}
				return nil
			}
			return req, true
		},
		func() (networktask.Request, bool) {
			return taskRequest(vn, kindVirtualNetwork, networktask.CreateVirtualNetwork, v1alpha1.NetworkJobManagerKubernetes, input), hasKubernetes(vn)
		},
	)
	if err != nil {
		return ctrl.Result{}, err
	}
	if done {
		networktask.SetSucceeded(vn)
		return ctrl.Result{}, nil
	}
	reportOutcome(vn, out)
	return outcomeResult(out), nil
}

// networkType is the network_type of the Fabric tasks: set only when a
// Kubernetes Manager is recorded (contract §4.9).
func (r *VirtualNetworkReconciler) networkType(vn *v1alpha1.VirtualNetwork) string {
	if !hasKubernetes(vn) {
		return ""
	}
	return string(vn.Status.NetworkType)
}

// fabricVars adds network_type to the Fabric tasks when a Kubernetes Manager
// is recorded (contract §4.9).
func (r *VirtualNetworkReconciler) fabricVars(vn *v1alpha1.VirtualNetwork, input map[string]any) map[string]any {
	if !hasKubernetes(vn) {
		return input
	}
	return networktask.WithLaunchKeys(input, map[string]any{"network_type": string(vn.Status.NetworkType)})
}

func (r *VirtualNetworkReconciler) remove(ctx context.Context, vn *v1alpha1.VirtualNetwork) (bool, ctrl.Result, error) {
	if id := vn.Labels[osacVirtualNetworkIDLabel]; id != "" {
		children, err := r.hasChildren(ctx, vn.Namespace, id)
		if err != nil || children {
			return false, waitResult(), err
		}
	}
	input := cleanupInput(vn)
	return runCleanup(ctx, r.Engine, vn, kindVirtualNetwork,
		cleanupTask{create: networktask.CreateVirtualNetwork, manager: v1alpha1.NetworkJobManagerKubernetes,
			request: func() networktask.Request {
				return taskRequest(vn, kindVirtualNetwork, networktask.DeleteVirtualNetwork, v1alpha1.NetworkJobManagerKubernetes, input)
			}},
		cleanupTask{create: networktask.CreateVirtualNetwork, manager: v1alpha1.NetworkJobManagerFabric,
			request: func() networktask.Request {
				return taskRequest(vn, kindVirtualNetwork, networktask.DeleteVirtualNetwork, v1alpha1.NetworkJobManagerFabric, r.fabricVars(vn, input))
			}},
	)
}

// hasChildren reports whether a Subnet, SecurityGroup, NATGateway or
// FabricDomain of the VirtualNetwork exists.
func (r *VirtualNetworkReconciler) hasChildren(ctx context.Context, namespace, id string) (bool, error) {
	checks := []struct {
		list  client.ObjectList
		match func(client.Object) bool
	}{
		{&v1alpha1.SubnetList{}, func(o client.Object) bool { return o.(*v1alpha1.Subnet).Spec.VirtualNetwork == id }},
		{&v1alpha1.SecurityGroupList{}, func(o client.Object) bool { return o.(*v1alpha1.SecurityGroup).Spec.VirtualNetwork == id }},
		{&v1alpha1.NATGatewayList{}, func(o client.Object) bool { return o.(*v1alpha1.NATGateway).Spec.VirtualNetwork == id }},
		{&v1alpha1.FabricDomainList{}, func(o client.Object) bool { return o.(*v1alpha1.FabricDomain).Spec.VirtualNetwork == id }},
	}
	for _, check := range checks {
		found, err := anyItem(ctx, r.Client, check.list, namespace, check.match)
		if err != nil || found {
			return found, err
		}
	}
	return false, nil
}

// SetupWithManager registers the controller. NetworkClass events re-queue
// waiting VirtualNetworks; child events re-queue a VirtualNetwork being deleted.
func (r *VirtualNetworkReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return errors.New("local manager is nil")
	}
	ns := r.NetworkingNamespace
	inNamespace := builder.WithPredicates(NetworkingNamespacePredicate(ns))
	vnList := func() client.ObjectList { return &v1alpha1.VirtualNetworkList{} }
	parentOf := func(vnID func(client.Object) string) func(obj, trigger client.Object) bool {
		return func(obj, trigger client.Object) bool {
			return obj.GetLabels()[osacVirtualNetworkIDLabel] == vnID(trigger)
		}
	}
	return ctrl.NewControllerManagedBy(localMgr).
		Named("virtualnetwork").
		For(&v1alpha1.VirtualNetwork{}, inNamespace).
		Watches(&v1alpha1.NetworkClass{}, enqueueWhere(r.Client, ns, vnList, func(obj, _ client.Object) bool {
			return obj.(*v1alpha1.VirtualNetwork).Status.NetworkManagers == nil
		}), inNamespace).
		Watches(&v1alpha1.Subnet{}, enqueueWhere(r.Client, ns, vnList, parentOf(func(o client.Object) string {
			return o.(*v1alpha1.Subnet).Spec.VirtualNetwork
		})), inNamespace).
		Watches(&v1alpha1.SecurityGroup{}, enqueueWhere(r.Client, ns, vnList, parentOf(func(o client.Object) string {
			return o.(*v1alpha1.SecurityGroup).Spec.VirtualNetwork
		})), inNamespace).
		Watches(&v1alpha1.NATGateway{}, enqueueWhere(r.Client, ns, vnList, parentOf(func(o client.Object) string {
			return o.(*v1alpha1.NATGateway).Spec.VirtualNetwork
		})), inNamespace).
		Watches(&v1alpha1.FabricDomain{}, enqueueWhere(r.Client, ns, vnList, parentOf(func(o client.Object) string {
			return o.(*v1alpha1.FabricDomain).Spec.VirtualNetwork
		})), inNamespace).
		Complete(r)
}
