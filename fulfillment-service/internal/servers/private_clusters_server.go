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
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/bits-and-blooms/bitset"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/exp/maps"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/references"
	"github.com/osac-project/osac/fulfillment-service/internal/utils"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type PrivateClustersServerBuilder struct {
	logger              *slog.Logger
	attributionLogic    auth.AttributionLogic
	tenancyLogic        auth.TenancyLogic
	metricsRegisterer   prometheus.Registerer
	filterDesc          protoreflect.MessageDescriptor
	addOnOperatorLookup references.ReferenceLookupFunc
}

var _ privatev1.ClustersServer = (*PrivateClustersServer)(nil)

type PrivateClustersServer struct {
	privatev1.UnimplementedClustersServer
	logger                    *slog.Logger
	tenancyLogic              auth.TenancyLogic
	templatesDao              *dao.GenericDAO[*privatev1.ClusterTemplate]
	catalogItemsDao           *dao.GenericDAO[*privatev1.ClusterCatalogItem]
	hostTypesDao              *dao.GenericDAO[*privatev1.HostType]
	bareMetalInstanceTypesDao *dao.GenericDAO[*privatev1.BareMetalInstanceType]
	clusterVersionsDao        *dao.GenericDAO[*privatev1.ClusterVersion]
	subnetsDao                *dao.GenericDAO[*privatev1.Subnet]
	securityGroupsDao         *dao.GenericDAO[*privatev1.SecurityGroup]
	externalIPPoolDao         *dao.GenericDAO[*privatev1.ExternalIPPool]
	externalIPDao             *dao.GenericDAO[*privatev1.ExternalIP]
	externalIPAttachmentDao   *dao.GenericDAO[*privatev1.ExternalIPAttachment]
	secretsDao                *dao.GenericDAO[*privatev1.Secret]
	addOnOperatorLookup       references.ReferenceLookupFunc
	generic                   *GenericServer[*privatev1.Cluster]
	lifecycle                 *externalIPLifecycle
}

func NewPrivateClustersServer() *PrivateClustersServerBuilder {
	return &PrivateClustersServerBuilder{}
}

func (b *PrivateClustersServerBuilder) SetLogger(value *slog.Logger) *PrivateClustersServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateClustersServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *PrivateClustersServerBuilder {
	b.attributionLogic = value
	return b
}

func (b *PrivateClustersServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateClustersServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *PrivateClustersServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *PrivateClustersServerBuilder {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter
// expressions. This is optional. When unset, the descriptor of this server's own private message type is used.
func (b *PrivateClustersServerBuilder) SetFilterDesc(value protoreflect.MessageDescriptor) *PrivateClustersServerBuilder {
	b.filterDesc = value
	return b
}

// SetAddOnOperatorLookup sets the resolver used for add-on operator references.
func (b *PrivateClustersServerBuilder) SetAddOnOperatorLookup(value references.ReferenceLookupFunc) *PrivateClustersServerBuilder {
	b.addOnOperatorLookup = value
	return b
}

func (b *PrivateClustersServerBuilder) Build() (result *PrivateClustersServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}
	// Create the templates DAO:
	templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the catalog items DAO:
	catalogItemsDao, err := dao.NewGenericDAO[*privatev1.ClusterCatalogItem]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	addOnOperatorLookup := b.addOnOperatorLookup
	if addOnOperatorLookup == nil {
		// Create the private add-on operators DAO:
		addOnOperatorsDao, daoErr := dao.NewGenericDAO[*privatev1.AddOnOperator]().
			SetLogger(b.logger).
			SetTenancyLogic(b.tenancyLogic).
			SetMetricsRegisterer(b.metricsRegisterer).
			Build()
		if daoErr != nil {
			err = daoErr
			return
		}
		addOnOperatorLookup = references.NewDAOLookupFunc(addOnOperatorsDao)
	}

	// Create the host types DAO:
	hostTypesDao, err := dao.NewGenericDAO[*privatev1.HostType]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the bare metal instance types DAO:
	bareMetalInstanceTypesDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceType]().
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

	// Create the subnets DAO:
	subnetsDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the ExternalIP DAOs:
	externalIPPoolDaoBuilder := dao.NewGenericDAO[*privatev1.ExternalIPPool]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer)
	externalIPPoolDao, err := externalIPPoolDaoBuilder.Build()
	if err != nil {
		return
	}

	// Create the security groups DAO:
	securityGroupsDao, err := dao.NewGenericDAO[*privatev1.SecurityGroup]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	externalIPDaoBuilder := dao.NewGenericDAO[*privatev1.ExternalIP]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer)
	externalIPDao, err := externalIPDaoBuilder.Build()
	if err != nil {
		return
	}

	externalIPAttachmentDaoBuilder := dao.NewGenericDAO[*privatev1.ExternalIPAttachment]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer)
	externalIPAttachmentDao, err := externalIPAttachmentDaoBuilder.Build()
	if err != nil {
		return
	}

	// Create the secrets DAO:
	secretsDao, err := dao.NewGenericDAO[*privatev1.Secret]().
		SetLogger(b.logger).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create the generic server:
	// TODO(OSAC-1060): Remove AddAllowedTenants(auth.SharedTenant) once the osac-operator storage
	// controller handles CaaS cluster deletion independently of tenant-level storage provisioning.
	// CaaS e2e tests use SA auth → shared tenant to avoid cluster-storage finalizer blocking deletion
	// when AAP storage provisioning jobs fail in test environments without configured backends.
	generic, err := NewGenericServer[*privatev1.Cluster]().
		SetLogger(b.logger).
		SetService(privatev1.Clusters_ServiceDesc.ServiceName).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetFilterDesc(b.filterDesc).
		AddAllowedTenants(auth.SharedTenant).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &PrivateClustersServer{
		logger:                    b.logger,
		tenancyLogic:              b.tenancyLogic,
		templatesDao:              templatesDao,
		catalogItemsDao:           catalogItemsDao,
		hostTypesDao:              hostTypesDao,
		bareMetalInstanceTypesDao: bareMetalInstanceTypesDao,
		clusterVersionsDao:        clusterVersionsDao,
		subnetsDao:                subnetsDao,
		securityGroupsDao:         securityGroupsDao,
		externalIPPoolDao:         externalIPPoolDao,
		externalIPDao:             externalIPDao,
		externalIPAttachmentDao:   externalIPAttachmentDao,
		secretsDao:                secretsDao,
		addOnOperatorLookup:       addOnOperatorLookup,
		generic:                   generic,
	}
	result.lifecycle = newExternalIPLifecycle(
		externalIPDao,
		externalIPAttachmentDao,
		nil,
		externalIPPoolDao,
		nil,
		generic.dao,
		nil,
		nil,
	)
	return
}

