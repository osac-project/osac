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
	"slices"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
	"github.com/osac-project/fulfillment-service/internal/events"
)

var validExternalIPTransitions = map[privatev1.ExternalIPState][]privatev1.ExternalIPState{
	privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING:   {privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED},
	privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED: {privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED, privatev1.ExternalIPState_EXTERNAL_IP_STATE_DELETING},
	privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED:    {privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED},
}

type PrivateExternalIPsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.ExternalIPsServer = (*PrivateExternalIPsServer)(nil)

type PrivateExternalIPsServer struct {
	privatev1.UnimplementedExternalIPsServer

	logger            *slog.Logger
	generic           *GenericServer[*privatev1.ExternalIP]
	externalIPPoolDao *dao.GenericDAO[*privatev1.ExternalIPPool]
}

func NewPrivateExternalIPsServer() *PrivateExternalIPsServerBuilder {
	return &PrivateExternalIPsServerBuilder{}
}

func (b *PrivateExternalIPsServerBuilder) SetLogger(value *slog.Logger) *PrivateExternalIPsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateExternalIPsServerBuilder) SetNotifier(value events.Notifier) *PrivateExternalIPsServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateExternalIPsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateExternalIPsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateExternalIPsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateExternalIPsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateExternalIPsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateExternalIPsServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateExternalIPsServerBuilder) Build() (result *PrivateExternalIPsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	if b.attributionLogic == nil {
		err = errors.New("attribution logic is mandatory")
		return
	}

	externalIPPoolDao, err := dao.NewGenericDAO[*privatev1.ExternalIPPool]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	generic, err := NewGenericServer[*privatev1.ExternalIP]().
		SetLogger(b.logger).
		SetService(privatev1.ExternalIPs_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	result = &PrivateExternalIPsServer{
		logger:            b.logger,
		generic:           generic,
		externalIPPoolDao: externalIPPoolDao,
	}
	return
}

func (s *PrivateExternalIPsServer) List(ctx context.Context,
	request *privatev1.ExternalIPsListRequest) (response *privatev1.ExternalIPsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateExternalIPsServer) Get(ctx context.Context,
	request *privatev1.ExternalIPsGetRequest) (response *privatev1.ExternalIPsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateExternalIPsServer) Create(ctx context.Context,
	request *privatev1.ExternalIPsCreateRequest) (response *privatev1.ExternalIPsCreateResponse, err error) {
	externalIP := request.GetObject()

	err = s.validateExternalIP(ctx, externalIP)
	if err != nil {
		return
	}

	poolID := externalIP.GetSpec().GetPool()
	err = s.validatePoolReference(ctx, poolID)
	if err != nil {
		return
	}

	if externalIP.GetStatus() == nil {
		externalIP.SetStatus(privatev1.ExternalIPStatus_builder{
			State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING,
		}.Build())
	} else {
		externalIP.GetStatus().SetState(privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING)
	}

	err = s.generic.Create(ctx, request, &response)
	if err != nil {
		return
	}

	err = s.updatePoolCapacity(ctx, poolID, 1)
	if err != nil {
		return
	}

	return
}

func (s *PrivateExternalIPsServer) Update(ctx context.Context,
	request *privatev1.ExternalIPsUpdateRequest) (response *privatev1.ExternalIPsUpdateResponse, err error) {
	id := request.GetObject().GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	getRequest := &privatev1.ExternalIPsGetRequest{}
	getRequest.SetId(id)
	var getResponse *privatev1.ExternalIPsGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}

	existingExternalIP := getResponse.GetObject()
	mask := request.GetUpdateMask()

	if updateIncludesField(mask, "spec.pool") {
		if err = validateImmutableFieldsExternalIP(request.GetObject(), existingExternalIP); err != nil {
			return
		}
	}

	if updateIncludesField(mask, "status.state") {
		newState := request.GetObject().GetStatus().GetState()
		existingState := existingExternalIP.GetStatus().GetState()
		if newState != existingState {
			if err = validateExternalIPStateTransition(existingState, newState); err != nil {
				return
			}
		}
	}

	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateExternalIPsServer) Delete(ctx context.Context,
	request *privatev1.ExternalIPsDeleteRequest) (response *privatev1.ExternalIPsDeleteResponse, err error) {
	id := request.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	getRequest := &privatev1.ExternalIPsGetRequest{}
	getRequest.SetId(id)
	var getResponse *privatev1.ExternalIPsGetResponse
	err = s.generic.Get(ctx, getRequest, &getResponse)
	if err != nil {
		return
	}

	existingExternalIP := getResponse.GetObject()

	if err = validateNotDefault(existingExternalIP.GetMetadata().GetLabels(), "external IP"); err != nil {
		return
	}

	state := existingExternalIP.GetStatus().GetState()
	if state != privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED {
		err = grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"cannot delete ExternalIP in state %s: must be in ALLOCATED state", state)
		return
	}

	err = s.generic.Delete(ctx, request, &response)
	if err != nil {
		return
	}

	poolID := existingExternalIP.GetSpec().GetPool()
	if poolID != "" {
		err = s.updatePoolCapacity(ctx, poolID, -1)
		if err != nil {
			return
		}
	}

	return
}

