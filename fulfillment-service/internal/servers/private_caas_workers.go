package servers

import (
	"context"
	"errors"
	"fmt"
	"slices"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const caasAutoClusterSecurityGroupPrefix = "auto-sg-"

func (s *PrivateBareMetalInstancesServer) validateCaaSWorkerCreate(
	ctx context.Context,
	instance *privatev1.BareMetalInstance,
) error {
	clusterID := instance.GetMetadata().GetAnnotations()[clusterAPIIDAnnotation]
	if clusterID == "" {
		return nil
	}
	if !auth.IsControllerServiceAccount(ctx) {
		return grpcstatus.Error(grpccodes.InvalidArgument,
			"osac.openshift.io/cluster-api-id is accepted only for private CaaS worker creates")
	}

	cluster, err := s.loadCaaSWorkerCluster(ctx, clusterID)
	if err != nil {
		return err
	}
	if err := validateCaaSWorkerClusterIdentity(instance, cluster, clusterID); err != nil {
		return err
	}
	networkEnabled := s.settings.ProvisioningEnabled
	if err := s.lockCaaSWorkerSubnet(ctx, instance, cluster, networkEnabled); err != nil {
		return err
	}
	lockedCluster, err := lockRecord(ctx, s.clustersDao, clusterID)
	if err != nil {
		return err
	}
	cluster = lockedCluster
	if err := validateCaaSWorkerClusterIdentity(instance, cluster, clusterID); err != nil {
		return err
	}
	if err := validateCaaSWorkerNodeSet(instance, cluster); err != nil {
		return err
	}
	addCaaSWorkerCleanupFinalizer(instance)

	if !networkEnabled {
		return validateCaaSWorkerNetworkingDisabled(instance)
	}
	return s.validateCaaSWorkerNetworkAttachments(ctx, instance, cluster)
}

func (s *PrivateBareMetalInstancesServer) loadCaaSWorkerCluster(
	ctx context.Context, clusterID string,
) (*privatev1.Cluster, error) {
	response, err := s.clustersDao.Get().SetId(clusterID).Do(ctx)
	if err == nil {
		return response.GetObject(), nil
	}
	var notFound *dao.ErrNotFound
	if errors.As(err, &notFound) || grpcstatus.Code(err) == grpccodes.NotFound {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "Cluster '%s' does not exist", clusterID)
	}
	return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to read owning Cluster '%s'", clusterID)
}

func validateCaaSWorkerClusterIdentity(
	instance *privatev1.BareMetalInstance, cluster *privatev1.Cluster, clusterID string,
) error {
	if instance.GetMetadata().GetTenant() != cluster.GetMetadata().GetTenant() {
		return grpcstatus.Error(grpccodes.InvalidArgument, "worker tenant does not match its owning Cluster")
	}
	if instance.GetStatus().GetCluster().GetId() != clusterID {
		return grpcstatus.Error(grpccodes.InvalidArgument, "worker status.cluster does not match osac.openshift.io/cluster-api-id")
	}
	if cluster.GetMetadata().HasDeletionTimestamp() ||
		cluster.GetStatus().GetState() == privatev1.ClusterState_CLUSTER_STATE_DELETING {
		return grpcstatus.Error(grpccodes.FailedPrecondition, "the owning Cluster is being deleted")
	}
	return nil
}

func (s *PrivateBareMetalInstancesServer) lockCaaSWorkerSubnet(
	ctx context.Context, instance *privatev1.BareMetalInstance, cluster *privatev1.Cluster, networkEnabled bool,
) error {
	if !networkEnabled || len(instance.GetSpec().GetNetworkAttachments()) == 0 {
		return nil
	}
	attachment := cluster.GetSpec().GetNetworkAttachment()
	if attachment == nil || refKey(attachment.GetSubnet()) == "" {
		return grpcstatus.Error(grpccodes.InvalidArgument, "worker has a network attachment but its Cluster does not")
	}
	// The shared lock order is Subnet before Cluster. Cluster delete takes
	// the same parent lock before setting its deletion timestamp.
	_, err := lockRecord(ctx, s.subnetsDao, refKey(attachment.GetSubnet()))
	return err
}

func validateCaaSWorkerNodeSet(instance *privatev1.BareMetalInstance, cluster *privatev1.Cluster) error {
	nodeSetName := instance.GetStatus().GetNodeSet()
	nodeSet := cluster.GetSpec().GetNodeSets()[nodeSetName]
	if nodeSet == nil || refKey(nodeSet.GetBaremetalInstanceType()) != refKey(instance.GetSpec().GetInstanceType()) {
		return grpcstatus.Error(grpccodes.InvalidArgument,
			"worker instance type is not authorized by a node set of its Cluster")
	}
	return nil
}

