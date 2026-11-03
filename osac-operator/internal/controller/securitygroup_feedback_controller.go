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
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/feedback"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// SecurityGroupFeedbackReconciler sends updates to the fulfillment service.
type SecurityGroupFeedbackReconciler struct {
	bridge              *feedback.Bridge[*v1alpha1.SecurityGroup, *privatev1.SecurityGroup]
	networkingNamespace string
}

// NewSecurityGroupFeedbackReconciler creates a reconciler that sends to the fulfillment service updates about security groups.
func NewSecurityGroupFeedbackReconciler(hubClient clnt.Client, grpcConn grpc.ClientConnInterface, networkingNamespace string) *SecurityGroupFeedbackReconciler {
	sgClient := privatev1.NewSecurityGroupsClient(grpcConn)
	r := &SecurityGroupFeedbackReconciler{networkingNamespace: networkingNamespace}
	r.bridge = &feedback.Bridge[*v1alpha1.SecurityGroup, *privatev1.SecurityGroup]{
		Client:    hubClient,
		Finalizer: osacSecurityGroupFeedbackFinalizer,
		IDLabel:   osacSecurityGroupIDLabel,
		Kind:      "SecurityGroup",
		IDKey:     "securityGroupID",
		NewObject: func() *v1alpha1.SecurityGroup { return &v1alpha1.SecurityGroup{} },
		Fetch: func(ctx context.Context, id string) (*privatev1.SecurityGroup, error) {
			response, err := sgClient.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			sg := response.GetObject()
			if sg == nil {
				return nil, errors.New("security group response contained nil object")
			}
			if !sg.HasSpec() {
				sg.SetSpec(&privatev1.SecurityGroupSpec{})
			}
			if !sg.HasStatus() {
				sg.SetStatus(&privatev1.SecurityGroupStatus{})
			}
			return sg, nil
		},
		Save: func(ctx context.Context, remote *privatev1.SecurityGroup) error {
			// Lock: a concurrent rule Update makes this write fail, and the
			// feedback retries against the new record (status barrier).
			_, err := sgClient.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
				Object:     remote,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{feedbackStatusStatePath, feedbackStatusMessagePath}},
				Lock:       true,
			}.Build())
			if err != nil {
				return err
			}
			// Signal on change, so the fulfillment service acts on the new state.
			_, err = sgClient.Signal(ctx, privatev1.SecurityGroupsSignalRequest_builder{Id: remote.GetId()}.Build())
			return err
		},
		Signal: func(ctx context.Context, id string) error {
			_, err := sgClient.Signal(ctx, privatev1.SecurityGroupsSignalRequest_builder{
				Id: id,
			}.Build())
			return err
		},
		SyncUpdate: syncSecurityGroupUpdate,
		SyncDelete: syncSecurityGroupDelete,
	}
	return r
}

// SetupWithManager adds the reconciler to the controller manager.
func (r *SecurityGroupFeedbackReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return fmt.Errorf("local manager is nil")
	}

	return ctrl.NewControllerManagedBy(localMgr).
		Named("securitygroup-feedback").
		For(&v1alpha1.SecurityGroup{}, builder.WithPredicates(NetworkingNamespacePredicate(r.networkingNamespace))).
		Complete(r)
}

// Reconcile delegates to the shared feedback Bridge.
func (r *SecurityGroupFeedbackReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	return r.bridge.Reconcile(ctx, request)
}

func syncSecurityGroupUpdate(_ context.Context, obj *v1alpha1.SecurityGroup, remote *privatev1.SecurityGroup) error {
	if obj.DeletionTimestamp == nil && !securityGroupRecordRulesStored(obj, remote) {
		// The record holds a rule update that the operator has not stored
		// yet: keep the state the Update set (Unified design, SecurityGroup
		// status barrier: the stored input's rules against the record's).
		return nil
	}
	status := remote.GetStatus()
	state, message := networkFeedback(obj, obj.Status.Conditions)
	switch state {
	case networkFeedbackReady:
		status.SetState(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY)
	case networkFeedbackFailed:
		status.SetState(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED)
	case networkFeedbackDeleting:
		status.SetState(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_DELETING)
	default:
		status.SetState(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING)
	}
	feedbackMessage(message, status.SetMessage, status.ClearMessage)
	return nil
}

func syncSecurityGroupDelete(ctx context.Context, obj *v1alpha1.SecurityGroup, remote *privatev1.SecurityGroup) error {
	return syncSecurityGroupUpdate(ctx, obj, remote)
}

// securityGroupRecordRulesStored reports whether the stored input holds the
// rules of the fulfillment record. It is true when no input is stored.
func securityGroupRecordRulesStored(sg *v1alpha1.SecurityGroup, remote *privatev1.SecurityGroup) bool {
	ingress, ok := recordSecurityRules(remote.GetSpec().GetIngress(), true)
	if !ok {
		return false
	}
	egress, ok := recordSecurityRules(remote.GetSpec().GetEgress(), false)
	if !ok {
		return false
	}
	record := sg.DeepCopy()
	record.Spec.IngressRules = ingress
	record.Spec.EgressRules = egress
	return securityGroupRulesStored(record)
}

// recordSecurityRules converts record rules to CR rules, as the fulfillment
// controller does: the IPv4 CIDR is the source of an ingress rule and the
// destination of an egress rule.
func recordSecurityRules(rules []*privatev1.SecurityRule, ingress bool) ([]v1alpha1.SecurityRule, bool) {
	if len(rules) == 0 {
		return nil, true
	}
	result := make([]v1alpha1.SecurityRule, 0, len(rules))
	for _, rule := range rules {
		if rule == nil || rule.GetIpv4Cidr() == "" {
			return nil, false
		}
		r := v1alpha1.SecurityRule{Protocol: v1alpha1.SecurityGroupProtocol(recordProtocol(rule.GetProtocol()))}
		if rule.HasPortFrom() {
			r.PortFrom = ptr.To(rule.GetPortFrom())
		}
		if rule.HasPortTo() {
			r.PortTo = ptr.To(rule.GetPortTo())
		}
		if ingress {
			r.SourceCIDR = rule.GetIpv4Cidr()
		} else {
			r.DestinationCIDR = rule.GetIpv4Cidr()
		}
		result = append(result, r)
	}
	return result, true
}

func recordProtocol(p privatev1.Protocol) string {
	switch p {
	case privatev1.Protocol_PROTOCOL_TCP:
		return "tcp"
	case privatev1.Protocol_PROTOCOL_UDP:
		return "udp"
	case privatev1.Protocol_PROTOCOL_ICMP:
		return "icmp"
	case privatev1.Protocol_PROTOCOL_ALL:
		return "all"
	default:
		return strings.ToLower(p.String())
	}
}
