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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/feedback"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// ExternalIPPoolFeedbackReconciler sends updates to the fulfillment service.
type ExternalIPPoolFeedbackReconciler struct {
	bridge              *feedback.Bridge[*v1alpha1.ExternalIPPool, *privatev1.ExternalIPPool]
	networkingNamespace string
}

// NewExternalIPPoolFeedbackReconciler creates a reconciler that sends to the fulfillment service updates about
// external IP pools.
func NewExternalIPPoolFeedbackReconciler(hubClient clnt.Client, grpcConn grpc.ClientConnInterface, networkingNamespace string) *ExternalIPPoolFeedbackReconciler {
	poolClient := privatev1.NewExternalIPPoolsClient(grpcConn)
	r := &ExternalIPPoolFeedbackReconciler{networkingNamespace: networkingNamespace}
	r.bridge = &feedback.Bridge[*v1alpha1.ExternalIPPool, *privatev1.ExternalIPPool]{
		Client:    hubClient,
		Finalizer: osacExternalIPPoolFeedbackFinalizer,
		IDLabel:   osacExternalIPPoolIDLabel,
		Kind:      "ExternalIPPool",
		IDKey:     "externalIPPoolID",
		NewObject: func() *v1alpha1.ExternalIPPool { return &v1alpha1.ExternalIPPool{} },
		Fetch: func(ctx context.Context, id string) (*privatev1.ExternalIPPool, error) {
			response, err := poolClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			pool := response.GetObject()
			if pool == nil {
				return nil, errors.New("external IP pool not found: response contained nil object")
			}
			if !pool.HasSpec() {
				pool.SetSpec(&privatev1.ExternalIPPoolSpec{})
			}
			if !pool.HasStatus() {
				pool.SetStatus(&privatev1.ExternalIPPoolStatus{})
			}
			return pool, nil
		},
		Save: func(ctx context.Context, remote *privatev1.ExternalIPPool) error {
			_, err := poolClient.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
				Object:     remote,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{feedbackStatusStatePath, feedbackStatusMessagePath}},
			}.Build())
			if err != nil {
				return err
			}
			// Signal on change, so the fulfillment service acts on the new state.
			_, err = poolClient.Signal(ctx, privatev1.ExternalIPPoolsSignalRequest_builder{Id: remote.GetId()}.Build())
			return err
		},
		Signal: func(ctx context.Context, id string) error {
			_, err := poolClient.Signal(ctx, privatev1.ExternalIPPoolsSignalRequest_builder{
				Id: id,
			}.Build())
			return err
		},
		SyncUpdate: syncExternalIPPoolUpdate,
		SyncDelete: syncExternalIPPoolDelete,
	}
	return r
}

// SetupWithManager adds the reconciler to the controller manager.
func (r *ExternalIPPoolFeedbackReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return fmt.Errorf("local manager is nil")
	}

	return ctrl.NewControllerManagedBy(localMgr).
		Named("externalippool-feedback").
		For(&v1alpha1.ExternalIPPool{}, builder.WithPredicates(NetworkingNamespacePredicate(r.networkingNamespace))).
		Complete(r)
}

// Reconcile delegates to the shared feedback Bridge.
func (r *ExternalIPPoolFeedbackReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	return r.bridge.Reconcile(ctx, request)
}

func syncExternalIPPoolUpdate(_ context.Context, obj *v1alpha1.ExternalIPPool, remote *privatev1.ExternalIPPool) error {
	status := remote.GetStatus()
	state, message := networkFeedback(obj, obj.Status.Conditions)
	switch state {
	case networkFeedbackReady:
		status.SetState(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY)
	case networkFeedbackFailed:
		status.SetState(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_FAILED)
	case networkFeedbackDeleting:
		status.SetState(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_DELETING)
	default:
		status.SetState(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING)
	}
	feedbackMessage(message, status.SetMessage, status.ClearMessage)
	return nil
}

func syncExternalIPPoolDelete(ctx context.Context, obj *v1alpha1.ExternalIPPool, remote *privatev1.ExternalIPPool) error {
	return syncExternalIPPoolUpdate(ctx, obj, remote)
}
