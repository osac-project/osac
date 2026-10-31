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

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

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
		computeInstanceTemplateId = newTemplateID("test_ci_template")
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
		ncResp, err := sharedNetworkClass(ctx, networkClassesClient)
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
					Ipv4Cidr: new("10.100.0.0/16"),
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

		setSubnetReady(ctx, subnetsClient, subnetId)
		securityGroupId = createComputeInstanceFixtureSecurityGroup(ctx, securityGroupsClient,
			fmt.Sprintf("test-sg-%s", uuid.New()), virtualNetworkId)
	})

	AfterEach(func() {
		cleanupComputeInstanceFixture(ctx, fixtureClients, computeInstanceId, "", instanceTypeId,
			securityGroupId, subnetId, virtualNetworkId, networkClassId, computeInstanceTemplateId, diskImageId, storageTierId, storageBackendId)
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
					InstanceType: publicv1.InstanceTypeReference_builder{Name: instanceTypeId, Shared: true}.Build(),
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

		computeInstanceId = fmt.Sprintf("test-ci-%s", uuid.New())
		createResp, err := computeInstancesClient.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-ci-%s", uuid.New()[24:32]),
				}.Build(),
				Id: computeInstanceId,
				Spec: publicv1.ComputeInstanceSpec_builder{
					Template:     publicv1.ComputeInstanceTemplateReference_builder{Id: computeInstanceTemplateId}.Build(),
					InstanceType: publicv1.InstanceTypeReference_builder{Name: instanceTypeId, Shared: true}.Build(),
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
		Expect(createResp.GetObject().GetSpec().GetNetworkAttachments()).To(HaveLen(1))
		attachment := createResp.GetObject().GetSpec().GetNetworkAttachments()[0]
		Expect(attachment.GetSubnet().GetId()).To(Equal(network.subnetID))
		Expect(attachment.GetSecurityGroups()).To(HaveLen(1))
		Expect(attachment.GetSecurityGroups()[0].GetId()).To(Equal(network.securityGroupID))

		getResp, err := computeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
			Id: computeInstanceId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResp.GetObject().GetSpec().GetNetworkAttachments()).To(Equal(createResp.GetObject().GetSpec().GetNetworkAttachments()))
	})

	// privateInstance builds a private ComputeInstance create of the fixture's template, instance type, disk and image,
	// in the given tenant, with the given network attachments.
	privateInstance := func(tenant string, attachments ...*privatev1.ComputeNetworkAttachment) *privatev1.ComputeInstance {
		return privatev1.ComputeInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   fmt.Sprintf("test-ci-%s", uuid.New()[24:32]),
				Tenant: tenant,
			}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{
				Template:     privatev1.ComputeInstanceTemplateReference_builder{Id: computeInstanceTemplateId}.Build(),
				InstanceType: privatev1.InstanceTypeReference_builder{Name: instanceTypeId, Shared: true}.Build(),
				RunStrategy:  privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
				BootDisk: privatev1.ComputeInstanceDisk_builder{
					SizeGib:     proto.Int32(20),
					StorageTier: privatev1.StorageTierReference_builder{Id: storageTierId}.Build(),
				}.Build(),
				DiskImage:          &privatev1.DiskImageReference{Id: diskImageId},
				NetworkAttachments: attachments,
			}.Build(),
		}.Build()
	}

	// expectNoInstanceNamed checks that a rejected create stored nothing.
	expectNoInstanceNamed := func(tenant, name string) {
		privateInstances := privatev1.NewComputeInstancesClient(tool.InternalView().AdminConn())
		filter := fmt.Sprintf("this.metadata.tenant == %q && this.metadata.name == %q", tenant, name)
		list, err := privateInstances.List(ctx, privatev1.ComputeInstancesListRequest_builder{Filter: &filter}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(list.GetItems()).To(BeEmpty(), "the rejected ComputeInstance %s was stored", name)
	}

	It("TC-ATTACH-01 defaults the attachment from the tenant: INVALID_ARGUMENT without a default, "+
		"FAILED_PRECONDITION while it isn't READY", func() {
		c := newNDClients()
		ndReadyNetworkClass(ctx, c)
		privateInstances := privatev1.NewComputeInstancesClient(tool.InternalView().AdminConn())
		_, tenantName := ndCreateTenant(ctx, c)

		By("waiting for the tenant's default VirtualNetwork, before its default Subnet exists")
		var vn *privatev1.VirtualNetwork
		Eventually(func(g Gomega) {
			filter := ndDefaultFilter(tenantName, "default")
			list, err := c.virtualNetworks.List(ctx, privatev1.VirtualNetworksListRequest_builder{Filter: &filter}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(list.GetItems()).To(HaveLen(1), "tenant %s has no default VirtualNetwork yet", tenantName)
			vn = list.GetItems()[0]
		}, ndTimeout, ndPoll).Should(Succeed())
		ndWaitVirtualNetworkPlaced(ctx, c, vn.GetId())

		By("creating a ComputeInstance without an attachment while the tenant has no default Subnet")
		object := privateInstance(tenantName)
		_, err := privateInstances.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{Object: object}.Build())
		ndExpectCode(err, grpccodes.InvalidArgument, "has no default Subnet")
		expectNoInstanceNamed(tenantName, object.GetMetadata().GetName())

		By("reporting the default VirtualNetwork READY, so the default Subnet and SecurityGroup are created PENDING")
		ndSetVirtualNetworkState(ctx, c, vn.GetId(), privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY)
		Eventually(func(g Gomega) {
			for _, check := range []func() (int, error){
				func() (int, error) {
					filter := ndDefaultFilter(tenantName, "default-ipv4")
					list, err := c.subnets.List(ctx, privatev1.SubnetsListRequest_builder{Filter: &filter}.Build())
					return len(list.GetItems()), err
				},
				func() (int, error) {
					filter := ndDefaultFilter(tenantName, "default")
					list, err := c.securityGroups.List(ctx, privatev1.SecurityGroupsListRequest_builder{
						Filter: &filter,
					}.Build())
					return len(list.GetItems()), err
				},
			} {
				count, err := check()
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(count).To(Equal(1), "tenant %s defaults not created yet", tenantName)
			}
		}, ndTimeout, ndPoll).Should(Succeed())

		By("creating a ComputeInstance without an attachment while the default Subnet is PENDING")
		object = privateInstance(tenantName)
		_, err = privateInstances.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{Object: object}.Build())
		ndExpectCode(err, grpccodes.FailedPrecondition, "is not in READY state")
		expectNoInstanceNamed(tenantName, object.GetMetadata().GetName())

		By("creating one with an empty attachment, which is defaulted the same way")
		object = privateInstance(tenantName, privatev1.ComputeNetworkAttachment_builder{}.Build())
		_, err = privateInstances.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{Object: object}.Build())
		ndExpectCode(err, grpccodes.FailedPrecondition, "is not in READY state")
		expectNoInstanceNamed(tenantName, object.GetMetadata().GetName())
	})

	It("TC-ATTACH-02 rejects SecurityGroups listed twice, of another VirtualNetwork or not READY", func() {
		c := newNDClients()
		attach := func(groups ...string) *privatev1.ComputeNetworkAttachment {
			refs := make([]*privatev1.SecurityGroupLocalReference, 0, len(groups))
			for _, group := range groups {
				refs = append(refs, privatev1.SecurityGroupLocalReference_builder{Id: group}.Build())
			}
			return privatev1.ComputeNetworkAttachment_builder{
				Subnet:         privatev1.SubnetLocalReference_builder{Id: subnetId}.Build(),
				SecurityGroups: refs,
			}.Build()
		}
		privateInstances := privatev1.NewComputeInstancesClient(tool.InternalView().AdminConn())
		create := func(attachment *privatev1.ComputeNetworkAttachment) (string, error) {
			object := privateInstance(usersGroup, attachment)
			_, err := privateInstances.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: object,
			}.Build())
			return object.GetMetadata().GetName(), err
		}

		By("listing the same SecurityGroup twice")
		name, err := create(attach(securityGroupId, securityGroupId))
		ndExpectCode(err, grpccodes.InvalidArgument, "is listed twice")
		expectNoInstanceNamed(usersGroup, name)

		By("selecting a SecurityGroup of another VirtualNetwork")
		other := ndReadyVirtualNetwork(ctx, c, usersGroup, ndVirtualNetworkCIDR())
		foreign := ndReadySecurityGroup(ctx, c, usersGroup, other.GetId(), nil, nil)
		name, err = create(attach(securityGroupId, foreign.GetId()))
		ndExpectCode(err, grpccodes.InvalidArgument, "belongs to another VirtualNetwork")
		expectNoInstanceNamed(usersGroup, name)

		By("selecting a SecurityGroup that is not READY")
		pending := ndCreateSecurityGroup(ctx, c, usersGroup, virtualNetworkId, nil, nil)
		name, err = create(attach(pending.GetId()))
		ndExpectCode(err, grpccodes.FailedPrecondition, "is not in READY state")
		expectNoInstanceNamed(usersGroup, name)

		By("selecting a Subnet that is not READY")
		pendingSubnet := ndCreateSubnet(ctx, c, usersGroup, virtualNetworkId, "10.100.7.0/24")
		name, err = create(privatev1.ComputeNetworkAttachment_builder{
			Subnet: privatev1.SubnetLocalReference_builder{Id: pendingSubnet.GetId()}.Build(),
			SecurityGroups: []*privatev1.SecurityGroupLocalReference{
				privatev1.SecurityGroupLocalReference_builder{Id: securityGroupId}.Build(),
			},
		}.Build())
		ndExpectCode(err, grpccodes.FailedPrecondition, "is not in READY state")
		expectNoInstanceNamed(usersGroup, name)

		// The fixture's AfterEach deletes its VirtualNetwork before DeferCleanup runs, so remove the children of
		// that VirtualNetwork here.
		By("removing the PENDING Subnet and SecurityGroup of the fixture VirtualNetwork")
		Expect(cleanupSubnetFixture(ctx, c.subnets, pendingSubnet.GetId())).To(Succeed())
		Expect(cleanupSecurityGroupFixture(ctx, c.securityGroups, pending.GetId())).To(Succeed())
	})

	It("rejects an attachment without SecurityGroups for a non-default VirtualNetwork", func() {
		computeInstanceId = fmt.Sprintf("test-ci-%s", uuid.New())
		_, err := computeInstancesClient.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Metadata: publicv1.Metadata_builder{Name: fmt.Sprintf("test-ci-%s", uuid.New()[24:32])}.Build(),
				Id:       computeInstanceId,
				Spec: publicv1.ComputeInstanceSpec_builder{
					Template:     publicv1.ComputeInstanceTemplateReference_builder{Id: computeInstanceTemplateId}.Build(),
					InstanceType: publicv1.InstanceTypeReference_builder{Name: instanceTypeId, Shared: true}.Build(),
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
		Expect(err.Error()).To(ContainSubstring("at least one SecurityGroup is required"))

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
					InstanceType: publicv1.InstanceTypeReference_builder{Name: instanceTypeId, Shared: true}.Build(),
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
					InstanceType: publicv1.InstanceTypeReference_builder{Name: instanceTypeId, Shared: true}.Build(),
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
					InstanceType: publicv1.InstanceTypeReference_builder{Name: instanceTypeId, Shared: true}.Build(),
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
