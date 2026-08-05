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
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/events"
)

type PrivateExternalIPPoolsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.ExternalIPPoolsServer = (*PrivateExternalIPPoolsServer)(nil)

type PrivateExternalIPPoolsServer struct {
	privatev1.UnimplementedExternalIPPoolsServer

	logger  *slog.Logger
	generic *GenericServer[*privatev1.ExternalIPPool]
}

func NewPrivateExternalIPPoolsServer() *PrivateExternalIPPoolsServerBuilder {
	return &PrivateExternalIPPoolsServerBuilder{}
}

func (b *PrivateExternalIPPoolsServerBuilder) SetLogger(value *slog.Logger) *PrivateExternalIPPoolsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateExternalIPPoolsServerBuilder) SetNotifier(value events.Notifier) *PrivateExternalIPPoolsServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateExternalIPPoolsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateExternalIPPoolsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateExternalIPPoolsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateExternalIPPoolsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateExternalIPPoolsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateExternalIPPoolsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateExternalIPPoolsServerBuilder) Build() (result *PrivateExternalIPPoolsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	generic, err := NewGenericServer[*privatev1.ExternalIPPool]().
		SetLogger(b.logger).
		SetService(privatev1.ExternalIPPools_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &PrivateExternalIPPoolsServer{
		logger:  b.logger,
		generic: generic,
	}
	return
}

func (s *PrivateExternalIPPoolsServer) List(ctx context.Context,
	request *privatev1.ExternalIPPoolsListRequest) (response *privatev1.ExternalIPPoolsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateExternalIPPoolsServer) Get(ctx context.Context,
	request *privatev1.ExternalIPPoolsGetRequest) (response *privatev1.ExternalIPPoolsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateExternalIPPoolsServer) Create(ctx context.Context,
	request *privatev1.ExternalIPPoolsCreateRequest) (response *privatev1.ExternalIPPoolsCreateResponse, err error) {
	pool := request.GetObject()

	err = s.validateCreate(ctx, pool)
	if err != nil {
		return
	}

	total := calculatePoolCapacity(pool.GetSpec().GetCidrs(), pool.GetSpec().GetIpFamily())
	pool.SetStatus(privatev1.ExternalIPPoolStatus_builder{
		Total:     total,
		Available: total,
	}.Build())

	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateExternalIPPoolsServer) Update(ctx context.Context,
	request *privatev1.ExternalIPPoolsUpdateRequest) (response *privatev1.ExternalIPPoolsUpdateResponse, err error) {
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	getRequest := &privatev1.ExternalIPPoolsGetRequest{}
	getRequest.SetId(id)
	var getResponse *privatev1.ExternalIPPoolsGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}

	err = validateExternalIPPoolUpdate(request.GetObject(), getResponse.GetObject())
	if err != nil {
		return
	}

	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateExternalIPPoolsServer) validateCreate(ctx context.Context,
	pool *privatev1.ExternalIPPool) error {

	if pool == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "external IP pool is mandatory")
	}

	spec := pool.GetSpec()
	if spec == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "external IP pool spec is mandatory")
	}

	if spec.GetIpFamily() == privatev1.IPFamily_IP_FAMILY_UNSPECIFIED {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.ip_family' is required")
	}

	cidrs := spec.GetCidrs()
	if len(cidrs) == 0 {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'spec.cidrs' is required")
	}

	canonicalCIDRs := make([]string, len(cidrs))
	for i, cidr := range cidrs {
		canonical, err := validatePoolCIDRFormat(cidr, spec.GetIpFamily(), i)
		if err != nil {
			return err
		}
		canonicalCIDRs[i] = canonical
	}
	spec.SetCidrs(canonicalCIDRs)

	if err := validateNoCIDRSelfOverlap(canonicalCIDRs); err != nil {
		return err
	}

	return s.validateNoExternalIPPoolCIDROverlap(ctx, pool)
}

func validateExternalIPPoolUpdate(newPool *privatev1.ExternalIPPool, existing *privatev1.ExternalIPPool) error {
	if newPool == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "external IP pool is mandatory")
	}

	spec := newPool.GetSpec()
	if spec == nil {
		return nil
	}

	if spec.GetIpFamily() != privatev1.IPFamily_IP_FAMILY_UNSPECIFIED &&
		spec.GetIpFamily() != existing.GetSpec().GetIpFamily() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.ip_family' is immutable and cannot be changed from '%s' to '%s'",
			existing.GetSpec().GetIpFamily().String(), spec.GetIpFamily().String())
	}

	if newCIDRs := spec.GetCidrs(); len(newCIDRs) > 0 && !cidrSlicesEqual(newCIDRs, existing.GetSpec().GetCidrs()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.cidrs' is immutable and cannot be changed after creation")
	}

	return nil
}

func (s *PrivateExternalIPPoolsServer) validateNoExternalIPPoolCIDROverlap(ctx context.Context,
	pool *privatev1.ExternalIPPool) error {

	newCIDRs := pool.GetSpec().GetCidrs()

	var offset int32
	for {
		listRequest := &privatev1.ExternalIPPoolsListRequest{}
		listRequest.SetOffset(offset)
		var listResponse *privatev1.ExternalIPPoolsListResponse
		if err := s.generic.List(ctx, listRequest, &listResponse); err != nil {
			s.logger.ErrorContext(ctx, "Failed to list pools for overlap check", slog.Any("error", err))
			return grpcstatus.Errorf(grpccodes.Internal, "failed to validate CIDR overlap")
		}

		for _, existing := range listResponse.GetItems() {
			for _, existingCIDR := range existing.GetSpec().GetCidrs() {
				for _, newCIDR := range newCIDRs {
					overlap, err := cidrsOverlap(newCIDR, existingCIDR)
					if err != nil {
						s.logger.ErrorContext(ctx, "Failed to check CIDR overlap", slog.Any("error", err))
						return grpcstatus.Errorf(grpccodes.Internal, "failed to validate CIDR overlap")
					}
					if overlap {
						return grpcstatus.Errorf(grpccodes.AlreadyExists,
							"CIDR '%s' overlaps with CIDR '%s' from existing pool '%s'",
							newCIDR, existingCIDR, existing.GetMetadata().GetName())
					}
				}
			}
		}

		if offset+listResponse.GetSize() >= listResponse.GetTotal() {
			break
		}
		offset += listResponse.GetSize()
	}

	return nil
}

func (s *PrivateExternalIPPoolsServer) Delete(ctx context.Context,
	request *privatev1.ExternalIPPoolsDeleteRequest) (response *privatev1.ExternalIPPoolsDeleteResponse, err error) {
	var getResponse *privatev1.ExternalIPPoolsGetResponse
	err = s.generic.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
		Id: request.GetId(),
	}.Build(), &getResponse)
	if err != nil {
		return
	}
	if allocated := getResponse.GetObject().GetStatus().GetAllocated(); allocated > 0 {
		err = grpcstatus.Errorf(
			grpccodes.FailedPrecondition,
			"cannot delete external IP pool '%s': %d external IP(s) are still allocated from it",
			request.GetId(), allocated,
		)
		return
	}
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateExternalIPPoolsServer) Signal(ctx context.Context,
	request *privatev1.ExternalIPPoolsSignalRequest) (response *privatev1.ExternalIPPoolsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
