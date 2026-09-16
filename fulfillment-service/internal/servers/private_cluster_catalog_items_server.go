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
	"google.golang.org/protobuf/proto"
	"log/slog"
	"maps"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
	"github.com/osac-project/osac/fulfillment-service/internal/utils"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type PrivateClusterCatalogItemsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.ClusterCatalogItemsServer = (*PrivateClusterCatalogItemsServer)(nil)

type PrivateClusterCatalogItemsServer struct {
	privatev1.UnimplementedClusterCatalogItemsServer
	templatesDao       *dao.GenericDAO[*privatev1.ClusterTemplate]
	clusterVersionsDao *dao.GenericDAO[*privatev1.ClusterVersion]
	secretsDao         *dao.GenericDAO[*privatev1.Secret]
	subnetsDao         *dao.GenericDAO[*privatev1.Subnet]
	securityGroupsDao  *dao.GenericDAO[*privatev1.SecurityGroup]
	generic            *GenericServer[*privatev1.ClusterCatalogItem]
	hostTypesDao       *dao.GenericDAO[*privatev1.HostType]
}

func NewPrivateClusterCatalogItemsServer() *PrivateClusterCatalogItemsServerBuilder {
	return &PrivateClusterCatalogItemsServerBuilder{}
}

func (b *PrivateClusterCatalogItemsServerBuilder) SetLogger(value *slog.Logger) *PrivateClusterCatalogItemsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateClusterCatalogItemsServerBuilder) SetNotifier(
	value events.Notifier) *PrivateClusterCatalogItemsServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateClusterCatalogItemsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateClusterCatalogItemsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateClusterCatalogItemsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateClusterCatalogItemsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateClusterCatalogItemsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateClusterCatalogItemsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf descriptor used to validate public CEL filters. When omitted, the private Catalog
// Item descriptor is used.
func (b *PrivateClusterCatalogItemsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateClusterCatalogItemsServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateClusterCatalogItemsServerBuilder) Build() (result *PrivateClusterCatalogItemsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
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

	secretsDao, err := dao.NewGenericDAO[*privatev1.Secret]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	subnetsDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	securityGroupsDao, err := dao.NewGenericDAO[*privatev1.SecurityGroup]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	generic, err := NewGenericServer[*privatev1.ClusterCatalogItem]().
		SetLogger(b.logger).
		SetService(privatev1.ClusterCatalogItems_ServiceDesc.ServiceName).
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

	hostTypesDao, err := dao.NewGenericDAO[*privatev1.HostType]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}
	result = &PrivateClusterCatalogItemsServer{
		hostTypesDao:       hostTypesDao,
		templatesDao:       templatesDao,
		clusterVersionsDao: clusterVersionsDao,
		secretsDao:         secretsDao,
		subnetsDao:         subnetsDao,
		securityGroupsDao:  securityGroupsDao,
		generic:            generic,
	}
	return
}

func (s *PrivateClusterCatalogItemsServer) List(ctx context.Context,
	request *privatev1.ClusterCatalogItemsListRequest) (response *privatev1.ClusterCatalogItemsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateClusterCatalogItemsServer) Get(ctx context.Context,
	request *privatev1.ClusterCatalogItemsGetRequest) (response *privatev1.ClusterCatalogItemsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateClusterCatalogItemsServer) Create(ctx context.Context,
	request *privatev1.ClusterCatalogItemsCreateRequest) (response *privatev1.ClusterCatalogItemsCreateResponse, err error) {
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response, s.prepareCatalogItemCandidate)
	return
}

func (s *PrivateClusterCatalogItemsServer) Update(ctx context.Context,
	request *privatev1.ClusterCatalogItemsUpdateRequest) (response *privatev1.ClusterCatalogItemsUpdateResponse, err error) {
	err = s.generic.UpdateWithCandidatePreparation(ctx, request, &response, s.prepareCatalogItemCandidate)
	return
}

