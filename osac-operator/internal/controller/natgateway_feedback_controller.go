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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/internal/controller/feedback"
)

// NATGatewayFeedbackReconciler sends updates to the fulfillment service.
type NATGatewayFeedbackReconciler struct {
	bridge              *feedback.Bridge[*v1alpha1.NATGateway, *privatev1.NATGateway]
	networkingNamespace string
}

// NewNATGatewayFeedbackReconciler creates a reconciler that sends to the fulfillment service
// updates about NAT gateways.
func NewNATGatewayFeedbackReconciler(hubClient clnt.Client, grpcConn *grpc.ClientConn, networkingNamespace string) *NATGatewayFeedbackReconciler {
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

func syncNATGatewayDelete(_ context.Context, obj *v1alpha1.NATGateway, remote *privatev1.NATGateway) error {
	if obj.Status.Phase == v1alpha1.NATGatewayPhaseFailed {
		remote.GetStatus().SetState(privatev1.NATGatewayState_NAT_GATEWAY_STATE_FAILED)
		return nil
	}
	remote.GetStatus().SetState(privatev1.NATGatewayState_NAT_GATEWAY_STATE_DELETING)
	return nil
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
}
