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
	"fmt"
	"math"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

func seedClusterVersion(ctx context.Context, cv *privatev1.ClusterVersion) {
	GinkgoHelper()
	cvDao, err := dao.NewGenericDAO[*privatev1.ClusterVersion]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	Expect(err).ToNot(HaveOccurred())
	_, err = cvDao.Create().SetObject(cv).Do(ctx)
	Expect(err).ToNot(HaveOccurred())
}

var _ = Describe("Private clusters server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateClustersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateClustersServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewPrivateClustersServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateClustersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})

	})

	Describe("Behaviour", func() {
		var server *PrivateClustersServer

		BeforeEach(func() {
			var err error

			// Create the server:
			server, err = NewPrivateClustersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create a default cluster version for version resolution:
			seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
				Id: "cv-default",
				Metadata: privatev1.Metadata_builder{
					Name:   "4-17-0",
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.ClusterVersionSpec_builder{
					Image:     "quay.io/openshift-release-dev/ocp-release:4.17.0-multi",
					Enabled:   proto.Bool(true),
					IsDefault: proto.Bool(true),
					Version:   "4.17.0",
					State:     privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE,
				}.Build(),
			}.Build())

			// Create the host types DAO:
			hostTypesDao, err := dao.NewGenericDAO[*privatev1.HostType]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create the templates DAO:
			templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create the host types:
			_, err = hostTypesDao.Create().
				SetObject(
					privatev1.HostType_builder{
						Id: "acme-1ti-id",
						Metadata: privatev1.Metadata_builder{
							Name:   "acme-1ti-name",
							Tenant: auth.SharedTenant,
						}.Build(),
						Title:       "ACME 1TiB",
						Description: "ACME 1TiB.",
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = hostTypesDao.Create().
				SetObject(
					privatev1.HostType_builder{
						Id: "acme-gpu-id",
						Metadata: privatev1.Metadata_builder{
							Name:   "acme-gpu-name",
							Tenant: auth.SharedTenant,
						}.Build(),
						Title:       "ACME GPU",
						Description: "ACME GPU.",
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create a usable template:
			_, err = templatesDao.Create().
				SetObject(
					privatev1.ClusterTemplate_builder{
						Id: "my-template-id",
						Metadata: privatev1.Metadata_builder{
							Name:   "my-template-name",
							Tenant: auth.SharedTenant,
						}.Build(),
						Title:       "My template",
						Description: "My template",
						NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
							"compute": privatev1.ClusterTemplateNodeSet_builder{
								HostType: privatev1.HostTypeReference_builder{Id: "acme-1ti-id"}.Build(),
								Size:     3,
							}.Build(),
							"gpu": privatev1.ClusterTemplateNodeSet_builder{
								HostType: privatev1.HostTypeReference_builder{Id: "acme-gpu-id"}.Build(),
								Size:     1,
							}.Build(),
						},
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create a virtual network and subnets for network attachment tests:
			vnDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = vnDao.Create().SetObject(privatev1.VirtualNetwork_builder{
				Id: "test-vnet",
				Metadata: privatev1.Metadata_builder{
					Name:   "test-vnet",
					Tenant: auth.SharedTenant,
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			subnetsDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			for _, subnetID := range []string{"subnet-1", "subnet-2"} {
				_, err = subnetsDao.Create().SetObject(privatev1.Subnet_builder{
					Id: subnetID,
					Metadata: privatev1.Metadata_builder{
						Name:   subnetID,
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.SubnetSpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "test-vnet"}.Build(),
						Ipv4Cidr:       new("10.0.0.0/24"),
					}.Build(),
					Status: privatev1.SubnetStatus_builder{
						State: privatev1.SubnetState_SUBNET_STATE_READY,
					}.Build(),
				}.Build()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			// Create numbered templates for list tests:
			for i := range 10 {
				_, err = templatesDao.Create().
					SetObject(
						privatev1.ClusterTemplate_builder{
							Id:          fmt.Sprintf("my-template-id-%d", i),
							Title:       fmt.Sprintf("My template %d", i),
							Description: fmt.Sprintf("My template %d", i),
							Metadata: privatev1.Metadata_builder{
								Name:   fmt.Sprintf("my-template-name-%d", i),
								Tenant: auth.SharedTenant,
							}.Build(),
							NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
								"compute": privatev1.ClusterTemplateNodeSet_builder{
									HostType: privatev1.HostTypeReference_builder{Id: "acme-1ti-id"}.Build(),
									Size:     3,
								}.Build(),
							},
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
		})

		It("Creates object with template specified by name", func() {
			// Create the object:
			response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-name"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())

			// Verify that the template name was replaced by the identifier:
			Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("my-template-id"))
		})

		It("Fails when creating object with non-existent template name", func() {
			_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "does-not-exist"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal(
				"there is no template with identifier or name 'does-not-exist'",
			))
		})

		It("Creates object with host type specified by name in node set", func() {
			// Create a cluster specifying the host type by name:
			response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								HostType: privatev1.HostTypeReference_builder{Id: "acme-1ti-name"}.Build(),
								Size:     5,
							}.Build(),
						},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())

			// Verify that the host type name was replaced by the identifier:
			nodeSets := object.GetSpec().GetNodeSets()
			Expect(nodeSets).To(HaveKey("compute"))
			nodeSet := nodeSets["compute"]
			Expect(nodeSet.GetHostType().GetId()).To(Equal("acme-1ti-id"))
		})

		It("Creates object with host type specified by identifier in node set", func() {
			// Create a cluster specifying the host type by identifier:
			response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								HostType: privatev1.HostTypeReference_builder{Id: "acme-1ti-id"}.Build(),
								Size:     7,
							}.Build(),
						},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())

			// Verify that the host type identifier is preserved:
			nodeSets := object.GetSpec().GetNodeSets()
			Expect(nodeSets).To(HaveKey("compute"))
			nodeSet := nodeSets["compute"]
			Expect(nodeSet.GetHostType().GetId()).To(Equal("acme-1ti-id"))
		})

		It("Creates object with template and host type specified by name", func() {
			// Create a cluster specifying the template and the host type by name:
			response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-name"}.Build(),
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								HostType: privatev1.HostTypeReference_builder{Id: "acme-1ti-name"}.Build(),
								Size:     7,
							}.Build(),
						},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())

			// Verify that the the template and host type names were replaced by the identifiers:
			Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("my-template-id"))
			nodeSets := object.GetSpec().GetNodeSets()
			Expect(nodeSets).To(HaveKey("compute"))
			nodeSet := nodeSets["compute"]
			Expect(nodeSet.GetHostType().GetId()).To(Equal("acme-1ti-id"))
		})

		It("Fails when creating object with non-existent host type name", func() {
			_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								HostType: privatev1.HostTypeReference_builder{Id: "does-not-exist"}.Build(),
								Size:     5,
							}.Build(),
						},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
			Expect(status.Message()).To(Equal(
				"there is no host type with identifier or name 'does-not-exist'",
			))
		})

		It("Fails when creating object with non-existent node set", func() {
			_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"does-not-exist": privatev1.ClusterNodeSet_builder{
								HostType: privatev1.HostTypeReference_builder{Id: "acme-1ti-id"}.Build(),
								Size:     5,
							}.Build(),
						},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal(
				"node set 'does-not-exist' doesn't exist, valid values for template 'my-template-id' " +
					"are 'compute' and 'gpu'",
			))
		})

		It("Fails when creating object with host type that doesn't match template", func() {
			_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								HostType: privatev1.HostTypeReference_builder{Id: "acme-gpu-id"}.Build(),
								Size:     5,
							}.Build(),
						},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal(
				"host type for node set 'compute' should be empty, 'acme-1ti-name' or 'acme-1ti-id', " +
					"like in template 'my-template-id', but it is 'acme-gpu-id'",
			))
		})

		It("Returns 'already exists' when creating object with existing identifier", func() {
			// Create an object with a specific identifier:
			id := uuid.New()
			_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: id,
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Try to create another object with the same identifier:
			_, err = server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: id,
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.AlreadyExists))
			Expect(status.Message()).To(Equal(fmt.Sprintf("object with identifier '%s' already exists", id)))
		})

		It("List objects", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: fmt.Sprintf("my-template-id-%d", i)}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: fmt.Sprintf("my-hub-id-%d", i),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, privatev1.ClustersListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: fmt.Sprintf("my-template-id-%d", i)}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: fmt.Sprintf("my-hub-id-%d", i),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, privatev1.ClustersListRequest_builder{
				Limit: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 1))
		})

		It("List objects with offset", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: fmt.Sprintf("my-template-id-%d", i)}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: fmt.Sprintf("my-hub-id-%d", i),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, privatev1.ClustersListRequest_builder{
				Offset: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", count-1))
		})

		It("List objects with filter", func() {
			// Create a few objects:
			const count = 10
			var objects []*privatev1.Cluster
			for i := range count {
				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: fmt.Sprintf("my-template-id-%d", i)}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: fmt.Sprintf("my-hub-%d", i),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				objects = append(objects, response.GetObject())
			}

			// List the objects:
			for _, object := range objects {
				response, err := server.List(ctx, privatev1.ClustersListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == '%s'", object.GetId())),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetSize()).To(BeNumerically("==", 1))
				Expect(response.GetItems()[0].GetId()).To(Equal(object.GetId()))
			}
		})

		It("Get object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get it:
			getResponse, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("Canonicalizes network CIDRs on Update", func() {
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						Template: object.GetSpec().GetTemplate(),
						Network: privatev1.ClusterNetwork_builder{
							PodCidr:     new("10.128.0.5/14"),
							ServiceCidr: new("172.30.1.0/16"),
						}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.network.pod_cidr", "spec.network.service_cidr"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			network := updateResponse.GetObject().GetSpec().GetNetwork()
			Expect(network.GetPodCidr()).To(Equal("10.128.0.0/14"))
			Expect(network.GetServiceCidr()).To(Equal("172.30.0.0/16"))

			getResponse, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			network = getResponse.GetObject().GetSpec().GetNetwork()
			Expect(network.GetPodCidr()).To(Equal("10.128.0.0/14"))
			Expect(network.GetServiceCidr()).To(Equal("172.30.0.0/16"))
		})

		It("Update object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Update the object (keeping template unchanged):
			updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						Template: object.GetSpec().GetTemplate(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "your_hub",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetTemplate().GetId()).To(Equal("my-template-id"))
			Expect(updateResponse.GetObject().GetStatus().GetHub()).To(Equal("your_hub"))

			// Get and verify:
			getResponse, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetSpec().GetTemplate().GetId()).To(Equal("my-template-id"))
			Expect(getResponse.GetObject().GetStatus().GetHub()).To(Equal("your_hub"))
		})

		It("Delete object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name:       "test-cluster",
						Finalizers: []string{"a"},
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Delete the object:
			_, err = server.Delete(ctx, privatev1.ClustersDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get and verify:
			getResponse, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		It("Rejects creation with duplicate condition", func() {
			_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Conditions: []*privatev1.ClusterCondition{
							privatev1.ClusterCondition_builder{
								Type: privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY,
							}.Build(),
							privatev1.ClusterCondition_builder{
								Type: privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal("condition 'CLUSTER_CONDITION_TYPE_READY' is duplicated"))
		})

		It("Rejects update with duplicate condition", func() {
			_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Conditions: []*privatev1.ClusterCondition{
							privatev1.ClusterCondition_builder{
								Type: privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Status: privatev1.ClusterStatus_builder{
						Conditions: []*privatev1.ClusterCondition{
							privatev1.ClusterCondition_builder{
								Type: privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY,
							}.Build(),
							privatev1.ClusterCondition_builder{
								Type: privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal("condition 'CLUSTER_CONDITION_TYPE_READY' is duplicated"))
		})

		It("Allows adding a new node set", func() {
			// Create a cluster with the default node sets from the template
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			Expect(object.GetSpec().GetNodeSets()).To(HaveLen(2)) // compute and gpu

			// Add a new node set
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								HostType: privatev1.HostTypeReference_builder{Id: "acme-1ti-id"}.Build(),
								Size:     3,
							}.Build(),
							"gpu": privatev1.ClusterNodeSet_builder{
								HostType: privatev1.HostTypeReference_builder{Id: "acme-gpu-id"}.Build(),
								Size:     1,
							}.Build(),
							"storage": privatev1.ClusterNodeSet_builder{
								HostType: privatev1.HostTypeReference_builder{Id: "acme-1ti-id"}.Build(),
								Size:     2,
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.node_sets"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows removing a node set when multiple exist", func() {
			// Create a cluster with the default node sets from the template
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			Expect(object.GetSpec().GetNodeSets()).To(HaveLen(2)) // compute and gpu

			// Remove the gpu node set
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								HostType: privatev1.HostTypeReference_builder{Id: "acme-1ti-id"}.Build(),
								Size:     3,
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.node_sets"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects removing the last node set", func() {
			// Create a cluster with a template that has only one node set
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id-0"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			Expect(object.GetSpec().GetNodeSets()).To(HaveLen(1)) // only compute

			// Try to remove the last node set
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						NodeSets: map[string]*privatev1.ClusterNodeSet{},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.node_sets"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal("cannot remove the last node set: clusters must have at least one node set"))
		})

		It("Rejects changing host_type of an existing node set", func() {
			// Create a cluster with the default node sets from the template
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Try to change the host_type of the compute node set
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								HostType: privatev1.HostTypeReference_builder{Id: "acme-gpu-id"}.Build(), // Changed from acme-1ti-id
								Size:     3,
							}.Build(),
							"gpu": privatev1.ClusterNodeSet_builder{
								HostType: privatev1.HostTypeReference_builder{Id: "acme-gpu-id"}.Build(),
								Size:     1,
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.node_sets"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal("cannot change host_type for node set 'compute' from 'acme-1ti-id' to 'acme-gpu-id': host_type is immutable"))
		})

		It("Allows changing size of an existing node set", func() {
			// Create a cluster with the default node sets from the template
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Change the size of the compute node set
			updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								HostType: privatev1.HostTypeReference_builder{Id: "acme-1ti-id"}.Build(),
								Size:     5, // Changed from 3
							}.Build(),
							"gpu": privatev1.ClusterNodeSet_builder{
								HostType: privatev1.HostTypeReference_builder{Id: "acme-gpu-id"}.Build(),
								Size:     1,
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.node_sets"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			updatedObject := updateResponse.GetObject()
			Expect(updatedObject.GetSpec().GetNodeSets()["compute"].GetSize()).To(Equal(int32(5)))
		})

		It("Allows changing size with granular field mask", func() {
			// Create a cluster with the default node sets from the template
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Get the initial size
			initialSize := object.GetSpec().GetNodeSets()["compute"].GetSize()
			newSize := initialSize + 2

			// Change only the size using a granular field mask
			updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								Size: newSize,
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.node_sets.compute.size"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			updatedObject := updateResponse.GetObject()
			Expect(updatedObject.GetSpec().GetNodeSets()["compute"].GetSize()).To(Equal(newSize))
		})

		It("Rejects changing template field", func() {
			oldTemplate := "my-template-id"
			newTemplate := "my-template-id-0"

			// Create a cluster
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: oldTemplate}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Try to change the template
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: newTemplate}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.template"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal(fmt.Sprintf(
				"cannot change spec.template from '%s' to '%s': template is immutable",
				oldTemplate, newTemplate,
			)))
		})

		It("Rejects changing template_parameters field", func() {
			// Create a cluster with template parameters
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Try to change the template parameters
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						Template:           object.GetSpec().GetTemplate(),
						TemplateParameters: map[string]*anypb.Any{"key": nil},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.template_parameters"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal("cannot change spec.template_parameters: template parameters are immutable"))
		})

		Describe("Network attachment immutability", func() {
			createClusterWithNetworkAttachment := func(subnet *privatev1.SubnetLocalReference, securityGroups []*privatev1.SecurityGroupLocalReference) *privatev1.Cluster {
				createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
							NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
								Subnet:         subnet,
								SecurityGroups: securityGroups,
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				return createResponse.GetObject()
			}

			It("Rejects changing subnet via whole attachment replacement", func() {
				object := createClusterWithNetworkAttachment(privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(), []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "sg-1"}.Build()})

				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
								Subnet:         privatev1.SubnetLocalReference_builder{Id: "subnet-2"}.Build(),
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "sg-1"}.Build()},
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.network_attachment"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal(
					"cannot change spec.network_attachment.subnet from 'subnet-1' to 'subnet-2': subnet is immutable",
				))
			})

			It("Rejects removing network_attachment when one exists", func() {
				object := createClusterWithNetworkAttachment(privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(), []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "sg-1"}.Build()})

				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id:   object.GetId(),
						Spec: privatev1.ClusterSpec_builder{}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.network_attachment"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal(
					"cannot change spec.network_attachment.subnet from 'subnet-1' to '': subnet is immutable",
				))
			})

			It("Rejects adding network_attachment when none existed", func() {
				createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := createResponse.GetObject()

				_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.network_attachment"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal(
					"cannot change spec.network_attachment.subnet from '' to 'subnet-1': subnet is immutable",
				))
			})

			It("Allows changing security_groups with same subnet", func() {
				object := createClusterWithNetworkAttachment(privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(), []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "sg-1"}.Build()})

				updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
								Subnet:         privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "sg-1"}.Build(), privatev1.SecurityGroupLocalReference_builder{Id: "sg-2"}.Build()},
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.network_attachment"},
					},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				updated := updateResponse.GetObject()
				Expect(updated.GetSpec().GetNetworkAttachment().GetSubnet().GetId()).To(Equal("subnet-1"))
				Expect(updated.GetSpec().GetNetworkAttachment().GetSecurityGroups()).To(HaveLen(2))
				Expect(updated.GetSpec().GetNetworkAttachment().GetSecurityGroups()[0].GetId()).To(Equal("sg-1"))
				Expect(updated.GetSpec().GetNetworkAttachment().GetSecurityGroups()[1].GetId()).To(Equal("sg-2"))
			})

			It("Allows updating security_groups via sub-field mask", func() {
				object := createClusterWithNetworkAttachment(privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(), []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "sg-1"}.Build()})

				updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "sg-2"}.Build(), privatev1.SecurityGroupLocalReference_builder{Id: "sg-3"}.Build()},
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.network_attachment.security_groups"},
					},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				updated := updateResponse.GetObject()
				Expect(updated.GetSpec().GetNetworkAttachment().GetSecurityGroups()).To(HaveLen(2))
				Expect(updated.GetSpec().GetNetworkAttachment().GetSecurityGroups()[0].GetId()).To(Equal("sg-2"))
				Expect(updated.GetSpec().GetNetworkAttachment().GetSecurityGroups()[1].GetId()).To(Equal("sg-3"))
			})

			It("Passes through when mask does not include network_attachment", func() {
				object := createClusterWithNetworkAttachment(privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(), []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "sg-1"}.Build()})

				updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Status: privatev1.ClusterStatus_builder{
							State: privatev1.ClusterState_CLUSTER_STATE_READY,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"status.state"},
					},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				updated := updateResponse.GetObject()
				Expect(updated.GetSpec().GetNetworkAttachment().GetSubnet().GetId()).To(Equal("subnet-1"))
			})
		})

		Describe("Catalog item", func() {
			var catalogItemsDao *dao.GenericDAO[*privatev1.ClusterCatalogItem]

			BeforeEach(func() {
				var err error
				catalogItemsDao, err = dao.NewGenericDAO[*privatev1.ClusterCatalogItem]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
			})

			createCatalogItem := func(id string, published bool, fieldDefs []*privatev1.FieldDefinition) {
				_, err := catalogItemsDao.Create().SetObject(
					privatev1.ClusterCatalogItem_builder{
						Id: id,
						Metadata: privatev1.Metadata_builder{
							Name:   id + "-name",
							Tenant: "shared",
						}.Build(),
						Title:            "Test Catalog Item",
						Published:        published,
						Template:         privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						FieldDefinitions: fieldDefs,
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			It("Creates cluster with catalog item and populates node sets from template", func() {
				createCatalogItem("cat-happy", true, nil)

				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "cat-happy"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				object := response.GetObject()
				Expect(object).ToNot(BeNil())
				Expect(object.GetId()).ToNot(BeEmpty())
				Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("my-template-id"))
				Expect(object.GetSpec().GetCatalogItem().GetId()).To(Equal("cat-happy"))

				// Verify node sets are populated from the template:
				nodeSets := object.GetSpec().GetNodeSets()
				Expect(nodeSets).To(HaveLen(2))
				Expect(nodeSets).To(HaveKey("compute"))
				Expect(nodeSets["compute"].GetHostType().GetId()).To(Equal("acme-1ti-id"))
				Expect(nodeSets["compute"].GetSize()).To(Equal(int32(3)))
				Expect(nodeSets).To(HaveKey("gpu"))
				Expect(nodeSets["gpu"].GetHostType().GetId()).To(Equal("acme-gpu-id"))
				Expect(nodeSets["gpu"].GetSize()).To(Equal(int32(1)))
			})

			It("Creates cluster with catalog item specified by name", func() {
				createCatalogItem("cat-by-name", true, nil)

				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "cat-by-name-name"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				object := response.GetObject()
				Expect(object).ToNot(BeNil())
				Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("my-template-id"))
			})

			It("Fails when catalog item not found", func() {
				_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "nonexistent"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.NotFound))
				Expect(status.Message()).To(Equal(
					"there is no catalog item with identifier or name 'nonexistent'",
				))
			})

			It("Fails when catalog item is not published", func() {
				createCatalogItem("cat-unpublished", false, nil)

				_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "cat-unpublished"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.NotFound))
				Expect(status.Message()).To(Equal(
					"catalog item 'cat-unpublished' is not published",
				))
			})

			It("Fails when both catalog_item and template are set", func() {
				_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "any-catalog-item"}.Build(),
							Template:    privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal("catalog_item and template are mutually exclusive"))
			})

			It("Rejects user value for non-editable field", func() {
				createCatalogItem("cat-noneditable", true, []*privatev1.FieldDefinition{
					privatev1.FieldDefinition_builder{
						Path:     "pull_secret",
						Editable: false,
						Default:  structpb.NewStringValue("forced-secret"),
					}.Build(),
				})

				_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "cat-noneditable"}.Build(),
							PullSecret:  new("user-secret"),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("not editable"))
			})

			DescribeTable("validates editable field against JSON Schema",
				func(catID string, value string, expectError bool) {
					createCatalogItem(catID, true, []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:             "pull_secret",
							Editable:         true,
							ValidationSchema: `{"type":"string","minLength":10}`,
						}.Build(),
					})

					response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
						Object: privatev1.Cluster_builder{
							Spec: privatev1.ClusterSpec_builder{
								CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: catID}.Build(),
								PullSecret:  new(value),
							}.Build(),
							Status: privatev1.ClusterStatus_builder{
								Hub: "my-hub-id",
							}.Build(),
						}.Build(),
					}.Build())
					if expectError {
						Expect(err).To(HaveOccurred())
						status, ok := grpcstatus.FromError(err)
						Expect(ok).To(BeTrue())
						Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
						Expect(status.Message()).To(ContainSubstring("validation failed for field 'pull_secret'"))
					} else {
						Expect(err).ToNot(HaveOccurred())
						Expect(response.GetObject().GetSpec().GetPullSecret()).To(Equal(value))
					}
				},
				Entry("rejects value below minLength", "cat-schema-reject", "short-val", true),
				Entry("accepts value meeting minLength", "cat-schema-accept", "long-enough-value", false),
			)

			It("Applies default for editable field when not provided", func() {
				createCatalogItem("cat-default", true, []*privatev1.FieldDefinition{
					privatev1.FieldDefinition_builder{
						Path:     "pull_secret",
						Editable: true,
						Default:  structpb.NewStringValue("default-secret"),
					}.Build(),
				})

				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "cat-default"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()
				Expect(object.GetSpec().GetPullSecret()).To(Equal("default-secret"))
			})

			It("Applies spec defaults from template when created via catalog item", func() {
				// Create a template with spec defaults:
				templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = templatesDao.Create().
					SetObject(
						privatev1.ClusterTemplate_builder{
							Id: "template-with-defaults",
							Metadata: privatev1.Metadata_builder{
								Name:   "template-with-defaults-name",
								Tenant: auth.SharedTenant,
							}.Build(),
							Title:       "Template with defaults",
							Description: "Template with spec defaults",
							NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
								"worker": privatev1.ClusterTemplateNodeSet_builder{
									HostType: privatev1.HostTypeReference_builder{Id: "acme-1ti-id"}.Build(),
									Size:     2,
								}.Build(),
							},
							SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
								SshPublicKey: proto.String("ssh-rsa TEMPLATE_DEFAULT_KEY"),
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a catalog item referencing the template with defaults:
				_, err = catalogItemsDao.Create().SetObject(
					privatev1.ClusterCatalogItem_builder{
						Id: "cat-with-defaults",
						Metadata: privatev1.Metadata_builder{
							Name:   "cat-with-defaults-name",
							Tenant: "shared",
						}.Build(),
						Title:     "Catalog Item with Template Defaults",
						Published: true,
						Template:  privatev1.ClusterTemplateReference_builder{Id: "template-with-defaults"}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a cluster via catalog item without specifying ssh_public_key:
				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "cat-with-defaults"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()

				// Verify spec defaults from template are applied:
				Expect(object.GetSpec().GetSshPublicKey()).To(Equal("ssh-rsa TEMPLATE_DEFAULT_KEY"))

				// Verify node sets are also populated:
				nodeSets := object.GetSpec().GetNodeSets()
				Expect(nodeSets).To(HaveLen(1))
				Expect(nodeSets).To(HaveKey("worker"))
				Expect(nodeSets["worker"].GetHostType().GetId()).To(Equal("acme-1ti-id"))
				Expect(nodeSets["worker"].GetSize()).To(Equal(int32(2)))
			})

			It("Applies version from template spec_defaults via catalog item", func() {
				// Seed a non-default ClusterVersion for the template to pin:
				seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Name:   "4-18-0",
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.ClusterVersionSpec_builder{
						Image:   "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
						Enabled: proto.Bool(true),
						Version: "4.18.0",
					}.Build(),
				}.Build())

				// Create a template whose spec_defaults pins version:
				templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = templatesDao.Create().
					SetObject(
						privatev1.ClusterTemplate_builder{
							Id: "template-version-pinned",
							Metadata: privatev1.Metadata_builder{
								Name:   "template-version-pinned-name",
								Tenant: auth.SharedTenant,
							}.Build(),
							Title:       "Version-pinned template",
							Description: "Template that pins version via spec_defaults",
							NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
								"worker": privatev1.ClusterTemplateNodeSet_builder{
									HostType: &privatev1.HostTypeReference{Id: "acme-1ti-id"},
									Size:     2,
								}.Build(),
							},
							SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
								Version: &privatev1.ClusterVersionReference{Name: "4-18-0"},
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a catalog item referencing the version-pinned template
				// (no version field_definition):
				_, err = catalogItemsDao.Create().SetObject(
					privatev1.ClusterCatalogItem_builder{
						Id: "cat-version-pinned",
						Metadata: privatev1.Metadata_builder{
							Name:   "cat-version-pinned-name",
							Tenant: "shared",
						}.Build(),
						Title:     "Catalog Item with Version-Pinned Template",
						Published: true,
						Template:  &privatev1.ClusterTemplateReference{Id: "template-version-pinned"},
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a cluster via catalog item without specifying version:
				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: &privatev1.ClusterCatalogItemReference{Id: "cat-version-pinned"},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// Template spec_defaults.version should win over system default:
				Expect(response.GetObject().GetSpec().GetVersion().GetName()).To(Equal("4-18-0"))
			})

			It("Field definition version overrides template spec_defaults via catalog item", func() {
				// Seed a non-default ClusterVersion for the field_definition to set:
				seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Name:   "4-19-0",
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.ClusterVersionSpec_builder{
						Image:   "quay.io/openshift-release-dev/ocp-release:4.19.0-multi",
						Enabled: proto.Bool(true),
						Version: "4.19.0",
					}.Build(),
				}.Build())

				// Create a template whose spec_defaults pins version to 4-17-0
				// (the system default, but specified explicitly as a template default):
				templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = templatesDao.Create().
					SetObject(
						privatev1.ClusterTemplate_builder{
							Id: "template-fd-override",
							Metadata: privatev1.Metadata_builder{
								Name:   "template-fd-override-name",
								Tenant: auth.SharedTenant,
							}.Build(),
							Title:       "Template for FD override test",
							Description: "Template whose spec_defaults are overridden by field_definitions",
							NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
								"worker": privatev1.ClusterTemplateNodeSet_builder{
									HostType: &privatev1.HostTypeReference{Id: "acme-1ti-id"},
									Size:     2,
								}.Build(),
							},
							SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
								Version: &privatev1.ClusterVersionReference{Name: "4-17-0"},
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a catalog item with a field_definition that overrides version:
				_, err = catalogItemsDao.Create().SetObject(
					privatev1.ClusterCatalogItem_builder{
						Id: "cat-fd-override",
						Metadata: privatev1.Metadata_builder{
							Name:   "cat-fd-override-name",
							Tenant: "shared",
						}.Build(),
						Title:     "Catalog Item with FD version override",
						Published: true,
						Template:  &privatev1.ClusterTemplateReference{Id: "template-fd-override"},
						FieldDefinitions: []*privatev1.FieldDefinition{
							privatev1.FieldDefinition_builder{
								Path:     "version",
								Editable: false,
								Default:  structpb.NewStructValue(&structpb.Struct{Fields: map[string]*structpb.Value{"name": structpb.NewStringValue("4-19-0")}}),
							}.Build(),
						},
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a cluster via catalog item without specifying version:
				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: &privatev1.ClusterCatalogItemReference{Id: "cat-fd-override"},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// Field definition default should win over template spec_defaults:
				Expect(response.GetObject().GetSpec().GetVersion().GetName()).To(Equal("4-19-0"))
			})

			It("Falls back to system default version via catalog item when nothing sets version", func() {
				// Create a template with no spec_defaults.version:
				templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = templatesDao.Create().
					SetObject(
						privatev1.ClusterTemplate_builder{
							Id: "template-no-version",
							Metadata: privatev1.Metadata_builder{
								Name:   "template-no-version-name",
								Tenant: auth.SharedTenant,
							}.Build(),
							Title:       "Template without version default",
							Description: "Template with no spec_defaults.version",
							NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
								"worker": privatev1.ClusterTemplateNodeSet_builder{
									HostType: &privatev1.HostTypeReference{Id: "acme-1ti-id"},
									Size:     2,
								}.Build(),
							},
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a catalog item with no version field_definition:
				_, err = catalogItemsDao.Create().SetObject(
					privatev1.ClusterCatalogItem_builder{
						Id: "cat-no-version",
						Metadata: privatev1.Metadata_builder{
							Name:   "cat-no-version-name",
							Tenant: "shared",
						}.Build(),
						Title:     "Catalog Item without version",
						Published: true,
						Template:  &privatev1.ClusterTemplateReference{Id: "template-no-version"},
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a cluster via catalog item without specifying version:
				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: &privatev1.ClusterCatalogItemReference{Id: "cat-no-version"},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// System default (4-17-0) should be used as last resort:
				Expect(response.GetObject().GetSpec().GetVersion().GetName()).To(Equal("4-17-0"))
			})

			It("Fails when catalog item has no template", func() {
				_, err := catalogItemsDao.Create().SetObject(
					privatev1.ClusterCatalogItem_builder{
						Id: "cat-no-template",
						Metadata: privatev1.Metadata_builder{
							Name:   "cat-no-template-name",
							Tenant: "shared",
						}.Build(),
						Title:     "Catalog Item Without Template",
						Published: true,
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "cat-no-template"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("no template"))
			})

			It("Rejects changing catalog_item on update", func() {
				createCatalogItem("cat-immut", true, nil)

				createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "cat-immut"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := createResponse.GetObject()

				_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "different-catalog-item"}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.catalog_item"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal(
					"cannot change spec.catalog_item from 'cat-immut' to 'different-catalog-item': catalog item is immutable",
				))
			})
		})

		It("Allows changing version to a valid version on update", func() {
			versionName := "4-17-0"

			// Create a cluster with an explicit version:
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster",
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						Version:  &privatev1.ClusterVersionReference{Name: versionName},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Seed a second usable ClusterVersion:
			seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
				Id: uuid.New(),
				Metadata: privatev1.Metadata_builder{
					Name:   "4-18-0",
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.ClusterVersionSpec_builder{
					Image:   "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
					Enabled: proto.Bool(true),
					Version: "4.18.0",
				}.Build(),
			}.Build())

			// Change the version:
			newVersion := "4-18-0"
			updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						Version: &privatev1.ClusterVersionReference{Name: newVersion},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.version"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetVersion().GetName()).To(Equal("4-18-0"))
		})

		It("Preserves existing version when update sends empty name", func() {
			versionName := "4-17-0"

			// Create a cluster with an explicit version:
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster",
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						Version:  &privatev1.ClusterVersionReference{Name: versionName},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Update with an explicitly empty version name:
			updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						Version: &privatev1.ClusterVersionReference{Name: ""},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.version"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetVersion().GetName()).To(Equal("4-17-0"))
		})

		It("Rejects changing version to a non-existent version", func() {
			versionName := "4-17-0"

			// Create a cluster with an explicit version:
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Spec: privatev1.ClusterSpec_builder{
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						Version:  &privatev1.ClusterVersionReference{Name: versionName},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Try to change to a non-existent version:
			nonExistent := "does-not-exist"
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						Version: &privatev1.ClusterVersionReference{Name: nonExistent},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.version"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		It("Rejects changing version to a disabled version", func() {
			versionName := "4-17-0"

			// Create a cluster with an explicit version:
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Spec: privatev1.ClusterSpec_builder{
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						Version:  &privatev1.ClusterVersionReference{Name: versionName},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Seed a disabled ClusterVersion:
			seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
				Id: uuid.New(),
				Metadata: privatev1.Metadata_builder{
					Name:   "4-18-0-disabled",
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.ClusterVersionSpec_builder{
					Image:   "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
					Enabled: proto.Bool(false),
					Version: "4.18.0",
				}.Build(),
			}.Build())

			// Try to change to the disabled version:
			disabledName := "4-18-0-disabled"
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						Version: &privatev1.ClusterVersionReference{Name: disabledName},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.version"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		It("Rejects changing version to an obsolete version", func() {
			versionName := "4-17-0"

			// Create a cluster with an explicit version:
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Spec: privatev1.ClusterSpec_builder{
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						Version:  &privatev1.ClusterVersionReference{Name: versionName},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Seed an obsolete ClusterVersion:
			seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
				Id: uuid.New(),
				Metadata: privatev1.Metadata_builder{
					Name:   "4-16-0-obsolete",
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.ClusterVersionSpec_builder{
					Image:   "quay.io/openshift-release-dev/ocp-release:4.16.0-multi",
					Enabled: proto.Bool(true),
					Version: "4.16.0",
					State:   privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE,
				}.Build(),
			}.Build())

			// Try to change to the obsolete version:
			obsoleteName := "4-16-0-obsolete"
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						Version: &privatev1.ClusterVersionReference{Name: obsoleteName},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.version"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		It("Rejects non-existent version on nil-mask full-object update", func() {
			versionName := "4-17-0"

			// Create a cluster with a valid version:
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Spec: privatev1.ClusterSpec_builder{
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						Version:  &privatev1.ClusterVersionReference{Name: versionName},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Full-object update with nil mask and a non-existent version:
			object.GetSpec().SetVersion(&privatev1.ClusterVersionReference{Name: "does-not-exist"})
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: object,
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		Describe("ClusterVersion validation", func() {
			var validatedServer *PrivateClustersServer

			BeforeEach(func() {
				var err error
				validatedServer, err = NewPrivateClustersServer().
					SetLogger(logger).
					SetAttributionLogic(attribution).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
			})

			It("Rejects create with non-existent version", func() {
				_, err := validatedServer.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
							Version:  &privatev1.ClusterVersionReference{Name: "does-not-exist"},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("cluster version 'does-not-exist' not found"))
			})

			It("Rejects create with disabled version", func() {
				// Seed a disabled ClusterVersion:
				seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Name:   "4-18-0-disabled",
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.ClusterVersionSpec_builder{
						Image:   "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
						Enabled: proto.Bool(false),
						Version: "4.18.0",
					}.Build(),
				}.Build())

				disabledName := "4-18-0-disabled"
				_, err := validatedServer.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
							Version:  &privatev1.ClusterVersionReference{Name: disabledName},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("is disabled"))
			})

			It("Rejects create with obsolete version", func() {
				// Seed an obsolete ClusterVersion:
				seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Name:   "4-16-0-obsolete",
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.ClusterVersionSpec_builder{
						Image:   "quay.io/openshift-release-dev/ocp-release:4.16.0-multi",
						Enabled: proto.Bool(true),
						Version: "4.16.0",
						State:   privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE,
					}.Build(),
				}.Build())

				obsoleteName := "4-16-0-obsolete"
				_, err := validatedServer.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
							Version:  &privatev1.ClusterVersionReference{Name: obsoleteName},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("is obsolete"))
			})

			It("Resolves system default version when none specified", func() {
				// The BeforeEach in the parent Behaviour block already seeds a default
				// ClusterVersion with name "4-17-0" and is_default=true.
				response, err := validatedServer.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetSpec().GetVersion().GetName()).To(Equal("4-17-0"))
			})

			It("Rejects create when no system default version exists", func() {
				// Delete the default ClusterVersion:
				clusterVersionsDao, err := dao.NewGenericDAO[*privatev1.ClusterVersion]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = clusterVersionsDao.Delete().
					SetId("cv-default").
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = validatedServer.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("no version specified and no system default"))
			})

			It("Rejects create when system default version is disabled", func() {
				// Replace the existing default with a disabled one:
				clusterVersionsDao, err := dao.NewGenericDAO[*privatev1.ClusterVersion]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = clusterVersionsDao.Delete().
					SetId("cv-default").
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = clusterVersionsDao.Create().
					SetObject(
						privatev1.ClusterVersion_builder{
							Id: uuid.New(),
							Metadata: privatev1.Metadata_builder{
								Name:   "4-18-0-disabled-default",
								Tenant: auth.SharedTenant,
							}.Build(),
							Spec: privatev1.ClusterVersionSpec_builder{
								Image:     "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
								Enabled:   proto.Bool(false),
								IsDefault: proto.Bool(true),
								Version:   "4.18.0",
								State:     privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE,
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = validatedServer.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("is disabled"))
			})

			It("Rejects create when system default version is obsolete", func() {
				// Replace the existing default with an obsolete one:
				clusterVersionsDao, err := dao.NewGenericDAO[*privatev1.ClusterVersion]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = clusterVersionsDao.Delete().
					SetId("cv-default").
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = clusterVersionsDao.Create().
					SetObject(
						privatev1.ClusterVersion_builder{
							Id: uuid.New(),
							Metadata: privatev1.Metadata_builder{
								Name:   "4-16-0-obsolete-default",
								Tenant: auth.SharedTenant,
							}.Build(),
							Spec: privatev1.ClusterVersionSpec_builder{
								Image:     "quay.io/openshift-release-dev/ocp-release:4.16.0-multi",
								Enabled:   proto.Bool(true),
								IsDefault: proto.Bool(true),
								Version:   "4.16.0",
								State:     privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE,
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = validatedServer.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Spec: privatev1.ClusterSpec_builder{
							Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("is obsolete"))
			})
		})

		Describe("Version", func() {
			createCluster := func() *privatev1.Cluster {
				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				return response.GetObject()
			}

			It("Is zero on create", func() {
				object := createCluster()
				Expect(object.GetMetadata().GetVersion()).To(BeZero())
			})

			It("Is zero when retrieved after create", func() {
				object := createCluster()
				id := object.GetId()
				getResponse, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{
					Id: id,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object = getResponse.GetObject()
				Expect(object.GetMetadata().GetVersion()).To(BeZero())
			})

			It("Is zero when listed after create", func() {
				object := createCluster()
				id := object.GetId()
				listResponse, err := server.List(ctx, privatev1.ClustersListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == %q", id)),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				items := listResponse.GetItems()
				Expect(items).To(HaveLen(1))
				item := items[0]
				Expect(item.GetMetadata().GetVersion()).To(BeZero())
			})

			It("Increments on update", func() {
				// Create the object:
				object := createCluster()
				version := object.GetMetadata().GetVersion()

				// Update the object and verify that the version has been incremented:
				object.GetStatus().SetHub("hub-v1")
				updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: object,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object = updateResponse.GetObject()
				Expect(object.GetMetadata().GetVersion()).To(BeNumerically(">", version))
			})

			It("Does not increment on no-op update", func() {
				// Create the object and get the initialversion:
				object := createCluster()
				version := object.GetMetadata().GetVersion()

				// Send an update request that doesn't really update anything, and then verify that the
				// version has not been incremented:
				updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: object,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object = updateResponse.GetObject()
				Expect(object.GetMetadata().GetVersion()).To(Equal(version))
			})

			It("Lock succeeds when version matches", func() {
				// Create the object:
				object := createCluster()

				// Update with lock enabled and the right version:
				object.GetStatus().SetHub("your-hub-id")
				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: object,
					Lock:   true,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			It("Lock fails when version does not match", func() {
				// Create the object:
				object := createCluster()

				// Try to update with lock enabled but a wrong version:
				object.GetMetadata().SetVersion(math.MaxInt32)
				object.GetStatus().SetHub("your-hub-id")
				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: object,
					Lock:   true,
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.Aborted))

				// Verify that our changes were not applied:
				getResponse, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object = getResponse.GetObject()
				Expect(object.GetStatus().GetHub()).To(Equal("my-hub-id"))
			})

			It("Lock is not enabled by default", func() {
				// Create the object:
				object := createCluster()

				// Send an update with a wrong version in the metadata but without enabling lock. The update
				// should succeed because optimistic locking is not enabled.
				object.GetMetadata().SetVersion(math.MaxInt32)
				object.GetStatus().SetHub("your-hub-id")
				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: object,
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// Verify that our changes were applied:
				getResponse, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object = getResponse.GetObject()
				Expect(object.GetStatus().GetHub()).To(Equal("your-hub-id"))

			})
		})

		Describe("Dry run", func() {
			It("Returns resolved cluster with template path", func() {
				response, err := server.Create(dryRunCtx(), privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-cluster",
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-name"}.Build(),
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{
									HostType: privatev1.HostTypeReference_builder{Id: "acme-1ti-name"}.Build(),
									Size:     7,
								}.Build(),
							},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()
				Expect(object).ToNot(BeNil())
				Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("my-template-id"))
				nodeSets := object.GetSpec().GetNodeSets()
				Expect(nodeSets).To(HaveKey("compute"))
				Expect(nodeSets["compute"].GetHostType().GetId()).To(Equal("acme-1ti-id"))
			})

			It("Returns resolved cluster with catalog item path", func() {
				catalogItemsDao, err := dao.NewGenericDAO[*privatev1.ClusterCatalogItem]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = catalogItemsDao.Create().SetObject(
					privatev1.ClusterCatalogItem_builder{
						Id: "cat-dry-run",
						Metadata: privatev1.Metadata_builder{
							Name:   "cat-dry-run-name",
							Tenant: "shared",
						}.Build(),
						Title:     "Dry Run Catalog Item",
						Published: true,
						Template:  privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				response, err := server.Create(dryRunCtx(), privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-cluster",
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "cat-dry-run"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()
				Expect(object).ToNot(BeNil())
				Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("my-template-id"))
				Expect(object.GetSpec().GetCatalogItem().GetId()).To(Equal("cat-dry-run"))
			})

			It("Does not persist the object", func() {
				_, err := server.Create(dryRunCtx(), privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-cluster",
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-name"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				listResponse, err := server.List(ctx, privatev1.ClustersListRequest_builder{}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(listResponse.GetTotal()).To(Equal(int32(0)))
			})

			It("Returns same error as real creation for invalid template", func() {
				_, realErr := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-cluster",
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "non-existent-template"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(realErr).To(HaveOccurred())

				_, dryRunErr := server.Create(dryRunCtx(), privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-cluster",
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "non-existent-template"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(dryRunErr).To(HaveOccurred())
				Expect(grpcstatus.Code(dryRunErr)).To(Equal(grpcstatus.Code(realErr)))
				Expect(grpcstatus.Convert(dryRunErr).Message()).To(Equal(grpcstatus.Convert(realErr).Message()))
			})
		})
	})
})
