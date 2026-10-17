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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/pkg/provisioning"

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
					feedbackStatusConditionsPath, feedbackStatusStatePath,
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
// The CR's osac.openshift.io/instance-type-name label names the type its spec was last reconciled
// toward; the stamp only moves once that label names the requested type, so a requested resize
// that the reconciler has not applied yet cannot be billed as running. The CR reports the resize
// complete when the instance is Running, the provisioning job that applies the new configuration
// has finished (ConfigurationApplied), and KubeVirt no longer reports a pending restart
// (RestartRequired). Until then the previous stamp is left untouched.
//
// The transition time is the CR's recorded observation of the running configuration changing
// (RunningConfigSince), not the time of this reconciliation: feedback can run long after the
// resize completed, and those hours belong to the type that was actually running.
func syncCIRunningInstanceType(obj *ckv1alpha1.ComputeInstance, remote *privatev1.ComputeInstance) {
	requested := remote.GetSpec().GetInstanceType()
	if requested == nil {
		return
	}
	if obj.Labels[osacComputeInstanceTypeNameLabel] != requested.GetName() {
		return
	}
	if sameInstanceTypeRef(remote.GetStatus().GetInstanceType(), requested) {
		return
	}
	if !ciRunningWithAppliedConfig(obj) {
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

// ciRunningWithAppliedConfig reports whether the CR confirms the instance is running with the
// configuration currently in its spec: the status was computed for that spec, the phase is
// Running, the configuration has been applied, and no restart is pending that would still be
// required to realize it.
//
// The desiredConfigVersion check is the freshness gate: status.desiredConfigVersion is the hash
// of the spec the CR controller last reconciled, and the conditions describe that spec. Right
// after the reconciler patches a resized spec, the stored version still names the old one, so
// the conditions cannot yet vouch for the new spec and the stamp waits.
func ciRunningWithAppliedConfig(obj *ckv1alpha1.ComputeInstance) bool {
	if obj.Status.Phase != ckv1alpha1.ComputeInstancePhaseRunning {
		return false
	}
	version, err := provisioning.ComputeDesiredConfigVersion(obj.Spec)
	if err != nil || version != obj.Status.DesiredConfigVersion {
		return false
	}
	// The recorded observation must be of this spec as well, so the time sent downstream is
	// the moment this configuration was observed running.
	if obj.Status.RunningConfigVersion != version {
		return false
	}
	applied := false
	for _, condition := range obj.Status.Conditions {
		switch condition.Type {
		case string(ckv1alpha1.ComputeInstanceConditionConfigurationApplied):
			// The condition must exist and be true: a missing condition vouches for nothing.
			applied = condition.Status == metav1.ConditionTrue
		case string(ckv1alpha1.ComputeInstanceConditionRestartRequired):
			if condition.Status == metav1.ConditionTrue {
				return false
			}
		}
	}
	return applied
}
