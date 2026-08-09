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

var ErrExternalIPAttachmentNotFound = errors.New("external IP attachment not found in fulfillment service")

type ExternalIPAttachmentFeedbackReconciler struct {
	bridge              *feedback.Bridge[*v1alpha1.ExternalIPAttachment, *privatev1.ExternalIPAttachment]
	networkingNamespace string
}

func NewExternalIPAttachmentFeedbackReconciler(hubClient clnt.Client, grpcConn *grpc.ClientConn, networkingNamespace string) *ExternalIPAttachmentFeedbackReconciler {
	attachClient := privatev1.NewExternalIPAttachmentsClient(grpcConn)
	eipClient := privatev1.NewExternalIPsClient(grpcConn)
	r := &ExternalIPAttachmentFeedbackReconciler{networkingNamespace: networkingNamespace}
	r.bridge = &feedback.Bridge[*v1alpha1.ExternalIPAttachment, *privatev1.ExternalIPAttachment]{
		Client:    hubClient,
		Finalizer: osacExternalIPAttachmentFeedbackFinalizer,
		IDLabel:   osacExternalIPAttachmentIDLabel,
		Kind:      "ExternalIPAttachment",
		IDKey:     "attachmentID",
		NewObject: func() *v1alpha1.ExternalIPAttachment { return &v1alpha1.ExternalIPAttachment{} },
		Fetch: func(ctx context.Context, id string) (*privatev1.ExternalIPAttachment, error) {
			response, err := attachClient.Get(ctx, privatev1.ExternalIPAttachmentsGetRequest_builder{Id: id}.Build())
			if err != nil {
				if status.Code(err) == codes.NotFound {
					return nil, fmt.Errorf("%w: %w", ErrExternalIPAttachmentNotFound, err)
				}
				return nil, err
			}
			obj := response.GetObject()
			if obj == nil {
				return nil, fmt.Errorf("%w: response contained nil object", ErrExternalIPAttachmentNotFound)
			}
			if !obj.HasSpec() {
				obj.SetSpec(&privatev1.ExternalIPAttachmentSpec{})
			}
			if !obj.HasStatus() {
				obj.SetStatus(&privatev1.ExternalIPAttachmentStatus{})
			}
			return obj, nil
		},
		Save: func(ctx context.Context, remote *privatev1.ExternalIPAttachment) error {
			_, err := attachClient.Update(ctx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
				Object: remote,
			}.Build())
			return err
		},
		Signal: func(ctx context.Context, id string) error {
			_, err := attachClient.Signal(ctx, privatev1.ExternalIPAttachmentsSignalRequest_builder{
				Id: id,
			}.Build())
			return err
		},
		SyncUpdate:       newExternalIPAttachmentSyncUpdate(eipClient),
		SyncDelete:       syncExternalIPAttachmentDelete,
		PostSaveOnDelete: newExternalIPAttachmentPostSaveOnDelete(eipClient),
		IsNotFound:       func(err error) bool { return errors.Is(err, ErrExternalIPAttachmentNotFound) },
	}
	return r
}

func (r *ExternalIPAttachmentFeedbackReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return fmt.Errorf("local manager is nil")
	}

	return ctrl.NewControllerManagedBy(localMgr).
		Named("externalipattachment-feedback").
		For(&v1alpha1.ExternalIPAttachment{}, builder.WithPredicates(NetworkingNamespacePredicate(r.networkingNamespace))).
		Complete(r)
}

// Reconcile delegates to the shared feedback Bridge.
func (r *ExternalIPAttachmentFeedbackReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	return r.bridge.Reconcile(ctx, request)
}

// newExternalIPAttachmentSyncUpdate returns a SyncUpdate that captures eipClient
// for setting attached=true on the parent ExternalIP when Ready, and for syncing
// the parent's address to the attachment.
func newExternalIPAttachmentSyncUpdate(eipClient privatev1.ExternalIPsClient) func(context.Context, *v1alpha1.ExternalIPAttachment, *privatev1.ExternalIPAttachment) error {
	return func(ctx context.Context, obj *v1alpha1.ExternalIPAttachment, remote *privatev1.ExternalIPAttachment) error {
		syncExternalIPAttachmentState(ctx, obj, remote)
		syncExternalIPAttachmentAddress(ctx, eipClient, remote)

		if obj.Status.Phase == v1alpha1.ExternalIPAttachmentPhaseReady {
			if err := syncAttachedOnParentExternalIP(ctx, eipClient, remote, true); err != nil {
				ctrllog.FromContext(ctx).Error(err, "Failed to set attached on parent ExternalIP, will retry")
				return err
			}
		}
		return nil
	}
}

