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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// TenantBillingAssignmentsServerBuilder builds a TenantBillingAssignmentsServer.
type TenantBillingAssignmentsServerBuilder struct {
	logger   *slog.Logger
	delegate *PrivateTenantBillingAssignmentsServer
}

var _ publicv1.TenantBillingAssignmentsServer = (*TenantBillingAssignmentsServer)(nil)

// TenantBillingAssignmentsServer is the public server; it delegates to the private one.
type TenantBillingAssignmentsServer struct {
	publicv1.UnimplementedTenantBillingAssignmentsServer
	logger    *slog.Logger
	private   *PrivateTenantBillingAssignmentsServer
	inMapper  *GenericMapper[*publicv1.TenantBillingAssignment, *privatev1.TenantBillingAssignment]
	outMapper *GenericMapper[*privatev1.TenantBillingAssignment, *publicv1.TenantBillingAssignment]
}

func NewTenantBillingAssignmentsServer() *TenantBillingAssignmentsServerBuilder {
	return &TenantBillingAssignmentsServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *TenantBillingAssignmentsServerBuilder) SetLogger(value *slog.Logger) *TenantBillingAssignmentsServerBuilder {
	b.logger = value
	return b
}

// SetDelegate sets the private server to delegate to. This is mandatory.
func (b *TenantBillingAssignmentsServerBuilder) SetDelegate(
	value *PrivateTenantBillingAssignmentsServer) *TenantBillingAssignmentsServerBuilder {
	b.delegate = value
	return b
}

func (b *TenantBillingAssignmentsServerBuilder) Build() (result *TenantBillingAssignmentsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.delegate == nil {
		err = errors.New("delegate is mandatory")
		return
	}
	inMapper, err := NewGenericMapper[*publicv1.TenantBillingAssignment, *privatev1.TenantBillingAssignment]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return nil, err
	}
	outMapper, err := NewGenericMapper[*privatev1.TenantBillingAssignment, *publicv1.TenantBillingAssignment]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return nil, err
	}
	result = &TenantBillingAssignmentsServer{
		logger: b.logger, private: b.delegate, inMapper: inMapper, outMapper: outMapper,
	}
	return
}

func (s *TenantBillingAssignmentsServer) toPublic(ctx context.Context, in *privatev1.TenantBillingAssignment) (
	*publicv1.TenantBillingAssignment, error) {
	out := &publicv1.TenantBillingAssignment{}
	if err := s.outMapper.Copy(ctx, in, out); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map private tenant billing assignment to public", slog.Any("error", err))
		return nil, status.Errorf(codes.Internal, "failed to process tenant billing assignment")
	}
	return out, nil
}

func (s *TenantBillingAssignmentsServer) toPrivate(ctx context.Context, in *publicv1.TenantBillingAssignment) (
	*privatev1.TenantBillingAssignment, error) {
	out := &privatev1.TenantBillingAssignment{}
	if err := s.inMapper.Copy(ctx, in, out); err != nil {
		s.logger.ErrorContext(ctx, "Failed to map public tenant billing assignment to private", slog.Any("error", err))
		return nil, status.Errorf(codes.Internal, "failed to process tenant billing assignment")
	}
	return out, nil
}

// List maps the public shared list request to the private one and the private response back to public items.
func (s *TenantBillingAssignmentsServer) List(ctx context.Context,
	request *publicv1.TenantBillingAssignmentsListRequest) (*publicv1.TenantBillingAssignmentsListResponse, error) {
	privateRequest := &privatev1.TenantBillingAssignmentsListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	if request.HasLimit() {
		privateRequest.SetLimit(request.GetLimit())
	}
	privateRequest.SetFilter(request.GetFilter())
	privateRequest.SetOrder(request.GetOrder())

	privateResponse, err := s.private.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.TenantBillingAssignment, 0, len(privateItems))
	for _, privateItem := range privateItems {
		publicItem, err := s.toPublic(ctx, privateItem)
		if err != nil {
			return nil, err
		}
		publicItems = append(publicItems, publicItem)
	}

	response := &publicv1.TenantBillingAssignmentsListResponse{}
	response.SetSize(int32(len(publicItems))) // #nosec G115 -- bounded by page size
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return response, nil
}

// Get maps the public request to the private one and the result back.
func (s *TenantBillingAssignmentsServer) Get(ctx context.Context,
	request *publicv1.TenantBillingAssignmentsGetRequest) (*publicv1.TenantBillingAssignmentsGetResponse, error) {
	privateRequest := &privatev1.TenantBillingAssignmentsGetRequest{}
	privateRequest.SetId(request.GetId())

	privateResponse, err := s.private.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	publicObject, err := s.toPublic(ctx, privateResponse.GetObject())
	if err != nil {
		return nil, err
	}
	response := &publicv1.TenantBillingAssignmentsGetResponse{}
	response.SetObject(publicObject)
	return response, nil
}

// Create maps the public request's object to the private shape, delegates, and maps the result back.
func (s *TenantBillingAssignmentsServer) Create(ctx context.Context,
	request *publicv1.TenantBillingAssignmentsCreateRequest) (*publicv1.TenantBillingAssignmentsCreateResponse, error) {
	privateObject, err := s.toPrivate(ctx, request.GetObject())
	if err != nil {
		return nil, err
	}
	privateRequest := &privatev1.TenantBillingAssignmentsCreateRequest{}
	privateRequest.SetObject(privateObject)

	privateResponse, err := s.private.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	publicObject, err := s.toPublic(ctx, privateResponse.GetObject())
	if err != nil {
		return nil, err
	}
	response := &publicv1.TenantBillingAssignmentsCreateResponse{}
	response.SetObject(publicObject)
	return response, nil
}

// Update maps the public request's object and update mask to the private shape, delegates, and maps the result
// back.
func (s *TenantBillingAssignmentsServer) Update(ctx context.Context,
	request *publicv1.TenantBillingAssignmentsUpdateRequest) (*publicv1.TenantBillingAssignmentsUpdateResponse, error) {
	privateObject, err := s.toPrivate(ctx, request.GetObject())
	if err != nil {
		return nil, err
	}
	privateRequest := &privatev1.TenantBillingAssignmentsUpdateRequest{}
	privateRequest.SetObject(privateObject)
	privateRequest.SetUpdateMask(request.GetUpdateMask())

	privateResponse, err := s.private.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}
	publicObject, err := s.toPublic(ctx, privateResponse.GetObject())
	if err != nil {
		return nil, err
	}
	response := &publicv1.TenantBillingAssignmentsUpdateResponse{}
	response.SetObject(publicObject)
	return response, nil
}
