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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/internal/controller/feedback"
)

var ErrExternalIPNotFound = errors.New("external IP not found in fulfillment service")

// ExternalIPFeedbackReconciler sends updates to the fulfillment service.
type ExternalIPFeedbackReconciler struct {
	bridge              *feedback.Bridge[*v1alpha1.ExternalIP, *privatev1.ExternalIP]
	networkingNamespace string
}

// NewExternalIPFeedbackReconciler creates a reconciler that sends to the fulfillment service updates about external IPs.
func NewExternalIPFeedbackReconciler(hubClient clnt.Client, grpcConn *grpc.ClientConn, networkingNamespace string) *ExternalIPFeedbackReconciler {
	eipClient := privatev1.NewExternalIPsClient(grpcConn)
	r := &ExternalIPFeedbackReconciler{networkingNamespace: networkingNamespace}
	r.bridge = &feedback.Bridge[*v1alpha1.ExternalIP, *privatev1.ExternalIP]{
		Client:    hubClient,
		Finalizer: osacExternalIPFeedbackFinalizer,
		IDLabel:   osacExternalIPIDLabel,
		Kind:      "ExternalIP",
		IDKey:     "externalIPID",
		NewObject: func() *v1alpha1.ExternalIP { return &v1alpha1.ExternalIP{} },
		Fetch: func(ctx context.Context, id string) (*privatev1.ExternalIP, error) {
			response, err := eipClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{Id: id}.Build())
			if err != nil {
				if status.Code(err) == codes.NotFound {
					return nil, fmt.Errorf("%w: %w", ErrExternalIPNotFound, err)
				}
				return nil, err
			}
			eip := response.GetObject()
			if eip == nil {
				return nil, fmt.Errorf("%w: response contained nil object", ErrExternalIPNotFound)
			}
			if !eip.HasSpec() {
				eip.SetSpec(&privatev1.ExternalIPSpec{})
			}
			if !eip.HasStatus() {
				eip.SetStatus(&privatev1.ExternalIPStatus{})
			}
			return eip, nil
		},
		Save: func(ctx context.Context, remote *privatev1.ExternalIP) error {
			_, err := eipClient.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
				Object: remote,
			}.Build())
			return err
		},
		Signal: func(ctx context.Context, id string) error {
			_, err := eipClient.Signal(ctx, privatev1.ExternalIPsSignalRequest_builder{
				Id: id,
			}.Build())
			return err
		},
		SyncUpdate: syncExternalIPUpdate,
		SyncDelete: syncExternalIPDelete,
		IsNotFound: func(err error) bool { return errors.Is(err, ErrExternalIPNotFound) },
	}
	return r
}

// SetupWithManager adds the reconciler to the controller manager.
func (r *ExternalIPFeedbackReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return fmt.Errorf("local manager is nil")
	}

	return ctrl.NewControllerManagedBy(localMgr).
		Named("externalip-feedback").
		For(&v1alpha1.ExternalIP{}, builder.WithPredicates(NetworkingNamespacePredicate(r.networkingNamespace))).
		Complete(r)
}

// Reconcile delegates to the shared feedback Bridge.
func (r *ExternalIPFeedbackReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	return r.bridge.Reconcile(ctx, request)
}

func syncExternalIPUpdate(ctx context.Context, obj *v1alpha1.ExternalIP, remote *privatev1.ExternalIP) error {
	syncExternalIPState(ctx, obj, remote)
	syncExternalIPAddress(obj, remote)
	return nil
}

func syncExternalIPDelete(_ context.Context, obj *v1alpha1.ExternalIP, remote *privatev1.ExternalIP) error {
	if obj.Status.State == v1alpha1.ExternalIPStateFailed {
		remote.GetStatus().SetState(privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED)
		return nil
	}
	remote.GetStatus().SetState(privatev1.ExternalIPState_EXTERNAL_IP_STATE_DELETING)
	return nil
}

func syncExternalIPState(ctx context.Context, obj *v1alpha1.ExternalIP, remote *privatev1.ExternalIP) {
	switch obj.Status.State {
	case v1alpha1.ExternalIPStatePending:
		remote.GetStatus().SetState(privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING)
	case v1alpha1.ExternalIPStateAllocated:
		remote.GetStatus().SetState(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
	case v1alpha1.ExternalIPStateFailed:
		remote.GetStatus().SetState(privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED)
	default:
		log := ctrllog.FromContext(ctx)
		log.Info("Unknown state, will ignore it", "state", obj.Status.State)
	}
}

func syncExternalIPAddress(obj *v1alpha1.ExternalIP, remote *privatev1.ExternalIP) {
	if obj.Status.Address != "" {
		remote.GetStatus().SetAddress(obj.Status.Address)
	}
}
