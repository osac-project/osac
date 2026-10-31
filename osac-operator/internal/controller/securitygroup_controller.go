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
	"reflect"
	"slices"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
)

// msgSecurityGroupUpdating is the Ready message of a READY SecurityGroup whose rule
// update has not started its first job yet (Unified design, Status).
const msgSecurityGroupUpdating = "Updating"

// SecurityGroupReconciler runs the SecurityGroup tasks (contract §4.10):
// create_security_group on the Fabric, then the Kubernetes Manager, again as
// the next attempt on each rule update; delete in reverse order once no
// NetworkAttachment selects the group.
type SecurityGroupReconciler struct {
	Client              client.Client
	Engine              *networktask.Engine
	NetworkingNamespace string
}

// Reconcile advances the create, rule update or delete of a SecurityGroup.
func (r *SecurityGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileNetworkResource(ctx, r.Client, r.Engine, req.NamespacedName, networkingHandlers[*v1alpha1.SecurityGroup]{
		kind:      kindSecurityGroup,
		finalizer: osacSecurityGroupFinalizer,
		newObject: func() *v1alpha1.SecurityGroup { return &v1alpha1.SecurityGroup{} },
		status:    func(o *v1alpha1.SecurityGroup) any { return &o.Status },
		create:    r.create,
		remove:    r.remove,
	})
}

// securityGroupResource is the SecurityGroup snapshot: spec.ingressRules and
// spec.egressRules, a direction without rules as [].
func securityGroupResource(sg *v1alpha1.SecurityGroup) (map[string]any, error) {
	resource, err := networktask.Snapshot(sg, kindSecurityGroup, sg.Spec, "ingressRules", "egressRules")
	if err != nil {
		return nil, err
	}
	emptyRules(resource, "ingressRules", "egressRules")
	return resource, nil
}

// securityGroupRulesStored reports whether the stored input holds the rules
// of the spec. It is true when no input is stored.
func securityGroupRulesStored(sg *v1alpha1.SecurityGroup) bool {
	input, err := networktask.LoadInput(sg)
	if err != nil || input == nil {
		return true
	}
	resource, err := securityGroupResource(sg)
	if err != nil {
		return true
	}
	stored, _ := input["resource"].(map[string]any)
	return stored != nil && reflect.DeepEqual(stored["spec"], resource["spec"])
}

func (r *SecurityGroupReconciler) create(ctx context.Context, sg *v1alpha1.SecurityGroup) (ctrl.Result, error) {
	var vn *v1alpha1.VirtualNetwork
	if sg.Status.TaskInput == nil {
		var err error
		vn, err = findVirtualNetwork(ctx, r.Client, sg.Namespace, sg.Spec.VirtualNetwork)
		if err != nil {
			return ctrl.Result{}, err
		}
		if vn == nil || !isReady(vn) {
			networktask.SetProgressing(sg, msgWaitingForParent(kindVirtualNetwork, sg.Spec.VirtualNetwork))
			return waitResult(), nil
		}
		copyManagers(sg, vn.Status.NetworkManagers, false)
		sg.Status.NetworkType = vn.Status.NetworkType
	} else if !securityGroupRulesStored(sg) {
		if res, handled, err := r.update(ctx, sg); handled {
			return res, err
		}
	}
	input, store, err := inputOf(sg, func() (map[string]any, error) {
		resource, err := securityGroupResource(sg)
		if err != nil {
			return nil, err
		}
		vnSnapshot, err := parentSnapshot(vn, kindVirtualNetwork, vn.Spec, "ipv4Cidr", nil)
		if err != nil {
			return nil, err
		}
		input := networktask.NewInput(sg, kindSecurityGroup, resource)
		input["virtual_network"] = vnSnapshot
		return input, nil
	}, nil)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !r.Engine.ProvisioningEnabled() {
		networktask.SetProvisioningDisabled(sg, networktask.CreateSecurityGroup)
		return ctrl.Result{}, store()
	}
	ready := meta.FindStatusCondition(sg.Status.Conditions, v1alpha1.NetworkConditionReady)
	updating := ready != nil && ready.Reason == v1alpha1.ReasonUpdating
	out, done, err := runTasks(ctx, r.Engine, sg,
		func() (networktask.Request, bool) {
			req := taskRequest(sg, kindSecurityGroup, networktask.CreateSecurityGroup, v1alpha1.NetworkJobManagerFabric, input)
			req.BeforeLaunch = store
			return req, true
		},
		func() (networktask.Request, bool) {
			return taskRequest(sg, kindSecurityGroup, networktask.CreateSecurityGroup, v1alpha1.NetworkJobManagerKubernetes, input), hasKubernetes(sg)
		},
	)
	if err != nil {
		return ctrl.Result{}, err
	}
	switch {
	case done:
		networktask.SetSucceeded(sg)
		return ctrl.Result{}, nil
	case out.Failed():
		// A final failure of the Fabric run means no Kubernetes run of this input.
		networktask.Cancel(sg, networktask.CreateSecurityGroup, v1alpha1.NetworkJobManagerKubernetes)
		networktask.SetFailed(sg, out.Entry.Message)
		return ctrl.Result{}, nil
	case updating:
		networktask.SetReady(sg, metav1.ConditionTrue, v1alpha1.ReasonUpdating, updatingMessage(out))
	default:
		reportOutcome(sg, out)
	}
	return outcomeResult(out), nil
}