func (s *PrivateClustersServer) List(ctx context.Context,
	request *privatev1.ClustersListRequest) (response *privatev1.ClustersListResponse, err error) {
	err = s.generic.List(ctx, request, &response)
	return
}

func (s *PrivateClustersServer) Get(ctx context.Context,
	request *privatev1.ClustersGetRequest) (response *privatev1.ClustersGetResponse, err error) {
	err = s.generic.Get(ctx, request, &response)
	return
}

func (s *PrivateClustersServer) Create(ctx context.Context, request *privatev1.ClustersCreateRequest) (response *privatev1.ClustersCreateResponse, err error) {
	err = s.generic.CreateWithCandidatePreparation(ctx, request, &response, func(ctx context.Context, _ *privatev1.Cluster, candidate *privatev1.Cluster) error {
		return s.prepareCreate(ctx, candidate)
	})
	if err != nil {
		return
	}
	if !isDryRun(ctx) && response.GetObject().GetSpec().GetAutoExternalIpAttachment() {
		err = s.autoProvisionExternalIPs(ctx, response.GetObject())
		if err != nil {
			if tx, txErr := database.TxFromContext(ctx); txErr == nil {
				tx.ReportError(&err)
			}
			return
		}
	}
	return
}

// prepareCreate fills the new Cluster before it is stored. It selects either a published
// Catalog Item or a direct Template, applies Catalog rules and Template defaults, then
// checks the resulting network, node sets, and other required inputs.
func (s *PrivateClustersServer) prepareCreate(ctx context.Context, candidate *privatev1.Cluster) (err error) {
	// Ensure sane defaults:
	s.setDefaults(candidate)
	if err = s.resolveAddOnOperators(ctx, candidate); err != nil {
		return
	}

	// Get the spec:
	spec := candidate.GetSpec()

	// Validate duplicate conditions first:
	err = s.validateNoDuplicateConditions(candidate)
	if err != nil {
		return
	}

	template, err := s.resolveCreationSource(ctx, candidate)
	if err != nil {
		return err
	}
	if err = s.applyClusterTemplate(ctx, candidate, template); err != nil {
		return
	}
	if key := spec.GetSshPublicKey(); key != "" {
		if err = validateOpenSSHPublicKey(key); err != nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "spec.ssh_public_key: %s", err)
		}
	}

	if candidate.GetSpec().GetNetworkAttachment() == nil {
		if err = s.injectDefaultNetworkAttachment(ctx, candidate); err != nil {
			return
		}
	}

	if err = s.validateNetworkAttachmentState(ctx, candidate); err != nil {
		return
	}

	// Resolve fabric_interface for each node set when the cluster has a
	// network attachment. The BareMetalInstanceType's network_ports list
	// is searched for the first port with role "fabric".
	if spec.GetNetworkAttachment() != nil {
		if err = s.resolveFabricInterfaces(ctx, spec); err != nil {
			return
		}
	}

	return
}

func (s *PrivateClustersServer) resolveAddOnOperators(ctx context.Context, cluster *privatev1.Cluster) error {
	metadata := cluster.GetMetadata()
	for index, ref := range cluster.GetSpec().GetAddOnOperators() {
		if ref == nil || (ref.GetId() == "" && ref.GetName() == "") {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"spec.add_on_operators[%d] must specify id or name", index)
		}

		resolved, err := s.addOnOperatorLookup(ctx, metadata.GetTenant(), metadata.GetProject(), ref.GetId(), ref.GetName())
		if err != nil {
			var notFound interface{ IsNotFound() bool }
			if errors.As(err, &notFound) && notFound.IsNotFound() {
				return grpcstatus.Errorf(grpccodes.InvalidArgument,
					"add-on operator %q referenced by spec.add_on_operators[%d] was not found",
					refKey(ref), index)
			}
			return grpcstatus.Errorf(grpccodes.Internal,
				"failed to resolve spec.add_on_operators[%d]", index)
		}
		if resolved == nil {
			return grpcstatus.Errorf(grpccodes.Internal,
				"failed to resolve spec.add_on_operators[%d]", index)
		}

		ref.SetId(resolved.ID)
		ref.SetName(resolved.Name)
	}
	return nil
}

// resolveCreationSource accepts exactly one provisioning source: spec.catalog_item or
// spec.template. For a Catalog Item it finds the item's Template and applies its field rules;
// for a direct Template it resolves that reference under the Cluster's assigned tenant/project.
func (s *PrivateClustersServer) resolveCreationSource(ctx context.Context,
	candidate *privatev1.Cluster) (*privatev1.ClusterTemplate, error) {
	spec := candidate.GetSpec()
	if spec.GetCatalogItem() != nil && spec.GetTemplate() != nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"catalog_item and template are mutually exclusive")
	}
	if spec.GetCatalogItem() != nil {
		return s.resolveCatalogItem(ctx, candidate)
	}
	if spec.GetTemplate() == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "template is mandatory")
	}
	return resolveAndCanonicalizeReference(ctx, s.templatesDao, candidate.GetMetadata(), spec.GetTemplate(),
		"template", grpccodes.InvalidArgument)
}

