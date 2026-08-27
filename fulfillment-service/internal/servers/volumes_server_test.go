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
	"google.golang.org/protobuf/reflect/protoreflect"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("Public volumes server", func() {
	Describe("Builder", func() {
		It("Builds successfully with required parameters", func() {
			s, err := NewVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(s).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			s, err := NewVolumesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(s).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			s, err := NewVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(s).To(BeNil())
		})
	})

	Describe("Read operations", func() {
		var (
			publicServer  *VolumesServer
			privateServer *PrivateVolumesServer
		)

		// stubResolver stamps a backend and protocol on created volumes so we can verify these
		// internal fields are NOT exposed through the public API.
		stubResolver := TierResolverFunc(func(_ context.Context, _ string) (*TierResolution, error) {
			return &TierResolution{
				BackendID: "internal-backend",
				Protocol:  privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
			}, nil
		})

		BeforeEach(func() {
			var err error

			publicServer, err = NewVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Volumes are created through the private API (public API is read-only).
			privateServer, err = NewPrivateVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetTierResolver(stubResolver).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		createVolume := func(name string) *privatev1.Volume {
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
			created := createVolume("public-vol")

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

			// The internal routing fields (backend, protocol, hub, vendor_volume_id) are not part of
			// the public Volume type at all, so they cannot leak. This is enforced at compile time by
			// publicv1.VolumeStatus only exposing state and message.
		})

		It("Returns an error getting a volume that does not exist", func() {
			_, err := publicServer.Get(ctx, publicv1.VolumesGetRequest_builder{
				Id: uuid.NewString(),
			}.Build())
			Expect(err).To(HaveOccurred())
		})

		It("Lists volumes visible to the caller", func() {
			for range 3 {
				createVolume(fmt.Sprintf("vol-%s", uuid.NewString()[:8]))
			}

			response, err := publicServer.List(ctx, publicv1.VolumesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(3))
			Expect(response.GetTotal()).To(Equal(int32(3)))
			// Every listed volume carries the caller-visible tenant (scoping delegated to the
			// private server's generic tenancy logic).
			for _, item := range response.GetItems() {
				Expect(item.GetMetadata().GetTenant()).To(Equal(testTenant))
			}
		})

		It("Supports CEL filtering on a public field", func() {
			createVolume("filter-vol")

			listRequest := &publicv1.VolumesListRequest{}
			listRequest.SetFilter("this.status.state == 1") // VOLUME_STATE_CREATING
			response, err := publicServer.List(ctx, listRequest)
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).ToNot(BeEmpty())
		})

		It("Rejects a CEL filter that references a private-only field", func() {
			createVolume("filter-private-vol")

			// status.backend exists on the private Volume but not the public one; SetFilterDesc
			// restricts the public filter surface to public fields, so this must be rejected rather
			// than silently ignored (which would let callers probe hidden fields).
			listRequest := &publicv1.VolumesListRequest{}
			listRequest.SetFilter(`this.status.backend == "internal-backend"`)
			_, err := publicServer.List(ctx, listRequest)
			Expect(err).To(HaveOccurred())
		})
	})

	// Guards the private→public field-hiding contract at the schema level: if a private field is
	// ever added without a `[(cleanapi.field).private = true]` annotation, it would appear in the
	// generated public Volume and fail this test instead of silently leaking.
	Describe("Public schema", func() {
		It("Does not expose any internal routing field", func() {
			forbidden := []string{"backend", "protocol", "hub", "vendor_volume_id"}

			// Recursively collect every field name reachable from the public Volume message.
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
	})
})
