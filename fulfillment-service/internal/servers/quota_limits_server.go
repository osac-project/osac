/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type QuotaLimitsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.QuotaLimitsServer = (*QuotaLimitsServer)(nil)

// QuotaLimitsServer is the public face of the quota limits. It adds one rule the method-level
// policy can't express: a limit with no project is a tenant override, which only a cloud provider
// admin may write, while a tenant admin may write any limit of a project they can see.
type QuotaLimitsServer struct {
	publicv1.UnimplementedQuotaLimitsServer

	logger       *slog.Logger
	tenancyLogic auth.TenancyLogic
	delegate     privatev1.QuotaLimitsServer
	inMapper     *GenericMapper[*publicv1.QuotaLimit, *privatev1.QuotaLimit]
	outMapper    *GenericMapper[*privatev1.QuotaLimit, *publicv1.QuotaLimit]
	defaultsIn   *GenericMapper[*publicv1.QuotaDefaultLimit, *privatev1.QuotaDefaultLimit]
	defaultsOut  *GenericMapper[*privatev1.QuotaDefaultLimit, *publicv1.QuotaDefaultLimit]
}

func NewQuotaLimitsServer() *QuotaLimitsServerBuilder {
	return &QuotaLimitsServerBuilder{}
}

func (b *QuotaLimitsServerBuilder) SetLogger(value *slog.Logger) *QuotaLimitsServerBuilder {
	b.logger = value
	return b
}

func (b *QuotaLimitsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *QuotaLimitsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *QuotaLimitsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *QuotaLimitsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *QuotaLimitsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *QuotaLimitsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *QuotaLimitsServerBuilder) Build() (result *QuotaLimitsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	if b.attributionLogic == nil {
		err = errors.New("attribution logic is mandatory")
		return
	}
	inMapper, err := NewGenericMapper[*publicv1.QuotaLimit, *privatev1.QuotaLimit]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.QuotaLimit, *publicv1.QuotaLimit]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}
	defaultsIn, err := NewGenericMapper[*publicv1.QuotaDefaultLimit, *privatev1.QuotaDefaultLimit]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	defaultsOut, err := NewGenericMapper[*privatev1.QuotaDefaultLimit, *publicv1.QuotaDefaultLimit]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}
	delegate, err := NewPrivateQuotaLimitsServer().
		SetLogger(b.logger).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc((*publicv1.QuotaLimit)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return
	}
	result = &QuotaLimitsServer{
		logger:       b.logger,
		tenancyLogic: b.tenancyLogic,
		delegate:     delegate,
		inMapper:     inMapper,
		outMapper:    outMapper,
		defaultsIn:   defaultsIn,
		defaultsOut:  defaultsOut,
	}
	return
}

func (s *QuotaLimitsServer) List(ctx context.Context,
	request *publicv1.QuotaLimitsListRequest) (response *publicv1.QuotaLimitsListResponse, err error) {
	privateRequest := &privatev1.QuotaLimitsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	if request.HasLimit() {
		privateRequest.SetLimit(request.GetLimit())
	}
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())

	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaLimitsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	items := make([]*publicv1.QuotaLimit, len(privateResponse.GetItems()))
	for i, item := range privateResponse.GetItems() {
		if items[i], err = s.mapLimitOut(ctx, item); err != nil {
			return nil, err
		}
	}
	response.SetItems(items)
	return
}

func (s *QuotaLimitsServer) Get(ctx context.Context,
	request *publicv1.QuotaLimitsGetRequest) (response *publicv1.QuotaLimitsGetResponse, err error) {
	privateRequest := &privatev1.QuotaLimitsGetRequest{}
	privateRequest.SetId(request.GetId())
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	object, err := s.mapLimitOut(ctx, privateResponse.GetObject())
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaLimitsGetResponse{}
	response.SetObject(object)
	return
}

func (s *QuotaLimitsServer) Create(ctx context.Context,
	request *publicv1.QuotaLimitsCreateRequest) (response *publicv1.QuotaLimitsCreateResponse, err error) {
	if err = s.checkOverrideWrite(ctx, request.GetObject().GetMetadata().GetProject()); err != nil {
		return
	}
	object, err := s.mapLimitIn(ctx, request.GetObject())
	if err != nil {
		return nil, err
	}
	privateRequest := &privatev1.QuotaLimitsCreateRequest{}
	privateRequest.SetObject(object)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	created, err := s.mapLimitOut(ctx, privateResponse.GetObject())
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaLimitsCreateResponse{}
	response.SetObject(created)
	return
}

func (s *QuotaLimitsServer) Update(ctx context.Context,
	request *publicv1.QuotaLimitsUpdateRequest) (response *publicv1.QuotaLimitsUpdateResponse, err error) {
	ctx = withPublicAPIView(ctx)
	if err = validatePublicObjectMetadata(request.GetObject()); err != nil {
		return
	}
	if err = s.checkOverrideWrite(ctx, request.GetObject().GetMetadata().GetProject()); err != nil {
		return
	}
	object, err := s.mapLimitIn(ctx, request.GetObject())
	if err != nil {
		return nil, err
	}
	privateRequest := &privatev1.QuotaLimitsUpdateRequest{}
	privateRequest.SetObject(object)
	privateRequest.SetUpdateMask(request.GetUpdateMask())
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	updated, err := s.mapLimitOut(ctx, privateResponse.GetObject())
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaLimitsUpdateResponse{}
	response.SetObject(updated)
	return
}

