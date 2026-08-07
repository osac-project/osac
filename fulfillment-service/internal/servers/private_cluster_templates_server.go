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

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
)

type PrivateClusterTemplatesServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.ClusterTemplatesServer = (*PrivateClusterTemplatesServer)(nil)

type PrivateClusterTemplatesServer struct {
	privatev1.UnimplementedClusterTemplatesServer
	logger             *slog.Logger
	clusterVersionsDao *dao.GenericDAO[*privatev1.ClusterVersion]
	generic            *GenericServer[*privatev1.ClusterTemplate]
}

func NewPrivateClusterTemplatesServer() *PrivateClusterTemplatesServerBuilder {
	return &PrivateClusterTemplatesServerBuilder{}
}

func (b *PrivateClusterTemplatesServerBuilder) SetLogger(value *slog.Logger) *PrivateClusterTemplatesServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateClusterTemplatesServerBuilder) SetNotifier(
	value events.Notifier) *PrivateClusterTemplatesServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateClusterTemplatesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateClusterTemplatesServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateClusterTemplatesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateClusterTemplatesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateClusterTemplatesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateClusterTemplatesServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *PrivateClusterTemplatesServerBuilder) Build() (result *PrivateClusterTemplatesServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the cluster versions DAO:
	clusterVersionsDao, err := dao.NewGenericDAO[*privatev1.ClusterVersion]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the generic server:
	generic, err := NewGenericServer[*privatev1.ClusterTemplate]().
		SetLogger(b.logger).
		SetService(privatev1.ClusterTemplates_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &PrivateClusterTemplatesServer{
		logger:             b.logger,
		clusterVersionsDao: clusterVersionsDao,
		generic:            generic,
	}
	return
}

func (s *PrivateClusterTemplatesServer) List(ctx context.Context,
	request *privatev1.ClusterTemplatesListRequest) (response *privatev1.ClusterTemplatesListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateClusterTemplatesServer) Get(ctx context.Context,
	request *privatev1.ClusterTemplatesGetRequest) (response *privatev1.ClusterTemplatesGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateClusterTemplatesServer) Create(ctx context.Context,
	request *privatev1.ClusterTemplatesCreateRequest) (response *privatev1.ClusterTemplatesCreateResponse, err error) {
	if object := request.GetObject(); object != nil {
		if err = s.validateSpecDefaultsVersion(ctx, object); err != nil {
			return
		}
		if object.GetMetadata().GetName() == "" && object.GetId() != "" {
			if object.GetMetadata() == nil {
				object.SetMetadata(&privatev1.Metadata{})
			}
			object.GetMetadata().SetName(templateNameFromID(object.GetId()))
		}
	}
	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateClusterTemplatesServer) Update(ctx context.Context,
	request *privatev1.ClusterTemplatesUpdateRequest) (response *privatev1.ClusterTemplatesUpdateResponse, err error) {
	if object := request.GetObject(); object != nil {
		if updateIncludesField(request.GetUpdateMask(), "spec_defaults") {
			if err = s.validateSpecDefaultsVersion(ctx, object); err != nil {
				return
			}
		}
	}
	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateClusterTemplatesServer) validateSpecDefaultsVersion(
	ctx context.Context, template *privatev1.ClusterTemplate,
) error {
	versionRef := template.GetSpecDefaults().GetVersion()
	if versionRef == nil || versionRef.GetName() == "" {
		return nil
	}
	return lookupAndValidateClusterVersion(ctx, s.logger, s.clusterVersionsDao, versionRef.GetName())
}

func (s *PrivateClusterTemplatesServer) Delete(ctx context.Context,
	request *privatev1.ClusterTemplatesDeleteRequest) (response *privatev1.ClusterTemplatesDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateClusterTemplatesServer) Signal(ctx context.Context,
	request *privatev1.ClusterTemplatesSignalRequest) (response *privatev1.ClusterTemplatesSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
