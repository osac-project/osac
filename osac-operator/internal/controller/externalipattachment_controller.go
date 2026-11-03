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
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
)

// ExternalIPAttachmentReconciler runs the Fabric attach_external_ip and
// detach_external_ip tasks (contract §4.10). It builds the destination from
// the target workload (VMaaS, BMaaS and CaaS designs, ExternalIPAttachment
// target), stores it in the task input and records it in status.destination.
// It adds no finalizer to the target and never deletes itself.
type ExternalIPAttachmentReconciler struct {
	Client              client.Client
	Engine              *networktask.Engine
	NetworkingNamespace string
}

// Reconcile advances the attach or detach of an ExternalIPAttachment.
func (r *ExternalIPAttachmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileNetworkResource(ctx, r.Client, r.Engine, req.NamespacedName, networkingHandlers[*v1alpha1.ExternalIPAttachment]{
		kind:      kindExternalIPAttachment,
		finalizer: osacExternalIPAttachmentFinalizer,
		newObject: func() *v1alpha1.ExternalIPAttachment { return &v1alpha1.ExternalIPAttachment{} },
		status:    func(o *v1alpha1.ExternalIPAttachment) any { return &o.Status },
		create:    r.create,
		remove:    r.remove,
		stateTransition: func(o *v1alpha1.ExternalIPAttachment, before, after networkFeedbackState) {
			stampTransition(o, &o.Status.StateTransitionTime, before, after)
		},
	})
}

// attachmentTarget is what the destination comes from.
type attachmentTarget struct {
	// found is false while the target CR does not exist.
	found    bool
	deleting bool
	// destination has an empty address while the endpoint is not ready.
	destination v1alpha1.ExternalIPAttachmentDestination
}

func (r *ExternalIPAttachmentReconciler) create(ctx context.Context, eipa *v1alpha1.ExternalIPAttachment) (ctrl.Result, error) {
	if !r.Engine.ProvisioningEnabled() {
		networktask.SetFailed(eipa, networktask.MessageNetworkingDisabled)
		return ctrl.Result{}, nil
	}
	target, err := r.resolveTarget(ctx, eipa)
	if err != nil {
		return ctrl.Result{}, err
	}
	var eip *v1alpha1.ExternalIP
	if eipa.Status.TaskInput == nil {
		eip, err = findExternalIP(ctx, r.Client, eipa.Namespace, eipa.Spec.ExternalIP)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !allocated(eip) {
			networktask.SetProgressing(eipa, msgWaitingForAllocation(eipa.Spec.ExternalIP))
			return waitResult(), nil
		}
		if !target.found || target.deleting || target.destination.Address == "" {
			networktask.SetProgressing(eipa, msgWaitingForEndpoint)
			return waitResult(), nil
		}
		copyManagers(eipa, eip.Status.NetworkManagers, true)
	}
	destination := target.destination
	input, store, err := inputOf(eipa, func() (map[string]any, error) {
		resource, err := networktask.Snapshot(eipa, kindExternalIPAttachment, eipa.Spec)
		if err != nil {
			return nil, err
		}
		input := networktask.NewInput(eipa, kindExternalIPAttachment, resource)
		input["external_ip"] = map[string]any{"id": eipa.Spec.ExternalIP, fieldAddress: eip.Status.Address}
		input["destination"] = destinationVars(destination)
		return input, nil
	}, func() { eipa.Status.Destination = destination.DeepCopy() })
	if err != nil {
		return ctrl.Result{}, err
	}
	req := taskRequest(eipa, kindExternalIPAttachment, networktask.AttachExternalIP, v1alpha1.NetworkJobManagerFabric, input)
	req.BeforeLaunch = store
	req.TargetDeleting = !target.found || target.deleting
	out, err := r.Engine.Run(ctx, eipa, req)
	if err != nil {
		return ctrl.Result{}, err
	}
	if out.Succeeded() {
		networktask.SetSucceeded(eipa)
		return ctrl.Result{}, nil
	}
	if out.Blocked {
		networktask.SetProgressing(eipa, msgWaitingForEndpoint)
		return waitResult(), nil
	}
	reportOutcome(eipa, out)
	return outcomeResult(out), nil
}

// destinationVars is the snake_case destination of the task input.
func destinationVars(d v1alpha1.ExternalIPAttachmentDestination) map[string]any {
	domain := map[string]any{"type": string(d.RoutingDomain.Type)}
	if d.RoutingDomain.Type == v1alpha1.RoutingDomainTenantVirtualNetwork {
		domain["virtual_network_id"] = d.RoutingDomain.VirtualNetworkID
		domain["subnet_id"] = d.RoutingDomain.SubnetID
	}
	return map[string]any{fieldAddress: d.Address, "routing_domain": domain}
}