func (s *QuotaLimitsServer) Delete(ctx context.Context,
	request *publicv1.QuotaLimitsDeleteRequest) (response *publicv1.QuotaLimitsDeleteResponse, err error) {
	// A delete of an override is a write to the tenant's quota state, so the stored scope decides:
	privateRequest := &privatev1.QuotaLimitsGetRequest{}
	privateRequest.SetId(request.GetId())
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	if err = s.checkOverrideWrite(ctx, privateResponse.GetObject().GetMetadata().GetProject()); err != nil {
		return
	}
	deleteRequest := &privatev1.QuotaLimitsDeleteRequest{}
	deleteRequest.SetId(request.GetId())
	_, err = s.delegate.Delete(ctx, deleteRequest)
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaLimitsDeleteResponse{}
	return
}

func (s *QuotaLimitsServer) ListDefaults(ctx context.Context,
	request *publicv1.QuotaLimitsListDefaultsRequest) (response *publicv1.QuotaLimitsListDefaultsResponse, err error) {
	privateRequest := &privatev1.QuotaLimitsListDefaultsRequest{}
	privateRequest.SetOffset(request.GetOffset())
	if request.HasLimit() {
		privateRequest.SetLimit(request.GetLimit())
	}
	privateRequest.SetFilter(request.GetFilter())
	privateResponse, err := s.delegate.ListDefaults(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaLimitsListDefaultsResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	items := make([]*publicv1.QuotaDefaultLimit, len(privateResponse.GetItems()))
	for i, item := range privateResponse.GetItems() {
		if items[i], err = s.mapDefaultOut(ctx, item); err != nil {
			return nil, err
		}
	}
	response.SetItems(items)
	return
}

func (s *QuotaLimitsServer) GetDefault(ctx context.Context,
	request *publicv1.QuotaLimitsGetDefaultRequest) (response *publicv1.QuotaLimitsGetDefaultResponse, err error) {
	privateRequest := &privatev1.QuotaLimitsGetDefaultRequest{}
	privateRequest.SetId(request.GetId())
	privateResponse, err := s.delegate.GetDefault(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	object, err := s.mapDefaultOut(ctx, privateResponse.GetObject())
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaLimitsGetDefaultResponse{}
	response.SetObject(object)
	return
}

func (s *QuotaLimitsServer) SetDefault(ctx context.Context,
	request *publicv1.QuotaLimitsSetDefaultRequest) (response *publicv1.QuotaLimitsSetDefaultResponse, err error) {
	object, err := s.mapDefaultIn(ctx, request.GetObject())
	if err != nil {
		return nil, err
	}
	privateRequest := &privatev1.QuotaLimitsSetDefaultRequest{}
	privateRequest.SetObject(object)
	privateResponse, err := s.delegate.SetDefault(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	created, err := s.mapDefaultOut(ctx, privateResponse.GetObject())
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaLimitsSetDefaultResponse{}
	response.SetObject(created)
	return
}

func (s *QuotaLimitsServer) ClearDefault(ctx context.Context,
	request *publicv1.QuotaLimitsClearDefaultRequest) (response *publicv1.QuotaLimitsClearDefaultResponse, err error) {
	privateRequest := &privatev1.QuotaLimitsClearDefaultRequest{}
	privateRequest.SetId(request.GetId())
	_, err = s.delegate.ClearDefault(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	response = &publicv1.QuotaLimitsClearDefaultResponse{}
	return
}

// checkOverrideWrite refuses a write to a tenant override, a limit of no project, unless the
// caller sees every tenant: only a cloud provider admin manages overrides, while a tenant admin
// manages the limits of their projects.
func (s *QuotaLimitsServer) checkOverrideWrite(ctx context.Context, project string) error {
	if project != "" {
		return nil
	}
	visibility, err := s.tenancyLogic.DetermineVisibility(ctx)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.Internal, "failed to determine the visible tenants")
	}
	if !visibility.IsTotal() {
		return grpcstatus.Errorf(grpccodes.PermissionDenied,
			"a tenant limit without a project is a tenant override, which only a cloud provider admin may set")
	}
	return nil
}

func (s *QuotaLimitsServer) mapLimitIn(
	ctx context.Context, object *publicv1.QuotaLimit,
) (*privatev1.QuotaLimit, error) {
	result := &privatev1.QuotaLimit{}
	if err := s.inMapper.Copy(ctx, object, result); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public quota limit to private", slog.Any("error", err))
		return nil, publicInputMappingError(err, "failed to process quota limit")
	}
	return result, nil
}

func (s *QuotaLimitsServer) mapLimitOut(
	ctx context.Context, object *privatev1.QuotaLimit,
) (*publicv1.QuotaLimit, error) {
	result := &publicv1.QuotaLimit{}
	if err := s.outMapper.Copy(ctx, object, result); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private quota limit to public", slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process quota limit")
	}
	return result, nil
}

func (s *QuotaLimitsServer) mapDefaultIn(
	ctx context.Context, object *publicv1.QuotaDefaultLimit,
) (*privatev1.QuotaDefaultLimit, error) {
	result := &privatev1.QuotaDefaultLimit{}
	if err := s.defaultsIn.Copy(ctx, object, result); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public quota default limit to private", slog.Any("error", err))
		return nil, publicInputMappingError(err, "failed to process quota default limit")
	}
	return result, nil
}

func (s *QuotaLimitsServer) mapDefaultOut(
	ctx context.Context, object *privatev1.QuotaDefaultLimit,
) (*publicv1.QuotaDefaultLimit, error) {
	result := &publicv1.QuotaDefaultLimit{}
	if err := s.defaultsOut.Copy(ctx, object, result); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private quota default limit to public", slog.Any("error", err))
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process quota default limit")
	}
	return result, nil
}
