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
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"sigs.k8s.io/controller-runtime/pkg/builder"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/internal/controller/feedback"
)

// SubnetFeedbackReconciler sends updates to the fulfillment service.
type SubnetFeedbackReconciler struct {
	bridge              *feedback.Bridge[*v1alpha1.Subnet, *privatev1.Subnet]
	networkingNamespace string
}

// NewSubnetFeedbackReconciler creates a reconciler that sends to the fulfillment service updates about subnets.
func NewSubnetFeedbackReconciler(hubClient clnt.Client, grpcConn *grpc.ClientConn, networkingNamespace string) *SubnetFeedbackReconciler {
	subnetsClient := privatev1.NewSubnetsClient(grpcConn)
	r := &SubnetFeedbackReconciler{networkingNamespace: networkingNamespace}
	r.bridge = &feedback.Bridge[*v1alpha1.Subnet, *privatev1.Subnet]{
		Client:    hubClient,
		Finalizer: osacSubnetFeedbackFinalizer,
		IDLabel:   osacSubnetIDLabel,
		Kind:      "Subnet",
		IDKey:     "subnetID",
		NewObject: func() *v1alpha1.Subnet { return &v1alpha1.Subnet{} },
		Fetch: func(ctx context.Context, id string) (*privatev1.Subnet, error) {
			response, err := subnetsClient.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			subnet := response.GetObject()
			if subnet == nil {
				return nil, errors.New("subnet response contained nil object")
			}
			if !subnet.HasSpec() {
				subnet.SetSpec(&privatev1.SubnetSpec{})
			}
			if !subnet.HasStatus() {
				subnet.SetStatus(&privatev1.SubnetStatus{})
			}
			return subnet, nil
		},
		Save: func(ctx context.Context, remote *privatev1.Subnet) error {
			_, err := subnetsClient.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
				Object: remote,
			}.Build())
			return err
		},
		Signal: func(ctx context.Context, id string) error {
			_, err := subnetsClient.Signal(ctx, privatev1.SubnetsSignalRequest_builder{
				Id: id,
			}.Build())
			return err
		},
		SyncUpdate: syncSubnetUpdate,
		SyncDelete: syncSubnetDelete,
	}
	return r
}

// SetupWithManager adds the reconciler to the controller manager.
func (r *SubnetFeedbackReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return fmt.Errorf("local manager is nil")
	}

	return ctrl.NewControllerManagedBy(localMgr).
		Named("subnet-feedback").
		For(&v1alpha1.Subnet{}, builder.WithPredicates(NetworkingNamespacePredicate(r.networkingNamespace))).
		Complete(r)
}

// Reconcile delegates to the shared feedback Bridge.
func (r *SubnetFeedbackReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	return r.bridge.Reconcile(ctx, request)
}

func syncSubnetUpdate(ctx context.Context, obj *v1alpha1.Subnet, remote *privatev1.Subnet) error {
	syncSubnetPhase(ctx, obj, remote)
	syncSubnetBackendNetworkID(obj, remote)
	return nil
}

func syncSubnetDelete(_ context.Context, obj *v1alpha1.Subnet, remote *privatev1.Subnet) error {
	if obj.Status.Phase == v1alpha1.SubnetPhaseFailed {
		remote.GetStatus().SetState(privatev1.SubnetState_SUBNET_STATE_DELETE_FAILED)
		return nil
	}
	remote.GetStatus().SetState(privatev1.SubnetState_SUBNET_STATE_DELETING)
	return nil
}

func syncSubnetPhase(ctx context.Context, obj *v1alpha1.Subnet, remote *privatev1.Subnet) {
	switch obj.Status.Phase {
	case v1alpha1.SubnetPhaseProgressing:
		remote.GetStatus().SetState(privatev1.SubnetState_SUBNET_STATE_PENDING)
	case v1alpha1.SubnetPhaseFailed:
		remote.GetStatus().SetState(privatev1.SubnetState_SUBNET_STATE_FAILED)
	case v1alpha1.SubnetPhaseReady:
		remote.GetStatus().SetState(privatev1.SubnetState_SUBNET_STATE_READY)
	case v1alpha1.SubnetPhaseDeleting:
		remote.GetStatus().SetState(privatev1.SubnetState_SUBNET_STATE_DELETING)
	default:
		log := ctrllog.FromContext(ctx)
		log.Info("Unknown phase, will ignore it", "phase", obj.Status.Phase)
	}
}

func syncSubnetBackendNetworkID(obj *v1alpha1.Subnet, remote *privatev1.Subnet) {
	if obj.Status.BackendNetworkID != "" {
		remote.GetStatus().SetMessage(sanitizeFeedbackText(obj.Status.BackendNetworkID))
	}
}
