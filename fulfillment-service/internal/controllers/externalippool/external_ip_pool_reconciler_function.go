/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package externalippool

//go:generate mockgen -source=../../api/osac/private/v1/external_ip_pools_service_grpc.pb.go -destination=external_ip_pools_client_mock.go -package=externalippool ExternalIPPoolsClient

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

const (
	objectPrefix = "externalippool-"
)

var (
	errUnsupportedIPFamily     = errors.New("unsupported or unspecified IP family")
	errInvalidTenantCount      = errors.New("external IP pool must have a tenant assigned")
	errDuplicateExternalIPPool = errors.New("expected at most one external IP pool with identifier")
	errNoHubsFound             = errors.New("no available hubs found")

	// Build() errors:
	errNoLogger     = errors.New("logger is mandatory")
	errNoConnection = errors.New("client connection is mandatory")
	errNoHubCache   = errors.New("hub cache is mandatory")
)

// FunctionBuilder contains the data and logic needed to build a function that reconciles external IP pools.
type FunctionBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	hubCache   controllers.HubCache
}

type function struct {
	logger                *slog.Logger
	hubCache              controllers.HubCache
	externalIPPoolsClient privatev1.ExternalIPPoolsClient
	hubsClient            privatev1.HubsClient
	maskCalculator        *masks.Calculator
}

type task struct {
	r              *function
	externalIPPool *privatev1.ExternalIPPool
	hubId          string
	hubNamespace   string
	hubClient      clnt.Client
}

// NewFunction creates a new builder that can then be used to create a new external IP pool reconciler function.
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

// Build uses the information stored in the builder to create a new external IP pool reconciler.
func (b *FunctionBuilder) Build() (controllers.ReconcilerFunction[*privatev1.ExternalIPPool], error) {
	if b.logger == nil {
		return nil, errNoLogger
	}
	if b.connection == nil {
		return nil, errNoConnection
	}
	if b.hubCache == nil {
		return nil, errNoHubCache
	}

	object := &function{
		logger:                b.logger,
		externalIPPoolsClient: privatev1.NewExternalIPPoolsClient(b.connection),
		hubsClient:            privatev1.NewHubsClient(b.connection),
		hubCache:              b.hubCache,
		maskCalculator:        masks.NewCalculator().Build(),
	}
	result := object.run
	return result, nil
}

