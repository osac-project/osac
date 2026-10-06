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
	"github.com/osac-project/osac/fulfillment-service/internal/services"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateHostTypesServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
	serviceFlags      *services.Flags
}

var _ privatev1.HostTypesServer = (*PrivateHostTypesServer)(nil)

type PrivateHostTypesServer struct {
	privatev1.UnimplementedHostTypesServer
	logger       *slog.Logger
	generic      *GenericServer[*privatev1.HostType]
	serviceFlags *services.Flags
}

const (
	bareMetalHostTypesFilter = "this.interfaces.size() > 0"
	virtualHostTypesFilter   = "this.interfaces.size() == 0"
)

func NewPrivateHostTypesServer() *PrivateHostTypesServerBuilder {
	return &PrivateHostTypesServerBuilder{}
}

func (b *PrivateHostTypesServerBuilder) SetLogger(value *slog.Logger) *PrivateHostTypesServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateHostTypesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateHostTypesServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateHostTypesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateHostTypesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateHostTypesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateHostTypesServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateHostTypesServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateHostTypesServerBuilder {
	b.filterDesc = value
	return b
}

// SetServiceFlags sets the enabled services used to filter host types.
func (b *PrivateHostTypesServerBuilder) SetServiceFlags(value *services.Flags) *PrivateHostTypesServerBuilder {
	b.serviceFlags = value
	return b
}

func (b *PrivateHostTypesServerBuilder) Build() (result *PrivateHostTypesServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the generic server:
	generic, err := NewGenericServer[*privatev1.HostType]().
		SetLogger(b.logger).
		SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		AddAllowedTenants(auth.SharedTenant).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &PrivateHostTypesServer{
		logger:       b.logger,
		generic:      generic,
		serviceFlags: b.serviceFlags,
	}
	return
}

func (s *PrivateHostTypesServer) List(ctx context.Context,
	request *privatev1.HostTypesListRequest) (response *privatev1.HostTypesListResponse, err error) {
	filter := hostTypesFilter(request.GetFilter(), s.serviceFlags)
	if filter != request.GetFilter() {
		request = proto.Clone(request).(*privatev1.HostTypesListRequest)
		request.SetFilter(filter)
	}
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateHostTypesServer) Get(ctx context.Context,
	request *privatev1.HostTypesGetRequest) (response *privatev1.HostTypesGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	if err != nil {
		return
	}
	if !hostTypeEnabled(response.GetObject(), s.serviceFlags) {
		response = nil
		err = grpcstatus.Errorf(grpccodes.NotFound, "object with identifier '%s' not found", request.GetId())
	}
	return
}

func hostTypesFilter(filter string, flags *services.Flags) string {
	predicate := hostTypesPredicate(flags)
	if predicate == "" {
		return filter
	}
	if filter == "" {
		return predicate
	}
	return "(" + filter + ") && (" + predicate + ")"
}

func hostTypesPredicate(flags *services.Flags) string {
	if flags == nil || (flags.VMaaS && flags.BMaaS) {
		return ""
	}
	if !flags.VMaaS && !flags.BMaaS {
		return "false"
	}
	if flags.BMaaS {
		return bareMetalHostTypesFilter
	}
	return virtualHostTypesFilter
}

func hostTypeEnabled(object *privatev1.HostType, flags *services.Flags) bool {
	if flags == nil || (flags.VMaaS && flags.BMaaS) {
		return true
	}
	return (flags.BMaaS && isBareMetalHostType(object)) ||
		(flags.VMaaS && isVirtualHostType(object))
}

// HostType interfaces describe physical NICs. The API contract uses a non-empty
// interfaces list to identify bare-metal host types; an empty list identifies
// virtual machine host types, whose NICs come from the overlay network.
func isBareMetalHostType(object *privatev1.HostType) bool {
	return len(object.GetInterfaces()) > 0
}

func isVirtualHostType(object *privatev1.HostType) bool {
	return !isBareMetalHostType(object)
}

func (s *PrivateHostTypesServer) Create(ctx context.Context,
	request *privatev1.HostTypesCreateRequest) (response *privatev1.HostTypesCreateResponse, err error) {
	obj := request.GetObject()
	if obj != nil && obj.GetMetadata().GetName() == "" && obj.GetId() != "" {
		if obj.GetMetadata() == nil {
			obj.SetMetadata(&privatev1.Metadata{})
		}
		obj.GetMetadata().SetName(toDNSLabel(obj.GetId()))
	}
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateHostTypesServer) Update(ctx context.Context,
	request *privatev1.HostTypesUpdateRequest) (response *privatev1.HostTypesUpdateResponse, err error) {
	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateHostTypesServer) Delete(ctx context.Context,
	request *privatev1.HostTypesDeleteRequest) (response *privatev1.HostTypesDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateHostTypesServer) Signal(ctx context.Context,
	request *privatev1.HostTypesSignalRequest) (response *privatev1.HostTypesSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
