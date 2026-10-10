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

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/utils"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateComputeInstanceCatalogItemsServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.ComputeInstanceCatalogItemsServer = (*PrivateComputeInstanceCatalogItemsServer)(nil)

type PrivateComputeInstanceCatalogItemsServer struct {
	privatev1.UnimplementedComputeInstanceCatalogItemsServer
	generic           *GenericServer[*privatev1.ComputeInstanceCatalogItem]
	templatesDao      *dao.GenericDAO[*privatev1.ComputeInstanceTemplate]
	instanceTypesDao  *dao.GenericDAO[*privatev1.InstanceType]
	diskImagesDao     *dao.GenericDAO[*privatev1.DiskImage]
	secretsDao        *dao.GenericDAO[*privatev1.Secret]
	storageTiersDao   *dao.GenericDAO[*privatev1.StorageTier]
	subnetsDao        *dao.GenericDAO[*privatev1.Subnet]
	securityGroupsDao *dao.GenericDAO[*privatev1.SecurityGroup]
}

func NewPrivateComputeInstanceCatalogItemsServer() *PrivateComputeInstanceCatalogItemsServerBuilder {
	return &PrivateComputeInstanceCatalogItemsServerBuilder{}
}

func (b *PrivateComputeInstanceCatalogItemsServerBuilder) SetLogger(value *slog.Logger) *PrivateComputeInstanceCatalogItemsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateComputeInstanceCatalogItemsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateComputeInstanceCatalogItemsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateComputeInstanceCatalogItemsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateComputeInstanceCatalogItemsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateComputeInstanceCatalogItemsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateComputeInstanceCatalogItemsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf descriptor used to validate public CEL filters. When omitted, the private Catalog
// Item descriptor is used.
func (b *PrivateComputeInstanceCatalogItemsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateComputeInstanceCatalogItemsServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateComputeInstanceCatalogItemsServerBuilder) Build() (result *PrivateComputeInstanceCatalogItemsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	templatesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceTemplate]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	instanceTypesDao, err := dao.NewGenericDAO[*privatev1.InstanceType]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	diskImagesDao, err := dao.NewGenericDAO[*privatev1.DiskImage]().
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

	storageTiersDao, err := dao.NewGenericDAO[*privatev1.StorageTier]().
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

	generic, err := NewGenericServer[*privatev1.ComputeInstanceCatalogItem]().
		SetLogger(b.logger).
		SetService(privatev1.ComputeInstanceCatalogItems_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		AddAllowedTenants(auth.SharedTenant).
		Build()
	if err != nil {
		return
	}

	result = &PrivateComputeInstanceCatalogItemsServer{
		generic:           generic,
		templatesDao:      templatesDao,
		instanceTypesDao:  instanceTypesDao,
		diskImagesDao:     diskImagesDao,
		secretsDao:        secretsDao,
		storageTiersDao:   storageTiersDao,
		subnetsDao:        subnetsDao,
		securityGroupsDao: securityGroupsDao,
	}
	return
}

func (s *PrivateComputeInstanceCatalogItemsServer) List(ctx context.Context,
	request *privatev1.ComputeInstanceCatalogItemsListRequest) (response *privatev1.ComputeInstanceCatalogItemsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateComputeInstanceCatalogItemsServer) Get(ctx context.Context,
	request *privatev1.ComputeInstanceCatalogItemsGetRequest) (response *privatev1.ComputeInstanceCatalogItemsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateComputeInstanceCatalogItemsServer) Create(ctx context.Context,
	request *privatev1.ComputeInstanceCatalogItemsCreateRequest) (response *privatev1.ComputeInstanceCatalogItemsCreateResponse, err error) {
	var warnings []string
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response, func(ctx context.Context, current, candidate *privatev1.ComputeInstanceCatalogItem) error {
		var err error
		warnings, err = s.prepareCatalogItemCandidate(ctx, current, candidate)
		return err
	})
	if err == nil {
		response.SetWarnings(warnings)
	}
	return
}

