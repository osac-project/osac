/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"
	"github.com/osac-project/osac/fulfillment-service/internal/gitops"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"k8s.io/client-go/dynamic"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const (
	defaultSubscriptionNamespace = "models-as-a-service"
	subscriptionPolicySuffix     = "-policy"
	subscriptionMaxIDLength      = 63 - len(subscriptionPolicySuffix)
)

var _ publicv1.SubscriptionsServer = (*SubscriptionsServer)(nil)

// SubscriptionsServer manages MaaS entitlements in the AI Grid catalog Git
// repository. Every subscription file contains both the MaaSSubscription and
// its managed MaaSAuthPolicy, so GitOps applies and prunes them together.
type SubscriptionsServer struct {
	publicv1.UnimplementedSubscriptionsServer
	logger            *slog.Logger
	hubsServer        privatev1.HubsServer
	tenantsServer     privatev1.TenantsServer
	hubClientProvider HubClientProvider
	writer            gitops.Writer
	namespace         string
}

type SubscriptionsServerBuilder struct {
	logger            *slog.Logger
	hubsServer        privatev1.HubsServer
	tenantsServer     privatev1.TenantsServer
	hubClientProvider HubClientProvider
	writer            gitops.Writer
	namespace         string
}

func NewSubscriptionsServer() *SubscriptionsServerBuilder {
	return &SubscriptionsServerBuilder{}
}

func (b *SubscriptionsServerBuilder) SetLogger(value *slog.Logger) *SubscriptionsServerBuilder {
	b.logger = value
	return b
}

func (b *SubscriptionsServerBuilder) SetHubsServer(value privatev1.HubsServer) *SubscriptionsServerBuilder {
	b.hubsServer = value
	return b
}

func (b *SubscriptionsServerBuilder) SetTenantsServer(value privatev1.TenantsServer) *SubscriptionsServerBuilder {
	b.tenantsServer = value
	return b
}

func (b *SubscriptionsServerBuilder) SetHubClientProvider(value HubClientProvider) *SubscriptionsServerBuilder {
	b.hubClientProvider = value
	return b
}

func (b *SubscriptionsServerBuilder) SetWriter(value gitops.Writer) *SubscriptionsServerBuilder {
	b.writer = value
	return b
}

func (b *SubscriptionsServerBuilder) SetNamespace(value string) *SubscriptionsServerBuilder {
	b.namespace = value
	return b
}

func (b *SubscriptionsServerBuilder) Build() (*SubscriptionsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.tenantsServer == nil {
		return nil, errors.New("tenants server is mandatory")
	}
	writer := b.writer
	if writer == nil {
		var err error
		writer, err = newModelsGitWriterFromEnv()
		if err != nil {
			return nil, fmt.Errorf("create subscriptions Git writer: %w", err)
		}
	}
	namespace := b.namespace
	if namespace == "" {
		namespace = envOrDefault("AI_GRID_SUBSCRIPTION_NAMESPACE", defaultSubscriptionNamespace)
	}
	return &SubscriptionsServer{
		logger: b.logger, hubsServer: b.hubsServer, tenantsServer: b.tenantsServer,
		hubClientProvider: b.hubClientProvider,
		writer:            writer, namespace: namespace,
	}, nil
}

func (s *SubscriptionsServer) validateSubscriptionTenant(ctx context.Context, tenantID string) error {
	if !validModelID(tenantID) {
		return status.Errorf(codes.InvalidArgument, "spec.organization.id %q is not a valid Tenant id", tenantID)
	}
	response, err := s.tenantsServer.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenantID}.Build())
	if status.Code(err) == codes.NotFound {
		return status.Errorf(codes.FailedPrecondition, "Tenant %q does not exist", tenantID)
	}
	if err != nil {
		return err
	}
	if response == nil || response.GetObject() == nil {
		return status.Errorf(codes.Internal, "Tenant lookup for %q returned no object", tenantID)
	}
	return nil
}

