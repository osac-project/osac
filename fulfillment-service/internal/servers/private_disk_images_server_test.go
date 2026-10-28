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
	"time"

	"buf.build/go/protovalidate"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Private disk images server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateDiskImagesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateDiskImagesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateDiskImagesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *PrivateDiskImagesServer

		BeforeEach(func() {
			var err error
			server, err = NewPrivateDiskImagesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object with required fields and defaults", func() {
			response, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "fedora-41",
					}.Build(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:  "quay.io/containerdisks/fedora:41",
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetId()).ToNot(Equal("fedora-41"))
			Expect(object.GetSpec().GetLifecycle()).To(Equal(
				privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE))
			Expect(object.GetSpec().GetGuestOsFamily()).To(Equal(
				privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX))
			Expect(object.GetSpec().GetSourceType()).To(Equal(
				privatev1.SourceType_SOURCE_TYPE_REGISTRY))
			Expect(object.GetSpec().GetSourceRef()).To(Equal("quay.io/containerdisks/fedora:41"))
			Expect(object.GetSpec().GetArchitecture()).To(ConsistOf(
				privatev1.Architecture_ARCHITECTURE_AMD64))
		})

		It("Creates object with explicit lifecycle and guest OS family", func() {
			response, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "windows-2022",
					}.Build(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:     "quay.io/containerdisks/windows:2022",
						GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_WINDOWS,
						Lifecycle:     privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE,
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetGuestOsFamily()).To(Equal(
				privatev1.GuestOSFamily_GUEST_OS_FAMILY_WINDOWS))
			Expect(response.GetObject().GetSpec().GetLifecycle()).To(Equal(
				privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE))
		})

		It("Allows same name across different tenants", func() {
			response1, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "fedora-41",
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:  "quay.io/containerdisks/fedora:41",
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			response2, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "fedora-41",
						Tenant: "shared",
					}.Build(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:  "quay.io/containerdisks/fedora:41",
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			Expect(response1.GetObject().GetId()).ToNot(Equal(response2.GetObject().GetId()))
		})

		It("Rejects create with unspecified source type", func() {
			_, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "bad-image",
					}.Build(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceRef: "quay.io/test:v1",
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("source_type"))
		})

		It("Rejects create with nil object", func() {
			_, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal("object is mandatory"))
		})

		It("Rejects create with nil spec", func() {
			_, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "no-spec",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal("object spec is mandatory"))
		})

		It("Rejects create with non-AVAILABLE lifecycle", func() {
			_, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "deprecated-on-create",
					}.Build(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:  "quay.io/test:v1",
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
						},
						Lifecycle: privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("AVAILABLE"))
		})

		It("Lists objects", func() {
			const count = 5
			for i := range count {
				_, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
					Object: privatev1.DiskImage_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("image-%d", i),
						}.Build(),
						Spec: privatev1.DiskImageSpec_builder{
							SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
							SourceRef:  fmt.Sprintf("quay.io/test:v%d", i),
							Architecture: []privatev1.Architecture{
								privatev1.Architecture_ARCHITECTURE_AMD64,
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := server.List(ctx, privatev1.DiskImagesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			Expect(response.GetItems()).To(HaveLen(count))
		})

		It("Gets object by ID", func() {
			createResponse, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "get-test",
					}.Build(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:  "quay.io/test:get",
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := server.Get(ctx, privatev1.DiskImagesGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("Updates mutable field (architecture) with field mask", func() {
			createResponse, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "update-test",
					}.Build(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:  "quay.io/test:update",
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			updateResponse, err := server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Id: object.GetId(),
					Spec: privatev1.DiskImageSpec_builder{
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
							privatev1.Architecture_ARCHITECTURE_ARM64,
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.architecture"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetArchitecture()).To(ConsistOf(
				privatev1.Architecture_ARCHITECTURE_AMD64,
				privatev1.Architecture_ARCHITECTURE_ARM64,
			))

			getResponse, err := server.Get(ctx, privatev1.DiskImagesGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetSpec().GetArchitecture()).To(ConsistOf(
				privatev1.Architecture_ARCHITECTURE_AMD64,
				privatev1.Architecture_ARCHITECTURE_ARM64,
			))
		})

		It("Updates without mask does not duplicate architecture", func() {
			createResponse, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "no-mask-update-test",
					}.Build(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:  "quay.io/test:nomask",
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			updateResponse, err := server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Id: object.GetId(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:  "quay.io/test:nomask",
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetArchitecture()).To(ConsistOf(
				privatev1.Architecture_ARCHITECTURE_AMD64,
			))
			Expect(updateResponse.GetObject().GetSpec().GetArchitecture()).To(HaveLen(1))
		})

		It("No-mask update without metadata preserves name and succeeds", func() {
			createResponse, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "nomask-metadata-test",
					}.Build(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:  "quay.io/test:nomask-meta",
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			updateResponse, err := server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Id: object.GetId(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:  "quay.io/test:nomask-meta",
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
							privatev1.Architecture_ARCHITECTURE_ARM64,
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetMetadata().GetName()).To(Equal("nomask-metadata-test"))
			Expect(updateResponse.GetObject().GetSpec().GetArchitecture()).To(ConsistOf(
				privatev1.Architecture_ARCHITECTURE_AMD64,
				privatev1.Architecture_ARCHITECTURE_ARM64,
			))
		})

		It("Masked update with stale version and lock returns ABORTED", func() {
			createResponse, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "lock-test",
					}.Build(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:  "quay.io/test:lock",
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			initialVersion := object.GetMetadata().GetVersion()

			_, err = server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Id: object.GetId(),
					Spec: privatev1.DiskImageSpec_builder{
						Lifecycle: privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.lifecycle"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Id: object.GetId(),
					Metadata: privatev1.Metadata_builder{
						Version: initialVersion,
					}.Build(),
					Spec: privatev1.DiskImageSpec_builder{
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
							privatev1.Architecture_ARCHITECTURE_ARM64,
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.architecture"}},
				Lock:       true,
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.Aborted))
		})

		It("Rejects masked update that empties architecture list", func() {
			createResponse, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "empty-arch-update-test",
					}.Build(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:  "quay.io/test:empty-arch",
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.DiskImageSpec_builder{
						Architecture: []privatev1.Architecture{},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.architecture"}},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("architecture"))
		})

		It("Masked update with UNSPECIFIED lifecycle preserves existing lifecycle", func() {
			createResponse, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "unspecified-lifecycle-test",
					}.Build(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:  "quay.io/test:unspecified-lc",
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			Expect(object.GetSpec().GetLifecycle()).To(
				Equal(privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE))

			updateResponse, err := server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Id: object.GetId(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:  "quay.io/test:unspecified-lc",
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.lifecycle"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetLifecycle()).To(
				Equal(privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE))
		})

		It("Deletes object", func() {
			createResponse, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Metadata: privatev1.Metadata_builder{
						Name:       "delete-test",
						Finalizers: []string{"a"},
					}.Build(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:  "quay.io/test:delete",
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			_, err = server.Delete(ctx, privatev1.DiskImagesDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := server.Get(ctx, privatev1.DiskImagesGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		Describe("Immutability", func() {
			var object *privatev1.DiskImage

			BeforeEach(func() {
				createResponse, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
					Object: privatev1.DiskImage_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "immutable-test",
						}.Build(),
						Spec: privatev1.DiskImageSpec_builder{
							SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
							SourceRef:     "quay.io/test:v1",
							GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
							Architecture: []privatev1.Architecture{
								privatev1.Architecture_ARCHITECTURE_AMD64,
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object = createResponse.GetObject()
			})

			It("Rejects source_ref change", func() {
				_, err := server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
					Object: privatev1.DiskImage_builder{
						Id: object.GetId(),
						Spec: privatev1.DiskImageSpec_builder{
							SourceRef: "quay.io/test:v2",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("source_ref"))
			})

			It("Rejects source_type change", func() {
				_, err := server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
					Object: privatev1.DiskImage_builder{
						Id: object.GetId(),
						Spec: privatev1.DiskImageSpec_builder{
							SourceType: privatev1.SourceType(99),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("source_type"))
			})

			It("Rejects name change", func() {
				_, err := server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
					Object: privatev1.DiskImage_builder{
						Id: object.GetId(),
						Metadata: privatev1.Metadata_builder{
							Name: "different-name",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("name"))
			})

			It("Rejects guest_os_family change", func() {
				_, err := server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
					Object: privatev1.DiskImage_builder{
						Id: object.GetId(),
						Spec: privatev1.DiskImageSpec_builder{
							GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_WINDOWS,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("guest_os_family"))
			})
		})

		Describe("Lifecycle transitions", func() {
			createWithLifecycle := func(name string, lifecycle privatev1.DiskImageLifecycle) *privatev1.DiskImage {
				createResponse, err := server.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
					Object: privatev1.DiskImage_builder{
						Metadata: privatev1.Metadata_builder{
							Name: name,
						}.Build(),
						Spec: privatev1.DiskImageSpec_builder{
							SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
							SourceRef:  "quay.io/test:" + name,
							Architecture: []privatev1.Architecture{
								privatev1.Architecture_ARCHITECTURE_AMD64,
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				if lifecycle == privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE {
					return createResponse.GetObject()
				}

				updateResponse, err := server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
					Object: privatev1.DiskImage_builder{
						Id: createResponse.GetObject().GetId(),
						Spec: privatev1.DiskImageSpec_builder{
							Lifecycle: lifecycle,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.lifecycle"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				return updateResponse.GetObject()
			}

			It("Transitions AVAILABLE to DEPRECATED", func() {
				object := createWithLifecycle("avail-to-deprecated",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE)

				before := time.Now()
				updateResponse, err := server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
					Object: privatev1.DiskImage_builder{
						Id: object.GetId(),
						Spec: privatev1.DiskImageSpec_builder{
							Lifecycle: privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.lifecycle"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				updated := updateResponse.GetObject()
				Expect(updated.GetSpec().GetLifecycle()).To(Equal(
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED))
				dep := updated.GetSpec().GetDeprecation()
				Expect(dep).ToNot(BeNil())
				Expect(dep.GetDeprecationTimestamp()).ToNot(BeNil())
				Expect(dep.GetDeprecationTimestamp().AsTime()).To(BeTemporally(">=", before))
				Expect(dep.HasObsolescenceTimestamp()).To(BeFalse())
			})

			It("Transitions AVAILABLE to OBSOLETE directly", func() {
				object := createWithLifecycle("avail-to-obsolete",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE)

				before := time.Now()
				updateResponse, err := server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
					Object: privatev1.DiskImage_builder{
						Id: object.GetId(),
						Spec: privatev1.DiskImageSpec_builder{
							Lifecycle: privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.lifecycle"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				updated := updateResponse.GetObject()
				Expect(updated.GetSpec().GetLifecycle()).To(Equal(
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE))
				dep := updated.GetSpec().GetDeprecation()
				Expect(dep).ToNot(BeNil())
				Expect(dep.HasDeprecationTimestamp()).To(BeFalse())
				Expect(dep.GetObsolescenceTimestamp()).ToNot(BeNil())
				Expect(dep.GetObsolescenceTimestamp().AsTime()).To(BeTemporally(">=", before))
			})

			It("Transitions DEPRECATED to OBSOLETE", func() {
				object := createWithLifecycle("depr-to-obsolete",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED)
				depTimestamp := object.GetSpec().GetDeprecation().GetDeprecationTimestamp()

				before := time.Now()
				updateResponse, err := server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
					Object: privatev1.DiskImage_builder{
						Id: object.GetId(),
						Spec: privatev1.DiskImageSpec_builder{
							Lifecycle: privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.lifecycle"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				updated := updateResponse.GetObject()
				Expect(updated.GetSpec().GetLifecycle()).To(Equal(
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE))
				dep := updated.GetSpec().GetDeprecation()
				Expect(dep).ToNot(BeNil())
				Expect(dep.GetDeprecationTimestamp().AsTime()).To(Equal(depTimestamp.AsTime()))
				Expect(dep.GetObsolescenceTimestamp()).ToNot(BeNil())
				Expect(dep.GetObsolescenceTimestamp().AsTime()).To(BeTemporally(">=", before))
			})

			It("Transitions OBSOLETE to AVAILABLE and clears deprecation", func() {
				object := createWithLifecycle("obs-to-available",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED)

				// First transition to OBSOLETE:
				updateResponse, err := server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
					Object: privatev1.DiskImage_builder{
						Id: object.GetId(),
						Spec: privatev1.DiskImageSpec_builder{
							Lifecycle: privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.lifecycle"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetSpec().GetDeprecation()).ToNot(BeNil())

				// Transition back to AVAILABLE:
				updateResponse, err = server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
					Object: privatev1.DiskImage_builder{
						Id: object.GetId(),
						Spec: privatev1.DiskImageSpec_builder{
							Lifecycle: privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.lifecycle"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				updated := updateResponse.GetObject()
				Expect(updated.GetSpec().GetLifecycle()).To(Equal(
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE))
				Expect(updated.GetSpec().HasDeprecation()).To(BeFalse())
			})

			It("Same-state update does not change timestamps", func() {
				object := createWithLifecycle("same-state",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED)
				depTimestamp := object.GetSpec().GetDeprecation().GetDeprecationTimestamp()

				updateResponse, err := server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
					Object: privatev1.DiskImage_builder{
						Id: object.GetId(),
						Spec: privatev1.DiskImageSpec_builder{
							Lifecycle: privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.lifecycle"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				updated := updateResponse.GetObject()
				Expect(updated.GetSpec().GetDeprecation().GetDeprecationTimestamp().AsTime()).To(
					Equal(depTimestamp.AsTime()))
			})
		})
	})

	Describe("Architecture validation", func() {
		var validator protovalidate.Validator

		BeforeEach(func() {
			var err error
			validator, err = protovalidate.New()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Accepts valid architectures", func() {
			spec := privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  "quay.io/test:v1",
				Architecture: []privatev1.Architecture{
					privatev1.Architecture_ARCHITECTURE_AMD64,
					privatev1.Architecture_ARCHITECTURE_ARM64,
				},
			}.Build()
			err := validator.Validate(spec)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects ARCHITECTURE_UNSPECIFIED", func() {
			spec := privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  "quay.io/test:v1",
				Architecture: []privatev1.Architecture{
					privatev1.Architecture_ARCHITECTURE_UNSPECIFIED,
				},
			}.Build()
			err := validator.Validate(spec)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("architecture"))
		})

		It("Rejects duplicate architectures", func() {
			spec := privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  "quay.io/test:v1",
				Architecture: []privatev1.Architecture{
					privatev1.Architecture_ARCHITECTURE_AMD64,
					privatev1.Architecture_ARCHITECTURE_AMD64,
				},
			}.Build()
			err := validator.Validate(spec)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("architecture"))
		})

		It("Rejects empty architecture list", func() {
			spec := privatev1.DiskImageSpec_builder{
				SourceType:   privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:    "quay.io/test:v1",
				Architecture: []privatev1.Architecture{},
			}.Build()
			err := validator.Validate(spec)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("architecture"))
		})
	})
	Describe("Architecture updates", func() {
		var (
			service *PrivateDiskImagesServer
			tx      database.Tx
			image   *privatev1.DiskImage
		)
		exec := func(query string, args ...any) {
			GinkgoHelper()
			_, err := tx.Exec(ctx, query, args...)
			Expect(err).NotTo(HaveOccurred())
		}
		BeforeEach(func() {
			var err error
			tx, err = database.TxFromContext(ctx)
			Expect(err).NotTo(HaveOccurred())
			service = &PrivateDiskImagesServer{logger: logger}
			image = privatev1.DiskImage_builder{Id: "update-image", Spec: privatev1.DiskImageSpec_builder{
				Architecture: []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64, privatev1.Architecture_ARCHITECTURE_ARM64, privatev1.Architecture_ARCHITECTURE_S390X},
			}.Build()}.Build()
			exec(`insert into disk_images (id, name, tenant, creator, data)
				values ('update-image', 'update-image', 'shared', 'system', '{"spec": {
					"source_type": "SOURCE_TYPE_REGISTRY", "source_ref": "quay.io/example/image:latest",
					"guest_os_family": "GUEST_OS_FAMILY_LINUX", "lifecycle": "DISK_IMAGE_LIFECYCLE_AVAILABLE",
					"architecture": ["ARCHITECTURE_AMD64", "ARCHITECTURE_ARM64", "ARCHITECTURE_S390X"]
				}}')`)
			exec(`insert into bare_metal_instance_types (id, name, tenant, data)
				select 'update-type-' || cpu, 'update-type-' || cpu, 'shared',
					jsonb_build_object('spec', jsonb_build_object('hardware',
						jsonb_build_object('cpu', jsonb_build_object('architecture', cpu))))
				from unnest(array['amd64', 'arm64', 's390x']) as cpu`)
			exec(`insert into bare_metal_instance_templates (id, name, tenant, data)
				values ('update-template', 'update-template', $1,
					'{"instance_type":{"id":"update-type-amd64"}}')`, testTenant)
		})
		insertInstance := func(cpu, imageID, tenant string) {
			GinkgoHelper()
			exec(`insert into bare_metal_instances (id, name, tenant, data)
				values ('dependent-' || $1, 'dependent-' || $1, $3,
					jsonb_build_object('spec', jsonb_build_object(
						'instance_type', jsonb_build_object('id', 'update-type-' || $1),
						'disk_image', jsonb_build_object('id', $2::text))))`, cpu, imageID, tenant)
		}
		insertCatalog := func(typePolicy, cpu string, lockedImage, published bool) {
			GinkgoHelper()
			var typeField string
			switch typePolicy {
			case "locked":
				typeField = fmt.Sprintf(`"instance_type":{"locked":{"id":"update-type-%s"}},`, cpu)
			case "default":
				typeField = fmt.Sprintf(`"instance_type":{"editable":{"default_value":{"id":"update-type-%s"}}},`, cpu)
			case "open":
				typeField = `"instance_type":{"editable":{}},`
			}
			imageField := `{"locked":{"id":"update-image"}}`
			if !lockedImage {
				imageField = `{"editable":{"default_value":{"id":"update-image"}}}`
			}
			fields := fmt.Sprintf(`{%s"disk_image":%s}`, typeField, imageField)
			exec(`insert into bare_metal_instance_catalog_items (id, name, tenant, data)
				values ('update-catalog', 'update-catalog', $1, jsonb_build_object(
					'template', jsonb_build_object('id', 'update-template'), 'fields', $2::jsonb, 'published', $3::boolean))`,
				testTenant, fields, published)
		}
		candidate := func(architectures ...privatev1.Architecture) *privatev1.DiskImage {
			result := proto.Clone(image).(*privatev1.DiskImage)
			result.GetSpec().SetArchitecture(architectures)
			return result
		}

		Describe("validateArchitectureUpdate", func() {
			DescribeTable("preserves architectures required by instances", func(cpu string, architectures []privatev1.Architecture, rejected bool) {
				insertInstance(cpu, image.GetId(), testTenant)
				err := service.validateArchitectureUpdate(ctx, image, candidate(architectures...))
				if rejected {
					Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
				} else {
					Expect(err).NotTo(HaveOccurred())
				}
			},
				Entry("amd64 removal", "amd64", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_ARM64}, true),
				Entry("arm64 removal", "arm64", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64}, true),
				Entry("s390x removal", "s390x", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64}, true),
				Entry("unused architectures removed", "amd64", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64}, false),
				Entry("arm64 retained", "arm64", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_ARM64}, false),
				Entry("s390x retained", "s390x", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_S390X}, false),
				Entry("unchanged", "amd64", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64, privatev1.Architecture_ARCHITECTURE_ARM64, privatev1.Architecture_ARCHITECTURE_S390X}, false),
				Entry("reordered", "amd64", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_S390X, privatev1.Architecture_ARCHITECTURE_ARM64, privatev1.Architecture_ARCHITECTURE_AMD64}, false))

			It("allows adding architectures without a dependency query", func() {
				current := candidate(privatev1.Architecture_ARCHITECTURE_AMD64)
				Expect(service.validateArchitectureUpdate(context.Background(), current,
					candidate(privatev1.Architecture_ARCHITECTURE_AMD64, privatev1.Architecture_ARCHITECTURE_ARM64))).To(Succeed())
			})
			It("allows unreferenced images to change architectures", func() {
				Expect(service.validateArchitectureUpdate(ctx, image, candidate(privatev1.Architecture_ARCHITECTURE_ARM64))).To(Succeed())
			})
			DescribeTable("protects concrete catalog combinations", func(typePolicy, cpu string, lockedImage, published, rejected bool) {
				insertCatalog(typePolicy, cpu, lockedImage, published)
				err := service.validateArchitectureUpdate(ctx, image, candidate(privatev1.Architecture_ARCHITECTURE_ARM64))
				if rejected {
					Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
				} else {
					Expect(err).NotTo(HaveOccurred())
				}
			}, Entry("locked type and image", "locked", "amd64", true, true, true),
				Entry("default type and image", "default", "amd64", false, true, true),
				Entry("template type and locked image", "template", "", true, true, true),
				Entry("template type and default image", "template", "", false, true, true),
				Entry("locked type overrides template", "locked", "arm64", true, true, false),
				Entry("default type overrides template", "default", "arm64", false, true, false),
				Entry("unpublished catalog", "locked", "amd64", true, false, true),
				Entry("open type choice", "open", "", true, true, false))

			It("checks shared-image dependencies across tenants without exposing their identities", func() {
				exec(`insert into tenants (id, name, tenant, data) values ('hidden-tenant', 'hidden-tenant', 'hidden-tenant', '{}')`)
				insertInstance("amd64", image.GetId(), "hidden-tenant")
				err := service.validateArchitectureUpdate(ctx, image, candidate(privatev1.Architecture_ARCHITECTURE_ARM64))
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
				Expect(grpcstatus.Convert(err).Message()).To(And(HavePrefix("DISK_IMAGE_ARCHITECTURE_IN_USE:"),
					Not(ContainSubstring("hidden-tenant")), Not(ContainSubstring("dependent-amd64")), Not(ContainSubstring("quay.io"))))
			})
			It("matches image IDs rather than names shared by different tenants", func() {
				exec(`insert into disk_images (id, name, tenant, data) values ('other-image', 'update-image', $1, '{}')`, testTenant)
				insertInstance("amd64", "other-image", testTenant)
				Expect(service.validateArchitectureUpdate(ctx, image, candidate(privatev1.Architecture_ARCHITECTURE_ARM64))).To(Succeed())
			})
			DescribeTable("ignores deleted dependents", func(catalog bool) {
				if catalog {
					insertCatalog("locked", "amd64", true, true)
					exec(`update bare_metal_instance_catalog_items set deletion_timestamp = now() where id = 'update-catalog'`)
				} else {
					insertInstance("amd64", image.GetId(), testTenant)
					exec(`update bare_metal_instances set deletion_timestamp = now() where id = 'dependent-amd64'`)
				}
				Expect(service.validateArchitectureUpdate(ctx, image, candidate(privatev1.Architecture_ARCHITECTURE_ARM64))).To(Succeed())
			}, Entry("instance", false), Entry("catalog", true))
			It("allows repairing a previously incompatible catalog combination", func() {
				insertCatalog("locked", "amd64", true, true)
				Expect(service.validateArchitectureUpdate(ctx, candidate(privatev1.Architecture_ARCHITECTURE_ARM64),
					candidate(privatev1.Architecture_ARCHITECTURE_AMD64))).To(Succeed())
			})
		})

		Describe("Update architecture validation", func() {
			BeforeEach(func() {
				var err error
				service, err = NewPrivateDiskImagesServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
				Expect(err).NotTo(HaveOccurred())
				stored, err := service.Get(ctx, privatev1.DiskImagesGetRequest_builder{Id: image.GetId()}.Build())
				Expect(err).NotTo(HaveOccurred())
				image = stored.GetObject()
				insertInstance("amd64", image.GetId(), testTenant)
			})
			It("saves an update that passes architecture validation", func() {
				response, err := service.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
					Object:     candidate(privatev1.Architecture_ARCHITECTURE_AMD64),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.architecture"}},
				}.Build())
				Expect(err).NotTo(HaveOccurred())
				stored, err := service.Get(ctx, privatev1.DiskImagesGetRequest_builder{Id: image.GetId()}.Build())
				Expect(err).NotTo(HaveOccurred())
				Expect(stored.GetObject().GetSpec().GetArchitecture()).To(Equal([]privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64}))
				Expect(proto.Equal(stored.GetObject(), response.GetObject())).To(BeTrue())
			})
			It("rejects a failing validation without changing stored data", func() {
				proposed := candidate(privatev1.Architecture_ARCHITECTURE_ARM64)
				proposed.GetSpec().SetLifecycle(privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED)
				proposed.GetMetadata().SetLabels(map[string]string{"purpose": "rejected"})
				_, err := service.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
					Object: proposed, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec", "metadata.labels"}},
				}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
				stored, err := service.Get(ctx, privatev1.DiskImagesGetRequest_builder{Id: image.GetId()}.Build())
				Expect(err).NotTo(HaveOccurred())
				Expect(proto.Equal(stored.GetObject(), image)).To(BeTrue(), "the image, metadata, lifecycle timestamps, and version must be unchanged")
			})
		})
	})
})
