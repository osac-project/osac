/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("ComputeInstance with Subnet attachment", func() {
	var (
		ctx                            context.Context
		fixtureClients                 computeInstanceFixtureClients
		subnetsClient                  privatev1.SubnetsClient
		securityGroupsClient           privatev1.SecurityGroupsClient
		virtualNetworksClient          privatev1.VirtualNetworksClient
		networkClassesClient           privatev1.NetworkClassesClient
		computeInstancesClient         publicv1.ComputeInstancesClient
		computeInstanceTemplatesClient privatev1.ComputeInstanceTemplatesClient
		instanceTypesClient            privatev1.InstanceTypesClient
		storageTiersClient             privatev1.StorageTiersClient
		storageBackendsClient          privatev1.StorageBackendsClient
		diskImagesClient               privatev1.DiskImagesClient

		networkClassId            string
		virtualNetworkId          string
		subnetId                  string
		securityGroupId           string
		computeInstanceId         string
		computeInstanceTemplateId string
		instanceTypeId            string
		storageBackendId          string
		storageTierId             string
		diskImageId               string
	)

	BeforeEach(func() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)

		fixtureClients = newComputeInstanceFixtureClients()
		subnetsClient = fixtureClients.subnets
		securityGroupsClient = fixtureClients.securityGroups
		virtualNetworksClient = fixtureClients.virtualNetworks
		networkClassesClient = fixtureClients.networkClasses
		computeInstancesClient = fixtureClients.computeInstances
		computeInstanceTemplatesClient = fixtureClients.computeInstanceTemplates
		instanceTypesClient = fixtureClients.instanceTypes
		storageTiersClient = fixtureClients.storageTiers
		storageBackendsClient = fixtureClients.storageBackends
		diskImagesClient = fixtureClients.diskImages

		// Create StorageBackend
		sbResp, err := storageBackendsClient.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{
			Object: privatev1.StorageBackend_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-sb-%s", uuid.New()),
				}.Build(),
				Spec: privatev1.StorageBackendSpec_builder{
					Provider:    "test",
					Description: "Test storage backend for subnet tests",
					Endpoint:    "https://test-backend.example.com",
					Credentials: privatev1.StorageBackendCredentials_builder{
						Username: "test-user",
						Password: "test-credential", //nolint:goconst // test-only dummy credential for fake provider
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		storageBackendId = sbResp.GetObject().GetId()
		waitForComputeInstanceFixtureStorageBackend(ctx, storageBackendsClient, storageBackendId)

		// Create StorageTier
		stResp, err := storageTiersClient.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
			Object: privatev1.StorageTier_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-st-%s", uuid.New()),
				}.Build(),
				Spec: privatev1.StorageTierSpec_builder{
					Description: "Test storage tier for subnet tests",
					Protocol:    privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
					Backends: []*privatev1.BackendAssociation{
						privatev1.BackendAssociation_builder{
							BackendId: storageBackendId,
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		storageTierId = stResp.GetObject().GetId()

		// Create InstanceType
		instanceTypeId = fmt.Sprintf("test-it-%s", uuid.New())
		_, err = instanceTypesClient.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Metadata: privatev1.Metadata_builder{
					Name: instanceTypeId,
				}.Build(),
				Spec: privatev1.InstanceTypeSpec_builder{
					Vcpus:     2,
					MemoryGib: 4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Create DiskImage
		diResp, err := diskImagesClient.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
			Object: privatev1.DiskImage_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-di-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: privatev1.DiskImageSpec_builder{
					SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
					SourceRef:     "quay.io/containerdisks/fedora:41",
					GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
					Architecture: []privatev1.Architecture{
						privatev1.Architecture_ARCHITECTURE_AMD64,
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		diskImageId = diResp.GetObject().GetId()

		// Create ComputeInstanceTemplate
		computeInstanceTemplateId = fmt.Sprintf("test-ci-template-%s", uuid.New())
		_, err = computeInstanceTemplatesClient.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
			Object: privatev1.ComputeInstanceTemplate_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-ci-tmpl-%s", uuid.New()[24:32]),
				}.Build(),
				Id:          computeInstanceTemplateId,
				Title:       "Test CI Template",
				Description: "Template for compute instance subnet test.",
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Create NetworkClass
		ncName := fmt.Sprintf("cudn-subnet-%s", uuid.New())
		ncResp, err := networkClassesClient.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Metadata:      privatev1.Metadata_builder{Name: ncName}.Build(),
				Title:         "Test CUDN Network Class",
				FabricManager: new("netris"),
				K8SManager:    new(testVMK8SManagerName),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassId = ncResp.GetObject().GetId()
		waitForNetworkClassReady(ctx, networkClassesClient, networkClassId)
		setNetworkClassCanonicalHub(ctx, networkClassesClient, networkClassId, hubId)
		expectNetworkClassStatus(
			ctx,
			networkClassesClient,
			networkClassId,
			privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			hubId,
			"",
		)

		// Create VirtualNetwork
		virtualNetworkId = fmt.Sprintf("test-vnet-%s", uuid.New())
		_, err = virtualNetworksClient.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-vnet-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Id: virtualNetworkId,
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: networkClassId}.Build(),
					Region:       "us-east-1",
					Ipv4Cidr:     new("10.100.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Set VirtualNetwork to READY state via private Update API.
		// In IT environment there is no osac-operator/feedback controller to reconcile state.
		setComputeInstanceFixtureVirtualNetworkReady(ctx, virtualNetworksClient, virtualNetworkId)

		// Create Subnet
		subnetId = fmt.Sprintf("test-subnet-%s", uuid.New())
		_, err = subnetsClient.Create(ctx, privatev1.SubnetsCreateRequest_builder{
			Object: privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-subnet-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Id: subnetId,
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
					Ipv4Cidr:       new("10.100.1.0/24"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Wait for the subnet reconciler to finish initial processing before
		// overriding state. The reconciler sets state from UNSPECIFIED to PENDING;
		// without this wait, a stale reconciler event can overwrite our READY state.
		Eventually(func(g Gomega) {
			resp, err := subnetsClient.Get(ctx, privatev1.SubnetsGetRequest_builder{
				Id: subnetId,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())

		// Set Subnet to READY state via private Update API
		subGetResp, err := subnetsClient.Get(ctx, privatev1.SubnetsGetRequest_builder{
			Id: subnetId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		sub := subGetResp.GetObject()
		sub.SetStatus(privatev1.SubnetStatus_builder{
			State: privatev1.SubnetState_SUBNET_STATE_READY,
		}.Build())
		_, err = subnetsClient.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
			Object:     sub,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		securityGroupId = createComputeInstanceFixtureSecurityGroup(ctx, securityGroupsClient,
			fmt.Sprintf("test-sg-%s", uuid.New()), virtualNetworkId)
	})

	AfterEach(func() {
		// Delete the workload first: the omission spec's ComputeInstance
		// references the tenant-default Subnet and SecurityGroup, so the
		// tenant-default cleanup below cannot run while it exists.
		cleanupComputeInstanceFixture(ctx, fixtureClients, computeInstanceId, "", instanceTypeId,
			"", "", "", "", computeInstanceTemplateId, diskImageId, storageTierId, storageBackendId)
		// Remove the tenant-default networking fixture next: its "default"
		// VirtualNetwork references this block's NetworkClass, and DeferCleanup
		// runs after this AfterEach, so the class delete below cannot succeed
		// until the default fixture is gone.
		manager, err := newTestDefaultNetworkingManager()
		Expect(err).NotTo(HaveOccurred())
		Expect(deleteTenantDefaultNetworkingFixture(ctx, manager, usersGroup)).To(Succeed())
		// Finally remove the block resources that depend on the NetworkClass.
		cleanupComputeInstanceFixture(ctx, fixtureClients, "", "", "",
			securityGroupId, subnetId, virtualNetworkId, networkClassId, "", "", "", "")
	})

	It("creates ComputeInstance with network attachments", func() {
		// Create ComputeInstance with network attachment
		computeInstanceId = fmt.Sprintf("test-ci-%s", uuid.New())
		createResp, err := computeInstancesClient.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ci-%s", uuid.New()[24:32]),
				}.Build(),
				Id: computeInstanceId,
				Spec: publicv1.ComputeInstanceSpec_builder{
					Template:     publicv1.ComputeInstanceTemplateReference_builder{Id: computeInstanceTemplateId}.Build(),
					InstanceType: publicv1.InstanceTypeReference_builder{Name: instanceTypeId}.Build(),
					RunStrategy:  publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
					BootDisk: publicv1.ComputeInstanceDisk_builder{
						SizeGib:     proto.Int32(20),
						StorageTier: publicv1.StorageTierReference_builder{Id: storageTierId}.Build(),
					}.Build(),
					DiskImage: &publicv1.DiskImageReference{Id: diskImageId},
					NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
						publicv1.ComputeNetworkAttachment_builder{
							Subnet: publicv1.SubnetLocalReference_builder{Id: subnetId}.Build(),
							SecurityGroups: []*publicv1.SecurityGroupLocalReference{
								publicv1.SecurityGroupLocalReference_builder{Id: securityGroupId}.Build(),
							},
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(createResp.GetObject()).ToNot(BeNil())

		// Verify the network attachment is persisted via Get
		getResp, err := computeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
			Id: computeInstanceId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResp.GetObject().GetSpec().GetNetworkAttachments()).To(HaveLen(1))
		Expect(getResp.GetObject().GetSpec().GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal(subnetId),
			"ComputeInstance should persist network attachment with subnet reference")
		Expect(getResp.GetObject().GetSpec().GetNetworkAttachments()[0].GetSecurityGroups()).To(HaveLen(1))
		Expect(getResp.GetObject().GetSpec().GetNetworkAttachments()[0].GetSecurityGroups()[0].GetId()).To(Equal(securityGroupId),
			"ComputeInstance should preserve the explicitly specified SecurityGroup")
	})

	It("uses tenant defaults when network_attachments is omitted", func() {
		ensureTenantDefaultNetworkingFixture(ctx, usersGroup, "")
		network := getTenantDefaultNetworkFixture(ctx, usersGroup, "")

		// Create ComputeInstance without any network attachment assignment.
		computeInstanceId = fmt.Sprintf("test-ci-%s", uuid.New())
		createResp, err := computeInstancesClient.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ci-%s", uuid.New()[24:32]),
				}.Build(),
				Id: computeInstanceId,
				Spec: publicv1.ComputeInstanceSpec_builder{
					Template:     publicv1.ComputeInstanceTemplateReference_builder{Id: computeInstanceTemplateId}.Build(),
					InstanceType: publicv1.InstanceTypeReference_builder{Name: instanceTypeId}.Build(),
					RunStrategy:  publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
					BootDisk: publicv1.ComputeInstanceDisk_builder{
						SizeGib:     proto.Int32(20),
						StorageTier: publicv1.StorageTierReference_builder{Id: storageTierId}.Build(),
					}.Build(),
					DiskImage: &publicv1.DiskImageReference{Id: diskImageId},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(createResp.GetObject()).ToNot(BeNil())

		// Omitted network_attachments should resolve the tenant default Subnet
		// and SecurityGroup in the Create response.
		Expect(createResp.GetObject().GetSpec().GetNetworkAttachments()).To(HaveLen(1))
		Expect(createResp.GetObject().GetSpec().GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal(network.subnetID),
			"ComputeInstance should default to the tenant default Subnet when network_attachments is omitted")
		Expect(createResp.GetObject().GetSpec().GetNetworkAttachments()[0].GetSecurityGroups()).To(HaveLen(1))
		Expect(createResp.GetObject().GetSpec().GetNetworkAttachments()[0].GetSecurityGroups()[0].GetId()).To(Equal(network.securityGroupID),
			"ComputeInstance should default to the tenant default SecurityGroup when network_attachments is omitted")

		// Verify the resolved default attachment is persisted via public Get.
		getResp, err := computeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
			Id: computeInstanceId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResp.GetObject().GetSpec().GetNetworkAttachments()).To(HaveLen(1))
		Expect(getResp.GetObject().GetSpec().GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal(network.subnetID),
			"ComputeInstance should persist the tenant default Subnet reference")
		Expect(getResp.GetObject().GetSpec().GetNetworkAttachments()[0].GetSecurityGroups()).To(HaveLen(1))
		Expect(getResp.GetObject().GetSpec().GetNetworkAttachments()[0].GetSecurityGroups()[0].GetId()).To(Equal(network.securityGroupID),
			"ComputeInstance should persist the tenant default SecurityGroup reference")
	})

	It("rejects ComputeInstance without attachments when the tenant has no default subnet", func() {
		computeInstanceId = fmt.Sprintf("test-ci-%s", uuid.New())
		project := createCatalogItemProjectFixture(ctx, usersGroup)
		_, err := computeInstancesClient.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name:    fmt.Sprintf("test-ci-%s", uuid.New()[24:32]),
					Project: project,
				}.Build(),
				Id: computeInstanceId,
				Spec: publicv1.ComputeInstanceSpec_builder{
					Template:     publicv1.ComputeInstanceTemplateReference_builder{Id: computeInstanceTemplateId}.Build(),
					InstanceType: publicv1.InstanceTypeReference_builder{Name: instanceTypeId}.Build(),
					RunStrategy:  publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
					BootDisk: publicv1.ComputeInstanceDisk_builder{
						SizeGib:     proto.Int32(20),
						StorageTier: publicv1.StorageTierReference_builder{Id: storageTierId}.Build(),
					}.Build(),
					DiskImage: &publicv1.DiskImageReference{Id: diskImageId},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("default subnet is required"))
		_, getErr := computeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: computeInstanceId}.Build())
		Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
	})

	It("rejects an attachment without SecurityGroups for a non-default VirtualNetwork", func() {
		computeInstanceId = fmt.Sprintf("test-ci-%s", uuid.New())
		_, err := computeInstancesClient.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Metadata: publicv1.Metadata_builder{Name: fmt.Sprintf("test-ci-%s", uuid.New()[24:32])}.Build(),
				Id:       computeInstanceId,
				Spec: publicv1.ComputeInstanceSpec_builder{
					Template:     publicv1.ComputeInstanceTemplateReference_builder{Id: computeInstanceTemplateId}.Build(),
					InstanceType: publicv1.InstanceTypeReference_builder{Name: instanceTypeId}.Build(),
					RunStrategy:  publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
					BootDisk: publicv1.ComputeInstanceDisk_builder{
						SizeGib:     proto.Int32(20),
						StorageTier: publicv1.StorageTierReference_builder{Id: storageTierId}.Build(),
					}.Build(),
					DiskImage: &publicv1.DiskImageReference{Id: diskImageId},
					NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
						publicv1.ComputeNetworkAttachment_builder{
							Subnet: publicv1.SubnetLocalReference_builder{Id: subnetId}.Build(),
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("security group is required"))

		_, getErr := computeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: computeInstanceId}.Build())
		Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
	})

	It("rejects multiple ComputeInstance network attachments without persisting the instance", func() {
		computeInstanceId = fmt.Sprintf("test-ci-%s", uuid.New())
		_, err := computeInstancesClient.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Metadata: publicv1.Metadata_builder{Name: fmt.Sprintf("test-ci-%s", uuid.New()[24:32])}.Build(),
				Id:       computeInstanceId,
				Spec: publicv1.ComputeInstanceSpec_builder{
					Template:     publicv1.ComputeInstanceTemplateReference_builder{Id: computeInstanceTemplateId}.Build(),
					InstanceType: publicv1.InstanceTypeReference_builder{Name: instanceTypeId}.Build(),
					RunStrategy:  publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
					BootDisk: publicv1.ComputeInstanceDisk_builder{
						SizeGib:     proto.Int32(20),
						StorageTier: publicv1.StorageTierReference_builder{Id: storageTierId}.Build(),
					}.Build(),
					DiskImage: &publicv1.DiskImageReference{Id: diskImageId},
					NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
						publicv1.ComputeNetworkAttachment_builder{Subnet: publicv1.SubnetLocalReference_builder{Id: subnetId}.Build()}.Build(),
						publicv1.ComputeNetworkAttachment_builder{Subnet: publicv1.SubnetLocalReference_builder{Id: subnetId}.Build()}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))

		_, getErr := computeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: computeInstanceId}.Build())
		Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
	})

	It("rejects ComputeInstance with non-existent subnet in network attachments", func() {
		computeInstanceId = fmt.Sprintf("test-ci-%s", uuid.New())
		_, err := computeInstancesClient.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ci-%s", uuid.New()[24:32]),
				}.Build(),
				Id: computeInstanceId,
				Spec: publicv1.ComputeInstanceSpec_builder{
					Template:     publicv1.ComputeInstanceTemplateReference_builder{Id: computeInstanceTemplateId}.Build(),
					InstanceType: publicv1.InstanceTypeReference_builder{Name: instanceTypeId}.Build(),
					RunStrategy:  publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
					BootDisk: publicv1.ComputeInstanceDisk_builder{
						SizeGib:     proto.Int32(20),
						StorageTier: publicv1.StorageTierReference_builder{Id: storageTierId}.Build(),
					}.Build(),
					DiskImage: &publicv1.DiskImageReference{Id: diskImageId},
					NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
						publicv1.ComputeNetworkAttachment_builder{
							Subnet: publicv1.SubnetLocalReference_builder{Name: "non-existent-subnet"}.Build(),
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		computeInstanceId = "" // not created, skip cleanup
	})

	It("guards deletion of workload-referenced Subnet and SecurityGroup through the API", func() {
		By("creating a ComputeInstance attached to the READY Subnet and SecurityGroup")
		computeInstanceId = fmt.Sprintf("test-ci-%s", uuid.New())
		_, err := computeInstancesClient.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ci-%s", uuid.New()[24:32]),
				}.Build(),
				Id: computeInstanceId,
				Spec: publicv1.ComputeInstanceSpec_builder{
					Template:     publicv1.ComputeInstanceTemplateReference_builder{Id: computeInstanceTemplateId}.Build(),
					InstanceType: publicv1.InstanceTypeReference_builder{Name: instanceTypeId}.Build(),
					RunStrategy:  publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
					BootDisk: publicv1.ComputeInstanceDisk_builder{
						SizeGib:     proto.Int32(20),
						StorageTier: publicv1.StorageTierReference_builder{Id: storageTierId}.Build(),
					}.Build(),
					DiskImage: &publicv1.DiskImageReference{Id: diskImageId},
					NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
						publicv1.ComputeNetworkAttachment_builder{
							Subnet: publicv1.SubnetLocalReference_builder{Id: subnetId}.Build(),
							SecurityGroups: []*publicv1.SecurityGroupLocalReference{
								publicv1.SecurityGroupLocalReference_builder{Id: securityGroupId}.Build(),
							},
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		By("rejecting Subnet deletion while the ComputeInstance references it")
		_, err = subnetsClient.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{
			Id: subnetId,
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
		getResp, err := subnetsClient.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetId}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResp.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse())

		By("rejecting SecurityGroup deletion while the ComputeInstance references it")
		_, err = securityGroupsClient.Delete(ctx, privatev1.SecurityGroupsDeleteRequest_builder{
			Id: securityGroupId,
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
		sgGetResp, err := securityGroupsClient.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: securityGroupId}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(sgGetResp.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse())

		By("deleting the ComputeInstance and waiting for its removal")
		deleteAndWaitForComputeInstanceFixtureResource(ctx,
			func(deleteCtx context.Context) error {
				_, err := computeInstancesClient.Delete(deleteCtx, publicv1.ComputeInstancesDeleteRequest_builder{
					Id: computeInstanceId,
				}.Build())
				return err
			},
			func(getCtx context.Context) error {
				_, err := computeInstancesClient.Get(getCtx, publicv1.ComputeInstancesGetRequest_builder{
					Id: computeInstanceId,
				}.Build())
				return err
			})
		computeInstanceId = "" // already removed; skip redundant cleanup

		By("deleting the released Subnet and SecurityGroup in dependency order")
		deleteComputeInstanceFixtureResource(ctx, func(deleteCtx context.Context) error {
			_, err := securityGroupsClient.Delete(deleteCtx, privatev1.SecurityGroupsDeleteRequest_builder{
				Id: securityGroupId,
			}.Build())
			return err
		})
		deleteComputeInstanceFixtureResource(ctx, func(deleteCtx context.Context) error {
			_, err := subnetsClient.Delete(deleteCtx, privatev1.SubnetsDeleteRequest_builder{
				Id: subnetId,
			}.Build())
			return err
		})
	})
})
