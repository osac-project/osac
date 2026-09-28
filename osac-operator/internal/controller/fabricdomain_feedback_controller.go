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

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/feedback"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// FabricDomainFeedbackReconciler synchronizes FabricDomain status with fulfillment-service.
type FabricDomainFeedbackReconciler struct {
	bridge              *feedback.Bridge[*v1alpha1.FabricDomain, *privatev1.FabricDomain]
	networkingNamespace string
}

// NewFabricDomainFeedbackReconciler creates the fulfillment-service feedback reconciler.
func NewFabricDomainFeedbackReconciler(hubClient clnt.Client, grpcConn *grpc.ClientConn, networkingNamespace string) *FabricDomainFeedbackReconciler {
	domainClient := privatev1.NewFabricDomainsClient(grpcConn)
	r := &FabricDomainFeedbackReconciler{networkingNamespace: networkingNamespace}
	r.bridge = &feedback.Bridge[*v1alpha1.FabricDomain, *privatev1.FabricDomain]{
		Client:    hubClient,
		Finalizer: osacFabricDomainFeedbackFinalizer,
		IDLabel:   osacFabricDomainIDLabel,
		Kind:      "FabricDomain",
		IDKey:     "fabricDomainID",
		NewObject: func() *v1alpha1.FabricDomain { return &v1alpha1.FabricDomain{} },
		Fetch: func(ctx context.Context, id string) (*privatev1.FabricDomain, error) {
			response, err := domainClient.Get(ctx, privatev1.FabricDomainsGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			domain := response.GetObject()
			if domain == nil {
				return nil, errors.New("FabricDomain response contained nil object")
			}
			if !domain.HasSpec() {
				domain.SetSpec(&privatev1.FabricDomainSpec{})
			}
			if !domain.HasStatus() {
				domain.SetStatus(&privatev1.FabricDomainStatus{})
			}
			return domain, nil
		},
		Save: func(ctx context.Context, remote *privatev1.FabricDomain) error {
			_, err := domainClient.Update(ctx, privatev1.FabricDomainsUpdateRequest_builder{
				Object: remote,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
					feedbackStatusConditionsPath, "status.backend_id", "status.vpc_id", "status.members",
				}},
			}.Build())
			return err
		},
		Signal: func(ctx context.Context, id string) error {
			_, err := domainClient.Signal(ctx, privatev1.FabricDomainsSignalRequest_builder{Id: id}.Build())
			return err
		},
		SyncUpdate: syncFabricDomainUpdate,
		SyncDelete: syncFabricDomainDelete,
	}
	return r
}

// SetupWithManager registers the feedback controller.
func (r *FabricDomainFeedbackReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return fmt.Errorf("local manager is nil")
	}
	return ctrl.NewControllerManagedBy(localMgr).
		Named("fabricdomain-feedback").
		For(&v1alpha1.FabricDomain{}, builder.WithPredicates(NetworkingNamespacePredicate(r.networkingNamespace))).
		Complete(r)
}

// Reconcile delegates to the shared feedback bridge.
func (r *FabricDomainFeedbackReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	return r.bridge.Reconcile(ctx, request)
}

