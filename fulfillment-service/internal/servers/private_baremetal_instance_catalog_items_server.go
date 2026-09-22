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
	"maps"

	"github.com/prometheus/client_golang/prometheus"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
	"github.com/osac-project/osac/fulfillment-service/internal/utils"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateBareMetalInstanceCatalogItemsServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

var _ privatev1.BareMetalInstanceCatalogItemsServer = (*PrivateBareMetalInstanceCatalogItemsServer)(nil)

type PrivateBareMetalInstanceCatalogItemsServer struct {
	privatev1.UnimplementedBareMetalInstanceCatalogItemsServer
	generic                   *GenericServer[*privatev1.BareMetalInstanceCatalogItem]
	templatesDao              *dao.GenericDAO[*privatev1.BareMetalInstanceTemplate]
	bareMetalInstanceTypesDao *dao.GenericDAO[*privatev1.BareMetalInstanceType]
	diskImagesDao             *dao.GenericDAO[*privatev1.DiskImage]
	hostTypesDao              *dao.GenericDAO[*privatev1.HostType]
	subnetsDao                *dao.GenericDAO[*privatev1.Subnet]
	virtualNetworksDao        *dao.GenericDAO[*privatev1.VirtualNetwork]
	networkClassesDao         *dao.GenericDAO[*privatev1.NetworkClass]
	securityGroupsDao         *dao.GenericDAO[*privatev1.SecurityGroup]
}

func NewPrivateBareMetalInstanceCatalogItemsServer() *PrivateBareMetalInstanceCatalogItemsServerBuilder {
	return &PrivateBareMetalInstanceCatalogItemsServerBuilder{}
}

func (b *PrivateBareMetalInstanceCatalogItemsServerBuilder) SetLogger(value *slog.Logger) *PrivateBareMetalInstanceCatalogItemsServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateBareMetalInstanceCatalogItemsServerBuilder) SetNotifier(value events.Notifier) *PrivateBareMetalInstanceCatalogItemsServerBuilder {
	b.notifier = value
	return b
}

func (b *PrivateBareMetalInstanceCatalogItemsServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateBareMetalInstanceCatalogItemsServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateBareMetalInstanceCatalogItemsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateBareMetalInstanceCatalogItemsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateBareMetalInstanceCatalogItemsServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateBareMetalInstanceCatalogItemsServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf descriptor used to validate public CEL filters. When omitted, the private Catalog
// Item descriptor is used.
func (b *PrivateBareMetalInstanceCatalogItemsServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateBareMetalInstanceCatalogItemsServerBuilder {
	b.filterDesc = value
	return b
}

func (b *PrivateBareMetalInstanceCatalogItemsServerBuilder) Build() (result *PrivateBareMetalInstanceCatalogItemsServer, err error) {
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

	templatesDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceTemplate]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	bareMetalInstanceTypesDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceType]().
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
	hostTypesDao, err := dao.NewGenericDAO[*privatev1.HostType]().
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
	virtualNetworksDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}
	networkClassesDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
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

	generic, err := NewGenericServer[*privatev1.BareMetalInstanceCatalogItem]().
		SetLogger(b.logger).
		SetService(privatev1.BareMetalInstanceCatalogItems_ServiceDesc.ServiceName).
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

	result = &PrivateBareMetalInstanceCatalogItemsServer{
		generic:                   generic,
		templatesDao:              templatesDao,
		bareMetalInstanceTypesDao: bareMetalInstanceTypesDao,
		diskImagesDao:             diskImagesDao,
		hostTypesDao:              hostTypesDao,
		subnetsDao:                subnetsDao,
		virtualNetworksDao:        virtualNetworksDao,
		networkClassesDao:         networkClassesDao,
		securityGroupsDao:         securityGroupsDao,
	}
	return
}