func addCaaSWorkerCleanupFinalizer(instance *privatev1.BareMetalInstance) {
	finalizers := instance.GetMetadata().GetFinalizers()
	if !slices.Contains(finalizers, caasAgentCleanupFinalizer) {
		instance.GetMetadata().SetFinalizers(append(finalizers, caasAgentCleanupFinalizer))
	}
}

func validateCaaSWorkerNetworkingDisabled(instance *privatev1.BareMetalInstance) error {
	if len(instance.GetSpec().GetNetworkAttachments()) != 0 {
		return grpcstatus.Error(grpccodes.InvalidArgument,
			"provider networking is disabled; a CaaS worker cannot request a tenant network attachment")
	}
	return nil
}

func (s *PrivateBareMetalInstancesServer) validateCaaSWorkerNetworkAttachments(
	ctx context.Context, instance *privatev1.BareMetalInstance, cluster *privatev1.Cluster,
) error {
	attachments := instance.GetSpec().GetNetworkAttachments()
	if len(attachments) != 1 || cluster.GetSpec().GetNetworkAttachment() == nil {
		return grpcstatus.Error(grpccodes.InvalidArgument, "CaaS workers require their Cluster's single network attachment")
	}
	clusterAttachment := cluster.GetSpec().GetNetworkAttachment()
	if refKey(attachments[0].GetSubnet()) != refKey(clusterAttachment.GetSubnet()) {
		return grpcstatus.Error(grpccodes.InvalidArgument, "worker Subnet differs from its owning Cluster")
	}
	nodeSet := cluster.GetSpec().GetNodeSets()[instance.GetStatus().GetNodeSet()]
	if nodeSet == nil {
		return grpcstatus.Error(grpccodes.InvalidArgument, "worker node set is not authorized by its owning Cluster")
	}
	if attachments[0].GetInterface() != nodeSet.GetFabricInterface() {
		return grpcstatus.Error(grpccodes.InvalidArgument, "worker interface differs from its Cluster node set")
	}
	expectedGroups := make([]string, 0, len(clusterAttachment.GetSecurityGroups())+1)
	for _, group := range clusterAttachment.GetSecurityGroups() {
		expectedGroups = append(expectedGroups, refKey(group))
	}
	if cluster.GetSpec().GetSupportsAutomaticCaasEndpoints() {
		autoGroup, err := s.lookupAutomaticClusterSecurityGroup(ctx, cluster)
		if err != nil {
			return err
		}
		expectedGroups = append(expectedGroups, autoGroup.GetId())
	}
	actualGroups := make([]string, 0, len(attachments[0].GetSecurityGroups()))
	for _, group := range attachments[0].GetSecurityGroups() {
		actualGroups = append(actualGroups, refKey(group))
	}
	if !slices.Equal(actualGroups, expectedGroups) {
		return grpcstatus.Error(grpccodes.InvalidArgument,
			"worker SecurityGroups must match its Cluster attachment followed by the automatic Cluster SecurityGroup")
	}
	return nil
}

func (s *PrivateBareMetalInstancesServer) lookupAutomaticClusterSecurityGroup(
	ctx context.Context,
	cluster *privatev1.Cluster,
) (*privatev1.SecurityGroup, error) {
	name := caasAutoClusterSecurityGroupPrefix + cluster.GetId()
	filter := fmt.Sprintf("this.metadata.name == %q && this.metadata.tenant == %q", name,
		cluster.GetMetadata().GetTenant())
	response, err := s.securityGroupsDao.List().SetFilter(filter).SetLimit(2).Do(ctx)
	if err != nil {
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to resolve the automatic Cluster SecurityGroup")
	}
	if len(response.GetItems()) != 1 {
		return nil, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"automatic Cluster SecurityGroup for Cluster '%s' is not available", cluster.GetId())
	}
	group := response.GetItems()[0]
	labels := group.GetMetadata().GetLabels()
	if group.GetMetadata().GetCreator() != systemCreator ||
		labels[autoCreatedLabel] != "true" || labels[autoCreatedForLabel] != cluster.GetId() ||
		labels[autoCreatedKindLabel] != "cluster" {
		return nil, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"automatic Cluster SecurityGroup for Cluster '%s' has invalid ownership metadata", cluster.GetId())
	}
	return group, nil
}
