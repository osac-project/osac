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
	"context"
	"fmt"

	"github.com/google/uuid"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
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

// seedClusterInProject creates a live Cluster in the given project through the DAO, as an
// owner-reference target.
func seedClusterInProject(project string) *privatev1.Cluster {
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
		}.Build(),
	).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return resp.GetObject()
}

var _ = Describe("Worker attribution", func() {
	Describe("Bare metal instance workers", func() {
		var (
			server      *PrivateBareMetalInstancesServer
			catalogItem *privatev1.BareMetalInstanceCatalogItem
		)

		BeforeEach(func() {
			var err error

			seedProject("web")
			seedTenantDefaultNetworking(testTenant, "web", new("netris"))
			createDiskImageWithLifecycle("worker-disk-image",
				privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE, nil)
			Expect(createTestBareMetalInstanceType(ctx, "worker-type", bareMetalNetworkPort("data-0", "fabric"))).To(Succeed())
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
			catalogItem = catalogResp.GetObject()

			server, err = NewPrivateBareMetalInstancesServer().
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
						Name:    fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						Tenant:  testTenant,
						Project: project,
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						DiskImage:    privatev1.DiskImageReference_builder{Id: "worker-disk-image"}.Build(),
						CatalogItem:  privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItem.GetId()}.Build(),
						InstanceType: bareMetalInstanceTypeReference("worker-type"),
						SshPublicKey: new(testSSHPublicKey),
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
			cluster := seedClusterInProject("web")
			response, err := server.Create(ctx, createWorker("web", clusterRef(cluster.GetId())))
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetMetadata().GetProject()).To(Equal("web"))
			Expect(response.GetObject().GetStatus().GetCluster().GetId()).To(Equal(cluster.GetId()))
			Expect(response.GetObject().GetStatus().GetNodeSet()).To(Equal("compute"))
		})

		It("rejects an owner reference to a missing cluster", func() {
			_, err := server.Create(ctx, createWorker("web", clusterRef("missing-cluster")))
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("owner reference"))
		})

		It("rejects an owner reference to a cluster in another project", func() {
			seedProject("other")
			cluster := seedClusterInProject("other")
			_, err := server.Create(ctx, createWorker("web", clusterRef(cluster.GetId())))
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("same tenant and project"))
		})

		It("rejects changing an owner reference on update", func() {
			cluster := seedClusterInProject("web")
			other := seedClusterInProject("web")
			response, err := server.Create(ctx, createWorker("web", clusterRef(cluster.GetId())))
			Expect(err).ToNot(HaveOccurred())
			worker := response.GetObject()

			update := proto.Clone(worker).(*privatev1.BareMetalInstance)
			update.GetStatus().SetCluster(clusterRef(other.GetId()))
			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: update,
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"status.cluster"},
				},
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("status.cluster"))
		})

		It("rejects clearing an owner reference on update", func() {
			cluster := seedClusterInProject("web")
			response, err := server.Create(ctx, createWorker("web", clusterRef(cluster.GetId())))
			Expect(err).ToNot(HaveOccurred())
			worker := response.GetObject()

			update := proto.Clone(worker).(*privatev1.BareMetalInstance)
			update.GetStatus().ClearCluster()
			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: update,
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"status"},
				},
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("status.cluster"))
		})

		It("allows updates that keep the owner reference unchanged", func() {
			cluster := seedClusterInProject("web")
			response, err := server.Create(ctx, createWorker("web", clusterRef(cluster.GetId())))
			Expect(err).ToNot(HaveOccurred())
			worker := response.GetObject()

			update := proto.Clone(worker).(*privatev1.BareMetalInstance)
			update.GetStatus().SetState(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING)
			updateResponse, err := server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: update,
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"status.state"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetStatus().GetCluster().GetId()).To(Equal(cluster.GetId()))
		})
	})

	Describe("Worker deletion through the public API", func() {
		var (
			privateServer *PrivateBareMetalInstancesServer
			publicServer  *BareMetalInstancesServer
			catalogItemID string
		)

		BeforeEach(func() {
			var err error
			seedProject("web")
			seedTenantDefaultNetworking(testTenant, "web", new("netris"))
			createDiskImageWithLifecycle("worker-disk-image",
				privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE, nil)
			Expect(createTestBareMetalInstanceType(ctx, "worker-type", bareMetalNetworkPort("data-0", "fabric"))).To(Succeed())
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

		createInstance := func(ownerRef *privatev1.ClusterLocalReference) *privatev1.BareMetalInstancesCreateRequest {
			return privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name:    fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						Tenant:  testTenant,
						Project: "web",
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						DiskImage:    privatev1.DiskImageReference_builder{Id: "worker-disk-image"}.Build(),
						CatalogItem:  privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
						InstanceType: bareMetalInstanceTypeReference("worker-type"),
						SshPublicKey: new(testSSHPublicKey),
					}.Build(),
					Status: privatev1.BareMetalInstanceStatus_builder{
						Cluster: ownerRef,
						NodeSet: new("compute"),
					}.Build(),
				}.Build(),
			}.Build()
		}

		It("refuses to delete a worker that was created for a cluster", func() {
			cluster := seedClusterInProject("web")
			response, err := privateServer.Create(ctx, createInstance(
				privatev1.ClusterLocalReference_builder{Id: cluster.GetId()}.Build()))
			Expect(err).ToNot(HaveOccurred())
			workerID := response.GetObject().GetId()

			_, err = publicServer.Delete(ctx, publicv1.BareMetalInstancesDeleteRequest_builder{
				Id: workerID,
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))
			Expect(err.Error()).To(ContainSubstring("created for another resource"))

			// The system path, through the private API, still deletes the worker.
			_, err = privateServer.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
				Id: workerID,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("deletes a standalone bare metal instance", func() {
			response, err := privateServer.Create(ctx, createInstance(nil))
			Expect(err).ToNot(HaveOccurred())

			_, err = publicServer.Delete(ctx, publicv1.BareMetalInstancesDeleteRequest_builder{
				Id: response.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("ignores a user-supplied owner reference on public create", func() {
			cluster := seedClusterInProject("web")
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
						SshPublicKey: new(testSSHPublicKey),
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
			cluster := seedClusterInProject("web")
			response, err := privateServer.Create(ctx, createInstance(
				privatev1.ClusterLocalReference_builder{Id: cluster.GetId()}.Build()))
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

		It("preserves the owner reference and node set on a public update without a mask", func() {
			cluster := seedClusterInProject("web")
			response, err := privateServer.Create(ctx, createInstance(
				privatev1.ClusterLocalReference_builder{Id: cluster.GetId()}.Build()))
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

		It("fails closed when the ownership lookup fails for a reason other than not-found", func() {
			// The guard is the only enforcement for user deletes of owned workers, so a
			// transient lookup failure must surface instead of skipping the check.
			failing := &failingGetBareMetalInstances{code: grpccodes.Unavailable}
			guarded := &BareMetalInstancesServer{logger: logger, delegate: failing}

			err := guarded.rejectOwnedResourceDelete(ctx, "worker-id")
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Unavailable))
			Expect(failing.gets).To(Equal(1))
		})

		It("allows the delete to proceed when the ownership lookup reports not-found", func() {
			failing := &failingGetBareMetalInstances{code: grpccodes.NotFound}
			guarded := &BareMetalInstancesServer{logger: logger, delegate: failing}

			err := guarded.rejectOwnedResourceDelete(ctx, "missing-id")
			Expect(err).ToNot(HaveOccurred())
			Expect(failing.gets).To(Equal(1))
		})
	})

	Describe("Cross-project attachment boundaries", func() {
		var (
			attachmentsServer  *PrivateExternalIPAttachmentsServer
			natGatewaysServer  *PrivateNATGatewaysServer
			externalIPDao      *dao.GenericDAO[*privatev1.ExternalIP]
			computeInstanceDao *dao.GenericDAO[*privatev1.ComputeInstance]
			vnDao              *dao.GenericDAO[*privatev1.VirtualNetwork]
			poolID             string
		)

		BeforeEach(func() {
			var err error

			seedProject("web")
			seedProject("other")

			networkClassesDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = networkClassesDao.Create().SetObject(privatev1.NetworkClass_builder{
				FabricManager: new("netris"),
				Metadata: privatev1.Metadata_builder{
					Tenant: testTenant,
					Name:   "boundary-network-class",
				}.Build(),
				Status: privatev1.NetworkClassStatus_builder{
					State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
					Hub:   "network-hub-a",
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			externalIPPoolDao, err := dao.NewGenericDAO[*privatev1.ExternalIPPool]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			poolResp, err := externalIPPoolDao.Create().SetObject(
				privatev1.ExternalIPPool_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs: []string{"203.0.113.0/24"},
					}.Build(),
					Status: privatev1.ExternalIPPoolStatus_builder{
						State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
						Total:     100,
						Allocated: 0,
						Available: 100,
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			poolID = poolResp.GetObject().GetId()

			externalIPDao, err = dao.NewGenericDAO[*privatev1.ExternalIP]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			computeInstanceDao, err = dao.NewGenericDAO[*privatev1.ComputeInstance]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			vnDao, err = dao.NewGenericDAO[*privatev1.VirtualNetwork]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			attachmentsServer, err = NewPrivateExternalIPAttachmentsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			natGatewaysServer, err = NewPrivateNATGatewaysServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		createEIPInProject := func(project string) *privatev1.ExternalIP {
			return createExternalIPInStateWithProject(ctx, externalIPDao, poolID, project)
		}

		createCIInProject := func(project string) *privatev1.ComputeInstance {
			resp, err := computeInstanceDao.Create().SetObject(
				privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name:    fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						Tenant:  testTenant,
						Project: project,
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "boundary-template"}.Build(),
					}.Build(),
					Status: privatev1.ComputeInstanceStatus_builder{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
						Hub:   "network-hub-a",
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			return resp.GetObject()
		}

		createVNInProject := func(project string) string {
			ncDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			ncList, err := ncDao.List().SetFilter("!has(this.metadata.deletion_timestamp)").SetLimit(1).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			resp, err := vnDao.Create().SetObject(
				privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{
						Name:    fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						Tenant:  testTenant,
						Project: project,
					}.Build(),
					Spec: privatev1.VirtualNetworkSpec_builder{
						NetworkClass: privatev1.NetworkClassReference_builder{Id: ncList.GetItems()[0].GetId()}.Build(),
					}.Build(),
					Status: privatev1.VirtualNetworkStatus_builder{Hub: "network-hub-a"}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			return resp.GetObject().GetId()
		}

		attachmentRequest := func(project string, eipID, ciID string) *privatev1.ExternalIPAttachmentsCreateRequest {
			return privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name:    fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						Tenant:  testTenant,
						Project: project,
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eipID}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ciID}.Build(),
					}.Build(),
				}.Build(),
			}.Build()
		}

		It("creates an attachment whose external IP and target share its project", func() {
			eip := createEIPInProject("web")
			ci := createCIInProject("web")
			_, err := attachmentsServer.Create(ctx, attachmentRequest("web", eip.GetId(), ci.GetId()))
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects an attachment whose external IP is in another project", func() {
			eip := createEIPInProject("other")
			ci := createCIInProject("web")
			_, err := attachmentsServer.Create(ctx, attachmentRequest("web", eip.GetId(), ci.GetId()))
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("project"))
		})

		It("rejects an attachment whose target is in another project", func() {
			eip := createEIPInProject("web")
			ci := createCIInProject("other")
			_, err := attachmentsServer.Create(ctx, attachmentRequest("web", eip.GetId(), ci.GetId()))
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("project"))
		})

		natRequest := func(project, vnID, eipID string) *privatev1.NATGatewaysCreateRequest {
			return privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{
						Name:    fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						Tenant:  testTenant,
						Project: project,
					}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vnID}.Build(),
						ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: eipID}.Build(),
					}.Build(),
				}.Build(),
			}.Build()
		}

		It("creates a NAT gateway whose virtual network and external IP share its project", func() {
			vnID := createVNInProject("web")
			eip := createEIPInProject("web")
			_, err := natGatewaysServer.Create(ctx, natRequest("web", vnID, eip.GetId()))
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects a NAT gateway whose virtual network is in another project", func() {
			vnID := createVNInProject("other")
			eip := createEIPInProject("web")
			_, err := natGatewaysServer.Create(ctx, natRequest("web", vnID, eip.GetId()))
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("project"))
		})

		It("rejects a NAT gateway whose external IP is in another project", func() {
			vnID := createVNInProject("web")
			eip := createEIPInProject("other")
			_, err := natGatewaysServer.Create(ctx, natRequest("web", vnID, eip.GetId()))
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("project"))
		})
	})
})

