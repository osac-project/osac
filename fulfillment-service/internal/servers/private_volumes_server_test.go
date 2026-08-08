/*
Copyright (c) 2026 Red Hat Inc.

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

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
)

var _ = Describe("Private volumes server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateVolumesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *PrivateVolumesServer

		BeforeEach(func() {
			var err error
			server, err = NewPrivateVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		createVolume := func() *privatev1.Volume {
			response, err := server.Create(ctx, privatev1.VolumesCreateRequest_builder{
				Object: privatev1.Volume_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-volume",
					}.Build(),
					Spec: privatev1.VolumeSpec_builder{
						StorageTier: "gold",
						SizeGib:     100,
						AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			return response.GetObject()
		}

		createVolumeWithName := func(name string) *privatev1.Volume {
			response, err := server.Create(ctx, privatev1.VolumesCreateRequest_builder{
				Object: privatev1.Volume_builder{
					Metadata: privatev1.Metadata_builder{
						Name: name,
					}.Build(),
					Spec: privatev1.VolumeSpec_builder{
						StorageTier: "gold",
						SizeGib:     100,
						AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			return response.GetObject()
		}

		createStandaloneVolume := func() *privatev1.Volume {
			response, err := server.Create(ctx, privatev1.VolumesCreateRequest_builder{
				Object: privatev1.Volume_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "standalone-volume",
					}.Build(),
					Spec: privatev1.VolumeSpec_builder{
						StorageTier: "gold",
						SizeGib:     50,
						AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			return response.GetObject()
		}

		It("Creates and gets a volume", func() {
			created := createVolume()

			Expect(created.GetId()).ToNot(BeEmpty())
			Expect(created.GetSpec().GetStorageTier()).To(Equal("gold"))
			Expect(created.GetSpec().GetSizeGib()).To(Equal(int64(100)))
			Expect(created.GetSpec().GetAccessMode()).To(Equal(
				privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE))
			Expect(created.GetStatus().GetState()).To(Equal(
				privatev1.VolumeState_VOLUME_STATE_CREATING))

			getResponse, err := server.Get(ctx, privatev1.VolumesGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			obj := getResponse.GetObject()
			Expect(obj.GetId()).To(Equal(created.GetId()))
			Expect(obj.GetSpec().GetStorageTier()).To(Equal("gold"))
		})

		It("Creates a standalone volume without pvc_ref", func() {
			created := createStandaloneVolume()

			Expect(created.GetId()).ToNot(BeEmpty())
			Expect(created.GetSpec().GetStorageTier()).To(Equal("gold"))
			Expect(created.GetSpec().GetSizeGib()).To(Equal(int64(50)))
			Expect(created.GetSpec().GetPvcRef()).To(BeNil())
			Expect(created.GetStatus().GetState()).To(Equal(
				privatev1.VolumeState_VOLUME_STATE_CREATING))
		})

		It("Creates a volume with pvc_ref", func() {
			response, err := server.Create(ctx, privatev1.VolumesCreateRequest_builder{
				Object: privatev1.Volume_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "pvc-volume",
					}.Build(),
					Spec: privatev1.VolumeSpec_builder{
						StorageTier: "gold",
						SizeGib:     100,
						AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
						PvcRef: privatev1.PVCReference_builder{
							Name:      "my-pvc",
							Namespace: "default",
							Cluster:   "cluster-1",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			obj := response.GetObject()
			Expect(obj.GetSpec().GetPvcRef().GetName()).To(Equal("my-pvc"))
			Expect(obj.GetSpec().GetPvcRef().GetNamespace()).To(Equal("default"))
			Expect(obj.GetSpec().GetPvcRef().GetCluster()).To(Equal("cluster-1"))
		})

		It("List volumes", func() {
			const count = 5
			for i := range count {
				createVolumeWithName(fmt.Sprintf("volume-%d", i))
			}

			response, err := server.List(ctx, privatev1.VolumesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(count))
		})

		It("List volumes with limit", func() {
			const count = 5
			for i := range count {
				createVolumeWithName(fmt.Sprintf("volume-%d", i))
			}

			response, err := server.List(ctx, privatev1.VolumesListRequest_builder{
				Limit: new(int32(2)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 2))
		})

		It("List volumes with filter", func() {
			const count = 3
			var ids []string
			for i := range count {
				obj := createVolumeWithName(fmt.Sprintf("volume-%d", i))
				ids = append(ids, obj.GetId())
			}

			for _, id := range ids {
				response, err := server.List(ctx, privatev1.VolumesListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == '%s'", id)),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetSize()).To(BeNumerically("==", 1))
				Expect(response.GetItems()[0].GetId()).To(Equal(id))
			}
		})

		It("List volumes with order", func() {
			createVolumeWithName("aaa-volume")
			createVolumeWithName("zzz-volume")

			response, err := server.List(ctx, privatev1.VolumesListRequest_builder{
				Order: new("metadata.name asc"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 2))
			Expect(response.GetItems()[0].GetMetadata().GetName()).To(Equal("aaa-volume"))
			Expect(response.GetItems()[1].GetMetadata().GetName()).To(Equal("zzz-volume"))
		})

		It("Update applies partial changes via field mask", func() {
			created := createVolume()

			updateResponse, err := server.Update(ctx, privatev1.VolumesUpdateRequest_builder{
				Object: privatev1.Volume_builder{
					Id: created.GetId(),
					Status: privatev1.VolumeStatus_builder{
						State:          privatev1.VolumeState_VOLUME_STATE_AVAILABLE,
						VendorVolumeId: "vast-vol-123",
						Backend:        "vast-1",
						Protocol:       privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
					"status.state",
					"status.vendor_volume_id",
					"status.backend",
					"status.protocol",
				}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetStatus().GetState()).To(Equal(
				privatev1.VolumeState_VOLUME_STATE_AVAILABLE))
			Expect(updateResponse.GetObject().GetStatus().GetVendorVolumeId()).To(Equal("vast-vol-123"))
			Expect(updateResponse.GetObject().GetStatus().GetBackend()).To(Equal("vast-1"))
			Expect(updateResponse.GetObject().GetStatus().GetProtocol()).To(Equal(
				privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS))
			Expect(updateResponse.GetObject().GetSpec().GetStorageTier()).To(Equal("gold"))
		})

		It("Delete removes the object", func() {
			created := createVolume()

			_, err := server.Delete(ctx, privatev1.VolumesDeleteRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Get(ctx, privatev1.VolumesGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.NotFound))
		})

		It("Generates UUID for id ignoring caller-provided value", func() {
			callerProvidedId := "my-custom-id"
			response, err := server.Create(ctx, privatev1.VolumesCreateRequest_builder{
				Object: privatev1.Volume_builder{
					Id: callerProvidedId,
					Metadata: privatev1.Metadata_builder{
						Name: "test-volume",
					}.Build(),
					Spec: privatev1.VolumeSpec_builder{
						StorageTier: "gold",
						SizeGib:     100,
						AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetId()).ToNot(Equal(callerProvidedId))
			_, err = uuid.Parse(response.GetObject().GetId())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Create always sets state to CREATING regardless of caller-provided state", func() {
			response, err := server.Create(ctx, privatev1.VolumesCreateRequest_builder{
				Object: privatev1.Volume_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-volume",
					}.Build(),
					Spec: privatev1.VolumeSpec_builder{
						StorageTier: "gold",
						SizeGib:     100,
						AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
					Status: privatev1.VolumeStatus_builder{
						State: privatev1.VolumeState_VOLUME_STATE_AVAILABLE,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetStatus().GetState()).To(Equal(
				privatev1.VolumeState_VOLUME_STATE_CREATING))
		})

		Describe("Validation", func() {
			It("Create without metadata.name fails", func() {
				_, err := server.Create(ctx, privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Spec: privatev1.VolumeSpec_builder{
							StorageTier: "gold",
							SizeGib:     100,
							AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.InvalidArgument))
				Expect(st.Message()).To(ContainSubstring("metadata.name"))
			})

			It("Create without storage_tier fails", func() {
				_, err := server.Create(ctx, privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-volume",
						}.Build(),
						Spec: privatev1.VolumeSpec_builder{
							SizeGib:    100,
							AccessMode: privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.InvalidArgument))
				Expect(st.Message()).To(ContainSubstring("storage_tier"))
			})

			It("Create without size_gib fails", func() {
				_, err := server.Create(ctx, privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-volume",
						}.Build(),
						Spec: privatev1.VolumeSpec_builder{
							StorageTier: "gold",
							AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.InvalidArgument))
				Expect(st.Message()).To(ContainSubstring("size_gib"))
			})

			It("Create without access_mode fails", func() {
				_, err := server.Create(ctx, privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-volume",
						}.Build(),
						Spec: privatev1.VolumeSpec_builder{
							StorageTier: "gold",
							SizeGib:     100,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.InvalidArgument))
				Expect(st.Message()).To(ContainSubstring("access_mode"))
			})

			It("Create without spec fails", func() {
				_, err := server.Create(ctx, privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-volume",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.InvalidArgument))
				Expect(st.Message()).To(ContainSubstring("spec"))
			})
		})

		Describe("Name uniqueness", func() {
			It("Create with duplicate active name fails", func() {
				createVolumeWithName("unique-name")

				_, err := server.Create(ctx, privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "unique-name",
						}.Build(),
						Spec: privatev1.VolumeSpec_builder{
							StorageTier: "gold",
							SizeGib:     50,
							AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.AlreadyExists))
			})

			It("Create after delete of same name succeeds", func() {
				created := createVolumeWithName("reusable-name")

				_, err := server.Delete(ctx, privatev1.VolumesDeleteRequest_builder{
					Id: created.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				second := createVolumeWithName("reusable-name")
				Expect(second.GetId()).ToNot(Equal(created.GetId()))
				Expect(second.GetMetadata().GetName()).To(Equal("reusable-name"))
			})
		})

		Describe("Signal", func() {
			It("Signal succeeds for existing volume", func() {
				created := createVolume()

				_, err := server.Signal(ctx, privatev1.VolumesSignalRequest_builder{
					Id: created.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			It("Signal fails for non-existent volume", func() {
				_, err := server.Signal(ctx, privatev1.VolumesSignalRequest_builder{
					Id: "non-existent-id",
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(codes.NotFound))
			})
		})
	})
})
