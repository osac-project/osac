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

	"google.golang.org/protobuf/reflect/protoreflect"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
)

type PrivateBareMetalInstanceTemplatesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.BareMetalInstanceTemplatesServer = (*PrivateBareMetalInstanceTemplatesServer)(nil)

type PrivateBareMetalInstanceTemplatesServer struct {
	privatev1.UnimplementedBareMetalInstanceTemplatesServer
	logger  *slog.Logger
	generic *GenericServer[*privatev1.BareMetalInstanceTemplate]
}

func NewPrivateBareMetalInstanceTemplatesServer() *PrivateBareMetalInstanceTemplatesServerBuilder {
	return &PrivateBareMetalInstanceTemplatesServerBuilder{}
}

func (b *PrivateBareMetalInstanceTemplatesServerBuilder) SetLogger(value *slog.Logger) *PrivateBareMetalInstanceTemplatesServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateBareMetalInstanceTemplatesServerBuilder) SetNotifier(value events.Notifier) *PrivateBareMetalInstanceTemplatesServerBuilder {
	b.notifier = value
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
// expressions. This is optional. When unset, the descriptor of the O generic parameter is used.
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
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return
	}

	result = &PrivateBareMetalInstanceTemplatesServer{
		logger:  b.logger,
		generic: generic,
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
	err = s.generic.Update(ctx, request, &response)
	return
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