func (s *PrivateClustersServer) Update(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) (response *privatev1.ClustersUpdateResponse, err error) {
	err = s.validateNoDuplicateConditions(request.GetObject())
	if err != nil {
		return
	}
	err = s.validateClusterStateForSpecUpdate(ctx, request)
	if err != nil {
		return
	}
	// Legacy upgrade validation may normalize version; never mutate the caller's request.
	request = proto.Clone(request).(*privatev1.ClustersUpdateRequest)
	err = s.validateVersionUpdate(ctx, request)
	if err != nil {
		return
	}
	err = s.validateNodeSetsUpdate(ctx, request)
	if err != nil {
		return
	}
	err = s.validateNetworkAttachmentImmutability(ctx, request)
	if err != nil {
		return
	}
	err = s.validateAutoExternalIPImmutability(ctx, request)
	if err != nil {
		return
	}
	err = s.validateSpecUpdateRequest(request)
	if err != nil {
		return
	}

	err = s.generic.UpdateWithCandidatePreparation(ctx, request, &response, func(ctx context.Context, current *privatev1.Cluster, candidate *privatev1.Cluster) error {
		if err := validateClusterTemplateImmutability(current, candidate, request.GetUpdateMask()); err != nil {
			return err
		}
		if updateIncludesField(request.GetUpdateMask(), "spec.ssh_public_key") {
			if key := candidate.GetSpec().GetSshPublicKey(); key != "" {
				if err := validateOpenSSHPublicKey(key); err != nil {
					return grpcstatus.Errorf(grpccodes.InvalidArgument, "spec.ssh_public_key: %s", err)
				}
			}
		}
		if err := s.validateAddOnOperatorImmutability(current, candidate, request.GetUpdateMask()); err != nil {
			return err
		}
		if err := utils.ValidateClusterSpecFields(candidate.GetSpec()); err != nil {
			return err
		}
		if updateIncludesField(request.GetUpdateMask(), "spec.network_attachment.security_groups") {
			if err := s.validateNetworkAttachmentState(ctx, candidate); err != nil {
				return err
			}
		}
		if !proto.Equal(current.GetSpec().GetPullSecretSecret(), candidate.GetSpec().GetPullSecretSecret()) {
			ref := candidate.GetSpec().GetPullSecretSecret()
			allowShared := ref.GetId() != "" && ref.GetId() == current.GetSpec().GetPullSecretSecret().GetId()
			if err := s.validatePullSecretSecret(ctx, candidate, allowShared); err != nil {
				return err
			}
		}
		return nil
	})
	return
}

func (s *PrivateClustersServer) validateSpecUpdateRequest(request *privatev1.ClustersUpdateRequest) error {
	updateMask := request.GetUpdateMask()
	if updateMask != nil && len(updateMask.GetPaths()) > 0 &&
		updateIncludesField(updateMask, "spec") && request.GetObject().GetSpec() == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "object and spec are required")
	}
	return nil
}

func (s *PrivateClustersServer) Delete(ctx context.Context,
	request *privatev1.ClustersDeleteRequest) (response *privatev1.ClustersDeleteResponse, err error) {
	id := request.GetId()
	if id != "" {
		getResponse, getErr := s.generic.dao.Get().SetId(id).Do(ctx)
		if getErr != nil {
			var notFoundErr *dao.ErrNotFound
			if !errors.As(getErr, &notFoundErr) {
				err = getErr
				return
			}
		} else if getResponse.GetObject().GetSpec().GetAutoExternalIpAttachment() {
			err = s.autoCleanupExternalIP(ctx, id)
			if err != nil {
				return
			}
		}
	}
	err = s.generic.Delete(ctx, request, &response)
	return
}

func (s *PrivateClustersServer) autoCleanupExternalIP(ctx context.Context, clusterID string) error {
	filter := fmt.Sprintf(
		"this.metadata.labels['%s'] == '%s'",
		autoCreatedForLabel, clusterID,
	)
	listResp, err := s.externalIPAttachmentDao.List().SetFilter(filter).Do(ctx)
	if err != nil {
		return fmt.Errorf("auto_external_ip_attachment cleanup: failed to list attachments: %w", err)
	}

	for _, attachment := range listResp.GetItems() {
		attachmentID := attachment.GetId()
		eipRef := attachment.GetSpec().GetExternalIp()
		eipID := refKey(eipRef)

		if eipID != "" {
			err = s.lifecycle.deleteAttachmentAndExternalIP(ctx, attachmentID, eipID)
		} else {
			err = s.lifecycle.deleteAttachment(ctx, attachmentID)
		}
		if err != nil {
			return fmt.Errorf("auto_external_ip_attachment cleanup: %w", err)
		}
	}

	return nil
}

func (s *PrivateClustersServer) Signal(ctx context.Context,
	request *privatev1.ClustersSignalRequest) (response *privatev1.ClustersSignalResponse, err error) {
	err = s.generic.Signal(ctx, request, &response)
	return
}

func (s *PrivateClustersServer) setDefaults(cluster *privatev1.Cluster) {
	if !cluster.HasSpec() {
		cluster.SetSpec(&privatev1.ClusterSpec{})
	}
	if !cluster.HasStatus() {
		cluster.SetStatus(&privatev1.ClusterStatus{})
	}
}

func (s *PrivateClustersServer) validatePullSecretSecret(
	ctx context.Context, cluster *privatev1.Cluster, allowShared bool,
) error {
	ref := cluster.GetSpec().GetPullSecretSecret()
	if ref == nil {
		return nil
	}
	if ref.GetId() == "" && ref.GetName() == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "pull_secret_secret must specify id or name")
	}
	identifier := refKey(ref)
	metadata := cluster.GetMetadata()
	var secret *privatev1.Secret
	var err error
	if allowShared {
		sharedMetadata := privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build()
		secret, err = resolveAndCanonicalizeReference(ctx, s.secretsDao, sharedMetadata, ref, "pull_secret_secret", grpccodes.NotFound)
		if err != nil && grpcstatus.Code(err) != grpccodes.NotFound {
			return err
		}
	}
	if secret == nil {
		secret, err = resolveAndCanonicalizeReference(ctx, s.secretsDao, metadata, ref, "pull_secret_secret", grpccodes.InvalidArgument)
		if err != nil {
			return err
		}
	}
	return validateResolvedSecretLifecycleAndType(secret, identifier, "pull_secret_secret",
		privatev1.SecretType_SECRET_TYPE_PULL_SECRET)
}

func (s *PrivateClustersServer) lookupBareMetalInstanceType(ctx context.Context,
	key string) (result *privatev1.BareMetalInstanceType, err error) {
	if key == "" {
		return
	}
	response, err := s.bareMetalInstanceTypesDao.List().
		SetFilter(fmt.Sprintf("this.id == %[1]s || this.metadata.name == %[1]s", strconv.Quote(key))).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			err = grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
		}
		return
	}
	switch response.GetTotal() {
	case 0:
		err = grpcstatus.Errorf(
			grpccodes.NotFound,
			"there is no bare metal instance type with identifier or name '%s'",
			key,
		)
	case 1:
		result = response.GetItems()[0]
	default:
		err = grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"there are multiple bare metal instance types with identifier or name '%s'",
			key,
		)
	}
	return
}

