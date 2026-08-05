/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package natgateway

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

const objectPrefix = "natgateway-"

// FunctionBuilder contains the data and logic needed to build a function that reconciles NAT gateways.
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	hubCache   controllers.HubCache
}

type function struct {
	logger                *slog.Logger
	hubCache              controllers.HubCache
	natGatewaysClient     privatev1.NATGatewaysClient
	virtualNetworksClient privatev1.VirtualNetworksClient
	maskCalculator        *masks.Calculator
}

type task struct {
	r            *function
	natGateway   *privatev1.NATGateway
	hubId        string
	hubNamespace string
	hubClient    clnt.Client
}

// NewFunction creates a new builder that can then be used to create a new NAT gateway reconciler function.
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

// Build uses the information stored in the builder to create a new NAT gateway reconciler function.
func (b *FunctionBuilder) Build() (result controllers.ReconcilerFunction[*privatev1.NATGateway], err error) {
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
		logger:                b.logger,
		natGatewaysClient:     privatev1.NewNATGatewaysClient(b.connection),
		virtualNetworksClient: privatev1.NewVirtualNetworksClient(b.connection),
		hubCache:              b.hubCache,
		maskCalculator:        masks.NewCalculator().Build(),
	}
	result = object.run
	return
}

func (r *function) run(ctx context.Context, natGateway *privatev1.NATGateway) error {
	oldGateway := proto.Clone(natGateway).(*privatev1.NATGateway)
	t := task{
		r:          r,
		natGateway: natGateway,
	}
	var err error
	if natGateway.HasMetadata() && natGateway.GetMetadata().HasDeletionTimestamp() {
		err = t.delete(ctx)
	} else {
		err = t.update(ctx)
	}
	if err != nil {
		return err
	}
	updateMask := r.maskCalculator.Calculate(oldGateway, natGateway)
	if len(updateMask.GetPaths()) == 0 {
		return nil
	}

	_, err = r.natGatewaysClient.Update(ctx, privatev1.NATGatewaysUpdateRequest_builder{
		Object:     natGateway,
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

	t.natGateway.GetStatus().SetHub(t.hubId)

	object, err := t.getKubeObject(ctx)
	if err != nil {
		return err
	}

	spec := t.buildSpec()

	if object == nil {
		newObject := &osacv1alpha1.NATGateway{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    t.hubNamespace,
				GenerateName: objectPrefix,
				Labels: map[string]string{
					labels.NATGatewayUuid: t.natGateway.GetId(),
				},
				Annotations: map[string]string{
					annotations.Tenant: t.natGateway.GetMetadata().GetTenant(),
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
			"Created NAT gateway",
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
			"Updated NAT gateway",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	}

	return err
}

func (t *task) setDefaults() {
	if !t.natGateway.HasStatus() {
		t.natGateway.SetStatus(&privatev1.NATGatewayStatus{})
	}
	if t.natGateway.GetStatus().GetState() == privatev1.NATGatewayState_NAT_GATEWAY_STATE_UNSPECIFIED {
		t.natGateway.GetStatus().SetState(privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING)
	}
}

func (t *task) validateTenant() error {
	if !t.natGateway.HasMetadata() || t.natGateway.GetMetadata().GetTenant() == "" {
		return errors.New("NAT gateway must have a tenant assigned")
	}
	return nil
}

func (t *task) delete(ctx context.Context) (err error) {
	t.hubId = t.natGateway.GetStatus().GetHub()
	if t.hubId == "" {
		t.removeFinalizer()
		return nil
	}
	err = t.getHub(ctx)
	if err != nil {
		if errors.Is(err, controllers.ErrHubNotFound) {
			controllers.RemoveFinalizerOnDecommissionedHub(ctx, t.r.logger, t.hubId, "nat_gateway_id", t.natGateway.GetId(), t.removeFinalizer)
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
			"NAT gateway doesn't exist",
			slog.String("id", t.natGateway.GetId()),
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
			"Deleted NAT gateway",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	} else {
		t.r.logger.DebugContext(
			ctx,
			"NAT gateway is still being deleted, waiting for K8s finalizers",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	}

	return
}

func (t *task) selectHub(ctx context.Context) error {
	t.hubId = t.natGateway.GetStatus().GetHub()
	if t.hubId == "" {
		vnResponse, err := t.r.virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
			Id: t.natGateway.GetSpec().GetVirtualNetwork(),
		}.Build())
		if err != nil {
			return err
		}
		vnHub := vnResponse.GetObject().GetStatus().GetHub()
		if vnHub == "" {
			return fmt.Errorf(
				"virtual network %s has no hub assigned yet, skipping",
				t.natGateway.GetSpec().GetVirtualNetwork(),
			)
		}
		t.hubId = vnHub
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
	t.hubId = t.natGateway.GetStatus().GetHub()
	hubEntry, err := t.r.hubCache.Get(ctx, t.hubId)
	if err != nil {
		return err
	}
	t.hubNamespace = hubEntry.Namespace
	t.hubClient = hubEntry.Client
	return nil
}

func (t *task) getKubeObject(ctx context.Context) (result *osacv1alpha1.NATGateway, err error) {
	list := &osacv1alpha1.NATGatewayList{}
	err = t.hubClient.List(
		ctx, list,
		clnt.InNamespace(t.hubNamespace),
		clnt.MatchingLabels{
			labels.NATGatewayUuid: t.natGateway.GetId(),
		},
	)
	if err != nil {
		return
	}
	items := list.Items
	count := len(items)
	if count > 1 {
		err = fmt.Errorf(
			"expected at most one NAT gateway with identifier '%s' but found %d",
			t.natGateway.GetId(), count,
		)
		return
	}
	if count > 0 {
		result = &items[0]
	}
	return
}

func (t *task) addFinalizer() bool {
	if !t.natGateway.HasMetadata() {
		t.natGateway.SetMetadata(&privatev1.Metadata{})
	}
	list := t.natGateway.GetMetadata().GetFinalizers()
	if !slices.Contains(list, finalizers.Controller) {
		list = append(list, finalizers.Controller)
		t.natGateway.GetMetadata().SetFinalizers(list)
		return true
	}
	return false
}

func (t *task) removeFinalizer() {
	if !t.natGateway.HasMetadata() {
		return
	}
	list := t.natGateway.GetMetadata().GetFinalizers()
	if slices.Contains(list, finalizers.Controller) {
		list = slices.DeleteFunc(list, func(item string) bool {
			return item == finalizers.Controller
		})
		t.natGateway.GetMetadata().SetFinalizers(list)
	}
}

func (t *task) buildSpec() osacv1alpha1.NATGatewaySpec {
	return osacv1alpha1.NATGatewaySpec{
		VirtualNetwork: t.natGateway.GetSpec().GetVirtualNetwork(),
		ExternalIP:     t.natGateway.GetSpec().GetExternalIp(),
	}
}
