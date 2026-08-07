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
	"sort"
	"strconv"
	"strings"

	"github.com/bits-and-blooms/bitset"
	"github.com/dustin/go-humanize/english"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/exp/maps"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
	"github.com/osac-project/osac/fulfillment-service/internal/utils"
)

type PrivateClustersServerBuilder struct {
	logger            *slog.Logger
	notifier          events.Notifier
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

var _ privatev1.ClustersServer = (*PrivateClustersServer)(nil)

type PrivateClustersServer struct {
	privatev1.UnimplementedClustersServer
	logger             *slog.Logger
	templatesDao       *dao.GenericDAO[*privatev1.ClusterTemplate]
	catalogItemsDao    *dao.GenericDAO[*privatev1.ClusterCatalogItem]
	hostTypesDao       *dao.GenericDAO[*privatev1.HostType]
	clusterVersionsDao *dao.GenericDAO[*privatev1.ClusterVersion]
	generic            *GenericServer[*privatev1.Cluster]
}

func NewPrivateClustersServer() *PrivateClustersServerBuilder {
	return &PrivateClustersServerBuilder{}
}

func (b *PrivateClustersServerBuilder) SetLogger(value *slog.Logger) *PrivateClustersServerBuilder {
	b.logger = value
	return b
}

func (b *PrivateClustersServerBuilder) SetNotifier(value events.Notifier) *PrivateClustersServerBuilder {
	b.notifier = value
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

	// Create the host types DAO:
	hostTypesDao, err := dao.NewGenericDAO[*privatev1.HostType]().
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

	// Create the generic server:
	generic, err := NewGenericServer[*privatev1.Cluster]().
		SetLogger(b.logger).
		SetService(privatev1.Clusters_ServiceDesc.ServiceName).
		SetNotifier(b.notifier).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &PrivateClustersServer{
		logger:             b.logger,
		templatesDao:       templatesDao,
		catalogItemsDao:    catalogItemsDao,
		hostTypesDao:       hostTypesDao,
		clusterVersionsDao: clusterVersionsDao,
		generic:            generic,
	}
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

func (s *PrivateClustersServer) Create(ctx context.Context,
	request *privatev1.ClustersCreateRequest) (response *privatev1.ClustersCreateResponse, err error) {
	// Ensure sane defaults:
	s.setDefaults(request.GetObject())

	// Get the spec:
	spec := request.GetObject().GetSpec()

	// The user may have specified the host types of the node sets by name, but we want to save the
	// identifiers, so we need to look them up:
	for _, nodeSet := range spec.GetNodeSets() {
		var hostType *privatev1.HostType
		hostType, err = s.lookupHostType(ctx, refKey(nodeSet.GetHostType()))
		if err != nil {
			return
		}
		if hostType != nil {
			hostTypeRef := &privatev1.HostTypeReference{}
			hostTypeRef.SetId(hostType.GetId())
			nodeSet.SetHostType(hostTypeRef)
		}
	}

	// Validate duplicate conditions first:
	err = s.validateNoDuplicateConditions(request.GetObject())
	if err != nil {
		return
	}

	// Dispatch between catalog item and template paths:
	catalogItemRef := spec.GetCatalogItem()
	templateRef := spec.GetTemplate()
	if catalogItemRef != nil && templateRef != nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument,
			"catalog_item and template are mutually exclusive")
		return
	}
	if catalogItemRef != nil {
		err = s.validateAndTransformCatalogItem(ctx, request.GetObject())
		if err != nil {
			return
		}
	} else {
		err = s.validateAndTransformCluster(ctx, request.GetObject())
		if err != nil {
			return
		}
	}

	err = s.generic.Create(ctx, request, &response)
	return
}

func (s *PrivateClustersServer) Update(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) (response *privatev1.ClustersUpdateResponse, err error) {
	err = s.validateNoDuplicateConditions(request.GetObject())
	if err != nil {
		return
	}
	err = s.validateTemplateImmutability(ctx, request)
	if err != nil {
		return
	}
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
	if err = utils.ValidateClusterSpecFields(request.GetObject().GetSpec()); err != nil {
		return
	}
	err = s.generic.Update(ctx, request, &response)
	return
}

