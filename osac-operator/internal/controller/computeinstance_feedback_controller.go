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
	"time"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	ckv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/feedback"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// ComputeInstanceFeedbackReconciler sends updates to the fulfillment service.
type ComputeInstanceFeedbackReconciler struct {
	bridge                   *feedback.Bridge[*ckv1alpha1.ComputeInstance, *privatev1.ComputeInstance]
	computeInstanceNamespace string
}

// NewComputeInstanceFeedbackReconciler creates a reconciler that sends to the fulfillment service updates about compute instances.
func NewComputeInstanceFeedbackReconciler(hubClient clnt.Client, grpcConn grpc.ClientConnInterface, computeInstanceNamespace string) *ComputeInstanceFeedbackReconciler {
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
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
					feedbackStatusConditionsPath, feedbackStatusStatePath, feedbackStatusStateTransitionTimePath,
					"status.external_ip_address", "status.internal_ip_address",
					"status.compute_network_attachment_statuses", "status.last_restarted_at",
					"status.instance_type", "status.instance_type_transition_time",
				}},
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
	syncCIRunningInstanceType(obj, remote)
	return nil
}

func syncComputeInstanceDelete(ctx context.Context, obj *ckv1alpha1.ComputeInstance, remote *privatev1.ComputeInstance) error {
	if err := syncComputeInstanceUpdate(ctx, obj, remote); err != nil {
		return err
	}
	// The CR may not have reached Phase=Deleting yet when feedback observes the deletion.
	// Force the DELETING state so the billing stops and the transition is recorded, dated
	// at the deletion rather than at the stale phase's transition time.
	if remote.GetStatus().GetState() != privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING {
		remote.GetStatus().SetState(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING)
		remote.GetStatus().SetStateTransitionTime(
			copyPhaseTransitionTime(ctrllog.FromContext(ctx), computeDeletionTransitionTime(obj), remote.GetStatus().GetStateTransitionTime()))
	}
	return nil
}

