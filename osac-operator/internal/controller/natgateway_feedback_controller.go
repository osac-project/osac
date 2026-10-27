/*
Copyright (c) 2026 Red Hat Inc.

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
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/feedback"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// NATGatewayFeedbackReconciler sends updates to the fulfillment service.
type NATGatewayFeedbackReconciler struct {
	bridge              *feedback.Bridge[*v1alpha1.NATGateway, *privatev1.NATGateway]
	networkingNamespace string
}

// NewNATGatewayFeedbackReconciler creates a reconciler that sends to the fulfillment service
// updates about NAT gateways.
func NewNATGatewayFeedbackReconciler(hubClient clnt.Client, grpcConn grpc.ClientConnInterface, networkingNamespace string) *NATGatewayFeedbackReconciler {
	ngClient := privatev1.NewNATGatewaysClient(grpcConn)
	r := &NATGatewayFeedbackReconciler{networkingNamespace: networkingNamespace}
	r.bridge = &feedback.Bridge[*v1alpha1.NATGateway, *privatev1.NATGateway]{
		Client:    hubClient,
		Finalizer: osacNATGatewayFeedbackFinalizer,
		IDLabel:   osacNATGatewayIDLabel,
		Kind:      "NATGateway",
		IDKey:     "natGatewayID",
		NewObject: func() *v1alpha1.NATGateway { return &v1alpha1.NATGateway{} },
		Fetch: func(ctx context.Context, id string) (*privatev1.NATGateway, error) {
			response, err := ngClient.Get(ctx, privatev1.NATGatewaysGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			ng := response.GetObject()
			if ng == nil {
				return nil, errors.New("NAT gateway response contained nil object")
			}
			if !ng.HasSpec() {
				ng.SetSpec(&privatev1.NATGatewaySpec{})
			}
			if !ng.HasStatus() {
				ng.SetStatus(&privatev1.NATGatewayStatus{})
			}
			return ng, nil
		},
		Save: func(ctx context.Context, remote *privatev1.NATGateway) error {
			_, err := ngClient.Update(ctx, privatev1.NATGatewaysUpdateRequest_builder{
				Object: remote,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
					feedbackStatusStatePath, feedbackStatusMessagePath, "status.hub", feedbackStatusStateTransitionTimePath,
				}},
			}.Build())
			return err
		},
		Signal: func(ctx context.Context, id string) error {
			_, err := ngClient.Signal(ctx, privatev1.NATGatewaysSignalRequest_builder{
				Id: id,
			}.Build())
			return err
		},
		SyncUpdate: syncNATGatewayUpdate,
		SyncDelete: syncNATGatewayDelete,
	}
	return r
}

// SetupWithManager adds the reconciler to the controller manager.
func (r *NATGatewayFeedbackReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return fmt.Errorf("local manager is nil")
	}

	return ctrl.NewControllerManagedBy(localMgr).
		Named("natgateway-feedback").
		For(&v1alpha1.NATGateway{}, builder.WithPredicates(NetworkingNamespacePredicate(r.networkingNamespace))).
		Complete(r)
}

// Reconcile delegates to the shared feedback Bridge.
func (r *NATGatewayFeedbackReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	return r.bridge.Reconcile(ctx, request)
}

func syncNATGatewayUpdate(ctx context.Context, obj *v1alpha1.NATGateway, remote *privatev1.NATGateway) error {
	syncNATGatewayPhase(ctx, obj, remote)
	return nil
}

// syncNATGatewayDelete maps NATGateway CR status during deletion. Failed gateways report
// FAILED, and everything else reports DELETING: a gateway whose deletion timestamp is set
// is going away whatever phase its status still carries, including the empty phase of a
// gateway deleted before any controller status write.
//
// The state and its time come from the same place. The Failed phase the CR records dates
// its own transition, an event the controller observed. The DELETING reported for every
// other phase is forced: the phase predates the deletion, so its time is not the
// deletion's — the deletion started at the deletion timestamp, and dating the report at
// the old phase's time would erase the billed span up to the deletion. A gateway deleted
// before any controller status write has no phase time either, and the deletion timestamp
// covers it.
func syncNATGatewayDelete(_ context.Context, obj *v1alpha1.NATGateway, remote *privatev1.NATGateway) error {
	var at *timestamppb.Timestamp
	switch obj.Status.Phase {
	case v1alpha1.NATGatewayPhaseFailed:
		remote.GetStatus().SetState(privatev1.NATGatewayState_NAT_GATEWAY_STATE_FAILED)
		at = stateTransitionTimeOf(obj.Status.StateTransitionTime, obj.DeletionTimestamp)
	default:
		remote.GetStatus().SetState(privatev1.NATGatewayState_NAT_GATEWAY_STATE_DELETING)
		at = laterTransitionTimeOf(obj.Status.StateTransitionTime, obj.DeletionTimestamp)
	}
	if at == nil {
		if remote.GetStatus().GetStateTransitionTime() != nil {
			return nil
		}
		return fmt.Errorf("NAT gateway %s has no state transition time", remote.GetId())
	}
	remote.GetStatus().SetStateTransitionTime(at)
	return nil
}

func syncNATGatewayStateTransitionTime(obj *v1alpha1.NATGateway, remote *privatev1.NATGateway) {
	at := stateTransitionTimeOf(obj.Status.StateTransitionTime, obj.DeletionTimestamp)
	if at == nil {
		remote.GetStatus().ClearStateTransitionTime()
		return
	}
	remote.GetStatus().SetStateTransitionTime(at)
}

func syncNATGatewayPhase(ctx context.Context, obj *v1alpha1.NATGateway, remote *privatev1.NATGateway) {
	switch obj.Status.Phase {
	case v1alpha1.NATGatewayPhaseProgressing:
		remote.GetStatus().SetState(privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING)
	case v1alpha1.NATGatewayPhaseFailed:
		remote.GetStatus().SetState(privatev1.NATGatewayState_NAT_GATEWAY_STATE_FAILED)
	case v1alpha1.NATGatewayPhaseReady:
		remote.GetStatus().SetState(privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY)
	case v1alpha1.NATGatewayPhaseDeleting:
		remote.GetStatus().SetState(privatev1.NATGatewayState_NAT_GATEWAY_STATE_DELETING)
	default:
		log := ctrllog.FromContext(ctx)
		log.Info("Unknown phase, will ignore it", "phase", obj.Status.Phase)
	}

	syncNATGatewayStateTransitionTime(obj, remote)
}
