/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
)

type AddOnOperatorsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.AddOnOperatorsServer = (*AddOnOperatorsServer)(nil)

type AddOnOperatorsServer struct {
	publicv1.UnimplementedAddOnOperatorsServer

	logger          *slog.Logger
	tenancyLogic    auth.TenancyLogic
	delegate        privatev1.AddOnOperatorsServer
	filterValidator *dao.FilterTranslator
	inMapper        *GenericMapper[*publicv1.AddOnOperator, *privatev1.AddOnOperator]
	outMapper       *GenericMapper[*privatev1.AddOnOperator, *publicv1.AddOnOperator]
}

func NewAddOnOperatorsServer() *AddOnOperatorsServerBuilder {
	return &AddOnOperatorsServerBuilder{}
}

func (b *AddOnOperatorsServerBuilder) SetLogger(value *slog.Logger) *AddOnOperatorsServerBuilder {
	b.logger = value
	return b
}

func (b *AddOnOperatorsServerBuilder) SetNotifier(value events.Notifier) *AddOnOperatorsServerBuilder {
	b.notifier = value
	return b
}

func (b *AddOnOperatorsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *AddOnOperatorsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *AddOnOperatorsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *AddOnOperatorsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *AddOnOperatorsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *AddOnOperatorsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *AddOnOperatorsServerBuilder) Build() (result *AddOnOperatorsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	inMapper, err := NewGenericMapper[*publicv1.AddOnOperator, *privatev1.AddOnOperator]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.AddOnOperator, *publicv1.AddOnOperator]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}
	filterValidator, err := dao.NewFilterTranslator().
		SetLogger(b.logger).
		SetDescriptor((*publicv1.AddOnOperator)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return
	}

	delegate, err := NewPrivateAddOnOperatorsServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc((*privatev1.AddOnOperator)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return
	}

	result = &AddOnOperatorsServer{
		logger:          b.logger,
		tenancyLogic:    b.tenancyLogic,
		delegate:        delegate,
		filterValidator: filterValidator,
		inMapper:        inMapper,
		outMapper:       outMapper,
	}
	return
}

func (s *AddOnOperatorsServer) List(ctx context.Context,
	request *publicv1.AddOnOperatorsListRequest) (response *publicv1.AddOnOperatorsListResponse, err error) {
	visibility, err := s.tenancyLogic.DetermineVisibility(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to determine visibility", slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to determine visibility")
	}

	privateRequest := &privatev1.AddOnOperatorsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	if request.HasLimit() {
		privateRequest.SetLimit(request.GetLimit())
	}
	if request.GetFilter() != "" {
		if _, err = s.filterValidator.Translate(ctx, request.GetFilter()); err != nil {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "invalid filter: %v", err)
		}
	}
	composedFilter, err := s.addPublishedFilter(request.GetFilter())
	if err != nil {
		return nil, err
	}
	composedFilter = addTenantVisibilityFilter(composedFilter, visibility)
	privateRequest.SetFilter(composedFilter)
	privateRequest.SetOrder(request.GetOrder())

	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.AddOnOperator, 0, len(privateItems))
	for _, privateItem := range privateItems {
		publicItem := &publicv1.AddOnOperator{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to map private add-on operator to public", slog.Any("error", err))
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process add-on operators")
		}
		publicItems = append(publicItems, publicItem)
	}

	response = &publicv1.AddOnOperatorsListResponse{}
	response.SetSize(int32(len(publicItems))) // #nosec G115 -- bounded by page size
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func addTenantVisibilityFilter(filter string, visibility *auth.Visibility) string {
	visibleTenants := visibility.VisibleTenants()
	if visibleTenants == nil {
		return filter
	}
	parts := []string{"!has(this.tenant)", `this.tenant == ""`}
	for _, tenant := range visibleTenants {
		parts = append(parts, "this.tenant == "+strconv.Quote(tenant))
	}
	return "(" + filter + ") && (" + strings.Join(parts, " || ") + ")"
}

func (s *AddOnOperatorsServer) Get(ctx context.Context,
	request *publicv1.AddOnOperatorsGetRequest) (response *publicv1.AddOnOperatorsGetResponse, err error) {
	privateRequest := &privatev1.AddOnOperatorsGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	object := privateResponse.GetObject()
	if !object.GetPublished() {
		return nil, grpcstatus.Errorf(grpccodes.NotFound, "add-on operator not found")
	}

	visibility, err := s.tenancyLogic.DetermineVisibility(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to determine visibility", slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to determine visibility")
	}
	if !s.isVisibleToTenant(object, visibility) {
		return nil, grpcstatus.Errorf(grpccodes.NotFound, "add-on operator not found")
	}

	publicOperator := &publicv1.AddOnOperator{}
	err = s.outMapper.Copy(ctx, object, publicOperator)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private add-on operator to public", slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process add-on operator")
	}

	response = &publicv1.AddOnOperatorsGetResponse{}
	response.SetObject(publicOperator)
	return
}

// isVisibleToTenant reports whether an add-on operator is visible given the caller's tenant
// visibility. Global operators (tenant=="") are visible to all; tenant-scoped operators are visible
// only when the caller can see that tenant.
func (s *AddOnOperatorsServer) isVisibleToTenant(object *privatev1.AddOnOperator, visibility *auth.Visibility) bool {
	scopeTenant := object.GetTenant()
	return scopeTenant == "" || visibility.IsTenantVisible(scopeTenant)
}

func (s *AddOnOperatorsServer) addPublishedFilter(filter string) (string, error) {
	if filter == "" {
		return "this.published", nil
	}
	if err := validateCELSyntax(filter); err != nil {
		return "", grpcstatus.Errorf(grpccodes.InvalidArgument, "invalid filter: %v", err)
	}
	return "(" + filter + ") && this.published", nil
}
