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
	"maps"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Public volumes API", func() {
	var (
		ctx context.Context

		publicVolumesClient   publicv1.VolumesClient
		privateVolumesClient  privatev1.VolumesClient
		storageBackendsClient privatev1.StorageBackendsClient
		storageTiersClient    privatev1.StorageTiersClient

		backendId string
		tierId    string
		tierName  string
	)

	const testBackendPassword = "test-password"

	BeforeEach(func() {
		ctx = context.Background()

		publicVolumesClient = publicv1.NewVolumesClient(tool.ExternalView().UserConn())
		privateVolumesClient = privatev1.NewVolumesClient(tool.InternalView().AdminConn())
		storageBackendsClient = privatev1.NewStorageBackendsClient(tool.InternalView().AdminConn())
		storageTiersClient = privatev1.NewStorageTiersClient(tool.InternalView().AdminConn())

		// --- Seed a StorageBackend ---
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

		DeferCleanup(func(ctx SpecContext) {
			_, _ = storageBackendsClient.Delete(ctx,
				privatev1.StorageBackendsDeleteRequest_builder{
					Id: backendId,
				}.Build())
		})

		// --- Seed a StorageTier ---
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

		DeferCleanup(func(ctx SpecContext) {
			_, _ = storageTiersClient.Delete(ctx,
				privatev1.StorageTiersDeleteRequest_builder{
					Id: tierId,
				}.Build())
		})
	})

	It("Creates, gets, and deletes a volume via the public API", func() {
		volName := fmt.Sprintf("pub-vol-%s", uuid.New()[24:32])

		// Create
		createResp, err := publicVolumesClient.Create(ctx,
			publicv1.VolumesCreateRequest_builder{
				Object: publicv1.Volume_builder{
					Metadata: publicv1.Metadata_builder{
						Name: volName,
					}.Build(),
					Spec: publicv1.VolumeSpec_builder{
						StorageTier: tierName,
						SizeGib:     10,
						AccessMode:  publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build(),
			}.Build())
		Expect(err).ToNot(HaveOccurred())
		volId := createResp.GetObject().GetId()
		Expect(volId).ToNot(BeEmpty())

		DeferCleanup(func(ctx SpecContext) {
			_, _ = publicVolumesClient.Delete(ctx,
				publicv1.VolumesDeleteRequest_builder{
					Id: volId,
				}.Build())
		})

		// Get
		getResp, err := publicVolumesClient.Get(ctx,
			publicv1.VolumesGetRequest_builder{
				Id: volId,
			}.Build())
		Expect(err).ToNot(HaveOccurred())

		vol := getResp.GetObject()
		Expect(vol.GetMetadata().GetName()).To(Equal(volName))
		Expect(vol.GetSpec().GetStorageTier()).To(Equal(tierName))
		Expect(vol.GetSpec().GetSizeGib()).To(Equal(int64(10)))
		Expect(vol.GetStatus().GetState()).To(Equal(
			publicv1.VolumeState_VOLUME_STATE_CREATING))

		// Delete
		_, err = publicVolumesClient.Delete(ctx,
			publicv1.VolumesDeleteRequest_builder{
				Id: volId,
			}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Verify deleted — volume deletion may be async, so retry until the
		// resource is no longer found rather than asserting immediately.
		Eventually(func(g Gomega) {
			_, err := publicVolumesClient.Get(ctx,
				publicv1.VolumesGetRequest_builder{
					Id: volId,
				}.Build())
			g.Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			g.Expect(ok).To(BeTrue())
			g.Expect(st.Code()).To(Equal(grpccodes.NotFound))
		}, 30*time.Second, time.Second).Should(Succeed())
	})

	It("rejects reserved metadata atomically and preserves private values on public replacement", func() {
		rejectedName := fmt.Sprintf("metadata-rejected-%s", uuid.New()[24:32])
		_, err := publicVolumesClient.Create(ctx, publicv1.VolumesCreateRequest_builder{
			Object: publicv1.Volume_builder{
				Metadata: publicv1.Metadata_builder{
					Name: rejectedName,
					Labels: map[string]string{
						"admin.osac.openshift.io/metadata-test": "private-marker",
						"example.com/metadata-test":             "ordinary-marker",
					},
					Annotations: map[string]string{
						"operator.admin.osac.openshift.io/metadata-test": "private-marker",
						"example.com/metadata-test":                      "ordinary-marker",
					},
				}.Build(),
				Spec: publicv1.VolumeSpec_builder{
					StorageTier: tierName,
					SizeGib:     10,
					AccessMode:  publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		filter := fmt.Sprintf("this.metadata.name == %q", rejectedName)
		listed, err := publicVolumesClient.List(ctx, publicv1.VolumesListRequest_builder{Filter: &filter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(listed.GetTotal()).To(BeZero())

		name := fmt.Sprintf("metadata-volume-%s", uuid.New()[24:32])
		created, err := publicVolumesClient.Create(ctx, publicv1.VolumesCreateRequest_builder{
			Object: publicv1.Volume_builder{
				Metadata: publicv1.Metadata_builder{
					Name:        name,
					Labels:      map[string]string{"example.com/metadata-test": "visible-before"},
					Annotations: map[string]string{"example.com/metadata-test": "visible-before"},
				}.Build(),
				Spec: publicv1.VolumeSpec_builder{
					StorageTier: tierName,
					SizeGib:     10,
					AccessMode:  publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		id := created.GetObject().GetId()
		DeferCleanup(func(cleanupCtx SpecContext) {
			_, deleteErr := publicVolumesClient.Delete(cleanupCtx, publicv1.VolumesDeleteRequest_builder{Id: id}.Build())
			Expect(deleteErr).NotTo(HaveOccurred())
		})

		_, err = privateVolumesClient.Update(ctx, privatev1.VolumesUpdateRequest_builder{
			Object: privatev1.Volume_builder{
				Id: id,
				Metadata: privatev1.Metadata_builder{
					Labels: map[string]string{
						"admin.osac.openshift.io/metadata-test": "stored-label",
						"example.com/metadata-test":             "visible-before",
					},
					Annotations: map[string]string{
						"operator.admin.osac.openshift.io/metadata-test": "stored-annotation",
						"example.com/metadata-test":                      "visible-before",
					},
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels", "metadata.annotations"}},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		before, err := privateVolumesClient.Get(ctx, privatev1.VolumesGetRequest_builder{Id: id}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(maps.Equal(before.GetObject().GetMetadata().GetLabels(), map[string]string{
			"admin.osac.openshift.io/metadata-test": "stored-label",
			"example.com/metadata-test":             "visible-before",
		})).To(BeTrue())
		Expect(maps.Equal(before.GetObject().GetMetadata().GetAnnotations(), map[string]string{
			"operator.admin.osac.openshift.io/metadata-test": "stored-annotation",
			"example.com/metadata-test":                      "visible-before",
		})).To(BeTrue())

		By("allowing private reserved-key queries while denying public existence probes")
		reservedFilter := fmt.Sprintf("this.metadata.name == %q && this.metadata.labels[%q] == %q",
			name, "admin.osac.openshift.io/metadata-test", "stored-label")
		privateList, err := privateVolumesClient.List(ctx, privatev1.VolumesListRequest_builder{Filter: &reservedFilter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(privateList.GetTotal()).To(Equal(int32(1)))
		for _, publicClient := range []publicv1.VolumesClient{
			publicVolumesClient,
			publicv1.NewVolumesClient(tool.ExternalView().AdminConn()),
		} {
			_, listErr := publicClient.List(ctx, publicv1.VolumesListRequest_builder{Filter: &reservedFilter}.Build())
			Expect(listErr).To(HaveOccurred())
			Expect(grpcstatus.Code(listErr)).To(Equal(grpccodes.InvalidArgument))
		}
		ordinaryFilter := fmt.Sprintf("this.metadata.name == %q && this.metadata.labels[%q] == %q",
			name, "example.com/metadata-test", "visible-before")
		ordinaryList, err := publicVolumesClient.List(ctx, publicv1.VolumesListRequest_builder{Filter: &ordinaryFilter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(ordinaryList.GetTotal()).To(Equal(int32(1)))
		Expect(ordinaryList.GetItems()).To(HaveLen(1))
		Expect(ordinaryList.GetItems()[0].GetMetadata().GetLabels()).To(Equal(map[string]string{
			"example.com/metadata-test": "visible-before",
		}))

		By("hiding reserved values from both user and administrator public views")
		userView, err := publicVolumesClient.Get(ctx, publicv1.VolumesGetRequest_builder{Id: id}.Build())
		Expect(err).NotTo(HaveOccurred())
		adminView, err := publicv1.NewVolumesClient(tool.ExternalView().AdminConn()).Get(ctx, publicv1.VolumesGetRequest_builder{Id: id}.Build())
		Expect(err).NotTo(HaveOccurred())
		for _, object := range []*publicv1.Volume{userView.GetObject(), adminView.GetObject()} {
			Expect(object.GetMetadata().GetLabels()).To(HaveKeyWithValue("example.com/metadata-test", "visible-before"))
			Expect(object.GetMetadata().GetAnnotations()).To(HaveKeyWithValue("example.com/metadata-test", "visible-before"))
			Expect(object.GetMetadata().GetLabels()).NotTo(HaveKey("admin.osac.openshift.io/metadata-test"))
			Expect(object.GetMetadata().GetAnnotations()).NotTo(HaveKey("operator.admin.osac.openshift.io/metadata-test"))
		}

		By("rejecting a mixed public replacement without changing private metadata")
		_, err = publicVolumesClient.Update(ctx, publicv1.VolumesUpdateRequest_builder{
			Object: publicv1.Volume_builder{
				Id: id,
				Metadata: publicv1.Metadata_builder{
					Labels: map[string]string{
						"admin.osac.openshift.io/metadata-test": "attacker-value",
						"example.com/metadata-test":             "must-not-persist",
					},
					Annotations: map[string]string{
						"operator.admin.osac.openshift.io/metadata-test": "attacker-value",
						"example.com/metadata-test":                      "must-not-persist",
					},
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels", "metadata.annotations"}},
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		afterRejected, err := privateVolumesClient.Get(ctx, privatev1.VolumesGetRequest_builder{Id: id}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(maps.Equal(afterRejected.GetObject().GetMetadata().GetLabels(), map[string]string{
			"admin.osac.openshift.io/metadata-test": "stored-label",
			"example.com/metadata-test":             "visible-before",
		})).To(BeTrue())
		Expect(maps.Equal(afterRejected.GetObject().GetMetadata().GetAnnotations(), map[string]string{
			"operator.admin.osac.openshift.io/metadata-test": "stored-annotation",
			"example.com/metadata-test":                      "visible-before",
		})).To(BeTrue())

		By("applying ordinary replacement while retaining the latest private maps")
		updated, err := publicVolumesClient.Update(ctx, publicv1.VolumesUpdateRequest_builder{
			Object: publicv1.Volume_builder{
				Id: id,
				Metadata: publicv1.Metadata_builder{
					Labels:      map[string]string{"example.com/metadata-test": "visible-after"},
					Annotations: map[string]string{"example.com/metadata-test": "visible-after"},
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels", "metadata.annotations"}},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.GetObject().GetMetadata().GetLabels()).To(Equal(map[string]string{"example.com/metadata-test": "visible-after"}))
		Expect(updated.GetObject().GetMetadata().GetAnnotations()).To(Equal(map[string]string{"example.com/metadata-test": "visible-after"}))
		afterReplacement, err := privateVolumesClient.Get(ctx, privatev1.VolumesGetRequest_builder{Id: id}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(maps.Equal(afterReplacement.GetObject().GetMetadata().GetLabels(), map[string]string{
			"admin.osac.openshift.io/metadata-test": "stored-label",
			"example.com/metadata-test":             "visible-after",
		})).To(BeTrue())
		Expect(maps.Equal(afterReplacement.GetObject().GetMetadata().GetAnnotations(), map[string]string{
			"operator.admin.osac.openshift.io/metadata-test": "stored-annotation",
			"example.com/metadata-test":                      "visible-after",
		})).To(BeTrue())

		By("clearing both private metadata maps with explicit empty maps")
		_, err = privateVolumesClient.Update(ctx, privatev1.VolumesUpdateRequest_builder{
			Object: privatev1.Volume_builder{
				Id: id,
				Metadata: privatev1.Metadata_builder{
					Labels:      map[string]string{},
					Annotations: map[string]string{},
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels", "metadata.annotations"}},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		cleared, err := privateVolumesClient.Get(ctx, privatev1.VolumesGetRequest_builder{Id: id}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(cleared.GetObject().GetMetadata().GetLabels()).To(BeEmpty())
		Expect(cleared.GetObject().GetMetadata().GetAnnotations()).To(BeEmpty())
	})

	// TC-IT6: Positive field allowlist — verify the public Volume response contains exactly
	// the approved fields and no unexpected fields. This prevents private fields from leaking
	// into the public API without explicit test updates.
	It("Public Volume response contains exactly the approved field set", func() {
		// Approved public fields per the CUD design document and public proto definitions.
		approvedVolumeFields := map[string]bool{
			"id": true, "metadata": true, "spec": true, "status": true,
		}
		approvedMetadataFields := map[string]bool{
			"creation_timestamp": true, "deletion_timestamp": true,
			"creator": true, "tenant": true, "name": true,
			"labels": true, "annotations": true, "version": true,
			"project": true, "display_name": true, "description": true,
		}
		approvedSpecFields := map[string]bool{
			"storage_tier": true, "size_gib": true, "access_mode": true,
		}
		approvedStatusFields := map[string]bool{
			"state": true, "message": true,
		}

		assertExactFields := func(desc protoreflect.MessageDescriptor, approved map[string]bool, msgName string) {
			fields := desc.Fields()
			actual := map[string]bool{}
			for i := range fields.Len() {
				actual[string(fields.Get(i).Name())] = true
			}
			for field := range approved {
				Expect(actual).To(HaveKey(field),
					fmt.Sprintf("approved field %q missing from public %s", field, msgName))
			}
			for field := range actual {
				Expect(approved).To(HaveKey(field),
					fmt.Sprintf("unexpected field %q found in public %s — add to approved list or mark private", field, msgName))
			}
		}

		volDesc := (*publicv1.Volume)(nil).ProtoReflect().Descriptor()
		assertExactFields(volDesc, approvedVolumeFields, "Volume")

		mdDesc := volDesc.Fields().ByName("metadata").Message()
		assertExactFields(mdDesc, approvedMetadataFields, "Metadata")

		specDesc := volDesc.Fields().ByName("spec").Message()
		assertExactFields(specDesc, approvedSpecFields, "VolumeSpec")

		statusDesc := volDesc.Fields().ByName("status").Message()
		assertExactFields(statusDesc, approvedStatusFields, "VolumeStatus")
	})
})
