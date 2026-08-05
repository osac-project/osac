/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package externalip

//go:generate mockgen -source=../../api/osac/private/v1/external_ips_service_grpc.pb.go -destination=external_ips_client_mock.go -package=externalip ExternalIPsClient

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

// objectPrefix is the prefix that will be used in the `generateName` field of the resources created in the hub.
const objectPrefix = "externalip-"

// FunctionBuilder contains the data and logic needed to build a function that reconciles external IPs.
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	hubCache   controllers.HubCache
}

type function struct {
	logger                *slog.Logger
	hubCache              controllers.HubCache
	externalIPsClient     privatev1.ExternalIPsClient
	externalIPPoolsClient privatev1.ExternalIPPoolsClient
	maskCalculator        *masks.Calculator
}

type task struct {
	r            *function
	externalIP   *privatev1.ExternalIP
	hubId        string
	hubNamespace string
	hubClient    clnt.Client
}

// NewFunction creates a new builder that can then be used to create a new external IP reconciler function.
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

// Build uses the information stored in the builder to create a new external IP reconciler.
func (b *FunctionBuilder) Build() (result controllers.ReconcilerFunction[*privatev1.ExternalIP], err error) {
	// Check parameters:
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

	// Create and populate the object:
	object := &function{
		logger:                b.logger,
		externalIPsClient:     privatev1.NewExternalIPsClient(b.connection),
		externalIPPoolsClient: privatev1.NewExternalIPPoolsClient(b.connection),
		hubCache:              b.hubCache,
		maskCalculator:        masks.NewCalculator().Build(),
	}
	result = object.run
	return
}

func (r *function) run(ctx context.Context, externalIP *privatev1.ExternalIP) error {
	oldExternalIP := proto.Clone(externalIP).(*privatev1.ExternalIP)
	t := task{
		r:          r,
		externalIP: externalIP,
	}
	var err error
	if externalIP.HasMetadata() && externalIP.GetMetadata().HasDeletionTimestamp() {
		err = t.delete(ctx)
	} else {
		err = t.update(ctx)
	}
	if err != nil {
		return err
	}
	updateMask := r.maskCalculator.Calculate(oldExternalIP, externalIP)
	if len(updateMask.GetPaths()) == 0 {
		return nil
	}

	_, err = r.externalIPsClient.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
		Object:     externalIP,
		UpdateMask: updateMask,
	}.Build())

	return err
}

