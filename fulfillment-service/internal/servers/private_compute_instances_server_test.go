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
	"strings"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
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
				Name:   "test-vnet",
				Tenant: testTenant,
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
				Name:   "test-subnet-default",
				Tenant: testTenant,
			}.Build(),
			Spec: privatev1.SubnetSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "test-vnet"}.Build(),
				Ipv4Cidr:       new("10.0.0.0/24"),
			}.Build(),
			Status: privatev1.SubnetStatus_builder{
				State: privatev1.SubnetState_SUBNET_STATE_READY,
			}.Build(),
		}.Build()

		_, err = subnetsDao.Create().SetObject(subnet).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Create a default DiskImage for tests that reference "test-disk-image" in template spec_defaults:
		diskImagesDao, err := dao.NewGenericDAO[*privatev1.DiskImage]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = diskImagesDao.Create().SetObject(
			privatev1.DiskImage_builder{
				Id: "test-disk-image",
				Metadata: privatev1.Metadata_builder{
					Name:   "test-disk-image",
					Tenant: testTenant,
				}.Build(),
				Spec: privatev1.DiskImageSpec_builder{
					Lifecycle: privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE,
				}.Build(),
			}.Build(),
		).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	// Helper function to create a NetworkClass for test setup
	createTestNetworkClass := func(ctx context.Context) *privatev1.NetworkClass {
		ncDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		fabricManager := "test-strategy"
		nc := privatev1.NetworkClass_builder{
			FabricManager: &fabricManager,
			Metadata: privatev1.Metadata_builder{
				Name:   "test-network-class",
				Tenant: testTenant,
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
	var vnNameSeq int
	createTestVirtualNetwork := func(ctx context.Context, networkClassID string) *privatev1.VirtualNetwork {
		vnNameSeq++
		vnDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		vn := privatev1.VirtualNetwork_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   fmt.Sprintf("test-vn-%d", vnNameSeq),
				Tenant: testTenant,
			}.Build(),
			Spec: privatev1.VirtualNetworkSpec_builder{
				Ipv4Cidr:     new("10.0.0.0/16"),
				NetworkClass: privatev1.NetworkClassReference_builder{Id: networkClassID}.Build(),
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
	var subnetNameSeq int
	createTestSubnet := func(ctx context.Context, vnID string, state privatev1.SubnetState) *privatev1.Subnet {
		subnetNameSeq++
		subnetDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		subnet := privatev1.Subnet_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   fmt.Sprintf("test-sn-%d", subnetNameSeq),
				Tenant: testTenant,
			}.Build(),
			Spec: privatev1.SubnetSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vnID}.Build(),
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
	var sgNameSeq int
	createTestSecurityGroup := func(ctx context.Context, vnID string, state privatev1.SecurityGroupState) *privatev1.SecurityGroup {
		sgNameSeq++
		sgDao, err := dao.NewGenericDAO[*privatev1.SecurityGroup]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		sg := privatev1.SecurityGroup_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   fmt.Sprintf("test-sg-%d", sgNameSeq),
				Tenant: testTenant,
			}.Build(),
			Spec: privatev1.SecurityGroupSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vnID}.Build(),
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
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Vcpus:     4,
						MemoryGib: 16,
						State:     privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			storageTiersDao, err := dao.NewGenericDAO[*privatev1.StorageTier]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = storageTiersDao.Create().SetObject(
				privatev1.StorageTier_builder{
					Id: "standard",
					Metadata: privatev1.Metadata_builder{
						Name:   "standard",
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.StorageTierSpec_builder{
						Description: "Standard storage tier",
					}.Build(),
					Status: privatev1.StorageTierStatus_builder{
						State: privatev1.StorageTierState_STORAGE_TIER_STATE_ACTIVE,
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
					Name:   strings.ReplaceAll(templateID, ".", "-"),
					Tenant: testTenant,
				}.Build(),
				Parameters: []*privatev1.ComputeInstanceTemplateParameterDefinition{
					{
						Name:        "cpu_count",
						Title:       "CPU Count",
						Description: "Number of vCPUs",
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
					InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
					DiskImage:    &privatev1.DiskImageReference{Id: "test-disk-image"},
					BootDisk: privatev1.ComputeInstanceDisk_builder{
						SizeGib:     proto.Int32(10),
						StorageTier: privatev1.StorageTierReference_builder{Name: "standard"}.Build(),
					}.Build(),
					RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
				}.Build(),
			}.Build()

			_, err = templatesDao.Create().SetObject(template).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
		}

		createUserDataSecret := func() *privatev1.Secret {
			result, err := server.secretsDao.Create().SetObject(privatev1.Secret_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("userdata-%s", uuid.NewString()[:8]),
					Tenant: testTenant,
				}.Build(),
				Type: privatev1.SecretType_SECRET_TYPE_USER_DATA,
				Data: map[string][]byte{userDataSecretDataKey: []byte("#cloud-config")},
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			return result.GetObject()
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template:           privatev1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						TemplateParameters: templateParams,
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
			Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("general.small"))
			Expect(object.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING))
		})

		It("Creates an object from a direct template with a canonical user data Secret", func() {
			createTemplate("secret-template")
			secret := createUserDataSecret()

			response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.NewString()[:8])}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template:       privatev1.ComputeInstanceTemplateReference_builder{Id: "secret-template"}.Build(),
						UserDataSecret: privatev1.SecretLocalReference_builder{Name: secret.GetMetadata().GetName()}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetUserDataSecret().GetId()).To(Equal(secret.GetId()))
			Expect(response.GetObject().GetSpec().GetUserDataSecret().GetName()).To(Equal(secret.GetMetadata().GetName()))
		})

		It("Creates object with additional disks", func() {
			createTemplate("general.small")

			response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						AdditionalDisks: []*privatev1.ComputeInstanceDisk{
							privatev1.ComputeInstanceDisk_builder{
								SizeGib:     proto.Int32(100),
								StorageTier: privatev1.StorageTierReference_builder{Name: "standard"}.Build(),
							}.Build(),
						},
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetSpec().GetAdditionalDisks()).To(HaveLen(1))
			Expect(object.GetSpec().GetAdditionalDisks()[0].GetSizeGib()).To(Equal(int32(100)))
			Expect(object.GetSpec().GetAdditionalDisks()[0].GetStorageTier().GetName()).To(Equal("standard"))
		})

		It("List objects", func() {
			// Create templates and objects:
			const count = 10
			for i := range count {
				templateID := fmt.Sprintf("template-%d", i)
				createTemplate(templateID)

				_, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: templateID}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: templateID}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: templateID}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
			Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("general.small"))
			Expect(object.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING))
		})

		It("Updates object", func() {
			// Create a template first
			createTemplate("general.small")

			// Create an object:
			createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
			Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("general.small"))
			Expect(object.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING))
		})

		It("Deletes object", func() {
			// Create a template first
			createTemplate("general.small")

			// Create an object:
			createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "non-existent-template"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "existing-template"}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "different-template"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template:           privatev1.ComputeInstanceTemplateReference_builder{Id: "params-template"}.Build(),
						TemplateParameters: map[string]*anypb.Any{"cpu_count": cpuParam},
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
						Template:           privatev1.ComputeInstanceTemplateReference_builder{Id: "params-template"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "same-template"}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "same-template"}.Build(),
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
			Expect(updateResponse.GetObject().GetSpec().GetTemplate().GetId()).To(Equal("same-template"))
			Expect(updateResponse.GetObject().GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING))
		})

		It("Rejects changing disk_image on update", func() {
			createTemplate("di-immut-template")

			createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "di-immut-template"}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			id := createResponse.GetObject().GetId()

			updateResponse, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id: id,
					Spec: privatev1.ComputeInstanceSpec_builder{
						DiskImage: &privatev1.DiskImageReference{Id: "different-disk-image"},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.disk_image"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(updateResponse).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("disk image is immutable"))
		})

		It("Validates template ID is not empty", func() {
			// Try to create with empty template ID:
			response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: ""}.Build(),
					}.Build(),
					Status: privatev1.ComputeInstanceStatus_builder{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})

		It("Rejects materialized catalog networks from a different project", func() {
			createTemplate("catalog-network-template")
			projectsDAO, err := dao.NewGenericDAO[*privatev1.Project]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = projectsDAO.Create().SetObject(privatev1.Project_builder{Metadata: privatev1.Metadata_builder{Name: "destination", Tenant: testTenant}.Build()}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			catalogs, err := NewPrivateComputeInstanceCatalogItemsServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			item, err := catalogs.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{Object: privatev1.ComputeInstanceCatalogItem_builder{
				Metadata: privatev1.Metadata_builder{Name: "network-offering"}.Build(), Title: "Network offering", Published: true,
				Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "catalog-network-template"}.Build(),
				Fields: privatev1.ComputeInstanceCatalogItemFields_builder{NetworkAttachments: privatev1.ComputeNetworkAttachmentListFieldPolicy_builder{
					Locked: privatev1.ComputeNetworkAttachmentList_builder{Items: []*privatev1.ComputeNetworkAttachment{privatev1.ComputeNetworkAttachment_builder{Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build()}.Build()}}.Build(),
				}.Build()}.Build(),
			}.Build()}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{Object: privatev1.ComputeInstance_builder{
				Metadata: privatev1.Metadata_builder{Name: "different-project-vm", Project: "destination"}.Build(),
				Spec:     privatev1.ComputeInstanceSpec_builder{CatalogItem: privatev1.ComputeInstanceCatalogItemReference_builder{Id: item.GetObject().GetId()}.Build()}.Build(),
			}.Build()}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("subnet"))
		})

		DescribeTable("validates final storage tiers for direct-template creation", func(state string, additional, inherited bool, expected grpccodes.Code) {
			createTemplate("storage-validation-template")
			ref := privatev1.StorageTierReference_builder{Name: "missing-tier"}.Build()
			if state != "missing" {
				tier, err := server.storageTiersDao.Create().SetObject(privatev1.StorageTier_builder{
					Metadata: privatev1.Metadata_builder{Name: "validated-tier", Tenant: auth.SharedTenant, Finalizers: []string{"keep"}}.Build(),
					Status:   privatev1.StorageTierStatus_builder{State: privatev1.StorageTierState_STORAGE_TIER_STATE_ACTIVE}.Build(),
				}.Build()).Do(ctx)
				Expect(err).NotTo(HaveOccurred())
				switch state {
				case "deleted":
					_, err = server.storageTiersDao.Delete().SetId(tier.GetObject().GetId()).Do(ctx)
					Expect(err).NotTo(HaveOccurred())
				case "inactive":
					tier.GetObject().GetStatus().SetState(privatev1.StorageTierState_STORAGE_TIER_STATE_UNSPECIFIED)
					_, err = server.storageTiersDao.Update().SetObject(tier.GetObject()).Do(ctx)
					Expect(err).NotTo(HaveOccurred())
				}
				ref.SetName("validated-tier")
			}
			disk := privatev1.ComputeInstanceDisk_builder{SizeGib: proto.Int32(20), StorageTier: ref}.Build()
			spec := privatev1.ComputeInstanceSpec_builder{
				Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "storage-validation-template"}.Build(),
				NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
					privatev1.ComputeNetworkAttachment_builder{Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build()}.Build(),
				},
			}.Build()
			if inherited {
				template, err := server.templatesDao.Get().SetId("storage-validation-template").Do(ctx)
				Expect(err).NotTo(HaveOccurred())
				template.GetObject().GetSpecDefaults().SetBootDisk(disk)
				_, err = server.templatesDao.Update().SetObject(template.GetObject()).Do(ctx)
				Expect(err).NotTo(HaveOccurred())
			} else if additional {
				spec.SetAdditionalDisks([]*privatev1.ComputeInstanceDisk{disk})
			} else {
				spec.SetBootDisk(disk)
			}
			response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{Metadata: privatev1.Metadata_builder{Name: "validate-tier-vm"}.Build(), Spec: spec}.Build(),
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(expected))
			if expected == grpccodes.OK {
				Expect(response.GetObject().GetSpec().GetBootDisk().GetStorageTier().GetId()).NotTo(BeEmpty())
				Expect(response.GetObject().GetSpec().GetBootDisk().GetStorageTier().GetName()).To(Equal("validated-tier"))
			} else {
				stored, err := server.List(ctx, privatev1.ComputeInstancesListRequest_builder{Filter: new("this.metadata.name == 'validate-tier-vm'")}.Build())
				Expect(err).NotTo(HaveOccurred())
				Expect(stored.GetItems()).To(BeEmpty())
			}
		},
			Entry("missing boot tier", "missing", false, false, grpccodes.NotFound),
			Entry("deleted boot tier", "deleted", false, false, grpccodes.InvalidArgument),
			Entry("inactive boot tier", "inactive", false, false, grpccodes.FailedPrecondition),
			Entry("missing additional tier", "missing", true, false, grpccodes.NotFound),
			Entry("deleted additional tier", "deleted", true, false, grpccodes.InvalidArgument),
			Entry("inactive additional tier", "inactive", true, false, grpccodes.FailedPrecondition),
			Entry("missing inherited tier", "missing", false, true, grpccodes.NotFound),
			Entry("deleted inherited tier", "deleted", false, true, grpccodes.InvalidArgument),
			Entry("inactive inherited tier", "inactive", false, true, grpccodes.FailedPrecondition),
			Entry("canonicalizes an active inherited tier", "active", false, true, grpccodes.OK),
		)

		It("Applies template spec defaults when user omits spec fields", func() {
			createTemplate("defaults-template")

			// Create a compute instance without any spec fields — validation should pass
			// because template defaults cover all required fields.
			response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "defaults-template"}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())

			spec := response.GetObject().GetSpec()
			// Template defaults should be stored:
			Expect(spec.GetInstanceType().GetId()).To(Equal("standard-4-16"))
			Expect(spec.GetRunStrategy()).To(Equal(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS))
			Expect(spec.GetDiskImage().GetId()).To(Equal("test-disk-image"))
			Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(10)))
			Expect(spec.GetBootDisk().GetStorageTier().GetName()).To(Equal("standard"))
			// Template reference should be preserved:
			Expect(spec.GetTemplate().GetId()).To(Equal("defaults-template"))
		})

		It("User-provided spec fields override template defaults", func() {
			createTemplate("override-template")

			// Create with user-provided run_strategy (overrides template default):
			response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template:    privatev1.ComputeInstanceTemplateReference_builder{Id: "override-template"}.Build(),
						RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED.Enum(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())

			spec := response.GetObject().GetSpec()
			// User-provided values should be stored:
			Expect(spec.GetRunStrategy()).To(Equal(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED))
			// Template defaults should be stored:
			Expect(spec.GetInstanceType().GetId()).To(Equal("standard-4-16"))
			Expect(spec.GetDiskImage().GetId()).To(Equal("test-disk-image"))
			Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(10)))
			Expect(spec.GetBootDisk().GetStorageTier().GetName()).To(Equal("standard"))
		})

		It("Rejects creation when the resolved boot disk storage tier is absent or empty", func() {
			templatesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceTemplate]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			const templateID = "missing-boot-tier-template"
			_, err = templatesDao.Create().SetObject(privatev1.ComputeInstanceTemplate_builder{
				Id:          templateID,
				Title:       "Missing Boot Tier Template",
				Description: "Template with a boot disk but no storage tier",
				Metadata: privatev1.Metadata_builder{
					Name:   templateID,
					Tenant: testTenant,
				}.Build(),
				SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
					InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
					DiskImage:    &privatev1.DiskImageReference{Id: "test-disk-image"},
					BootDisk: privatev1.ComputeInstanceDisk_builder{
						SizeGib: proto.Int32(10),
					}.Build(),
					RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			for _, bootDisk := range []*privatev1.ComputeInstanceDisk{
				nil,
				privatev1.ComputeInstanceDisk_builder{
					SizeGib:     proto.Int32(20),
					StorageTier: privatev1.StorageTierReference_builder{Name: ""}.Build(),
				}.Build(),
			} {
				name := fmt.Sprintf("test-%s", uuid.NewString()[:8])
				response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{Name: name}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: templateID}.Build(),
							BootDisk: bootDisk,
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
				Expect(status.Message()).To(Equal("boot_disk.storage_tier is required"))

				listResponse, err := server.List(ctx, privatev1.ComputeInstancesListRequest_builder{
					Filter: new(fmt.Sprintf("this.metadata.name == '%s'", name)),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(listResponse.GetSize()).To(Equal(int32(0)))
			}
		})

		It("Rejects creation when an additional disk storage tier is absent or empty", func() {
			createTemplate("additional-tier-template")

			for _, storageTier := range []*privatev1.StorageTierReference{nil, privatev1.StorageTierReference_builder{Name: ""}.Build()} {
				name := fmt.Sprintf("test-%s", uuid.NewString()[:8])
				additionalDisk := privatev1.ComputeInstanceDisk_builder{SizeGib: proto.Int32(50)}
				if storageTier != nil {
					additionalDisk.StorageTier = storageTier
				}
				response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{Name: name}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "additional-tier-template"}.Build(),
							AdditionalDisks: []*privatev1.ComputeInstanceDisk{
								additionalDisk.Build(),
							},
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
				Expect(status.Message()).To(Equal("additional_disks[0].storage_tier is required"))

				listResponse, err := server.List(ctx, privatev1.ComputeInstancesListRequest_builder{
					Filter: new(fmt.Sprintf("this.metadata.name == '%s'", name)),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(listResponse.GetSize()).To(Equal(int32(0)))
			}
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
					Name:   "no-defaults-template",
					Tenant: testTenant,
				}.Build(),
			}.Build()
			_, err = templatesDao.Create().SetObject(template).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create a compute instance without user-provided spec fields:
			response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "no-defaults-template"}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
					Name:   "bare-template",
					Tenant: testTenant,
				}.Build(),
			}.Build()
			_, err = templatesDao.Create().SetObject(template).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create with all required fields provided by user:
			response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template:     privatev1.ComputeInstanceTemplateReference_builder{Id: "bare-template"}.Build(),
						InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
						DiskImage:    &privatev1.DiskImageReference{Id: "test-disk-image"},
						BootDisk: privatev1.ComputeInstanceDisk_builder{
							SizeGib:     proto.Int32(20),
							StorageTier: privatev1.StorageTierReference_builder{Name: "standard"}.Build(),
						}.Build(),
						RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			Expect(response.GetObject().GetSpec().GetInstanceType().GetId()).To(Equal("standard-4-16"))
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
					Name:   "partial-defaults-template",
					Tenant: testTenant,
				}.Build(),
				SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
					InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
					RunStrategy:  privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
				}.Build(),
			}.Build()
			_, err = templatesDao.Create().SetObject(template).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// User provides the remaining required fields:
			response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template:  privatev1.ComputeInstanceTemplateReference_builder{Id: "partial-defaults-template"}.Build(),
						DiskImage: &privatev1.DiskImageReference{Id: "test-disk-image"},
						BootDisk: privatev1.ComputeInstanceDisk_builder{
							SizeGib:     proto.Int32(20),
							StorageTier: privatev1.StorageTierReference_builder{Name: "standard"}.Build(),
						}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())

			spec := response.GetObject().GetSpec()
			// Template defaults should be stored:
			Expect(spec.GetInstanceType().GetId()).To(Equal("standard-4-16"))
			Expect(spec.GetRunStrategy()).To(Equal(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS))
			// User-provided fields should be stored:
			Expect(spec.GetDiskImage().GetId()).To(Equal("test-disk-image"))
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

			createCICatalogItem := func(id string, published bool, fields *privatev1.ComputeInstanceCatalogItemFields) {
				_, err := catalogItemsDao.Create().SetObject(
					privatev1.ComputeInstanceCatalogItem_builder{
						Id: id,
						Metadata: privatev1.Metadata_builder{
							Name:   id + "-name",
							Tenant: testTenant,
						}.Build(),
						Title:     "Test CI Catalog Item",
						Published: published,
						Template:  privatev1.ComputeInstanceTemplateReference_builder{Id: "ci-template-id"}.Build(),
						Fields:    fields,
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			It("Creates compute instance with catalog item", func() {
				createCICatalogItem("ci-cat-happy", true, nil)

				response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: privatev1.ComputeInstanceCatalogItemReference_builder{Id: "ci-cat-happy"}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
				Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("ci-template-id"))
				Expect(object.GetSpec().GetCatalogItem().GetId()).To(Equal("ci-cat-happy"))
			})

			It("Materializes a catalog item and user data Secret during dry run without persisting", func() {
				createCICatalogItem("ci-cat-secret", true, nil)
				secret := createUserDataSecret()

				response, err := server.Create(dryRunCtx(), privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.NewString()[:8])}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem:    privatev1.ComputeInstanceCatalogItemReference_builder{Id: "ci-cat-secret"}.Build(),
							UserDataSecret: privatev1.SecretLocalReference_builder{Name: secret.GetMetadata().GetName()}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetSpec().GetUserDataSecret().GetId()).To(Equal(secret.GetId()))
				_, err = server.generic.dao.Get().SetId(response.GetObject().GetId()).Do(ctx)
				Expect(err).To(HaveOccurred())
			})

			It("Rejects a user data Secret when a catalog policy supplies inline user data", func() {
				userData := "#cloud-config"
				createCICatalogItem("ci-cat-userdata", true, privatev1.ComputeInstanceCatalogItemFields_builder{
					UserData: privatev1.StringFieldPolicy_builder{Locked: &userData}.Build(),
				}.Build())
				secret := createUserDataSecret()

				_, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.NewString()[:8])}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem:    privatev1.ComputeInstanceCatalogItemReference_builder{Id: "ci-cat-userdata"}.Build(),
							UserDataSecret: privatev1.SecretLocalReference_builder{Id: secret.GetId()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("user_data and user_data_secret are mutually exclusive"))
			})

			It("Creates compute instance with catalog item specified by name", func() {
				createCICatalogItem("ci-cat-byname", true, nil)

				response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: privatev1.ComputeInstanceCatalogItemReference_builder{Name: "ci-cat-byname-name"}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				object := response.GetObject()
				Expect(object).ToNot(BeNil())
				Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("ci-template-id"))
			})

			It("Fails when catalog item not found", func() {
				_, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: privatev1.ComputeInstanceCatalogItemReference_builder{Id: "nonexistent"}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
					"catalog item 'nonexistent' not found",
				))
			})

			It("Fails when catalog item is not published", func() {
				createCICatalogItem("ci-cat-unpub", false, nil)

				_, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: privatev1.ComputeInstanceCatalogItemReference_builder{Id: "ci-cat-unpub"}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
							Tenant: testTenant,
						}.Build(),
						Title:     "Catalog Item without template",
						Published: true,
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: privatev1.ComputeInstanceCatalogItemReference_builder{Id: "ci-cat-no-template"}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: privatev1.ComputeInstanceCatalogItemReference_builder{Id: "any-catalog-item"}.Build(),
							Template:    privatev1.ComputeInstanceTemplateReference_builder{Id: "some-template"}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
				createCICatalogItem("ci-cat-nonedit", true, privatev1.ComputeInstanceCatalogItemFields_builder{SshPublicKey: privatev1.StringFieldPolicy_builder{Locked: proto.String("forced-key")}.Build(), NetworkAttachments: privatev1.ComputeNetworkAttachmentListFieldPolicy_builder{Editable: &privatev1.EditableComputeNetworkAttachmentList{}}.Build()}.Build())

				_, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem:  privatev1.ComputeInstanceCatalogItemReference_builder{Id: "ci-cat-nonedit"}.Build(),
							SshPublicKey: new("user-key"),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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

			DescribeTable("validates editable SSH public keys",
				func(catID string, value string, expectError bool) {
					createCICatalogItem(catID, true, privatev1.ComputeInstanceCatalogItemFields_builder{SshPublicKey: privatev1.StringFieldPolicy_builder{Editable: privatev1.EditableStringField_builder{}.Build()}.Build(), NetworkAttachments: privatev1.ComputeNetworkAttachmentListFieldPolicy_builder{Editable: &privatev1.EditableComputeNetworkAttachmentList{}}.Build()}.Build())

					response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
						Object: privatev1.ComputeInstance_builder{
							Metadata: privatev1.Metadata_builder{
								Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
							}.Build(),
							Spec: privatev1.ComputeInstanceSpec_builder{
								CatalogItem:  privatev1.ComputeInstanceCatalogItemReference_builder{Id: catID}.Build(),
								SshPublicKey: new(value),
								NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
									privatev1.ComputeNetworkAttachment_builder{
										Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
						Expect(status.Message()).To(ContainSubstring("spec.ssh_public_key"))
						Expect(status.Message()).To(ContainSubstring("invalid OpenSSH public key"))
					} else {
						Expect(err).ToNot(HaveOccurred())
						Expect(response.GetObject().GetSpec().GetSshPublicKey()).To(Equal(value))
					}
				},
				Entry("rejects malformed value", "ci-cat-ssh-invalid", "short-val", true),
				Entry("accepts an OpenSSH public key", "ci-cat-ssh-valid", testSSHPublicKey, false),
			)

			It("Applies default for editable field when not provided", func() {
				createCICatalogItem("ci-cat-dflt", true, privatev1.ComputeInstanceCatalogItemFields_builder{SshPublicKey: privatev1.StringFieldPolicy_builder{Editable: privatev1.EditableStringField_builder{DefaultValue: proto.String(testSSHPublicKey)}.Build()}.Build(), NetworkAttachments: privatev1.ComputeNetworkAttachmentListFieldPolicy_builder{Editable: &privatev1.EditableComputeNetworkAttachmentList{}}.Build()}.Build())

				response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: privatev1.ComputeInstanceCatalogItemReference_builder{Id: "ci-cat-dflt"}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()
				Expect(object.GetSpec().GetSshPublicKey()).To(Equal(testSSHPublicKey))
			})

			It("Rejects changing catalog_item on update", func() {
				createCICatalogItem("ci-cat-immut", true, nil)

				createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-compute-instance",
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: privatev1.ComputeInstanceCatalogItemReference_builder{Id: "ci-cat-immut"}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
							CatalogItem: privatev1.ComputeInstanceCatalogItemReference_builder{Id: "different-catalog-item"}.Build(),
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
							Name:   "ci-template-no-defaults",
							Tenant: testTenant,
						}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = catalogItemsDao.Create().SetObject(
					privatev1.ComputeInstanceCatalogItem_builder{
						Id: "ci-cat-no-defaults",
						Metadata: privatev1.Metadata_builder{
							Name:   "ci-cat-no-defaults-name",
							Tenant: testTenant,
						}.Build(),
						Title:     "Catalog Item without defaults",
						Published: true,
						Template:  privatev1.ComputeInstanceTemplateReference_builder{Id: "ci-template-no-defaults"}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: privatev1.ComputeInstanceCatalogItemReference_builder{Id: "ci-cat-no-defaults"}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
							Name:   "ci-template-with-it",
							Tenant: testTenant,
						}.Build(),
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							InstanceType: privatev1.InstanceTypeReference_builder{Id: "nonexistent-instance-type"}.Build(),
							DiskImage:    &privatev1.DiskImageReference{Id: "test-disk-image"},
							BootDisk: privatev1.ComputeInstanceDisk_builder{
								SizeGib:     proto.Int32(10),
								StorageTier: privatev1.StorageTierReference_builder{Name: "standard"}.Build(),
							}.Build(),
							RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
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
							Tenant: testTenant,
						}.Build(),
						Title:     "Catalog Item with IT",
						Published: true,
						Template:  privatev1.ComputeInstanceTemplateReference_builder{Id: "ci-template-with-it"}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create via catalog item — should fail because instance type doesn't exist:
				_, err = server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: privatev1.ComputeInstanceCatalogItemReference_builder{Id: "ci-cat-with-it"}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
							Name:   "ci-template-valid-it",
							Tenant: testTenant,
						}.Build(),
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
							DiskImage:    &privatev1.DiskImageReference{Id: "test-disk-image"},
							BootDisk: privatev1.ComputeInstanceDisk_builder{
								SizeGib:     proto.Int32(10),
								StorageTier: privatev1.StorageTierReference_builder{Name: "standard"}.Build(),
							}.Build(),
							RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
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
							Tenant: testTenant,
						}.Build(),
						Title:     "Catalog Item with valid IT",
						Published: true,
						Template:  privatev1.ComputeInstanceTemplateReference_builder{Id: "ci-template-valid-it"}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create via catalog item — should succeed:
				response, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							CatalogItem: privatev1.ComputeInstanceCatalogItemReference_builder{Id: "ci-cat-valid-it"}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				object := response.GetObject()
				Expect(object).ToNot(BeNil())
				Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("ci-template-valid-it"))
				Expect(object.GetSpec().GetCatalogItem().GetId()).To(Equal("ci-cat-valid-it"))
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
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Vcpus:     4,
						MemoryGib: 16,
						State:     privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			storageTiersDao, err := dao.NewGenericDAO[*privatev1.StorageTier]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = storageTiersDao.Create().SetObject(
				privatev1.StorageTier_builder{
					Id: "standard",
					Metadata: privatev1.Metadata_builder{
						Name:   "standard",
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.StorageTierSpec_builder{
						Description: "Standard storage tier",
					}.Build(),
					Status: privatev1.StorageTierStatus_builder{
						State: privatev1.StorageTierState_STORAGE_TIER_STATE_ACTIVE,
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			template = privatev1.ComputeInstanceTemplate_builder{
				Id:          "test-template",
				Title:       "Test Template",
				Description: "Test template for network validation",
				Metadata: privatev1.Metadata_builder{
					Name:   "test-template",
					Tenant: testTenant,
				}.Build(),
				Parameters: []*privatev1.ComputeInstanceTemplateParameterDefinition{
					{
						Name:        "cpu_count",
						Title:       "CPU Count",
						Description: "Number of vCPUs",
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
					InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
					DiskImage:    &privatev1.DiskImageReference{Id: "test-disk-image"},
					BootDisk: privatev1.ComputeInstanceDisk_builder{
						SizeGib:     proto.Int32(10),
						StorageTier: privatev1.StorageTierReference_builder{Name: "standard"}.Build(),
					}.Build(),
					RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{Subnet: privatev1.SubnetLocalReference_builder{Id: s1.GetId()}.Build()}.Build(),
							privatev1.ComputeNetworkAttachment_builder{Subnet: privatev1.SubnetLocalReference_builder{Id: s2.GetId()}.Build()}.Build(),
						},
					}.Build(),
				}.Build()

				request := &privatev1.ComputeInstancesCreateRequest{}
				request.SetObject(vm)

				response, err := server.Create(ctx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				Expect(response.GetObject().GetSpec().GetNetworkAttachments()).To(HaveLen(2))
				Expect(response.GetObject().GetSpec().GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal(s1.GetId()))
				Expect(response.GetObject().GetSpec().GetNetworkAttachments()[1].GetSubnet().GetId()).To(Equal(s2.GetId()))
			})
		})

		Context("Required network fields", func() {
			It("Should reject when network_attachments is missing and no default subnet exists", func() {
				vm := privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
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

			It("Should auto-inject default subnet and security group when network_attachments is empty", func() {
				defaultSubnetDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				defaultSubnet := privatev1.Subnet_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "test-default-subnet",
						Tenant: testTenant,
						Labels: map[string]string{
							"osac.openshift.io/default": "true",
						},
					}.Build(),
					Spec: privatev1.SubnetSpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetwork.GetId()}.Build(),
						Ipv4Cidr:       new("10.0.1.0/24"),
					}.Build(),
					Status: privatev1.SubnetStatus_builder{
						State: privatev1.SubnetState_SUBNET_STATE_READY,
					}.Build(),
				}.Build()

				subnetResponse, err := defaultSubnetDao.Create().SetObject(defaultSubnet).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				defaultSGDao, err := dao.NewGenericDAO[*privatev1.SecurityGroup]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				defaultSG := privatev1.SecurityGroup_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "test-default-sg",
						Tenant: testTenant,
						Labels: map[string]string{
							"osac.openshift.io/default": "true",
						},
					}.Build(),
					Spec: privatev1.SecurityGroupSpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetwork.GetId()}.Build(),
					}.Build(),
					Status: privatev1.SecurityGroupStatus_builder{
						State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
					}.Build(),
				}.Build()

				sgResponse, err := defaultSGDao.Create().SetObject(defaultSG).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				vm := privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
					}.Build(),
				}.Build()

				request := &privatev1.ComputeInstancesCreateRequest{}
				request.SetObject(vm)

				response, err := server.Create(ctx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				attachments := response.GetObject().GetSpec().GetNetworkAttachments()
				Expect(attachments).To(HaveLen(1))
				Expect(attachments[0].GetSubnet().GetId()).To(Equal(subnetResponse.GetObject().GetId()))
				Expect(attachments[0].GetSecurityGroups()).To(HaveLen(1))
				Expect(attachments[0].GetSecurityGroups()[0].GetId()).To(Equal(sgResponse.GetObject().GetId()))
			})

			It("Should auto-inject default subnet without security group when no default SG exists", func() {
				defaultSubnetDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				defaultSubnet := privatev1.Subnet_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "test-default-subnet",
						Tenant: testTenant,
						Labels: map[string]string{
							"osac.openshift.io/default": "true",
						},
					}.Build(),
					Spec: privatev1.SubnetSpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetwork.GetId()}.Build(),
						Ipv4Cidr:       new("10.0.1.0/24"),
					}.Build(),
					Status: privatev1.SubnetStatus_builder{
						State: privatev1.SubnetState_SUBNET_STATE_READY,
					}.Build(),
				}.Build()

				subnetResponse, err := defaultSubnetDao.Create().SetObject(defaultSubnet).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				vm := privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-pod-network-vm",
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
					}.Build(),
				}.Build()

				request := &privatev1.ComputeInstancesCreateRequest{}
				request.SetObject(vm)

				response, err := server.Create(ctx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				attachments := response.GetObject().GetSpec().GetNetworkAttachments()
				Expect(attachments).To(HaveLen(1))
				Expect(attachments[0].GetSubnet().GetId()).To(Equal(subnetResponse.GetObject().GetId()))
				Expect(attachments[0].GetSecurityGroups()).To(BeEmpty())
			})

			It("Should not auto-inject when default subnet is not READY", func() {
				defaultSubnetDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				defaultSubnet := privatev1.Subnet_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "test-default-subnet",
						Tenant: testTenant,
						Labels: map[string]string{
							"osac.openshift.io/default": "true",
						},
					}.Build(),
					Spec: privatev1.SubnetSpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetwork.GetId()}.Build(),
						Ipv4Cidr:       new("10.0.1.0/24"),
					}.Build(),
					Status: privatev1.SubnetStatus_builder{
						State: privatev1.SubnetState_SUBNET_STATE_PENDING,
					}.Build(),
				}.Build()

				_, err = defaultSubnetDao.Create().SetObject(defaultSubnet).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				vm := privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "pod-network-vm",
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
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
						Name:   "pod-network-vm",
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
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
							Template:    privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
							RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.run_strategy"},
					},
				}.Build())

				// Update should succeed for backward compatibility
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse).ToNot(BeNil())
				Expect(updateResponse.GetObject().GetSpec().GetRunStrategy()).To(Equal(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS))
			})
		})

		Context("NetworkAttachments validation", func() {
			It("Should reject when subnet not found in network_attachments", func() {
				vm := privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{Subnet: privatev1.SubnetLocalReference_builder{Id: "nonexistent-subnet"}.Build()}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{Subnet: privatev1.SubnetLocalReference_builder{Id: subnet.GetId()}.Build()}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet:         privatev1.SubnetLocalReference_builder{Id: subnet.GetId()}.Build(),
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "nonexistent-sg"}.Build()},
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet:         privatev1.SubnetLocalReference_builder{Id: subnet.GetId()}.Build(),
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: wrongSG.GetId()}.Build()},
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
				Expect(status.Message()).To(ContainSubstring("belongs to a different virtual network"))
			})

			It("Should allow empty security_groups in network_attachments", func() {
				subnet := createTestSubnet(ctx, virtualNetwork.GetId(), privatev1.SubnetState_SUBNET_STATE_READY)

				vm := privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet:         privatev1.SubnetLocalReference_builder{Id: subnet.GetId()}.Build(),
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{},
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet:         privatev1.SubnetLocalReference_builder{Id: subnet.GetId()}.Build(),
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: sg.GetId()}.Build()},
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
			It("uses persisted deletion state when validating network updates", func() {
				// Create with a READY subnet
				subnet := createTestSubnet(ctx, virtualNetwork.GetId(), privatev1.SubnetState_SUBNET_STATE_READY)
				sg := createTestSecurityGroup(ctx, virtualNetwork.GetId(), privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY)

				createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-compute-instance",
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet:         privatev1.SubnetLocalReference_builder{Id: subnet.GetId()}.Build(),
									SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: sg.GetId()}.Build()},
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				created := createResponse.GetObject()
				originalAttachment := proto.Clone(created.GetSpec().GetNetworkAttachments()[0]).(*privatev1.ComputeNetworkAttachment)

				// Update subnet to non-READY state (simulate resource being deleted/modified)
				subnet.GetStatus().SetState(privatev1.SubnetState_SUBNET_STATE_PENDING)
				subnetDAO, err := dao.NewGenericDAO[*privatev1.Subnet]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = subnetDAO.Update().SetObject(subnet).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Supplying a timestamp must not mark the stored ComputeInstance as deleting.
				deletionTime := timestamppb.Now()
				created.GetMetadata().SetDeletionTimestamp(deletionTime)

				// An unchanged attachment still goes through readiness validation while the instance is live.
				updateRequest := &privatev1.ComputeInstancesUpdateRequest{}
				updateRequest.SetObject(created)
				updateRequest.SetUpdateMask(&fieldmaskpb.FieldMask{Paths: []string{"spec.network_attachments"}})

				// An unmasked timestamp must not bypass validation of an active instance.
				_, err = server.Update(ctx, updateRequest)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
				updateRequest.SetUpdateMask(&fieldmaskpb.FieldMask{Paths: []string{
					"spec.network_attachments", "metadata.deletion_timestamp",
				}})
				_, err = server.Update(ctx, updateRequest)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
				stored, err := server.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: created.GetId()}.Build())
				Expect(err).NotTo(HaveOccurred())
				Expect(stored.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse())
				Expect(stored.GetObject().GetSpec().GetNetworkAttachments()[0].GetSecurityGroups()).To(HaveLen(1))

				// Only Delete may establish the exemption; a finalizer keeps the object available.
				_, err = server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object:     privatev1.ComputeInstance_builder{Id: created.GetId(), Metadata: privatev1.Metadata_builder{Finalizers: []string{"test"}}.Build()}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
				}.Build())
				Expect(err).NotTo(HaveOccurred())
				_, err = server.Delete(ctx, privatev1.ComputeInstancesDeleteRequest_builder{Id: created.GetId()}.Build())
				Expect(err).NotTo(HaveOccurred())

				// Deletion permits readiness checks to tolerate the pending subnet, but does not make the attachment mutable.
				deletingObject := proto.Clone(created).(*privatev1.ComputeInstance)
				deletingObject.GetSpec().GetNetworkAttachments()[0].SetSecurityGroups(nil)
				updateRequest.SetObject(deletingObject)
				updateRequest.SetUpdateMask(&fieldmaskpb.FieldMask{Paths: []string{"spec.network_attachments"}})
				_, err = server.Update(ctx, updateRequest)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				stored, err = server.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: created.GetId()}.Build())
				Expect(err).NotTo(HaveOccurred())
				Expect(stored.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())
				Expect(stored.GetObject().GetSpec().GetNetworkAttachments()[0].GetSecurityGroups()).To(HaveLen(1))

				// An unchanged attachment update is still accepted for a deleting instance.
				deletingObject.GetSpec().SetNetworkAttachments([]*privatev1.ComputeNetworkAttachment{originalAttachment})
				updateRequest.SetObject(deletingObject)
				_, err = server.Update(ctx, updateRequest)
				Expect(err).NotTo(HaveOccurred())
				stored, err = server.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: created.GetId()}.Build())
				Expect(err).NotTo(HaveOccurred())
				Expect(proto.Equal(stored.GetObject().GetSpec().GetNetworkAttachments()[0], originalAttachment)).To(BeTrue())

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
						Metadata: privatev1.Metadata_builder{
							Name: "test-compute-instance",
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet:         privatev1.SubnetLocalReference_builder{Id: subnet1.GetId()}.Build(),
									SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: sg1.GetId()}.Build()},
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
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet:         privatev1.SubnetLocalReference_builder{Id: subnet2.GetId()}.Build(),
									SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: sg1.GetId()}.Build()},
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
				stored, err := server.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(proto.Equal(stored.GetObject().GetSpec(), createResponse.GetObject().GetSpec())).To(BeTrue())
			})

			It("Rejects changing security groups in network_attachments", func() {
				// Create a ComputeInstance with networkAttachments
				createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-compute-instance",
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet:         privatev1.SubnetLocalReference_builder{Id: subnet1.GetId()}.Build(),
									SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: sg1.GetId()}.Build()},
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(createResponse).ToNot(BeNil())

				id := createResponse.GetObject().GetId()

				// Change only security groups.
				updateResponse, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: id,
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet:         privatev1.SubnetLocalReference_builder{Id: subnet1.GetId()}.Build(),                                                                                                                      // Same subnet
									SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: sg1.GetId()}.Build(), privatev1.SecurityGroupLocalReference_builder{Id: sg2.GetId()}.Build()}, // Different SGs
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.network_attachments"},
					},
				}.Build())
				Expect(updateResponse).To(BeNil())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				stored, err := server.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build())
				Expect(err).ToNot(HaveOccurred())
				attachments := stored.GetObject().GetSpec().GetNetworkAttachments()
				Expect(attachments).To(HaveLen(1))
				Expect(attachments[0].GetSubnet().GetId()).To(Equal(subnet1.GetId()))
				Expect(attachments[0].GetSecurityGroups()).To(HaveLen(1))
				Expect(attachments[0].GetSecurityGroups()[0].GetId()).To(Equal(sg1.GetId()))
			})

			It("Accepts an identical network attachment", func() {
				created, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{Name: "test-compute-instance"}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: subnet1.GetId()}.Build(),
									SecurityGroups: []*privatev1.SecurityGroupLocalReference{
										privatev1.SecurityGroupLocalReference_builder{Id: sg1.GetId()}.Build(),
									},
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				id := created.GetObject().GetId()
				updated, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: id,
						Spec: privatev1.ComputeInstanceSpec_builder{
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: subnet1.GetId()}.Build(),
									SecurityGroups: []*privatev1.SecurityGroupLocalReference{
										privatev1.SecurityGroupLocalReference_builder{Id: sg1.GetId()}.Build(),
									},
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.network_attachments"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updated.GetObject().GetSpec().GetNetworkAttachments()).To(HaveLen(1))
				Expect(updated.GetObject().GetSpec().GetNetworkAttachments()[0].GetSecurityGroups()[0].GetId()).To(Equal(sg1.GetId()))
			})

			It("Rejects reordering network attachments", func() {
				first := privatev1.ComputeNetworkAttachment_builder{
					Subnet: privatev1.SubnetLocalReference_builder{Id: subnet1.GetId()}.Build(),
				}.Build()
				second := privatev1.ComputeNetworkAttachment_builder{
					Subnet: privatev1.SubnetLocalReference_builder{Id: subnet2.GetId()}.Build(),
				}.Build()
				created, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{Name: "test-compute-instance"}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template:           privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{first, second},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				id := created.GetObject().GetId()
				response, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: id,
						Spec: privatev1.ComputeInstanceSpec_builder{
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{second, first},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.network_attachments"}},
				}.Build())
				Expect(response).To(BeNil())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				stored, err := server.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build())
				Expect(err).ToNot(HaveOccurred())
				attachments := stored.GetObject().GetSpec().GetNetworkAttachments()
				Expect(attachments).To(HaveLen(2))
				Expect(attachments[0].GetSubnet().GetId()).To(Equal(subnet1.GetId()))
				Expect(attachments[1].GetSubnet().GetId()).To(Equal(subnet2.GetId()))
				original := created.GetObject().GetSpec().GetNetworkAttachments()
				Expect(original).To(HaveLen(2))
				Expect(proto.Equal(attachments[0], original[0])).To(BeTrue())
				Expect(proto.Equal(attachments[1], original[1])).To(BeTrue())
			})

			It("Rejects adding network attachments", func() {
				// Create with 1 attachment
				createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-compute-instance",
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: subnet1.GetId()}.Build(),
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
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: subnet1.GetId()}.Build(),
								}.Build(),
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: subnet2.GetId()}.Build(),
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
				stored, err := server.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(proto.Equal(stored.GetObject().GetSpec(), createResponse.GetObject().GetSpec())).To(BeTrue())
			})

			It("Rejects removing network attachments", func() {
				// Create with 2 attachments
				createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-compute-instance",
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: subnet1.GetId()}.Build(),
								}.Build(),
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: subnet2.GetId()}.Build(),
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
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: subnet1.GetId()}.Build(),
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
				stored, err := server.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(proto.Equal(stored.GetObject().GetSpec(), createResponse.GetObject().GetSpec())).To(BeTrue())
			})
		})

		Context("Disk immutability", func() {
			var (
				storageTiersDao *dao.GenericDAO[*privatev1.StorageTier]
				subnet          *privatev1.Subnet
			)

			BeforeEach(func() {
				var err error
				storageTiersDao, err = dao.NewGenericDAO[*privatev1.StorageTier]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				// Create tier1
				_, err = storageTiersDao.Create().SetObject(
					privatev1.StorageTier_builder{
						Id: "tier1",
						Metadata: privatev1.Metadata_builder{
							Name:   "tier1",
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.StorageTierSpec_builder{
							Description: "Test storage tier 1",
						}.Build(),
						Status: privatev1.StorageTierStatus_builder{
							State: privatev1.StorageTierState_STORAGE_TIER_STATE_ACTIVE,
						}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create tier2
				_, err = storageTiersDao.Create().SetObject(
					privatev1.StorageTier_builder{
						Id: "tier2",
						Metadata: privatev1.Metadata_builder{
							Name:   "tier2",
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.StorageTierSpec_builder{
							Description: "Test storage tier 2",
						}.Build(),
						Status: privatev1.StorageTierStatus_builder{
							State: privatev1.StorageTierState_STORAGE_TIER_STATE_ACTIVE,
						}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create subnet for network attachments
				subnet = createTestSubnet(ctx, virtualNetwork.GetId(), privatev1.SubnetState_SUBNET_STATE_READY)
			})

			It("Rejects changing boot_disk.storage_tier", func() {
				// Create a ComputeInstance with boot_disk storage tier
				createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-compute-instance",
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
							BootDisk: privatev1.ComputeInstanceDisk_builder{
								SizeGib:     proto.Int32(100),
								StorageTier: privatev1.StorageTierReference_builder{Name: "tier1"}.Build(),
							}.Build(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: subnet.GetId()}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(createResponse).ToNot(BeNil())

				id := createResponse.GetObject().GetId()

				// Try to change boot_disk.storage_tier
				updateResponse, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: id,
						Spec: privatev1.ComputeInstanceSpec_builder{
							BootDisk: privatev1.ComputeInstanceDisk_builder{
								SizeGib:     proto.Int32(100),
								StorageTier: privatev1.StorageTierReference_builder{Name: "tier2"}.Build(),
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.boot_disk"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(updateResponse).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("boot disk is immutable"))
			})

			It("Rejects changing additional_disks.storage_tier", func() {
				// Create a ComputeInstance with additional disks
				createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-compute-instance",
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
							BootDisk: privatev1.ComputeInstanceDisk_builder{
								SizeGib:     proto.Int32(100),
								StorageTier: privatev1.StorageTierReference_builder{Name: "tier1"}.Build(),
							}.Build(),
							AdditionalDisks: []*privatev1.ComputeInstanceDisk{
								privatev1.ComputeInstanceDisk_builder{
									SizeGib:     proto.Int32(200),
									StorageTier: privatev1.StorageTierReference_builder{Name: "tier1"}.Build(),
								}.Build(),
							},
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: subnet.GetId()}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(createResponse).ToNot(BeNil())

				id := createResponse.GetObject().GetId()

				// Try to change additional_disks[0].storage_tier
				updateResponse, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: id,
						Spec: privatev1.ComputeInstanceSpec_builder{
							AdditionalDisks: []*privatev1.ComputeInstanceDisk{
								privatev1.ComputeInstanceDisk_builder{
									SizeGib:     proto.Int32(200),
									StorageTier: privatev1.StorageTierReference_builder{Name: "tier2"}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.additional_disks"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(updateResponse).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("disk is immutable"))
			})

			It("Rejects changing additional_disks.size_gib", func() {
				// Create a ComputeInstance with additional disks
				createResponse, err := server.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-compute-instance",
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: template.GetId()}.Build(),
							BootDisk: privatev1.ComputeInstanceDisk_builder{
								SizeGib:     proto.Int32(100),
								StorageTier: privatev1.StorageTierReference_builder{Name: "tier1"}.Build(),
							}.Build(),
							AdditionalDisks: []*privatev1.ComputeInstanceDisk{
								privatev1.ComputeInstanceDisk_builder{
									SizeGib:     proto.Int32(200),
									StorageTier: privatev1.StorageTierReference_builder{Name: "tier1"}.Build(),
								}.Build(),
							},
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: subnet.GetId()}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(createResponse).ToNot(BeNil())

				id := createResponse.GetObject().GetId()

				// Try to change additional_disks[0].size_gib
				updateResponse, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: id,
						Spec: privatev1.ComputeInstanceSpec_builder{
							AdditionalDisks: []*privatev1.ComputeInstanceDisk{
								privatev1.ComputeInstanceDisk_builder{
									SizeGib:     proto.Int32(300),
									StorageTier: privatev1.StorageTierReference_builder{Name: "tier1"}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.additional_disks"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(updateResponse).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("disk is immutable"))
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
							Tenant: testTenant,
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Vcpus:     4,
							MemoryGib: 16,
							State:     state,
						}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			createInstanceTypeWithIdentity := func(id, name string, state privatev1.InstanceTypeState, vcpus, memoryGib int32, gpu *privatev1.GpuSpec, deprecation *privatev1.InstanceTypeDeprecation) {
				instanceTypesDao, err := dao.NewGenericDAO[*privatev1.InstanceType]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				_, err = instanceTypesDao.Create().SetObject(
					privatev1.InstanceType_builder{
						Id: id,
						Metadata: privatev1.Metadata_builder{
							Name:   name,
							Tenant: testTenant,
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Vcpus:       vcpus,
							MemoryGib:   memoryGib,
							State:       state,
							Gpu:         gpu,
							Deprecation: deprecation,
						}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			createInstanceTypeWithSpec := func(name string, state privatev1.InstanceTypeState, vcpus, memoryGib int32, gpu *privatev1.GpuSpec, deprecation *privatev1.InstanceTypeDeprecation) {
				createInstanceTypeWithIdentity(name, name, state, vcpus, memoryGib, gpu, deprecation)
			}

			setInstanceTypeState := func(instanceTypeName string, state privatev1.InstanceTypeState) {
				instanceTypesDao, err := dao.NewGenericDAO[*privatev1.InstanceType]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				getResponse, err := instanceTypesDao.Get().SetId(instanceTypeName).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				instanceType := getResponse.GetObject()
				instanceType.GetSpec().SetState(state)
				_, err = instanceTypesDao.Update().SetObject(instanceType).Do(ctx)
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
							Name:   templateID,
							Tenant: testTenant,
						}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				return privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template:     privatev1.ComputeInstanceTemplateReference_builder{Id: templateID}.Build(),
							InstanceType: privatev1.InstanceTypeReference_builder{Id: instanceTypeName}.Build(),
							DiskImage:    &privatev1.DiskImageReference{Id: "test-disk-image"},
							BootDisk: privatev1.ComputeInstanceDisk_builder{
								SizeGib:     proto.Int32(20),
								StorageTier: privatev1.StorageTierReference_builder{Name: "standard"}.Build(),
							}.Build(),
							RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build()
			}

			createComputeInstanceWithType := func(instanceTypeName string) string {
				response, err := server.Create(ctx, createRequestWithInstanceType(instanceTypeName))
				Expect(err).ToNot(HaveOccurred())
				return response.GetObject().GetId()
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

			It("Allows InstanceType changes with more or fewer vCPUs and memory", func() {
				createInstanceTypeWithSpec("resize-current", privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE, 4, 16, nil, nil)
				createInstanceTypeWithSpec("resize-large", privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE, 8, 32, nil, nil)
				createInstanceTypeWithSpec("resize-small", privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE, 2, 8, nil, nil)
				id := createComputeInstanceWithType("resize-current")

				update := func(instanceTypeName string) *privatev1.ComputeInstancesUpdateResponse {
					response, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
						Object: privatev1.ComputeInstance_builder{
							Id: id,
							Spec: privatev1.ComputeInstanceSpec_builder{
								InstanceType: privatev1.InstanceTypeReference_builder{Id: instanceTypeName}.Build(),
							}.Build(),
						}.Build(),
						UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.instance_type"}},
					}.Build())
					Expect(err).ToNot(HaveOccurred())
					return response
				}

				Expect(update("resize-large").GetObject().GetSpec().GetInstanceType().GetId()).To(Equal("resize-large"))
				Expect(update("resize-small").GetObject().GetSpec().GetInstanceType().GetId()).To(Equal("resize-small"))
			})

			It("Resolves name-only current and target InstanceType references before comparison", func() {
				createInstanceTypeWithIdentity("resize-current-id", "resize-current", privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE, 4, 16, nil, nil)
				createInstanceTypeWithIdentity("resize-target-id", "resize-target", privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE, 8, 32, nil, nil)
				id := createComputeInstanceWithType("resize-current-id")

				stored, err := server.generic.dao.Get().SetId(id).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				stored.GetObject().GetSpec().SetInstanceType(
					privatev1.InstanceTypeReference_builder{Name: "resize-current"}.Build(),
				)
				_, err = server.generic.dao.Update().SetObject(stored.GetObject()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				response, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: id,
						Spec: privatev1.ComputeInstanceSpec_builder{
							InstanceType: privatev1.InstanceTypeReference_builder{Name: "resize-target"}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.instance_type"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetSpec().GetInstanceType().GetId()).To(Equal("resize-target-id"))
			})

			It("Returns a warning when resizing to a DEPRECATED InstanceType", func() {
				createInstanceTypeWithSpec("resize-current", privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE, 4, 16, nil, nil)
				createInstanceTypeWithSpec("resize-deprecated", privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED, 8, 32, nil,
					privatev1.InstanceTypeDeprecation_builder{
						Replacement:           privatev1.InstanceTypeLocalReference_builder{Id: "resize-replacement"}.Build(),
						ObsolescenceTimestamp: timestamppb.New(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)),
					}.Build())
				id := createComputeInstanceWithType("resize-current")

				response, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: id,
						Spec: privatev1.ComputeInstanceSpec_builder{
							InstanceType: privatev1.InstanceTypeReference_builder{Id: "resize-deprecated"}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.instance_type"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetWarnings()).To(HaveLen(1))
				Expect(response.GetWarnings()[0]).To(ContainSubstring("resize-replacement"))
				Expect(response.GetWarnings()[0]).To(ContainSubstring("2027"))
			})

			It("Rejects OBSOLETE and missing resize targets before persistence", func() {
				createInstanceTypeWithSpec("resize-current", privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE, 4, 16, nil, nil)
				createInstanceTypeWithSpec("resize-obsolete", privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE, 8, 32, nil, nil)
				id := createComputeInstanceWithType("resize-current")

				for _, target := range []struct {
					name string
					code grpccodes.Code
				}{
					{name: "resize-obsolete", code: grpccodes.FailedPrecondition},
					{name: "resize-missing", code: grpccodes.InvalidArgument},
				} {
					response, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
						Object: privatev1.ComputeInstance_builder{
							Id: id,
							Spec: privatev1.ComputeInstanceSpec_builder{
								InstanceType: privatev1.InstanceTypeReference_builder{Id: target.name}.Build(),
							}.Build(),
						}.Build(),
						UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.instance_type"}},
					}.Build())
					Expect(response).To(BeNil())
					status, ok := grpcstatus.FromError(err)
					Expect(ok).To(BeTrue())
					Expect(status.Code()).To(Equal(target.code))

					getResponse, getErr := server.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build())
					Expect(getErr).ToNot(HaveOccurred())
					Expect(getResponse.GetObject().GetSpec().GetInstanceType().GetId()).To(Equal("resize-current"))
				}
			})

			It("Rejects a resize with a different GPU and accepts the same GPU", func() {
				currentGPU := privatev1.GpuSpec_builder{
					PciDeviceSelector: "10DE:20B0",
					ResourceName:      "nvidia.com/A100",
					Count:             1,
				}.Build()
				otherGPU := privatev1.GpuSpec_builder{
					PciDeviceSelector: "10DE:1EB8",
					ResourceName:      "nvidia.com/V100",
					Count:             1,
				}.Build()
				createInstanceTypeWithSpec("resize-gpu-current", privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE, 4, 16, currentGPU, nil)
				createInstanceTypeWithSpec("resize-gpu-compatible", privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE, 8, 32, currentGPU, nil)
				createInstanceTypeWithSpec("resize-gpu-incompatible", privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE, 8, 32, otherGPU, nil)
				id := createComputeInstanceWithType("resize-gpu-current")

				response, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: id,
						Spec: privatev1.ComputeInstanceSpec_builder{
							InstanceType: privatev1.InstanceTypeReference_builder{Id: "resize-gpu-compatible"}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.instance_type"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetSpec().GetInstanceType().GetId()).To(Equal("resize-gpu-compatible"))

				response, err = server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: id,
						Spec: privatev1.ComputeInstanceSpec_builder{
							InstanceType: privatev1.InstanceTypeReference_builder{Id: "resize-gpu-incompatible"}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.instance_type"}},
				}.Build())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			})

			It("Treats the current OBSOLETE InstanceType as a no-op", func() {
				createInstanceTypeWithSpec("resize-current", privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED, 4, 16, nil, nil)
				id := createComputeInstanceWithType("resize-current")
				setInstanceTypeState("resize-current", privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE)

				response, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: id,
						Spec: privatev1.ComputeInstanceSpec_builder{
							InstanceType: privatev1.InstanceTypeReference_builder{Id: "resize-current"}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.instance_type"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetWarnings()).To(BeEmpty())
				Expect(response.GetObject().GetSpec().GetInstanceType().GetId()).To(Equal("resize-current"))
			})

			It("Rejects a stale optimistic-lock no-op update", func() {
				createInstanceTypeWithSpec("resize-current", privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE, 4, 16, nil, nil)
				id := createComputeInstanceWithType("resize-current")
				getResponse, err := server.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build())
				Expect(err).ToNot(HaveOccurred())

				staleVersion := getResponse.GetObject().GetMetadata().GetVersion() - 1
				response, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id:       id,
						Metadata: privatev1.Metadata_builder{Version: staleVersion}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							InstanceType: privatev1.InstanceTypeReference_builder{Id: "resize-current"}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.instance_type"}},
					Lock:       true,
				}.Build())
				Expect(response).To(BeNil())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Aborted))
			})

			It("Does not treat a parent spec mask as an InstanceType-only no-op", func() {
				createInstanceTypeWithSpec("resize-current", privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE, 4, 16, nil, nil)
				id := createComputeInstanceWithType("resize-current")
				getResponse, err := server.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build())
				Expect(err).ToNot(HaveOccurred())
				spec := getResponse.GetObject().GetSpec()
				spec.SetRunStrategy(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED)

				response, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object:     privatev1.ComputeInstance_builder{Id: id, Spec: spec}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetSpec().GetRunStrategy()).To(Equal(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED))
			})
		})

		Context("disk_image validation", func() {
			createRequestWithDiskImage := func(diskImageKey string) *privatev1.ComputeInstancesCreateRequest {
				templatesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceTemplate]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				templateID := fmt.Sprintf("bare-template-di-%s", diskImageKey)
				_, err = templatesDao.Create().SetObject(
					privatev1.ComputeInstanceTemplate_builder{
						Id:          templateID,
						Title:       "Bare Template",
						Description: "Template without defaults",
						Metadata: privatev1.Metadata_builder{
							Name:   templateID,
							Tenant: testTenant,
						}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				return privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template:     privatev1.ComputeInstanceTemplateReference_builder{Id: templateID}.Build(),
							InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
							DiskImage:    &privatev1.DiskImageReference{Id: diskImageKey},
							BootDisk: privatev1.ComputeInstanceDisk_builder{
								SizeGib:     proto.Int32(20),
								StorageTier: privatev1.StorageTierReference_builder{Name: "standard"}.Build(),
							}.Build(),
							RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build()
			}

			It("Rejects creation when disk_image references a non-existent image", func() {
				request := createRequestWithDiskImage("nonexistent-disk-image")
				response, err := server.Create(ctx, request)
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.NotFound))
				Expect(status.Message()).To(ContainSubstring("nonexistent-disk-image"))
			})

			It("Rejects creation when disk_image references an OBSOLETE image", func() {
				createDiskImageWithLifecycle("obsolete-image",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE, nil)

				request := createRequestWithDiskImage("obsolete-image")
				response, err := server.Create(ctx, request)
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
				Expect(status.Message()).To(ContainSubstring("obsolete"))
			})

			It("Returns warning when disk_image references a DEPRECATED image", func() {
				createDiskImageWithLifecycle("deprecated-image",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED,
					privatev1.DiskImageDeprecation_builder{
						ObsolescenceTimestamp: timestamppb.New(
							time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)),
					}.Build())

				request := createRequestWithDiskImage("deprecated-image")
				response, err := server.Create(ctx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				Expect(response.GetWarnings()).To(HaveLen(1))
				Expect(response.GetWarnings()[0]).To(ContainSubstring("deprecated"))
				Expect(response.GetWarnings()[0]).To(ContainSubstring("2027"))
			})

			It("Succeeds when disk_image references an AVAILABLE image", func() {
				createDiskImageWithLifecycle("available-image",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE, nil)

				request := createRequestWithDiskImage("available-image")
				response, err := server.Create(ctx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				Expect(response.GetWarnings()).To(BeEmpty())
			})

			It("Resolves disk_image by name and backfills id+name", func() {
				createDiskImageWithLifecycle("di-by-name",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE, nil)

				templatesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceTemplate]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				_, err = templatesDao.Create().SetObject(
					privatev1.ComputeInstanceTemplate_builder{
						Id:          "bare-template-di-by-name",
						Title:       "Bare Template",
						Description: "Template without defaults",
						Metadata: privatev1.Metadata_builder{
							Name:   "bare-template-di-by-name",
							Tenant: testTenant,
						}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				request := privatev1.ComputeInstancesCreateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template:     privatev1.ComputeInstanceTemplateReference_builder{Id: "bare-template-di-by-name"}.Build(),
							InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
							DiskImage:    &privatev1.DiskImageReference{Name: "di-by-name"},
							BootDisk: privatev1.ComputeInstanceDisk_builder{
								SizeGib:     proto.Int32(20),
								StorageTier: privatev1.StorageTierReference_builder{Name: "standard"}.Build(),
							}.Build(),
							RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
							NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
								privatev1.ComputeNetworkAttachment_builder{
									Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build()

				response, err := server.Create(ctx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				diskImageRef := response.GetObject().GetSpec().GetDiskImage()
				Expect(diskImageRef.GetId()).ToNot(BeEmpty())
				Expect(diskImageRef.GetName()).To(Equal("di-by-name"))
			})
		})
	})

	Context("Auto external IP attachment", func() {
		var server *PrivateComputeInstancesServer
		var externalIPPoolDao *dao.GenericDAO[*privatev1.ExternalIPPool]
		var externalIPDao *dao.GenericDAO[*privatev1.ExternalIP]
		var externalIPAttachmentDao *dao.GenericDAO[*privatev1.ExternalIPAttachment]

		BeforeEach(func() {
			var err error

			server, err = NewPrivateComputeInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create template
			templatesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceTemplate]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			cpuDefault, err := anypb.New(wrapperspb.Int32(1))
			Expect(err).ToNot(HaveOccurred())
			memoryDefault, err := anypb.New(wrapperspb.Int32(2))
			Expect(err).ToNot(HaveOccurred())

			_, err = templatesDao.Create().SetObject(
				privatev1.ComputeInstanceTemplate_builder{
					Id:    "auto-eip-template",
					Title: "Auto EIP Template",
					Metadata: privatev1.Metadata_builder{
						Tenant: testTenant,
					}.Build(),
					Parameters: []*privatev1.ComputeInstanceTemplateParameterDefinition{
						{
							Name:    "cpu_count",
							Title:   "CPU",
							Type:    "type.googleapis.com/google.protobuf.Int32Value",
							Default: cpuDefault,
						},
						{
							Name:    "memory_gb",
							Title:   "Memory",
							Type:    "type.googleapis.com/google.protobuf.Int32Value",
							Default: memoryDefault,
						},
					},
					SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
						InstanceType: privatev1.InstanceTypeReference_builder{Id: "auto-eip-it"}.Build(),
						DiskImage:    &privatev1.DiskImageReference{Id: "test-disk-image"},
						BootDisk: privatev1.ComputeInstanceDisk_builder{
							SizeGib:     proto.Int32(10),
							StorageTier: privatev1.StorageTierReference_builder{Name: "standard"}.Build(),
						}.Build(),
						RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create storage tier
			storageTiersDao, err := dao.NewGenericDAO[*privatev1.StorageTier]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = storageTiersDao.Create().SetObject(
				privatev1.StorageTier_builder{
					Id: "standard",
					Metadata: privatev1.Metadata_builder{
						Name:   "standard",
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.StorageTierSpec_builder{
						Description: "Standard storage tier",
					}.Build(),
					Status: privatev1.StorageTierStatus_builder{
						State: privatev1.StorageTierState_STORAGE_TIER_STATE_ACTIVE,
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create instance type
			instanceTypesDao, err := dao.NewGenericDAO[*privatev1.InstanceType]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = instanceTypesDao.Create().SetObject(
				privatev1.InstanceType_builder{
					Id: "auto-eip-it",
					Metadata: privatev1.Metadata_builder{
						Name:   "auto-eip-it",
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Vcpus:     4,
						MemoryGib: 16,
						State:     privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create DAOs for verification
			externalIPPoolDao, err = dao.NewGenericDAO[*privatev1.ExternalIPPool]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			externalIPDao, err = dao.NewGenericDAO[*privatev1.ExternalIP]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			externalIPAttachmentDao, err = dao.NewGenericDAO[*privatev1.ExternalIPAttachment]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		createPool := func(id string, available int64) {
			_, err := externalIPPoolDao.Create().SetObject(
				privatev1.ExternalIPPool_builder{
					Id: id,
					Metadata: privatev1.Metadata_builder{
						Tenant: auth.SharedTenant,
					}.Build(),
					Status: privatev1.ExternalIPPoolStatus_builder{
						State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
						Available: available,
						Allocated: 0,
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
		}

		createRequest := func(autoEIP bool) *privatev1.ComputeInstancesCreateRequest {
			return privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: testTenant,
						Name:   "auto-eip-test",
					}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "auto-eip-template"}.Build(),
						NetworkAttachments: []*privatev1.ComputeNetworkAttachment{
							privatev1.ComputeNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
						AutoExternalIpAttachment: proto.Bool(autoEIP),
					}.Build(),
					Status: privatev1.ComputeInstanceStatus_builder{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
					}.Build(),
				}.Build(),
			}.Build()
		}

		It("Auto-provisions ExternalIP and ExternalIPAttachment on create", func() {
			createPool("pool-1", 5)

			response, err := server.Create(ctx, createRequest(true))
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())

			ciID := response.GetObject().GetId()

			// Verify ExternalIP was created
			eipList, err := externalIPDao.List().
				SetFilter(fmt.Sprintf("this.metadata.labels['%s'] == '%s'", autoCreatedForLabel, ciID)).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(eipList.GetItems()).To(HaveLen(1))

			eip := eipList.GetItems()[0]
			Expect(eip.GetMetadata().GetLabels()[autoCreatedLabel]).To(Equal("true"))
			Expect(eip.GetSpec().GetPool().GetId()).To(Equal("pool-1"))
			Expect(eip.GetStatus().GetAttached()).To(BeFalse())

			// Verify ExternalIPAttachment was created
			eiaList, err := externalIPAttachmentDao.List().
				SetFilter(fmt.Sprintf("this.metadata.labels['%s'] == '%s'", autoCreatedForLabel, ciID)).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(eiaList.GetItems()).To(HaveLen(1))

			eia := eiaList.GetItems()[0]
			Expect(eia.GetMetadata().GetLabels()[autoCreatedLabel]).To(Equal("true"))
			Expect(eia.GetSpec().GetExternalIp().GetId()).To(Equal(eip.GetId()))
			Expect(eia.GetSpec().GetComputeInstance().GetId()).To(Equal(ciID))

			// Verify pool capacity was updated
			poolResp, err := externalIPPoolDao.Get().SetId("pool-1").Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(poolResp.GetObject().GetStatus().GetAvailable()).To(Equal(int64(4)))
			Expect(poolResp.GetObject().GetStatus().GetAllocated()).To(Equal(int64(1)))

		})

		It("Does not auto-provision when auto_external_ip_attachment is false", func() {
			createPool("pool-1", 5)

			response, err := server.Create(ctx, createRequest(false))
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())

			// Verify no ExternalIP was created
			eipList, err := externalIPDao.List().Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(eipList.GetItems()).To(BeEmpty())

			// Verify pool capacity was not changed
			poolResp, err := externalIPPoolDao.Get().SetId("pool-1").Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(poolResp.GetObject().GetStatus().GetAvailable()).To(Equal(int64(5)))
		})

		It("Fails with FailedPrecondition when no pool has capacity", func() {
			createPool("empty-pool", 0)

			_, err := server.Create(ctx, createRequest(true))
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
		})

		It("Fails with FailedPrecondition when no pool exists", func() {
			_, err := server.Create(ctx, createRequest(true))
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
		})

		It("Cascade-deletes auto-created resources on ComputeInstance delete", func() {
			createPool("pool-1", 5)

			// Create CI with auto EIP
			response, err := server.Create(ctx, createRequest(true))
			Expect(err).ToNot(HaveOccurred())
			ciID := response.GetObject().GetId()

			// Delete the CI
			_, err = server.Delete(ctx, privatev1.ComputeInstancesDeleteRequest_builder{
				Id: ciID,
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Verify ExternalIPAttachment was deleted
			eiaList, err := externalIPAttachmentDao.List().Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(eiaList.GetItems()).To(BeEmpty())

			// Verify ExternalIP was deleted
			eipList, err := externalIPDao.List().Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(eipList.GetItems()).To(BeEmpty())

			// Verify pool capacity was restored
			poolResp, err := externalIPPoolDao.Get().SetId("pool-1").Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(poolResp.GetObject().GetStatus().GetAvailable()).To(Equal(int64(5)))
			Expect(poolResp.GetObject().GetStatus().GetAllocated()).To(Equal(int64(0)))

		})

		It("Rejects update to auto_external_ip_attachment", func() {
			createPool("pool-1", 5)

			response, err := server.Create(ctx, createRequest(true))
			Expect(err).ToNot(HaveOccurred())
			ciID := response.GetObject().GetId()

			_, err = server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id: ciID,
					Spec: privatev1.ComputeInstanceSpec_builder{
						AutoExternalIpAttachment: proto.Bool(false),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.auto_external_ip_attachment"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("auto_external_ip_attachment"))
		})
	})
})

var _ = Describe("Catalog materialized defaults", func() {
	It("selects a default subnet only from the destination project", func() {
		projects, err := dao.NewGenericDAO[*privatev1.Project]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		subnets, err := dao.NewGenericDAO[*privatev1.Subnet]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		for _, name := range []string{"destination", "other"} {
			_, err := projects.Create().SetObject(privatev1.Project_builder{Id: name, Metadata: privatev1.Metadata_builder{Name: name, Tenant: testTenant}.Build()}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = subnets.Create().SetObject(privatev1.Subnet_builder{Id: name, Metadata: privatev1.Metadata_builder{Name: name, Tenant: testTenant, Project: name, Labels: map[string]string{defaultLabel: "true"}}.Build(), Spec: privatev1.SubnetSpec_builder{Ipv4Cidr: new("10.0.0.0/24")}.Build(), Status: privatev1.SubnetStatus_builder{State: privatev1.SubnetState_SUBNET_STATE_READY}.Build()}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
		}
		subnet, err := findDefaultSubnet(ctx, logger, subnets, testTenant, "destination")
		Expect(err).ToNot(HaveOccurred())
		Expect(subnet.GetId()).To(Equal("destination"))
	})
	DescribeTable("rechecks catalog disk tiers after authoring", func(additional bool) {
		server, err := NewPrivateComputeInstancesServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		result, err := server.storageTiersDao.Create().SetObject(privatev1.StorageTier_builder{Metadata: privatev1.Metadata_builder{Name: "tier", Tenant: auth.SharedTenant}.Build(), Status: privatev1.StorageTierStatus_builder{State: privatev1.StorageTierState_STORAGE_TIER_STATE_ACTIVE}.Build()}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		tier := result.GetObject()
		instance := privatev1.ComputeInstance_builder{Spec: &privatev1.ComputeInstanceSpec{}}.Build()
		disk := privatev1.ComputeInstanceDisk_builder{StorageTier: privatev1.StorageTierReference_builder{Id: tier.GetId()}.Build()}.Build()
		if additional {
			instance.GetSpec().SetAdditionalDisks([]*privatev1.ComputeInstanceDisk{disk})
		} else {
			instance.GetSpec().SetBootDisk(disk)
		}
		Expect(server.validateStorageTiers(ctx, instance)).To(Succeed())
		tier.GetStatus().SetState(privatev1.StorageTierState_STORAGE_TIER_STATE_UNSPECIFIED)
		_, err = server.storageTiersDao.Update().SetObject(tier).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(server.validateStorageTiers(ctx, instance)).ToNot(Succeed())
		tier.GetStatus().SetState(privatev1.StorageTierState_STORAGE_TIER_STATE_ACTIVE)
		_, err = server.storageTiersDao.Update().SetObject(tier).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	},
		Entry("boot disk", false),
		Entry("additional disk", true),
	)
})