// ensureClusterVersion makes sure the cluster spec has a usable version reference: if the user didn't provide one, it
// resolves the system default. Either way, it validates that the resulting ClusterVersion isn't deleted, disabled,
// or obsolete.
func (s *PrivateClustersServer) ensureClusterVersion(ctx context.Context, cluster *privatev1.Cluster) error {
	versionRef := cluster.GetSpec().GetVersion()
	if versionRef != nil {
		version, err := resolveAndCanonicalizeReference(ctx, s.clusterVersionsDao, cluster.GetMetadata(), versionRef, "version", grpccodes.InvalidArgument)
		if err != nil {
			return err
		}
		return validateResolvedClusterVersion(version, version.GetMetadata().GetName(), "")
	}
	ref, err := resolveDefaultClusterVersion(ctx, s.logger, s.clusterVersionsDao)
	if err != nil {
		return err
	}
	cluster.GetSpec().SetVersion(ref)
	return nil
}

func (s *PrivateClustersServer) validateNoDuplicateConditions(object *privatev1.Cluster) error {
	conditions := object.GetStatus().GetConditions()
	if conditions == nil {
		return nil
	}
	conditionTypes := &bitset.BitSet{}
	for _, condition := range conditions {
		conditionType := condition.GetType()
		if conditionTypes.Test(uint(conditionType)) { // #nosec G115 -- proto enum, non-negative
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"condition '%s' is duplicated",
				conditionType.String(),
			)
		}
		conditionTypes.Set(uint(conditionType)) // #nosec G115 -- proto enum, non-negative
	}
	return nil
}

// validateNodeSetsUpdate validates that changes to node_sets are allowed.
// It delegates to specific validators for different aspects of the validation.
func (s *PrivateClustersServer) validateNodeSetsUpdate(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) error {
	// Check if the update affects node_sets at all:
	if !s.updateAffectsNodeSets(request.GetUpdateMask()) {
		// Update doesn't touch node_sets, no validation needed
		return nil
	}

	// Check if only size fields are being updated - these are always allowed
	if s.isUpdatingOnlySizes(request.GetUpdateMask()) {
		return nil
	}

	// Fetch the existing cluster from the database:
	existingCluster, found, err := s.getExistingCluster(ctx, request)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	// Get the node sets from both clusters:
	existingNodeSets := existingCluster.GetSpec().GetNodeSets()
	newNodeSets := request.GetObject().GetSpec().GetNodeSets()

	// Run specific validations:
	if err := s.validateAtLeastOneNodeSet(newNodeSets); err != nil {
		return err
	}
	if err := s.validateNodeSetHostTypeImmutability(existingNodeSets, newNodeSets); err != nil {
		return err
	}

	return nil
}

// getExistingCluster fetches the existing cluster from the database.
// Returns the cluster, a boolean indicating if it was found, and any error that occurred.
func (s *PrivateClustersServer) getExistingCluster(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) (*privatev1.Cluster, bool, error) {
	cluster := request.GetObject()
	if cluster == nil {
		return nil, false, nil
	}
	id := cluster.GetId()
	if id == "" {
		return nil, false, nil
	}
	getResponse, err := s.generic.dao.Get().
		SetId(id).
		Do(ctx)
	if err != nil {
		return nil, false, err
	}
	existingCluster := getResponse.GetObject()
	return existingCluster, true, nil
}

// validateClusterStateForSpecUpdate rejects spec modifications when the cluster is in a
// terminal or non-reconcilable state. The reconciler processes UNSPECIFIED (by transitioning
// to PROGRESSING via setDefaults), PROGRESSING, and READY clusters — it returns nil for
// every other state — so spec changes outside those states would be silently ignored.
//
// This uses SetLock(true) (SELECT ... FOR UPDATE) so the row lock is held for the
// remainder of the transaction, preventing a concurrent status transition from
// bypassing the check before GenericServer.Update writes the spec changes.
func (s *PrivateClustersServer) validateClusterStateForSpecUpdate(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) error {
	if !updateIncludesField(request.GetUpdateMask(), "spec") {
		return nil
	}
	cluster := request.GetObject()
	if cluster == nil {
		return nil
	}
	id := cluster.GetId()
	if id == "" {
		return nil
	}
	getResponse, err := s.generic.dao.Get().
		SetId(id).
		SetLock(true).
		Do(ctx)
	if err != nil {
		return err
	}
	existingCluster := getResponse.GetObject()
	state := existingCluster.GetStatus().GetState()
	if state != privatev1.ClusterState_CLUSTER_STATE_UNSPECIFIED &&
		state != privatev1.ClusterState_CLUSTER_STATE_PROGRESSING &&
		state != privatev1.ClusterState_CLUSTER_STATE_READY {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"cannot update cluster spec when cluster state is %s",
			state,
		)
	}
	return nil
}

// updateAffectsNodeSets checks if the update mask indicates that node_sets are being modified.
func (s *PrivateClustersServer) updateAffectsNodeSets(updateMask *fieldmaskpb.FieldMask) bool {
	return updateIncludesField(updateMask, "spec.node_sets")
}

// isUpdatingOnlySizes checks if the update mask is only modifying size fields of node sets.
func (s *PrivateClustersServer) isUpdatingOnlySizes(updateMask *fieldmaskpb.FieldMask) bool {
	for _, path := range updateMask.GetPaths() {
		if strings.HasPrefix(path, "spec.node_sets") {
			if !strings.HasSuffix(path, ".size") {
				return false
			}
		}
	}
	return true
}

// validateAtLeastOneNodeSet ensures that clusters always have at least one node set.
func (s *PrivateClustersServer) validateAtLeastOneNodeSet(nodeSets map[string]*privatev1.ClusterNodeSet) error {
	if len(nodeSets) == 0 {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"cannot remove the last node set: clusters must have at least one node set",
		)
	}
	return nil
}

// validateClusterNodeSetMap checks that a node-set map has at least one entry and that
// every entry has the fields required after defaults and references have been resolved.
func validateClusterNodeSetMap(nodeSets map[string]*privatev1.ClusterNodeSet) error {
	if len(nodeSets) == 0 {
		return fmt.Errorf("must contain at least one node set")
	}
	for name, nodeSet := range nodeSets {
		if nodeSet == nil {
			return fmt.Errorf("node set '%s' must not be null", name)
		}
		if !nodeSet.HasSize() {
			return fmt.Errorf("size for node set '%s' is required", name)
		}
		if nodeSet.GetSize() <= 0 {
			return fmt.Errorf("size for node set '%s' should be greater than zero, but it is %d", name, nodeSet.GetSize())
		}
	}
	return nil
}

