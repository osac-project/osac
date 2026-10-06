/*
Copyright (c) 2026 Red Hat Inc.

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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("DiskImage lifecycle", func() {
	// testCredential is a dummy credential used only for the fake StorageBackend
	// created within this suite; it never authenticates against a real service.
	const testCredential = "test-credential" //nolint:goconst // test-only dummy credential for fake provider

	var (
		ctx context.Context

		diskImagesClient               privatev1.DiskImagesClient
		computeInstancesClient         publicv1.ComputeInstancesClient
		computeInstanceTemplatesClient privatev1.ComputeInstanceTemplatesClient
		instanceTypesClient            privatev1.InstanceTypesClient
		storageTiersClient             privatev1.StorageTiersClient
		storageBackendsClient          privatev1.StorageBackendsClient
		networkClassesClient           privatev1.NetworkClassesClient
		virtualNetworksClient          privatev1.VirtualNetworksClient
		subnetsClient                  privatev1.SubnetsClient

		storageBackendId          string
		storageTierId             string
		instanceTypeId            string
		computeInstanceTemplateId string
		diskImageId               string
		computeInstanceId         string
		networkClassId            string
		virtualNetworkId          string
		subnetId                  string
	)

	BeforeEach(func() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 3*time.Minute)
		DeferCleanup(cancel)

		diskImagesClient = privatev1.NewDiskImagesClient(tool.InternalView().AdminConn())
		computeInstancesClient = publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
		computeInstanceTemplatesClient = privatev1.NewComputeInstanceTemplatesClient(tool.InternalView().AdminConn())
		instanceTypesClient = privatev1.NewInstanceTypesClient(tool.InternalView().AdminConn())
		storageTiersClient = privatev1.NewStorageTiersClient(tool.InternalView().AdminConn())
		storageBackendsClient = privatev1.NewStorageBackendsClient(tool.InternalView().AdminConn())
		networkClassesClient = privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn())
		virtualNetworksClient = privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn())
		subnetsClient = privatev1.NewSubnetsClient(tool.InternalView().AdminConn())

		// Create StorageBackend
		sbResp, err := storageBackendsClient.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{
			Object: privatev1.StorageBackend_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-sb-%s", uuid.New()),
				}.Build(),
				Spec: privatev1.StorageBackendSpec_builder{
					Provider:    "test",
					Description: "Test storage backend for disk image lifecycle tests",
					Endpoint:    "https://test-backend.example.com",
					Credentials: privatev1.StorageBackendCredentials_builder{
						Username: "test-user",
						Password: testCredential,
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		storageBackendId = sbResp.GetObject().GetId()

		// Create StorageTier
		stResp, err := storageTiersClient.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
			Object: privatev1.StorageTier_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-st-%s", uuid.New()),
				}.Build(),
				Spec: privatev1.StorageTierSpec_builder{
					Description: "Test storage tier for disk image lifecycle tests",
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

		// Create ComputeInstanceTemplate
		computeInstanceTemplateId = fmt.Sprintf("test-ci-template-%s", uuid.New())
		_, err = computeInstanceTemplatesClient.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
			Object: privatev1.ComputeInstanceTemplate_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-ci-tmpl-%s", uuid.New()[24:32]),
				}.Build(),
				Id:          computeInstanceTemplateId,
				Title:       "Test CI Template",
				Description: "Template for disk image lifecycle test.",
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Create NetworkClass — required for VirtualNetwork/Subnet chain so the
		// ComputeInstance has a valid network attachment (the server rejects
		// creation when no default subnet exists and no attachments are provided).
		ncResp, err := networkClassesClient.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Metadata:      privatev1.Metadata_builder{Name: fmt.Sprintf("di-nc-%s", uuid.New())}.Build(),
				Title:         "Test Network Class",
				FabricManager: new("netris"),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassId = ncResp.GetObject().GetId()
		waitForNetworkClassReady(ctx, networkClassesClient, networkClassId)

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
					Ipv4Cidr:     new("10.200.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Wait for the VN reconciler to finish initial processing before
		// overriding state.
		Eventually(func(g Gomega) {
			resp, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
				Id: virtualNetworkId,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())

		// Set VirtualNetwork to READY state via private Update API.
		// In IT environment there is no osac-operator/feedback controller to reconcile state.
		vnGetResp, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
			Id: virtualNetworkId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		vn := vnGetResp.GetObject()
		vn.SetStatus(privatev1.VirtualNetworkStatus_builder{
			State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
		}.Build())
		_, err = virtualNetworksClient.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
			Object:     vn,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

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
					Ipv4Cidr:       new("10.200.1.0/24"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Wait for the subnet reconciler to finish initial processing before
		// overriding state.
		Eventually(func(g Gomega) {
			resp, err := subnetsClient.Get(ctx, privatev1.SubnetsGetRequest_builder{
				Id: subnetId,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())

		// Set Subnet to READY state via private Update API.
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
	})

	AfterEach(func() {
		if computeInstanceId != "" {
			delCtx, delCancel := context.WithTimeout(context.Background(), time.Minute)
			defer delCancel()
			if _, err := computeInstancesClient.Delete(delCtx, publicv1.ComputeInstancesDeleteRequest_builder{
				Id: computeInstanceId,
			}.Build()); err != nil {
				GinkgoT().Logf("cleanup: failed to delete ComputeInstance %s: %v", computeInstanceId, err)
			}
			computeInstanceId = ""
		}
		if diskImageId != "" {
			delCtx, delCancel := context.WithTimeout(context.Background(), time.Minute)
			defer delCancel()
			if _, err := diskImagesClient.Delete(delCtx, privatev1.DiskImagesDeleteRequest_builder{
				Id: diskImageId,
			}.Build()); err != nil {
				GinkgoT().Logf("cleanup: failed to delete DiskImage %s: %v", diskImageId, err)
			}
			diskImageId = ""
		}
		if subnetId != "" {
			delCtx, delCancel := context.WithTimeout(context.Background(), time.Minute)
			defer delCancel()
			if _, err := subnetsClient.Delete(delCtx, privatev1.SubnetsDeleteRequest_builder{
				Id: subnetId,
			}.Build()); err != nil {
				GinkgoT().Logf("cleanup: failed to delete Subnet %s: %v", subnetId, err)
			}
			subnetId = ""
		}
		if virtualNetworkId != "" {
			delCtx, delCancel := context.WithTimeout(context.Background(), time.Minute)
			defer delCancel()
			if _, err := virtualNetworksClient.Delete(delCtx, privatev1.VirtualNetworksDeleteRequest_builder{
				Id: virtualNetworkId,
			}.Build()); err != nil {
				GinkgoT().Logf("cleanup: failed to delete VirtualNetwork %s: %v", virtualNetworkId, err)
			}
			virtualNetworkId = ""
		}
		if networkClassId != "" {
			delCtx, delCancel := context.WithTimeout(context.Background(), time.Minute)
			defer delCancel()
			if _, err := networkClassesClient.Delete(delCtx, privatev1.NetworkClassesDeleteRequest_builder{
				Id: networkClassId,
			}.Build()); err != nil {
				GinkgoT().Logf("cleanup: failed to delete NetworkClass %s: %v", networkClassId, err)
			}
			networkClassId = ""
		}
		if computeInstanceTemplateId != "" {
			delCtx, delCancel := context.WithTimeout(context.Background(), time.Minute)
			defer delCancel()
			if _, err := computeInstanceTemplatesClient.Delete(delCtx, privatev1.ComputeInstanceTemplatesDeleteRequest_builder{
				Id: computeInstanceTemplateId,
			}.Build()); err != nil {
				GinkgoT().Logf("cleanup: failed to delete ComputeInstanceTemplate %s: %v", computeInstanceTemplateId, err)
			}
			computeInstanceTemplateId = ""
		}
		if instanceTypeId != "" {
			delCtx, delCancel := context.WithTimeout(context.Background(), time.Minute)
			defer delCancel()
			if _, err := instanceTypesClient.Delete(delCtx, privatev1.InstanceTypesDeleteRequest_builder{
				Id: instanceTypeId,
			}.Build()); err != nil {
				GinkgoT().Logf("cleanup: failed to delete InstanceType %s: %v", instanceTypeId, err)
			}
			instanceTypeId = ""
		}
		if storageTierId != "" {
			delCtx, delCancel := context.WithTimeout(context.Background(), time.Minute)
			defer delCancel()
			if _, err := storageTiersClient.Delete(delCtx, privatev1.StorageTiersDeleteRequest_builder{
				Id: storageTierId,
			}.Build()); err != nil {
				GinkgoT().Logf("cleanup: failed to delete StorageTier %s: %v", storageTierId, err)
			}
			storageTierId = ""
		}
		if storageBackendId != "" {
			delCtx, delCancel := context.WithTimeout(context.Background(), time.Minute)
			defer delCancel()
			if _, err := storageBackendsClient.Delete(delCtx, privatev1.StorageBackendsDeleteRequest_builder{
				Id: storageBackendId,
			}.Build()); err != nil {
				GinkgoT().Logf("cleanup: failed to delete StorageBackend %s: %v", storageBackendId, err)
			}
			storageBackendId = ""
		}
	})

	It("deprecated DiskImage allows ComputeInstance creation with warning", func() {
		By("Creating a DiskImage")
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

		By("Deprecating the DiskImage (lifecycle → DEPRECATED)")
		diGetResp, err := diskImagesClient.Get(ctx, privatev1.DiskImagesGetRequest_builder{
			Id: diskImageId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		diObj := diGetResp.GetObject()
		diObj.GetSpec().SetLifecycle(privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED)
		_, err = diskImagesClient.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
			Object:     diObj,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.lifecycle"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		By("Verifying the DiskImage is now DEPRECATED")
		diGetResp, err = diskImagesClient.Get(ctx, privatev1.DiskImagesGetRequest_builder{
			Id: diskImageId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(diGetResp.GetObject().GetSpec().GetLifecycle()).To(
			Equal(privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED))

		By("Creating a ComputeInstance referencing the deprecated DiskImage")
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
					DiskImage: publicv1.DiskImageReference_builder{Id: diskImageId}.Build(),
					NetworkAttachments: []*publicv1.ComputeNetworkAttachment{
						publicv1.ComputeNetworkAttachment_builder{
							Subnet: publicv1.SubnetLocalReference_builder{Id: subnetId}.Build(),
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred(), "CI creation with DEPRECATED DiskImage should succeed")
		Expect(createResp.GetObject()).ToNot(BeNil())

		By("Verifying the response contains a deprecation warning")
		warnings := createResp.GetWarnings()
		hasDeprecationWarning := false
		for _, w := range warnings {
			if strings.Contains(strings.ToLower(w), "deprecated") {
				hasDeprecationWarning = true
				break
			}
		}
		Expect(hasDeprecationWarning).To(BeTrue(),
			fmt.Sprintf("Response should contain deprecation warning, got: %v", warnings))
	})
})
