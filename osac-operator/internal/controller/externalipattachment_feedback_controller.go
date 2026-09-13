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
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/feedback"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
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
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
					feedbackStatusStatePath, "status.external_ip_address", feedbackStatusMessagePath, feedbackStatusStateTransitionTimePath,
				}},
			}.Build())
			return err
		},
		Signal: func(ctx context.Context, id string) error {
			_, err := attachClient.Signal(ctx, privatev1.ExternalIPAttachmentsSignalRequest_builder{
				Id: id,
			}.Build())
			return err
		},
		SyncUpdate: newExternalIPAttachmentSyncUpdate(eipClient, hubClient),
		SyncDelete: newExternalIPAttachmentSyncDelete(hubClient),
		IsNotFound: func(err error) bool { return errors.Is(err, ErrExternalIPAttachmentNotFound) },
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
// for syncing the parent's address to the attachment.
func newExternalIPAttachmentSyncUpdate(eipClient privatev1.ExternalIPsClient, hubClient clnt.Client) func(context.Context, *v1alpha1.ExternalIPAttachment, *privatev1.ExternalIPAttachment) error {
	return func(ctx context.Context, obj *v1alpha1.ExternalIPAttachment, remote *privatev1.ExternalIPAttachment) error {
		syncExternalIPAttachmentState(ctx, obj, remote)
		syncExternalIPAttachmentAddress(ctx, eipClient, remote)
		transition := obj.Status.StateTransitionTime
		var transitionTime *timestamppb.Timestamp
		if transition != nil {
			transitionTime = timestamppb.New(transition.Time)
		}
		return syncExternalIPAttachmentParentCRD(ctx, hubClient, obj.Namespace, remote.GetSpec().GetExternalIp().GetId(),
			obj.Status.Phase == v1alpha1.ExternalIPAttachmentPhaseReady, transitionTime)
	}
}

func newExternalIPAttachmentSyncDelete(hubClient clnt.Client) func(context.Context, *v1alpha1.ExternalIPAttachment, *privatev1.ExternalIPAttachment) error {
	return func(ctx context.Context, obj *v1alpha1.ExternalIPAttachment, remote *privatev1.ExternalIPAttachment) error {
		if err := syncExternalIPAttachmentDelete(ctx, obj, remote); err != nil {
			return err
		}
		transition := obj.Status.StateTransitionTime
		var transitionTime *timestamppb.Timestamp
		if transition != nil {
			transitionTime = timestamppb.New(transition.Time)
		}
		return syncExternalIPAttachmentParentCRD(ctx, hubClient, obj.Namespace, remote.GetSpec().GetExternalIp().GetId(), false, transitionTime)
	}
}

func syncExternalIPAttachmentDelete(_ context.Context, obj *v1alpha1.ExternalIPAttachment, remote *privatev1.ExternalIPAttachment) error {
	syncExternalIPAttachmentStateTransitionTime(obj, remote)
	if obj.Status.Phase == v1alpha1.ExternalIPAttachmentPhaseFailed {
		remote.GetStatus().SetState(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_FAILED)
		return nil
	}
	remote.GetStatus().SetState(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_DELETING)
	return nil
}

func syncExternalIPAttachmentStateTransitionTime(obj *v1alpha1.ExternalIPAttachment, remote *privatev1.ExternalIPAttachment) {
	if obj.Status.StateTransitionTime == nil {
		remote.GetStatus().ClearStateTransitionTime()
		return
	}
	remote.GetStatus().SetStateTransitionTime(timestamppb.New(obj.Status.StateTransitionTime.Time))
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

	syncExternalIPAttachmentStateTransitionTime(obj, remote)
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

func syncExternalIPAttachmentParentCRD(
	ctx context.Context,
	hubClient clnt.Client,
	namespace string,
	externalIPID string,
	attached bool,
	transitionTime *timestamppb.Timestamp,
) error {
	if externalIPID == "" {
		return nil
	}
	list := &v1alpha1.ExternalIPList{}
	if err := hubClient.List(ctx, list, clnt.InNamespace(namespace), clnt.MatchingLabels{osacExternalIPIDLabel: externalIPID}); err != nil {
		return err
	}
	if len(list.Items) == 0 && !attached {
		return nil
	}
	if len(list.Items) != 1 {
		return fmt.Errorf("expected one ExternalIP CR for ID %s in namespace %s, found %d", externalIPID, namespace, len(list.Items))
	}
	parent := &list.Items[0]
	var desiredTransition *metav1.Time
	if transitionTime != nil {
		time := metav1.NewTime(transitionTime.AsTime())
		desiredTransition = &time
	}
	if parent.Status.Attached == attached && externalIPCRDTimeEqual(parent.Status.AttachmentTransitionTime, desiredTransition) {
		return nil
	}
	parent.Status.Attached = attached
	parent.Status.AttachmentTransitionTime = desiredTransition
	return hubClient.Status().Update(ctx, parent)
}

func externalIPCRDTimeEqual(left, right *metav1.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Time.Equal(right.Time)
}