// validateNodeSetHostTypeImmutability ensures that the baremetal_instance_type field of existing node sets
// cannot be changed. This is an existing documented restriction in the API specification.
func (s *PrivateClustersServer) validateNodeSetHostTypeImmutability(
	existingNodeSets map[string]*privatev1.ClusterNodeSet,
	newNodeSets map[string]*privatev1.ClusterNodeSet) error {
	for nodeSetName, existingNodeSet := range existingNodeSets {
		newNodeSet, exists := newNodeSets[nodeSetName]
		if !exists {
			// Node set is being removed, which is allowed (if at least one remains)
			continue
		}
		existingBMIT := existingNodeSet.GetBaremetalInstanceType()
		newBMIT := newNodeSet.GetBaremetalInstanceType()
		if refKey(existingBMIT) != refKey(newBMIT) {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"cannot change baremetal_instance_type for node set '%s' from '%s' to '%s': baremetal_instance_type is immutable",
				nodeSetName,
				refKey(existingBMIT),
				refKey(newBMIT),
			)
		}
	}
	return nil
}

// validateClusterTemplateImmutability checks that Update keeps the Cluster's original Template
// and parameters. It also preserves the stored spec.catalog_item as history, so a normal Update
// works after that Catalog Item is deleted without reading the deleted item.
func validateClusterTemplateImmutability(current, candidate *privatev1.Cluster, mask *fieldmaskpb.FieldMask) error {
	oldSpec, newSpec := current.GetSpec(), candidate.GetSpec()
	// Preserve the legacy unmasked Update behavior for omitted immutable inputs.
	// An explicit mask, including a parent mask, still makes clearing an error.
	if mask == nil {
		if newSpec == nil {
			newSpec = &privatev1.ClusterSpec{}
			candidate.SetSpec(newSpec)
		}
		if newSpec.GetTemplate() == nil {
			newSpec.SetTemplate(cloneMessage(oldSpec.GetTemplate()))
		}
		if len(newSpec.GetTemplateParameters()) == 0 {
			newSpec.SetTemplateParameters(cloneMessage(oldSpec).GetTemplateParameters())
		}
	}
	if updateIncludesField(mask, "spec.template") && refKey(oldSpec.GetTemplate()) != refKey(newSpec.GetTemplate()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "cannot change spec.template from '%s' to '%s': template is immutable", refKey(oldSpec.GetTemplate()), refKey(newSpec.GetTemplate()))
	}
	if updateIncludesField(mask, "spec.template_parameters") && !maps.EqualFunc(oldSpec.GetTemplateParameters(), newSpec.GetTemplateParameters(), func(a, b *anypb.Any) bool { return proto.Equal(a, b) }) { //nolint:govet // inline: type parameter inference is not supported yet
		return grpcstatus.Error(grpccodes.InvalidArgument, "cannot change spec.template_parameters: template parameters are immutable")
	}
	if updateIncludesField(mask, "spec.catalog_item") {
		ref, err := preserveCatalogItemProvenance(oldSpec.GetCatalogItem(), newSpec.GetCatalogItem(), mask)
		if err != nil {
			return err
		}
		if newSpec != nil {
			newSpec.SetCatalogItem(ref)
		}
	}

	return nil
}

// validateAddOnOperatorImmutability validates and preserves the canonical operator references during updates.
func (s *PrivateClustersServer) validateAddOnOperatorImmutability(
	current, candidate *privatev1.Cluster, mask *fieldmaskpb.FieldMask,
) error {
	currentOperators := current.GetSpec().GetAddOnOperators()
	candidateOperators := candidate.GetSpec().GetAddOnOperators()
	cloneCurrent := func() []*privatev1.AddOnOperatorReference {
		result := make([]*privatev1.AddOnOperatorReference, len(currentOperators))
		for i, ref := range currentOperators {
			result[i] = cloneMessage(ref)
		}
		return result
	}

	if !updateIncludesField(mask, "spec.add_on_operators") || (mask == nil && len(candidateOperators) == 0) {
		candidate.GetSpec().SetAddOnOperators(cloneCurrent())
		return nil
	}
	if len(currentOperators) != len(candidateOperators) {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"cannot change spec.add_on_operators: add-on operators are immutable",
		)
	}

	for i, ref := range candidateOperators {
		stored := currentOperators[i]
		if err := validateImmutableReferenceIdentity(
			stored, ref, fmt.Sprintf("spec.add_on_operators[%d]", i), "add-on operators", true,
		); err != nil {
			return err
		}
	}
	candidate.GetSpec().SetAddOnOperators(cloneCurrent())
	return nil
}

// validateVersionUpdate ensures version stays valid on update. Absent
// version is normalized to the existing value (cannot be cleared once set).
// Changed values are validated the same way as Create via ensureClusterVersion.
func (s *PrivateClustersServer) validateVersionUpdate(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) error {
	if !updateIncludesField(request.GetUpdateMask(), "spec.version") {
		return nil
	}
	newSpec := request.GetObject().GetSpec()
	if newSpec == nil {
		return nil
	}
	existing, found, err := s.getExistingCluster(ctx, request)
	if err != nil || !found {
		return err
	}
	newRef := newSpec.GetVersion()
	existingRef := existing.GetSpec().GetVersion()
	if newRef == nil || (newRef.GetName() == "" && newRef.GetId() == "") {
		if existingRef != nil {
			newSpec.SetVersion(existingRef)
		}
		return nil
	}
	if existingRef != nil && newRef.GetName() == existingRef.GetName() {
		return nil
	}
	version, err := resolveAndCanonicalizeReference(ctx, s.clusterVersionsDao, existing.GetMetadata(), newRef, "version", grpccodes.InvalidArgument)
	if err != nil {
		return err
	}
	return validateResolvedClusterVersion(version, version.GetMetadata().GetName(), "")
}

// validateNetworkAttachmentImmutability ensures that the subnet field within
// network_attachment cannot be changed after cluster creation.
func (s *PrivateClustersServer) validateNetworkAttachmentImmutability(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) error {
	updateMask := request.GetUpdateMask()

	if !updateIncludesField(updateMask, "spec.network_attachment.subnet") {
		return nil
	}

	existingCluster, found, err := s.getExistingCluster(ctx, request)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	existingSubnet := existingCluster.GetSpec().GetNetworkAttachment().GetSubnet()
	newSubnet := request.GetObject().GetSpec().GetNetworkAttachment().GetSubnet()

	if refKey(existingSubnet) != refKey(newSubnet) {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"cannot change spec.network_attachment.subnet from '%s' to '%s': subnet is immutable",
			refKey(existingSubnet), refKey(newSubnet),
		)
	}

	return nil
}