// resolveTarget reads the target workload and builds the destination.
func (r *ExternalIPAttachmentReconciler) resolveTarget(ctx context.Context, eipa *v1alpha1.ExternalIPAttachment) (attachmentTarget, error) {
	spec := eipa.Spec
	switch {
	case spec.ComputeInstance != nil:
		obj, err := findByID(ctx, r.Client, &v1alpha1.ComputeInstanceList{}, "", kindComputeInstance, *spec.ComputeInstance)
		if obj == nil || err != nil {
			return attachmentTarget{}, err
		}
		ci := obj.(*v1alpha1.ComputeInstance)
		target := attachmentTarget{found: true, deleting: ci.DeletionTimestamp != nil}
		if len(ci.Status.ComputeNetworkAttachmentStatuses) == 0 || len(ci.Spec.NetworkAttachments) == 0 {
			return target, nil
		}
		return r.tenantDestination(ctx, eipa.Namespace, target,
			ci.Status.ComputeNetworkAttachmentStatuses[0].IPAddress, ci.Spec.NetworkAttachments[0].SubnetRef)
	case spec.BaremetalInstance != nil:
		obj, err := findByID(ctx, r.Client, &bmfov1alpha1.BareMetalInstanceList{}, "", kindBareMetalInstance, *spec.BaremetalInstance)
		if obj == nil || err != nil {
			return attachmentTarget{}, err
		}
		bmi := obj.(*bmfov1alpha1.BareMetalInstance)
		target := attachmentTarget{found: true, deleting: bmi.DeletionTimestamp != nil}
		if len(bmi.Status.NetworkAttachmentStatuses) == 0 || len(bmi.Spec.NetworkAttachments) == 0 {
			return target, nil
		}
		return r.tenantDestination(ctx, eipa.Namespace, target,
			bmi.Status.NetworkAttachmentStatuses[0].IPAddress, bmi.Spec.NetworkAttachments[0].SubnetRef)
	case spec.Cluster != nil:
		obj, err := findByID(ctx, r.Client, &v1alpha1.ClusterOrderList{}, "", kindClusterOrder, *spec.Cluster)
		if obj == nil || err != nil {
			return attachmentTarget{}, err
		}
		co := obj.(*v1alpha1.ClusterOrder)
		target := attachmentTarget{found: true, deleting: co.DeletionTimestamp != nil}
		if spec.TargetEndpoint == nil {
			return target, nil
		}
		switch *spec.TargetEndpoint {
		case v1alpha1.ExternalIPAttachmentTargetEndpointAPI:
			target.destination = v1alpha1.ExternalIPAttachmentDestination{
				Address:       co.Status.ApiEndpoint,
				RoutingDomain: v1alpha1.ExternalIPAttachmentRoutingDomain{Type: v1alpha1.RoutingDomainProviderManagement},
			}
			return target, nil
		case v1alpha1.ExternalIPAttachmentTargetEndpointIngress:
			if co.Spec.NetworkAttachment == nil {
				return target, nil
			}
			return r.tenantDestination(ctx, eipa.Namespace, target, co.Status.IngressEndpoint, co.Spec.NetworkAttachment.SubnetRef)
		}
		return target, nil
	}
	return attachmentTarget{}, fmt.Errorf("ExternalIPAttachment %s has no target", eipa.Name)
}

// tenantDestination is a destination on the tenant VirtualNetwork of a Subnet.
// The address stays empty while the Subnet is unknown.
func (r *ExternalIPAttachmentReconciler) tenantDestination(ctx context.Context, namespace string, target attachmentTarget, address, subnetID string) (attachmentTarget, error) {
	if address == "" {
		return target, nil
	}
	subnet, err := findSubnet(ctx, r.Client, namespace, subnetID)
	if subnet == nil || err != nil {
		return target, err
	}
	target.destination = v1alpha1.ExternalIPAttachmentDestination{
		Address: address,
		RoutingDomain: v1alpha1.ExternalIPAttachmentRoutingDomain{
			Type:             v1alpha1.RoutingDomainTenantVirtualNetwork,
			VirtualNetworkID: subnet.Spec.VirtualNetwork,
			SubnetID:         subnetID,
		},
	}
	return target, nil
}

func (r *ExternalIPAttachmentReconciler) remove(ctx context.Context, eipa *v1alpha1.ExternalIPAttachment) (bool, ctrl.Result, error) {
	input := cleanupInput(eipa)
	return runCleanup(ctx, r.Engine, eipa, kindExternalIPAttachment,
		cleanupTask{create: networktask.AttachExternalIP, manager: v1alpha1.NetworkJobManagerFabric,
			request: func() networktask.Request {
				return taskRequest(eipa, kindExternalIPAttachment, networktask.DetachExternalIP, v1alpha1.NetworkJobManagerFabric, input)
			}},
	)
}

// SetupWithManager registers the controller. ExternalIP events and target
// workload events re-queue the ExternalIPAttachments that use them.
func (r *ExternalIPAttachmentReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return errors.New("local manager is nil")
	}
	ns := r.NetworkingNamespace
	inNamespace := builder.WithPredicates(NetworkingNamespacePredicate(ns))
	eipaList := func() client.ObjectList { return &v1alpha1.ExternalIPAttachmentList{} }
	targetIs := func(kind string, ref func(v1alpha1.ExternalIPAttachmentSpec) *string) handler.EventHandler {
		return enqueueWhere(r.Client, ns, eipaList, func(obj, trigger client.Object) bool {
			id := ref(obj.(*v1alpha1.ExternalIPAttachment).Spec)
			return id != nil && *id == triggerID(trigger, kind)
		})
	}
	return ctrl.NewControllerManagedBy(localMgr).
		Named("externalipattachment").
		For(&v1alpha1.ExternalIPAttachment{}, inNamespace).
		Watches(&v1alpha1.ExternalIP{}, enqueueWhere(r.Client, ns, eipaList, func(obj, trigger client.Object) bool {
			return obj.(*v1alpha1.ExternalIPAttachment).Spec.ExternalIP == triggerID(trigger, kindExternalIP)
		}), inNamespace).
		Watches(&v1alpha1.ComputeInstance{}, targetIs(kindComputeInstance, func(s v1alpha1.ExternalIPAttachmentSpec) *string { return s.ComputeInstance })).
		Watches(&v1alpha1.ClusterOrder{}, targetIs(kindClusterOrder, func(s v1alpha1.ExternalIPAttachmentSpec) *string { return s.Cluster })).
		Watches(&bmfov1alpha1.BareMetalInstance{},
			targetIs(kindBareMetalInstance, func(s v1alpha1.ExternalIPAttachmentSpec) *string { return s.BaremetalInstance })).
		Complete(r)
}
