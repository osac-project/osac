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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/internal/controller/feedback"
)

// VirtualNetworkFeedbackReconciler sends updates to the fulfillment service.
type VirtualNetworkFeedbackReconciler struct {
	bridge              *feedback.Bridge[*v1alpha1.VirtualNetwork, *privatev1.VirtualNetwork]
	networkingNamespace string
}

// NewVirtualNetworkFeedbackReconciler creates a reconciler that sends to the fulfillment service updates about virtual networks.
func NewVirtualNetworkFeedbackReconciler(hubClient clnt.Client, grpcConn *grpc.ClientConn, networkingNamespace string) *VirtualNetworkFeedbackReconciler {
	vnClient := privatev1.NewVirtualNetworksClient(grpcConn)
	r := &VirtualNetworkFeedbackReconciler{networkingNamespace: networkingNamespace}
	r.bridge = &feedback.Bridge[*v1alpha1.VirtualNetwork, *privatev1.VirtualNetwork]{
		Client:    hubClient,
		Finalizer: osacVirtualNetworkFeedbackFinalizer,
		IDLabel:   osacVirtualNetworkIDLabel,
		Kind:      "VirtualNetwork",
		IDKey:     "virtualNetworkID",
		NewObject: func() *v1alpha1.VirtualNetwork { return &v1alpha1.VirtualNetwork{} },
		Fetch: func(ctx context.Context, id string) (*privatev1.VirtualNetwork, error) {
			response, err := vnClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			vn := response.GetObject()
			if vn == nil {
				return nil, errors.New("virtual network response contained nil object")
			}
			if !vn.HasSpec() {
				vn.SetSpec(&privatev1.VirtualNetworkSpec{})
			}
			if !vn.HasStatus() {
				vn.SetStatus(&privatev1.VirtualNetworkStatus{})
			}
			return vn, nil
		},
		Save: func(ctx context.Context, remote *privatev1.VirtualNetwork) error {
			_, err := vnClient.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
				Object: remote,
			}.Build())
			return err
		},
		Signal: func(ctx context.Context, id string) error {
			_, err := vnClient.Signal(ctx, privatev1.VirtualNetworksSignalRequest_builder{
				Id: id,
			}.Build())
			return err
		},
		SyncUpdate: syncVirtualNetworkUpdate,
		SyncDelete: syncVirtualNetworkDelete,
	}
	return r
}

// SetupWithManager adds the reconciler to the controller manager.
func (r *VirtualNetworkFeedbackReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return fmt.Errorf("local manager is nil")
	}

	return ctrl.NewControllerManagedBy(localMgr).
		Named("virtualnetwork-feedback").
		For(&v1alpha1.VirtualNetwork{}, builder.WithPredicates(NetworkingNamespacePredicate(r.networkingNamespace))).
		Complete(r)
}

// Reconcile delegates to the shared feedback Bridge.
func (r *VirtualNetworkFeedbackReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	return r.bridge.Reconcile(ctx, request)
}

func syncVirtualNetworkUpdate(ctx context.Context, obj *v1alpha1.VirtualNetwork, remote *privatev1.VirtualNetwork) error {
	syncVirtualNetworkPhase(ctx, obj, remote)
	return nil
}

// VN proto has no DELETING/DELETE_FAILED states, so deletion maps Failed to
// FAILED and everything else to PENDING.
func syncVirtualNetworkDelete(_ context.Context, obj *v1alpha1.VirtualNetwork, remote *privatev1.VirtualNetwork) error {
	if obj.Status.Phase == v1alpha1.VirtualNetworkPhaseFailed {
		remote.GetStatus().SetState(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_FAILED)
		return nil
	}
	remote.GetStatus().SetState(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING)
	return nil
}

func syncVirtualNetworkPhase(ctx context.Context, obj *v1alpha1.VirtualNetwork, remote *privatev1.VirtualNetwork) {
	switch obj.Status.Phase {
	case v1alpha1.VirtualNetworkPhaseProgressing:
		remote.GetStatus().SetState(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING)
	case v1alpha1.VirtualNetworkPhaseFailed:
		remote.GetStatus().SetState(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_FAILED)
	case v1alpha1.VirtualNetworkPhaseReady:
		remote.GetStatus().SetState(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY)
	case v1alpha1.VirtualNetworkPhaseDeleting:
		remote.GetStatus().SetState(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING)
	default:
		log := ctrllog.FromContext(ctx)
		log.Info("Unknown phase, will ignore it", "phase", obj.Status.Phase)
	}
}
