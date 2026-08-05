/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	ckv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/internal/controller/feedback"
)

// ComputeInstanceFeedbackReconciler sends updates to the fulfillment service.
type ComputeInstanceFeedbackReconciler struct {
	bridge                   *feedback.Bridge[*ckv1alpha1.ComputeInstance, *privatev1.ComputeInstance]
	computeInstanceNamespace string
}

// NewComputeInstanceFeedbackReconciler creates a reconciler that sends to the fulfillment service updates about compute instances.
func NewComputeInstanceFeedbackReconciler(hubClient clnt.Client, grpcConn *grpc.ClientConn, computeInstanceNamespace string) *ComputeInstanceFeedbackReconciler {
	return &ComputeInstanceFeedbackReconciler{
		bridge:                   newComputeInstanceFeedbackBridge(hubClient, privatev1.NewComputeInstancesClient(grpcConn)),
		computeInstanceNamespace: computeInstanceNamespace,
	}
}

// SetupWithManager adds the reconciler to the controller manager.
func (r *ComputeInstanceFeedbackReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return fmt.Errorf("local manager is nil")
	}

	return ctrl.NewControllerManagedBy(localMgr).
		Named("computeinstance-feedback").
		For(&ckv1alpha1.ComputeInstance{}, builder.WithPredicates(ComputeInstanceNamespacePredicate(r.computeInstanceNamespace))).
		Complete(r)
}

// Reconcile delegates to the shared feedback Bridge.
func (r *ComputeInstanceFeedbackReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	return r.bridge.Reconcile(ctx, request)
}

// newComputeInstanceFeedbackBridge creates a Bridge wired to the given client. Exported for testing.
func newComputeInstanceFeedbackBridge(hubClient clnt.Client, ciClient privatev1.ComputeInstancesClient) *feedback.Bridge[*ckv1alpha1.ComputeInstance, *privatev1.ComputeInstance] {
	return &feedback.Bridge[*ckv1alpha1.ComputeInstance, *privatev1.ComputeInstance]{
		Client:    hubClient,
		Finalizer: osacComputeInstanceFeedbackFinalizer,
		IDLabel:   osacComputeInstanceIDLabel,
		Kind:      "ComputeInstance",
		IDKey:     "ciID",
		NewObject: func() *ckv1alpha1.ComputeInstance { return &ckv1alpha1.ComputeInstance{} },
		Fetch: func(ctx context.Context, id string) (*privatev1.ComputeInstance, error) {
			response, err := ciClient.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			ci := response.GetObject()
			if ci == nil {
				return nil, errors.New("compute instance response contained nil object")
			}
			if !ci.HasSpec() {
				ci.SetSpec(&privatev1.ComputeInstanceSpec{})
			}
			if !ci.HasStatus() {
				ci.SetStatus(&privatev1.ComputeInstanceStatus{})
			}
			return ci, nil
		},
		Save: func(ctx context.Context, remote *privatev1.ComputeInstance) error {
			_, err := ciClient.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
				Object: remote,
			}.Build())
			return err
		},
		Signal: func(ctx context.Context, id string) error {
			_, err := ciClient.Signal(ctx, privatev1.ComputeInstancesSignalRequest_builder{
				Id: id,
			}.Build())
			return err
		},
		SyncUpdate: syncComputeInstanceUpdate,
		SyncDelete: syncComputeInstanceDelete,
	}
}

func syncComputeInstanceUpdate(ctx context.Context, obj *ckv1alpha1.ComputeInstance, remote *privatev1.ComputeInstance) error {
	syncCIConditions(obj, remote)
	syncCIPhase(ctx, obj, remote)
	syncCIIPAddresses(obj, remote)
	syncCILastRestartedAt(obj, remote)
	return nil
}

func syncComputeInstanceDelete(ctx context.Context, obj *ckv1alpha1.ComputeInstance, remote *privatev1.ComputeInstance) error {
	return syncComputeInstanceUpdate(ctx, obj, remote)
}

