/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package volume

//go:generate mockgen -source=../../api/osac/private/v1/volumes_service_grpc.pb.go -destination=volumes_client_mock.go -package=volume VolumesClient
//go:generate mockgen -source=../../api/osac/private/v1/hubs_service_grpc.pb.go -destination=hubs_client_mock.go -package=volume HubsClient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/annotations"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
)

const objectPrefix = "vol-"

// FunctionBuilder contains the data and logic needed to build a function that
// reconciles volumes. It follows the same builder pattern as other reconciler
// functions (NATGateway, ComputeInstance, etc.).
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	hubCache   controllers.HubCache
}

// function holds long-lived clients shared across all reconcile invocations.
type function struct {
	logger         *slog.Logger
	hubCache       controllers.HubCache
	volumesClient  privatev1.VolumesClient
	hubsClient     privatev1.HubsClient
	maskCalculator *masks.Calculator
}

// task carries per-reconciliation state for a single Volume object.
type task struct {
	r            *function
	volume       *privatev1.Volume
	hubId        string
	hubNamespace string
	hubClient    clnt.Client
}

// NewFunction creates a new builder for the volume reconciler function.
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

// Build uses the information stored in the builder to create a new volume
// reconciler function. The returned function maps fulfillment-service Volume
// proto objects to Volume CRs on a hub cluster.
func (b *FunctionBuilder) Build() (result controllers.ReconcilerFunction[*privatev1.Volume], err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.connection == nil {
		err = errors.New("connection is mandatory")
		return
	}
	if b.hubCache == nil {
		err = errors.New("hub cache is mandatory")
		return
	}

	object := &function{
		logger:         b.logger,
		volumesClient:  privatev1.NewVolumesClient(b.connection),
		hubsClient:     privatev1.NewHubsClient(b.connection),
		hubCache:       b.hubCache,
		maskCalculator: masks.NewCalculator().Build(),
	}
	result = object.run
	return
}

// run is the ReconcilerFunction entry point. It clones the proto object before
// reconciling, then diffs before/after to compute a FieldMask for a targeted
// gRPC Update that avoids overwriting concurrent changes.
func (r *function) run(ctx context.Context, volume *privatev1.Volume) error {
	oldVolume := proto.Clone(volume).(*privatev1.Volume)
	t := task{
		r:      r,
		volume: volume,
	}
	var err error
	if volume.HasMetadata() && volume.GetMetadata().HasDeletionTimestamp() {
		err = t.delete(ctx)
	} else {
		err = t.update(ctx)
	}
	if err != nil {
		return err
	}
	updateMask := r.maskCalculator.Calculate(oldVolume, volume)
	if len(updateMask.GetPaths()) == 0 {
		return nil
	}

	_, err = r.volumesClient.Update(ctx, privatev1.VolumesUpdateRequest_builder{
		Object:     volume,
		UpdateMask: updateMask,
	}.Build())
	return err
}

// update handles the non-delete path: adds the controller finalizer, sets
// default state, validates the tenant, selects a hub, and creates or patches
// the Volume CR on the hub cluster.
func (t *task) update(ctx context.Context) error {
	if t.addFinalizer() {
		return nil
	}

	t.setDefaults()

	if err := t.validateTenant(); err != nil {
		t.setFailed(err)
		return nil
	}

	hubJustSelected := t.volume.GetStatus().GetHub() == ""
	if err := t.selectHub(ctx); err != nil {
		return err
	}
	t.volume.GetStatus().SetHub(t.hubId)
	if hubJustSelected {
		return nil
	}

	object, err := t.getKubeObject(ctx)
	if err != nil {
		return err
	}

	spec := t.buildSpec()

	if object == nil {
		newObject := &osacv1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    t.hubNamespace,
				GenerateName: objectPrefix,
				Labels: map[string]string{
					labels.VolumeUuid: t.volume.GetId(),
				},
				Annotations: map[string]string{
					annotations.Tenant: t.volume.GetMetadata().GetTenant(),
				},
			},
			Spec: spec,
		}
		err = t.hubClient.Create(ctx, newObject)
		if err != nil {
			return controllers.HandleK8sWriteError(ctx, t.r.logger, err, t.setFailed)
		}
		t.r.logger.DebugContext(
			ctx,
			"Created volume",
			slog.String("namespace", newObject.GetNamespace()),
			slog.String("name", newObject.GetName()),
		)
	} else {
		update := object.DeepCopy()
		update.Spec = spec
		err = t.hubClient.Patch(ctx, update, clnt.MergeFrom(object))
		if err != nil {
			return controllers.HandleK8sWriteError(ctx, t.r.logger, err, t.setFailed)
		}
		t.r.logger.DebugContext(
			ctx,
			"Updated volume",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	}

	return nil
}

func (t *task) setDefaults() {
	if !t.volume.HasStatus() {
		t.volume.SetStatus(&privatev1.VolumeStatus{})
	}
	if t.volume.GetStatus().GetState() == privatev1.VolumeState_VOLUME_STATE_UNSPECIFIED {
		t.volume.GetStatus().SetState(privatev1.VolumeState_VOLUME_STATE_CREATING)
	}
}

func (t *task) validateTenant() error {
	if !t.volume.HasMetadata() || t.volume.GetMetadata().GetTenant() == "" {
		return errors.New("volume must have a tenant assigned")
	}
	return nil
}

