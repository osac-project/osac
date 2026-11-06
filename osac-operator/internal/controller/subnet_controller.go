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

// SubnetReconciler runs the Subnet tasks (contract §4.8-§4.10): it records
// the address layout, runs Fabric create_subnet (accepting the network
// segment), then Kubernetes create_subnet with the segment, one Subnet task
// per VirtualNetwork at a time; delete in reverse order once no
// NetworkAttachment uses the Subnet.
type SubnetReconciler struct {
	Client              client.Client
	Engine              *networktask.Engine
	NetworkingNamespace string
}

// Reconcile advances the create or delete of a Subnet.
func (r *SubnetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileNetworkResource(ctx, r.Client, r.Engine, req.NamespacedName, networkingHandlers[*v1alpha1.Subnet]{
		kind:      kindSubnet,
		finalizer: osacSubnetFinalizer,
		newObject: func() *v1alpha1.Subnet { return &v1alpha1.Subnet{} },
		status:    func(o *v1alpha1.Subnet) any { return &o.Status },
		create:    r.create,
		remove:    r.remove,
	})
}

func (r *SubnetReconciler) create(ctx context.Context, subnet *v1alpha1.Subnet) (ctrl.Result, error) {
	label, exists := subnet.Labels[networktask.VirtualNetworkIDLabel]
	if exists && label != subnet.Spec.VirtualNetwork {
		return ctrl.Result{}, errors.New("subnet virtual-network label disagrees with spec.virtualNetwork")
	}
	if !exists {
		if subnet.Labels == nil {
			subnet.Labels = map[string]string{}
		}
		subnet.Labels[networktask.VirtualNetworkIDLabel] = subnet.Spec.VirtualNetwork
		if err := r.Client.Update(ctx, subnet); err != nil {
			return ctrl.Result{}, err
		}
		return waitResult(), nil
	}
	var vn *v1alpha1.VirtualNetwork
	if subnet.Status.TaskInput == nil {
		var err error
		vn, err = findVirtualNetwork(ctx, r.Client, subnet.Namespace, subnet.Spec.VirtualNetwork)
		if err != nil {
			return ctrl.Result{}, err
		}
		if vn == nil || !isReady(vn) {
			networktask.SetProgressing(subnet, msgWaitingForParent(kindVirtualNetwork, subnet.Spec.VirtualNetwork))
			return waitResult(), nil
		}
		// With a network type the stored input carries the VirtualNetwork's
		// l3_vni (EP #427): store nothing and launch nothing until it is recorded.
		if hasKubernetes(vn) && vn.Status.NetworkType != "" && vn.Status.L3VNI == 0 {
			networktask.SetProgressing(subnet, msgWaitingForL3VNI)
			return waitResult(), nil
		}
		copyManagers(subnet, vn.Status.NetworkManagers, false)
		subnet.Status.NetworkType = vn.Status.NetworkType
	}
	var ipam *v1alpha1.SubnetIPAM
	input, store, err := inputOf(subnet, func() (map[string]any, error) {
		layout, err := networktask.Layout(subnet.Spec.IPv4CIDR, subnet.Spec.AddressPlan)
		if err != nil {
			return nil, err
		}
		ipam = layout
		resource, err := networktask.Snapshot(subnet, kindSubnet, subnet.Spec, "ipv4Cidr")
		if err != nil {
			return nil, err
		}
		vnSnapshot, err := parentSnapshot(vn, kindVirtualNetwork, vn.Spec, "ipv4Cidr", virtualNetworkStatus(vn))
		if err != nil {
			return nil, err
		}
		input := networktask.NewInput(subnet, kindSubnet, resource)
		input["virtual_network"] = vnSnapshot
		input["ipam"] = networktask.IPAMVars(layout)
		return input, nil
	}, func() { subnet.Status.IPAM = ipam })
	if err != nil {
		// Only the layout can fail here; the address plan is fixed, so this is final.
		networktask.SetFailed(subnet, err.Error())
		return ctrl.Result{}, nil
	}
	if !r.Engine.ProvisioningEnabled() {
		// VMs use the cluster default pod network in this installation mode.
		supported := true
		subnet.Status.ComputeInstanceSupported = &supported
		subnet.Status.ComputeInstanceUnsupportedReason = ""
		networktask.SetProvisioningDisabled(subnet, networktask.CreateSubnet)
		return ctrl.Result{}, store()
	}
	if !hasKubernetes(subnet) {
		supported := false
		subnet.Status.ComputeInstanceSupported = &supported
		subnet.Status.ComputeInstanceUnsupportedReason = "NetworkClass has no Kubernetes manager for VM workloads"
	}
	slot := &networktask.SubnetSlot{VirtualNetworkID: subnet.Spec.VirtualNetwork}
	networkType := r.networkType(subnet)
	out, done, err := runTasks(ctx, r.Engine, subnet,
		func() (networktask.Request, bool) {
			req := taskRequest(subnet, kindSubnet, networktask.CreateSubnet, v1alpha1.NetworkJobManagerFabric, r.fabricVars(subnet, input))
			req.BeforeLaunch = store
			// The shared key prevents concurrent Subnet tasks within one
			// VirtualNetwork and protects acceptance through its status write.
			req.Slot = slot
			req.SerializeVNI = true
			req.Accept = func(ctx context.Context, result networktask.Result) error {
				return networktask.AcceptSegment(ctx, r.Engine.APIReader(), subnet, networkType, result)
			}
			return req, true
		},
		func() (networktask.Request, bool) {
			req := taskRequest(subnet, kindSubnet, networktask.CreateSubnet, v1alpha1.NetworkJobManagerKubernetes, r.kubernetesVars(subnet, input))
			req.Slot = slot
			req.Accept = func(_ context.Context, result networktask.Result) error {
				return networktask.AcceptSubnetEligibility(subnet, result)
			}
			return req, hasKubernetes(subnet)
		},
	)
	if err != nil {
		return ctrl.Result{}, err
	}
	if done {
		networktask.SetSucceeded(subnet)
		return ctrl.Result{}, nil
	}
	reportOutcome(subnet, out)
	return outcomeResult(out), nil
}