// createExternalIPInStateWithProject seeds an allocated ExternalIP in the given project of the
// suite tenant.
func createExternalIPInStateWithProject(
	ctx context.Context,
	externalIPDao *dao.GenericDAO[*privatev1.ExternalIP],
	poolID, project string,
) *privatev1.ExternalIP {
	resp, err := externalIPDao.Create().SetObject(
		privatev1.ExternalIP_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant:  testTenant,
				Project: project,
				Name:    fmt.Sprintf("test-%s", uuid.NewString()[:8]),
			}.Build(),
			Spec: privatev1.ExternalIPSpec_builder{
				Pool: privatev1.ExternalIPPoolReference_builder{Id: poolID}.Build(),
			}.Build(),
			Status: privatev1.ExternalIPStatus_builder{
				State:    privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
				Address:  "203.0.113.1",
				Attached: false,
				Hub:      "network-hub-a",
			}.Build(),
		}.Build(),
	).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return resp.GetObject()
}

// failingGetBareMetalInstances is a private-API delegate whose Get fails with a fixed code, for
// exercising the public delete guard's fail-closed behavior.
type failingGetBareMetalInstances struct {
	privatev1.UnimplementedBareMetalInstancesServer
	code grpccodes.Code
	gets int
}

func (f *failingGetBareMetalInstances) Get(
	_ context.Context, _ *privatev1.BareMetalInstancesGetRequest,
) (*privatev1.BareMetalInstancesGetResponse, error) {
	f.gets++
	return nil, grpcstatus.Error(f.code, "lookup failure")
}