func (s *PrivateComputeInstanceCatalogItemsServer) Update(ctx context.Context,
	request *privatev1.ComputeInstanceCatalogItemsUpdateRequest) (response *privatev1.ComputeInstanceCatalogItemsUpdateResponse, err error) {
	var warnings []string
	err = s.generic.UpdateWithCandidatePreparation(ctx, request, &response, func(ctx context.Context, current, candidate *privatev1.ComputeInstanceCatalogItem) error {
		var err error
		warnings, err = s.prepareCatalogItemCandidate(ctx, current, candidate)
		return err
	})
	if err == nil {
		response.SetWarnings(warnings)
	}
	return
}

// prepareCatalogItemCandidate checks the Catalog Item that Create or Update would store.
// GenericServer has assigned its tenant on Create or merged the update mask on Update, so
// references are checked against that complete item. Recheck dependencies when an offering
// changes or is published; descriptive edits and unpublishing need no new dependency lookup.
func (s *PrivateComputeInstanceCatalogItemsServer) prepareCatalogItemCandidate(
	ctx context.Context, current *privatev1.ComputeInstanceCatalogItem, candidate *privatev1.ComputeInstanceCatalogItem,
) ([]string, error) {
	if candidate == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "catalog item is mandatory")
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
			return nil, nil
		}
	}
	if err := s.validateAndCanonicalizeTemplate(ctx, current, candidate); err != nil {
		return nil, err
	}
	return validateAndCanonicalizeComputeInstanceCatalogItemPolicies(
		ctx, candidate, s.instanceTypesDao, s.diskImagesDao, s.secretsDao, s.storageTiersDao, s.subnetsDao, s.securityGroupsDao,
	)
}

// validateAndCanonicalizeTemplate finds the Template named by this Catalog Item. A name lookup
// starts in the item's tenant/project; project or shared selectors can choose another scope.
// It stores the Template's actual ID/name/scope, checks parameter policies against that
// Template, and forbids changing the Template on Update.
func (s *PrivateComputeInstanceCatalogItemsServer) validateAndCanonicalizeTemplate(
	ctx context.Context, current *privatev1.ComputeInstanceCatalogItem, candidate *privatev1.ComputeInstanceCatalogItem,
) error {
	ref := candidate.GetTemplate()
	if ref == nil || (ref.GetId() == "" && ref.GetName() == "") {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'template' must specify id or name")
	}
	resolved, err := resolveLockedFullResourceReference(ctx, s.templatesDao, catalogItemScope(candidate), ref,
		"compute instance template", " in template", grpccodes.InvalidArgument)
	if err != nil {
		return err
	}
	if err := validateResourceNotDeleted("compute instance template", refKey(ref), " in template", resolved.GetMetadata()); err != nil {
		return err
	}
	if current != nil {
		currentRef := current.GetTemplate()
		if currentRef == nil || currentRef.GetId() == "" {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "existing catalog item has no valid template reference")
		}
		if currentRef.GetId() != resolved.GetId() {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"cannot change template from '%s' to '%s': template is immutable", currentRef.GetName(), resolved.GetMetadata().GetName())
		}
	}
	if err := validateCatalogItemTemplateParameterPolicies(utils.ComputeInstanceTemplateAdapter{ComputeInstanceTemplate: resolved}, candidate.GetTemplateParameters()); err != nil {
		return err
	}
	candidate.SetTemplate(canonicalComputeInstanceTemplateReference(resolved))
	return nil
}

func (s *PrivateComputeInstanceCatalogItemsServer) Delete(ctx context.Context,
	request *privatev1.ComputeInstanceCatalogItemsDeleteRequest) (response *privatev1.ComputeInstanceCatalogItemsDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateComputeInstanceCatalogItemsServer) Signal(ctx context.Context,
	request *privatev1.ComputeInstanceCatalogItemsSignalRequest) (response *privatev1.ComputeInstanceCatalogItemsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