func (s *SubscriptionsServer) List(ctx context.Context, request *publicv1.SubscriptionsListRequest) (*publicv1.SubscriptionsListResponse, error) {
	if request.GetOffset() < 0 {
		return nil, status.Error(codes.InvalidArgument, "offset must be non-negative")
	}
	if request.GetLimit() < 0 {
		return nil, status.Error(codes.InvalidArgument, "limit must be non-negative")
	}
	items, err := s.gitList(ctx, s.statusReports(ctx))
	if err != nil {
		return nil, err
	}
	filter, err := compileSubscriptionFilter(request.GetFilter())
	if err != nil {
		return nil, err
	}
	if filter != nil {
		now := time.Now().UTC()
		matched := make([]*publicv1.Subscription, 0, len(items))
		for _, item := range items {
			ok, err := filter.Match(ctx, item, now)
			if err != nil {
				return nil, err
			}
			if ok {
				matched = append(matched, item)
			}
		}
		items = matched
	}
	if err := orderSubscriptions(items, request.GetOrder()); err != nil {
		return nil, err
	}
	total, ok := int32FromInt64(int64(len(items)))
	if !ok {
		return nil, status.Error(codes.ResourceExhausted, "subscription catalog is too large for an int32 response")
	}
	start := min(int(request.GetOffset()), len(items))
	end := len(items)
	if request.HasLimit() {
		end = min(start+int(request.GetLimit()), end)
	}
	page := items[start:end]
	size, ok := int32FromInt64(int64(len(page)))
	if !ok {
		return nil, status.Error(codes.ResourceExhausted, "subscription page is too large for an int32 response")
	}
	return &publicv1.SubscriptionsListResponse{Size: size, Total: total, Items: page}, nil
}

func (s *SubscriptionsServer) Get(ctx context.Context, request *publicv1.SubscriptionsGetRequest) (*publicv1.SubscriptionsGetResponse, error) {
	if err := validateSubscriptionID(request.GetId()); err != nil {
		return nil, err
	}
	return s.gitGet(ctx, request.GetId(), s.statusReports(ctx))
}

func (s *SubscriptionsServer) Create(ctx context.Context, request *publicv1.SubscriptionsCreateRequest) (*publicv1.SubscriptionsCreateResponse, error) {
	object := request.GetObject()
	if object == nil {
		return nil, status.Error(codes.InvalidArgument, "object is required")
	}
	id := subscriptionID(object)
	if err := validateSubscriptionID(id); err != nil {
		return nil, err
	}
	if err := validateSubscriptionSpec(object.GetSpec()); err != nil {
		return nil, err
	}
	if object.GetMetadata() == nil || object.GetMetadata().GetName() != id {
		return nil, status.Error(codes.InvalidArgument, "id must match metadata.name")
	}
	if err := validateSubscriptionMetadata(object.GetMetadata()); err != nil {
		return nil, err
	}
	if err := s.validateSubscriptionTenant(ctx, object.GetSpec().GetOrganization().GetId()); err != nil {
		return nil, err
	}
	var response *publicv1.SubscriptionsCreateResponse
	err := gitops.RetryOnConflict(ctx, func() error {
		var err error
		response, err = s.gitCreate(ctx, proto.Clone(object).(*publicv1.Subscription))
		return err
	})
	if err != nil {
		return nil, subscriptionGitError("create", id, err)
	}
	return response, nil
}

