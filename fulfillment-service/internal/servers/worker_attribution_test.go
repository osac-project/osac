/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"fmt"

	"github.com/google/uuid"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// seedProject creates a project of the suite tenant so resources can be placed in it.
func seedProject(name string) {
	projectsDao, err := dao.NewGenericDAO[*privatev1.Project]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	_, err = projectsDao.Create().SetObject(
		privatev1.Project_builder{
			Id: name,
			Metadata: privatev1.Metadata_builder{
				Name:   name,
				Tenant: testTenant,
			}.Build(),
		}.Build(),
	).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
}

// seedProjectNetworking creates an authorized Subnet and SecurityGroup in a
// specific tenant project for the Cluster's saved network attachment.
func seedProjectNetworking(tenant, project string) (subnetID, securityGroupID string) {
	cidr := "10.251.0.0/16"
	subnetCIDR := "10.251.0.0/24"
	if project == "other" {
		cidr = "10.252.0.0/16"
		subnetCIDR = "10.252.0.0/24"
	}
	virtualNetwork, err := fixtureDAO[*privatev1.VirtualNetwork]().Create().SetObject(
		privatev1.VirtualNetwork_builder{
			Metadata: privatev1.Metadata_builder{
				Name:    fmt.Sprintf("test-vn-%s", uuid.NewString()[:8]),
				Tenant:  tenant,
				Project: project,
			}.Build(),
			Spec: privatev1.VirtualNetworkSpec_builder{Ipv4Cidr: new(cidr)}.Build(),
			Status: privatev1.VirtualNetworkStatus_builder{
				State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
				Hub:   "network-hub-a",
			}.Build(),
		}.Build()).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())

	subnet, err := fixtureDAO[*privatev1.Subnet]().Create().SetObject(privatev1.Subnet_builder{
		Metadata: privatev1.Metadata_builder{
			Name:        fmt.Sprintf("test-subnet-%s", uuid.NewString()[:8]),
			Tenant:      tenant,
			Project:     project,
			Annotations: map[string]string{ownerReferenceAnnotation: virtualNetwork.GetObject().GetId()},
		}.Build(),
		Spec: privatev1.SubnetSpec_builder{
			VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetwork.GetObject().GetId()}.Build(),
			Ipv4Cidr:       new(subnetCIDR),
		}.Build(),
		Status: privatev1.SubnetStatus_builder{
			State: privatev1.SubnetState_SUBNET_STATE_READY,
			Hub:   "network-hub-a",
		}.Build(),
	}.Build()).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())

	group, err := fixtureDAO[*privatev1.SecurityGroup]().Create().SetObject(privatev1.SecurityGroup_builder{
		Metadata: privatev1.Metadata_builder{
			Name:        fmt.Sprintf("test-sg-%s", uuid.NewString()[:8]),
			Tenant:      tenant,
			Project:     project,
			Annotations: map[string]string{ownerReferenceAnnotation: virtualNetwork.GetObject().GetId()},
		}.Build(),
		Spec: privatev1.SecurityGroupSpec_builder{
			VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetwork.GetObject().GetId()}.Build(),
		}.Build(),
		Status: privatev1.SecurityGroupStatus_builder{
			State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
			Hub:   "network-hub-a",
		}.Build(),
	}.Build()).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return subnet.GetObject().GetId(), group.GetObject().GetId()
}

// seedClusterInProject creates a live Cluster in the given project through the DAO, as an
// owner-reference target.
func seedClusterInProject(project, subnetID, securityGroupID string) *privatev1.Cluster {
	clustersDao, err := dao.NewGenericDAO[*privatev1.Cluster]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	resp, err := clustersDao.Create().SetObject(
		privatev1.Cluster_builder{
			Metadata: privatev1.Metadata_builder{
				Name:    fmt.Sprintf("test-%s", uuid.NewString()[:8]),
				Tenant:  testTenant,
				Project: project,
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
					Subnet: privatev1.SubnetLocalReference_builder{Id: subnetID}.Build(),
					SecurityGroups: []*privatev1.SecurityGroupLocalReference{
						privatev1.SecurityGroupLocalReference_builder{Id: securityGroupID}.Build(),
					},
				}.Build(),
				NodeSets: map[string]*privatev1.ClusterNodeSet{
					"compute": privatev1.ClusterNodeSet_builder{
						BaremetalInstanceType: bareMetalInstanceTypeReference("worker-type"),
						FabricInterface:       "data-0",
					}.Build(),
				},
			}.Build(),
		}.Build(),
	).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return resp.GetObject()
}

