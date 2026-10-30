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
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
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
			k8sManager := "test-k8s-manager"
			networkClassesDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = networkClassesDao.Create().SetObject(privatev1.NetworkClass_builder{
				Id:         "test-network-class",
				K8SManager: &k8sManager,
				Metadata: privatev1.Metadata_builder{
					Name:   "test-network-class",
					Tenant: testTenant,
				}.Build(),
				Status: privatev1.NetworkClassStatus_builder{
					State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
					Hub:   "network-hub-a",
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

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
					Labels: map[string]string{defaultLabel: "true"},
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: "test-network-class"}.Build(),
				}.Build(),
				Status: privatev1.VirtualNetworkStatus_builder{
					State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
					Hub:   "network-hub-a",
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
					Name:   "test-subnet",
					Tenant: testTenant,
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "test-vnet"}.Build(),
					Ipv4Cidr:       new("10.0.0.0/24"),
				}.Build(),
				Status: privatev1.SubnetStatus_builder{
					State: privatev1.SubnetState_SUBNET_STATE_READY,
					Hub:   "network-hub-a",
				}.Build(),
			}.Build()

			_, err = subnetsDao.Create().SetObject(subnet).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			securityGroupsDao, err := dao.NewGenericDAO[*privatev1.SecurityGroup]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = securityGroupsDao.Create().SetObject(privatev1.SecurityGroup_builder{
				Id: "test-sg-default",
				Metadata: privatev1.Metadata_builder{
					Name:   "test-sg-default",
					Tenant: testTenant,
					Labels: map[string]string{defaultLabel: "true"},
				}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "test-vnet"}.Build(),
				}.Build(),
				Status: privatev1.SecurityGroupStatus_builder{
					State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
					Hub:   "network-hub-a",
				}.Build(),
			}.Build()).Do(ctx)
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
						Tenant: "shared",
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
						SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:     "quay.io/containerdisks/fedora:41",
						GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
						Lifecycle:     privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE,
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
						},
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
					DiskImage:    privatev1.DiskImageReference_builder{Id: "test-disk-image"}.Build(),
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
					Metadata: publicv1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template:           publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						TemplateParameters: templateParams,
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
					Status: publicv1.ComputeInstanceStatus_builder{
						State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
						Volumes: []*publicv1.ComputeInstanceVolume{
							publicv1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "volume-spoofed"}.Build(),
						},
						ComputeNetworkAttachmentStatuses: []*publicv1.ComputeNetworkAttachmentStatus{
							publicv1.ComputeNetworkAttachmentStatus_builder{
								SubnetRef: "spoofed-subnet",
								IpAddress: "192.0.2.99",
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
			Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("general.small"))
			// The status is output-only: a caller-supplied state and attachment
			// statuses are dropped instead of persisted.
			Expect(object.GetStatus().GetState()).To(Equal(publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED))
			Expect(object.GetStatus().GetComputeNetworkAttachmentStatuses()).To(BeEmpty())
			Expect(object.GetStatus().GetVolumes()).To(BeEmpty())
		})

		It("List objects", func() {
			// Create templates and objects:
			const count = 10
			for i := range count {
				templateID := fmt.Sprintf("template-%d", i)
				createTemplate(templateID)

				_, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
					Object: publicv1.ComputeInstance_builder{
						Metadata: publicv1.Metadata_builder{
							Name: fmt.Sprintf("test-ci-%d", i),
						}.Build(),
						Spec: publicv1.ComputeInstanceSpec_builder{
							Template: publicv1.ComputeInstanceTemplateReference_builder{Id: templateID}.Build(),
							NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
								publicv1.ComputeNetworkAttachment_builder{
									Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
						Metadata: publicv1.Metadata_builder{
							Name: fmt.Sprintf("test-ci-limit-%d", i),
						}.Build(),
						Spec: publicv1.ComputeInstanceSpec_builder{
							Template: publicv1.ComputeInstanceTemplateReference_builder{Id: templateID}.Build(),
							NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
								publicv1.ComputeNetworkAttachment_builder{
									Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
						Metadata: publicv1.Metadata_builder{
							Name: fmt.Sprintf("test-ci-offset-%d", i),
						}.Build(),
						Spec: publicv1.ComputeInstanceSpec_builder{
							Template: publicv1.ComputeInstanceTemplateReference_builder{Id: templateID}.Build(),
							NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
								publicv1.ComputeNetworkAttachment_builder{
									Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
					Metadata: publicv1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
			Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("general.small"))
			// No status was supplied on create and the system has not written one yet:
			Expect(object.GetStatus().GetState()).To(Equal(publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED))
		})

		It("Updates object", func() {
			// Create templates first
			createTemplate("general.small")

			// Create an object with explicit fields:
			createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template:     publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						InstanceType: publicv1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
						RunStrategy:  publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
						DiskImage:    publicv1.DiskImageReference_builder{Id: "test-disk-image"}.Build(),
						BootDisk: publicv1.ComputeInstanceDisk_builder{
							SizeGib:     proto.Int32(20),
							StorageTier: publicv1.StorageTierReference_builder{Name: "standard"}.Build(),
						}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
			restartTime := timestamppb.New(time.Now().Truncate(time.Second))
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
			Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("general.small"))
			Expect(object.GetSpec().GetInstanceType().GetId()).To(Equal("standard-4-16"))
			Expect(object.GetSpec().GetRunStrategy()).To(Equal(publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS))
			Expect(object.GetSpec().GetDiskImage().GetId()).To(Equal("test-disk-image"))
			Expect(object.GetSpec().GetBootDisk().GetSizeGib()).To(BeNumerically("==", 20))

			// Verify they survive a round-trip through the database:
			getResponse, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			fetched := getResponse.GetObject()
			Expect(fetched.GetSpec().GetInstanceType().GetId()).To(Equal("standard-4-16"))
			Expect(fetched.GetSpec().GetRunStrategy()).To(Equal(publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS))
			Expect(fetched.GetSpec().GetDiskImage().GetId()).To(Equal("test-disk-image"))
			Expect(fetched.GetSpec().GetBootDisk().GetSizeGib()).To(BeNumerically("==", 20))
			Expect(fetched.GetSpec().GetRestartRequestedAt()).ToNot(BeNil())
		})

		It("Keeps the stored status across a maskless update carrying a status", func() {
			createTemplate("general.small")

			// Create an object:
			createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template:     publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						InstanceType: publicv1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
						RunStrategy:  publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
						DiskImage:    publicv1.DiskImageReference_builder{Id: "test-disk-image"}.Build(),
						BootDisk: publicv1.ComputeInstanceDisk_builder{
							SizeGib:     proto.Int32(20),
							StorageTier: publicv1.StorageTierReference_builder{Name: "standard"}.Build(),
						}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			// Store a state with its transition time, as the operator feedback would. The
			// time must be after creation (the source contract clamps a first time on or
			// before it) and truncated to whole seconds: the contract stores transition
			// times at second granularity, so a sub-second seed would not round-trip.
			storedTime := timestamppb.New(time.Now().Truncate(time.Second))
			ciDao, err := dao.NewGenericDAO[*privatev1.ComputeInstance]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			stored, err := ciDao.Get().SetId(id).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			storedObject := stored.GetObject()
			storedObject.SetStatus(privatev1.ComputeInstanceStatus_builder{
				State:               privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
				StateTransitionTime: storedTime,
				Volumes: []*privatev1.ComputeInstanceVolume{
					privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "volume-stored"}.Build(),
				},
				VolumeRegistration: []*privatev1.ComputeInstanceVolumeRegistration{
					privatev1.ComputeInstanceVolumeRegistration_builder{
						DiskKey: "boot",
						State:   privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED,
					}.Build(),
				},
			}.Build())
			_, err = ciDao.Update().SetObject(storedObject).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Round-trip the public object without a mask, carrying a fabricated state
			// with a later transition time:
			getResponse, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			tampered := getResponse.GetObject()
			tampered.GetStatus().SetState(publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			tampered.GetStatus().SetStateTransitionTime(timestamppb.New(time.Now().Truncate(time.Second)))
			Expect(tampered.GetStatus().GetVolumes()).To(HaveLen(1))
			tampered.GetStatus().GetVolumes()[0].SetVolumeId("volume-forged")
			restartTime := timestamppb.Now()
			tampered.GetSpec().SetRestartRequestedAt(restartTime)
			updateResponse, err := server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object: tampered,
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// The stored status and its transition time are unchanged:
			Expect(updateResponse.GetObject().GetStatus().GetState()).To(
				Equal(publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING))
			Expect(updateResponse.GetObject().GetStatus().GetStateTransitionTime().AsTime()).To(
				Equal(storedTime.AsTime()))
			Expect(updateResponse.GetObject().GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal("volume-stored"))
			Expect(updateResponse.GetObject().GetSpec().GetRestartRequestedAt().AsTime()).To(
				BeTemporally("~", restartTime.AsTime()))

			// The public correlation and private registration state also survive persistence:
			after, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(after.GetObject().GetStatus().GetState()).To(
				Equal(publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING))
			Expect(after.GetObject().GetStatus().GetStateTransitionTime().AsTime()).To(
				Equal(storedTime.AsTime()))
			Expect(after.GetObject().GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal("volume-stored"))
			Expect(after.GetObject().GetSpec().GetRestartRequestedAt().AsTime()).To(
				BeTemporally("~", restartTime.AsTime()))
			persisted, err := ciDao.Get().SetId(id).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(persisted.GetObject().GetStatus().GetVolumeRegistration()[0].GetState()).To(
				Equal(privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED))
		})

		It("projects disk correlations without private registration state in responses and logs", func() {
			createTemplate("public-disk-projection-template")
			created, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: "public-disk-projection"}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "public-disk-projection-template"}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			ciDao, err := dao.NewGenericDAO[*privatev1.ComputeInstance]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			stored, err := ciDao.Get().SetId(created.GetObject().GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			storedObject := stored.GetObject()
			condition := func(reason, message string) *privatev1.ComputeInstanceCondition {
				return privatev1.ComputeInstanceCondition_builder{
					Type:   privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED,
					Status: privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
					Reason: proto.String(reason), Message: proto.String(message),
				}.Build()
			}
			storedObject.SetStatus(privatev1.ComputeInstanceStatus_builder{
				Conditions: []*privatev1.ComputeInstanceCondition{
					condition("VolumeRegistrationPending", "Disk Volume IDs persisted: 0/1; blocked disks: boot; registration pending."),
				},
			}.Build())
			_, err = ciDao.Update().SetObject(storedObject).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			pendingGet, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: created.GetObject().GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(pendingGet.GetObject().GetStatus().GetVolumes()).To(BeEmpty())
			Expect(pendingGet.GetObject().GetStatus().GetConditions()).To(ContainElement(WithTransform(
				func(item *publicv1.ComputeInstanceCondition) string { return item.GetReason() },
				Equal("VolumeRegistrationPending"))))
			pendingList, err := server.List(ctx, publicv1.ComputeInstancesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(pendingList.GetItems()).To(ContainElement(WithTransform(
				func(item *publicv1.ComputeInstance) string {
					if item.GetId() != created.GetObject().GetId() {
						return ""
					}
					return item.GetStatus().GetConditions()[0].GetReason()
				}, Equal("VolumeRegistrationPending"))))
			stored, err = ciDao.Get().SetId(created.GetObject().GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			storedObject = stored.GetObject()
			storedObject.SetStatus(privatev1.ComputeInstanceStatus_builder{
				Volumes: []*privatev1.ComputeInstanceVolume{
					privatev1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "volume-public"}.Build(),
				},
				VolumeRegistration: []*privatev1.ComputeInstanceVolumeRegistration{
					privatev1.ComputeInstanceVolumeRegistration_builder{
						DiskKey: "boot",
						State:   privatev1.ComputeInstanceVolumeRegistration_STATE_RESOLVED,
					}.Build(),
				},
				Conditions: []*privatev1.ComputeInstanceCondition{
					condition("WaitingForVM", "All disk Volume IDs are registered; waiting for the VM."),
				},
			}.Build())
			_, err = ciDao.Update().SetObject(storedObject).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			getRequest := publicv1.ComputeInstancesGetRequest_builder{Id: created.GetObject().GetId()}.Build()
			getResponse, err := server.Get(ctx, getRequest)
			Expect(err).ToNot(HaveOccurred())
			publicJSON, err := protojson.Marshal(getResponse.GetObject())
			Expect(err).ToNot(HaveOccurred())
			Expect(string(publicJSON)).To(ContainSubstring(`"volumeId":"volume-public"`))
			Expect(string(publicJSON)).ToNot(ContainSubstring("volumeRegistration"))
			Expect(string(publicJSON)).ToNot(ContainSubstring("STATE_RESOLVED"))

			ownerGet, err := server.Get(ctx, getRequest)
			Expect(err).ToNot(HaveOccurred())
			Expect(ownerGet.GetObject().GetStatus().GetVolumes()).To(HaveLen(1))
			Expect(ownerGet.GetObject().GetStatus().GetVolumes()[0].GetDiskKey()).To(Equal("boot"))
			Expect(ownerGet.GetObject().GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal("volume-public"))
			Expect(ownerGet.GetObject().GetStatus().GetConditions()[0].GetReason()).To(Equal("WaitingForVM"))
			ownerList, err := server.List(ctx, publicv1.ComputeInstancesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			var listedInstance *publicv1.ComputeInstance
			for _, item := range ownerList.GetItems() {
				if item.GetId() == created.GetObject().GetId() {
					listedInstance = item
					break
				}
			}
			Expect(listedInstance).ToNot(BeNil())
			Expect(listedInstance.GetStatus().GetVolumes()).To(HaveLen(1))
			Expect(listedInstance.GetStatus().GetVolumes()[0].GetDiskKey()).To(Equal("boot"))
			Expect(listedInstance.GetStatus().GetVolumes()[0].GetVolumeId()).To(Equal("volume-public"))
			Expect(listedInstance.GetStatus().GetConditions()[0].GetReason()).To(Equal("WaitingForVM"))

			// A same-tenant caller with access to a different project cannot see this instance.
			projectName := "hidden-disk-project"
			projectsDao, err := dao.NewGenericDAO[*privatev1.Project]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = projectsDao.Create().SetObject(privatev1.Project_builder{
				Id: projectName,
				Metadata: privatev1.Metadata_builder{
					Name:   projectName,
					Tenant: testTenant,
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			projectInstance, err := ciDao.Create().SetObject(privatev1.ComputeInstance_builder{
				Metadata: privatev1.Metadata_builder{
					Name:    "project-scoped-disk-instance",
					Tenant:  testTenant,
					Project: projectName,
				}.Build(),
				Spec: privatev1.ComputeInstanceSpec_builder{
					Template: privatev1.ComputeInstanceTemplateReference_builder{
						Id: "public-disk-projection-template",
					}.Build(),
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			foreignVisibility, err := auth.NewVisibility().
				AddVisibleTenant("other-tenant").
				SetTotalProjectVisibility("other-tenant").
				Build()
			Expect(err).ToNot(HaveOccurred())
			foreignTenancy := auth.NewMockTenancyLogic(ctrl)
			foreignTenancy.EXPECT().DetermineVisibility(gomock.Any()).Return(foreignVisibility, nil).AnyTimes()
			foreignServer, err := NewComputeInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(foreignTenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = foreignServer.Get(ctx, getRequest)
			Expect(status.Code(err)).To(Equal(codes.NotFound))
			foreignList, err := foreignServer.List(ctx, publicv1.ComputeInstancesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(foreignList.GetItems()).To(BeEmpty())

			projectVisibility, err := auth.NewVisibility().
				AddVisibleProject(testTenant, "another-project").
				Build()
			Expect(err).ToNot(HaveOccurred())
			projectTenancy := auth.NewMockTenancyLogic(ctrl)
			projectTenancy.EXPECT().DetermineVisibility(gomock.Any()).Return(projectVisibility, nil).AnyTimes()
			projectServer, err := NewComputeInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(projectTenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			projectGetRequest := publicv1.ComputeInstancesGetRequest_builder{Id: projectInstance.GetObject().GetId()}.Build()
			_, err = projectServer.Get(ctx, projectGetRequest)
			Expect(status.Code(err)).To(Equal(codes.NotFound))
			projectList, err := projectServer.List(ctx, publicv1.ComputeInstancesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(projectList.GetItems()).To(ContainElement(HaveField("Id", created.GetObject().GetId())))
			Expect(projectList.GetItems()).ToNot(ContainElement(HaveField("Id", projectInstance.GetObject().GetId())))

			var logOutput bytes.Buffer
			debugLogger := slog.New(slog.NewTextHandler(&logOutput, &slog.HandlerOptions{Level: slog.LevelDebug}))
			interceptor, err := logging.NewInterceptor().SetLogger(debugLogger).SetBodies(true).Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = interceptor.UnaryServer(ctx, getRequest, &grpc.UnaryServerInfo{
				FullMethod: "/osac.public.v1.ComputeInstances/Get",
			}, func(callCtx context.Context, request any) (any, error) {
				return server.Get(callCtx, request.(*publicv1.ComputeInstancesGetRequest))
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(logOutput.String()).To(ContainSubstring("volume-public"))
			Expect(logOutput.String()).ToNot(ContainSubstring("volumeRegistration"))
			Expect(logOutput.String()).ToNot(ContainSubstring("STATE_RESOLVED"))
		})

		DescribeTable("Rejects every public status mask path",
			func(path, message string) {
				createTemplate("general.small")

				// Create an object:
				createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
					Object: publicv1.ComputeInstance_builder{
						Metadata: publicv1.Metadata_builder{
							Name: "test-compute-instance",
						}.Build(),
						Spec: publicv1.ComputeInstanceSpec_builder{
							Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
							NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
								publicv1.ComputeNetworkAttachment_builder{
									Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				id := createResponse.GetObject().GetId()

				// Store a state with its transition time and attachment feedback, as the
				// operator feedback would. The time must be after creation (the source
				// contract clamps a first time on or before it) and truncated to whole
				// seconds: the contract stores transition times at second granularity,
				// so a sub-second seed would not round-trip.
				storedTime := timestamppb.New(time.Now().Truncate(time.Second))
				ciDao, err := dao.NewGenericDAO[*privatev1.ComputeInstance]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				stored, err := ciDao.Get().SetId(id).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				storedObject := stored.GetObject()
				storedObject.SetStatus(privatev1.ComputeInstanceStatus_builder{
					State:               privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
					StateTransitionTime: storedTime,
					ComputeNetworkAttachmentStatuses: []*privatev1.ComputeNetworkAttachmentStatus{
						privatev1.ComputeNetworkAttachmentStatus_builder{
							SubnetRef: "test-subnet",
							IpAddress: "192.0.2.10",
						}.Build(),
					},
				}.Build())
				_, err = ciDao.Update().SetObject(storedObject).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// A public update whose mask selects a status path is rejected, even though
				// the object carries a fabricated state with a later transition time:
				updateResponse, err := server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
					Object: publicv1.ComputeInstance_builder{
						Id: id,
						Status: publicv1.ComputeInstanceStatus_builder{
							State:               publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
							StateTransitionTime: timestamppb.New(time.Now().Truncate(time.Second)),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{path},
					},
				}.Build())
				Expect(updateResponse).To(BeNil())
				grpcStatus, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(grpcStatus.Code()).To(Equal(codes.InvalidArgument))
				Expect(grpcStatus.Message()).To(ContainSubstring(message))

				// The stored status, its transition time, and the attachment feedback are
				// unchanged:
				after, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
					Id: id,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				statusObject := after.GetObject().GetStatus()
				Expect(statusObject.GetState()).To(
					Equal(publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING))
				Expect(statusObject.GetStateTransitionTime().AsTime()).To(Equal(storedTime.AsTime()))
				Expect(statusObject.GetComputeNetworkAttachmentStatuses()).To(HaveLen(1))
				Expect(statusObject.GetComputeNetworkAttachmentStatuses()[0].GetSubnetRef()).To(Equal("test-subnet"))
				Expect(statusObject.GetComputeNetworkAttachmentStatuses()[0].GetIpAddress()).To(Equal("192.0.2.10"))
			},
			Entry("Rejects the whole status", "status", "status is output-only"),
			Entry("Rejects the state", "status.state", "status.state is output-only"),
			Entry("Rejects the state transition time", "status.state_transition_time", "status.state_transition_time is output-only"),
			Entry("Rejects the conditions", "status.conditions", "status.conditions is output-only"),
			Entry("Rejects the running instance type", "status.instance_type", "status.instance_type is output-only"),
			Entry("Rejects a running instance type subpath", "status.instance_type.name", "status.instance_type.name is output-only"),
			Entry("Rejects the running instance type transition time", "status.instance_type_transition_time", "status.instance_type_transition_time is output-only"),
			Entry("Rejects a running instance type transition time subpath", "status.instance_type_transition_time.seconds", "status.instance_type_transition_time.seconds is output-only"),
			Entry("Rejects the attachment statuses", "status.compute_network_attachment_statuses", "status.compute_network_attachment_statuses is output-only"),
			Entry("Rejects an attachment status subpath", "status.compute_network_attachment_statuses.ip_address", "status.compute_network_attachment_statuses.ip_address is output-only"),
			Entry("Rejects a normalized whitespace and double-dot spelling", " status .. state ", "status.state is output-only"),
		)

		It("Keeps the stored status across a masked update echoing a status", func() {
			createTemplate("general.small")

			// Create an object:
			createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			// Store a state with its transition time, as the operator feedback would:
			storedTime := timestamppb.New(time.Now().Truncate(time.Second))
			ciDao, err := dao.NewGenericDAO[*privatev1.ComputeInstance]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			stored, err := ciDao.Get().SetId(id).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			storedObject := stored.GetObject()
			storedObject.SetStatus(privatev1.ComputeInstanceStatus_builder{
				State:               privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
				StateTransitionTime: storedTime,
			}.Build())
			_, err = ciDao.Update().SetObject(storedObject).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Update restart_requested_at with an explicit mask while the object echoes
			// the fetched status unchanged:
			getResponse, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			echoed := getResponse.GetObject()
			restartTime := timestamppb.New(time.Now().Truncate(time.Second))
			echoed.GetSpec().SetRestartRequestedAt(restartTime)
			updateResponse, err := server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object: echoed,
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.restart_requested_at"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetRestartRequestedAt().AsTime()).To(
				BeTemporally("~", restartTime.AsTime()))

			// The echoed status did not change the stored status or its transition time:
			after, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(after.GetObject().GetSpec().GetRestartRequestedAt()).ToNot(BeNil())
			Expect(after.GetObject().GetStatus().GetState()).To(
				Equal(publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING))
			Expect(after.GetObject().GetStatus().GetStateTransitionTime().AsTime()).To(
				Equal(storedTime.AsTime()))
		})

		It("Exposes private status feedback through the public API", func() {
			// Create the private server that controllers use:
			privateServer, err := NewPrivateComputeInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			createTemplate("general.small")

			// Create an object through the public API:
			createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			// Report a state transition and attachment feedback through the private API.
			// The time must be after creation: the source contract clamps a first time on
			// or before it.
			feedbackTime := timestamppb.New(time.Now().Truncate(time.Second))
			updateResponse, err := privateServer.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id: id,
					Status: privatev1.ComputeInstanceStatus_builder{
						State:               privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
						StateTransitionTime: feedbackTime,
						ComputeNetworkAttachmentStatuses: []*privatev1.ComputeNetworkAttachmentStatus{
							privatev1.ComputeNetworkAttachmentStatus_builder{
								SubnetRef: "test-subnet",
								IpAddress: "192.0.2.11",
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"status.state", "status.state_transition_time", "status.compute_network_attachment_statuses"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING))

			// The feedback is visible through the public API:
			getResponse, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			statusObject := getResponse.GetObject().GetStatus()
			Expect(statusObject.GetState()).To(
				Equal(publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING))
			Expect(statusObject.GetStateTransitionTime().AsTime()).To(Equal(feedbackTime.AsTime()))
			Expect(statusObject.GetComputeNetworkAttachmentStatuses()).To(HaveLen(1))
			Expect(statusObject.GetComputeNetworkAttachmentStatuses()[0].GetSubnetRef()).To(Equal("test-subnet"))
			Expect(statusObject.GetComputeNetworkAttachmentStatuses()[0].GetIpAddress()).To(Equal("192.0.2.11"))
		})

		It("Infers supplied writable fields from a maskless update", func() {
			createTemplate("general.small")

			// Create an object with a run strategy:
			createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template:    publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						RunStrategy: publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			// A maskless update echoing the fetched object with one changed field,
			// network attachments omitted, and the optional run strategy omitted:
			getResponse, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			echoed := getResponse.GetObject()
			restartTime := timestamppb.New(time.Now().Truncate(time.Second))
			echoed.GetSpec().SetRestartRequestedAt(restartTime)
			echoed.GetSpec().NetworkAttachments = nil
			echoed.GetSpec().ClearRunStrategy()
			updateResponse, err := server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object: echoed,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetRestartRequestedAt().AsTime()).To(
				BeTemporally("~", restartTime.AsTime()))

			// Omitted siblings and the omitted optional field remain intact:
			after, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(after.GetObject().GetSpec().GetNetworkAttachments()).To(HaveLen(1))
			Expect(after.GetObject().GetSpec().GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal("test-subnet"))
			Expect(after.GetObject().GetSpec().GetRunStrategy()).To(
				Equal(publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS))
		})

		It("Changes or clears only the selected field under an explicit mask", func() {
			createTemplate("general.small")

			createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			// An explicit mask changes only the selected field:
			restartTime := timestamppb.New(time.Now().Truncate(time.Second))
			updateResponse, err := server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Id: id,
					Spec: publicv1.ComputeInstanceSpec_builder{
						RestartRequestedAt: restartTime,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.restart_requested_at"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetRestartRequestedAt().AsTime()).To(
				BeTemporally("~", restartTime.AsTime()))

			// An explicit mask with an absent value clears only the selected field:
			updateResponse, err = server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Id: id,
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.restart_requested_at"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetRestartRequestedAt()).To(BeNil())

			// The untargeted sibling is intact after both updates:
			after, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(after.GetObject().GetSpec().GetNetworkAttachments()).To(HaveLen(1))
			Expect(after.GetObject().GetSpec().GetRestartRequestedAt()).To(BeNil())
		})

		It("Treats an explicit empty mask as a no-op", func() {
			createTemplate("general.small")

			createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			// Store a state with its transition time, as the operator feedback would:
			storedTime := timestamppb.New(time.Now().Truncate(time.Second))
			ciDao, err := dao.NewGenericDAO[*privatev1.ComputeInstance]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			stored, err := ciDao.Get().SetId(id).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			storedObject := stored.GetObject()
			storedObject.SetStatus(privatev1.ComputeInstanceStatus_builder{
				State:               privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
				StateTransitionTime: storedTime,
			}.Build())
			updated, err := ciDao.Update().SetObject(storedObject).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			storedVersion := updated.GetObject().GetMetadata().GetVersion()

			// An explicit empty mask changes nothing, even though the object omits
			// every field:
			updateResponse, err := server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object:     publicv1.ComputeInstance_builder{Id: id}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetMetadata().GetVersion()).To(Equal(storedVersion))
			Expect(updateResponse.GetObject().GetStatus().GetState()).To(
				Equal(publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING))
			Expect(updateResponse.GetObject().GetSpec().GetTemplate().GetId()).To(Equal("general.small"))
		})

		It("Rejects unknown mask paths without persistence", func() {
			createTemplate("general.small")

			createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			updateResponse, err := server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Id: id,
					Spec: publicv1.ComputeInstanceSpec_builder{
						RestartRequestedAt: timestamppb.New(time.Now().Truncate(time.Second)),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.nonexistent"}},
			}.Build())
			Expect(updateResponse).To(BeNil())
			Expect(err).To(HaveOccurred())

			after, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(after.GetObject().GetSpec().GetRestartRequestedAt()).To(BeNil())
		})

		It("Preserves private metadata through a whole metadata update", func() {
			createTemplate("general.small")

			createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			// Initialize private-only metadata through the DAO, as controllers do:
			instancesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstance]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			storedResponse, err := instancesDao.Get().SetId(id).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			stored := storedResponse.GetObject()
			stored.GetMetadata().SetFinalizers([]string{"example.finalizer/osac"})
			_, err = instancesDao.Update().SetObject(stored).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// A whole metadata update expands to the permitted child paths.
			// The renamed metadata name is not writable and is ignored:
			updateResponse, err := server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Id: id,
					Metadata: publicv1.Metadata_builder{
						Name:   "renamed-instance",
						Labels: map[string]string{"whole": "update"},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetMetadata().GetLabels()).To(Equal(map[string]string{
				"whole": "update",
			}))

			// Private finalizers and the system-owned creator survive, the
			// name is unchanged, and the tenant is set by the tenancy logic:
			storedResponse, err = instancesDao.Get().SetId(id).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(storedResponse.GetObject().GetMetadata().GetFinalizers()).To(Equal(
				[]string{"example.finalizer/osac"}))
			Expect(storedResponse.GetObject().GetMetadata().GetCreator()).To(Equal("system"))
			Expect(storedResponse.GetObject().GetMetadata().GetName()).To(Equal("test-compute-instance"))
			Expect(storedResponse.GetObject().GetMetadata().GetTenant()).To(Equal(testTenant))
			Expect(storedResponse.GetObject().GetMetadata().GetLabels()).To(Equal(map[string]string{
				"whole": "update",
			}))
		})

		DescribeTable("Rejects forbidden metadata mask paths",
			func(path string) {
				createTemplate("general.small")

				createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
					Object: publicv1.ComputeInstance_builder{
						Metadata: publicv1.Metadata_builder{
							Name: "test-compute-instance",
						}.Build(),
						Spec: publicv1.ComputeInstanceSpec_builder{
							Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
							NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
								publicv1.ComputeNetworkAttachment_builder{
									Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				id := createResponse.GetObject().GetId()

				updateResponse, err := server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
					Object: publicv1.ComputeInstance_builder{
						Id:       id,
						Metadata: publicv1.Metadata_builder{Name: "test-compute-instance"}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}},
				}.Build())
				Expect(updateResponse).To(BeNil())
				grpcStatus, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(grpcStatus.Code()).To(Equal(codes.InvalidArgument))
			},
			Entry("Rejects the metadata version", "metadata.version"),
			Entry("Rejects the creation timestamp", "metadata.creation_timestamp"),
			Entry("Rejects the deletion timestamp", "metadata.deletion_timestamp"),
			Entry("Rejects the creator", "metadata.creator"),
			Entry("Rejects private finalizers", "metadata.finalizers"),
			Entry("Rejects the metadata name", "metadata.name"),
			Entry("Rejects the metadata tenant", "metadata.tenant"),
			Entry("Rejects the metadata project", "metadata.project"),
		)

		It("Rejects a stale version without changing user fields or status", func() {
			createTemplate("general.small")

			createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			// Store a state with its transition time, as the operator feedback would:
			storedTime := timestamppb.New(time.Now().Truncate(time.Second))
			ciDao, err := dao.NewGenericDAO[*privatev1.ComputeInstance]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			stored, err := ciDao.Get().SetId(id).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			storedObject := stored.GetObject()
			storedObject.SetStatus(privatev1.ComputeInstanceStatus_builder{
				State:               privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
				StateTransitionTime: storedTime,
			}.Build())
			_, err = ciDao.Update().SetObject(storedObject).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// A lock=true update with a stale version is rejected:
			getResponse, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			stale := getResponse.GetObject()
			staleVersion := stale.GetMetadata().GetVersion()
			stale.GetSpec().SetRestartRequestedAt(timestamppb.New(time.Now().Truncate(time.Second)))
			stale.GetMetadata().SetVersion(staleVersion - 1)
			updateResponse, err := server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object: stale,
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.restart_requested_at"},
				},
				Lock: true,
			}.Build())
			Expect(updateResponse).To(BeNil())
			grpcStatus, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(grpcStatus.Code()).To(Equal(codes.Aborted))

			// Neither the user fields nor the current status changed:
			after, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(after.GetObject().GetSpec().GetRestartRequestedAt()).To(BeNil())
			Expect(after.GetObject().GetMetadata().GetVersion()).To(Equal(staleVersion))
			Expect(after.GetObject().GetStatus().GetState()).To(
				Equal(publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING))
			Expect(after.GetObject().GetStatus().GetStateTransitionTime().AsTime()).To(
				Equal(storedTime.AsTime()))
		})

		It("Preserves status, fields, and version through a no-change round trip", func() {
			createTemplate("general.small")

			createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			// Report a state transition and attachment feedback through the private API:
			feedbackTime := timestamppb.New(time.Now().Truncate(time.Second))
			privateServer, err := NewPrivateComputeInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = privateServer.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id: id,
					Status: privatev1.ComputeInstanceStatus_builder{
						State:               privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
						StateTransitionTime: feedbackTime,
						ComputeNetworkAttachmentStatuses: []*privatev1.ComputeNetworkAttachmentStatus{
							privatev1.ComputeNetworkAttachmentStatus_builder{
								SubnetRef: "test-subnet",
								IpAddress: "192.0.2.13",
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"status.state", "status.state_transition_time", "status.compute_network_attachment_statuses"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// A no-change maskless round trip preserves status, fields, and version:
			getResponse, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			version := getResponse.GetObject().GetMetadata().GetVersion()
			updateResponse, err := server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object: getResponse.GetObject(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetMetadata().GetVersion()).To(Equal(version))
			Expect(updateResponse.GetObject().GetStatus().GetState()).To(
				Equal(publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING))
			Expect(updateResponse.GetObject().GetStatus().GetStateTransitionTime().AsTime()).To(
				Equal(feedbackTime.AsTime()))
			Expect(updateResponse.GetObject().GetStatus().GetComputeNetworkAttachmentStatuses()).To(HaveLen(1))
			Expect(updateResponse.GetObject().GetStatus().GetComputeNetworkAttachmentStatuses()[0].GetIpAddress()).To(
				Equal("192.0.2.13"))
			Expect(updateResponse.GetObject().GetSpec().GetTemplate().GetId()).To(Equal("general.small"))

			after, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(after.GetObject().GetMetadata().GetVersion()).To(Equal(version))
			Expect(after.GetObject().GetStatus().GetState()).To(
				Equal(publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING))
		})

		It("Enforces template immutability through the public update path", func() {
			createTemplate("general.small")
			createTemplate("general.large")

			createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			// A maskless update that changes the immutable template is rejected:
			getResponse, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			moved := getResponse.GetObject()
			moved.GetSpec().SetTemplate(publicv1.ComputeInstanceTemplateReference_builder{Id: "general.large"}.Build())
			updateResponse, err := server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object: moved,
			}.Build())
			Expect(updateResponse).To(BeNil())
			grpcStatus, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(grpcStatus.Code()).To(Equal(codes.InvalidArgument))

			after, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(after.GetObject().GetSpec().GetTemplate().GetId()).To(Equal("general.small"))
		})

		DescribeTable("Preserves concurrent private feedback across a maskless public update",
			func(keepStatus bool) {
				createTemplate("general.small")

				// Create the private server that controllers use:
				privateServer, err := NewPrivateComputeInstancesServer().
					SetLogger(logger).
					SetAttributionLogic(attribution).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				// Create a valid instance through the public API:
				createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
					Object: publicv1.ComputeInstance_builder{
						Metadata: publicv1.Metadata_builder{
							Name: "test-compute-instance",
						}.Build(),
						Spec: publicv1.ComputeInstanceSpec_builder{
							Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
							NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
								publicv1.ComputeNetworkAttachment_builder{
									Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				id := createResponse.GetObject().GetId()

				// Set a valid initial system status via the private API. The time must
				// be after creation: the source contract clamps a first time on or
				// before it.
				initialTime := timestamppb.New(time.Now().Truncate(time.Second))
				_, err = privateServer.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: id,
						Status: privatev1.ComputeInstanceStatus_builder{
							State:               privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
							StateTransitionTime: initialTime,
							ComputeNetworkAttachmentStatuses: []*privatev1.ComputeNetworkAttachmentStatus{
								privatev1.ComputeNetworkAttachmentStatus_builder{
									SubnetRef: "test-subnet",
									IpAddress: "192.0.2.10",
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"status.state", "status.state_transition_time", "status.compute_network_attachment_statuses"},
					},
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// Fetch the public representation for the future public update. The
				// changed labels ensure the update actually persists a user edit:
				getResponse, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
					Id: id,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				publicObject := getResponse.GetObject()
				publicObject.GetMetadata().SetLabels(map[string]string{"updated": "by-public"})
				if !keepStatus {
					publicObject.ClearStatus()
				}

				// Commit the fixture so two independent database transactions can see it:
				Expect(suiteTx.End(ctx)).To(Succeed())
				suiteTx = nil

				var publicTx database.Tx
				var updateDone chan error
				writerTx, err := tm.Begin(context.Background())
				Expect(err).ToNot(HaveOccurred())
				writerCtx := database.TxIntoContext(context.Background(), writerTx)
				var writerPID int32
				err = writerTx.QueryRow(writerCtx, "select pg_backend_pid()").Scan(&writerPID)
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func() {
					if writerTx != nil {
						_ = writerTx.End(context.Background())
						writerTx = nil
					}
					if updateDone != nil {
						select {
						case <-updateDone:
						case <-time.After(10 * time.Second):
						}
					}
					if publicTx != nil {
						_ = publicTx.End(context.Background())
						publicTx = nil
					}
				})

				// Transition the state without committing, so the public update blocks
				// on the row lock:
				writerTime := timestamppb.New(time.Now().Truncate(time.Second))
				_, err = privateServer.Update(writerCtx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id: id,
						Status: privatev1.ComputeInstanceStatus_builder{
							State:               privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
							StateTransitionTime: writerTime,
							ComputeNetworkAttachmentStatuses: []*privatev1.ComputeNetworkAttachmentStatus{
								privatev1.ComputeNetworkAttachmentStatus_builder{
									SubnetRef: "test-subnet",
									IpAddress: "192.0.2.12",
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"status.state", "status.state_transition_time", "status.compute_network_attachment_statuses"},
					},
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				publicTx, err = tm.Begin(context.Background())
				Expect(err).ToNot(HaveOccurred())
				publicCtx, cancel := context.WithTimeout(
					database.TxIntoContext(context.Background(), publicTx),
					10*time.Second,
				)
				defer cancel()
				var publicResponse *publicv1.ComputeInstancesUpdateResponse
				updateDone = make(chan error, 1)
				go func() {
					response, err := server.Update(publicCtx, publicv1.ComputeInstancesUpdateRequest_builder{
						Object: publicObject,
					}.Build())
					publicResponse = response
					updateDone <- err
				}()

				// Wait until PostgreSQL reports a compute_instances query blocked by
				// writerTx, proving the public update read after the fixture was
				// committed but before the lock was released:
				Eventually(func() (bool, error) {
					var waiting bool
					err := testPool.QueryRow(context.Background(), `
						select exists (
							select 1
							from pg_stat_activity
							where datname = current_database()
							  and state = 'active'
							  and wait_event_type = 'Lock'
							  and $1 = any(pg_blocking_pids(pid))
							  and query ilike '%compute_instances%'
						)
					`, writerPID).Scan(&waiting)
					return waiting, err
				}, 5*time.Second, 10*time.Millisecond).Should(BeTrue())

				Expect(writerTx.End(context.Background())).To(Succeed())
				writerTx = nil

				Expect(<-updateDone).To(Succeed())
				updateDone = nil
				Expect(publicTx.End(publicCtx)).To(Succeed())
				publicTx = nil

				// The response carries the exact newer state, time, and attachment
				// feedback:
				statusObject := publicResponse.GetObject().GetStatus()
				Expect(statusObject.GetState()).To(
					Equal(publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING))
				Expect(statusObject.GetStateTransitionTime().AsTime()).To(Equal(writerTime.AsTime()))
				Expect(statusObject.GetComputeNetworkAttachmentStatuses()).To(HaveLen(1))
				Expect(statusObject.GetComputeNetworkAttachmentStatuses()[0].GetSubnetRef()).To(Equal("test-subnet"))
				Expect(statusObject.GetComputeNetworkAttachmentStatuses()[0].GetIpAddress()).To(Equal("192.0.2.12"))

				// A fresh committed transaction shows the newer status AND the user
				// edit:
				readTx, err := tm.Begin(context.Background())
				Expect(err).ToNot(HaveOccurred())
				readCtx := database.TxIntoContext(context.Background(), readTx)
				after, err := server.Get(readCtx, publicv1.ComputeInstancesGetRequest_builder{
					Id: id,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(after.GetObject().GetMetadata().GetLabels()).To(Equal(map[string]string{
					"updated": "by-public",
				}))
				statusObject = after.GetObject().GetStatus()
				Expect(statusObject.GetState()).To(
					Equal(publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING))
				Expect(statusObject.GetStateTransitionTime().AsTime()).To(Equal(writerTime.AsTime()))
				Expect(statusObject.GetComputeNetworkAttachmentStatuses()).To(HaveLen(1))
				Expect(statusObject.GetComputeNetworkAttachmentStatuses()[0].GetIpAddress()).To(Equal("192.0.2.12"))
				Expect(readTx.End(context.Background())).To(Succeed())
			},
			Entry("Keeps committed feedback when the public update omits the status", false),
			Entry("Keeps committed feedback when the public update echoes the older status", true),
		)

		It("Rejects changing network attachments via the public update path", func() {
			createTemplate("general.small")

			// Second READY subnet for the replacement attempt:
			subnetsDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = subnetsDao.Create().SetObject(privatev1.Subnet_builder{
				Id: "test-subnet-2",
				Metadata: privatev1.Metadata_builder{
					Name:   "test-subnet-2",
					Tenant: testTenant,
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "test-vnet"}.Build(),
					Ipv4Cidr:       new("10.0.1.0/24"),
				}.Build(),
				Status: privatev1.SubnetStatus_builder{
					State: privatev1.SubnetState_SUBNET_STATE_READY,
					Hub:   "network-hub-a",
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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

			updateResponse, err := server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Id: id,
					Spec: publicv1.ComputeInstanceSpec_builder{
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet-2"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.network_attachments"},
				},
			}.Build())
			Expect(updateResponse).To(BeNil())
			status, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(codes.InvalidArgument))
			stored, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: id}.Build())
			Expect(err).ToNot(HaveOccurred())
			attachments := stored.GetObject().GetSpec().GetNetworkAttachments()
			Expect(attachments).To(HaveLen(1))
			Expect(attachments[0].GetSubnet().GetId()).To(Equal("test-subnet"))
		})
		It("Replaces unmasked nonempty labels and requires a mask to clear", func() {
			createTemplate("general.small")

			createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name:        "compute-instance-metadata-merge",
						Labels:      map[string]string{"retained": "before"},
						Annotations: map[string]string{"ordinary": "before"},
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			instancesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstance]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			storedResponse, err := instancesDao.Get().SetId(id).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			stored := storedResponse.GetObject()
			stored.GetMetadata().GetLabels()["admin.osac.openshift.io/managed"] = "system"
			stored.GetMetadata().GetAnnotations()["provider.admin.osac.openshift.io/id"] = "provider"
			_, err = instancesDao.Update().SetObject(stored).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// A maskless update with nonempty labels replaces the caller-managed map.
			// Reserved entries are retained and untargeted maps survive:
			updateObject := proto.Clone(createResponse.GetObject()).(*publicv1.ComputeInstance)
			updateObject.GetMetadata().SetLabels(map[string]string{"added": "after"})
			updateResponse, err := server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object: updateObject,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetMetadata().GetLabels()).To(Equal(map[string]string{
				"added": "after",
			}))
			Expect(updateResponse.GetObject().GetMetadata().GetAnnotations()).To(Equal(map[string]string{
				"ordinary": "before",
			}))

			storedResponse, err = instancesDao.Get().SetId(id).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(storedResponse.GetObject().GetMetadata().GetLabels()).To(Equal(map[string]string{
				"admin.osac.openshift.io/managed": "system",
				"added":                           "after",
			}))
			Expect(storedResponse.GetObject().GetMetadata().GetAnnotations()).To(Equal(map[string]string{
				"provider.admin.osac.openshift.io/id": "provider",
				"ordinary":                            "before",
			}))

			// A maskless update with an empty map is a no-op. Clearing the
			// caller-managed map requires an explicit mask:
			clearObject := proto.Clone(updateResponse.GetObject()).(*publicv1.ComputeInstance)
			clearObject.GetMetadata().SetLabels(map[string]string{})
			clearResponse, err := server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object: clearObject,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			storedResponse, err = instancesDao.Get().SetId(id).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(storedResponse.GetObject().GetMetadata().GetLabels()).To(Equal(map[string]string{
				"admin.osac.openshift.io/managed": "system",
				"added":                           "after",
			}))

			maskedObject := proto.Clone(clearResponse.GetObject()).(*publicv1.ComputeInstance)
			maskedObject.GetMetadata().SetLabels(map[string]string{})
			maskedResponse, err := server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object:     maskedObject,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(maskedResponse.GetObject().GetMetadata().GetLabels()).To(BeEmpty())
			storedResponse, err = instancesDao.Get().SetId(id).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(storedResponse.GetObject().GetMetadata().GetLabels()).To(Equal(map[string]string{
				"admin.osac.openshift.io/managed": "system",
			}))
		})

		It("Deletes object", func() {
			// Create a template first
			createTemplate("general.small")

			// Create an object:
			createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
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
					Metadata: publicv1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template:    publicv1.ComputeInstanceTemplateReference_builder{Id: "mapping-template"}.Build(),
						RunStrategy: publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED.Enum(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())

			spec := response.GetObject().GetSpec()
			// User-provided values preserved through mapping:
			Expect(spec.GetRunStrategy()).To(Equal(publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED))
			// Template defaults should be stored:
			Expect(spec.GetInstanceType().GetId()).To(Equal("standard-4-16"))
			Expect(spec.GetDiskImage().GetId()).To(Equal("test-disk-image"))
			Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(10)))
		})

		It("Drops a user-supplied running instance type on public create", func() {
			createTemplate("running-type-template")

			response, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-compute-instance",
					}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "running-type-template"}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
							publicv1.ComputeNetworkAttachment_builder{
								Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
					Status: publicv1.ComputeInstanceStatus_builder{
						InstanceType: publicv1.InstanceTypeReference_builder{
							Id: "standard-4-16", Name: "standard-4-16",
						}.Build(),
						InstanceTypeTransitionTime: timestamppb.New(time.Now().Truncate(time.Second)),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			// Only the system sets the running type fields; user-supplied values are dropped.
			Expect(response.GetObject().GetStatus().GetInstanceType()).To(BeNil())
			Expect(response.GetObject().GetStatus().GetInstanceTypeTransitionTime()).To(BeNil())

			getResponse, err := server.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: response.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetStatus().GetInstanceType()).To(BeNil())
			Expect(getResponse.GetObject().GetStatus().GetInstanceTypeTransitionTime()).To(BeNil())
		})

		DescribeTable("Rejects a public update of the output-only running instance type fields",
			func(path string) {
				createTemplate("running-type-update-template")

				createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
					Object: publicv1.ComputeInstance_builder{
						Metadata: publicv1.Metadata_builder{
							Name: "test-compute-instance",
						}.Build(),
						Spec: publicv1.ComputeInstanceSpec_builder{
							Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "running-type-update-template"}.Build(),
							NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
								publicv1.ComputeNetworkAttachment_builder{
									Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
					Object: publicv1.ComputeInstance_builder{
						Id: createResponse.GetObject().GetId(),
						Status: publicv1.ComputeInstanceStatus_builder{
							InstanceType: publicv1.InstanceTypeReference_builder{
								Id: "standard-4-16", Name: "standard-4-16",
							}.Build(),
							InstanceTypeTransitionTime: timestamppb.Now(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{path},
					},
				}.Build())
				Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring(path + " is output-only"))
			},
			Entry("instance type", "status.instance_type"),
			Entry("instance type transition time", "status.instance_type_transition_time"),
			Entry("instance type name subpath", "status.instance_type.name"),
			Entry("instance type transition time subpath", "status.instance_type_transition_time.seconds"),
		)

		DescribeTable("Rejects public updates of disk registration status fields",
			func(path string) {
				createTemplate("disk-status-update-template")

				createResponse, err := server.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
					Object: publicv1.ComputeInstance_builder{
						Metadata: publicv1.Metadata_builder{Name: "disk-status-update"}.Build(),
						Spec: publicv1.ComputeInstanceSpec_builder{
							Template: publicv1.ComputeInstanceTemplateReference_builder{Id: "disk-status-update-template"}.Build(),
							NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
								publicv1.ComputeNetworkAttachment_builder{
									Subnet: publicv1.SubnetLocalReference_builder{Id: "test-subnet"}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
					Object: publicv1.ComputeInstance_builder{
						Id: createResponse.GetObject().GetId(),
						Status: publicv1.ComputeInstanceStatus_builder{
							Volumes: []*publicv1.ComputeInstanceVolume{
								publicv1.ComputeInstanceVolume_builder{DiskKey: "boot", VolumeId: "volume-spoofed"}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}},
				}.Build())
				Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring(path + " is output-only"))
			},
			Entry("public correlation list", "status.volumes"),
			Entry("public correlation subfield", "status.volumes.volume_id"),
			Entry("public correlation JSON subfield", "status.volumes.volumeId"),
			Entry("private registration list", "status.volume_registration"),
			Entry("private registration subfield", "status.volume_registration.state"),
			Entry("private registration JSON field", "status.volumeRegistration"),
			Entry("private registration JSON subfield", "status.volumeRegistration.state"),
		)
	})
})

var _ = Describe("Catalog publication and references", func() {
	DescribeTable("resolves same-name catalog creation sources in the requested tenant", func(shared bool) {
		Expect(seedComputeCatalogItemTemplate(ctx, auth.SharedTenant, "", "source-template")).To(Succeed())
		catalogs, err := NewPrivateComputeInstanceCatalogItemsServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		for _, tenant := range []string{testTenant, auth.SharedTenant} {
			_, err = catalogs.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{Object: privatev1.ComputeInstanceCatalogItem_builder{
				Metadata: privatev1.Metadata_builder{Name: "same-name", Tenant: tenant}.Build(), Title: "Offering", Published: tenant == auth.SharedTenant,
				Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "source-template"}.Build(),
			}.Build()}.Build())
			Expect(err).ToNot(HaveOccurred())
		}
		server, err := NewComputeInstancesServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		request := publicv1.ComputeInstancesCreateRequest_builder{Object: publicv1.ComputeInstance_builder{
			Metadata: publicv1.Metadata_builder{Name: "vm"}.Build(), Spec: publicv1.ComputeInstanceSpec_builder{
				CatalogItem: publicv1.ComputeInstanceCatalogItemReference_builder{Name: "same-name", Shared: shared}.Build(),
			}.Build(),
		}.Build()}.Build()
		original := proto.Clone(request)
		_, err = server.Create(ctx, request)
		if shared {
			// Correct shared source reaches ordinary required-field validation.
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(status.Convert(err).Message()).To(ContainSubstring("instance_type"))
		} else {
			Expect(status.Code(err)).To(Equal(codes.NotFound))
			Expect(status.Convert(err).Message()).To(ContainSubstring("not published"))
		}
		Expect(proto.Equal(request, original)).To(BeTrue())
	},
		Entry("selects the published shared item", true),
		Entry("rejects the unpublished tenant item", false),
	)
})