func (s *SubscriptionsServer) Update(ctx context.Context, request *publicv1.SubscriptionsUpdateRequest) (*publicv1.SubscriptionsUpdateResponse, error) {
	object := request.GetObject()
	if object == nil || object.GetMetadata() == nil {
		return nil, status.Error(codes.InvalidArgument, "object and object.metadata are required")
	}
	if err := validateSubscriptionID(subscriptionID(object)); err != nil {
		return nil, err
	}
	if object.GetMetadata().GetName() != subscriptionID(object) {
		return nil, status.Error(codes.InvalidArgument, "id must match metadata.name")
	}
	paths, err := subscriptionUpdatePaths(request.GetUpdateMask(), object)
	if err != nil {
		return nil, err
	}
	if request.GetLock() && object.GetMetadata().GetVersion() == 0 {
		return nil, status.Error(codes.InvalidArgument, "lock requires metadata.version")
	}
	var response *publicv1.SubscriptionsUpdateResponse
	err = gitops.RetryOnConflict(ctx, func() error {
		var err error
		response, err = s.gitUpdate(ctx, proto.Clone(request).(*publicv1.SubscriptionsUpdateRequest), paths)
		return err
	})
	if err != nil {
		return nil, subscriptionGitError("update", subscriptionID(object), err)
	}
	return response, nil
}

func (s *SubscriptionsServer) Delete(ctx context.Context, request *publicv1.SubscriptionsDeleteRequest) (*publicv1.SubscriptionsDeleteResponse, error) {
	if err := validateSubscriptionID(request.GetId()); err != nil {
		return nil, err
	}
	if err := gitops.RetryOnConflict(ctx, func() error {
		return s.gitDelete(ctx, request.GetId(), request.GetResourceVersion())
	}); err != nil {
		return nil, subscriptionGitError("delete", request.GetId(), err)
	}
	return &publicv1.SubscriptionsDeleteResponse{}, nil
}

type subscriptionFilter struct {
	src string
	prg cel.Program
}

func compileSubscriptionFilter(src string) (*subscriptionFilter, error) {
	if strings.TrimSpace(src) == "" {
		return nil, nil
	}
	message := &publicv1.Subscription{}
	name := string(message.ProtoReflect().Descriptor().FullName())
	env, err := cel.NewEnv(cel.Types(message), cel.Variable("this", cel.ObjectType(name)), cel.Variable("now", cel.TimestampType), ext.Strings())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create subscription CEL environment: %v", err)
	}
	ast, issues := env.Compile(src)
	if issues.Err() != nil {
		return nil, status.Errorf(codes.InvalidArgument, "filter %s is not a valid CEL expression: %v", src, issues.Err())
	}
	if out := ast.OutputType(); !out.IsExactType(cel.BoolType) && !out.IsExactType(cel.DynType) {
		return nil, status.Errorf(codes.InvalidArgument, "filter %s must evaluate to a bool, not %s", src, out)
	}
	prg, err := env.Program(ast, cel.CostLimit(1_000_000), cel.InterruptCheckFrequency(100))
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "filter %s is not a valid CEL expression: %v", src, err)
	}
	return &subscriptionFilter{src: src, prg: prg}, nil
}

func (f *subscriptionFilter) Match(ctx context.Context, object *publicv1.Subscription, now time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, status.FromContextError(err).Err()
	}
	value, _, err := f.prg.ContextEval(ctx, map[string]any{"this": object, "now": now})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, status.FromContextError(ctxErr).Err()
		}
		return false, status.Errorf(codes.InvalidArgument, "filter %s failed on %q: %v", f.src, subscriptionID(object), err)
	}
	matched, ok := value.Value().(bool)
	if !ok {
		return false, status.Errorf(codes.InvalidArgument, "filter %s must evaluate to a bool", f.src)
	}
	return matched, nil
}

func orderSubscriptions(items []*publicv1.Subscription, order string) error {
	key := strings.TrimSpace(order)
	desc := strings.HasPrefix(key, "-")
	if desc {
		key = strings.TrimPrefix(key, "-")
	}
	switch key {
	case "", "id", "metadata.name":
		sort.SliceStable(items, func(i, j int) bool {
			a, b := subscriptionID(items[i]), subscriptionID(items[j])
			if desc {
				return a > b
			}
			return a < b
		})
	case "priority":
		sort.SliceStable(items, func(i, j int) bool {
			a, b := items[i].GetSpec().GetPriority(), items[j].GetSpec().GetPriority()
			if a != b {
				if desc {
					return a > b
				}
				return a < b
			}
			return subscriptionID(items[i]) < subscriptionID(items[j])
		})
	default:
		return status.Errorf(codes.InvalidArgument, "order %q is not supported; allowed values: id, -id, priority, -priority, metadata.name, -metadata.name", order)
	}
	return nil
}

