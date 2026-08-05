/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package externalipattachment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"

	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/controllers"
	"github.com/osac-project/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/fulfillment-service/internal/kubernetes/annotations"
	"github.com/osac-project/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/fulfillment-service/internal/masks"
)

const objectPrefix = "externalipattachment-"

// FunctionBuilder contains the data and logic needed to build a function that reconciles external IP attachments.
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	hubCache   controllers.HubCache
}

type function struct {
	logger                      *slog.Logger
	hubCache                    controllers.HubCache
	externalIPAttachmentsClient privatev1.ExternalIPAttachmentsClient
	externalIPsClient           privatev1.ExternalIPsClient
	maskCalculator              *masks.Calculator
}

type task struct {
	r                    *function
	externalIPAttachment *privatev1.ExternalIPAttachment
	hubId                string
	hubNamespace         string
	hubClient            clnt.Client
}

// NewFunction creates a new builder that can then be used to create a new external IP attachment reconciler function.
func NewFunction() *FunctionBuilder {
	return &FunctionBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *FunctionBuilder) SetLogger(value *slog.Logger) *FunctionBuilder {
	b.logger = value
	return b
}

// SetConnection sets the gRPC client connection. This is mandatory.
func (b *FunctionBuilder) SetConnection(value *grpc.ClientConn) *FunctionBuilder {
	b.connection = value
	return b
}

// SetHubCache sets the cache of hubs. This is mandatory.
func (b *FunctionBuilder) SetHubCache(value controllers.HubCache) *FunctionBuilder {
	b.hubCache = value
	return b
}

// Build uses the information stored in the builder to create a new external IP attachment reconciler.
func (b *FunctionBuilder) Build() (result controllers.ReconcilerFunction[*privatev1.ExternalIPAttachment], err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.connection == nil {
		err = errors.New("client is mandatory")
		return
	}
	if b.hubCache == nil {
		err = errors.New("hub cache is mandatory")
		return
	}

	object := &function{
		logger:                      b.logger,
		externalIPAttachmentsClient: privatev1.NewExternalIPAttachmentsClient(b.connection),
		externalIPsClient:           privatev1.NewExternalIPsClient(b.connection),
		hubCache:                    b.hubCache,
		maskCalculator:              masks.NewCalculator().Build(),
	}
	result = object.run
	return
}

func (r *function) run(ctx context.Context, externalIPAttachment *privatev1.ExternalIPAttachment) error {
	oldAttachment := proto.Clone(externalIPAttachment).(*privatev1.ExternalIPAttachment)
	t := task{
		r:                    r,
		externalIPAttachment: externalIPAttachment,
	}
	var err error
	if externalIPAttachment.HasMetadata() && externalIPAttachment.GetMetadata().HasDeletionTimestamp() {
		err = t.delete(ctx)
	} else {
		err = t.update(ctx)
	}
	if err != nil {
		return err
	}
	updateMask := r.maskCalculator.Calculate(oldAttachment, externalIPAttachment)
	if len(updateMask.GetPaths()) == 0 {
		return nil
	}

	_, err = r.externalIPAttachmentsClient.Update(ctx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
		Object:     externalIPAttachment,
		UpdateMask: updateMask,
	}.Build())

	return err
}

func (t *task) update(ctx context.Context) error {
	if t.addFinalizer() {
		return nil
	}

	t.setDefaults()

	if err := t.validateTenant(); err != nil {
		return err
	}

	if err := t.selectHub(ctx); err != nil {
		return err
	}

	t.externalIPAttachment.GetStatus().SetHub(t.hubId)

	object, err := t.getKubeObject(ctx)
	if err != nil {
		return err
	}

	spec := t.buildSpec()

	if object == nil {
		newObject := &osacv1alpha1.ExternalIPAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    t.hubNamespace,
				GenerateName: objectPrefix,
				Labels: map[string]string{
					labels.ExternalIPAttachmentUuid: t.externalIPAttachment.GetId(),
				},
				Annotations: map[string]string{
					annotations.Tenant: t.externalIPAttachment.GetMetadata().GetTenant(),
				},
			},
			Spec: spec,
		}
		err = t.hubClient.Create(ctx, newObject)
		if err != nil {
			return err
		}
		t.r.logger.DebugContext(
			ctx,
			"Created external IP attachment",
			slog.String("namespace", newObject.GetNamespace()),
			slog.String("name", newObject.GetName()),
		)
	} else {
		update := object.DeepCopy()
		update.Spec = spec
		err = t.hubClient.Patch(ctx, update, clnt.MergeFrom(object))
		if err != nil {
			return err
		}
		t.r.logger.DebugContext(
			ctx,
			"Updated external IP attachment",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	}

	return err
}

func (t *task) setDefaults() {
	if !t.externalIPAttachment.HasStatus() {
		t.externalIPAttachment.SetStatus(&privatev1.ExternalIPAttachmentStatus{})
	}
	if t.externalIPAttachment.GetStatus().GetState() == privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_UNSPECIFIED {
		t.externalIPAttachment.GetStatus().SetState(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING)
	}
}

