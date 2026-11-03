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
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
)

// Shared plumbing of the networking resource controllers, which run their
// manager tasks on the network task engine (internal/networktask).

// CR kinds, as used in API ID labels, snapshots and job labels.
const (
	kindVirtualNetwork       = "VirtualNetwork"
	kindSubnet               = "Subnet"
	kindSecurityGroup        = "SecurityGroup"
	kindExternalIPPool       = "ExternalIPPool"
	kindExternalIP           = "ExternalIP"
	kindExternalIPAttachment = "ExternalIPAttachment"
	kindNATGateway           = "NATGateway"
	kindFabricDomain         = "FabricDomain"
	kindNetworkAttachment    = "NetworkAttachment"
	kindComputeInstance      = "ComputeInstance"
	kindBareMetalInstance    = "BareMetalInstance"
	kindClusterOrder         = "ClusterOrder"
	externalIPAddressKey     = "address"
)

// Finalizers of the networking resource controllers.
const (
	osacVirtualNetworkFinalizer       = "osac.openshift.io/virtualnetwork-finalizer"
	osacSubnetFinalizer               = "osac.openshift.io/subnet-finalizer"
	osacSecurityGroupFinalizer        = "osac.openshift.io/securitygroup-finalizer"
	osacExternalIPPoolFinalizer       = "osac.openshift.io/externalippool-finalizer"
	osacExternalIPFinalizer           = "osac.openshift.io/externalip-finalizer"
	osacExternalIPAttachmentFinalizer = "osac.openshift.io/externalipattachment-finalizer"
	osacNATGatewayFinalizer           = "osac.openshift.io/natgateway-finalizer"
	osacFabricDomainFinalizer         = "osac.openshift.io/fabricdomain-finalizer"
	// osacNetworkAttachmentFinalizer is the networking controller's finalizer
	// on a NetworkAttachment (Unified design, NetworkAttachment).
	osacNetworkAttachmentFinalizer = "osac.openshift.io/networkattachment"
)

// networkingWaitInterval is how often a resource rechecks what it waits for
// (a parent becoming Ready, children being deleted, a target address).
const networkingWaitInterval = 10 * time.Second

// Progressing messages of the networking resource controllers.
const (
	msgWaitingForNetworkClass = "waiting for the NetworkClass to be READY"
	msgWaitingForSlot         = "waiting for another Subnet task of the VirtualNetwork to finish"
	msgWaitingForEndpoint     = "waiting for the target endpoint address"
	msgWaitingForVMAddress    = "waiting for the VM address"
	msgWaitingForL3VNI        = "waiting for the VirtualNetwork's l3_vni"
	msgEastWestNotEnabled     = "east-west Ethernet is not enabled for this deployment"
)

func msgWaitingForParent(kind, id string) string {
	return fmt.Sprintf("waiting for %s %s to be READY", kind, id)
}

// networkResource is a networking CR the engine runs tasks on.
type networkResource interface {
	v1alpha1.NetworkResourceObject
	client.Object
}

// networkingHandlers are the kind-specific parts of a networking resource controller.
type networkingHandlers[T networkResource] struct {
	kind      string
	finalizer string
	newObject func() T
	// status returns the object's status, compared to decide the final write.
	status func(T) any
	// create advances the create (or update) and sets the Ready condition.
	create func(ctx context.Context, obj T) (ctrl.Result, error)
	// remove advances the delete; it returns true once the finalizer may go.
	remove func(ctx context.Context, obj T) (bool, ctrl.Result, error)
	// stateTransition, when set, records the API state transition time.
	stateTransition func(obj T, before, after networkFeedbackState)
	// observeTransition, when set, runs after a status write succeeded, with
	// the object as it was read. A conflict or failed write emits nothing.
	observeTransition func(obj, before T)
	// observeDeleted, when set, runs after the finalizer removal succeeded.
	observeDeleted func(obj T)
}

