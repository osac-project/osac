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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
	"github.com/osac-project/fulfillment-service/internal/events"
)

type PrivateComputeInstanceCatalogItemsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.ComputeInstanceCatalogItemsServer = (*PrivateComputeInstanceCatalogItemsServer)(nil)

type PrivateComputeInstanceCatalogItemsServer struct {
	privatev1.UnimplementedComputeInstanceCatalogItemsServer
	logger           *slog.Logger
	generic          *GenericServer[*privatev1.ComputeInstanceCatalogItem]
	instanceTypesDao *dao.GenericDAO[*privatev1.InstanceType]
}

func NewPrivateComputeInstanceCatalogItemsServer() *PrivateComputeInstanceCatalogItemsServerBuilder {
	return &PrivateComputeInstanceCatalogItemsServerBuilder{}
}

func (b *PrivateComputeInstanceCatalogItemsServerBuilder) SetLogger(value *slog.Logger) *PrivateComputeInstanceCatalogItemsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateComputeInstanceCatalogItemsServerBuilder) SetNotifier(
	value events.Notifier) *PrivateComputeInstanceCatalogItemsServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateComputeInstanceCatalogItemsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateComputeInstanceCatalogItemsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateComputeInstanceCatalogItemsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateComputeInstanceCatalogItemsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateComputeInstanceCatalogItemsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateComputeInstanceCatalogItemsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateComputeInstanceCatalogItemsServerBuilder) Build() (result *PrivateComputeInstanceCatalogItemsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	// Create the InstanceTypes DAO for field_definitions instance type validation:
	instanceTypesDao, err := dao.NewGenericDAO[*privatev1.InstanceType]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	generic, err := NewGenericServer[*privatev1.ComputeInstanceCatalogItem]().
		SetLogger(b.logger).
		SetService(privatev1.ComputeInstanceCatalogItems_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &PrivateComputeInstanceCatalogItemsServer{
		logger:           b.logger,
		generic:          generic,
		instanceTypesDao: instanceTypesDao,
	}
	return
}

func (s *PrivateComputeInstanceCatalogItemsServer) List(ctx context.Context,
	request *privatev1.ComputeInstanceCatalogItemsListRequest) (response *privatev1.ComputeInstanceCatalogItemsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateComputeInstanceCatalogItemsServer) Get(ctx context.Context,
	request *privatev1.ComputeInstanceCatalogItemsGetRequest) (response *privatev1.ComputeInstanceCatalogItemsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateComputeInstanceCatalogItemsServer) Create(ctx context.Context,
	request *privatev1.ComputeInstanceCatalogItemsCreateRequest) (response *privatev1.ComputeInstanceCatalogItemsCreateResponse, err error) {
	var warnings []string
	if request.GetObject() != nil {
		if err = validateFieldDefinitions(request.GetObject().GetFieldDefinitions()); err != nil {
			return
		}
		warnings, err = s.validateFieldDefinitionsInstanceType(ctx, request.GetObject().GetFieldDefinitions())
		if err != nil {
			return
		}
	}
	err = s.generic.Create(ctx, request, &response)
	if err != nil {
		return
	}
	if len(warnings) > 0 && response != nil {
		response.SetWarnings(warnings)
	}
	return
}

func (s *PrivateComputeInstanceCatalogItemsServer) Update(ctx context.Context,
	request *privatev1.ComputeInstanceCatalogItemsUpdateRequest) (response *privatev1.ComputeInstanceCatalogItemsUpdateResponse, err error) {
	var warnings []string
	if request.GetObject() != nil {
		if err = validateFieldDefinitions(request.GetObject().GetFieldDefinitions()); err != nil {
			return
		}
		warnings, err = s.validateFieldDefinitionsInstanceType(ctx, request.GetObject().GetFieldDefinitions())
		if err != nil {
			return
		}
	}
	err = s.generic.Update(ctx, request, &response)
	if err != nil {
		return
	}
	if len(warnings) > 0 && response != nil {
		response.SetWarnings(warnings)
	}
	return
}

func (s *PrivateComputeInstanceCatalogItemsServer) Delete(ctx context.Context,
	request *privatev1.ComputeInstanceCatalogItemsDeleteRequest) (response *privatev1.ComputeInstanceCatalogItemsDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateComputeInstanceCatalogItemsServer) Signal(ctx context.Context,
	request *privatev1.ComputeInstanceCatalogItemsSignalRequest) (response *privatev1.ComputeInstanceCatalogItemsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// validateFieldDefinitionsInstanceType validates instance_type constraints in field_definitions.
// Rejects OBSOLETE instance types, warns on DEPRECATED.
func (s *PrivateComputeInstanceCatalogItemsServer) validateFieldDefinitionsInstanceType(
	ctx context.Context,
	fieldDefinitions []*privatev1.FieldDefinition,
) ([]string, error) {
	// Scan field_definitions to extract the spec.instance_type default value.
	var instanceTypeName string
	for _, fd := range fieldDefinitions {
		if fd.GetPath() == "spec.instance_type" {
			defaultValue := fd.GetDefault()
			if defaultValue != nil {
				instanceTypeName = defaultValue.GetStringValue()
			}
			break
		}
	}

	if instanceTypeName == "" {
		return nil, nil
	}

	// Look up the instance type and validate its state.
	return validateInstanceTypeState(ctx, s.instanceTypesDao, instanceTypeName, " in field_definitions")
}
