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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/events"
)

type SecurityGroupsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ publicv1.SecurityGroupsServer = (*SecurityGroupsServer)(nil)

type SecurityGroupsServer struct {
	publicv1.UnimplementedSecurityGroupsServer

	logger    *slog.Logger
	delegate  privatev1.SecurityGroupsServer
	inMapper  *GenericMapper[*publicv1.SecurityGroup, *privatev1.SecurityGroup]
	outMapper *GenericMapper[*privatev1.SecurityGroup, *publicv1.SecurityGroup]
}

func NewSecurityGroupsServer() *SecurityGroupsServerBuilder {
	return &SecurityGroupsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *SecurityGroupsServerBuilder) SetLogger(value *slog.Logger) *SecurityGroupsServerBuilder {
	b.logger = value
	return b
}

// SetNotifier sets the notifier to use. This is optional.
func (b *SecurityGroupsServerBuilder) SetNotifier(value events.Notifier) *SecurityGroupsServerBuilder {
	b.notifier = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *SecurityGroupsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *SecurityGroupsServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *SecurityGroupsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *SecurityGroupsServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *SecurityGroupsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *SecurityGroupsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *SecurityGroupsServerBuilder) Build() (result *SecurityGroupsServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the mappers:
	inMapper, err := NewGenericMapper[*publicv1.SecurityGroup, *privatev1.SecurityGroup]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.SecurityGroup, *publicv1.SecurityGroup]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	// Create the private server to delegate to:
	delegate, err := NewPrivateSecurityGroupsServer().
		SetLogger(b.logger).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &SecurityGroupsServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *SecurityGroupsServer) List(ctx context.Context,
	request *publicv1.SecurityGroupsListRequest) (response *publicv1.SecurityGroupsListResponse, err error) {
	// Create private request with same parameters:
	privateRequest := &privatev1.SecurityGroupsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	privateRequest.SetLimit(request.GetLimit())
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())

	// Delegate to private server:
	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.SecurityGroup, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.SecurityGroup{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private security group to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process security groups")
		}
		publicItems[i] = publicItem
	}

	// Create the public response:
	response = &publicv1.SecurityGroupsListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *SecurityGroupsServer) Get(ctx context.Context,
	request *publicv1.SecurityGroupsGetRequest) (response *publicv1.SecurityGroupsGetResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.SecurityGroupsGetRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateSecurityGroup := privateResponse.GetObject()
	publicSecurityGroup := &publicv1.SecurityGroup{}
	err = s.outMapper.Copy(ctx, privateSecurityGroup, publicSecurityGroup)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private security group to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process security group")
	}

	// Create the public response:
	response = &publicv1.SecurityGroupsGetResponse{}
	response.SetObject(publicSecurityGroup)
	return
}

func (s *SecurityGroupsServer) Create(ctx context.Context,
	request *publicv1.SecurityGroupsCreateRequest) (response *publicv1.SecurityGroupsCreateResponse, err error) {
	// Map the public security group to private format:
	publicSecurityGroup := request.GetObject()
	if publicSecurityGroup == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateSecurityGroup := &privatev1.SecurityGroup{}
	err = s.inMapper.Copy(ctx, publicSecurityGroup, privateSecurityGroup)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public security group to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process security group")
		return
	}

	// Delegate to the private server:
	privateRequest := &privatev1.SecurityGroupsCreateRequest{}
	privateRequest.SetObject(privateSecurityGroup)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	createdPrivateSecurityGroup := privateResponse.GetObject()
	createdPublicSecurityGroup := &publicv1.SecurityGroup{}
	err = s.outMapper.Copy(ctx, createdPrivateSecurityGroup, createdPublicSecurityGroup)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private security group to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process security group")
		return
	}

	// Create the public response:
	response = &publicv1.SecurityGroupsCreateResponse{}
	response.SetObject(createdPublicSecurityGroup)
	return
}

func (s *SecurityGroupsServer) Update(ctx context.Context,
	request *publicv1.SecurityGroupsUpdateRequest) (response *publicv1.SecurityGroupsUpdateResponse, err error) {
	// Validate the request:
	publicSecurityGroup := request.GetObject()
	if publicSecurityGroup == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	id := publicSecurityGroup.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	// Get the existing object from the private server:
	getRequest := &privatev1.SecurityGroupsGetRequest{}
	getRequest.SetId(id)
	getResponse, err := s.delegate.Get(ctx, getRequest)
	if err != nil {
		return nil, err
	}
	existingPrivateSecurityGroup := getResponse.GetObject()

	// Map the public changes to the existing private object (preserving private data):
	err = s.inMapper.Copy(ctx, publicSecurityGroup, existingPrivateSecurityGroup)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public security group to private",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process security group")
		return
	}

	// Delegate to the private server with the merged object:
	privateRequest := &privatev1.SecurityGroupsUpdateRequest{}
	privateRequest.SetObject(existingPrivateSecurityGroup)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	updatedPrivateSecurityGroup := privateResponse.GetObject()
	updatedPublicSecurityGroup := &publicv1.SecurityGroup{}
	err = s.outMapper.Copy(ctx, updatedPrivateSecurityGroup, updatedPublicSecurityGroup)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private security group to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process security group")
		return
	}

	// Create the public response:
	response = &publicv1.SecurityGroupsUpdateResponse{}
	response.SetObject(updatedPublicSecurityGroup)
	return
}

func (s *SecurityGroupsServer) Delete(ctx context.Context,
	request *publicv1.SecurityGroupsDeleteRequest) (response *publicv1.SecurityGroupsDeleteResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.SecurityGroupsDeleteRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Create the public response:
	response = &publicv1.SecurityGroupsDeleteResponse{}
	return
}
