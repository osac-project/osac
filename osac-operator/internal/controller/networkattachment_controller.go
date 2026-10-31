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
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
)

// handoffRecheckInterval is how often a physical NetworkAttachment checks the
// BareMetalInstance for NetworkHandoffComplete before query_dhcp_lease.
const handoffRecheckInterval = 30 * time.Second

// NetworkAttachmentReconciler runs the attachment tasks of one workload
// interface (Unified design, NetworkAttachment; contract §4.10):
//   - overlay (VM): Kubernetes apply_network_attachment (accepting
//     vm_attachment), then, once VMaaS reported the VM address, the Fabric
//     apply with network_attachment.interface.ipv4_address; delete Fabric,
//     then Kubernetes;
//   - physical (bare metal): the Fabric apply, then query_dhcp_lease once the
//     BareMetalInstance reports NetworkHandoffComplete; delete on the Fabric.
type NetworkAttachmentReconciler struct {
	Client              client.Client
	Engine              *networktask.Engine
	NetworkingNamespace string
}

// Reconcile advances the apply or delete of a NetworkAttachment.
func (r *NetworkAttachmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileNetworkResource(ctx, r.Client, r.Engine, req.NamespacedName, networkingHandlers[*v1alpha1.NetworkAttachment]{
		kind:      kindNetworkAttachment,
		finalizer: osacNetworkAttachmentFinalizer,
		newObject: func() *v1alpha1.NetworkAttachment { return &v1alpha1.NetworkAttachment{} },
		status:    func(o *v1alpha1.NetworkAttachment) any { return &o.Status },
		create:    r.create,
		remove:    r.remove,
	})
}

func msgNoPhysicalRange(subnetID string) string {
	return fmt.Sprintf("Subnet %s has no physical address range", subnetID)
}

func msgNoKubernetesManager(subnetID string) string {
	return fmt.Sprintf("Subnet %s has no Kubernetes Manager for VM attachments", subnetID)
}

// attachmentWorkload is the target workload of a NetworkAttachment.
type attachmentWorkload struct {
	ci  *v1alpha1.ComputeInstance
	bmi *bmfov1alpha1.BareMetalInstance
}

func (w attachmentWorkload) deleting() bool {
	switch {
	case w.ci != nil:
		return w.ci.DeletionTimestamp != nil
	case w.bmi != nil:
		return w.bmi.DeletionTimestamp != nil
	}
	return true // a missing target never gets a new apply
}

func (r *NetworkAttachmentReconciler) workload(ctx context.Context, na *v1alpha1.NetworkAttachment) (attachmentWorkload, error) {
	if na.Spec.Target.Kind == v1alpha1.NetworkAttachmentTargetComputeInstance {
		obj, err := findByID(ctx, r.Client, &v1alpha1.ComputeInstanceList{}, "", kindComputeInstance, na.Spec.Target.ID)
		if obj == nil || err != nil {
			return attachmentWorkload{}, err
		}
		return attachmentWorkload{ci: obj.(*v1alpha1.ComputeInstance)}, nil
	}
	obj, err := findByID(ctx, r.Client, &bmfov1alpha1.BareMetalInstanceList{}, "", kindBareMetalInstance, na.Spec.Target.ID)
	if obj == nil || err != nil {
		return attachmentWorkload{}, err
	}
	return attachmentWorkload{bmi: obj.(*bmfov1alpha1.BareMetalInstance)}, nil
}