var _ = Describe("Worker attribution", func() {
	var (
		privateServer   *PrivateBareMetalInstancesServer
		publicServer    *BareMetalInstancesServer
		catalogItemID   string
		subnetID        string
		securityGroupID string
	)

	BeforeEach(func() {
		var err error

		seedProject("web")
		seedReadyDefaultNetworking(testTenant, 0)
		subnetID, securityGroupID = seedProjectNetworking(testTenant, "web")
		createDiskImageWithLifecycle("worker-disk-image",
			privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE, nil)
		Expect(createTestBareMetalInstanceType(ctx, "worker-type",
			bareMetalNetworkPort("data-0", "fabric"), bareMetalNetworkPort("data-1", "fabric"))).To(Succeed())
		Expect(seedBareMetalCatalogItemTemplate(ctx, testTenant, "", "worker-template",
			bareMetalInstanceTypeReference("worker-type"))).To(Succeed())

		catalogServer, err := NewPrivateBareMetalInstanceCatalogItemsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		catalogResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
			Object: privatev1.BareMetalInstanceCatalogItem_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
				}.Build(),
				Title:     "Test catalog item",
				Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "worker-template"}.Build(),
				Published: true,
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		catalogItemID = catalogResp.GetObject().GetId()

		privateServer, err = NewPrivateBareMetalInstancesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		publicServer, err = NewBareMetalInstancesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	createWorker := func(project string, ownerRef *privatev1.ClusterLocalReference) *privatev1.BareMetalInstancesCreateRequest {
		return privatev1.BareMetalInstancesCreateRequest_builder{
			Object: privatev1.BareMetalInstance_builder{
				Metadata: privatev1.Metadata_builder{
					Name:        fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					Tenant:      testTenant,
					Project:     project,
					Annotations: map[string]string{clusterAPIIDAnnotation: refKey(ownerRef)},
				}.Build(),
				Spec: privatev1.BareMetalInstanceSpec_builder{
					DiskImage:      privatev1.DiskImageReference_builder{Id: "worker-disk-image"}.Build(),
					CatalogItem:    privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
					InstanceType:   bareMetalInstanceTypeReference("worker-type"),
					UserDataSource: privatev1.UserDataSource_builder{Inline: new("#cloud-config")}.Build(),
					NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
						privatev1.BareMetalNetworkAttachment_builder{
							Subnet: privatev1.SubnetLocalReference_builder{Id: subnetID}.Build(),
							SecurityGroups: []*privatev1.SecurityGroupLocalReference{
								privatev1.SecurityGroupLocalReference_builder{Id: securityGroupID}.Build(),
							},
							Interface: new("data-0"),
						}.Build(),
					},
				}.Build(),
				Status: privatev1.BareMetalInstanceStatus_builder{
					Cluster: ownerRef,
					NodeSet: new("compute"),
				}.Build(),
			}.Build(),
		}.Build()
	}

	clusterRef := func(id string) *privatev1.ClusterLocalReference {
		return privatev1.ClusterLocalReference_builder{Id: id}.Build()
	}

	It("records the cluster and node set at creation in the cluster's project", func() {
		cluster := seedClusterInProject("web", subnetID, securityGroupID)
		response, err := privateServer.Create(ctx, createWorker("web", clusterRef(cluster.GetId())))
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetMetadata().GetProject()).To(Equal("web"))
		Expect(response.GetObject().GetStatus().GetCluster().GetId()).To(Equal(cluster.GetId()))
		Expect(response.GetObject().GetStatus().GetNodeSet()).To(Equal("compute"))
		Expect(response.GetObject().GetSpec().GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal(subnetID))
		Expect(response.GetObject().GetSpec().GetNetworkAttachments()[0].GetInterface()).To(Equal("data-0"))
	})

	It("rejects a worker interface that differs from the Cluster node set", func() {
		cluster := seedClusterInProject("web", subnetID, securityGroupID)
		request := createWorker("web", clusterRef(cluster.GetId()))
		request.GetObject().GetSpec().GetNetworkAttachments()[0].SetInterface("data-1")
		_, err := privateServer.Create(ctx, request)
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err).To(MatchError(ContainSubstring("must match Cluster node set")))
	})

	It("rejects a worker whose cluster is in another project", func() {
		seedProject("other")
		otherSubnet, otherSecurityGroup := seedProjectNetworking(testTenant, "other")
		cluster := seedClusterInProject("other", otherSubnet, otherSecurityGroup)
		_, err := privateServer.Create(ctx, createWorker("web", clusterRef(cluster.GetId())))
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("tenant and project must match"))
	})

	It("rejects a worker whose cluster is missing", func() {
		_, err := privateServer.Create(ctx, createWorker("web", clusterRef("missing-cluster")))
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("missing"))
	})

	It("rejects changing an owner reference on update", func() {
		cluster := seedClusterInProject("web", subnetID, securityGroupID)
		other := seedClusterInProject("web", subnetID, securityGroupID)
		response, err := privateServer.Create(ctx, createWorker("web", clusterRef(cluster.GetId())))
		Expect(err).ToNot(HaveOccurred())
		worker := response.GetObject()

		update := proto.Clone(worker).(*privatev1.BareMetalInstance)
		update.GetStatus().SetCluster(clusterRef(other.GetId()))
		_, err = privateServer.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
			Object: update,
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"status.cluster"},
			},
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
		Expect(err.Error()).To(ContainSubstring("status.cluster"))
	})

	It("rejects clearing an owner reference on update", func() {
		cluster := seedClusterInProject("web", subnetID, securityGroupID)
		response, err := privateServer.Create(ctx, createWorker("web", clusterRef(cluster.GetId())))
		Expect(err).ToNot(HaveOccurred())
		worker := response.GetObject()

		update := proto.Clone(worker).(*privatev1.BareMetalInstance)
		update.GetStatus().ClearCluster()
		_, err = privateServer.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
			Object: update,
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"status"},
			},
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
		Expect(err.Error()).To(ContainSubstring("status.cluster"))
	})

	It("allows updates that keep the owner reference unchanged", func() {
		cluster := seedClusterInProject("web", subnetID, securityGroupID)
		response, err := privateServer.Create(ctx, createWorker("web", clusterRef(cluster.GetId())))
		Expect(err).ToNot(HaveOccurred())
		worker := response.GetObject()

		update := proto.Clone(worker).(*privatev1.BareMetalInstance)
		update.GetStatus().SetState(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING)
		update.GetStatus().SetStateTransitionTime(timestamppb.Now())
		updateResponse, err := privateServer.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
			Object: update,
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"status.state", "status.state_transition_time"},
			},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResponse.GetObject().GetStatus().GetCluster().GetId()).To(Equal(cluster.GetId()))
	})

	It("ignores a user-supplied owner reference on public create", func() {
		cluster := seedClusterInProject("web", subnetID, securityGroupID)
		response, err := publicServer.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name:    fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					Tenant:  testTenant,
					Project: "web",
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					DiskImage:   publicv1.DiskImageReference_builder{Id: "worker-disk-image"}.Build(),
					CatalogItem: publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
					InstanceType: publicv1.BareMetalInstanceTypeReference_builder{
						Id: "worker-type", Shared: true,
					}.Build(),
					UserDataSource: publicv1.UserDataSource_builder{Inline: new("#cloud-config")}.Build(),
				}.Build(),
				Status: publicv1.BareMetalInstanceStatus_builder{
					Cluster: publicv1.ClusterLocalReference_builder{Id: cluster.GetId()}.Build(),
					NodeSet: new("compute"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		// Only the system sets the attribution fields; a user-supplied value is dropped.
		Expect(response.GetObject().GetStatus().GetCluster()).To(BeNil())
		Expect(response.GetObject().GetStatus().GetNodeSet()).To(BeEmpty())

		getResponse, err := publicServer.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{
			Id: response.GetObject().GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetStatus().GetCluster()).To(BeNil())
	})

	It("rejects a public update of the output-only node set field", func() {
		cluster := seedClusterInProject("web", subnetID, securityGroupID)
		response, err := privateServer.Create(ctx, createWorker("web", clusterRef(cluster.GetId())))
		Expect(err).ToNot(HaveOccurred())

		_, err = publicServer.Update(ctx, publicv1.BareMetalInstancesUpdateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Id: response.GetObject().GetId(),
				Status: publicv1.BareMetalInstanceStatus_builder{
					NodeSet: new("gpu"),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"status.node_set"},
			},
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("status.node_set is output-only"))
	})

	It("rejects the private Cluster marker on public create", func() {
		_, err := publicServer.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name:        fmt.Sprintf("test-%s", uuid.NewString()),
					Annotations: map[string]string{clusterAPIIDAnnotation: "cluster-id"},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err).To(MatchError(ContainSubstring("reserved for private CaaS worker creation")))
	})

	It("preserves the owner reference and node set on a public update without a mask", func() {
		cluster := seedClusterInProject("web", subnetID, securityGroupID)
		response, err := privateServer.Create(ctx, createWorker("web", clusterRef(cluster.GetId())))
		Expect(err).ToNot(HaveOccurred())
		workerID := response.GetObject().GetId()

		// A no-mask update round-trips the public object; the system-set attribution
		// fields survive it.
		getResponse, err := publicServer.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{
			Id: workerID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		_, err = publicServer.Update(ctx, publicv1.BareMetalInstancesUpdateRequest_builder{
			Object: getResponse.GetObject(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		after, err := publicServer.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{
			Id: workerID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(after.GetObject().GetStatus().GetCluster().GetId()).To(Equal(cluster.GetId()))
		Expect(after.GetObject().GetStatus().GetNodeSet()).To(Equal("compute"))
	})
})