func (s *SubscriptionsServer) statusReports(ctx context.Context) map[string]ObjectReports {
	if s.hubsServer == nil || s.hubClientProvider == nil {
		return nil
	}
	hubID, err := s.aiGridHubID(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "cannot resolve AI Grid Hub for subscription status; returning Git catalog", "error", err)
		return nil
	}
	hub, err := s.hubClientProvider.GetClient(ctx, hubID)
	if err != nil || hub.Config == nil {
		s.logger.WarnContext(ctx, "cannot read AI Grid Hub status for subscriptions", "hub", hubID, "error", err)
		return nil
	}
	client, err := dynamic.NewForConfig(hub.Config)
	if err != nil {
		s.logger.WarnContext(ctx, "cannot configure AI Grid Hub status client", "hub", hubID, "error", err)
		return nil
	}
	reader := NewCatalogStatusReader(client, CatalogApps{
		Namespace:     envOrDefault("AI_GRID_CATALOG_APP_NAMESPACE", DefaultCatalogAppNamespace),
		Models:        envOrDefault("AI_GRID_CATALOG_MODELS_APPSET", DefaultCatalogModelsAppSet),
		Ingress:       envOrDefault("AI_GRID_CATALOG_INGRESS_APPSET", DefaultCatalogIngressAppSet),
		Subscriptions: envOrDefault("AI_GRID_CATALOG_SUBSCRIPTIONS_APPSET", DefaultCatalogSubscriptionsAppSet),
	}).SetLogger(s.logger)
	statusCtx, cancel := context.WithTimeout(ctx, modelCatalogStatusTimeout)
	defer cancel()
	result := map[string]ObjectReports{}
	for _, request := range []struct {
		key  string
		set  CatalogSet
		kind string
	}{
		{"subscriptions", SubscriptionsSet, kindMaaSSubscription},
		{"policies", SubscriptionsSet, kindMaaSAuthPolicy},
		{"models", IngressSet, kindMaaSModelRef},
	} {
		reports, err := reader.Read(statusCtx, request.set, request.kind)
		if err != nil {
			s.logger.WarnContext(ctx, "cannot read AI Grid catalog status", "kind", request.kind, "error", err)
			continue
		}
		result[request.key] = reports
	}
	return result
}

func (s *SubscriptionsServer) aiGridHubID(ctx context.Context) (string, error) {
	const pageSize int32 = 100
	var matches []*privatev1.Hub
	for offset := int32(0); ; offset += pageSize {
		response, err := s.hubsServer.List(ctx, privatev1.HubsListRequest_builder{Offset: proto.Int32(offset), Limit: proto.Int32(pageSize)}.Build())
		if err != nil {
			return "", err
		}
		for _, hub := range response.GetItems() {
			if hub.GetMetadata().GetLabels()[aiGridHubLabel] == "true" {
				matches = append(matches, hub)
			}
		}
		if len(response.GetItems()) < int(pageSize) || offset+pageSize >= response.GetTotal() {
			break
		}
	}
	if len(matches) == 0 {
		return "", status.Error(codes.FailedPrecondition, "no AI Grid Hub is registered; register a Hub with the ai-grid=true label first")
	}
	if len(matches) > 1 {
		return "", status.Error(codes.FailedPrecondition, "multiple AI Grid Hubs are registered; subscription management requires exactly one AI Grid Hub")
	}
	return matches[0].GetId(), nil
}