// delete handles the deletion path: looks up the hub, finds the Volume CR,
// and deletes it. Removes the controller finalizer when the CR is gone or
// when the hub has been decommissioned.
func (t *task) delete(ctx context.Context) (err error) {
	t.hubId = t.volume.GetStatus().GetHub()
	if t.hubId == "" {
		t.removeFinalizer()
		return nil
	}
	err = t.getHub(ctx)
	if err != nil {
		if errors.Is(err, controllers.ErrHubNotFound) {
			controllers.RemoveFinalizerOnDecommissionedHub(ctx, t.r.logger, t.hubId, "volume_id", t.volume.GetId(), t.removeFinalizer)
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
			"Volume doesn't exist",
			slog.String("id", t.volume.GetId()),
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
			"Deleted volume",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	} else {
		t.r.logger.DebugContext(
			ctx,
			"Volume is still being deleted, waiting for K8s finalizers",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	}

	return
}

// selectHub assigns a hub to the volume. Unlike resources that inherit their
// hub from a parent (e.g. NATGateway inherits from VirtualNetwork), volumes
// are independent, so the reconciler picks a hub randomly from the available
// hubs (same as ComputeInstance).
func (t *task) selectHub(ctx context.Context) error {
	t.hubId = t.volume.GetStatus().GetHub()
	if t.hubId == "" {
		response, err := t.r.hubsClient.List(ctx, privatev1.HubsListRequest_builder{}.Build())
		if err != nil {
			return err
		}
		if response == nil || len(response.Items) == 0 {
			return errors.New("no hubs available")
		}
		t.hubId = response.Items[rand.IntN(len(response.Items))].GetId()
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
	t.hubId = t.volume.GetStatus().GetHub()
	hubEntry, err := t.r.hubCache.Get(ctx, t.hubId)
	if err != nil {
		return err
	}
	t.hubNamespace = hubEntry.Namespace
	t.hubClient = hubEntry.Client
	return nil
}

// getKubeObject finds the Volume CR on the hub cluster by the UUID label.
// Returns nil if no CR exists yet (first reconcile).
func (t *task) getKubeObject(ctx context.Context) (result *osacv1alpha1.Volume, err error) {
	list := &osacv1alpha1.VolumeList{}
	err = t.hubClient.List(
		ctx, list,
		clnt.InNamespace(t.hubNamespace),
		clnt.MatchingLabels{
			labels.VolumeUuid: t.volume.GetId(),
		},
	)
	if err != nil {
		return
	}
	items := list.Items
	count := len(items)
	if count > 1 {
		err = fmt.Errorf(
			"expected at most one volume with identifier '%s' but found %d",
			t.volume.GetId(), count,
		)
		return
	}
	if count > 0 {
		result = &items[0]
	}
	return
}

func (t *task) addFinalizer() bool {
	if !t.volume.HasMetadata() {
		t.volume.SetMetadata(&privatev1.Metadata{})
	}
	list := t.volume.GetMetadata().GetFinalizers()
	if !slices.Contains(list, finalizers.Controller) {
		list = append(list, finalizers.Controller)
		t.volume.GetMetadata().SetFinalizers(list)
		return true
	}
	return false
}

func (t *task) removeFinalizer() {
	if !t.volume.HasMetadata() {
		return
	}
	list := t.volume.GetMetadata().GetFinalizers()
	if slices.Contains(list, finalizers.Controller) {
		list = slices.DeleteFunc(list, func(item string) bool {
			return item == finalizers.Controller
		})
		t.volume.GetMetadata().SetFinalizers(list)
	}
}

func (t *task) setFailed(err error) {
	if !t.volume.HasStatus() {
		t.volume.SetStatus(&privatev1.VolumeStatus{})
	}
	t.volume.GetStatus().SetState(privatev1.VolumeState_VOLUME_STATE_FAILED)
	t.volume.GetStatus().SetMessage(err.Error())
}

// buildSpec maps the proto VolumeSpec to the osac-operator CRD VolumeSpec.
// Access mode is converted from the proto enum to the CRD typed string.
func (t *task) buildSpec() osacv1alpha1.VolumeSpec {
	return osacv1alpha1.VolumeSpec{
		StorageTier: t.volume.GetSpec().GetStorageTier(),
		SizeGiB:     t.volume.GetSpec().GetSizeGib(),
		AccessMode:  protoAccessModeToCRD(t.volume.GetSpec().GetAccessMode()),
	}
}

// protoAccessModeToCRD converts the proto VolumeAccessMode enum to the CRD
// VolumeAccessMode typed string.
func protoAccessModeToCRD(mode privatev1.VolumeAccessMode) osacv1alpha1.VolumeAccessMode {
	switch mode {
	case privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE:
		return osacv1alpha1.VolumeAccessModeReadWriteOnce
	case privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_ONLY_MANY:
		return osacv1alpha1.VolumeAccessModeReadOnlyMany
	case privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_MANY:
		return osacv1alpha1.VolumeAccessModeReadWriteMany
	case privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE_POD:
		return osacv1alpha1.VolumeAccessModeReadWriteOncePod
	default:
		return osacv1alpha1.VolumeAccessModeReadWriteOnce
	}
}