func syncFabricDomainUpdate(_ context.Context, obj *v1alpha1.FabricDomain, remote *privatev1.FabricDomain) error {
	status := remote.GetStatus()
	status.SetBackendId(obj.Status.BackendID)
	status.SetVpcId(obj.Status.VPCID)

	conditionType, conditionStatus := fabricDomainConditionForPhase(obj.Status.Phase)

	var reason, message string
	var lastTransitionTime metav1.Time
	if condition := apimeta.FindStatusCondition(obj.Status.Conditions, v1alpha1.ConditionReady); condition != nil {
		reason = condition.Reason
		message = condition.Message
		lastTransitionTime = condition.LastTransitionTime
	}
	if lastTransitionTime.IsZero() {
		if condition := matchingRemoteFabricDomainCondition(status.GetConditions(), conditionType, conditionStatus, "", false); condition != nil {
			if transitionTime := condition.GetLastTransitionTime(); transitionTime != nil {
				lastTransitionTime = metav1.NewTime(transitionTime.AsTime())
			}
		}
	}
	if lastTransitionTime.IsZero() {
		lastTransitionTime = metav1.Now()
	}
	status.SetConditions([]*privatev1.FabricDomainCondition{privatev1.FabricDomainCondition_builder{
		Type:               conditionType,
		Status:             conditionStatus,
		LastTransitionTime: timestamppb.New(lastTransitionTime.Time),
		Reason:             ptr.To(reason),
		Message:            ptr.To(message),
	}.Build()})

	members := make([]*privatev1.FabricDomainMemberStatus, len(obj.Status.Members))
	for i, member := range obj.Status.Members {
		state := privatev1.FabricDomainMemberState_FABRIC_DOMAIN_MEMBER_STATE_UNSPECIFIED
		switch member.State {
		case v1alpha1.FabricDomainMemberStatePending:
			state = privatev1.FabricDomainMemberState_FABRIC_DOMAIN_MEMBER_STATE_PENDING
		case v1alpha1.FabricDomainMemberStateActive:
			state = privatev1.FabricDomainMemberState_FABRIC_DOMAIN_MEMBER_STATE_ACTIVE
		case v1alpha1.FabricDomainMemberStateFailed:
			state = privatev1.FabricDomainMemberState_FABRIC_DOMAIN_MEMBER_STATE_FAILED
		}
		members[i] = privatev1.FabricDomainMemberStatus_builder{
			Server: member.Server, State: state, Message: member.Message,
		}.Build()
	}
	status.SetMembers(members)
	return nil
}

func syncFabricDomainDelete(ctx context.Context, obj *v1alpha1.FabricDomain, remote *privatev1.FabricDomain) error {
	if obj.Status.Phase == v1alpha1.FabricDomainPhaseFailed {
		return syncFabricDomainUpdate(ctx, obj, remote)
	}
	status := remote.GetStatus()
	conditionType := privatev1.FabricDomainConditionType_FABRIC_DOMAIN_CONDITION_TYPE_PROGRESSING
	conditionStatus := privatev1.ConditionStatus_CONDITION_STATUS_TRUE
	const reason = "Deleting"
	lastTransitionTime := timestamppb.Now()
	if condition := matchingRemoteFabricDomainCondition(status.GetConditions(), conditionType, conditionStatus, reason, true); condition != nil {
		if transitionTime := condition.GetLastTransitionTime(); transitionTime != nil {
			lastTransitionTime = transitionTime
		}
	}
	status.SetConditions([]*privatev1.FabricDomainCondition{privatev1.FabricDomainCondition_builder{
		Type:               conditionType,
		Status:             conditionStatus,
		LastTransitionTime: lastTransitionTime,
		Reason:             ptr.To(reason),
		Message:            ptr.To("FabricDomain cleanup is in progress"),
	}.Build()})
	return nil
}

func fabricDomainConditionForPhase(phase v1alpha1.FabricDomainPhase) (privatev1.FabricDomainConditionType, privatev1.ConditionStatus) {
	switch phase {
	case v1alpha1.FabricDomainPhaseReady:
		return privatev1.FabricDomainConditionType_FABRIC_DOMAIN_CONDITION_TYPE_READY,
			privatev1.ConditionStatus_CONDITION_STATUS_TRUE
	case v1alpha1.FabricDomainPhaseProgressing, v1alpha1.FabricDomainPhaseDeleting:
		return privatev1.FabricDomainConditionType_FABRIC_DOMAIN_CONDITION_TYPE_PROGRESSING,
			privatev1.ConditionStatus_CONDITION_STATUS_TRUE
	case v1alpha1.FabricDomainPhaseFailed:
		return privatev1.FabricDomainConditionType_FABRIC_DOMAIN_CONDITION_TYPE_FAILED,
			privatev1.ConditionStatus_CONDITION_STATUS_TRUE
	default:
		return privatev1.FabricDomainConditionType_FABRIC_DOMAIN_CONDITION_TYPE_UNSPECIFIED,
			privatev1.ConditionStatus_CONDITION_STATUS_UNSPECIFIED
	}
}

func matchingRemoteFabricDomainCondition(
	conditions []*privatev1.FabricDomainCondition,
	conditionType privatev1.FabricDomainConditionType,
	conditionStatus privatev1.ConditionStatus,
	reason string,
	matchReason bool,
) *privatev1.FabricDomainCondition {
	for _, condition := range conditions {
		if condition.GetType() != conditionType || condition.GetStatus() != conditionStatus {
			continue
		}
		if matchReason && condition.GetReason() != reason {
			continue
		}
		return condition
	}
	return nil
}