func (s *PrivateClustersServer) Delete(ctx context.Context,
	request *privatev1.ClustersDeleteRequest) (response *privatev1.ClustersDeleteResponse, err error) {
	err = s.generic.Delete(ctx, request, &response)
	return
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

func (s *PrivateClustersServer) lookupTemplate(ctx context.Context,
	key string) (result *privatev1.ClusterTemplate, err error) {
	if key == "" {
		return
	}
	response, err := s.templatesDao.List().
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
	switch response.GetSize() {
	case 0:
		err = grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"there is no template with identifier or name '%s'",
			key,
		)
	case 1:
		result = response.GetItems()[0]
	default:
		err = grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"there are multiple templates with identifier or name '%s'",
			key,
		)
	}
	return
}

func (s *PrivateClustersServer) lookupHostType(ctx context.Context,
	key string) (result *privatev1.HostType, err error) {
	if key == "" {
		return
	}
	response, err := s.hostTypesDao.List().
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
	switch response.GetSize() {
	case 0:
		err = grpcstatus.Errorf(
			grpccodes.NotFound,
			"there is no host type with identifier or name '%s'",
			key,
		)
	case 1:
		result = response.GetItems()[0]
	default:
		err = grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"there are multiple host types with identifier or name '%s'",
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
	if versionRef != nil && versionRef.GetName() != "" {
		return lookupAndValidateClusterVersion(ctx, s.logger, s.clusterVersionsDao, versionRef.GetName())
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

// validateNodeSetHostTypeImmutability ensures that the host_type field of existing node sets
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
		existingHostType := existingNodeSet.GetHostType()
		newHostType := newNodeSet.GetHostType()
		if refKey(existingHostType) != refKey(newHostType) {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"cannot change host_type for node set '%s' from '%s' to '%s': host_type is immutable",
				nodeSetName,
				refKey(existingHostType),
				refKey(newHostType),
			)
		}
	}
	return nil
}

// validateTemplateImmutability ensures that the template and template_parameters fields
// cannot be changed after cluster creation.
func (s *PrivateClustersServer) validateTemplateImmutability(ctx context.Context,
	request *privatev1.ClustersUpdateRequest) error {
	// Check if template, template_parameters, or catalog_item are being updated:
	updateMask := request.GetUpdateMask()
	updatingTemplate := updateIncludesField(updateMask, "spec.template")
	updatingTemplateParams := updateIncludesField(updateMask, "spec.template_parameters")
	updatingCatalogItem := updateIncludesField(updateMask, "spec.catalog_item")

	// If none of the immutable fields are being updated, no validation needed:
	if !updatingTemplate && !updatingTemplateParams && !updatingCatalogItem {
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

	// Get the specs from both clusters:
	existingSpec := existingCluster.GetSpec()
	newSpec := request.GetObject().GetSpec()

	// Sparse updates can reach private validation with spec omitted entirely.
	// Without a spec message, this validator has no immutable spec fields to compare.
	if newSpec == nil {
		return nil
	}

	// These immutable non-optional proto3 fields have no presence tracking, so a
	// zero value is ambiguous. Treat zero as absent and normalize to the existing
	// value before comparing.
	if updatingTemplate && newSpec.GetTemplate().GetId() == "" && newSpec.GetTemplate().GetName() == "" {
		newSpec.SetTemplate(existingSpec.GetTemplate())
	}
	if updatingTemplateParams && len(newSpec.GetTemplateParameters()) == 0 {
		newSpec.SetTemplateParameters(existingSpec.GetTemplateParameters())
	}
	if updatingCatalogItem && newSpec.GetCatalogItem().GetId() == "" && newSpec.GetCatalogItem().GetName() == "" {
		newSpec.SetCatalogItem(existingSpec.GetCatalogItem())
	}

	// Check if template has changed. Compare by refKey (Id) rather than proto.Equal because
	// validateAndTransformCluster normalizes the stored reference to Id-only, while the
	// reference validator interceptor may backfill Name on incoming requests.
	if updatingTemplate && refKey(existingSpec.GetTemplate()) != refKey(newSpec.GetTemplate()) {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"cannot change spec.template from '%s' to '%s': template is immutable",
			refKey(existingSpec.GetTemplate()),
			refKey(newSpec.GetTemplate()),
		)
	}

	// Check if template_parameters have changed:
	if updatingTemplateParams {
		templateParamsEqual := func(first, second *anypb.Any) bool {
			return proto.Equal(first, second)
		}
		if !maps.EqualFunc(existingSpec.GetTemplateParameters(), newSpec.GetTemplateParameters(), templateParamsEqual) { //nolint:govet // inline: Go compiler doesn't support type param inference for inlining yet
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"cannot change spec.template_parameters: template parameters are immutable",
			)
		}
	}

	if updatingCatalogItem && refKey(existingSpec.GetCatalogItem()) != refKey(newSpec.GetCatalogItem()) {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"cannot change spec.catalog_item from '%s' to '%s': catalog item is immutable",
			refKey(existingSpec.GetCatalogItem()),
			refKey(newSpec.GetCatalogItem()),
		)
	}

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
	if newRef == nil || newRef.GetName() == "" {
		if existingRef != nil {
			newSpec.SetVersion(existingRef)
		}
		return nil
	}
	if existingRef != nil && newRef.GetName() == existingRef.GetName() {
		return nil
	}
	return s.ensureClusterVersion(ctx, request.GetObject())
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