// reconcileNetworkResource is the shared reconcile loop of the networking
// resource controllers: management-state check, finalizer, create or delete,
// and one final status write. Status writes of the engine happen in between
// (resourceVersion-conditional); a conflict requeues without a write.
func reconcileNetworkResource[T networkResource](
	ctx context.Context, c client.Client, engine *networktask.Engine, key types.NamespacedName, h networkingHandlers[T],
) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx).WithValues("kind", h.kind)
	obj := h.newObject()
	if err := c.Get(ctx, key, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	deleting := obj.GetDeletionTimestamp() != nil
	if !deleting && obj.GetAnnotations()[osacManagementStateAnnotation] == ManagementStateUnmanaged {
		log.Info("Ignoring resource due to the management-state annotation")
		return ctrl.Result{}, nil
	}
	before := obj.DeepCopyObject().(T)
	stateBefore, _ := networkFeedback(before, *before.GetConditionsRef())

	var result ctrl.Result
	var err error
	if deleting {
		if !controllerutil.ContainsFinalizer(obj, h.finalizer) {
			return ctrl.Result{}, nil
		}
		var done bool
		done, result, err = h.remove(ctx, obj)
		if err == nil && done {
			controllerutil.RemoveFinalizer(obj, h.finalizer)
			if err := c.Update(ctx, obj); err != nil {
				return requeueOnConflict(err)
			}
			engine.Forget(obj.GetUID())
			log.Info("Delete complete; finalizer removed")
			if h.observeDeleted != nil {
				h.observeDeleted(obj)
			}
			return ctrl.Result{}, nil
		}
	} else {
		if controllerutil.AddFinalizer(obj, h.finalizer) {
			if err := c.Update(ctx, obj); err != nil {
				return requeueOnConflict(err)
			}
		}
		result, err = h.create(ctx, obj)
	}
	if err != nil {
		return requeueOnConflict(err)
	}
	if h.stateTransition != nil {
		stateAfter, _ := networkFeedback(obj, *obj.GetConditionsRef())
		h.stateTransition(obj, stateBefore, stateAfter)
	}
	if !equality.Semantic.DeepEqual(h.status(before), h.status(obj)) {
		if err := c.Status().Update(ctx, obj); err != nil {
			return requeueOnConflict(err)
		}
		if h.observeTransition != nil {
			h.observeTransition(obj, before)
		}
	}
	return result, nil
}

// requeueOnConflict turns a write conflict into a quick requeue: the
// in-memory object is stale and must not be written again.
func requeueOnConflict(err error) (ctrl.Result, error) {
	if networktask.IsConflict(err) {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	return ctrl.Result{}, err
}

// stampTransition sets *at to now when the API state changed, and once when a
// delete is first seen.
func stampTransition(obj client.Object, at **metav1.Time, before, after networkFeedbackState) {
	now := metav1.Now()
	switch {
	case after == networkFeedbackDeleting:
		if *at == nil || (*at).Before(obj.GetDeletionTimestamp()) {
			*at = &now
		}
	case before != after || *at == nil:
		*at = &now
	}
}

// waitResult requeues a resource that waits for something outside the engine.
func waitResult() ctrl.Result { return ctrl.Result{RequeueAfter: networkingWaitInterval} }

// outcomeResult requeues as the engine asks.
func outcomeResult(out networktask.Outcome) ctrl.Result {
	return ctrl.Result{RequeueAfter: out.RequeueAfter}
}

// isReady reports whether a networking CR is Ready and not being deleted.
func isReady(obj networkResource) bool {
	return obj.GetDeletionTimestamp() == nil &&
		meta.IsStatusConditionTrue(*obj.GetConditionsRef(), v1alpha1.NetworkConditionReady)
}

// reportOutcome sets the Ready condition of an unfinished or failed task.
func reportOutcome(obj networkResource, out networktask.Outcome) {
	switch {
	case out.Failed():
		networktask.SetFailed(obj, out.Entry.Message)
	case out.WaitingForSlot:
		networktask.SetProgressing(obj, msgWaitingForSlot)
	default:
		networktask.SetProgressing(obj, networktask.ProgressMessage(out.Entry))
	}
}

// findByID returns the CR of kind with the API ID label, or nil when none
// exists. An empty namespace searches every namespace.
func findByID(ctx context.Context, c client.Reader, list client.ObjectList, namespace, kind, id string) (client.Object, error) {
	if id == "" {
		return nil, nil
	}
	opts := []client.ListOption{client.MatchingLabels{networktask.IDLabel(kind): id}}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if err := c.List(ctx, list, opts...); err != nil {
		return nil, fmt.Errorf("listing %s CRs with API ID %s: %w", kind, id, err)
	}
	items, err := meta.ExtractList(list)
	if err != nil {
		return nil, err
	}
	switch len(items) {
	case 0:
		return nil, nil
	case 1:
		return items[0].(client.Object), nil
	default:
		return nil, fmt.Errorf("%d %s CRs carry the API ID %s", len(items), kind, id)
	}
}

func findVirtualNetwork(ctx context.Context, c client.Reader, namespace, id string) (*v1alpha1.VirtualNetwork, error) {
	obj, err := findByID(ctx, c, &v1alpha1.VirtualNetworkList{}, namespace, kindVirtualNetwork, id)
	if obj == nil || err != nil {
		return nil, err
	}
	return obj.(*v1alpha1.VirtualNetwork), nil
}

func findSubnet(ctx context.Context, c client.Reader, namespace, id string) (*v1alpha1.Subnet, error) {
	obj, err := findByID(ctx, c, &v1alpha1.SubnetList{}, namespace, kindSubnet, id)
	if obj == nil || err != nil {
		return nil, err
	}
	return obj.(*v1alpha1.Subnet), nil
}

func findExternalIPPool(ctx context.Context, c client.Reader, namespace, id string) (*v1alpha1.ExternalIPPool, error) {
	obj, err := findByID(ctx, c, &v1alpha1.ExternalIPPoolList{}, namespace, kindExternalIPPool, id)
	if obj == nil || err != nil {
		return nil, err
	}
	return obj.(*v1alpha1.ExternalIPPool), nil
}

func findExternalIP(ctx context.Context, c client.Reader, namespace, id string) (*v1alpha1.ExternalIP, error) {
	obj, err := findByID(ctx, c, &v1alpha1.ExternalIPList{}, namespace, kindExternalIP, id)
	if obj == nil || err != nil {
		return nil, err
	}
	return obj.(*v1alpha1.ExternalIP), nil
}

// readyNetworkClass returns the deployment's NetworkClass when it is Ready
// (a deployment has exactly one), or nil while none is. Manager status is
// required only when provider networking is enabled.
func readyNetworkClass(
	ctx context.Context, c client.Reader, namespace string, provisioningEnabled bool,
) (*v1alpha1.NetworkClass, error) {
	list := &v1alpha1.NetworkClassList{}
	if err := c.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("listing NetworkClasses: %w", err)
	}
	var ready []*v1alpha1.NetworkClass
	for i := range list.Items {
		nc := &list.Items[i]
		if nc.DeletionTimestamp == nil && meta.IsStatusConditionTrue(nc.Status.Conditions, v1alpha1.NetworkConditionReady) &&
			(!provisioningEnabled || (nc.Status.NetworkManagers != nil && nc.Status.NetworkManagers.Fabric != nil)) {
			ready = append(ready, nc)
		}
	}
	if len(ready) != 1 {
		return nil, nil
	}
	return ready[0], nil
}