func syncCIConditions(obj *ckv1alpha1.ComputeInstance, remote *privatev1.ComputeInstance) {
	conditionMappings := []struct {
		crType ckv1alpha1.ComputeInstanceConditionType
		vmType privatev1.ComputeInstanceConditionType
	}{
		{ckv1alpha1.ComputeInstanceConditionConfigurationApplied, privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_CONFIGURATION_APPLIED},
		{ckv1alpha1.ComputeInstanceConditionReady, privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_READY},
		{ckv1alpha1.ComputeInstanceConditionRestartInProgress, privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_RESTART_IN_PROGRESS},
		{ckv1alpha1.ComputeInstanceConditionRestartFailed, privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_RESTART_FAILED},
		{ckv1alpha1.ComputeInstanceConditionProvisioned, privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED},
		{ckv1alpha1.ComputeInstanceConditionRestartRequired, privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_RESTART_REQUIRED},
	}
	for _, m := range conditionMappings {
		crCondition := obj.GetStatusCondition(m.crType)
		if crCondition == nil {
			continue
		}
		syncCIConditionFromCR(remote, m.vmType, crCondition)
	}
}

func syncCIConditionFromCR(remote *privatev1.ComputeInstance, vmConditionType privatev1.ComputeInstanceConditionType, crCondition *metav1.Condition) {
	vmCondition := findComputeInstanceCondition(remote, vmConditionType)
	oldStatus := vmCondition.GetStatus()
	newStatus := mapCIConditionStatus(crCondition.Status)
	vmCondition.SetStatus(newStatus)
	vmCondition.SetReason(crCondition.Reason)
	vmCondition.SetMessage(crCondition.Message)
	if newStatus != oldStatus {
		vmCondition.SetLastTransitionTime(timestamppb.Now())
	}
}

func mapCIConditionStatus(status metav1.ConditionStatus) privatev1.ConditionStatus {
	switch status {
	case metav1.ConditionFalse:
		return privatev1.ConditionStatus_CONDITION_STATUS_FALSE
	case metav1.ConditionTrue:
		return privatev1.ConditionStatus_CONDITION_STATUS_TRUE
	default:
		return privatev1.ConditionStatus_CONDITION_STATUS_UNSPECIFIED
	}
}

func syncCIPhase(ctx context.Context, obj *ckv1alpha1.ComputeInstance, remote *privatev1.ComputeInstance) {
	switch obj.Status.Phase {
	case ckv1alpha1.ComputeInstancePhaseStarting:
		remote.GetStatus().SetState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING)
	case ckv1alpha1.ComputeInstancePhaseFailed:
		remote.GetStatus().SetState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED)
	case ckv1alpha1.ComputeInstancePhaseRunning:
		remote.GetStatus().SetState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
	case ckv1alpha1.ComputeInstancePhaseDeleting:
		remote.GetStatus().SetState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING)
	case ckv1alpha1.ComputeInstancePhaseStopping:
		remote.GetStatus().SetState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING)
	case ckv1alpha1.ComputeInstancePhaseStopped:
		remote.GetStatus().SetState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED)
	case ckv1alpha1.ComputeInstancePhasePaused:
		remote.GetStatus().SetState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED)
	default:
		log := ctrllog.FromContext(ctx)
		log.Info("Unknown phase, will ignore it", "phase", obj.Status.Phase)
	}
}

func findComputeInstanceCondition(remote *privatev1.ComputeInstance, kind privatev1.ComputeInstanceConditionType) *privatev1.ComputeInstanceCondition {
	for _, current := range remote.Status.Conditions {
		if current.Type == kind {
			return current
		}
	}
	condition := &privatev1.ComputeInstanceCondition{
		Type:   kind,
		Status: privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
	}
	remote.Status.Conditions = append(remote.Status.Conditions, condition)
	return condition
}

func syncCIIPAddresses(obj *ckv1alpha1.ComputeInstance, remote *privatev1.ComputeInstance) {
	remote.GetStatus().SetExternalIpAddress(obj.Status.ExternalIPAddress)
	remote.GetStatus().SetInternalIpAddress(obj.Status.IPAddress)
}

func syncCILastRestartedAt(obj *ckv1alpha1.ComputeInstance, remote *privatev1.ComputeInstance) {
	if obj.Status.LastRestartedAt != nil {
		remote.GetStatus().SetLastRestartedAt(timestamppb.New(obj.Status.LastRestartedAt.Time))
	}
}