func (s *PrivateClustersServer) validateAndTransformCluster(ctx context.Context, cluster *privatev1.Cluster) error {
	// Check that the template is specified and that refers to a existing template. If the reference was a name
	// then we replace it with the identifier.
	if cluster == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
	}
	templateRef := cluster.GetSpec().GetTemplate()
	if templateRef == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "template is mandatory")
	}
	templateRefStr := refKey(templateRef)
	template, err := s.lookupTemplate(ctx, templateRefStr)
	if err != nil {
		return err
	}
	if template.GetMetadata().HasDeletionTimestamp() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "template '%s' has been deleted", templateRefStr)
	}

	// Apply spec defaults from the template (user values take precedence):
	utils.ApplyClusterSpecDefaults(cluster.GetSpec(), template.GetSpecDefaults())

	if err = s.ensureClusterVersion(ctx, cluster); err != nil {
		return err
	}

	// Validate cluster spec fields (CIDR format, etc.) after defaults have been applied:
	if err = utils.ValidateClusterSpecFields(cluster.GetSpec()); err != nil {
		return err
	}

	// Check that the host types given in the cluster and the template exist, and index them by identifier and
	// name, so that it will be easier to look them up later.
	hostTypes, err := s.lookupAndIndexHostTypes(ctx, template)
	if err != nil {
		return err
	}

	// Validate node sets against the template:
	templateNodeSets := template.GetNodeSets()
	clusterNodeSets := cluster.GetSpec().GetNodeSets()
	if err = s.validateNodeSets(clusterNodeSets, templateNodeSets, hostTypes, templateRefStr); err != nil {
		return err
	}

	// Replace the node sets given in the cluster with those from the template, taking only the size from cluster:
	mergeNodeSetsWithTemplate(cluster, templateNodeSets, clusterNodeSets)

	// Validate template parameters:
	clusterParameters := cluster.GetSpec().GetTemplateParameters()
	err = utils.ValidateClusterTemplateParameters(template, clusterParameters)
	if err != nil {
		return err
	}

	// Set default values for template parameters:
	actualClusterParameters := utils.ProcessTemplateParametersWithDefaults(
		utils.ClusterTemplateAdapter{ClusterTemplate: template},
		clusterParameters,
	)
	cluster.GetSpec().SetTemplateParameters(actualClusterParameters)

	// Make sure that the template and the host types of the node sets are referenced by their identifiers, as
	// that is what we want to save to the database.
	resolvedTemplateRef := &privatev1.ClusterTemplateReference{}
	resolvedTemplateRef.SetId(template.GetId())
	cluster.GetSpec().SetTemplate(resolvedTemplateRef)
	for _, clusterNodeSet := range cluster.GetSpec().GetNodeSets() {
		hostType := hostTypes[refKey(clusterNodeSet.GetHostType())]
		if hostType != nil {
			resolvedHostTypeRef := &privatev1.HostTypeReference{}
			resolvedHostTypeRef.SetId(hostType.GetId())
			clusterNodeSet.SetHostType(resolvedHostTypeRef)
		}
	}

	return nil
}

