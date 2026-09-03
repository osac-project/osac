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
	"fmt"
	"log/slog"
	"os"
	"strconv"

	semver "github.com/Masterminds/semver/v3"
	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
)

type PrivateAddOnOperatorsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
	defaultPublished  bool
}

var _ privatev1.AddOnOperatorsServer = (*PrivateAddOnOperatorsServer)(nil)

type PrivateAddOnOperatorsServer struct {
	privatev1.UnimplementedAddOnOperatorsServer
	logger           *slog.Logger
	defaultPublished bool
	generic          *GenericServer[*privatev1.AddOnOperator]
}

func NewPrivateAddOnOperatorsServer() *PrivateAddOnOperatorsServerBuilder {
	return &PrivateAddOnOperatorsServerBuilder{}
}

func (b *PrivateAddOnOperatorsServerBuilder) SetLogger(value *slog.Logger) *PrivateAddOnOperatorsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateAddOnOperatorsServerBuilder) SetNotifier(
	value events.Notifier) *PrivateAddOnOperatorsServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateAddOnOperatorsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateAddOnOperatorsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateAddOnOperatorsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateAddOnOperatorsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateAddOnOperatorsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateAddOnOperatorsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateAddOnOperatorsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateAddOnOperatorsServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateAddOnOperatorsServerBuilder) SetDefaultPublished(value bool) *PrivateAddOnOperatorsServerBuilder {
	b.defaultPublished = value
	return b
}

func (b *PrivateAddOnOperatorsServerBuilder) Build() (result *PrivateAddOnOperatorsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	defaultPublished := b.defaultPublished
	if !defaultPublished {
		if raw := os.Getenv("ADDON_OPERATOR_DEFAULT_PUBLISHED"); raw != "" {
			defaultPublished, err = strconv.ParseBool(raw)
			if err != nil {
				err = fmt.Errorf("invalid ADDON_OPERATOR_DEFAULT_PUBLISHED value %q: %w", raw, err)
				return
			}
		}
	}

	generic, err := NewGenericServer[*privatev1.AddOnOperator]().
		SetLogger(b.logger).
		SetService(privatev1.AddOnOperators_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		AddAllowedTenants(auth.SharedTenant).
		Build()
	if err != nil {
		return
	}

	result = &PrivateAddOnOperatorsServer{
		logger:           b.logger,
		defaultPublished: defaultPublished,
		generic:          generic,
	}
	return
}

func (s *PrivateAddOnOperatorsServer) List(ctx context.Context,
	request *privatev1.AddOnOperatorsListRequest) (response *privatev1.AddOnOperatorsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateAddOnOperatorsServer) Get(ctx context.Context,
	request *privatev1.AddOnOperatorsGetRequest) (response *privatev1.AddOnOperatorsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateAddOnOperatorsServer) Create(ctx context.Context,
	request *privatev1.AddOnOperatorsCreateRequest) (response *privatev1.AddOnOperatorsCreateResponse, err error) {
	if object := request.GetObject(); object != nil {
		if s.defaultPublished && !object.HasPublished() {
			object.SetPublished(true)
		}
		if err = validateOCPVersionRange(object.GetMinOcpVersion(), object.GetMaxOcpVersion()); err != nil {
			return
		}
	}
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateAddOnOperatorsServer) Update(ctx context.Context,
	request *privatev1.AddOnOperatorsUpdateRequest) (response *privatev1.AddOnOperatorsUpdateResponse, err error) {
	if object := request.GetObject(); object != nil {
		if updateIncludesField(request.GetUpdateMask(), "min_ocp_version", "max_ocp_version") {
			minVersion := object.GetMinOcpVersion()
			maxVersion := object.GetMaxOcpVersion()

			// When only one version field is in the mask, resolve the other from the existing object.
			if !updateIncludesField(request.GetUpdateMask(), "min_ocp_version") ||
				!updateIncludesField(request.GetUpdateMask(), "max_ocp_version") {
				var getResponse *privatev1.AddOnOperatorsGetResponse
				if err = s.generic.Get(ctx, &privatev1.AddOnOperatorsGetRequest{Id: object.GetId()}, &getResponse); err != nil {
					return
				}
				existing := getResponse.GetObject()
				if !updateIncludesField(request.GetUpdateMask(), "min_ocp_version") {
					minVersion = existing.GetMinOcpVersion()
				}
				if !updateIncludesField(request.GetUpdateMask(), "max_ocp_version") {
					maxVersion = existing.GetMaxOcpVersion()
				}
			}

			if err = validateOCPVersionRange(minVersion, maxVersion); err != nil {
				return
			}
		}
	}
	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateAddOnOperatorsServer) Delete(ctx context.Context,
	request *privatev1.AddOnOperatorsDeleteRequest) (response *privatev1.AddOnOperatorsDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateAddOnOperatorsServer) Signal(ctx context.Context,
	request *privatev1.AddOnOperatorsSignalRequest) (response *privatev1.AddOnOperatorsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

// validateOCPVersionRange validates that non-empty version strings are valid semver and that
// min is not greater than max when both are provided.
func validateOCPVersionRange(minVersion, maxVersion string) error {
	var min, max *semver.Version
	if minVersion != "" {
		var err error
		min, err = semver.NewVersion(minVersion)
		if err != nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"field 'min_ocp_version' is not a valid version: %v", err)
		}
	}
	if maxVersion != "" {
		var err error
		max, err = semver.NewVersion(maxVersion)
		if err != nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"field 'max_ocp_version' is not a valid version: %v", err)
		}
	}
	if min != nil && max != nil && min.GreaterThan(max) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"min_ocp_version '%s' must be <= max_ocp_version '%s'", minVersion, maxVersion)
	}
	return nil
}