func (s *PrivateBareMetalInstanceCatalogItemsServer) List(ctx context.Context,
	request *privatev1.BareMetalInstanceCatalogItemsListRequest) (response *privatev1.BareMetalInstanceCatalogItemsListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstanceCatalogItemsServer) Get(ctx context.Context,
	request *privatev1.BareMetalInstanceCatalogItemsGetRequest) (response *privatev1.BareMetalInstanceCatalogItemsGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstanceCatalogItemsServer) Create(ctx context.Context,
	request *privatev1.BareMetalInstanceCatalogItemsCreateRequest) (response *privatev1.BareMetalInstanceCatalogItemsCreateResponse, err error) {
	var warnings []string
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response, func(ctx context.Context, current, candidate *privatev1.BareMetalInstanceCatalogItem) error {
		var err error
		warnings, err = s.prepareCatalogItemCandidate(ctx, current, candidate)
		return err
	})
	if err == nil {
		response.SetWarnings(warnings)
	}
	return
}

func (s *PrivateBareMetalInstanceCatalogItemsServer) Update(ctx context.Context,
	request *privatev1.BareMetalInstanceCatalogItemsUpdateRequest) (response *privatev1.BareMetalInstanceCatalogItemsUpdateResponse, err error) {
	var warnings []string
	err = s.generic.UpdateWithCandidatePreparation(ctx, request, &response, func(ctx context.Context, current, candidate *privatev1.BareMetalInstanceCatalogItem) error {
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
func (s *PrivateBareMetalInstanceCatalogItemsServer) prepareCatalogItemCandidate(
	ctx context.Context, current *privatev1.BareMetalInstanceCatalogItem, candidate *privatev1.BareMetalInstanceCatalogItem,
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
	template, err := s.validateAndCanonicalizeTemplate(ctx, current, candidate)
	if err != nil {
		return nil, err
	}
	return validateAndCanonicalizeBareMetalInstanceCatalogItemPolicies(ctx, s.generic.logger, candidate, template,
		s.bareMetalInstanceTypesDao, s.diskImagesDao, s.hostTypesDao, s.subnetsDao,
		s.virtualNetworksDao, s.networkClassesDao, s.securityGroupsDao)
}

// validateAndCanonicalizeTemplate finds the Template named by this Catalog Item. A name lookup
// starts in the item's tenant/project; project or shared selectors can choose another scope.
// It stores the Template's actual ID/name/scope, checks parameter policies against that
// Template, and forbids changing the Template on Update.
func (s *PrivateBareMetalInstanceCatalogItemsServer) validateAndCanonicalizeTemplate(
	ctx context.Context, current *privatev1.BareMetalInstanceCatalogItem, candidate *privatev1.BareMetalInstanceCatalogItem,
) (*privatev1.BareMetalInstanceTemplate, error) {
	ref := candidate.GetTemplate()
	if ref == nil || (ref.GetId() == "" && ref.GetName() == "") {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "field 'template' must specify id or name")
	}
	resolved, err := resolveLockedFullResourceReference(ctx, s.templatesDao, catalogItemScope(candidate), ref,
		"bare metal instance template", " in template", grpccodes.InvalidArgument)
	if err != nil {
		return nil, err
	}
	if err := validateResourceNotDeleted("bare metal instance template", refKey(ref), " in template", resolved.GetMetadata()); err != nil {
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
	if err := validateCatalogItemTemplateParameterPolicies(utils.BareMetalInstanceTemplateAdapter{BareMetalInstanceTemplate: resolved}, candidate.GetTemplateParameters()); err != nil {
		return nil, err
	}
	candidate.SetTemplate(canonicalBareMetalInstanceTemplateReference(resolved))
	return resolved, nil
}

func (s *PrivateBareMetalInstanceCatalogItemsServer) Delete(ctx context.Context,
	request *privatev1.BareMetalInstanceCatalogItemsDeleteRequest) (response *privatev1.BareMetalInstanceCatalogItemsDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateBareMetalInstanceCatalogItemsServer) Signal(ctx context.Context,
	request *privatev1.BareMetalInstanceCatalogItemsSignalRequest) (response *privatev1.BareMetalInstanceCatalogItemsSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}