// computeDeletionTransitionTime returns the time to date a forced DELETING transition at: the CR's
// deletion timestamp when present, else the observation time.
func computeDeletionTransitionTime(obj *ckv1alpha1.ComputeInstance) *metav1.Time {
	if !obj.GetDeletionTimestamp().IsZero() {
		return &metav1.Time{Time: obj.GetDeletionTimestamp().Time}
	}
	now := metav1.Now()
	return &now
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
	vmCondition.SetMessage(sanitizeFeedbackText(crCondition.Message))
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

// syncCIPhase maps the CR phase to the fulfillment state. The transition time is the CR's
// recorded phase transition time (PhaseTransitionTime), not the moment feedback observed the
// change: feedback can run long after the phase moved, and those hours belong to the state that
// was actually current. The metering source contract rejects a state change whose
// status.state_transition_time is not later than the stored one, so a CR stamp that is not
// later than the stored time is bumped by one microsecond (see copyPhaseTransitionTime). An
// unchanged state keeps the stored time. A CR that predates PhaseTransitionTime carries none;
// feedback then falls back to its own observation time so the change is still stamped later
// than the stored one.
func syncCIPhase(ctx context.Context, obj *ckv1alpha1.ComputeInstance, remote *privatev1.ComputeInstance) {
	var state privatev1.ComputeInstanceState
	switch obj.Status.Phase {
	case ckv1alpha1.ComputeInstancePhaseStarting:
		state = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING
	case ckv1alpha1.ComputeInstancePhaseFailed:
		state = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED
	case ckv1alpha1.ComputeInstancePhaseRunning:
		state = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING
	case ckv1alpha1.ComputeInstancePhaseDeleting:
		state = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING
	case ckv1alpha1.ComputeInstancePhaseStopping:
		state = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING
	case ckv1alpha1.ComputeInstancePhaseStopped:
		state = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED
	case ckv1alpha1.ComputeInstancePhasePaused:
		state = privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED
	default:
		log := ctrllog.FromContext(ctx)
		log.Info("Unknown phase, will ignore it", "phase", obj.Status.Phase)
		return
	}
	if remote.GetStatus().GetState() != state {
		remote.GetStatus().SetStateTransitionTime(copyPhaseTransitionTime(
			ctrllog.FromContext(ctx), obj.Status.PhaseTransitionTime, remote.GetStatus().GetStateTransitionTime()))
	}
	remote.GetStatus().SetState(state)
}

// phaseTransitionTime returns the CR's recorded phase transition time, falling back to the
// current time when the CR carries none (created before the field existed and not yet
// re-stamped by a phase change).
func phaseTransitionTime(since *metav1.Time) *timestamppb.Timestamp {
	if since == nil || since.IsZero() {
		return timestamppb.Now()
	}
	return timestamppb.New(since.Time)
}

// copyPhaseTransitionTime copies the CR's recorded phase transition time into the fulfillment
// status. The CR stores whole seconds, so two phase changes within one second carry the same
// stamp, and the metering source contract rejects a changed value whose time is not later than
// the stored one: the write would fail and retry forever, because the CR stamp never moves.
// When the CR stamp is not later than the stored time, the copied time becomes the stored time
// plus one microsecond instead. A CR stamp earlier than the stored one should not happen (the
// controller stamps monotonically); the same bump guards it without backdating. The distortion
// is bounded by one second, unlike the backdating the contract rejects.
func copyPhaseTransitionTime(log logr.Logger, since *metav1.Time, stored *timestamppb.Timestamp) *timestamppb.Timestamp {
	copied := phaseTransitionTime(since)
	if stored == nil || copied.AsTime().After(stored.AsTime()) {
		return copied
	}
	log.Info("phase transition time is not later than the stored one, bumping it by one microsecond",
		"phaseTransitionTime", copied.AsTime(), "storedTransitionTime", stored.AsTime())
	return timestamppb.New(stored.AsTime().Add(time.Microsecond))
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
	statuses := make([]*privatev1.ComputeNetworkAttachmentStatus, 0, len(obj.Status.ComputeNetworkAttachmentStatuses))
	for _, status := range obj.Status.ComputeNetworkAttachmentStatuses {
		statuses = append(statuses, privatev1.ComputeNetworkAttachmentStatus_builder{
			SubnetRef: status.SubnetRef,
			IpAddress: status.IPAddress,
		}.Build())
	}
	remote.GetStatus().SetComputeNetworkAttachmentStatuses(statuses)
}

func syncCILastRestartedAt(obj *ckv1alpha1.ComputeInstance, remote *privatev1.ComputeInstance) {
	if obj.Status.LastRestartedAt != nil {
		remote.GetStatus().SetLastRestartedAt(timestamppb.New(obj.Status.LastRestartedAt.Time))
	}
}

// syncCIRunningInstanceType records the instance type that is actually running, with the time it
// became running, so metering can bill the VM against it. spec.instance_type is only the request:
// it changes as soon as a resize is requested, while the VM keeps running the old type until any
// restart the resize requires has completed.
//
// The gate is crConfirmsRunningType, the same predicate recordRunningConfig establishes: the
// instance-type label names the requested type, the recorded running-config observation names
// it too, and the RestartRequired condition is explicitly false. Until all three hold, the
// previous stamp is left untouched.
//
// The transition time is the CR's recorded observation of the running configuration changing
// (RunningConfigSince), not the time of this reconciliation: feedback can run long after the
// resize completed, and those hours belong to the type that was actually running.
func syncCIRunningInstanceType(obj *ckv1alpha1.ComputeInstance, remote *privatev1.ComputeInstance) {
	requested := remote.GetSpec().GetInstanceType()
	if requested == nil {
		return
	}
	if sameInstanceTypeRef(remote.GetStatus().GetInstanceType(), requested) {
		return
	}
	if !crConfirmsRunningType(obj, requested.GetName()) {
		return
	}
	since := obj.Status.RunningConfigSince
	if since == nil || since.IsZero() {
		return
	}
	remote.GetStatus().SetInstanceType(proto.Clone(requested).(*privatev1.InstanceTypeReference))
	remote.GetStatus().SetInstanceTypeTransitionTime(timestamppb.New(since.Time))
}

// sameInstanceTypeRef reports whether two references point to the same instance type. Identity
// is the id, falling back to the name when either side carries none; this mirrors the
// reference-key convention used across the fulfillment service (RefKeyStr, refKey), which the
// operator cannot import. Project and shared are resolution hints, not identity: the same type
// can be referenced with different hints, and treating those as a different type would re-stamp
// the billing transition time for a resize that never happened.
func sameInstanceTypeRef(a, b *privatev1.InstanceTypeReference) bool {
	if a == nil || b == nil {
		return false
	}
	if a.GetId() != "" && b.GetId() != "" {
		return a.GetId() == b.GetId()
	}
	return a.GetName() != "" && a.GetName() == b.GetName()
}