// resolveTargetTenant returns the tenant from the cluster metadata, falling
// back to the caller's default tenant when not explicitly set.
func (s *PrivateClustersServer) resolveTargetTenant(ctx context.Context, cluster *privatev1.Cluster) (string, error) {
	if t := cluster.GetMetadata().GetTenant(); t != "" {
		return t, nil
	}
	return s.tenancyLogic.DetermineDefaultTenant(ctx)
}

// injectDefaultNetworkAttachment populates spec.network_attachment from the
// tenant's default subnet and security group when the caller omits it.
func (s *PrivateClustersServer) injectDefaultNetworkAttachment(ctx context.Context,
	cluster *privatev1.Cluster) error {
	tenant, err := s.resolveTargetTenant(ctx, cluster)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to determine target tenant", slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to determine target tenant")
	}

	spec := cluster.GetSpec()
	subnet, err := findDefaultSubnet(ctx, s.logger, s.subnetsDao, tenant, cluster.GetMetadata().GetProject())
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to look up default subnet", slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to look up default subnet")
	}
	if subnet == nil {
		return nil
	}

	attachment := privatev1.ClusterNetworkAttachment_builder{
		Subnet: privatev1.SubnetLocalReference_builder{Id: subnet.GetId()}.Build(),
	}.Build()

	virtualNetworkID := refKey(subnet.GetSpec().GetVirtualNetwork())
	sg, err := findDefaultSecurityGroup(ctx, s.logger, s.securityGroupsDao, virtualNetworkID, tenant, cluster.GetMetadata().GetProject())
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to look up default security group", slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to look up default security group")
	}
	if sg != nil {
		attachment.SetSecurityGroups([]*privatev1.SecurityGroupLocalReference{
			privatev1.SecurityGroupLocalReference_builder{Id: sg.GetId()}.Build(),
		})
	}

	spec.SetNetworkAttachment(attachment)

	attrs := []slog.Attr{
		slog.String("subnet_id", subnet.GetId()),
	}
	if sg != nil {
		attrs = append(attrs, slog.String("security_group_id", sg.GetId()))
	}
	s.logger.LogAttrs(ctx, slog.LevelInfo, "auto-injected default network attachment", attrs...)
	return nil
}

// validateNetworkAttachmentState validates that the subnet referenced by the
// cluster's network attachment is READY, and that all security groups are READY
// and belong to the same virtual network as the subnet.
func (s *PrivateClustersServer) validateNetworkAttachmentState(ctx context.Context, cluster *privatev1.Cluster) error {
	if cluster == nil || cluster.GetSpec() == nil {
		return nil
	}
	att := cluster.GetSpec().GetNetworkAttachment()
	if att == nil {
		return nil
	}

	subnetRef := att.GetSubnet()
	subnetKey := refKey(subnetRef)
	if subnetKey == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"spec.network_attachment.subnet is required")
	}

	subnet, err := resolveAndCanonicalizeReference(ctx, s.subnetsDao, cluster.GetMetadata(), subnetRef,
		"subnet", grpccodes.NotFound)
	if err != nil {
		if grpcstatus.Code(err) == grpccodes.NotFound {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"spec.network_attachment: subnet '%s' does not exist", subnetKey)
		}
		return err
	}
	if err := validateResolvedSubnetReady(subnet, subnetKey, " in spec.network_attachment"); err != nil {
		return err
	}

	virtualNetworkID := refKey(subnet.GetSpec().GetVirtualNetwork())
	if virtualNetworkID == "" {
		return grpcstatus.Errorf(grpccodes.Internal,
			"spec.network_attachment: subnet '%s' has no virtual network reference", subnetKey)
	}

	// Use the subnet's VirtualNetwork as the expected owner for every SecurityGroup.
	for i, sgRef := range att.GetSecurityGroups() {
		sgKey := refKey(sgRef)
		if sgKey == "" {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"spec.network_attachment.security_groups[%d]: reference is empty", i)
		}

		sg, err := resolveAndCanonicalizeReference(ctx, s.securityGroupsDao, cluster.GetMetadata(), sgRef,
			"security group", grpccodes.NotFound)
		if err != nil {
			if grpcstatus.Code(err) == grpccodes.NotFound {
				return grpcstatus.Errorf(grpccodes.InvalidArgument,
					"spec.network_attachment.security_groups[%d]: security group '%s' does not exist", i, sgKey)
			}
			return err
		}
		if err := validateResolvedSecurityGroup(sg, sgKey,
			fmt.Sprintf(" in spec.network_attachment.security_groups[%d]", i), virtualNetworkID); err != nil {
			return err
		}
	}

	return nil
}

// validateAutoExternalIPImmutability prevents changing auto_external_ip_attachment after creation.
func (s *PrivateClustersServer) validateAutoExternalIPImmutability(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) error {
	if !updateIncludesField(request.GetUpdateMask(), "spec.auto_external_ip_attachment") {
		return nil
	}

	existing, found, err := s.getExistingCluster(ctx, request)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	oldVal := existing.GetSpec().GetAutoExternalIpAttachment()
	newVal := request.GetObject().GetSpec().GetAutoExternalIpAttachment()
	if oldVal != newVal {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cannot change spec.auto_external_ip_attachment: auto_external_ip_attachment is immutable after creation")
	}
	return nil
}

// resolveFabricInterfaces populates fabric_interface on each node set by
// looking up the BareMetalInstanceType and selecting the first port with role "fabric".
func (s *PrivateClustersServer) resolveFabricInterfaces(ctx context.Context, spec *privatev1.ClusterSpec) error {
	for name, nodeSet := range spec.GetNodeSets() {
		bmitKey := refKey(nodeSet.GetBaremetalInstanceType())
		if bmitKey == "" {
			// Template-derived node sets may have no BareMetalInstanceType yet;
			// the fabric interface will be resolved once the caller supplies one.
			continue
		}
		bmit, err := s.lookupBareMetalInstanceType(ctx, bmitKey)
		if err != nil {
			return err
		}
		if bmit == nil {
			return grpcstatus.Errorf(grpccodes.NotFound,
				"node_sets[%s]: bare metal instance type '%s' not found", name, bmitKey)
		}
		fabricInterface := ""
		for _, port := range bmit.GetSpec().GetHardware().GetNetworkPorts() {
			if strings.EqualFold(port.GetRole(), "fabric") {
				fabricInterface = port.GetName()
				break
			}
		}
		if fabricInterface == "" {
			return grpcstatus.Errorf(grpccodes.FailedPrecondition,
				"node_sets[%s]: bare metal instance type '%s' has no network port with role 'fabric'",
				name, bmitKey)
		}
		nodeSet.SetFabricInterface(fabricInterface)
	}
	return nil
}

