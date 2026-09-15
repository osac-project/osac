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

		storageBackendId          string
		storageTierId             string
		instanceTypeId            string
		computeInstanceTemplateId string
		diskImageId               string
		computeInstanceId         string
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
					Cores:     2,
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
	})

	AfterEach(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()

		if computeInstanceId != "" {
			if _, err := computeInstancesClient.Delete(cleanupCtx, publicv1.ComputeInstancesDeleteRequest_builder{
				Id: computeInstanceId,
			}.Build()); err != nil {
				GinkgoT().Logf("cleanup: failed to delete ComputeInstance %s: %v", computeInstanceId, err)
			}
			computeInstanceId = ""
		}
		if diskImageId != "" {
			if _, err := diskImagesClient.Delete(cleanupCtx, privatev1.DiskImagesDeleteRequest_builder{
				Id: diskImageId,
			}.Build()); err != nil {
				GinkgoT().Logf("cleanup: failed to delete DiskImage %s: %v", diskImageId, err)
			}
			diskImageId = ""
		}
		if computeInstanceTemplateId != "" {
			if _, err := computeInstanceTemplatesClient.Delete(cleanupCtx, privatev1.ComputeInstanceTemplatesDeleteRequest_builder{
				Id: computeInstanceTemplateId,
			}.Build()); err != nil {
				GinkgoT().Logf("cleanup: failed to delete ComputeInstanceTemplate %s: %v", computeInstanceTemplateId, err)
			}
			computeInstanceTemplateId = ""
		}
		if instanceTypeId != "" {
			if _, err := instanceTypesClient.Delete(cleanupCtx, privatev1.InstanceTypesDeleteRequest_builder{
				Id: instanceTypeId,
			}.Build()); err != nil {
				GinkgoT().Logf("cleanup: failed to delete InstanceType %s: %v", instanceTypeId, err)
			}
			instanceTypeId = ""
		}
		if storageTierId != "" {
			if _, err := storageTiersClient.Delete(cleanupCtx, privatev1.StorageTiersDeleteRequest_builder{
				Id: storageTierId,
			}.Build()); err != nil {
				GinkgoT().Logf("cleanup: failed to delete StorageTier %s: %v", storageTierId, err)
			}
			storageTierId = ""
		}
		if storageBackendId != "" {
			if _, err := storageBackendsClient.Delete(cleanupCtx, privatev1.StorageBackendsDeleteRequest_builder{
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
