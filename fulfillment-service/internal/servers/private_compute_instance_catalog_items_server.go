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
	"google.golang.org/protobuf/types/known/structpb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
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
	diskImagesDao    *dao.GenericDAO[*privatev1.DiskImage]
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

	// Create the DiskImages DAO for field_definitions disk image validation:
	diskImagesDao, err := dao.NewGenericDAO[*privatev1.DiskImage]().
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
		diskImagesDao:    diskImagesDao,
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
		var diskImageWarnings []string
		diskImageWarnings, err = s.validateFieldDefinitionsDiskImage(ctx, request.GetObject().GetFieldDefinitions())
		if err != nil {
			return
		}
		warnings = append(warnings, diskImageWarnings...)
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
		var diskImageWarnings []string
		diskImageWarnings, err = s.validateFieldDefinitionsDiskImage(ctx, request.GetObject().GetFieldDefinitions())
		if err != nil {
			return
		}
		warnings = append(warnings, diskImageWarnings...)
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

// validateFieldDefinitionsDiskImage validates the disk_image constraint in field_definitions and
// normalizes the stored default to a DiskImageReference object.
//
// The path is the prefix-less, spec-relative "disk_image" — the convention the apply mechanism
// (applyDefault) and the UI already use, and the one the deletion-protection trigger (migration
// 101) matches. The default is accepted as either a bare name string (the shape clients and the
// UI send) or an already-normalized {"name": ...} object, and is rewritten in place to the object
// form keyed by the resolved name, so the persisted default matches the trigger and mirrors the
// version reference convention (migration 93).
//
// Rejects OBSOLETE (and not-found) disk images, warns on DEPRECATED. Tenant visibility is
// enforced by the DiskImages DAO's tenancy filter (a cross-tenant reference resolves to
// not-found), mirroring how validateFieldDefinitionsInstanceType relies on the DAO.
func (s *PrivateComputeInstanceCatalogItemsServer) validateFieldDefinitionsDiskImage(
	ctx context.Context,
	fieldDefinitions []*privatev1.FieldDefinition,
) ([]string, error) {
	// Scan field_definitions for the disk_image default. It is a name string (as clients and the
	// UI send it) or an already-normalized {"name": ...} reference object.
	var diskImageFd *privatev1.FieldDefinition
	var diskImageKey string
	for _, fd := range fieldDefinitions {
		if fd.GetPath() == "disk_image" {
			diskImageFd = fd
			diskImageKey = diskImageDefaultKey(fd.GetDefault())
			break
		}
	}

	if diskImageKey == "" {
		return nil, nil
	}

	// Look up the disk image and validate its state.
	diskImage, warnings, err := validateDiskImageState(ctx, s.diskImagesDao, diskImageKey, " in field_definitions")
	if err != nil {
		return nil, err
	}

	// Normalize the stored default to a DiskImageReference object keyed by the resolved name, so the
	// deletion-protection trigger (migration 101) matches it. The resolved name is used rather than
	// the raw input, so a lookup by id still stores the canonical name.
	if diskImage != nil {
		diskImageFd.SetDefault(structpb.NewStructValue(&structpb.Struct{
			Fields: map[string]*structpb.Value{
				"name": structpb.NewStringValue(diskImage.GetMetadata().GetName()),
			},
		}))
	}

	return warnings, nil
}

// diskImageDefaultKey extracts the disk image identifier from a field definition default, which
// may be a bare name string (client/UI input) or a normalized {"name": ...} reference object.
func diskImageDefaultKey(v *structpb.Value) string {
	if v == nil {
		return ""
	}
	if s := v.GetStringValue(); s != "" {
		return s
	}
	if st := v.GetStructValue(); st != nil {
		if nameVal, ok := st.GetFields()["name"]; ok {
			return nameVal.GetStringValue()
		}
	}
	return ""
}