// lookupAndIndexHostTypes fetches host types referenced by the template's node sets and indexes them
// by both identifier and name.
func (s *PrivateClustersServer) lookupAndIndexHostTypes(
	ctx context.Context, template *privatev1.ClusterTemplate,
) (map[string]*privatev1.HostType, error) {
	hostTypes := map[string]*privatev1.HostType{}
	for _, nodeSet := range template.GetNodeSets() {
		hostTypeRef := nodeSet.GetHostType()
		if hostTypeRef == nil {
			continue
		}
		hostType, err := s.lookupHostType(ctx, refKey(hostTypeRef))
		if err != nil {
			return nil, err
		}
		hostTypeName := hostType.GetMetadata().GetName()
		if hostTypeName != "" {
			hostTypes[hostTypeName] = hostType
		}
		hostTypeId := hostType.GetId()
		hostTypes[hostTypeId] = hostType
	}
	return hostTypes, nil
}

// validateNodeSets checks membership, host-type consistency, and positive size for cluster node sets.
func (s *PrivateClustersServer) validateNodeSets(
	clusterNodeSets map[string]*privatev1.ClusterNodeSet,
	templateNodeSets map[string]*privatev1.ClusterTemplateNodeSet,
	hostTypes map[string]*privatev1.HostType,
	templateRef string,
) error {
	// Check that all the node sets given in the cluster correspond to node sets that exist in the template:
	for clusterNodeSetKey := range clusterNodeSets {
		templateNodeSet := templateNodeSets[clusterNodeSetKey]
		if templateNodeSet == nil {
			templateNodeSetKeys := maps.Keys(templateNodeSets)
			sort.Strings(templateNodeSetKeys)
			for i, templateNodeSetKey := range templateNodeSetKeys {
				templateNodeSetKeys[i] = fmt.Sprintf("'%s'", templateNodeSetKey)
			}
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"node set '%s' doesn't exist, valid values for template '%s' are %s",
				clusterNodeSetKey, templateRef, english.WordSeries(templateNodeSetKeys, "and"),
			)
		}
	}

	// Check that all the node sets given in the cluster specify the same host type that is specified in the
	// template:
	for clusterNodeSetKey, clusterNodeSet := range clusterNodeSets {
		clusterHostTypeRef := clusterNodeSet.GetHostType()
		clusterHostTypeKey := refKey(clusterHostTypeRef)
		if clusterHostTypeKey == "" {
			continue
		}
		templateNodeSet := templateNodeSets[clusterNodeSetKey]
		templateHostTypeRef := templateNodeSet.GetHostType()
		templateHostType := hostTypes[refKey(templateHostTypeRef)]
		templateHostTypeId := templateHostType.GetId()
		templateHostTypeName := templateHostType.GetMetadata().GetName()
		if templateHostTypeName != "" {
			if clusterHostTypeKey != templateHostTypeId && clusterHostTypeKey != templateHostTypeName {
				return grpcstatus.Errorf(
					grpccodes.InvalidArgument,
					"host type for node set '%s' should be empty, '%s' or '%s', like in template '%s', "+
						"but it is '%s'",
					clusterNodeSetKey,
					templateHostTypeName,
					templateHostTypeId,
					templateRef,
					clusterHostTypeKey,
				)
			}
		} else {
			if clusterHostTypeKey != templateHostTypeId {
				return grpcstatus.Errorf(
					grpccodes.InvalidArgument,
					"host type for node set '%s' should be empty or '%s', like in template '%s', "+
						"but it is '%s'",
					clusterNodeSetKey,
					templateHostTypeId,
					templateRef,
					clusterHostTypeKey,
				)
			}
		}
	}

	// Check that all the node sets given in the cluster have a positive size:
	for clusterNodeSetKey, clusterNodeSet := range clusterNodeSets {
		clusterNodeSetSize := clusterNodeSet.GetSize()
		if clusterNodeSetSize <= 0 {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"size for node set '%s' should be greater than zero, but it is %d",
				clusterNodeSetKey, clusterNodeSetSize,
			)
		}
	}

	return nil
}

// mergeNodeSetsWithTemplate replaces the cluster's node sets with template-derived sets, keeping only
// the size from the cluster.
func mergeNodeSetsWithTemplate(
	cluster *privatev1.Cluster,
	templateNodeSets map[string]*privatev1.ClusterTemplateNodeSet,
	clusterNodeSets map[string]*privatev1.ClusterNodeSet,
) {
	actualNodeSets := map[string]*privatev1.ClusterNodeSet{}
	for templateNodeSetKey, templateNodeSet := range templateNodeSets {
		var actualNodeSetSize int32
		clusterNodeSet := clusterNodeSets[templateNodeSetKey]
		if clusterNodeSet != nil {
			actualNodeSetSize = clusterNodeSet.GetSize()
		} else {
			actualNodeSetSize = templateNodeSet.GetSize()
		}
		actualNodeSets[templateNodeSetKey] = privatev1.ClusterNodeSet_builder{
			HostType: templateNodeSet.GetHostType(),
			Size:     actualNodeSetSize,
		}.Build()
	}
	cluster.GetSpec().SetNodeSets(actualNodeSets)
}