func (s *PrivateExternalIPsServer) Signal(ctx context.Context,
	request *privatev1.ExternalIPsSignalRequest) (response *privatev1.ExternalIPsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

func (s *PrivateExternalIPsServer) validateExternalIP(ctx context.Context,
	externalIP *privatev1.ExternalIP) error {
	if externalIP == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "external IP is mandatory")
	}
	spec := externalIP.GetSpec()
	if spec == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "external IP spec is mandatory")
	}
	if spec.GetPool() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.pool' is required")
	}
	return nil
}

func (s *PrivateExternalIPsServer) validatePoolReference(ctx context.Context, poolID string) error {
	getResponse, err := s.externalIPPoolDao.Get().
		SetId(poolID).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"pool '%s' does not exist", poolID)
		}
		s.logger.ErrorContext(ctx, "Failed to query ExternalIPPool",
			slog.String("pool_id", poolID),
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to validate pool")
	}

	pool := getResponse.GetObject()

	if pool.GetStatus().GetState() != privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"pool '%s' is not in READY state (current state: %s)",
			poolID, pool.GetStatus().GetState().String())
	}

	if pool.GetStatus().GetAvailable() <= 0 {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"pool '%s' has no available capacity", poolID)
	}

	return nil
}

func (s *PrivateExternalIPsServer) updatePoolCapacity(ctx context.Context, poolID string, delta int64) error {
	poolResponse, err := s.externalIPPoolDao.Get().
		SetId(poolID).
		SetLock(true).
		Do(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to get pool for capacity update",
			slog.String("pool_id", poolID),
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to update pool capacity")
	}

	pool := poolResponse.GetObject()
	newAllocated := pool.GetStatus().GetAllocated() + delta
	newAvailable := pool.GetStatus().GetAvailable() - delta
	if newAvailable < 0 {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"pool '%s' has no available capacity", poolID)
	}
	if newAllocated < 0 {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"pool '%s' capacity inconsistency: allocated would become %d (available: %d)",
			poolID, newAllocated, newAvailable)
	}
	pool.GetStatus().SetAllocated(newAllocated)
	pool.GetStatus().SetAvailable(newAvailable)

	_, err = s.externalIPPoolDao.Update().
		SetObject(pool).
		Do(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to update pool capacity",
			slog.String("pool_id", poolID),
			slog.Any("error", err))
		var conflictErr *dao.ErrConflict
		if errors.As(err, &conflictErr) {
			return grpcstatus.Errorf(grpccodes.Aborted, "%s", conflictErr.Error())
		}
		return grpcstatus.Errorf(grpccodes.Internal, "failed to update pool capacity")
	}

	return nil
}

func validateImmutableFieldsExternalIP(newExternalIP, existingExternalIP *privatev1.ExternalIP) error {
	if existingExternalIP == nil {
		return nil
	}

	newPool := newExternalIP.GetSpec().GetPool()
	existingPool := existingExternalIP.GetSpec().GetPool()

	if newPool != existingPool {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"field 'spec.pool' is immutable and cannot be changed from '%s' to '%s'",
			existingPool, newPool)
	}

	return nil
}

func validateExternalIPStateTransition(from, to privatev1.ExternalIPState) error {
	allowed, exists := validExternalIPTransitions[from]
	if !exists || !slices.Contains(allowed, to) {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"invalid state transition from %s to %s", from.String(), to.String())
	}

	return nil
}
