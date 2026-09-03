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
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("Volume lifecycle", func() {
	var (
		ctx context.Context

		// Volumes are private-only (no public API), served on the internal
		// endpoint, which is admin-authorized. A regular user token is rejected
		// with PermissionDenied there; admin defaults to the "shared" tenant,
		// which cannot hold tenant-scoped objects. So we use the admin conn and
		// set metadata.tenant explicitly to a real, provisioned tenant.
		volumesClient         privatev1.VolumesClient
		storageBackendsClient privatev1.StorageBackendsClient
		storageTiersClient    privatev1.StorageTiersClient

		kubeClient crclient.Client

		backendId string
		tierId    string
		tierName  string
	)

	// "users" is a tenant the test tool provisions and waits for SYNCED
	// (see createTenants/usersGroup in it_tool.go). Admin has universal ("*")
	// tenant access, so it may create objects in this tenant.
	const testTenant = "users"
	const testBackendPassword = "test-password"

	BeforeEach(func() {
		ctx = context.Background()

		volumesClient = privatev1.NewVolumesClient(tool.InternalView().AdminConn())
		storageBackendsClient = privatev1.NewStorageBackendsClient(tool.InternalView().AdminConn())
		storageTiersClient = privatev1.NewStorageTiersClient(tool.InternalView().AdminConn())
		kubeClient = tool.KubeClient()

		// --- Seed a StorageBackend (global admin resource, no tenant) ---
		backendName := fmt.Sprintf("test-backend-%s", uuid.New()[24:32])
		backendResp, err := storageBackendsClient.Create(ctx,
			privatev1.StorageBackendsCreateRequest_builder{
				Object: privatev1.StorageBackend_builder{
					Metadata: privatev1.Metadata_builder{
						Name: backendName,
					}.Build(),
					Spec: privatev1.StorageBackendSpec_builder{
						Provider: "test-provider",
						Endpoint: "https://test-storage:8443",
						Credentials: privatev1.StorageBackendCredentials_builder{
							Username: "admin",
							Password: testBackendPassword,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
		Expect(err).ToNot(HaveOccurred())
		backendId = backendResp.GetObject().GetId()

		DeferCleanup(func() {
			_, _ = storageBackendsClient.Delete(ctx,
				privatev1.StorageBackendsDeleteRequest_builder{
					Id: backendId,
				}.Build())
		})

		// --- Seed a StorageTier with backend association ---
		// Note: the storage tiers server always sets status.state = ACTIVE on
		// create, regardless of any caller-provided state.
		tierName = fmt.Sprintf("test-tier-%s", uuid.New()[24:32])
		tierResp, err := storageTiersClient.Create(ctx,
			privatev1.StorageTiersCreateRequest_builder{
				Object: privatev1.StorageTier_builder{
					Metadata: privatev1.Metadata_builder{
						Name: tierName,
					}.Build(),
					Spec: privatev1.StorageTierSpec_builder{
						Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
						Backends: []*privatev1.BackendAssociation{
							privatev1.BackendAssociation_builder{
								BackendId: backendId,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
		Expect(err).ToNot(HaveOccurred())
		tierId = tierResp.GetObject().GetId()

		DeferCleanup(func() {
			_, _ = storageTiersClient.Delete(ctx,
				privatev1.StorageTiersDeleteRequest_builder{
					Id: tierId,
				}.Build())
		})
	})

	// =========================================================================
	// Phase 1 — Volume Lifecycle (happy path)
	// =========================================================================

	It("should create and delete a Volume end-to-end", func() {
		volName := fmt.Sprintf("test-vol-%s", uuid.New()[24:32])

		// 1. CREATE volume (admin conn + explicit tenant)
		createResp, err := volumesClient.Create(ctx,
			privatev1.VolumesCreateRequest_builder{
				Object: privatev1.Volume_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   volName,
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.VolumeSpec_builder{
						StorageTier: tierName,
						SizeGib:     10,
						AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build(),
			}.Build())
		Expect(err).ToNot(HaveOccurred())

		volId := createResp.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = volumesClient.Delete(ctx,
				privatev1.VolumesDeleteRequest_builder{
					Id: volId,
				}.Build())
		})

		// 2. VERIFY status fields set synchronously by the tier resolver during Create.
		//    (hub is NOT set here — it is assigned asynchronously by the reconciler.)
		getResp, err := volumesClient.Get(ctx,
			privatev1.VolumesGetRequest_builder{
				Id: volId,
			}.Build())
		Expect(err).ToNot(HaveOccurred())
		vol := getResp.GetObject()
		Expect(vol.GetStatus().GetState()).To(Equal(privatev1.VolumeState_VOLUME_STATE_CREATING))
		Expect(vol.GetStatus().GetBackend()).To(Equal(backendId))
		Expect(vol.GetStatus().GetProtocol()).To(Equal(privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK))

		// 2b. VERIFY hub is assigned asynchronously by the reconciler (selectHub → SetHub).
		Eventually(func(g Gomega) {
			opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			resp, err := volumesClient.Get(opCtx,
				privatev1.VolumesGetRequest_builder{
					Id: volId,
				}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetHub()).ToNot(BeEmpty())
		}, time.Minute, time.Second).Should(Succeed())

		// 3. VERIFY Volume CR appears on hub (async — reconciler creates it)
		Eventually(func(g Gomega) {
			opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			crList := &osacv1alpha1.VolumeList{}
			g.Expect(kubeClient.List(opCtx, crList,
				crclient.MatchingLabels{labels.VolumeUuid: volId},
			)).To(Succeed())
			g.Expect(crList.Items).To(HaveLen(1))

			cr := crList.Items[0]
			g.Expect(cr.Spec.StorageTier).To(Equal(tierName))
			g.Expect(cr.Spec.SizeGiB).To(Equal(int64(10)))
			g.Expect(string(cr.Spec.AccessMode)).To(ContainSubstring("ReadWriteOnce"))
		}, time.Minute, time.Second).Should(Succeed())

		// 4. FEEDBACK — simulate controller updating status to AVAILABLE
		feedbackResp, err := volumesClient.Get(ctx,
			privatev1.VolumesGetRequest_builder{
				Id: volId,
			}.Build())
		Expect(err).ToNot(HaveOccurred())

		volObj := feedbackResp.GetObject()
		volObj.GetStatus().SetState(privatev1.VolumeState_VOLUME_STATE_AVAILABLE)
		volObj.GetStatus().SetVendorVolumeId("test-vendor-vol-123")

		_, err = volumesClient.Update(ctx,
			privatev1.VolumesUpdateRequest_builder{
				Object: volObj,
			}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = volumesClient.Signal(ctx,
			privatev1.VolumesSignalRequest_builder{
				Id: volId,
			}.Build())
		Expect(err).ToNot(HaveOccurred())

		Eventually(func(g Gomega) {
			opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			resp, err := volumesClient.Get(opCtx,
				privatev1.VolumesGetRequest_builder{
					Id: volId,
				}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.VolumeState_VOLUME_STATE_AVAILABLE))
			g.Expect(resp.GetObject().GetStatus().GetVendorVolumeId()).To(Equal("test-vendor-vol-123"))
		}, time.Minute, time.Second).Should(Succeed())

		// 5. DELETE volume → verify CR removed from hub
		_, err = volumesClient.Delete(ctx,
			privatev1.VolumesDeleteRequest_builder{
				Id: volId,
			}.Build())
		Expect(err).ToNot(HaveOccurred())

		Eventually(func(g Gomega) {
			opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			crList := &osacv1alpha1.VolumeList{}
			g.Expect(kubeClient.List(opCtx, crList,
				crclient.MatchingLabels{labels.VolumeUuid: volId},
			)).To(Succeed())
			g.Expect(crList.Items).To(BeEmpty())
		}, time.Minute, time.Second).Should(Succeed())
	})

	// =========================================================================
	// Phase 2 — Tier Resolver Coverage
	// =========================================================================

	Describe("Tier resolution", func() {

		It("should fail with NotFound for non-existent tier", func() {
			volName := fmt.Sprintf("test-vol-%s", uuid.New()[24:32])
			_, err := volumesClient.Create(ctx,
				privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Metadata: privatev1.Metadata_builder{
							Name:   volName,
							Tenant: testTenant,
						}.Build(),
						Spec: privatev1.VolumeSpec_builder{
							StorageTier: "tier-does-not-exist",
							SizeGib:     10,
							AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
						}.Build(),
					}.Build(),
				}.Build())
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
		})

		// Note: two tier-resolver error branches are NOT reachable through the
		// gRPC API, so they are not covered here (they belong to unit tests):
		//   - "tier not ACTIVE" (FailedPrecondition): the storage tiers server
		//     always sets status.state = ACTIVE on create, regardless of the
		//     caller-provided state — an INACTIVE tier cannot be created.
		//   - "tier with zero backends" (FailedPrecondition): StorageTier proto
		//     validation requires spec.backends to be non-empty (InvalidArgument
		//     on create) — a backend-less tier cannot be created.

		It("should resolve the correct tier among multiple tiers", func() {
			// Seed a second backend + NFS tier
			backend2Name := fmt.Sprintf("test-backend-2-%s", uuid.New()[24:32])
			backend2Resp, err := storageBackendsClient.Create(ctx,
				privatev1.StorageBackendsCreateRequest_builder{
					Object: privatev1.StorageBackend_builder{
						Metadata: privatev1.Metadata_builder{
							Name: backend2Name,
						}.Build(),
						Spec: privatev1.StorageBackendSpec_builder{
							Provider: "test-provider-nfs",
							Endpoint: "https://test-nfs:2049",
							Credentials: privatev1.StorageBackendCredentials_builder{
								Username: "admin",
								Password: testBackendPassword,
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			backend2Id := backend2Resp.GetObject().GetId()
			DeferCleanup(func() {
				_, _ = storageBackendsClient.Delete(ctx,
					privatev1.StorageBackendsDeleteRequest_builder{
						Id: backend2Id,
					}.Build())
			})

			tier2Name := fmt.Sprintf("test-tier-nfs-%s", uuid.New()[24:32])
			tier2Resp, err := storageTiersClient.Create(ctx,
				privatev1.StorageTiersCreateRequest_builder{
					Object: privatev1.StorageTier_builder{
						Metadata: privatev1.Metadata_builder{
							Name: tier2Name,
						}.Build(),
						Spec: privatev1.StorageTierSpec_builder{
							Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
							Backends: []*privatev1.BackendAssociation{
								privatev1.BackendAssociation_builder{
									BackendId: backend2Id,
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() {
				_, _ = storageTiersClient.Delete(ctx,
					privatev1.StorageTiersDeleteRequest_builder{
						Id: tier2Resp.GetObject().GetId(),
					}.Build())
			})

			// Create volume with NFS tier → should resolve to backend2 + NFS
			vol2Name := fmt.Sprintf("test-vol-%s", uuid.New()[24:32])
			vol2Resp, err := volumesClient.Create(ctx,
				privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Metadata: privatev1.Metadata_builder{
							Name:   vol2Name,
							Tenant: testTenant,
						}.Build(),
						Spec: privatev1.VolumeSpec_builder{
							StorageTier: tier2Name,
							SizeGib:     20,
							AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
						}.Build(),
					}.Build(),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			vol2Id := vol2Resp.GetObject().GetId()
			DeferCleanup(func() {
				_, _ = volumesClient.Delete(ctx,
					privatev1.VolumesDeleteRequest_builder{
						Id: vol2Id,
					}.Build())
			})

			vol2Get, err := volumesClient.Get(ctx,
				privatev1.VolumesGetRequest_builder{Id: vol2Id}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(vol2Get.GetObject().GetStatus().GetBackend()).To(Equal(backend2Id))
			Expect(vol2Get.GetObject().GetStatus().GetProtocol()).To(Equal(privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS))

			// Create volume with original BLOCK tier → should resolve to original backend
			vol1Name := fmt.Sprintf("test-vol-%s", uuid.New()[24:32])
			vol1Resp, err := volumesClient.Create(ctx,
				privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Metadata: privatev1.Metadata_builder{
							Name:   vol1Name,
							Tenant: testTenant,
						}.Build(),
						Spec: privatev1.VolumeSpec_builder{
							StorageTier: tierName,
							SizeGib:     5,
							AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
						}.Build(),
					}.Build(),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			vol1Id := vol1Resp.GetObject().GetId()
			DeferCleanup(func() {
				_, _ = volumesClient.Delete(ctx,
					privatev1.VolumesDeleteRequest_builder{
						Id: vol1Id,
					}.Build())
			})

			vol1Get, err := volumesClient.Get(ctx,
				privatev1.VolumesGetRequest_builder{Id: vol1Id}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(vol1Get.GetObject().GetStatus().GetBackend()).To(Equal(backendId))
			Expect(vol1Get.GetObject().GetStatus().GetProtocol()).To(Equal(privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK))
		})
	})

	// =========================================================================
	// Phase 3 — Validation Tests
	// =========================================================================

	Describe("Volume validation", func() {

		It("should reject volume without spec", func() {
			_, err := volumesClient.Create(ctx,
				privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Metadata: privatev1.Metadata_builder{
							Name:   "vol-no-spec",
							Tenant: testTenant,
						}.Build(),
					}.Build(),
				}.Build())
			Expect(err).To(HaveOccurred())
		})

		It("should reject volume with size_gib = 0", func() {
			_, err := volumesClient.Create(ctx,
				privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Metadata: privatev1.Metadata_builder{
							Name:   "vol-bad-size",
							Tenant: testTenant,
						}.Build(),
						Spec: privatev1.VolumeSpec_builder{
							StorageTier: tierName,
							SizeGib:     0,
							AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
						}.Build(),
					}.Build(),
				}.Build())
			Expect(err).To(HaveOccurred())
		})

		It("should reject volume with negative size_gib", func() {
			_, err := volumesClient.Create(ctx,
				privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Metadata: privatev1.Metadata_builder{
							Name:   "vol-bad-size",
							Tenant: testTenant,
						}.Build(),
						Spec: privatev1.VolumeSpec_builder{
							StorageTier: tierName,
							SizeGib:     -1,
							AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
						}.Build(),
					}.Build(),
				}.Build())
			Expect(err).To(HaveOccurred())
		})

		It("should reject volume with UNSPECIFIED access_mode", func() {
			_, err := volumesClient.Create(ctx,
				privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Metadata: privatev1.Metadata_builder{
							Name:   "vol-bad-access",
							Tenant: testTenant,
						}.Build(),
						Spec: privatev1.VolumeSpec_builder{
							StorageTier: tierName,
							SizeGib:     10,
							AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_UNSPECIFIED,
						}.Build(),
					}.Build(),
				}.Build())
			Expect(err).To(HaveOccurred())
		})

		It("should reject update that changes immutable storage_tier", func() {
			// Seed a SECOND valid tier so the rejection proves immutability,
			// not just "target tier doesn't exist". Reuses backendId.
			otherTierName := fmt.Sprintf("test-tier-alt-%s", uuid.New()[24:32])
			otherTierResp, err := storageTiersClient.Create(ctx,
				privatev1.StorageTiersCreateRequest_builder{
					Object: privatev1.StorageTier_builder{
						Metadata: privatev1.Metadata_builder{Name: otherTierName}.Build(),
						Spec: privatev1.StorageTierSpec_builder{
							Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
							Backends: []*privatev1.BackendAssociation{
								privatev1.BackendAssociation_builder{
									BackendId: backendId,
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() {
				_, _ = storageTiersClient.Delete(ctx,
					privatev1.StorageTiersDeleteRequest_builder{
						Id: otherTierResp.GetObject().GetId(),
					}.Build())
			})

			volName := fmt.Sprintf("test-vol-%s", uuid.New()[24:32])
			createResp, err := volumesClient.Create(ctx,
				privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Metadata: privatev1.Metadata_builder{
							Name:   volName,
							Tenant: testTenant,
						}.Build(),
						Spec: privatev1.VolumeSpec_builder{
							StorageTier: tierName,
							SizeGib:     10,
							AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
						}.Build(),
					}.Build(),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			volId := createResp.GetObject().GetId()
			DeferCleanup(func() {
				_, _ = volumesClient.Delete(ctx,
					privatev1.VolumesDeleteRequest_builder{
						Id: volId,
					}.Build())
			})

			getResp, err := volumesClient.Get(ctx,
				privatev1.VolumesGetRequest_builder{
					Id: volId,
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			volObj := getResp.GetObject()
			volObj.GetSpec().SetStorageTier(otherTierName)
			_, err = volumesClient.Update(ctx,
				privatev1.VolumesUpdateRequest_builder{
					Object: volObj,
				}.Build())
			Expect(err).To(HaveOccurred())

			afterResp, err := volumesClient.Get(ctx,
				privatev1.VolumesGetRequest_builder{
					Id: volId,
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(afterResp.GetObject().GetSpec().GetStorageTier()).To(Equal(tierName))
		})
	})
})