// copyManagers records the managers (and, under a VirtualNetwork, the network
// type) of a parent on a child once, at create (contract §4.1).
func copyManagers(obj networkResource, managers *v1alpha1.NetworkManagers, fabricOnly bool) {
	if obj.GetNetworkManagers() != nil || managers == nil {
		return
	}
	copied := managers.DeepCopy()
	if fabricOnly {
		copied.Kubernetes = nil
	}
	obj.SetNetworkManagers(copied)
}

// inputOf returns the stored task input, or the input built by build when
// none is stored yet. store writes it (and extra, when set) to the object; it
// is a no-op once the input is stored. Pass store as the BeforeLaunch of the
// first task, so the input lands in the same status write as the first
// Running entry (contract §4.5).
func inputOf(obj v1alpha1.NetworkInputObject, build func() (map[string]any, error), extra func()) (map[string]any, func() error, error) {
	input, err := networktask.LoadInput(obj)
	if err != nil {
		return nil, nil, err
	}
	if input != nil {
		return input, func() error { return nil }, nil
	}
	input, err = build()
	if err != nil {
		return nil, nil, err
	}
	return input, func() error {
		if extra != nil {
			extra()
		}
		return networktask.StoreInput(obj, input)
	}, nil
}

// parentSnapshot is a parent snapshot: spec.ipv4Cidr (a pool: spec.cidrs)
// only, plus status when it is not empty.
func parentSnapshot(obj client.Object, kind string, spec any, field string, status map[string]any) (map[string]any, error) {
	snapshot, err := networktask.Snapshot(obj, kind, spec, field)
	if err != nil {
		return nil, err
	}
	if len(status) > 0 {
		snapshot["status"] = status
	}
	return snapshot, nil
}

// virtualNetworkStatus is the status of the virtual_network parent snapshot
// of the Subnet and NetworkAttachment inputs: the recorded l3Vni (EP #427),
// or nil when none was recorded.
func virtualNetworkStatus(vn *v1alpha1.VirtualNetwork) map[string]any {
	if vn.Status.L3VNI == 0 {
		return nil
	}
	return map[string]any{"l3Vni": vn.Status.L3VNI}
}

