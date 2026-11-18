package servers

import (
	"fmt"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("CaaS worker Create authorization", func() {
	It("requires the controller caller and the owning Cluster attachment contract", func() {
		server, err := NewPrivateBareMetalInstancesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).NotTo(HaveOccurred())

		clusterID := uuid.NewString()
		subnetID := uuid.NewString()
		userSecurityGroupID := uuid.NewString()
		autoSecurityGroupID := uuid.NewString()
		_, err = server.subnetsDao.Create().SetObject(privatev1.Subnet_builder{
			Id: subnetID,
			Metadata: privatev1.Metadata_builder{
				Name: fmt.Sprintf("worker-subnet-%s", uuid.NewString()[:8]), Tenant: testTenant,
			}.Build(),
			Spec: privatev1.SubnetSpec_builder{
				Ipv4Cidr:       new("10.44.0.0/24"),
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "worker-vnet"}.Build(),
			}.Build(),
			Status: privatev1.SubnetStatus_builder{
				State: privatev1.SubnetState_SUBNET_STATE_READY,
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())

		_, err = server.securityGroupsDao.Create().SetObject(privatev1.SecurityGroup_builder{
			Id: userSecurityGroupID,
			Metadata: privatev1.Metadata_builder{
				Name: fmt.Sprintf("worker-sg-%s", uuid.NewString()[:8]), Tenant: testTenant,
			}.Build(),
			Spec: privatev1.SecurityGroupSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "worker-vnet"}.Build(),
			}.Build(),
			Status: privatev1.SecurityGroupStatus_builder{State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY}.Build(),
		}.Build()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())

		_, err = server.securityGroupsDao.Create().SetObject(privatev1.SecurityGroup_builder{
			Id: autoSecurityGroupID,
			Metadata: privatev1.Metadata_builder{
				Name:    caasAutoClusterSecurityGroupPrefix + clusterID,
				Tenant:  testTenant,
				Creator: systemCreator,
				Labels: map[string]string{
					autoCreatedLabel:     "true",
					autoCreatedForLabel:  clusterID,
					autoCreatedKindLabel: "cluster",
				},
				Annotations: map[string]string{tenantAnnotation: testTenant, ownerReferenceAnnotation: clusterID},
			}.Build(),
			Spec: privatev1.SecurityGroupSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "worker-vnet"}.Build(),
			}.Build(),
			Status: privatev1.SecurityGroupStatus_builder{State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY}.Build(),
		}.Build()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())

		_, err = server.clustersDao.Create().SetObject(privatev1.Cluster_builder{
			Id:       clusterID,
			Metadata: privatev1.Metadata_builder{Name: "worker-cluster", Tenant: testTenant}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				SupportsAutomaticCaasEndpoints: true,
				NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
					Subnet: privatev1.SubnetLocalReference_builder{Id: subnetID}.Build(),
					SecurityGroups: []*privatev1.SecurityGroupLocalReference{
						privatev1.SecurityGroupLocalReference_builder{Id: userSecurityGroupID}.Build(),
					},
				}.Build(),
				NodeSets: map[string]*privatev1.ClusterNodeSet{
					"workers": privatev1.ClusterNodeSet_builder{
						BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "default-type"}.Build(),
						FabricInterface:       "data-0",
					}.Build(),
				},
			}.Build(),
			Status: privatev1.ClusterStatus_builder{State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING}.Build(),
		}.Build()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())

		worker := privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   "worker-bmi",
				Tenant: testTenant,
				Annotations: map[string]string{
					clusterAPIIDAnnotation: clusterID,
				},
			}.Build(),
			Spec: privatev1.BareMetalInstanceSpec_builder{
				InstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "default-type"}.Build(),
				NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet:    privatev1.SubnetLocalReference_builder{Id: subnetID}.Build(),
						Interface: new("data-0"),
						SecurityGroups: []*privatev1.SecurityGroupLocalReference{
							privatev1.SecurityGroupLocalReference_builder{Id: userSecurityGroupID}.Build(),
							privatev1.SecurityGroupLocalReference_builder{Id: autoSecurityGroupID}.Build(),
						},
					}.Build(),
				},
			}.Build(),
			Status: privatev1.BareMetalInstanceStatus_builder{
				Cluster: privatev1.ClusterLocalReference_builder{Id: clusterID}.Build(),
				NodeSet: new("workers"),
			}.Build(),
		}.Build()

		unauthorizedErr := server.validateCaaSWorkerCreate(ctx, protoCloneBareMetalInstance(worker))
		Expect(grpcstatus.Code(unauthorizedErr)).To(Equal(grpccodes.InvalidArgument))

		Expect(server.validateCaaSWorkerCreate(controllerContext(), worker)).To(Succeed())
		Expect(worker.GetMetadata().GetFinalizers()).To(ContainElement(caasAgentCleanupFinalizer))

		wrongGroups := protoCloneBareMetalInstance(worker)
		wrongGroups.GetSpec().GetNetworkAttachments()[0].SetSecurityGroups([]*privatev1.SecurityGroupLocalReference{
			privatev1.SecurityGroupLocalReference_builder{Id: autoSecurityGroupID}.Build(),
			privatev1.SecurityGroupLocalReference_builder{Id: userSecurityGroupID}.Build(),
		})
		Expect(grpcstatus.Code(server.validateCaaSWorkerCreate(controllerContext(), wrongGroups))).
			To(Equal(grpccodes.InvalidArgument))
	})
})

func protoCloneBareMetalInstance(instance *privatev1.BareMetalInstance) *privatev1.BareMetalInstance {
	return proto.Clone(instance).(*privatev1.BareMetalInstance)
}