func (r *NetworkAttachmentReconciler) create(ctx context.Context, na *v1alpha1.NetworkAttachment) (ctrl.Result, error) {
	if !r.Engine.ProvisioningEnabled() {
		// Workload services create no NetworkAttachment with provider networking disabled.
		networktask.SetFailed(na, networktask.MessageNetworkingDisabled)
		return ctrl.Result{}, nil
	}
	subnet, err := findSubnet(ctx, r.Client, na.Namespace, na.Spec.Subnet)
	if err != nil {
		return ctrl.Result{}, err
	}
	if subnet == nil || (na.Status.TaskInput == nil && !isReady(subnet)) {
		networktask.SetProgressing(na, msgWaitingForParent(kindSubnet, na.Spec.Subnet))
		return waitResult(), nil
	}
	workload, err := r.workload(ctx, na)
	if err != nil {
		return ctrl.Result{}, err
	}
	physical := na.Spec.Interface.Type == v1alpha1.NetworkInterfacePhysical
	if na.Status.TaskInput == nil {
		copyManagers(na, subnet.Status.NetworkManagers, false)
		na.Status.NetworkType = subnet.Status.NetworkType
		if physical && (subnet.Status.IPAM == nil || len(subnet.Status.IPAM.PhysicalIPv4CIDRs) == 0) {
			networktask.SetFailed(na, msgNoPhysicalRange(na.Spec.Subnet))
			return ctrl.Result{}, nil
		}
		if !physical && !hasKubernetes(na) {
			networktask.SetFailed(na, msgNoKubernetesManager(na.Spec.Subnet))
			return ctrl.Result{}, nil
		}
		if physical && (workload.bmi == nil || workload.bmi.Spec.ExternalHostName == "") {
			networktask.SetProgressing(na, fmt.Sprintf("waiting for the host name of BareMetalInstance %s", na.Spec.Target.ID))
			return waitResult(), nil
		}
	}
	input, store, err := inputOf(na, func() (map[string]any, error) { return r.buildInput(ctx, na, subnet, workload) }, nil)
	if err != nil {
		return ctrl.Result{}, err
	}
	if physical {
		return r.applyPhysical(ctx, na, subnet, workload, input, store)
	}
	return r.applyOverlay(ctx, na, subnet, workload, input, store)
}

// buildInput builds the attachment inputs (contract §4.10).
func (r *NetworkAttachmentReconciler) buildInput(ctx context.Context, na *v1alpha1.NetworkAttachment, subnet *v1alpha1.Subnet, workload attachmentWorkload) (map[string]any, error) {
	vn, err := findVirtualNetwork(ctx, r.Client, subnet.Namespace, subnet.Spec.VirtualNetwork)
	if err != nil {
		return nil, err
	}
	if vn == nil {
		return nil, fmt.Errorf("VirtualNetwork %s of Subnet %s not found", subnet.Spec.VirtualNetwork, na.Spec.Subnet)
	}
	resource, err := networktask.Snapshot(na, kindNetworkAttachment, na.Spec, "target", "subnet")
	if err != nil {
		return nil, err
	}
	vnSnapshot, err := parentSnapshot(vn, kindVirtualNetwork, vn.Spec, "ipv4Cidr", virtualNetworkStatus(vn))
	if err != nil {
		return nil, err
	}
	subnetSnapshot, err := parentSnapshot(subnet, kindSubnet, subnet.Spec, "ipv4Cidr", nil)
	if err != nil {
		return nil, err
	}
	target := map[string]any{"kind": string(na.Spec.Target.Kind), "id": na.Spec.Target.ID}
	iface := map[string]any{"type": string(na.Spec.Interface.Type)}
	if na.Spec.Interface.Type == v1alpha1.NetworkInterfacePhysical {
		target["host_name"] = workload.bmi.Spec.ExternalHostName
		iface["name"] = na.Spec.Interface.Name
		iface["mac_address"] = na.Spec.Interface.MACAddress
	}
	groups := make([]any, 0, len(na.Spec.SecurityGroups))
	for _, id := range na.Spec.SecurityGroups {
		groups = append(groups, id)
	}
	input := networktask.NewInput(na, kindNetworkAttachment, resource)
	input["target"] = target
	input["network_attachment"] = map[string]any{
		"subnet":             na.Spec.Subnet,
		"interface":          iface,
		"security_group_ids": groups,
	}
	input["virtual_network"] = vnSnapshot
	input["subnet"] = subnetSnapshot
	input["ipam"] = networktask.IPAMVars(subnet.Status.IPAM)
	return input, nil
}

func (r *NetworkAttachmentReconciler) kubernetesVars(subnet *v1alpha1.Subnet, input map[string]any) map[string]any {
	return networktask.WithLaunchKeys(input, map[string]any{"network_segment": networktask.SegmentVars(subnet.Status.NetworkSegment)})
}