// taskRequest builds the Request of a manager task of a networking CR.
func taskRequest(obj networkResource, kind, task string, manager v1alpha1.NetworkJobManager, vars map[string]any) networktask.Request {
	return networktask.Request{
		Task:       task,
		Manager:    manager,
		ManagerRef: networktask.ManagerRef(obj.GetNetworkManagers(), manager),
		Kind:       kind,
		Vars:       vars,
	}
}

// hasKubernetes reports whether a Kubernetes Manager is recorded.
func hasKubernetes(obj networkResource) bool {
	m := obj.GetNetworkManagers()
	return m != nil && m.Kubernetes != nil
}

// runTasks runs the requests in order and stops at the first one that has
// not succeeded. It returns that request's outcome (or the last one) and
// whether all succeeded. A builder returning ok=false is skipped.
func runTasks(ctx context.Context, engine *networktask.Engine, obj networkResource, builders ...func() (networktask.Request, bool)) (networktask.Outcome, bool, error) {
	var out networktask.Outcome
	for _, build := range builders {
		req, ok := build()
		if !ok {
			continue
		}
		var err error
		out, err = engine.Run(ctx, obj, req)
		if err != nil {
			return out, false, err
		}
		if !out.Succeeded() {
			return out, false, nil
		}
	}
	return out, true, nil
}

// cleanupTask is one delete, detach or release of a delete sequence; it runs
// only when its create task started on that manager (contract §4.7 Finalizers).
type cleanupTask struct {
	create  string
	manager v1alpha1.NetworkJobManager
	request func() networktask.Request
}

// runCleanup cancels the running create jobs, then runs the cleanup tasks in
// order. It returns true once every started manager's cleanup succeeded.
func runCleanup(ctx context.Context, engine *networktask.Engine, obj networkResource, kind string, tasks ...cleanupTask) (bool, ctrl.Result, error) {
	ok, wait, err := engine.CancelForDelete(ctx, obj, kind)
	if err != nil {
		return false, ctrl.Result{}, err
	}
	if !ok {
		return false, ctrl.Result{RequeueAfter: wait}, nil
	}
	for _, task := range tasks {
		if !networktask.Started(obj, task.create, task.manager) {
			continue
		}
		out, err := engine.Run(ctx, obj, task.request())
		if err != nil {
			return false, ctrl.Result{}, err
		}
		if !out.Succeeded() {
			requeue := out.RequeueAfter
			if requeue == 0 {
				requeue = networkingWaitInterval
			}
			return false, ctrl.Result{RequeueAfter: requeue}, nil
		}
	}
	return true, ctrl.Result{}, nil
}

// cleanupInput returns the stored input for a cleanup task.
func cleanupInput(obj v1alpha1.NetworkInputObject) map[string]any {
	input, err := networktask.LoadInput(obj)
	if err != nil || input == nil {
		return map[string]any{}
	}
	return input
}

// anyItem reports whether any item of a list in namespace matches.
func anyItem(ctx context.Context, c client.Reader, list client.ObjectList, namespace string, match func(client.Object) bool) (bool, error) {
	if err := c.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return false, err
	}
	items, err := meta.ExtractList(list)
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if match(item.(client.Object)) {
			return true, nil
		}
	}
	return false, nil
}

// enqueueWhere maps an event on trigger to the networking CRs (listed by
// newList in namespace) that match it.
func enqueueWhere(c client.Reader, namespace string, newList func() client.ObjectList, match func(obj, trigger client.Object) bool) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, trigger client.Object) []reconcile.Request {
		list := newList()
		if err := c.List(ctx, list, client.InNamespace(namespace)); err != nil {
			ctrllog.FromContext(ctx).Error(err, "Failed to list objects to enqueue")
			return nil
		}
		items, err := meta.ExtractList(list)
		if err != nil {
			return nil
		}
		var requests []reconcile.Request
		for _, item := range items {
			obj := item.(client.Object)
			if match(obj, trigger) {
				requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(obj)})
			}
		}
		return requests
	})
}

// triggerID is the API ID of the object that triggered an event.
func triggerID(trigger client.Object, kind string) string {
	return trigger.GetLabels()[networktask.IDLabel(kind)]
}

// emptyRules makes a missing rule direction [] in a SecurityGroup snapshot (contract §4.10).
func emptyRules(snapshot map[string]any, keys ...string) {
	spec, _ := snapshot["spec"].(map[string]any)
	if spec == nil {
		return
	}
	for _, k := range keys {
		if _, ok := spec[k]; !ok {
			spec[k] = []any{}
		}
	}
}