// prepareCatalogItemCandidate checks the Catalog Item that Create or Update would store.
// GenericServer has assigned its tenant on Create or merged the update mask on Update, so
// references are checked against that complete item. Recheck dependencies when an offering
// changes or is published; descriptive edits and unpublishing need no new dependency lookup.
func (s *PrivateClusterCatalogItemsServer) prepareCatalogItemCandidate(
	ctx context.Context, current *privatev1.ClusterCatalogItem, candidate *privatev1.ClusterCatalogItem,
) error {
	if candidate == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "catalog item is mandatory")
	}
	if current != nil {
		publishing := !current.GetPublished() && candidate.GetPublished()
		configurationChanged := current.GetMetadata().GetTenant() != candidate.GetMetadata().GetTenant() ||
			current.GetMetadata().GetProject() != candidate.GetMetadata().GetProject() ||
			!proto.Equal(current.GetTemplate(), candidate.GetTemplate()) ||
			!proto.Equal(current.GetFields(), candidate.GetFields()) ||
			!maps.EqualFunc(current.GetTemplateParameters(), candidate.GetTemplateParameters(), func(a, b *privatev1.TemplateParameterPolicy) bool { return proto.Equal(a, b) })
		// Unpublishing and descriptive edits must work even when dependencies are no longer usable.
		if !publishing && !configurationChanged {
			return nil
		}
	}
	template, err := s.validateAndCanonicalizeTemplate(ctx, current, candidate)
	if err != nil {
		return err
	}
	if err := validateAndCanonicalizeClusterCatalogItemPolicies(ctx, candidate, template, s.hostTypesDao, s.clusterVersionsDao, s.secretsDao, s.subnetsDao, s.securityGroupsDao); err != nil {
		return err
	}
	return nil
}

// validateAndCanonicalizeTemplate finds the Template named by this Catalog Item. A name lookup
// starts in the item's tenant/project; project or shared selectors can choose another scope.
// It stores the Template's actual ID/name/scope, checks parameter policies against that
// Template, and forbids changing the Template on Update. The resolved Template also supplies
// the allowed HostTypes for node-set policies.
func (s *PrivateClusterCatalogItemsServer) validateAndCanonicalizeTemplate(
	ctx context.Context, current *privatev1.ClusterCatalogItem, candidate *privatev1.ClusterCatalogItem,
) (*privatev1.ClusterTemplate, error) {
	ref := candidate.GetTemplate()
	if ref == nil || (ref.GetId() == "" && ref.GetName() == "") {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'template' must specify id or name")
	}
	resolved, err := resolveLockedFullResourceReference(ctx, s.templatesDao, catalogItemScope(candidate), ref,
		"cluster template", " in template", grpccodes.InvalidArgument)
	if err != nil {
		return nil, err
	}
	if err := validateResourceNotDeleted("cluster template", refKey(ref), " in template", resolved.GetMetadata()); err != nil {
		return nil, err
	}
	if current != nil {
		currentRef := current.GetTemplate()
		if currentRef == nil || currentRef.GetId() == "" {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "existing catalog item has no valid template reference")
		}
		if currentRef.GetId() != resolved.GetId() {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "cannot change template from '%s' to '%s': template is immutable", currentRef.GetName(), resolved.GetMetadata().GetName())
		}
	}
	if err := validateCatalogItemTemplateParameterPolicies(utils.ClusterTemplateAdapter{ClusterTemplate: resolved}, candidate.GetTemplateParameters()); err != nil {
		return nil, err
	}
	candidate.SetTemplate(canonicalClusterTemplateReference(resolved))
	return resolved, nil
}

func (s *PrivateClusterCatalogItemsServer) Delete(ctx context.Context,
	request *privatev1.ClusterCatalogItemsDeleteRequest) (response *privatev1.ClusterCatalogItemsDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateClusterCatalogItemsServer) Signal(ctx context.Context,
	request *privatev1.ClusterCatalogItemsSignalRequest) (response *privatev1.ClusterCatalogItemsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