func syncExternalIPAttachmentDelete(_ context.Context, obj *v1alpha1.ExternalIPAttachment, remote *privatev1.ExternalIPAttachment) error {
	if obj.Status.Phase == v1alpha1.ExternalIPAttachmentPhaseFailed {
		remote.GetStatus().SetState(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_FAILED)
		return nil
	}
	remote.GetStatus().SetState(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_DELETING)
	return nil
}

// newExternalIPAttachmentPostSaveOnDelete returns a PostSaveOnDelete that clears
// the attached flag on the parent ExternalIP after the attachment's DELETING
// state is persisted.
func newExternalIPAttachmentPostSaveOnDelete(eipClient privatev1.ExternalIPsClient) func(context.Context, *v1alpha1.ExternalIPAttachment, *privatev1.ExternalIPAttachment) error {
	return func(ctx context.Context, _ *v1alpha1.ExternalIPAttachment, remote *privatev1.ExternalIPAttachment) error {
		if err := syncAttachedOnParentExternalIP(ctx, eipClient, remote, false); err != nil {
			ctrllog.FromContext(ctx).Error(err, "Failed to clear attached on parent ExternalIP, will retry")
			return err
		}
		return nil
	}
}

func syncExternalIPAttachmentState(ctx context.Context, obj *v1alpha1.ExternalIPAttachment, remote *privatev1.ExternalIPAttachment) {
	switch obj.Status.Phase {
	case v1alpha1.ExternalIPAttachmentPhaseProgressing:
		remote.GetStatus().SetState(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING)
	case v1alpha1.ExternalIPAttachmentPhaseReady:
		remote.GetStatus().SetState(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY)
	case v1alpha1.ExternalIPAttachmentPhaseFailed:
		remote.GetStatus().SetState(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_FAILED)
	default:
		log := ctrllog.FromContext(ctx)
		log.Info("Unknown phase, will ignore it", "phase", obj.Status.Phase)
	}
}

func syncExternalIPAttachmentAddress(ctx context.Context, eipClient privatev1.ExternalIPsClient, remote *privatev1.ExternalIPAttachment) {
	externalIPRef := remote.GetSpec().GetExternalIp()
	if externalIPRef.GetId() == "" {
		return
	}
	response, err := eipClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
		Id: externalIPRef.GetId(),
	}.Build())
	if err != nil {
		ctrllog.FromContext(ctx).Error(err, "Failed to fetch parent ExternalIP for address sync", "externalIPID", externalIPRef.GetId())
		return
	}
	obj := response.GetObject()
	if obj == nil || !obj.HasStatus() {
		return
	}
	if addr := obj.GetStatus().GetAddress(); addr != "" {
		remote.GetStatus().SetExternalIpAddress(addr)
	}
}

func syncAttachedOnParentExternalIP(ctx context.Context, eipClient privatev1.ExternalIPsClient, remote *privatev1.ExternalIPAttachment, attached bool) error {
	externalIPRef := remote.GetSpec().GetExternalIp()
	if externalIPRef.GetId() == "" {
		return nil
	}

	response, err := eipClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
		Id: externalIPRef.GetId(),
	}.Build())
	if err != nil {
		if status.Code(err) == codes.NotFound {
			ctrllog.FromContext(ctx).Info("Parent ExternalIP not found, skipping attached sync", "externalIPID", externalIPRef.GetId())
			return nil
		}
		return err
	}

	externalIP := response.GetObject()
	if externalIP == nil {
		return fmt.Errorf("parent ExternalIP %s: response contained nil object", externalIPRef.GetId())
	}
	if !externalIP.HasStatus() {
		externalIP.SetStatus(&privatev1.ExternalIPStatus{})
	}

	if externalIP.GetStatus().GetAttached() == attached {
		return nil
	}

	externalIP.GetStatus().SetAttached(attached)
	_, err = eipClient.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
		Object: externalIP,
	}.Build())
	return err
}