func updatingMessage(out networktask.Outcome) string {
	if message := networktask.ProgressMessage(out.Entry); message != "" && message != networktask.MessageRerun {
		return message
	}
	return msgSecurityGroupUpdating
}

// update stores the new rules as the input of the next attempt (Unified
// design, SecurityGroup rule updates). With provider networking enabled it
// marks each recorded manager's create for a rerun and sets the reason
// (Updating for a Ready group, else Progressing) in the same status write.
// It does nothing while a job of the group runs. handled reports that the
// reconcile must return.
func (r *SecurityGroupReconciler) update(ctx context.Context, sg *v1alpha1.SecurityGroup) (ctrl.Result, bool, error) {
	if slices.ContainsFunc(sg.Status.Jobs, func(j v1alpha1.NetworkJobStatus) bool {
		return j.State == v1alpha1.NetworkJobStateRunning
	}) {
		ctrllog.FromContext(ctx).Info("SecurityGroup rules changed while a job runs; waiting for it to end")
		return ctrl.Result{}, false, nil
	}
	input, err := networktask.LoadInput(sg)
	if err != nil {
		return ctrl.Result{}, true, err
	}
	resource, err := securityGroupResource(sg)
	if err != nil {
		return ctrl.Result{}, true, err
	}
	input["resource"] = resource
	if err := networktask.StoreInput(sg, input); err != nil {
		return ctrl.Result{}, true, err
	}
	if !r.Engine.ProvisioningEnabled() {
		// The input is stored; the reason stays ProvisioningDisabled.
		return ctrl.Result{}, false, nil
	}
	for _, manager := range []v1alpha1.NetworkJobManager{v1alpha1.NetworkJobManagerFabric, v1alpha1.NetworkJobManagerKubernetes} {
		r.Engine.MarkForRerun(sg, networktask.CreateSecurityGroup, manager)
	}
	if meta.IsStatusConditionTrue(sg.Status.Conditions, v1alpha1.NetworkConditionReady) {
		networktask.SetReady(sg, metav1.ConditionTrue, v1alpha1.ReasonUpdating, msgSecurityGroupUpdating)
	} else {
		networktask.SetProgressing(sg, networktask.MessageRerun)
	}
	ctrllog.FromContext(ctx).Info("SecurityGroup rules changed; running create_security_group with the new rules")
	if err := r.Client.Status().Update(ctx, sg); err != nil {
		return ctrl.Result{}, true, err
	}
	return ctrl.Result{}, false, nil
}

func (r *SecurityGroupReconciler) remove(ctx context.Context, sg *v1alpha1.SecurityGroup) (bool, ctrl.Result, error) {
	if id := sg.Labels[osacSecurityGroupIDLabel]; id != "" {
		used, err := anyItem(ctx, r.Client, &v1alpha1.NetworkAttachmentList{}, sg.Namespace, func(o client.Object) bool {
			return slices.Contains(o.(*v1alpha1.NetworkAttachment).Spec.SecurityGroups, id)
		})
		if err != nil || used {
			return false, waitResult(), err
		}
	}
	input := cleanupInput(sg)
	return runCleanup(ctx, r.Engine, sg, kindSecurityGroup,
		cleanupTask{create: networktask.CreateSecurityGroup, manager: v1alpha1.NetworkJobManagerKubernetes,
			request: func() networktask.Request {
				return taskRequest(sg, kindSecurityGroup, networktask.DeleteSecurityGroup, v1alpha1.NetworkJobManagerKubernetes, input)
			}},
		cleanupTask{create: networktask.CreateSecurityGroup, manager: v1alpha1.NetworkJobManagerFabric,
			request: func() networktask.Request {
				return taskRequest(sg, kindSecurityGroup, networktask.DeleteSecurityGroup, v1alpha1.NetworkJobManagerFabric, input)
			}},
	)
}

// SetupWithManager registers the controller. VirtualNetwork events re-queue
// its SecurityGroups; NetworkAttachment events re-queue the groups they select.
func (r *SecurityGroupReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return errors.New("local manager is nil")
	}
	ns := r.NetworkingNamespace
	inNamespace := builder.WithPredicates(NetworkingNamespacePredicate(ns))
	sgList := func() client.ObjectList { return &v1alpha1.SecurityGroupList{} }
	return ctrl.NewControllerManagedBy(localMgr).
		Named("securitygroup").
		For(&v1alpha1.SecurityGroup{}, inNamespace).
		Watches(&v1alpha1.VirtualNetwork{}, enqueueWhere(r.Client, ns, sgList, func(obj, trigger client.Object) bool {
			return obj.(*v1alpha1.SecurityGroup).Spec.VirtualNetwork == triggerID(trigger, kindVirtualNetwork)
		}), inNamespace).
		Watches(&v1alpha1.NetworkAttachment{}, enqueueWhere(r.Client, ns, sgList, func(obj, trigger client.Object) bool {
			return slices.Contains(trigger.(*v1alpha1.NetworkAttachment).Spec.SecurityGroups, triggerID(obj, kindSecurityGroup))
		}), inNamespace).
		Complete(r)
}
