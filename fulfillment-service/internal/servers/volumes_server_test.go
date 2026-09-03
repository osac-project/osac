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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

var _ = Describe("Volumes server", func() {
	stubResolver := TierResolverFunc(func(_ context.Context, _ string) (*TierResolution, error) {
		return &TierResolution{
			BackendID: "test-backend",
			Protocol:  privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
		}, nil
	})

	Describe("Builder", func() {
		It("Creates server with all required parameters", func() {
			server, err := NewVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetTierResolver(stubResolver).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewVolumesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetTierResolver(stubResolver).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTierResolver(stubResolver).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tier resolver is not set", func() {
			server, err := NewVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("tier resolver is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var (
			server *VolumesServer
		)

		BeforeEach(func() {
			// Put a context into the subject:
			ctx = auth.ContextWithSubject(
				ctx,
				&auth.Subject{
					User:    auth.SystemTenant,
					Tenants: auth.AllTenants,
				},
			)

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
						StorageTier: "gold",
						SizeGib:     100,
						AccessMode:  publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			return response.GetObject()
		}

		It("Creates a volume through the public API", func() {
			vol := createVolumeViaPublic("pub-vol-1")
			Expect(vol.GetId()).ToNot(BeEmpty())
			Expect(vol.GetSpec().GetStorageTier()).To(Equal("gold"))
			Expect(vol.GetSpec().GetSizeGib()).To(Equal(int64(100)))
			Expect(vol.GetSpec().GetAccessMode()).To(Equal(
				publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE))
			Expect(vol.GetStatus().GetState()).To(Equal(
				publicv1.VolumeState_VOLUME_STATE_CREATING))
		})

		It("Gets a volume through the public API", func() {
			created := createVolumeViaPublic("pub-vol-get")

			response, err := server.Get(ctx, publicv1.VolumesGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetId()).To(Equal(created.GetId()))
			Expect(response.GetObject().GetSpec().GetStorageTier()).To(Equal("gold"))
		})

		It("Lists volumes through the public API", func() {
			const count = 3
			for i := range count {
				createVolumeViaPublic(fmt.Sprintf("pub-vol-list-%d", i))
			}

			response, err := server.List(ctx, publicv1.VolumesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(count))
			Expect(response.GetTotal()).To(BeNumerically("==", count))
		})

		It("Lists volumes with limit", func() {
			const count = 5
			for i := range count {
				createVolumeViaPublic(fmt.Sprintf("pub-vol-limit-%d", i))
			}

			response, err := server.List(ctx, publicv1.VolumesListRequest_builder{
				Limit: new(int32(2)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 2))
			Expect(response.GetTotal()).To(BeNumerically("==", count))
		})

		It("Forwards order parameter to private server without error", func() {
			createVolumeViaPublic("vol-order-test")

			// The order parameter should be accepted and forwarded to the private
			// server without error. Note: the DAO currently always sorts by id
			// (order translation is not yet implemented), so we only verify that
			// the parameter is forwarded without causing an error — not that the
			// actual sort changes. This matches the private server's behaviour.
			response, err := server.List(ctx, publicv1.VolumesListRequest_builder{
				Order: new("metadata.name asc"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically(">=", 1))
		})

		It("Updates a volume through the public API", func() {
			created := createVolumeViaPublic("pub-vol-update")

			response, err := server.Update(ctx, publicv1.VolumesUpdateRequest_builder{
				Object: publicv1.Volume_builder{
					Id:       created.GetId(),
					Metadata: created.GetMetadata(),
					Spec:     created.GetSpec(),
					Status: publicv1.VolumeStatus_builder{
						State:          publicv1.VolumeState_VOLUME_STATE_AVAILABLE,
						VendorVolumeId: "vendor-123",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetStatus().GetState()).To(Equal(
				publicv1.VolumeState_VOLUME_STATE_AVAILABLE))
			Expect(response.GetObject().GetStatus().GetVendorVolumeId()).To(Equal("vendor-123"))
		})

		It("Deletes a volume through the public API", func() {
			created := createVolumeViaPublic("pub-vol-delete")

			_, err := server.Delete(ctx, publicv1.VolumesDeleteRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Verify it's gone:
			_, err = server.Get(ctx, publicv1.VolumesGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.NotFound))
		})

		It("Returns error for create without object", func() {
			_, err := server.Create(ctx, publicv1.VolumesCreateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring("object is mandatory"))
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

		It("Public response does not contain private-only status fields", func() {
			created := createVolumeViaPublic("pub-vol-private-fields")

			response, err := server.Get(ctx, publicv1.VolumesGetRequest_builder{
				Id: created.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// The public VolumeStatus type should not have backend, protocol, or hub fields.
			// These fields are filtered out by cleanapi. The public type's status will have
			// state, message, and vendor_volume_id — but not backend/protocol/hub.
			status := response.GetObject().GetStatus()
			Expect(status).ToNot(BeNil())
			Expect(status.GetState()).To(Equal(publicv1.VolumeState_VOLUME_STATE_CREATING))

			// Verify the public VolumeStatus proto descriptor does not contain private-only fields:
			desc := status.ProtoReflect().Descriptor()
			Expect(desc.Fields().ByName("backend")).To(BeNil())
			Expect(desc.Fields().ByName("protocol")).To(BeNil())
			Expect(desc.Fields().ByName("hub")).To(BeNil())
		})
	})
})