// selectClusterFabricInterface returns the name of the first interface with role "fabric" on a
// HostType. Used during catalog-item validation where the Template still references HostTypes.
func selectClusterFabricInterface(hostType *privatev1.HostType) (string, error) {
	for _, networkInterface := range hostType.GetInterfaces() {
		if strings.EqualFold(networkInterface.GetRole(), "fabric") {
			return networkInterface.GetName(), nil
		}
	}
	return "", fmt.Errorf("host type '%s' has no interface with role 'fabric'", hostType.GetId())
}

// autoProvisionExternalIPs creates two ExternalIPs and two ExternalIPAttachments
// (one for API, one for ingress) from the best available pool.
func (s *PrivateClustersServer) autoProvisionExternalIPs(ctx context.Context, cluster *privatev1.Cluster) error {
	pool, err := SelectExternalIPPool(ctx, s.externalIPPoolDao, privatev1.IPFamily_IP_FAMILY_UNSPECIFIED)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "auto_external_ip_attachment: %s", err)
	}
	if pool.GetStatus().GetAvailable() < 2 {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"auto_external_ip_attachment: ExternalIP pool '%s' needs at least 2 available IPs, has %d",
			pool.GetId(), pool.GetStatus().GetAvailable())
	}

	tenant := cluster.GetMetadata().GetTenant()
	clusterID := cluster.GetId()

	endpoints := []privatev1.ExternalIPAttachmentEndpoint{
		privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
		privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
	}

	for _, endpoint := range endpoints {
		eip := privatev1.ExternalIP_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: tenant,
				Labels: map[string]string{
					autoCreatedLabel:    "true",
					autoCreatedForLabel: clusterID,
				},
				Annotations: map[string]string{
					ownerReferenceAnnotation: clusterID,
				},
				Creator: "system",
			}.Build(),
			Spec: privatev1.ExternalIPSpec_builder{
				Pool: privatev1.ExternalIPPoolReference_builder{Id: pool.GetId()}.Build(),
			}.Build(),
			Status: privatev1.ExternalIPStatus_builder{
				State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING,
			}.Build(),
		}.Build()

		eipResp, err := s.externalIPDao.Create().SetObject(eip).Do(ctx)
		if err != nil {
			return fmt.Errorf("auto_external_ip_attachment: failed to create ExternalIP: %w", err)
		}
		eipID := eipResp.GetObject().GetId()
		if err = s.lifecycle.lockNewClusterAttachmentReferences(ctx, eipID, clusterID); err != nil {
			return fmt.Errorf("auto_external_ip_attachment: failed to lock attachment references: %w", err)
		}

		attachment := privatev1.ExternalIPAttachment_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: tenant,
				Labels: map[string]string{
					autoCreatedLabel:    "true",
					autoCreatedForLabel: clusterID,
				},
				Annotations: map[string]string{
					ownerReferenceAnnotation: clusterID,
				},
				Creator: "system",
			}.Build(),
			Spec: privatev1.ExternalIPAttachmentSpec_builder{
				ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: eipID}.Build(),
				Cluster:        privatev1.ClusterLocalReference_builder{Id: clusterID}.Build(),
				TargetEndpoint: endpoint,
			}.Build(),
			Status: privatev1.ExternalIPAttachmentStatus_builder{
				State: privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING,
			}.Build(),
		}.Build()

		_, err = s.externalIPAttachmentDao.Create().SetObject(attachment).Do(ctx)
		if err != nil {
			return fmt.Errorf("auto_external_ip_attachment: failed to create ExternalIPAttachment: %w", err)
		}

	}

	err = UpdatePoolCapacity(ctx, s.externalIPPoolDao, pool.GetId(), 2)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "auto_external_ip_attachment: %s", err)
	}

	s.logger.InfoContext(ctx, "auto-provisioned external IP attachments for cluster",
		slog.String("cluster_id", clusterID),
		slog.String("pool_id", pool.GetId()),
	)
	return nil
}

func (s *PrivateClustersServer) applyClusterTemplate(ctx context.Context, cluster *privatev1.Cluster, template *privatev1.ClusterTemplate) (err error) {
	actualClusterParameters, err := utils.ApplyTemplateParameterDefaultsAndValidate(
		utils.ClusterTemplateAdapter{ClusterTemplate: template}, cluster.GetSpec().GetTemplateParameters(),
	)
	if err != nil {
		return err
	}
	cluster.GetSpec().SetTemplateParameters(actualClusterParameters)

	// Only a pull secret inherited from a shared template may cross the tenant boundary. Direct
	// shared SecretLocalReference values on tenant clusters stay local.
	inheritsPullSecretSecret := cluster.GetSpec().GetPullSecretSecret() == nil &&
		template.GetMetadata().GetTenant() == auth.SharedTenant &&
		template.GetSpecDefaults().GetPullSecretSecret() != nil

	// Apply spec defaults from the template (user values take precedence):
	inheritVersion := cluster.GetSpec().GetVersion() == nil
	utils.ApplyClusterSpecDefaults(cluster.GetSpec(), template.GetSpecDefaults())
	if inheritVersion && cluster.GetSpec().GetVersion() != nil {
		inheritReferenceScope(cluster.GetSpec().GetVersion(), template.GetMetadata())
	}

	// Validate pull_secret_secret reference exists:
	if err = s.validatePullSecretSecret(ctx, cluster, inheritsPullSecretSecret); err != nil {
		return err
	}

	if err = s.ensureClusterVersion(ctx, cluster); err != nil {
		return err
	}

	// Validate cluster spec fields (CIDR format, etc.) after defaults have been applied:
	if err = utils.ValidateClusterSpecFields(cluster.GetSpec()); err != nil {
		return err
	}

	if err := s.resolveClusterNodeSets(ctx, cluster, template); err != nil {
		return err
	}

	// Make sure that the template is referenced by its identifier and name, as that is what we want to save
	// to the database. Both fields are needed: id for lookups, name for display and billing dimensions
	// (metering reads the name).
	cluster.GetSpec().SetTemplate(canonicalClusterTemplateReference(template))

	return nil
}

