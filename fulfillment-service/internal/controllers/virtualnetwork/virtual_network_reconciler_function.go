/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package virtualnetwork

//go:generate mockgen -source=../../api/osac/private/v1/virtual_networks_service_grpc.pb.go -destination=virtual_networks_client_mock.go -package=virtualnetwork VirtualNetworksClient

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

	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/controllers"
	"github.com/osac-project/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/fulfillment-service/internal/kubernetes/annotations"
	"github.com/osac-project/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/fulfillment-service/internal/masks"
)

// objectPrefix is the prefix that will be used in the `generateName` field of the resources created in the hub.
const objectPrefix = "virtualnetwork-"

// FunctionBuilder contains the data and logic needed to build a function that reconciles virtual networks.
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	hubCache   controllers.HubCache
}

type function struct {
	logger                *slog.Logger
	hubCache              controllers.HubCache
	virtualNetworksClient privatev1.VirtualNetworksClient
	hubsClient            privatev1.HubsClient
	maskCalculator        *masks.Calculator
}

type task struct {
	r              *function
	virtualNetwork *privatev1.VirtualNetwork
	hubId          string
	hubNamespace   string
	hubClient      clnt.Client
}

// NewFunction creates a new builder that can then be used to create a new virtual network reconciler function.
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

// Build uses the information stored in the builder to create a new virtual network reconciler.
func (b *FunctionBuilder) Build() (result controllers.ReconcilerFunction[*privatev1.VirtualNetwork], err error) {
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
		virtualNetworksClient: privatev1.NewVirtualNetworksClient(b.connection),
		hubsClient:            privatev1.NewHubsClient(b.connection),
		hubCache:              b.hubCache,
		maskCalculator:        masks.NewCalculator().Build(),
	}
	result = object.run
	return
}

func (r *function) run(ctx context.Context, virtualNetwork *privatev1.VirtualNetwork) error {
	oldVirtualNetwork := proto.Clone(virtualNetwork).(*privatev1.VirtualNetwork)
	t := task{
		r:              r,
		virtualNetwork: virtualNetwork,
	}
	var err error
	if virtualNetwork.HasMetadata() && virtualNetwork.GetMetadata().HasDeletionTimestamp() {
		err = t.delete(ctx)
	} else {
		err = t.update(ctx)
	}
	if err != nil {
		return err
	}
	// Calculate which fields the reconciler actually modified and use a field mask
	// to update only those fields. This prevents overwriting concurrent user changes.
	updateMask := r.maskCalculator.Calculate(oldVirtualNetwork, virtualNetwork)

	// Only send an update if there are actual changes
	_, err = r.virtualNetworksClient.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
		Object:     virtualNetwork,
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
	hubJustSelected := t.virtualNetwork.GetStatus().GetHub() == ""
	if err := t.selectHub(ctx); err != nil {
		return err
	}
	t.virtualNetwork.GetStatus().SetHub(t.hubId)
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
		object := &osacv1alpha1.VirtualNetwork{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    t.hubNamespace,
				GenerateName: objectPrefix,
				Labels: map[string]string{
					labels.VirtualNetworkUuid: t.virtualNetwork.GetId(),
				},
				Annotations: map[string]string{
					annotations.Tenant: t.virtualNetwork.GetMetadata().GetTenant(),
				},
			},
			Spec: spec,
		}
		err = t.hubClient.Create(ctx, object)
		if err != nil {
			return err
		}
		t.r.logger.DebugContext(
			ctx,
			"Created virtual network",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
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
			"Updated virtual network",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	}

	return err
}

func (t *task) setDefaults() {
	if !t.virtualNetwork.HasStatus() {
		t.virtualNetwork.SetStatus(&privatev1.VirtualNetworkStatus{})
	}
	if t.virtualNetwork.GetStatus().GetState() == privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_UNSPECIFIED {
		t.virtualNetwork.GetStatus().SetState(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING)
	}
}

func (t *task) validateTenant() error {
	if !t.virtualNetwork.HasMetadata() || t.virtualNetwork.GetMetadata().GetTenant() == "" {
		return errors.New("virtual network must have a tenant assigned")
	}
	return nil
}

