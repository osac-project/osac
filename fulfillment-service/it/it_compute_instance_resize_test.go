/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package it

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("ComputeInstance InstanceType resize", func() {
	var (
		ctx                            context.Context
		fixtureClients                 computeInstanceFixtureClients
		computeInstancesClient         publicv1.ComputeInstancesClient
		computeInstanceTemplatesClient privatev1.ComputeInstanceTemplatesClient
		instanceTypesClient            privatev1.InstanceTypesClient
		storageTiersClient             privatev1.StorageTiersClient
		storageBackendsClient          privatev1.StorageBackendsClient
		subnetsClient                  privatev1.SubnetsClient
		virtualNetworksClient          privatev1.VirtualNetworksClient
		networkClassesClient           privatev1.NetworkClassesClient
		diskImagesClient               privatev1.DiskImagesClient

		computeInstanceId         string
		computeInstanceTemplateId string
		instanceTypeId            string
		resizeInstanceTypeId      string
		storageBackendId          string
		storageTierId             string
		networkClassId            string
		virtualNetworkId          string
		subnetId                  string
		diskImageId               string
	)

	BeforeEach(func() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
		fixtureClients = newComputeInstanceFixtureClients()
		computeInstancesClient = fixtureClients.computeInstances
		computeInstanceTemplatesClient = fixtureClients.computeInstanceTemplates
		instanceTypesClient = fixtureClients.instanceTypes
		storageTiersClient = fixtureClients.storageTiers
		storageBackendsClient = fixtureClients.storageBackends
		subnetsClient = fixtureClients.subnets
		virtualNetworksClient = fixtureClients.virtualNetworks
		networkClassesClient = fixtureClients.networkClasses
		diskImagesClient = fixtureClients.diskImages

		requestedStorageBackendId := fmt.Sprintf("test-resize-sb-%s", uuid.New())
		storageBackendResponse, err := storageBackendsClient.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{
			Object: privatev1.StorageBackend_builder{
				Id:       requestedStorageBackendId,
				Metadata: privatev1.Metadata_builder{Name: requestedStorageBackendId}.Build(),
				Spec: privatev1.StorageBackendSpec_builder{
					Provider:    "test",
					Description: "Resize test backend",
					Endpoint:    "https://test-backend.example.com",
					Credentials: privatev1.StorageBackendCredentials_builder{
						Username: "test-user",
						Password: "test-credential", //nolint:goconst // test-only dummy credential for fake provider
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		storageBackendId = storageBackendResponse.GetObject().GetId()
		waitForComputeInstanceFixtureStorageBackend(ctx, storageBackendsClient, storageBackendId)

		storageTierId = fmt.Sprintf("test-resize-st-%s", uuid.New())
		_, err = storageTiersClient.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
			Object: privatev1.StorageTier_builder{
				Id:       storageTierId,
				Metadata: privatev1.Metadata_builder{Name: storageTierId}.Build(),
				Spec: privatev1.StorageTierSpec_builder{
					Description: "Resize test storage tier",
					Protocol:    privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
					Backends: []*privatev1.BackendAssociation{
						privatev1.BackendAssociation_builder{BackendId: storageBackendId}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		instanceTypeId = fmt.Sprintf("test-resize-it-%s", uuid.New())
		_, err = instanceTypesClient.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id:       instanceTypeId,
				Metadata: privatev1.Metadata_builder{Name: instanceTypeId}.Build(),
				Spec: privatev1.InstanceTypeSpec_builder{
					Vcpus:     2,
					MemoryGib: 4,
					State:     privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		diskImageId = fmt.Sprintf("test-resize-di-%s", uuid.New())
		_, err = diskImagesClient.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
			Object: privatev1.DiskImage_builder{
				Id:       diskImageId,
				Metadata: privatev1.Metadata_builder{Name: diskImageId}.Build(),
				Spec: privatev1.DiskImageSpec_builder{
					SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
					SourceRef:     "quay.io/containerdisks/fedora:41",
					GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
					Architecture:  []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		computeInstanceTemplateId = fmt.Sprintf("test-resize-template-%s", uuid.New())
		_, err = computeInstanceTemplatesClient.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
			Object: privatev1.ComputeInstanceTemplate_builder{
				Id:          computeInstanceTemplateId,
				Title:       "Resize test template",
				Description: "Template for InstanceType resize tests",
				Metadata:    privatev1.Metadata_builder{Name: computeInstanceTemplateId}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		networkClassName := fmt.Sprintf("test-resize-nc-%s", uuid.New())
		ncResp, err := networkClassesClient.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Metadata:      privatev1.Metadata_builder{Name: networkClassName}.Build(),
				Title:         "Resize test network class",
				FabricManager: new("netris"),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassId = ncResp.GetObject().GetId()
		waitForComputeInstanceFixtureResource(ctx, func(probeCtx context.Context) error {
			_, err := networkClassesClient.Get(probeCtx, privatev1.NetworkClassesGetRequest_builder{Id: networkClassId}.Build())
			return err
		})

		virtualNetworkId = fmt.Sprintf("test-resize-vn-%s", uuid.New())
		_, err = virtualNetworksClient.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Id:       virtualNetworkId,
				Metadata: privatev1.Metadata_builder{Name: virtualNetworkId, Tenant: usersGroup}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: networkClassId}.Build(),
					Region:       "us-east-1",
					Ipv4Cidr:     new("10.110.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		// Set VirtualNetwork to READY state via private Update API.
		// In IT environment there is no osac-operator/feedback controller to reconcile state.
		setComputeInstanceFixtureVirtualNetworkReady(ctx, virtualNetworksClient, virtualNetworkId)

		subnetId = fmt.Sprintf("test-resize-subnet-%s", uuid.New())
		_, err = subnetsClient.Create(ctx, privatev1.SubnetsCreateRequest_builder{
			Object: privatev1.Subnet_builder{
				Id:       subnetId,
				Metadata: privatev1.Metadata_builder{Name: subnetId, Tenant: usersGroup}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
					Ipv4Cidr:       new("10.110.1.0/24"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		Eventually(func(g Gomega) {
			response, getErr := subnetsClient.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetId}.Build())
			g.Expect(getErr).ToNot(HaveOccurred())
			g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())

		subnetResponse, err := subnetsClient.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetId}.Build())
		Expect(err).ToNot(HaveOccurred())
		subnet := subnetResponse.GetObject()
		subnet.SetStatus(privatev1.SubnetStatus_builder{State: privatev1.SubnetState_SUBNET_STATE_READY}.Build())
		_, err = subnetsClient.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
			Object:     subnet,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		cleanupComputeInstanceFixture(ctx, fixtureClients, computeInstanceId, resizeInstanceTypeId, instanceTypeId,
			subnetId, virtualNetworkId, networkClassId, computeInstanceTemplateId, diskImageId, storageTierId, storageBackendId)
	})

	It("updates ComputeInstance InstanceType through the public API", func() {
		resizeInstanceTypeId = fmt.Sprintf("test-resize-target-%s", uuid.New())
		_, err := instanceTypesClient.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id:       resizeInstanceTypeId,
				Metadata: privatev1.Metadata_builder{Name: resizeInstanceTypeId}.Build(),
				Spec: privatev1.InstanceTypeSpec_builder{
					Vcpus:     4,
					MemoryGib: 8,
					State:     privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		updateCtx, cancel := context.WithTimeout(ctx, computeInstanceFixtureProbeTimeout)
		defer cancel()
		_, err = instanceTypesClient.Update(updateCtx, privatev1.InstanceTypesUpdateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Id:       resizeInstanceTypeId,
				Metadata: privatev1.Metadata_builder{Name: resizeInstanceTypeId}.Build(),
				Spec: privatev1.InstanceTypeSpec_builder{
					State: privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		computeInstanceId = fmt.Sprintf("test-resize-ci-%s", uuid.New())
		createResponse, err := computeInstancesClient.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Id:       computeInstanceId,
				Metadata: publicv1.Metadata_builder{Name: computeInstanceId}.Build(),
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
		Expect(err).ToNot(HaveOccurred())
		createdObject := createResponse.GetObject()

		updateResponse, err := computeInstancesClient.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Id: computeInstanceId,
				Spec: publicv1.ComputeInstanceSpec_builder{
					InstanceType: publicv1.InstanceTypeReference_builder{Name: resizeInstanceTypeId}.Build(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.instance_type"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResponse.GetObject().GetSpec().GetInstanceType().GetName()).To(Equal(resizeInstanceTypeId))
		Expect(updateResponse.GetWarnings()).To(HaveLen(1))

		getResponse, err := computeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: computeInstanceId}.Build())
		Expect(err).ToNot(HaveOccurred())
		updatedObject := getResponse.GetObject()
		Expect(updatedObject.GetSpec().GetInstanceType().GetName()).To(Equal(resizeInstanceTypeId))
		Expect(proto.Equal(updatedObject.GetSpec().GetTemplate(), createdObject.GetSpec().GetTemplate())).To(BeTrue())
		Expect(updatedObject.GetSpec().GetRunStrategy()).To(Equal(createdObject.GetSpec().GetRunStrategy()))
		Expect(proto.Equal(updatedObject.GetSpec().GetBootDisk(), createdObject.GetSpec().GetBootDisk())).To(BeTrue())
		Expect(proto.Equal(updatedObject.GetSpec().GetDiskImage(), createdObject.GetSpec().GetDiskImage())).To(BeTrue())
		updatedAttachments := updatedObject.GetSpec().GetNetworkAttachments()
		createdAttachments := createdObject.GetSpec().GetNetworkAttachments()
		Expect(updatedAttachments).To(HaveLen(len(createdAttachments)))
		if len(updatedAttachments) == len(createdAttachments) {
			for i := range createdAttachments {
				Expect(proto.Equal(updatedAttachments[i], createdAttachments[i])).To(BeTrue())
			}
		}
	})
})