func (r *function) run(ctx context.Context, externalIPPool *privatev1.ExternalIPPool) error {
	oldPool := proto.Clone(externalIPPool).(*privatev1.ExternalIPPool)
	t := task{
		r:              r,
		externalIPPool: externalIPPool,
	}
	var err error
	if externalIPPool.HasMetadata() && externalIPPool.GetMetadata().HasDeletionTimestamp() {
		err = t.delete(ctx)
	} else {
		err = t.update(ctx)
	}
	if err != nil {
		return err
	}
	updateMask := r.maskCalculator.Calculate(oldPool, externalIPPool)

	_, err = r.externalIPPoolsClient.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
		Object:     externalIPPool,
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

	if err := t.validateIPFamily(); err != nil {
		return err
	}

	// Select the hub and return immediately if it was just selected. This ensures the hub is
	// persisted before any Kubernetes objects are created.
	hubJustSelected := t.externalIPPool.GetStatus().GetHub() == ""
	if err := t.selectHub(ctx); err != nil {
		return err
	}
	t.externalIPPool.GetStatus().SetHub(t.hubId)
	if hubJustSelected {
		return nil
	}

	existingObject, err := t.getKubeObject(ctx)
	if err != nil {
		return err
	}

	spec := t.buildSpec()

	if existingObject == nil {
		object := &osacv1alpha1.ExternalIPPool{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    t.hubNamespace,
				GenerateName: objectPrefix,
				Labels: map[string]string{
					labels.ExternalIPPoolUuid: t.externalIPPool.GetId(),
				},
				Annotations: map[string]string{
					annotations.Tenant: t.externalIPPool.GetMetadata().GetTenant(),
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
			"Created external IP pool",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	} else {
		update := existingObject.DeepCopy()
		update.Spec = spec
		err = t.hubClient.Patch(ctx, update, clnt.MergeFrom(existingObject))
		if err != nil {
			return err
		}
		t.r.logger.DebugContext(
			ctx,
			"Updated external IP pool",
			slog.String("namespace", existingObject.GetNamespace()),
			slog.String("name", existingObject.GetName()),
		)
	}

	return nil
}

func (t *task) setDefaults() {
	if !t.externalIPPool.HasStatus() {
		t.externalIPPool.SetStatus(&privatev1.ExternalIPPoolStatus{})
	}
	if t.externalIPPool.GetStatus().GetState() == privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_UNSPECIFIED {
		t.externalIPPool.GetStatus().SetState(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING)
	}
}

func (t *task) validateTenant() error {
	if !t.externalIPPool.HasMetadata() || t.externalIPPool.GetMetadata().GetTenant() == "" {
		return errInvalidTenantCount
	}
	return nil
}

func (t *task) validateIPFamily() error {
	ipFamily := t.externalIPPool.GetSpec().GetIpFamily()
	switch ipFamily {
	case privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.IPFamily_IP_FAMILY_IPV6:
		return nil
	default:
		return fmt.Errorf("%w: %v", errUnsupportedIPFamily, ipFamily)
	}
}

func (t *task) delete(ctx context.Context) error {
	t.hubId = t.externalIPPool.GetStatus().GetHub()
	if t.hubId == "" {
		t.removeFinalizer()
		return nil
	}
	err := t.getHub(ctx)
	if err != nil {
		// Check if the hub has been decommissioned (deleted from database)
		if errors.Is(err, controllers.ErrHubNotFound) {
			controllers.RemoveFinalizerOnDecommissionedHub(ctx, t.r.logger, t.hubId, "external_ip_pool_id", t.externalIPPool.GetId(), t.removeFinalizer)
			return nil
		}
		// For transient errors (network, timeout, etc.), continue retrying
		return err
	}

	object, err := t.getKubeObject(ctx)
	if err != nil {
		return err
	}
	if object == nil {
		t.r.logger.DebugContext(
			ctx,
			"External IP pool doesn't exist",
			slog.String("id", t.externalIPPool.GetId()),
		)
		t.removeFinalizer()
		return nil
	}

	if object.GetDeletionTimestamp() == nil {
		err = t.hubClient.Delete(ctx, object)
		if err != nil {
			return err
		}
		t.r.logger.DebugContext(
			ctx,
			"Deleted external IP pool",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	} else {
		t.r.logger.DebugContext(
			ctx,
			"External IP pool is still being deleted, waiting for K8s finalizers",
			slog.String("namespace", object.GetNamespace()),
			slog.String("name", object.GetName()),
		)
	}

	return nil
}

func (t *task) selectHub(ctx context.Context) error {
	t.hubId = t.externalIPPool.GetStatus().GetHub()
	if t.hubId == "" {
		response, err := t.r.hubsClient.List(ctx, privatev1.HubsListRequest_builder{}.Build())
		if err != nil {
			return err
		}
		if len(response.Items) == 0 {
			return errNoHubsFound
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
	t.hubId = t.externalIPPool.GetStatus().GetHub()
	hubEntry, err := t.r.hubCache.Get(ctx, t.hubId)
	if err != nil {
		return err
	}
	t.hubNamespace = hubEntry.Namespace
	t.hubClient = hubEntry.Client
	return nil
}

func (t *task) getKubeObject(ctx context.Context) (*osacv1alpha1.ExternalIPPool, error) {
	list := &osacv1alpha1.ExternalIPPoolList{}
	err := t.hubClient.List(
		ctx, list,
		clnt.InNamespace(t.hubNamespace),
		clnt.MatchingLabels{
			labels.ExternalIPPoolUuid: t.externalIPPool.GetId(),
		},
	)
	if err != nil {
		return nil, err
	}
	items := list.Items
	count := len(items)
	if count > 1 {
		return nil, fmt.Errorf("%w: found %d", errDuplicateExternalIPPool, count)
	}
	var result *osacv1alpha1.ExternalIPPool
	if count > 0 {
		result = &items[0]
	}
	return result, nil
}

func (t *task) addFinalizer() bool {
	if !t.externalIPPool.HasMetadata() {
		t.externalIPPool.SetMetadata(&privatev1.Metadata{})
	}
	list := t.externalIPPool.GetMetadata().GetFinalizers()
	if !slices.Contains(list, finalizers.Controller) {
		list = append(list, finalizers.Controller)
		t.externalIPPool.GetMetadata().SetFinalizers(list)
		return true
	}
	return false
}

func (t *task) removeFinalizer() {
	if !t.externalIPPool.HasMetadata() {
		return
	}
	list := t.externalIPPool.GetMetadata().GetFinalizers()
	if slices.Contains(list, finalizers.Controller) {
		list = slices.DeleteFunc(list, func(item string) bool {
			return item == finalizers.Controller
		})
		t.externalIPPool.GetMetadata().SetFinalizers(list)
	}
}

func (t *task) buildSpec() osacv1alpha1.ExternalIPPoolSpec {
	spec := osacv1alpha1.ExternalIPPoolSpec{
		CIDRs: t.externalIPPool.GetSpec().GetCidrs(),
	}

	switch t.externalIPPool.GetSpec().GetIpFamily() {
	case privatev1.IPFamily_IP_FAMILY_IPV4:
		spec.IPFamily = "IPv4"
	case privatev1.IPFamily_IP_FAMILY_IPV6:
		spec.IPFamily = "IPv6"
	}

	implStrategy := t.externalIPPool.GetSpec().GetImplementationStrategy()
	if implStrategy != "" {
		spec.ImplementationStrategy = implStrategy
	}

	return spec
}
