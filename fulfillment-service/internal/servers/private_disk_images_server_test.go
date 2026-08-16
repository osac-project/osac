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
	"time"

	"buf.build/go/protovalidate"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
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
})