func (t *task) update(ctx context.Context) error {
	// Add the finalizer and return immediately if it was added. This ensures the finalizer is persisted before any
	// other work is done, reducing the chance of the object being deleted before the finalizer is saved.
	if t.addFinalizer() {
		return nil
	}

	// Set the default values:
	t.setDefaults()

	// Validate that exactly one tenant is assigned:
	if err := t.validateTenant(); err != nil {
		return err
	}

	// Select the hub and return immediately if it was just selected. This ensures the hub is
	// persisted before any Kubernetes objects are created.
	hubJustSelected := t.externalIP.GetStatus().GetHub() == ""
	if err := t.selectHub(ctx); err != nil {
		return err
	}
	t.externalIP.GetStatus().SetHub(t.hubId)
	if hubJustSelected {
		return nil
	}

	// Get the K8S object:
	object, err := t.getKubeObject(ctx)
	if err != nil {
		return err
	}

	// Prepare the changes to the spec:
	spec := t.buildSpec()

	// Create or update the Kubernetes object:
	if object == nil {
		newObject := &osacv1alpha1.ExternalIP{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    t.hubNamespace,
				GenerateName: objectPrefix,
				Labels: map[string]string{
					labels.ExternalIPUuid: t.externalIP.GetId(),
				},
				Annotations: map[string]string{
					annotations.Tenant: t.externalIP.GetMetadata().GetTenant(),
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
			"Created external IP",
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
			"Updated external IP",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	}

	return err
}

func (t *task) setDefaults() {
	if !t.externalIP.HasStatus() {
		t.externalIP.SetStatus(&privatev1.ExternalIPStatus{})
	}
	if t.externalIP.GetStatus().GetState() == privatev1.ExternalIPState_EXTERNAL_IP_STATE_UNSPECIFIED {
		t.externalIP.GetStatus().SetState(privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING)
	}
}

func (t *task) validateTenant() error {
	if !t.externalIP.HasMetadata() || t.externalIP.GetMetadata().GetTenant() == "" {
		return errors.New("external IP must have a tenant assigned")
	}
	return nil
}

func (t *task) delete(ctx context.Context) (err error) {
	// Do nothing if we don't know the hub yet:
	t.hubId = t.externalIP.GetStatus().GetHub()
	if t.hubId == "" {
		// No hub assigned, nothing to clean up on K8s side.
		t.removeFinalizer()
		return nil
	}
	err = t.getHub(ctx)
	if err != nil {
		// Check if the hub has been decommissioned (deleted from database)
		if errors.Is(err, controllers.ErrHubNotFound) {
			controllers.RemoveFinalizerOnDecommissionedHub(ctx, t.r.logger, t.hubId, "external_ip_id", t.externalIP.GetId(), t.removeFinalizer)
			return nil
		}
		// For transient errors (network, timeout, etc.), continue retrying
		return
	}

	// Check if the K8S object still exists:
	object, err := t.getKubeObject(ctx)
	if err != nil {
		return
	}
	if object == nil {
		// K8s object is fully gone (all K8s finalizers processed).
		// Safe to remove our DB finalizer and allow archiving.
		t.r.logger.DebugContext(
			ctx,
			"External IP doesn't exist",
			slog.String("id", t.externalIP.GetId()),
		)
		t.removeFinalizer()
		return
	}

	// Initiate K8s deletion if not already in progress:
	if object.GetDeletionTimestamp() == nil {
		err = t.hubClient.Delete(ctx, object)
		if err != nil {
			return
		}
		t.r.logger.DebugContext(
			ctx,
			"Deleted external IP",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	} else {
		t.r.logger.DebugContext(
			ctx,
			"External IP is still being deleted, waiting for K8s finalizers",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	}

	// Don't remove finalizer: K8s object still exists with finalizers being processed.
	return
}

// selectHub derives the hub from the parent ExternalIPPool instead of random selection.
// If the external IP already has a hub assigned, it uses that directly.
// If the pool has no hub yet (not reconciled), the external IP is skipped and retried next cycle.
func (t *task) selectHub(ctx context.Context) error {
	t.hubId = t.externalIP.GetStatus().GetHub()
	if t.hubId == "" {
		// Look up the parent pool to derive the hub:
		poolResponse, err := t.r.externalIPPoolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
			Id: t.externalIP.GetSpec().GetPool(),
		}.Build())
		if err != nil {
			return err
		}
		poolHub := poolResponse.GetObject().GetStatus().GetHub()
		if poolHub == "" {
			return fmt.Errorf(
				"pool %s has no hub assigned yet, skipping",
				t.externalIP.GetSpec().GetPool(),
			)
		}
		t.hubId = poolHub
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
	t.hubId = t.externalIP.GetStatus().GetHub()
	hubEntry, err := t.r.hubCache.Get(ctx, t.hubId)
	if err != nil {
		return err
	}
	t.hubNamespace = hubEntry.Namespace
	t.hubClient = hubEntry.Client
	return nil
}

func (t *task) getKubeObject(ctx context.Context) (result *osacv1alpha1.ExternalIP, err error) {
	list := &osacv1alpha1.ExternalIPList{}
	err = t.hubClient.List(
		ctx, list,
		clnt.InNamespace(t.hubNamespace),
		clnt.MatchingLabels{
			labels.ExternalIPUuid: t.externalIP.GetId(),
		},
	)
	if err != nil {
		return
	}
	items := list.Items
	count := len(items)
	if count > 1 {
		err = fmt.Errorf(
			"expected at most one external IP with identifier '%s' but found %d",
			t.externalIP.GetId(), count,
		)
		return
	}
	if count > 0 {
		result = &items[0]
	}
	return
}

// addFinalizer adds the controller finalizer if it is not already present. Returns true if the finalizer was added,
// false if it was already present.
func (t *task) addFinalizer() bool {
	if !t.externalIP.HasMetadata() {
		t.externalIP.SetMetadata(&privatev1.Metadata{})
	}
	list := t.externalIP.GetMetadata().GetFinalizers()
	if !slices.Contains(list, finalizers.Controller) {
		list = append(list, finalizers.Controller)
		t.externalIP.GetMetadata().SetFinalizers(list)
		return true
	}
	return false
}

func (t *task) removeFinalizer() {
	if !t.externalIP.HasMetadata() {
		return
	}
	list := t.externalIP.GetMetadata().GetFinalizers()
	if slices.Contains(list, finalizers.Controller) {
		list = slices.DeleteFunc(list, func(item string) bool {
			return item == finalizers.Controller
		})
		t.externalIP.GetMetadata().SetFinalizers(list)
	}
}

// buildSpec constructs the spec map for the Kubernetes ExternalIP object based on the
// external IP from the database. Only spec fields are pushed to the CRD; status fields
// (address, state) originate from the operator side and flow K8s -> fulfillment-service.
func (t *task) buildSpec() osacv1alpha1.ExternalIPSpec {
	spec := osacv1alpha1.ExternalIPSpec{
		Pool: t.externalIP.GetSpec().GetPool(),
	}
	return spec
}
