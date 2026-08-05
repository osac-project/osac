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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

var _ = Describe("Compute instances server", func() {
	Describe("Builder", func() {
		It("Creates server with logger and tenancy logic", func() {
			// Create the public server:
			server, err := NewComputeInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Doesn't create server without logger", func() {
			// Try to create the public server without logger:
			server, err := NewComputeInstancesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewComputeInstancesServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewComputeInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var (
			server *ComputeInstancesServer
		)

		BeforeEach(func() {
			var err error

			// Create the public server:
			server, err = NewComputeInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create a test virtual network and subnet for all tests to use:
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

			response, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template:           "general.small",
						TemplateParameters: templateParams,
						NetworkAttachments: []*publicv1.NetworkAttachment{
							publicv1.NetworkAttachment_builder{
								Subnet: "test-subnet",
							}.Build(),
						},
					}.Build(),
					Status: publicv1.ComputeInstanceStatus_builder{
						State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetSpec().GetTemplate()).To(Equal("general.small"))
			Expect(object.GetStatus().GetState()).To(Equal(publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING))
		})

		It("List objects", func() {
			// Create templates and objects:
			const count = 10
			for i := range count {
				templateID := fmt.Sprintf("template-%d", i)
				createTemplate(templateID)

				_, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
					Object: publicv1.ComputeInstance_builder{
						Spec: publicv1.ComputeInstanceSpec_builder{
							Template: templateID,
							NetworkAttachments: []*publicv1.NetworkAttachment{
								publicv1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
						}.Build(),
						Status: publicv1.ComputeInstanceStatus_builder{
							State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, publicv1.ComputeInstancesListRequest_builder{}.Build())
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

				_, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
					Object: publicv1.ComputeInstance_builder{
						Spec: publicv1.ComputeInstanceSpec_builder{
							Template: templateID,
							NetworkAttachments: []*publicv1.NetworkAttachment{
								publicv1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
						}.Build(),
						Status: publicv1.ComputeInstanceStatus_builder{
							State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects with limit:
			response, err := server.List(ctx, publicv1.ComputeInstancesListRequest_builder{
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

				_, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
					Object: publicv1.ComputeInstance_builder{
						Spec: publicv1.ComputeInstanceSpec_builder{
							Template: templateID,
							NetworkAttachments: []*publicv1.NetworkAttachment{
								publicv1.NetworkAttachment_builder{
									Subnet: "test-subnet",
								}.Build(),
							},
						}.Build(),
						Status: publicv1.ComputeInstanceStatus_builder{
							State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects with offset:
			response, err := server.List(ctx, publicv1.ComputeInstancesListRequest_builder{
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
			createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template: "general.small",
						NetworkAttachments: []*publicv1.NetworkAttachment{
							publicv1.NetworkAttachment_builder{
								Subnet: "test-subnet",
							}.Build(),
						},
					}.Build(),
					Status: publicv1.ComputeInstanceStatus_builder{
						State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
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
			getResponse, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse).ToNot(BeNil())
			object := getResponse.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).To(Equal(id))
			Expect(object.GetSpec().GetTemplate()).To(Equal("general.small"))
			Expect(object.GetStatus().GetState()).To(Equal(publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING))
		})

		It("Updates object", func() {
			// Create templates first
			createTemplate("general.small")

			// Create an object with explicit fields:
			createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template:     "general.small",
						InstanceType: new("standard-4-16"),
						RunStrategy:  new("Always"),
						Image: publicv1.ComputeInstanceImage_builder{
							SourceType: "registry",
							SourceRef:  "quay.io/test:latest",
						}.Build(),
						BootDisk: publicv1.ComputeInstanceDisk_builder{
							SizeGib: 20,
						}.Build(),
						NetworkAttachments: []*publicv1.NetworkAttachment{
							publicv1.NetworkAttachment_builder{
								Subnet: "test-subnet",
							}.Build(),
						},
					}.Build(),
					Status: publicv1.ComputeInstanceStatus_builder{
						State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			// Update only restart_requested_at via field mask, explicit fields must survive:
			restartTime := timestamppb.Now()
			updateResponse, err := server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Id: id,
					Spec: publicv1.ComputeInstanceSpec_builder{
						RestartRequestedAt: restartTime,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.restart_requested_at"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := updateResponse.GetObject()
			Expect(object.GetId()).To(Equal(id))

			// Verify the masked field was updated:
			Expect(object.GetSpec().GetRestartRequestedAt().AsTime()).To(
				BeTemporally("~", restartTime.AsTime()),
			)

			// Verify explicit fields were preserved:
			Expect(object.GetSpec().GetTemplate()).To(Equal("general.small"))
			Expect(object.GetSpec().GetInstanceType()).To(Equal("standard-4-16"))
			Expect(object.GetSpec().GetRunStrategy()).To(Equal("Always"))
			Expect(object.GetSpec().GetImage().GetSourceRef()).To(Equal("quay.io/test:latest"))
			Expect(object.GetSpec().GetBootDisk().GetSizeGib()).To(BeNumerically("==", 20))

			// Verify they survive a round-trip through the database:
			getResponse, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			fetched := getResponse.GetObject()
			Expect(fetched.GetSpec().GetInstanceType()).To(Equal("standard-4-16"))
			Expect(fetched.GetSpec().GetRunStrategy()).To(Equal("Always"))
			Expect(fetched.GetSpec().GetImage().GetSourceRef()).To(Equal("quay.io/test:latest"))
			Expect(fetched.GetSpec().GetBootDisk().GetSizeGib()).To(BeNumerically("==", 20))
			Expect(fetched.GetSpec().GetRestartRequestedAt()).ToNot(BeNil())
		})

		It("Deletes object", func() {
			// Create a template first
			createTemplate("general.small")

			// Create an object:
			createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template: "general.small",
						NetworkAttachments: []*publicv1.NetworkAttachment{
							publicv1.NetworkAttachment_builder{
								Subnet: "test-subnet",
							}.Build(),
						},
					}.Build(),
					Status: publicv1.ComputeInstanceStatus_builder{
						State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
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
			deleteResponse, err := server.Delete(ctx, publicv1.ComputeInstancesDeleteRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(deleteResponse).ToNot(BeNil())

			// Verify the object is deleted:
			getResponse, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(getResponse).To(BeNil())
		})

		It("Handles non-existent object", func() {
			// Try to get a non-existent object:
			getResponse, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: "non-existent-id",
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(getResponse).To(BeNil())
		})

		It("Handles empty object in create request", func() {
			// Try to create with nil object:
			response, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})

		It("Handles empty object in update request", func() {
			// Try to update with nil object:
			response, err := server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})

		It("Handles empty ID in get request", func() {
			// Try to get with empty ID:
			response, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})

		It("Handles empty ID in delete request", func() {
			// Try to delete with empty ID:
			response, err := server.Delete(ctx, publicv1.ComputeInstancesDeleteRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})

		It("User-provided values survive public-to-private mapping, missing fields filled from template", func() {
			createTemplate("mapping-template")

			// Create with some user-provided fields and let template cover the rest for validation:
			response, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template:    "mapping-template",
						RunStrategy: new("Halted"),
						NetworkAttachments: []*publicv1.NetworkAttachment{
							publicv1.NetworkAttachment_builder{
								Subnet: "test-subnet",
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())

			spec := response.GetObject().GetSpec()
			// User-provided values preserved through mapping:
			Expect(spec.GetRunStrategy()).To(Equal("Halted"))
			// Template defaults should be stored:
			Expect(spec.GetInstanceType()).To(Equal("standard-4-16"))
			Expect(spec.GetImage().GetSourceType()).To(Equal("registry"))
			Expect(spec.GetImage().GetSourceRef()).To(Equal("quay.io/containerdisks/fedora:latest"))
			Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(10)))
		})
	})
})
