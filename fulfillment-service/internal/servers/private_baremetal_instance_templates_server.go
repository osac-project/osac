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

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateBareMetalInstanceTemplatesServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.BareMetalInstanceTemplatesServer = (*PrivateBareMetalInstanceTemplatesServer)(nil)

type PrivateBareMetalInstanceTemplatesServer struct {
	privatev1.UnimplementedBareMetalInstanceTemplatesServer
	logger           *slog.Logger
	generic          *GenericServer[*privatev1.BareMetalInstanceTemplate]
	instanceTypesDao *dao.GenericDAO[*privatev1.BareMetalInstanceType]
}

func NewPrivateBareMetalInstanceTemplatesServer() *PrivateBareMetalInstanceTemplatesServerBuilder {
	return &PrivateBareMetalInstanceTemplatesServerBuilder{}
}

func (b *PrivateBareMetalInstanceTemplatesServerBuilder) SetLogger(value *slog.Logger) *PrivateBareMetalInstanceTemplatesServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateBareMetalInstanceTemplatesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateBareMetalInstanceTemplatesServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateBareMetalInstanceTemplatesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateBareMetalInstanceTemplatesServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateBareMetalInstanceTemplatesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateBareMetalInstanceTemplatesServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateBareMetalInstanceTemplatesServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateBareMetalInstanceTemplatesServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateBareMetalInstanceTemplatesServerBuilder) Build() (result *PrivateBareMetalInstanceTemplatesServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	generic, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
		SetLogger(b.logger).
		SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		AddAllowedTenants(auth.SharedTenant).
		Build()
	if err != nil {
		return
	}

	instanceTypesDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceType]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		Build()
	if err != nil {
		return
	}
	result = &PrivateBareMetalInstanceTemplatesServer{
		logger:           b.logger,
		generic:          generic,
		instanceTypesDao: instanceTypesDao,
	}
	return
}

func (s *PrivateBareMetalInstanceTemplatesServer) List(ctx context.Context,
	request *privatev1.BareMetalInstanceTemplatesListRequest) (response *privatev1.BareMetalInstanceTemplatesListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstanceTemplatesServer) Get(ctx context.Context,
	request *privatev1.BareMetalInstanceTemplatesGetRequest) (response *privatev1.BareMetalInstanceTemplatesGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstanceTemplatesServer) Create(ctx context.Context,
	request *privatev1.BareMetalInstanceTemplatesCreateRequest) (response *privatev1.BareMetalInstanceTemplatesCreateResponse, err error) {
	obj := request.GetObject()
	if obj != nil && obj.GetMetadata().GetName() == "" && obj.GetId() != "" {
		if obj.GetMetadata() == nil {
			obj.SetMetadata(&privatev1.Metadata{})
		}
		obj.GetMetadata().SetName(templateNameFromID(obj.GetId()))
	}
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstanceTemplatesServer) Update(ctx context.Context,
	request *privatev1.BareMetalInstanceTemplatesUpdateRequest) (response *privatev1.BareMetalInstanceTemplatesUpdateResponse, err error) {
	err = s.generic.UpdateWithCandidatePreparation(ctx, request, &response, s.validateArchitectureUpdate)
	return
}

func (s *PrivateBareMetalInstanceTemplatesServer) validateArchitectureUpdate(
	ctx context.Context, current, candidate *privatev1.BareMetalInstanceTemplate,
) (err error) {
	ref := candidate.GetInstanceType()
	if proto.Equal(current.GetInstanceType(), ref) || ref == nil {
		return nil
	}
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return err
	}
	defer tx.ReportError(&err)
	const source = " in instance_type"
	if err := validatePlatformReference(ref, "bare metal instance type", source); err != nil {
		return err
	}
	instanceType, err := resolveLockedPlatformResource(ctx, s.instanceTypesDao, ref.GetId(), ref.GetName(),
		"bare metal instance type", source, grpccodes.NotFound)
	if err != nil {
		return err
	}
	if err := validateResourceNotDeleted("bare metal instance type", refKey(ref), source, instanceType.GetMetadata()); err != nil {
		return err
	}
	conflicts, err := dao.BareMetalTemplateArchitectureConflicts(ctx, current.GetId(), instanceType.GetSpec().GetHardware().GetCpu().GetArchitecture())
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to check template architecture dependencies",
			slog.String("template", current.GetId()), slog.Any("error", err))
		return grpcstatus.Error(grpccodes.Internal, "failed to check template architecture dependencies")
	}
	if conflicts {
		return grpcstatus.Error(grpccodes.FailedPrecondition,
			"INCOMPATIBLE_DISK_IMAGE_ARCHITECTURE: template instance type is incompatible with an existing catalog item's DiskImage; select a compatible type or replace the dependent catalog items")
	}
	candidate.SetInstanceType(canonicalBareMetalInstanceTypeReference(instanceType))
	return nil
}

func (s *PrivateBareMetalInstanceTemplatesServer) Delete(ctx context.Context,
	request *privatev1.BareMetalInstanceTemplatesDeleteRequest) (response *privatev1.BareMetalInstanceTemplatesDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstanceTemplatesServer) Signal(ctx context.Context,
	request *privatev1.BareMetalInstanceTemplatesSignalRequest) (response *privatev1.BareMetalInstanceTemplatesSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