// applyOverlay runs the Kubernetes apply, waits for the VM address, records
// it in the stored input once, and runs the Fabric apply.
func (r *NetworkAttachmentReconciler) applyOverlay(ctx context.Context, na *v1alpha1.NetworkAttachment, subnet *v1alpha1.Subnet,
	workload attachmentWorkload, input map[string]any, store func() error) (ctrl.Result, error) {
	if !networktask.Started(na, networktask.ApplyNetworkAttachment, v1alpha1.NetworkJobManagerKubernetes) && subnet.Status.NetworkSegment == nil {
		networktask.SetProgressing(na, msgWaitingForParent(kindSubnet, na.Spec.Subnet))
		return waitResult(), nil
	}
	req := taskRequest(na, kindNetworkAttachment, networktask.ApplyNetworkAttachment, v1alpha1.NetworkJobManagerKubernetes, r.kubernetesVars(subnet, input))
	req.BeforeLaunch = store
	req.TargetDeleting = workload.deleting()
	tenant := na.Annotations[networktask.TenantAnnotation]
	req.Accept = func(ctx context.Context, result networktask.Result) error {
		attachment, err := networktask.ParseVMAttachment(ctx, r.Engine.APIReader(), result, tenant)
		if err != nil {
			return err
		}
		na.Status.VMAttachment = attachment
		return nil
	}
	out, err := r.Engine.Run(ctx, na, req)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !out.Succeeded() {
		return r.report(na, out)
	}

	// The Fabric apply binds the VM address VMaaS reported; it is added to
	// the stored input once, before the first Fabric apply (contract §4.5).
	iface := interfaceVars(input)
	if _, ok := iface["ipv4_address"]; !ok {
		address := ""
		if ci := workload.ci; ci != nil && len(ci.Status.ComputeNetworkAttachmentStatuses) > 0 {
			address = ci.Status.ComputeNetworkAttachmentStatuses[0].IPAddress
		}
		if address == "" {
			networktask.SetProgressing(na, msgWaitingForVMAddress)
			return waitResult(), nil
		}
		iface["ipv4_address"] = address
	}
	fabric := taskRequest(na, kindNetworkAttachment, networktask.ApplyNetworkAttachment, v1alpha1.NetworkJobManagerFabric, input)
	fabric.BeforeLaunch = func() error { return networktask.StoreInput(na, input) }
	fabric.TargetDeleting = workload.deleting()
	out, err = r.Engine.Run(ctx, na, fabric)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !out.Succeeded() {
		return r.report(na, out)
	}
	networktask.SetSucceeded(na)
	return ctrl.Result{}, nil
}

// interfaceVars returns network_attachment.interface of an input.
func interfaceVars(input map[string]any) map[string]any {
	attachment, _ := input["network_attachment"].(map[string]any)
	iface, _ := attachment["interface"].(map[string]any)
	if iface == nil {
		iface = map[string]any{}
		if attachment != nil {
			attachment["interface"] = iface
		}
	}
	return iface
}

// applyPhysical runs the Fabric apply, then query_dhcp_lease once the
// BareMetalInstance reports NetworkHandoffComplete. The query never changes Ready.
func (r *NetworkAttachmentReconciler) applyPhysical(ctx context.Context, na *v1alpha1.NetworkAttachment, subnet *v1alpha1.Subnet,
	workload attachmentWorkload, input map[string]any, store func() error) (ctrl.Result, error) {
	req := taskRequest(na, kindNetworkAttachment, networktask.ApplyNetworkAttachment, v1alpha1.NetworkJobManagerFabric, input)
	req.BeforeLaunch = store
	req.TargetDeleting = workload.deleting()
	out, err := r.Engine.Run(ctx, na, req)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !out.Succeeded() {
		return r.report(na, out)
	}
	networktask.SetSucceeded(na)

	if na.Status.DHCPLease != nil {
		return ctrl.Result{}, nil
	}
	if !networktask.Started(na, networktask.QueryDHCPLease, v1alpha1.NetworkJobManagerFabric) {
		if workload.bmi == nil || !meta.IsStatusConditionTrue(workload.bmi.Status.Conditions,
			string(bmfov1alpha1.HostConditionNetworkHandoffComplete)) {
			return ctrl.Result{RequeueAfter: handoffRecheckInterval}, nil
		}
	}
	query := taskRequest(na, kindNetworkAttachment, networktask.QueryDHCPLease, v1alpha1.NetworkJobManagerFabric, input)
	query.TargetDeleting = workload.deleting()
	query.Accept = func(_ context.Context, result networktask.Result) error {
		lease, err := networktask.ParseDHCPLease(result, na.Spec.Interface.MACAddress, subnet.Spec.IPv4CIDR, subnet.Status.IPAM)
		if err != nil {
			return err
		}
		na.Status.DHCPLease = lease
		return nil
	}
	out, err = r.Engine.Run(ctx, na, query)
	if err != nil {
		return ctrl.Result{}, err
	}
	return outcomeResult(out), nil
}

