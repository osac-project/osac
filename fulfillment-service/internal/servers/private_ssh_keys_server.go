/*
Copyright (c) 2026 Red Hat Inc.

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
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateSshKeysServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.SshKeysServer = (*PrivateSshKeysServer)(nil)

type PrivateSshKeysServer struct {
	privatev1.UnimplementedSshKeysServer

	generic *GenericServer[*privatev1.SshKey]
}

func NewPrivateSshKeysServer() *PrivateSshKeysServerBuilder {
	return &PrivateSshKeysServerBuilder{}
}

func (b *PrivateSshKeysServerBuilder) SetLogger(value *slog.Logger) *PrivateSshKeysServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateSshKeysServerBuilder) SetNotifier(value events.Notifier) *PrivateSshKeysServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateSshKeysServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateSshKeysServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateSshKeysServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateSshKeysServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateSshKeysServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateSshKeysServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter expressions.
func (b *PrivateSshKeysServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateSshKeysServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateSshKeysServerBuilder) Build() (result *PrivateSshKeysServer, err error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.tenancyLogic == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}

	generic, err := NewGenericServer[*privatev1.SshKey]().
		SetLogger(b.logger).
		SetService(privatev1.SshKeys_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		Build()
	if err != nil {
		return nil, err
	}

	return &PrivateSshKeysServer{
		generic: generic,
	}, nil
}

func (s *PrivateSshKeysServer) List(ctx context.Context,
	request *privatev1.SshKeysListRequest) (response *privatev1.SshKeysListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateSshKeysServer) Get(ctx context.Context,
	request *privatev1.SshKeysGetRequest) (response *privatev1.SshKeysGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateSshKeysServer) Create(ctx context.Context,
	request *privatev1.SshKeysCreateRequest) (response *privatev1.SshKeysCreateResponse, err error) {
	if err = validateSshKeyCreate(request.GetObject()); err != nil {
		return nil, err
	}
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateSshKeysServer) Update(context.Context,
	*privatev1.SshKeysUpdateRequest) (*privatev1.SshKeysUpdateResponse, error) {
	return nil, grpcstatus.Error(grpccodes.Unimplemented, "SSH key update is not implemented")
}

func (s *PrivateSshKeysServer) Delete(ctx context.Context,
	request *privatev1.SshKeysDeleteRequest) (response *privatev1.SshKeysDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateSshKeysServer) Signal(ctx context.Context,
	request *privatev1.SshKeysSignalRequest) (response *privatev1.SshKeysSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

func validateSshKeyCreate(object *privatev1.SshKey) error {
	if object == nil {
		return grpcstatus.Error(grpccodes.InvalidArgument, "ssh key is mandatory")
	}
	if object.GetMetadata() == nil || object.GetMetadata().GetName() == "" {
		return grpcstatus.Error(grpccodes.InvalidArgument, "field 'metadata.name' is required")
	}
	if object.GetMetadata().GetProject() != "" {
		return grpcstatus.Error(grpccodes.InvalidArgument, "field 'metadata.project' must be empty")
	}
	if object.GetSpec() == nil || object.GetSpec().GetPublicKey() == "" {
		return grpcstatus.Error(grpccodes.InvalidArgument, "field 'spec.public_key' is required")
	}
	if err := validateOpenSSHPublicKey(object.GetSpec().GetPublicKey()); err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "spec.public_key: %s", err)
	}
	return nil
}
