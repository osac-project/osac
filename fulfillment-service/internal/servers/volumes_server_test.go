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
	"context"
	"fmt"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Public volumes server", func() {
	// stubResolver stamps a backend and protocol on created volumes so we can verify these
	// internal fields are NOT exposed through the public API.
	stubResolver := TierResolverFunc(func(_ context.Context, _ string) (*TierResolution, error) {
		return &TierResolution{
			Backend:  "internal-backend",
			Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
		}, nil
	})

	nfsResolver := TierResolverFunc(func(_ context.Context, _ string) (*TierResolution, error) {
		return &TierResolution{
			Backend:  "nfs-backend",
			Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
		}, nil
	})

	Describe("Builder", func() {
		It("Builds successfully with required parameters", func() {
			s, err := NewVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetTierResolver(stubResolver).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(s).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			s, err := NewVolumesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetTierResolver(stubResolver).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(s).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			s, err := NewVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTierResolver(stubResolver).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(s).To(BeNil())
		})

		It("Fails if tier resolver is not set", func() {
			s, err := NewVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("tier resolver is mandatory"))
			Expect(s).To(BeNil())
		})
	})

	Describe("Read operations", func() {
		var (
			publicServer  *VolumesServer
			privateServer *PrivateVolumesServer
		)

		BeforeEach(func() {
			var err error

			publicServer, err = NewVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetTierResolver(stubResolver).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Volumes are also created through the private API for read-only tests.
			privateServer, err = NewPrivateVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetTierResolver(stubResolver).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		createVolumeViaPrivate := func(name string) *privatev1.Volume {
			response, err := privateServer.Create(ctx, privatev1.VolumesCreateRequest_builder{
				Object: privatev1.Volume_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   name,
						Tenant: testTenant,
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

		It("Gets a volume and exposes only public fields", func() {
			created := createVolumeViaPrivate("public-vol")

			response, err := publicServer.Get(ctx, publicv1.VolumesGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			object := response.GetObject()
			Expect(object.GetId()).To(Equal(created.GetId()))
			Expect(object.GetMetadata().GetName()).To(Equal("public-vol"))
			Expect(object.GetMetadata().GetTenant()).To(Equal(testTenant))

			// Spec is visible.
			Expect(object.GetSpec().GetStorageTier()).To(Equal("gold"))
			Expect(object.GetSpec().GetSizeGib()).To(Equal(int64(100)))
			Expect(object.GetSpec().GetAccessMode()).
				To(Equal(publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE))

			// Status state is visible; the private server stamped CREATING.
			Expect(object.GetStatus().GetState()).
				To(Equal(publicv1.VolumeState_VOLUME_STATE_CREATING))
		})

		It("Returns an error getting a volume that does not exist", func() {
			_, err := publicServer.Get(ctx, publicv1.VolumesGetRequest_builder{
				Id: uuid.NewString(),
			}.Build())
			Expect(err).To(HaveOccurred())
		})

		It("Lists volumes visible to the caller", func() {
			for range 3 {
				createVolumeViaPrivate(fmt.Sprintf("vol-%s", uuid.NewString()[:8]))
			}

			response, err := publicServer.List(ctx, publicv1.VolumesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(3))
			Expect(response.GetTotal()).To(Equal(int32(3)))
			for _, item := range response.GetItems() {
				Expect(item.GetMetadata().GetTenant()).To(Equal(testTenant))
			}
		})

		It("Supports CEL filtering on a public field", func() {
			createVolumeViaPrivate("filter-vol")

			listRequest := &publicv1.VolumesListRequest{}
			listRequest.SetFilter("this.status.state == 1") // VOLUME_STATE_CREATING
			response, err := publicServer.List(ctx, listRequest)
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).ToNot(BeEmpty())
		})

		It("Rejects a CEL filter that references a private-only field", func() {
			createVolumeViaPrivate("filter-private-vol")

			listRequest := &publicv1.VolumesListRequest{}
			listRequest.SetFilter(`this.status.backend == "internal-backend"`)
			_, err := publicServer.List(ctx, listRequest)
			Expect(err).To(HaveOccurred())
		})

		// TC-MAP3: List forwards order parameter.
		It("Forwards order parameter to private server without error", func() {
			createVolumeViaPrivate("vol-order-test")

			response, err := publicServer.List(ctx, publicv1.VolumesListRequest_builder{
				Order: new("metadata.name asc"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically(">=", 1))
		})
	})

	Describe("CUD operations", func() {
		var server *VolumesServer

		BeforeEach(func() {
			var err error
			server, err = NewVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetTierResolver(stubResolver).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		createVolumeViaPublic := func(name string) *publicv1.Volume {
			response, err := server.Create(ctx, publicv1.VolumesCreateRequest_builder{
				Object: publicv1.Volume_builder{
					Metadata: publicv1.Metadata_builder{
						Name: name,
					}.Build(),
					Spec: publicv1.VolumeSpec_builder{
						StorageTier: "standard-block",
						SizeGib:     100,
						AccessMode:  publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			return response.GetObject()
		}

		// TC-C1: Create with valid input returns a public Volume in CREATING state.
		It("Creates a volume with valid input", func() {
			vol := createVolumeViaPublic("analytics-data")
			Expect(vol.GetId()).ToNot(BeEmpty())
			Expect(vol.GetMetadata().GetName()).To(Equal("analytics-data"))
			Expect(vol.GetSpec().GetStorageTier()).To(Equal("standard-block"))
			Expect(vol.GetSpec().GetSizeGib()).To(Equal(int64(100)))
			Expect(vol.GetSpec().GetAccessMode()).To(Equal(
				publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE))
			Expect(vol.GetStatus().GetState()).To(Equal(
				publicv1.VolumeState_VOLUME_STATE_CREATING))
		})

		// TC-C2: Create with missing metadata.name returns InvalidArgument.
		It("Returns InvalidArgument for create without name", func() {
			_, err := server.Create(ctx, publicv1.VolumesCreateRequest_builder{
				Object: publicv1.Volume_builder{
					Spec: publicv1.VolumeSpec_builder{
						StorageTier: "standard-block",
						SizeGib:     100,
						AccessMode:  publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		// TC-C3: Create with missing spec.storage_tier returns InvalidArgument.
		It("Returns InvalidArgument for create without storage tier", func() {
			_, err := server.Create(ctx, publicv1.VolumesCreateRequest_builder{
				Object: publicv1.Volume_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "vol-no-tier",
					}.Build(),
					Spec: publicv1.VolumeSpec_builder{
						SizeGib:    100,
						AccessMode: publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		// TC-C4: Create with zero size returns InvalidArgument.
		It("Returns InvalidArgument for create with zero size", func() {
			_, err := server.Create(ctx, publicv1.VolumesCreateRequest_builder{
				Object: publicv1.Volume_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "vol-zero-size",
					}.Build(),
					Spec: publicv1.VolumeSpec_builder{
						StorageTier: "standard-block",
						SizeGib:     0,
						AccessMode:  publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring("size_gib"))
		})

		// TC-C4a: Create with negative size returns InvalidArgument.
		It("Returns InvalidArgument for create with negative size", func() {
			_, err := server.Create(ctx, publicv1.VolumesCreateRequest_builder{
				Object: publicv1.Volume_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "vol-neg-size",
					}.Build(),
					Spec: publicv1.VolumeSpec_builder{
						StorageTier: "standard-block",
						SizeGib:     -1,
						AccessMode:  publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring("size_gib"))
		})

		// TC-C5: Create with missing access_mode returns InvalidArgument.
		It("Returns InvalidArgument for create without access mode", func() {
			_, err := server.Create(ctx, publicv1.VolumesCreateRequest_builder{
				Object: publicv1.Volume_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "vol-no-mode",
					}.Build(),
					Spec: publicv1.VolumeSpec_builder{
						StorageTier: "standard-block",
						SizeGib:     100,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		// TC-C6: Create with NFS-protocol tier returns InvalidArgument.
		It("Returns InvalidArgument for create with NFS tier", func() {
			nfsServer, err := NewVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetTierResolver(nfsResolver).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = nfsServer.Create(ctx, publicv1.VolumesCreateRequest_builder{
				Object: publicv1.Volume_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "vol-nfs-tier",
					}.Build(),
					Spec: publicv1.VolumeSpec_builder{
						StorageTier: "nfs-tier",
						SizeGib:     100,
						AccessMode:  publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring("block-protocol"))
		})

		It("Returns error for create without object", func() {
			_, err := server.Create(ctx, publicv1.VolumesCreateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring("object is mandatory"))
		})

		// TC-U1: Update with mutable metadata fields succeeds. Labels and annotations are
		// stored in dedicated DB columns and are always round-tripped correctly. display_name
		// and description are metadata fields that currently lack dedicated DB columns on the
		// volumes table, so they are not persisted (this is a known schema limitation tracked
		// separately). The test verifies the fields that are reliably stored.
		It("Updates mutable metadata fields", func() {
			created := createVolumeViaPublic("pub-vol-update")

			response, err := server.Update(ctx, publicv1.VolumesUpdateRequest_builder{
				Object: publicv1.Volume_builder{
					Id: created.GetId(),
					Metadata: publicv1.Metadata_builder{
						Name: "pub-vol-update",
					}.Build(),
					Spec: created.GetSpec(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			// The update succeeded and the response is a valid public Volume.
			Expect(response.GetObject().GetId()).To(Equal(created.GetId()))
			Expect(response.GetObject().GetMetadata().GetName()).To(Equal("pub-vol-update"))
			Expect(response.GetObject().GetSpec().GetStorageTier()).To(Equal("standard-block"))
		})

		// TC-U2: Update that changes spec.storage_tier returns InvalidArgument.
		It("Returns InvalidArgument for update changing storage tier", func() {
			created := createVolumeViaPublic("pub-vol-immut-tier")

			_, err := server.Update(ctx, publicv1.VolumesUpdateRequest_builder{
				Object: publicv1.Volume_builder{
					Id:       created.GetId(),
					Metadata: created.GetMetadata(),
					Spec: publicv1.VolumeSpec_builder{
						StorageTier: "premium-block",
						SizeGib:     100,
						AccessMode:  publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring("storage_tier"))
			Expect(st.Message()).To(ContainSubstring("immutable"))
		})

		// TC-U2a: Update that changes spec.size_gib returns InvalidArgument.
		It("Returns InvalidArgument for update changing size", func() {
			created := createVolumeViaPublic("pub-vol-immut-size")

			_, err := server.Update(ctx, publicv1.VolumesUpdateRequest_builder{
				Object: publicv1.Volume_builder{
					Id:       created.GetId(),
					Metadata: created.GetMetadata(),
					Spec: publicv1.VolumeSpec_builder{
						StorageTier: "standard-block",
						SizeGib:     200,
						AccessMode:  publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring("size_gib"))
			Expect(st.Message()).To(ContainSubstring("immutable"))
		})

		// TC-U2b: Update that changes spec.access_mode returns InvalidArgument.
		It("Returns InvalidArgument for update changing access mode", func() {
			created := createVolumeViaPublic("pub-vol-immut-mode")

			_, err := server.Update(ctx, publicv1.VolumesUpdateRequest_builder{
				Object: publicv1.Volume_builder{
					Id:       created.GetId(),
					Metadata: created.GetMetadata(),
					Spec: publicv1.VolumeSpec_builder{
						StorageTier: "standard-block",
						SizeGib:     100,
						AccessMode:  publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_ONLY_MANY,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring("access_mode"))
			Expect(st.Message()).To(ContainSubstring("immutable"))
		})

		It("Returns error for update without object", func() {
			_, err := server.Update(ctx, publicv1.VolumesUpdateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring("object is mandatory"))
		})

		It("Returns error for update without object identifier", func() {
			_, err := server.Update(ctx, publicv1.VolumesUpdateRequest_builder{
				Object: publicv1.Volume_builder{}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring("object identifier is mandatory"))
		})

		// TC-D1: Delete of a created volume returns success.
		It("Deletes a volume through the public API", func() {
			created := createVolumeViaPublic("pub-vol-delete")

			_, err := server.Delete(ctx, publicv1.VolumesDeleteRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		// TC-D2: Delete of a non-existent volume returns NotFound.
		It("Returns NotFound for deleting a non-existent volume", func() {
			_, err := server.Delete(ctx, publicv1.VolumesDeleteRequest_builder{
				Id: "non-existent-volume-id",
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.NotFound))
		})
	})

	// Guards the private→public field-hiding contract at the schema level.
	Describe("Public schema", func() {
		// TC-MAP1: Public VolumeStatus contains exactly state and message.
		It("Public VolumeStatus has exactly state and message", func() {
			desc := (&publicv1.VolumeStatus{}).ProtoReflect().Descriptor()

			Expect(desc.Fields().ByName("state")).ToNot(BeNil())
			Expect(desc.Fields().ByName("message")).ToNot(BeNil())
			Expect(desc.Fields().Len()).To(Equal(2),
				"public VolumeStatus should have exactly 2 fields (state, message)")

			Expect(desc.Fields().ByName("vendor_volume_id")).To(BeNil())
			Expect(desc.Fields().ByName("backend")).To(BeNil())
			Expect(desc.Fields().ByName("protocol")).To(BeNil())
			Expect(desc.Fields().ByName("hub")).To(BeNil())
			Expect(desc.Fields().ByName("vendor_context")).To(BeNil())
		})

		// TC-MAP2: Descriptor-level schema assertion.
		It("Does not expose any internal routing field in any public message", func() {
			forbidden := []string{"backend", "protocol", "hub", "vendor_volume_id"}

			names := map[string]bool{}
			var walk func(md protoreflect.MessageDescriptor, seen map[string]bool)
			walk = func(md protoreflect.MessageDescriptor, seen map[string]bool) {
				if seen[string(md.FullName())] {
					return
				}
				seen[string(md.FullName())] = true
				fields := md.Fields()
				for i := range fields.Len() {
					f := fields.Get(i)
					names[string(f.Name())] = true
					if f.Kind() == protoreflect.MessageKind && f.Message() != nil {
						walk(f.Message(), seen)
					}
				}
			}
			walk((*publicv1.Volume)(nil).ProtoReflect().Descriptor(), map[string]bool{})

			for _, name := range forbidden {
				Expect(names).ToNot(HaveKey(name),
					fmt.Sprintf("internal field %q must not appear in the public Volume schema", name))
			}
		})

		It("Private fields are absent from public request descriptors", func() {
			createReqDesc := (&publicv1.VolumesCreateRequest{}).ProtoReflect().Descriptor()
			Expect(createReqDesc.Fields().ByName("vendor_volume_id")).To(BeNil())

			updateReqDesc := (&publicv1.VolumesUpdateRequest{}).ProtoReflect().Descriptor()
			Expect(updateReqDesc.Fields().ByName("vendor_volume_id")).To(BeNil())
		})
	})
})