func (r *NetworkAttachmentReconciler) report(na *v1alpha1.NetworkAttachment, out networktask.Outcome) (ctrl.Result, error) {
	if out.Blocked {
		networktask.SetProgressing(na, fmt.Sprintf("%s %s is being deleted", na.Spec.Target.Kind, na.Spec.Target.ID))
		return waitResult(), nil
	}
	reportOutcome(na, out)
	return outcomeResult(out), nil
}

func (r *NetworkAttachmentReconciler) remove(ctx context.Context, na *v1alpha1.NetworkAttachment) (bool, ctrl.Result, error) {
	input := cleanupInput(na)
	request := func(manager v1alpha1.NetworkJobManager) func() networktask.Request {
		return func() networktask.Request {
			vars := input
			if manager == v1alpha1.NetworkJobManagerKubernetes {
				subnet, err := findSubnet(ctx, r.Client, na.Namespace, na.Spec.Subnet)
				if err == nil && subnet != nil {
					vars = r.kubernetesVars(subnet, input)
				}
			}
			return taskRequest(na, kindNetworkAttachment, networktask.DeleteNetworkAttachment, manager, vars)
		}
	}
	return runCleanup(ctx, r.Engine, na, kindNetworkAttachment,
		cleanupTask{create: networktask.ApplyNetworkAttachment, manager: v1alpha1.NetworkJobManagerFabric,
			request: request(v1alpha1.NetworkJobManagerFabric)},
		cleanupTask{create: networktask.ApplyNetworkAttachment, manager: v1alpha1.NetworkJobManagerKubernetes,
			request: request(v1alpha1.NetworkJobManagerKubernetes)},
	)
}

// SetupWithManager registers the controller. Subnet events re-queue its
// NetworkAttachments; ComputeInstance and BareMetalInstance events re-queue
// the NetworkAttachment of the workload.
func (r *NetworkAttachmentReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return errors.New("local manager is nil")
	}
	ns := r.NetworkingNamespace
	inNamespace := builder.WithPredicates(NetworkingNamespacePredicate(ns))
	naList := func() client.ObjectList { return &v1alpha1.NetworkAttachmentList{} }
	targetIs := func(kind v1alpha1.NetworkAttachmentTargetKind) func(obj, trigger client.Object) bool {
		return func(obj, trigger client.Object) bool {
			target := obj.(*v1alpha1.NetworkAttachment).Spec.Target
			return target.Kind == kind && target.ID == triggerID(trigger, string(kind))
		}
	}
	return ctrl.NewControllerManagedBy(localMgr).
		Named("networkattachment").
		For(&v1alpha1.NetworkAttachment{}, inNamespace).
		Watches(&v1alpha1.Subnet{}, enqueueWhere(r.Client, ns, naList, func(obj, trigger client.Object) bool {
			return obj.(*v1alpha1.NetworkAttachment).Spec.Subnet == triggerID(trigger, kindSubnet)
		}), inNamespace).
		Watches(&v1alpha1.ComputeInstance{}, enqueueWhere(r.Client, ns, naList, targetIs(v1alpha1.NetworkAttachmentTargetComputeInstance))).
		Watches(&bmfov1alpha1.BareMetalInstance{}, enqueueWhere(r.Client, ns, naList, targetIs(v1alpha1.NetworkAttachmentTargetBareMetalInstance))).
		Complete(r)
}