// networkType is the network_type of the Fabric tasks: set only when a
// Kubernetes Manager is recorded (contract §4.9).
func (r *SubnetReconciler) networkType(subnet *v1alpha1.Subnet) string {
	if !hasKubernetes(subnet) {
		return ""
	}
	return string(subnet.Status.NetworkType)
}

func (r *SubnetReconciler) fabricVars(subnet *v1alpha1.Subnet, input map[string]any) map[string]any {
	if t := r.networkType(subnet); t != "" {
		return networktask.WithLaunchKeys(input, map[string]any{"network_type": t})
	}
	return input
}

func (r *SubnetReconciler) kubernetesVars(subnet *v1alpha1.Subnet, input map[string]any) map[string]any {
	return networktask.WithLaunchKeys(input, map[string]any{"network_segment": networktask.SegmentVars(subnet.Status.NetworkSegment)})
}

func (r *SubnetReconciler) remove(ctx context.Context, subnet *v1alpha1.Subnet) (bool, ctrl.Result, error) {
	if id := subnet.Labels[osacSubnetIDLabel]; id != "" {
		used, err := anyItem(ctx, r.Client, &v1alpha1.NetworkAttachmentList{}, subnet.Namespace, func(o client.Object) bool {
			return o.(*v1alpha1.NetworkAttachment).Spec.Subnet == id
		})
		if err != nil || used {
			return false, waitResult(), err
		}
	}
	input := cleanupInput(subnet)
	slot := &networktask.SubnetSlot{VirtualNetworkID: subnet.Spec.VirtualNetwork}
	done, result, err := runCleanup(ctx, r.Engine, subnet, kindSubnet,
		cleanupTask{create: networktask.CreateSubnet, manager: v1alpha1.NetworkJobManagerKubernetes,
			request: func() networktask.Request {
				req := taskRequest(subnet, kindSubnet, networktask.DeleteSubnet, v1alpha1.NetworkJobManagerKubernetes, r.kubernetesVars(subnet, input))
				req.Slot = slot
				return req
			}},
		cleanupTask{create: networktask.CreateSubnet, manager: v1alpha1.NetworkJobManagerFabric,
			request: func() networktask.Request {
				req := taskRequest(subnet, kindSubnet, networktask.DeleteSubnet, v1alpha1.NetworkJobManagerFabric, r.fabricVars(subnet, input))
				req.Slot = slot
				return req
			}},
	)
	if err != nil || !done {
		return done, result, err
	}
	return true, result, nil
}

// SetupWithManager registers the controller. VirtualNetwork events re-queue
// its Subnets; NetworkAttachment events re-queue their Subnet; Subnet events
// re-queue the siblings waiting for the VirtualNetwork's Subnet-task slot.
func (r *SubnetReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return errors.New("local manager is nil")
	}
	ns := r.NetworkingNamespace
	inNamespace := builder.WithPredicates(NetworkingNamespacePredicate(ns))
	subnetList := func() client.ObjectList { return &v1alpha1.SubnetList{} }
	return ctrl.NewControllerManagedBy(localMgr).
		Named("subnet").
		For(&v1alpha1.Subnet{}, inNamespace).
		Watches(&v1alpha1.VirtualNetwork{}, enqueueWhere(r.Client, ns, subnetList, func(obj, trigger client.Object) bool {
			return obj.(*v1alpha1.Subnet).Spec.VirtualNetwork == triggerID(trigger, kindVirtualNetwork)
		}), inNamespace).
		Watches(&v1alpha1.NetworkAttachment{}, enqueueWhere(r.Client, ns, subnetList, func(obj, trigger client.Object) bool {
			return triggerID(obj, kindSubnet) == trigger.(*v1alpha1.NetworkAttachment).Spec.Subnet
		}), inNamespace).
		Watches(&v1alpha1.Subnet{}, enqueueWhere(r.Client, ns, subnetList, func(obj, trigger client.Object) bool {
			return obj.GetUID() != trigger.GetUID() &&
				obj.(*v1alpha1.Subnet).Spec.VirtualNetwork == trigger.(*v1alpha1.Subnet).Spec.VirtualNetwork
		}), inNamespace).
		Complete(r)
}
