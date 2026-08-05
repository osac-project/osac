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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

var _ = Describe("Private compute instances server", func() {
	BeforeEach(func() {
		var err error

		// Create a default test virtual network and subnet for tests that don't explicitly create one:
		vnDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		vn := privatev1.VirtualNetwork_builder{
			Id: "test-vnet",
			Metadata: privatev1.Metadata_builder{
				Tenant: auth.SharedTenant,
			}.Build(),
		}.Build()

		_, err = vnDao.Create().SetObject(vn).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		subnetsDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		subnet := privatev1.Subnet_builder{
			Id: "test-subnet",
			Metadata: privatev1.Metadata_builder{
				Tenant: auth.SharedTenant,
			}.Build(),
			Spec: privatev1.SubnetSpec_builder{
				VirtualNetwork: "test-vnet",
				Ipv4Cidr:       new("10.0.0.0/24"),
			}.Build(),
			Status: privatev1.SubnetStatus_builder{
				State: privatev1.SubnetState_SUBNET_STATE_READY,
			}.Build(),
		}.Build()

		_, err = subnetsDao.Create().SetObject(subnet).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	// Helper function to create a NetworkClass for test setup
	createTestNetworkClass := func(ctx context.Context) *privatev1.NetworkClass {
		ncDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		nc := privatev1.NetworkClass_builder{
			ImplementationStrategy: "test-strategy",
			Metadata: privatev1.Metadata_builder{
				Tenant: auth.SharedTenant,
			}.Build(),
			Capabilities: privatev1.NetworkClassCapabilities_builder{
				SupportsIpv4:      true,
				SupportsIpv6:      true,
				SupportsDualStack: true,
			}.Build(),
			Status: privatev1.NetworkClassStatus_builder{
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			}.Build(),
		}.Build()

		response, err := ncDao.Create().SetObject(nc).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		return response.GetObject()
	}

	// Helper function to create a VirtualNetwork for test setup
	createTestVirtualNetwork := func(ctx context.Context, networkClassID string) *privatev1.VirtualNetwork {
		vnDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		vn := privatev1.VirtualNetwork_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: auth.SharedTenant,
			}.Build(),
			Spec: privatev1.VirtualNetworkSpec_builder{
				Ipv4Cidr:     new("10.0.0.0/16"),
				NetworkClass: networkClassID,
			}.Build(),
			Status: privatev1.VirtualNetworkStatus_builder{
				State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
			}.Build(),
		}.Build()

		response, err := vnDao.Create().SetObject(vn).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		return response.GetObject()
	}

	// Helper function to create a Subnet with specified state
	createTestSubnet := func(ctx context.Context, vnID string, state privatev1.SubnetState) *privatev1.Subnet {
		subnetDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		subnet := privatev1.Subnet_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: auth.SharedTenant,
			}.Build(),
			Spec: privatev1.SubnetSpec_builder{
				VirtualNetwork: vnID,
				Ipv4Cidr:       new("10.0.1.0/24"),
			}.Build(),
			Status: privatev1.SubnetStatus_builder{
				State: state,
			}.Build(),
		}.Build()

		response, err := subnetDao.Create().SetObject(subnet).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		return response.GetObject()
	}

	// Helper function to create a SecurityGroup with specified state
	createTestSecurityGroup := func(ctx context.Context, vnID string, state privatev1.SecurityGroupState) *privatev1.SecurityGroup {
		sgDao, err := dao.NewGenericDAO[*privatev1.SecurityGroup]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		sg := privatev1.SecurityGroup_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: auth.SharedTenant,
			}.Build(),
			Spec: privatev1.SecurityGroupSpec_builder{
				VirtualNetwork: vnID,
			}.Build(),
			Status: privatev1.SecurityGroupStatus_builder{
				State: state,
			}.Build(),
		}.Build()

		response, err := sgDao.Create().SetObject(sg).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		return response.GetObject()
	}

	Describe("Builder", func() {
		It("Creates server with logger", func() {
			server, err := NewPrivateComputeInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Doesn't create server without logger", func() {
			server, err := NewPrivateComputeInstancesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewPrivateComputeInstancesServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateComputeInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *PrivateComputeInstancesServer

		BeforeEach(func() {
			var err error

			// Create the server:
			server, err = NewPrivateComputeInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create a default InstanceType for tests that need it:
			instanceTypesDao, err := dao.NewGenericDAO[*privatev1.InstanceType]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = instanceTypesDao.Create().SetObject(
				privatev1.InstanceType_builder{
					Id: "standard-4-16",
					Metadata: privatev1.Metadata_builder{
						Name:   "standard-4-16",
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:     4,
						MemoryGib: 16,
						State:     privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
		})

		// Helper function to create a template
		createTemplate := func(templateID string) {
			// Create a template DAO to insert a template
			templatesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceTemplate]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create default values for parameters
			cpuDefault, err := anypb.New(wrapperspb.Int32(1))
			Expect(err).ToNot(HaveOccurred())
			memoryDefault, err := anypb.New(wrapperspb.Int32(2))
			Expect(err).ToNot(HaveOccurred())

			template := privatev1.ComputeInstanceTemplate_builder{
				Id:          templateID,
				Title:       "Test Template",
				Description: "Test template for validation",
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				Parameters: []*privatev1.ComputeInstanceTemplateParameterDefinition{
					{
						Name:        "cpu_count",
						Title:       "CPU Count",
						Description: "Number of CPU cores",
						Required:    false,
						Type:        "type.googleapis.com/google.protobuf.Int32Value",
						Default:     cpuDefault,
					},
					{
						Name:        "memory_gb",
						Title:       "Memory (GB)",
						Description: "Amount of memory in GB",
						Required:    false,
						Type:        "type.googleapis.com/google.protobuf.Int32Value",
						Default:     memoryDefault,
					},
				},
				SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
					InstanceType: new("standard-4-16"),
					Image: privatev1.ComputeInstanceImage_builder{
						SourceType: "registry",
						SourceRef:  "quay.io/containerdisks/fedora:latest",
					}.Build(),
					BootDisk: privatev1.ComputeInstanceDisk_builder{
						SizeGib: 10,
					}.Build(),
					RunStrategy: new("Always"),
				}.Build(),
			}.Build()

			_, err = templatesDao.Create().SetObject(template).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
		}

		It("Creates object", func() {
			// Create a template first
			createTemplate("general.small")

			// Create template parameters
			templateParams := make(map[string]*anypb.Any)
			cpuParam, err := anypb.New(wrapperspb.Int32(2))
			Expect(err).ToNot(HaveOccurred())
			templateParams["cpu_count"] = cpuParam

			memoryParam, err := anypb.New(wrapperspb.Int32(4))
			Expect(err).ToNot(HaveOccurred())
			templateParams["memory_gb"] = memoryParam

			response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template:           "general.small",
						TemplateParameters: templateParams,
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{
								Subnet: "test-subnet",
							}.Build(),
						},
					}.Build(),
					Status: privatev1.ComputeInstanceStatus_builder{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetSpec().GetTemplate()).To(Equal("general.small"))
			Expect(object.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING))
		})

		It("List objects", func() {
			// Create templates and objects:
			const count = 10
			for i := range count {
				templateID := fmt.Sprintf("template-%d", i)
				createTemplate(templateID)

				_, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: templateID,
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
						}.Build(),
						Status: privatev1.ComputeInstanceStatus_builder{
							State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, privatev1.ComputeInstancesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			// Create templates and objects:
			const count = 10
			for i := range count {
				templateID := fmt.Sprintf("template-limit-%d", i)
				createTemplate(templateID)

				_, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: templateID,
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
						}.Build(),
						Status: privatev1.ComputeInstanceStatus_builder{
							State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects with limit:
			response, err := server.List(ctx, privatev1.ComputeInstancesListRequest_builder{
				Limit: new(int32(5)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(5))
		})

		It("List objects with offset", func() {
			// Create templates and objects:
			const count = 10
			for i := range count {
				templateID := fmt.Sprintf("template-offset-%d", i)
				createTemplate(templateID)

				_, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: templateID,
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
						}.Build(),
						Status: privatev1.ComputeInstanceStatus_builder{
							State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects with offset:
			response, err := server.List(ctx, privatev1.ComputeInstancesListRequest_builder{
				Offset: new(int32(5)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(5))
		})

		It("Gets object", func() {
			// Create a template first
			createTemplate("general.small")

			// Create an object:
			createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: "general.small",
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{
								Subnet: "test-subnet",
							}.Build(),
						},
					}.Build(),
					Status: privatev1.ComputeInstanceStatus_builder{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResponse).ToNot(BeNil())
			createdObject := createResponse.GetObject()
			Expect(createdObject).ToNot(BeNil())
			id := createdObject.GetId()
			Expect(id).ToNot(BeEmpty())

			// Get the object:
			getResponse, err := server.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse).ToNot(BeNil())
			object := getResponse.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).To(Equal(id))
			Expect(object.GetSpec().GetTemplate()).To(Equal("general.small"))
			Expect(object.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING))
		})

		It("Updates object", func() {
			// Create a template first
			createTemplate("general.small")

			// Create an object:
			createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: "general.small",
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{
								Subnet: "test-subnet",
							}.Build(),
						},
					}.Build(),
					Status: privatev1.ComputeInstanceStatus_builder{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResponse).ToNot(BeNil())
			createdObject := createResponse.GetObject()
			Expect(createdObject).ToNot(BeNil())
			id := createdObject.GetId()
			Expect(id).ToNot(BeEmpty())

			// Update the object (only status, template is immutable):
			updateResponse, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id: id,
					Status: privatev1.ComputeInstanceStatus_builder{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"status.state"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse).ToNot(BeNil())
			object := updateResponse.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).To(Equal(id))
			Expect(object.GetSpec().GetTemplate()).To(Equal("general.small"))
			Expect(object.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING))
		})

		It("Deletes object", func() {
			// Create a template first
			createTemplate("general.small")

			// Create an object:
			createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: "general.small",
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{
								Subnet: "test-subnet",
							}.Build(),
						},
					}.Build(),
					Status: privatev1.ComputeInstanceStatus_builder{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResponse).ToNot(BeNil())
			createdObject := createResponse.GetObject()
			Expect(createdObject).ToNot(BeNil())
			id := createdObject.GetId()
			Expect(id).ToNot(BeEmpty())

			// Delete the object:
			deleteResponse, err := server.Delete(ctx, privatev1.ComputeInstancesDeleteRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(deleteResponse).ToNot(BeNil())

			// Verify the object is deleted:
			getResponse, err := server.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(getResponse).To(BeNil())
		})

		It("Handles non-existent object", func() {
			// Try to get a non-existent object:
			getResponse, err := server.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{
				Id: "non-existent-id",
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(getResponse).To(BeNil())
		})

		It("Handles empty object in create request", func() {
			// Try to create with nil object:
			response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})

		It("Handles empty object in update request", func() {
			// Try to update with nil object:
			response, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})

		It("Handles empty ID in get request", func() {
			// Try to get with empty ID:
			response, err := server.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})

		It("Handles empty ID in delete request", func() {
			// Try to delete with empty ID:
			response, err := server.Delete(ctx, privatev1.ComputeInstancesDeleteRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})

		It("Validates template exists on create", func() {
			// Try to create with non-existent template:
			response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: "non-existent-template",
					}.Build(),
					Status: privatev1.ComputeInstanceStatus_builder{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})

		It("Rejects changing template on update", func() {
			createTemplate("existing-template")

			createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: "existing-template",
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{
								Subnet: "test-subnet",
							}.Build(),
						},
					}.Build(),
					Status: privatev1.ComputeInstanceStatus_builder{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResponse).ToNot(BeNil())

			id := createResponse.GetObject().GetId()

			// Try to change the template:
			updateResponse, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id: id,
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: "different-template",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.template"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(updateResponse).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("template is immutable"))
		})

		It("Rejects changing template_parameters on update", func() {
			createTemplate("params-template")

			// Create with initial parameters:
			cpuParam, err := anypb.New(wrapperspb.Int32(2))
			Expect(err).ToNot(HaveOccurred())

			createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template:           "params-template",
						TemplateParameters: map[string]*anypb.Any{"cpu_count": cpuParam},
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{
								Subnet: "test-subnet",
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResponse).ToNot(BeNil())

			id := createResponse.GetObject().GetId()

			// Try to change template_parameters:
			newCpuParam, err := anypb.New(wrapperspb.Int32(8))
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id: id,
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template:           "params-template",
						TemplateParameters: map[string]*anypb.Any{"cpu_count": newCpuParam},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.template_parameters"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(updateResponse).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("template parameters are immutable"))
		})

		It("Allows update when template in mask but unchanged", func() {
			createTemplate("same-template")

			createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: "same-template",
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{
								Subnet: "test-subnet",
							}.Build(),
						},
					}.Build(),
					Status: privatev1.ComputeInstanceStatus_builder{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResponse).ToNot(BeNil())

			id := createResponse.GetObject().GetId()

			// Update with template in mask but same value:
			updateResponse, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id: id,
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: "same-template",
					}.Build(),
					Status: privatev1.ComputeInstanceStatus_builder{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.template", "status.state"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse).ToNot(BeNil())
			Expect(updateResponse.GetObject().GetSpec().GetTemplate()).To(Equal("same-template"))
			Expect(updateResponse.GetObject().GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING))
		})

		It("Validates template ID is not empty", func() {
			// Try to create with empty template ID:
			response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: "",
					}.Build(),
					Status: privatev1.ComputeInstanceStatus_builder{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})

		It("Applies template spec defaults when user omits spec fields", func() {
			createTemplate("defaults-template")

			// Create a compute instance without any spec fields — validation should pass
			// because template defaults cover all required fields.
			response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: "defaults-template",
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{
								Subnet: "test-subnet",
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())

			spec := response.GetObject().GetSpec()
			// Template defaults should be stored:
			Expect(spec.GetInstanceType()).To(Equal("standard-4-16"))
			Expect(spec.GetRunStrategy()).To(Equal("Always"))
			Expect(spec.GetImage().GetSourceType()).To(Equal("registry"))
			Expect(spec.GetImage().GetSourceRef()).To(Equal("quay.io/containerdisks/fedora:latest"))
			Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(10)))
			// Template reference should be preserved:
			Expect(spec.GetTemplate()).To(Equal("defaults-template"))
		})

		It("User-provided spec fields override template defaults", func() {
			createTemplate("override-template")

			// Create with user-provided run_strategy (overrides template default):
			response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template:    "override-template",
						RunStrategy: new("Halted"),
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{
								Subnet: "test-subnet",
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())

			spec := response.GetObject().GetSpec()
			// User-provided values should be stored:
			Expect(spec.GetRunStrategy()).To(Equal("Halted"))
			// Template defaults should be stored:
			Expect(spec.GetInstanceType()).To(Equal("standard-4-16"))
			Expect(spec.GetImage().GetSourceType()).To(Equal("registry"))
			Expect(spec.GetImage().GetSourceRef()).To(Equal("quay.io/containerdisks/fedora:latest"))
			Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(10)))
		})

		It("Rejects creation when required spec fields are missing", func() {
			// Create a template WITHOUT spec defaults:
			templatesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceTemplate]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			template := privatev1.ComputeInstanceTemplate_builder{
				Id:          "no-defaults-template",
				Title:       "No Defaults Template",
				Description: "Template without spec defaults",
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
			}.Build()
			_, err = templatesDao.Create().SetObject(template).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create a compute instance without user-provided spec fields:
			response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: "no-defaults-template",
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{
								Subnet: "test-subnet",
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())

			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("boot_disk"))
			Expect(status.Message()).To(ContainSubstring("image"))
			Expect(status.Message()).To(ContainSubstring("instance_type"))
			Expect(status.Message()).To(ContainSubstring("run_strategy"))
		})

		It("Accepts creation when user provides all required fields without template defaults", func() {
			// Create a template WITHOUT spec defaults:
			templatesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceTemplate]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			template := privatev1.ComputeInstanceTemplate_builder{
				Id:          "bare-template",
				Title:       "Bare Template",
				Description: "Template without defaults",
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
			}.Build()
			_, err = templatesDao.Create().SetObject(template).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create with all required fields provided by user:
			response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template:     "bare-template",
						InstanceType: new("standard-4-16"),
						Image: privatev1.ComputeInstanceImage_builder{
							SourceType: "registry",
							SourceRef:  "quay.io/containerdisks/fedora:latest",
						}.Build(),
						BootDisk: privatev1.ComputeInstanceDisk_builder{
							SizeGib: 20,
						}.Build(),
						RunStrategy: new("Always"),
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{
								Subnet: "test-subnet",
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			Expect(response.GetObject().GetSpec().GetInstanceType()).To(Equal("standard-4-16"))
		})

		It("Partial defaults plus partial user input satisfies validation", func() {
			// Create a template with only some spec defaults:
			templatesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceTemplate]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			template := privatev1.ComputeInstanceTemplate_builder{
				Id:          "partial-defaults-template",
				Title:       "Partial Defaults Template",
				Description: "Template with partial spec defaults",
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
					InstanceType: new("standard-4-16"),
					RunStrategy:  new("Always"),
				}.Build(),
			}.Build()
			_, err = templatesDao.Create().SetObject(template).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// User provides the remaining required fields:
			response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: "partial-defaults-template",
						Image: privatev1.ComputeInstanceImage_builder{
							SourceType: "registry",
							SourceRef:  "quay.io/containerdisks/fedora:latest",
						}.Build(),
						BootDisk: privatev1.ComputeInstanceDisk_builder{
							SizeGib: 20,
						}.Build(),
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{
								Subnet: "test-subnet",
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())

			spec := response.GetObject().GetSpec()
			// Template defaults should be stored:
			Expect(spec.GetInstanceType()).To(Equal("standard-4-16"))
			Expect(spec.GetRunStrategy()).To(Equal("Always"))
			// User-provided fields should be stored:
			Expect(spec.GetImage().GetSourceRef()).To(Equal("quay.io/containerdisks/fedora:latest"))
			Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(20)))
		})

		Describe("Catalog item", func() {
			var catalogItemsDao *dao.GenericDAO[*privatev1.ComputeInstanceCatalogItem]

			BeforeEach(func() {
				var err error
				catalogItemsDao, err = dao.NewGenericDAO[*privatev1.ComputeInstanceCatalogItem]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				// Backing template for the catalog items created below, with full
				// spec_defaults so catalog-item creation succeeds for the right
				// reason (defaults resolved from the template), not because
				// required-field validation was skipped.
				createTemplate("ci-template-id")
			})

			createCICatalogItem := func(id string, published bool, fieldDefs []*privatev1.FieldDefinition) {
				_, err := catalogItemsDao.Create().SetObject(
					privatev1.ComputeInstanceCatalogItem_builder{
						Id: id,
						Metadata: privatev1.Metadata_builder{
							Name:   id + "-name",
							Tenant: "shared",
						}.Build(),
						Title:            "Test CI Catalog Item",
						Published:        published,
						Template:         "ci-template-id",
						FieldDefinitions: fieldDefs,
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			It("Creates compute instance with catalog item", func() {
				createCICatalogItem("ci-cat-happy", true, nil)

				response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: "ci-cat-happy",
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				object := response.GetObject()
				Expect(object).ToNot(BeNil())
				Expect(object.GetId()).ToNot(BeEmpty())
				Expect(object.GetSpec().GetTemplate()).To(Equal("ci-template-id"))
				Expect(object.GetSpec().GetCatalogItem()).To(Equal("ci-cat-happy"))
			})

			It("Creates compute instance with catalog item specified by name", func() {
				createCICatalogItem("ci-cat-byname", true, nil)

				response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: "ci-cat-byname-name",
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				object := response.GetObject()
				Expect(object).ToNot(BeNil())
				Expect(object.GetSpec().GetTemplate()).To(Equal("ci-template-id"))
			})

			It("Fails when catalog item not found", func() {
				_, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: "nonexistent",
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
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
				createCICatalogItem("ci-cat-unpub", false, nil)

				_, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: "ci-cat-unpub",
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.NotFound))
				Expect(status.Message()).To(Equal(
					"catalog item 'ci-cat-unpub' is not published",
				))
			})

			It("Fails when catalog item does not reference a template", func() {
				_, err := catalogItemsDao.Create().SetObject(
					privatev1.ComputeInstanceCatalogItem_builder{
						Id: "ci-cat-no-template",
						Metadata: privatev1.Metadata_builder{
							Name:   "ci-cat-no-template-name",
							Tenant: "shared",
						}.Build(),
						Title:     "Catalog Item without template",
						Published: true,
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: "ci-cat-no-template",
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal(
					"catalog item 'ci-cat-no-template' does not reference a template",
				))
			})

			It("Fails when both catalog_item and template are set", func() {
				_, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: "any-catalog-item",
							Template:    "some-template",
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
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
				createCICatalogItem("ci-cat-nonedit", true, []*privatev1.FieldDefinition{
					privatev1.FieldDefinition_builder{
						Path:     "ssh_public_key",
						Editable: false,
						Default:  structpb.NewStringValue("forced-key"),
					}.Build(),
					privatev1.FieldDefinition_builder{
						Path:     "network_attachments",
						Editable: true,
					}.Build(),
				})

				_, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem:  "ci-cat-nonedit",
							SshPublicKey: new("user-key"),
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
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
					createCICatalogItem(catID, true, []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:             "ssh_public_key",
							Editable:         true,
							ValidationSchema: `{"type":"string","minLength":10}`,
						}.Build(),
						privatev1.FieldDefinition_builder{
							Path:     "network_attachments",
							Editable: true,
						}.Build(),
					})

					response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
						Object: privatev1.ComputeInstance_builder{
							Spec: privatev1.ComputeInstanceSpec_builder{
								CatalogItem:  catID,
								SshPublicKey: new(value),
								NetworkAttachments: []*privatev1.NetworkAttachment{
									privatev1.NetworkAttachment_builder{
										Subnet: "test-subnet",
									}.Build(),
								},
							}.Build(),
						}.Build(),
					}.Build())
					if expectError {
						Expect(err).To(HaveOccurred())
						status, ok := grpcstatus.FromError(err)
						Expect(ok).To(BeTrue())
						Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
						Expect(status.Message()).To(ContainSubstring("validation failed for field 'ssh_public_key'"))
					} else {
						Expect(err).ToNot(HaveOccurred())
						Expect(response.GetObject().GetSpec().GetSshPublicKey()).To(Equal(value))
					}
				},
				Entry("rejects value below minLength", "ci-cat-schema-reject", "short-val", true),
				Entry("accepts value meeting minLength", "ci-cat-schema-accept", "long-enough-key", false),
			)

			It("Applies default for editable field when not provided", func() {
				createCICatalogItem("ci-cat-dflt", true, []*privatev1.FieldDefinition{
					privatev1.FieldDefinition_builder{
						Path:     "ssh_public_key",
						Editable: true,
						Default:  structpb.NewStringValue("default-key"),
					}.Build(),
					privatev1.FieldDefinition_builder{
						Path:     "network_attachments",
						Editable: true,
					}.Build(),
				})

				response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: "ci-cat-dflt",
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()
				Expect(object.GetSpec().GetSshPublicKey()).To(Equal("default-key"))
			})

			It("Rejects changing catalog_item on update", func() {
				createCICatalogItem("ci-cat-immut", true, nil)

				createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: "ci-cat-immut",
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := createResponse.GetObject()

				_, err = server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: object.GetId(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: "different-catalog-item",
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
					"cannot change spec.catalog_item from 'ci-cat-immut' to 'different-catalog-item': catalog item is immutable",
				))
			})

			It("Rejects creation via catalog item when template has no spec defaults", func() {
				templatesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceTemplate]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				_, err = templatesDao.Create().SetObject(
					privatev1.ComputeInstanceTemplate_builder{
						Id:    "ci-template-no-defaults",
						Title: "Template without spec defaults",
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = catalogItemsDao.Create().SetObject(
					privatev1.ComputeInstanceCatalogItem_builder{
						Id: "ci-cat-no-defaults",
						Metadata: privatev1.Metadata_builder{
							Name:   "ci-cat-no-defaults-name",
							Tenant: "shared",
						}.Build(),
						Title:     "Catalog Item without defaults",
						Published: true,
						Template:  "ci-template-no-defaults",
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: "ci-cat-no-defaults",
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("instance_type"))
				Expect(status.Message()).To(ContainSubstring("image"))
				Expect(status.Message()).To(ContainSubstring("boot_disk"))
				Expect(status.Message()).To(ContainSubstring("run_strategy"))
			})

			It("Validates instance_type from template spec_defaults via catalog item", func() {
				// Create a template with instance_type in spec_defaults:
				templatesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceTemplate]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				_, err = templatesDao.Create().SetObject(
					privatev1.ComputeInstanceTemplate_builder{
						Id:    "ci-template-with-it",
						Title: "Template with instance type",
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							InstanceType: new("nonexistent-instance-type"),
							Image: privatev1.ComputeInstanceImage_builder{
								SourceType: "registry",
								SourceRef:  "quay.io/containerdisks/fedora:latest",
							}.Build(),
							BootDisk: privatev1.ComputeInstanceDisk_builder{
								SizeGib: 10,
							}.Build(),
							RunStrategy: new("Always"),
						}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a catalog item pointing to that template:
				_, err = catalogItemsDao.Create().SetObject(
					privatev1.ComputeInstanceCatalogItem_builder{
						Id: "ci-cat-with-it",
						Metadata: privatev1.Metadata_builder{
							Name:   "ci-cat-with-it-name",
							Tenant: "shared",
						}.Build(),
						Title:     "Catalog Item with IT",
						Published: true,
						Template:  "ci-template-with-it",
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create via catalog item — should fail because instance type doesn't exist:
				_, err = server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: "ci-cat-with-it",
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.NotFound))
				Expect(status.Message()).To(ContainSubstring("nonexistent-instance-type"))
			})

			It("Creates compute instance when template spec_defaults has valid instance_type", func() {
				// Create a template with instance_type in spec_defaults
				// (the "standard-4-16" instance type is already created by BeforeEach):
				templatesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceTemplate]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				_, err = templatesDao.Create().SetObject(
					privatev1.ComputeInstanceTemplate_builder{
						Id:    "ci-template-valid-it",
						Title: "Template with valid instance type",
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							InstanceType: new("standard-4-16"),
							Image: privatev1.ComputeInstanceImage_builder{
								SourceType: "registry",
								SourceRef:  "quay.io/containerdisks/fedora:latest",
							}.Build(),
							BootDisk: privatev1.ComputeInstanceDisk_builder{
								SizeGib: 10,
							}.Build(),
							RunStrategy: new("Always"),
						}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a catalog item pointing to that template:
				_, err = catalogItemsDao.Create().SetObject(
					privatev1.ComputeInstanceCatalogItem_builder{
						Id: "ci-cat-valid-it",
						Metadata: privatev1.Metadata_builder{
							Name:   "ci-cat-valid-it-name",
							Tenant: "shared",
						}.Build(),
						Title:     "Catalog Item with valid IT",
						Published: true,
						Template:  "ci-template-valid-it",
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create via catalog item — should succeed:
				response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: "ci-cat-valid-it",
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				object := response.GetObject()
				Expect(object).ToNot(BeNil())
				Expect(object.GetSpec().GetTemplate()).To(Equal("ci-template-valid-it"))
				Expect(object.GetSpec().GetCatalogItem()).To(Equal("ci-cat-valid-it"))
			})
		})
	})

	Describe("Network validation", func() {
		var (
			server         *PrivateComputeInstancesServer
			template       *privatev1.ComputeInstanceTemplate
			networkClass   *privatev1.NetworkClass
			virtualNetwork *privatev1.VirtualNetwork
		)

		BeforeEach(func() {
			var err error

			// Create network resources
			networkClass = createTestNetworkClass(ctx)
			virtualNetwork = createTestVirtualNetwork(ctx, networkClass.GetId())

			// Create test template
			templatesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceTemplate]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			cpuDefault, err := anypb.New(wrapperspb.Int32(1))
			Expect(err).ToNot(HaveOccurred())
			memoryDefault, err := anypb.New(wrapperspb.Int32(2))
			Expect(err).ToNot(HaveOccurred())

			// Create an InstanceType for network validation tests:
			instanceTypesDao, err := dao.NewGenericDAO[*privatev1.InstanceType]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = instanceTypesDao.Create().SetObject(
				privatev1.InstanceType_builder{
					Id: "standard-4-16",
					Metadata: privatev1.Metadata_builder{
						Name:   "standard-4-16",
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:     4,
						MemoryGib: 16,
						State:     privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			template = privatev1.ComputeInstanceTemplate_builder{
				Id:          "test-template",
				Title:       "Test Template",
				Description: "Test template for network validation",
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				Parameters: []*privatev1.ComputeInstanceTemplateParameterDefinition{
					{
						Name:        "cpu_count",
						Title:       "CPU Count",
						Description: "Number of CPU cores",
						Required:    false,
						Type:        "type.googleapis.com/google.protobuf.Int32Value",
						Default:     cpuDefault,
					},
					{
						Name:        "memory_gb",
						Title:       "Memory (GB)",
						Description: "Amount of memory in GB",
						Required:    false,
						Type:        "type.googleapis.com/google.protobuf.Int32Value",
						Default:     memoryDefault,
					},
				},
				SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
					InstanceType: new("standard-4-16"),
					Image: privatev1.ComputeInstanceImage_builder{
						SourceType: "registry",
						SourceRef:  "quay.io/containerdisks/fedora:latest",
					}.Build(),
					BootDisk: privatev1.ComputeInstanceDisk_builder{
						SizeGib: 10,
					}.Build(),
					RunStrategy: new("Always"),
				}.Build(),
			}.Build()

			_, err = templatesDao.Create().SetObject(template).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create the server:
			server, err = NewPrivateComputeInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		Context("network_attachments", func() {
			It("Should succeed with two READY subnets as separate attachments", func() {
				s1 := createTestSubnet(ctx, virtualNetwork.GetId(), privatev1.SubnetState_SUBNET_STATE_READY)
				s2 := createTestSubnet(ctx, virtualNetwork.GetId(), privatev1.SubnetState_SUBNET_STATE_READY)

				vm := privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: template.GetId(),
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{Subnet: s1.GetId()}.Build(),
							privatev1.NetworkAttachment_builder{Subnet: s2.GetId()}.Build(),
						},
					}.Build(),
				}.Build()

				request := &privatev1.ComputeInstancesCreateRequest{}
				request.SetObject(vm)

				response, err := server.Create(ctx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				Expect(response.GetObject().GetSpec().GetNetworkAttachments()).To(HaveLen(2))
				Expect(response.GetObject().GetSpec().GetNetworkAttachments()[0].GetSubnet()).To(Equal(s1.GetId()))
				Expect(response.GetObject().GetSpec().GetNetworkAttachments()[1].GetSubnet()).To(Equal(s2.GetId()))
			})
		})

		Context("Required network fields", func() {
			It("Should reject when network_attachments is missing", func() {
				vm := privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: template.GetId(),
					}.Build(),
				}.Build()

				request := &privatev1.ComputeInstancesCreateRequest{}
				request.SetObject(vm)

				response, err := server.Create(ctx, request)
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("network_attachments"))
				Expect(status.Message()).To(ContainSubstring("at least one network attachment is required"))
			})

			It("Should allow updating VM with empty network_attachments (pod network)", func() {
				// Insert a VM with empty network_attachments directly into database
				// (simulating a migrated VM that uses pod network)
				ciDao, err := dao.NewGenericDAO[*privatev1.ComputeInstance]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				podNetworkVM := privatev1.ComputeInstance_builder{
					Id: "pod-network-vm",
					Metadata: privatev1.Metadata_builder{
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: template.GetId(),
						// No network_attachments - pod network
					}.Build(),
					Status: privatev1.ComputeInstanceStatus_builder{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
					}.Build(),
				}.Build()

				_, err = ciDao.Create().
					SetObject(podNetworkVM).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Try to update the VM (e.g., change run strategy)
				updateResponse, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: "pod-network-vm",
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template:    template.GetId(),
							RunStrategy: new("Always"),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.run_strategy"},
					},
				}.Build())

				// Update should succeed for backward compatibility
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse).ToNot(BeNil())
				Expect(updateResponse.GetObject().GetSpec().GetRunStrategy()).To(Equal("Always"))
			})
		})

		Context("NetworkAttachments validation", func() {
			It("Should reject when subnet not found in network_attachments", func() {
				vm := privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: template.GetId(),
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{Subnet: "nonexistent-subnet"}.Build(),
						},
					}.Build(),
				}.Build()

				request := &privatev1.ComputeInstancesCreateRequest{}
				request.SetObject(vm)

				response, err := server.Create(ctx, request)
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("subnet"))
				Expect(status.Message()).To(ContainSubstring("does not exist"))
			})

			It("Should reject when subnet not READY in network_attachments", func() {
				subnet := createTestSubnet(ctx, virtualNetwork.GetId(), privatev1.SubnetState_SUBNET_STATE_PENDING)

				vm := privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: template.GetId(),
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{Subnet: subnet.GetId()}.Build(),
						},
					}.Build(),
				}.Build()

				request := &privatev1.ComputeInstancesCreateRequest{}
				request.SetObject(vm)

				response, err := server.Create(ctx, request)
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
				Expect(status.Message()).To(ContainSubstring("subnet"))
				Expect(status.Message()).To(ContainSubstring("is not in READY state"))
			})

			It("Should reject when security group not found in network_attachments", func() {
				subnet := createTestSubnet(ctx, virtualNetwork.GetId(), privatev1.SubnetState_SUBNET_STATE_READY)

				vm := privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: template.GetId(),
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{
								Subnet:         subnet.GetId(),
								SecurityGroups: []string{"nonexistent-sg"},
							}.Build(),
						},
					}.Build(),
				}.Build()

				request := &privatev1.ComputeInstancesCreateRequest{}
				request.SetObject(vm)

				response, err := server.Create(ctx, request)
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("security group"))
				Expect(status.Message()).To(ContainSubstring("does not exist"))
			})

			It("Should reject when security group belongs to wrong VirtualNetwork in network_attachments", func() {
				// Create another virtual network with the same network class
				otherVNet := createTestVirtualNetwork(ctx, networkClass.GetId())
				subnet := createTestSubnet(ctx, virtualNetwork.GetId(), privatev1.SubnetState_SUBNET_STATE_READY)
				wrongSG := createTestSecurityGroup(ctx, otherVNet.GetId(), privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY)

				vm := privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: template.GetId(),
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{
								Subnet:         subnet.GetId(),
								SecurityGroups: []string{wrongSG.GetId()},
							}.Build(),
						},
					}.Build(),
				}.Build()

				request := &privatev1.ComputeInstancesCreateRequest{}
				request.SetObject(vm)

				response, err := server.Create(ctx, request)
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("security group"))
				Expect(status.Message()).To(ContainSubstring("belongs to VirtualNetwork"))
			})

			It("Should allow empty security_groups in network_attachments", func() {
				subnet := createTestSubnet(ctx, virtualNetwork.GetId(), privatev1.SubnetState_SUBNET_STATE_READY)

				vm := privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: template.GetId(),
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{
								Subnet:         subnet.GetId(),
								SecurityGroups: []string{},
							}.Build(),
						},
					}.Build(),
				}.Build()

				request := &privatev1.ComputeInstancesCreateRequest{}
				request.SetObject(vm)

				response, err := server.Create(ctx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				Expect(response.GetObject().GetSpec().GetNetworkAttachments()).To(HaveLen(1))
				Expect(response.GetObject().GetSpec().GetNetworkAttachments()[0].GetSecurityGroups()).To(BeEmpty())
			})

			It("Should reject when security group not in READY state in network_attachments", func() {
				subnet := createTestSubnet(ctx, virtualNetwork.GetId(), privatev1.SubnetState_SUBNET_STATE_READY)
				sg := createTestSecurityGroup(ctx, virtualNetwork.GetId(), privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING)

				vm := privatev1.ComputeInstance_builder{
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: template.GetId(),
						NetworkAttachments: []*privatev1.NetworkAttachment{
							privatev1.NetworkAttachment_builder{
								Subnet:         subnet.GetId(),
								SecurityGroups: []string{sg.GetId()},
							}.Build(),
						},
					}.Build(),
				}.Build()

				request := &privatev1.ComputeInstancesCreateRequest{}
				request.SetObject(vm)

				response, err := server.Create(ctx, request)
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
				Expect(status.Message()).To(ContainSubstring("security group"))
				Expect(status.Message()).To(ContainSubstring("is not in READY state"))
			})
		})

		Context("Update validation with deletion", func() {
			It("Should skip state validation when isBeingDeleted=true", func() {
				// Create with a READY subnet
				subnet := createTestSubnet(ctx, virtualNetwork.GetId(), privatev1.SubnetState_SUBNET_STATE_READY)
				sg := createTestSecurityGroup(ctx, virtualNetwork.GetId(), privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY)

				createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: template.GetId(),
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet:         subnet.GetId(),
									SecurityGroups: []string{sg.GetId()},
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				created := createResponse.GetObject()

				// Update subnet to non-READY state (simulate resource being deleted/modified)
				subnet.GetStatus().SetState(privatev1.SubnetState_SUBNET_STATE_PENDING)
				subnetDAO, err := dao.NewGenericDAO[*privatev1.Subnet]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = subnetDAO.Update().SetObject(subnet).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Mark the ComputeInstance as being deleted
				deletionTime := timestamppb.Now()
				created.GetMetadata().SetDeletionTimestamp(deletionTime)

				// Try to update security groups while subnet is PENDING
				// Should succeed because isBeingDeleted=true skips state validation
				created.GetSpec().SetNetworkAttachments([]*privatev1.NetworkAttachment{
					privatev1.NetworkAttachment_builder{
						Subnet:         subnet.GetId(),
						SecurityGroups: []string{}, // Change security groups (allowed)
					}.Build(),
				})
				updateRequest := &privatev1.ComputeInstancesUpdateRequest{}
				updateRequest.SetObject(created)
				updateRequest.SetUpdateMask(&fieldmaskpb.FieldMask{Paths: []string{"spec.network_attachments"}})

				response, err := server.Update(ctx, updateRequest)
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
			})
		})

		Context("NetworkAttachments immutability", func() {
			var (
				subnet1 *privatev1.Subnet
				subnet2 *privatev1.Subnet
				sg1     *privatev1.SecurityGroup
				sg2     *privatev1.SecurityGroup
			)

			BeforeEach(func() {
				subnet1 = createTestSubnet(ctx, virtualNetwork.GetId(), privatev1.SubnetState_SUBNET_STATE_READY)
				subnet2 = createTestSubnet(ctx, virtualNetwork.GetId(), privatev1.SubnetState_SUBNET_STATE_READY)
				sg1 = createTestSecurityGroup(ctx, virtualNetwork.GetId(), privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY)
				sg2 = createTestSecurityGroup(ctx, virtualNetwork.GetId(), privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY)
			})

			It("Rejects changing subnet in network_attachments", func() {
				// Create a ComputeInstance with networkAttachments
				createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: template.GetId(),
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet:         subnet1.GetId(),
									SecurityGroups: []string{sg1.GetId()},
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(createResponse).ToNot(BeNil())

				id := createResponse.GetObject().GetId()

				// Try to change subnet reference
				updateResponse, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: id,
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: template.GetId(),
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet:         subnet2.GetId(),
									SecurityGroups: []string{sg1.GetId()},
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.network_attachments"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(updateResponse).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("subnet is immutable"))
			})

			It("Allows changing security groups in network_attachments", func() {
				// Create a ComputeInstance with networkAttachments
				createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: template.GetId(),
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet:         subnet1.GetId(),
									SecurityGroups: []string{sg1.GetId()},
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(createResponse).ToNot(BeNil())

				id := createResponse.GetObject().GetId()

				// Change security groups (should succeed)
				updateResponse, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: id,
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: template.GetId(),
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet:         subnet1.GetId(),                    // Same subnet
									SecurityGroups: []string{sg1.GetId(), sg2.GetId()}, // Different SGs
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.network_attachments"},
					},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse).ToNot(BeNil())
				Expect(updateResponse.GetObject().GetSpec().GetNetworkAttachments()[0].GetSecurityGroups()).To(
					Equal([]string{sg1.GetId(), sg2.GetId()}),
				)
			})

			It("Rejects adding network attachments", func() {
				// Create with 1 attachment
				createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: template.GetId(),
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: subnet1.GetId(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(createResponse).ToNot(BeNil())

				id := createResponse.GetObject().GetId()

				// Try to add second attachment
				updateResponse, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: id,
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: template.GetId(),
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: subnet1.GetId(),
								}.Build(),
								privatev1.NetworkAttachment_builder{
									Subnet: subnet2.GetId(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.network_attachments"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(updateResponse).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("cannot change number"))
			})

			It("Rejects removing network attachments", func() {
				// Create with 2 attachments
				createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: template.GetId(),
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: subnet1.GetId(),
								}.Build(),
								privatev1.NetworkAttachment_builder{
									Subnet: subnet2.GetId(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(createResponse).ToNot(BeNil())

				id := createResponse.GetObject().GetId()

				// Try to remove one attachment
				updateResponse, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: id,
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: template.GetId(),
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: subnet1.GetId(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.network_attachments"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(updateResponse).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("cannot change number"))
			})
		})

		Context("instance_type validation", func() {
			// Helper to create an InstanceType with a specific state via DAO.
			createInstanceTypeWithState := func(name string, state privatev1.InstanceTypeState) {
				instanceTypesDao, err := dao.NewGenericDAO[*privatev1.InstanceType]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				_, err = instanceTypesDao.Create().SetObject(
					privatev1.InstanceType_builder{
						Id: name,
						Metadata: privatev1.Metadata_builder{
							Name:   name,
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:     4,
							MemoryGib: 16,
							State:     state,
						}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			// Helper to build a full ComputeInstance create request with all required fields.
			createRequestWithInstanceType := func(instanceTypeName string) *privatev1.ComputeInstancesCreateRequest {
				// Use a bare template without spec defaults so the instance_type
				// on the spec is used directly.
				templatesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceTemplate]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				templateID := fmt.Sprintf("bare-template-%s", instanceTypeName)
				_, err = templatesDao.Create().SetObject(
					privatev1.ComputeInstanceTemplate_builder{
						Id:          templateID,
						Title:       "Bare Template",
						Description: "Template without defaults",
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				return privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template:     templateID,
							InstanceType: new(instanceTypeName),
							Image: privatev1.ComputeInstanceImage_builder{
								SourceType: "registry",
								SourceRef:  "quay.io/containerdisks/fedora:latest",
							}.Build(),
							BootDisk: privatev1.ComputeInstanceDisk_builder{
								SizeGib: 20,
							}.Build(),
							RunStrategy: new("Always"),
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build()
			}

			It("Rejects creation when instance_type references a non-existent instance type", func() {
				request := createRequestWithInstanceType("nonexistent-type")
				response, err := server.Create(ctx, request)
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.NotFound))
				Expect(status.Message()).To(ContainSubstring("nonexistent-type"))
			})

			It("Rejects creation when instance_type references an OBSOLETE instance type", func() {
				createInstanceTypeWithState("obsolete-type",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE)

				request := createRequestWithInstanceType("obsolete-type")
				response, err := server.Create(ctx, request)
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
				Expect(status.Message()).To(ContainSubstring("obsolete"))
			})

			It("Returns warning when instance_type references a DEPRECATED instance type", func() {
				createInstanceTypeWithState("deprecated-type",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED)

				request := createRequestWithInstanceType("deprecated-type")
				response, err := server.Create(ctx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				Expect(response.GetWarnings()).To(HaveLen(1))
				Expect(response.GetWarnings()[0]).To(ContainSubstring("deprecated"))
			})

			It("Succeeds when instance_type references an ACTIVE instance type", func() {
				createInstanceTypeWithState("active-type",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE)

				request := createRequestWithInstanceType("active-type")
				response, err := server.Create(ctx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				Expect(response.GetWarnings()).To(BeEmpty())
			})
		})
	})
})
