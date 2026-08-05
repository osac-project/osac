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

// ExternalIPPoolFeedbackReconciler sends updates to the fulfillment service.
type ExternalIPPoolFeedbackReconciler struct {
	bridge              *feedback.Bridge[*v1alpha1.ExternalIPPool, *privatev1.ExternalIPPool]
	networkingNamespace string
}

// NewExternalIPPoolFeedbackReconciler creates a reconciler that sends to the fulfillment service updates about
// external IP pools.
func NewExternalIPPoolFeedbackReconciler(hubClient clnt.Client, grpcConn *grpc.ClientConn, networkingNamespace string) *ExternalIPPoolFeedbackReconciler {
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
				Object: remote,
			}.Build())
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

func syncExternalIPPoolUpdate(ctx context.Context, obj *v1alpha1.ExternalIPPool, remote *privatev1.ExternalIPPool) error {
	syncExternalIPPoolPhase(ctx, obj, remote)
	return nil
}

func syncExternalIPPoolDelete(_ context.Context, obj *v1alpha1.ExternalIPPool, remote *privatev1.ExternalIPPool) error {
	if obj.Status.Phase == v1alpha1.ExternalIPPoolPhaseFailed {
		remote.GetStatus().SetState(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_DELETE_FAILED)
		return nil
	}
	remote.GetStatus().SetState(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_DELETING)
	return nil
}

func syncExternalIPPoolPhase(ctx context.Context, obj *v1alpha1.ExternalIPPool, remote *privatev1.ExternalIPPool) {
	switch obj.Status.Phase {
	case v1alpha1.ExternalIPPoolPhaseProgressing:
		remote.GetStatus().SetState(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING)
	case v1alpha1.ExternalIPPoolPhaseFailed:
		remote.GetStatus().SetState(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_FAILED)
	case v1alpha1.ExternalIPPoolPhaseReady:
		remote.GetStatus().SetState(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY)
	case v1alpha1.ExternalIPPoolPhaseDeleting:
		remote.GetStatus().SetState(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_DELETING)
	default:
		log := ctrllog.FromContext(ctx)
		log.Info("Unknown phase, will ignore it", "phase", obj.Status.Phase)
	}
}