func (t *task) delete(ctx context.Context) (err error) {
	// Do nothing if we don't know the hub yet:
	t.hubId = t.virtualNetwork.GetStatus().GetHub()
	if t.hubId == "" {
		// No hub assigned, nothing to clean up on K8s side.
		t.removeFinalizer()
		return nil
	}
	err = t.getHub(ctx)
	if err != nil {
		// Check if the hub has been decommissioned (deleted from database)
		if errors.Is(err, controllers.ErrHubNotFound) {
			controllers.RemoveFinalizerOnDecommissionedHub(ctx, t.r.logger, t.hubId, "virtual_network_id", t.virtualNetwork.GetId(), t.removeFinalizer)
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
			"Virtual network doesn't exist",
			slog.String("id", t.virtualNetwork.GetId()),
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
			"Deleted virtual network",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	} else {
		t.r.logger.DebugContext(
			ctx,
			"Virtual network is still being deleted, waiting for K8s finalizers",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	}

	// Don't remove finalizer — K8s object still exists with finalizers being processed.
	return
}

func (t *task) selectHub(ctx context.Context) error {
	t.hubId = t.virtualNetwork.GetStatus().GetHub()
	if t.hubId == "" {
		response, err := t.r.hubsClient.List(ctx, privatev1.HubsListRequest_builder{}.Build())
		if err != nil {
			return err
		}
		if len(response.Items) == 0 {
			return errors.New("there are no hubs")
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
	t.hubId = t.virtualNetwork.GetStatus().GetHub()
	hubEntry, err := t.r.hubCache.Get(ctx, t.hubId)
	if err != nil {
		return err
	}
	t.hubNamespace = hubEntry.Namespace
	t.hubClient = hubEntry.Client
	return nil
}

func (t *task) getKubeObject(ctx context.Context) (result *osacv1alpha1.VirtualNetwork, err error) {
	list := &osacv1alpha1.VirtualNetworkList{}
	err = t.hubClient.List(
		ctx, list,
		clnt.InNamespace(t.hubNamespace),
		clnt.MatchingLabels{
			labels.VirtualNetworkUuid: t.virtualNetwork.GetId(),
		},
	)
	if err != nil {
		return
	}
	items := list.Items
	count := len(items)
	if count > 1 {
		err = fmt.Errorf(
			"expected at most one virtual network with identifier '%s' but found %d",
			t.virtualNetwork.GetId(), count,
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
	if !t.virtualNetwork.HasMetadata() {
		t.virtualNetwork.SetMetadata(&privatev1.Metadata{})
	}
	list := t.virtualNetwork.GetMetadata().GetFinalizers()
	if !slices.Contains(list, finalizers.Controller) {
		list = append(list, finalizers.Controller)
		t.virtualNetwork.GetMetadata().SetFinalizers(list)
		return true
	}
	return false
}

func (t *task) removeFinalizer() {
	if !t.virtualNetwork.HasMetadata() {
		return
	}
	list := t.virtualNetwork.GetMetadata().GetFinalizers()
	if slices.Contains(list, finalizers.Controller) {
		list = slices.DeleteFunc(list, func(item string) bool {
			return item == finalizers.Controller
		})
		t.virtualNetwork.GetMetadata().SetFinalizers(list)
	}
}

// buildSpec constructs the spec for the Kubernetes VirtualNetwork object based on the
// virtual network from the database.
func (t *task) buildSpec() osacv1alpha1.VirtualNetworkSpec {
	spec := osacv1alpha1.VirtualNetworkSpec{
		Region:                 t.virtualNetwork.GetSpec().GetRegion(),
		NetworkClass:           t.virtualNetwork.GetSpec().GetNetworkClass(),
		ImplementationStrategy: t.virtualNetwork.GetSpec().GetImplementationStrategy(),
	}

	// Add IPv4 CIDR if present:
	if t.virtualNetwork.GetSpec().HasIpv4Cidr() {
		spec.IPv4CIDR = t.virtualNetwork.GetSpec().GetIpv4Cidr()
	}

	// Add IPv6 CIDR if present:
	if t.virtualNetwork.GetSpec().HasIpv6Cidr() {
		spec.IPv6CIDR = t.virtualNetwork.GetSpec().GetIpv6Cidr()
	}

	return spec
}