func (s *PrivateClustersServer) validateAndTransformCatalogItem(ctx context.Context, cluster *privatev1.Cluster) error {
	if cluster == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
	}
	catalogItemRef := cluster.GetSpec().GetCatalogItem()
	if catalogItemRef == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "catalog_item is mandatory")
	}
	catalogItemRefStr := refKey(catalogItemRef)

	catalogItem, err := s.lookupCatalogItem(ctx, catalogItemRefStr)
	if err != nil {
		return err
	}

	if err := validateCatalogItemAccess(catalogItem, catalogItemRefStr); err != nil {
		return err
	}

	templateRef := catalogItem.GetTemplate()
	if templateRef == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "catalog item '%s' has no template", catalogItemRefStr)
	}
	cluster.GetSpec().SetTemplate(templateRef)

	if err := applyFieldDefinitions(cluster.GetSpec(), catalogItem.GetFieldDefinitions()); err != nil {
		return err
	}

	// Look up the template to apply spec defaults, node sets, and parameter validation:
	templateRefStr := refKey(templateRef)
	template, err := s.lookupTemplate(ctx, templateRefStr)
	if err != nil {
		return err
	}
	if template.GetMetadata().HasDeletionTimestamp() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "template '%s' has been deleted", templateRefStr)
	}

	// Apply spec defaults from the template (user and field_definition values take precedence):
	utils.ApplyClusterSpecDefaults(cluster.GetSpec(), template.GetSpecDefaults())

	// Version resolution (catalog-item path):
	// user input > field_definition default > template spec_defaults > system default.
	if err := s.ensureClusterVersion(ctx, cluster); err != nil {
		return err
	}

	if err := utils.ValidateClusterSpecFields(cluster.GetSpec()); err != nil {
		return err
	}

	hostTypes, err := s.lookupAndIndexHostTypes(ctx, template)
	if err != nil {
		return err
	}

	templateNodeSets := template.GetNodeSets()
	clusterNodeSets := cluster.GetSpec().GetNodeSets()
	if err := s.validateNodeSets(clusterNodeSets, templateNodeSets, hostTypes, templateRefStr); err != nil {
		return err
	}

	mergeNodeSetsWithTemplate(cluster, templateNodeSets, clusterNodeSets)

	clusterParameters := cluster.GetSpec().GetTemplateParameters()
	if err := utils.ValidateClusterTemplateParameters(template, clusterParameters); err != nil {
		return err
	}

	actualClusterParameters := utils.ProcessTemplateParametersWithDefaults(
		utils.ClusterTemplateAdapter{ClusterTemplate: template},
		clusterParameters,
	)
	cluster.GetSpec().SetTemplateParameters(actualClusterParameters)

	for _, clusterNodeSet := range cluster.GetSpec().GetNodeSets() {
		hostType := hostTypes[refKey(clusterNodeSet.GetHostType())]
		if hostType != nil {
			resolvedHostTypeRef := &privatev1.HostTypeReference{}
			resolvedHostTypeRef.SetId(hostType.GetId())
			clusterNodeSet.SetHostType(resolvedHostTypeRef)
		}
	}

	return nil
}

func (s *PrivateClustersServer) lookupCatalogItem(ctx context.Context,
	key string) (result *privatev1.ClusterCatalogItem, err error) {
	if key == "" {
		return
	}
	response, err := s.catalogItemsDao.List().
		SetFilter(fmt.Sprintf("this.id == %[1]s || this.metadata.name == %[1]s", strconv.Quote(key))).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			err = grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
			return
		}
		s.logger.ErrorContext(ctx, "Failed to lookup catalog item",
			slog.String("key", key),
			slog.Any("error", err))
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to lookup catalog item")
		return
	}
	items := response.GetItems()
	if len(items) == 0 {
		err = grpcstatus.Errorf(grpccodes.NotFound,
			"there is no catalog item with identifier or name '%s'", key)
		return
	}
	result = items[0]
	return
}