func (t *task) validateTenant() error {
	if !t.externalIPAttachment.HasMetadata() || t.externalIPAttachment.GetMetadata().GetTenant() == "" {
		return errors.New("external IP attachment must have a tenant assigned")
	}
	return nil
}

func (t *task) delete(ctx context.Context) (err error) {
	t.hubId = t.externalIPAttachment.GetStatus().GetHub()
	if t.hubId == "" {
		t.removeFinalizer()
		return nil
	}
	err = t.getHub(ctx)
	if err != nil {
		if errors.Is(err, controllers.ErrHubNotFound) {
			controllers.RemoveFinalizerOnDecommissionedHub(ctx, t.r.logger, t.hubId, "external_ip_attachment_id", t.externalIPAttachment.GetId(), t.removeFinalizer)
			return nil
		}
		return
	}

	object, err := t.getKubeObject(ctx)
	if err != nil {
		return
	}
	if object == nil {
		t.r.logger.DebugContext(
			ctx,
			"External IP attachment doesn't exist",
			slog.String("id", t.externalIPAttachment.GetId()),
		)
		t.removeFinalizer()
		return
	}

	if object.GetDeletionTimestamp() == nil {
		err = t.hubClient.Delete(ctx, object)
		if err != nil {
			return
		}
		t.r.logger.DebugContext(
			ctx,
			"Deleted external IP attachment",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	} else {
		t.r.logger.DebugContext(
			ctx,
			"External IP attachment is still being deleted, waiting for K8s finalizers",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	}

	return
}

func (t *task) selectHub(ctx context.Context) error {
	t.hubId = t.externalIPAttachment.GetStatus().GetHub()
	if t.hubId == "" {
		eipResponse, err := t.r.externalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
			Id: t.externalIPAttachment.GetSpec().GetExternalIp(),
		}.Build())
		if err != nil {
			return err
		}
		eipHub := eipResponse.GetObject().GetStatus().GetHub()
		if eipHub == "" {
			return fmt.Errorf(
				"external IP %s has no hub assigned yet, skipping",
				t.externalIPAttachment.GetSpec().GetExternalIp(),
			)
		}
		t.hubId = eipHub
	}
	t.r.logger.DebugContext(
		ctx,
		"Selected hub",
		slog.String("id", t.hubId),
	)
	hubEntry, err := t.r.hubCache.Get(ctx, t.hubId)
	if err != nil {
		return err
	}
	t.hubNamespace = hubEntry.Namespace
	t.hubClient = hubEntry.Client
	return nil
}

func (t *task) getHub(ctx context.Context) error {
	t.hubId = t.externalIPAttachment.GetStatus().GetHub()
	hubEntry, err := t.r.hubCache.Get(ctx, t.hubId)
	if err != nil {
		return err
	}
	t.hubNamespace = hubEntry.Namespace
	t.hubClient = hubEntry.Client
	return nil
}

func (t *task) getKubeObject(ctx context.Context) (result *osacv1alpha1.ExternalIPAttachment, err error) {
	list := &osacv1alpha1.ExternalIPAttachmentList{}
	err = t.hubClient.List(
		ctx, list,
		clnt.InNamespace(t.hubNamespace),
		clnt.MatchingLabels{
			labels.ExternalIPAttachmentUuid: t.externalIPAttachment.GetId(),
		},
	)
	if err != nil {
		return
	}
	items := list.Items
	count := len(items)
	if count > 1 {
		err = fmt.Errorf(
			"expected at most one external IP attachment with identifier '%s' but found %d",
			t.externalIPAttachment.GetId(), count,
		)
		return
	}
	if count > 0 {
		result = &items[0]
	}
	return
}

func (t *task) addFinalizer() bool {
	if !t.externalIPAttachment.HasMetadata() {
		t.externalIPAttachment.SetMetadata(&privatev1.Metadata{})
	}
	list := t.externalIPAttachment.GetMetadata().GetFinalizers()
	if !slices.Contains(list, finalizers.Controller) {
		list = append(list, finalizers.Controller)
		t.externalIPAttachment.GetMetadata().SetFinalizers(list)
		return true
	}
	return false
}

func (t *task) removeFinalizer() {
	if !t.externalIPAttachment.HasMetadata() {
		return
	}
	list := t.externalIPAttachment.GetMetadata().GetFinalizers()
	if slices.Contains(list, finalizers.Controller) {
		list = slices.DeleteFunc(list, func(item string) bool {
			return item == finalizers.Controller
		})
		t.externalIPAttachment.GetMetadata().SetFinalizers(list)
	}
}

func (t *task) buildSpec() osacv1alpha1.ExternalIPAttachmentSpec {
	spec := osacv1alpha1.ExternalIPAttachmentSpec{
		ExternalIP: t.externalIPAttachment.GetSpec().GetExternalIp(),
	}
	if t.externalIPAttachment.GetSpec().HasComputeInstance() {
		ci := t.externalIPAttachment.GetSpec().GetComputeInstance()
		spec.ComputeInstance = &ci
	}
	return spec
}