// convertTemplateNodeSets copies Template node sets into resource node sets, preserving names and nil entries.
// Sizes gain explicit presence; resolution and compatibility checks run later.
func convertTemplateNodeSets(value map[string]*privatev1.ClusterTemplateNodeSet) map[string]*privatev1.ClusterNodeSet {
	if value == nil {
		return nil
	}
	result := make(map[string]*privatev1.ClusterNodeSet, len(value))
	for name, nodeSet := range value {
		if nodeSet == nil {
			result[name] = nil
			continue
		}
		size := nodeSet.GetSize()
		result[name] = privatev1.ClusterNodeSet_builder{
			Size: &size,
		}.Build()
	}
	return result
}

// resolveClusterNodeSets applies whole-map defaults and resolves BareMetalInstanceType references.
// When a node set carries no baremetal_instance_type but the corresponding template node set
// provides a host_type, the host_type key is used as a fallback to look up a matching
// BareMetalInstanceType. This keeps existing templates that only define HostType working
// (OSAC-5117).
func (s *PrivateClustersServer) resolveClusterNodeSets(ctx context.Context, cluster *privatev1.Cluster, template *privatev1.ClusterTemplate) error {
	nodes := cluster.GetSpec().GetNodeSets()
	useTemplateMap := len(nodes) == 0
	if useTemplateMap {
		nodes = convertTemplateNodeSets(template.GetNodeSets())
	}
	// An existing Template may contain no node sets. Keep the Cluster map empty so the
	// infrastructure provider can choose node placement.
	if len(nodes) == 0 && len(template.GetNodeSets()) == 0 {
		return nil
	}
	for name, node := range nodes {
		if node == nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "node set '%s' is required", name)
		}

		// Resolve host_type from the template, then optionally override with cluster's host_type.
		var templateHost *privatev1.HostType
		if defaults := template.GetNodeSets()[name]; defaults != nil && defaults.GetHostType() != nil {
			ref := cloneMessage(defaults.GetHostType())
			var err error
			templateHost, err = resolveAndCanonicalizeReference(ctx, s.hostTypesDao, template.GetMetadata(), ref, "host type", grpccodes.NotFound)
			if err != nil {
				return err
			}
		}
		host := templateHost
		if ref := node.GetHostType(); !useTemplateMap && ref != nil {
			var err error
			host, err = resolveAndCanonicalizeReference(ctx, s.hostTypesDao, cluster.GetMetadata(), ref, "host type", grpccodes.NotFound)
			if err != nil {
				return err
			}
			if templateHost != nil && host.GetId() != templateHost.GetId() {
				return grpcstatus.Errorf(grpccodes.InvalidArgument,
					"host type for node set '%s' should be empty, '%s' or '%s', like in template '%s', but it is '%s'",
					name, templateHost.GetMetadata().GetName(), templateHost.GetId(), template.GetId(), refKey(ref))
			}
		}
		if host == nil && node.GetBaremetalInstanceType() == nil {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "host type or baremetal instance type for node set '%s' is required", name)
		}
		if host != nil {
			node.SetHostType(privatev1.HostTypeReference_builder{
				Id: host.GetId(), Name: host.GetMetadata().GetName(),
				Shared: host.GetMetadata().GetTenant() == auth.SharedTenant, Project: host.GetMetadata().GetProject(),
			}.Build())
		}

		// Resolve baremetal_instance_type when present. Missing BMIT is not an error —
		// legacy clusters may only carry a host_type reference.
		if bmitRef := node.GetBaremetalInstanceType(); bmitRef != nil {
			bmitKey := refKey(bmitRef)
			if bmitKey != "" {
				bmit, err := s.lookupBareMetalInstanceType(ctx, bmitKey)
				if err != nil {
					return err
				}
				if bmit != nil {
					node.SetBaremetalInstanceType(privatev1.BareMetalInstanceTypeLocalReference_builder{
						Id: bmit.GetId(), Name: bmit.GetMetadata().GetName(),
					}.Build())
				}
			}
		}
	}
	cluster.GetSpec().SetNodeSets(nodes)
	if err := validateClusterNodeSetMap(nodes); err != nil {
		return grpcstatus.Error(grpccodes.InvalidArgument, err.Error())
	}
	return nil
}

// resolveCatalogItem finds the Cluster's published Catalog Item in the selected tenant/project
// or shared scope, then finds the item's Template under the item's ownership. It applies locked
// and editable field and parameter rules and returns that Template for defaults.
func (s *PrivateClustersServer) resolveCatalogItem(ctx context.Context,
	cluster *privatev1.Cluster) (*privatev1.ClusterTemplate, error) {
	if cluster == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
	}
	catalogItemRef := cluster.GetSpec().GetCatalogItem()
	if catalogItemRef == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "catalog_item is mandatory")
	}
	catalogItemRefStr := refKey(catalogItemRef)

	catalogItem, err := resolveAndCanonicalizeLockedReference(ctx, s.catalogItemsDao, cluster.GetMetadata(), catalogItemRef, "catalog item", grpccodes.NotFound)
	if err != nil {
		return nil, err
	}

	if err := validateCatalogItemForCreation(catalogItem, catalogItemRefStr); err != nil {
		return nil, err
	}

	templateRef := catalogItem.GetTemplate()
	if templateRef == nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "catalog item '%s' has no template", catalogItemRefStr)
	}
	templateRef = cloneMessage(templateRef)
	resolvedTemplate, resolveErr := resolveAndCanonicalizeLockedReference(ctx, s.templatesDao, catalogItem.GetMetadata(), templateRef, "template", grpccodes.InvalidArgument)
	if resolveErr != nil {
		return nil, resolveErr
	}

	if err := applyClusterCatalogItemPolicies(cluster.GetSpec(), catalogItem.GetFields()); err != nil {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "%s", err)
	}

	parameters, err := applyCatalogItemTemplateParameterPolicies(utils.ClusterTemplateAdapter{ClusterTemplate: resolvedTemplate}, catalogItem.GetTemplateParameters(), cluster.GetSpec().GetTemplateParameters())
	if err != nil {
		return nil, err
	}
	cluster.GetSpec().SetTemplateParameters(parameters)
	return resolvedTemplate, nil
}
