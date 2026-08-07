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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("Disk images server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewDiskImagesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewDiskImagesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewDiskImagesServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewDiskImagesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("OBSOLETE filtering", func() {
		var server *DiskImagesServer
		var privateServer *PrivateDiskImagesServer

		BeforeEach(func() {
			var err error
			server, err = NewDiskImagesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			privateServer, err = NewPrivateDiskImagesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			createWithLifecycle := func(name string, lifecycle privatev1.DiskImageLifecycle) {
				createResponse, createErr := privateServer.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
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
				Expect(createErr).ToNot(HaveOccurred())

				if lifecycle == privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE {
					return
				}

				_, updateErr := privateServer.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
					Object: privatev1.DiskImage_builder{
						Id: createResponse.GetObject().GetId(),
						Spec: privatev1.DiskImageSpec_builder{
							Lifecycle: privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.lifecycle"}},
				}.Build())
				Expect(updateErr).ToNot(HaveOccurred())

				if lifecycle == privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED {
					return
				}

				_, updateErr = privateServer.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
					Object: privatev1.DiskImage_builder{
						Id: createResponse.GetObject().GetId(),
						Spec: privatev1.DiskImageSpec_builder{
							Lifecycle: privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.lifecycle"}},
				}.Build())
				Expect(updateErr).ToNot(HaveOccurred())
			}

			createWithLifecycle("image-available", privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE)
			createWithLifecycle("image-deprecated", privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED)
			createWithLifecycle("image-obsolete", privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE)
		})

		It("Excludes OBSOLETE from default list", func() {
			response, err := server.List(ctx, publicv1.DiskImagesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(2))
			names := make([]string, len(response.GetItems()))
			for i, item := range response.GetItems() {
				names[i] = item.GetId()
			}
			Expect(names).To(ConsistOf("image-available", "image-deprecated"))
		})

		It("Includes OBSOLETE when explicit lifecycle filter is set", func() {
			request := publicv1.DiskImagesListRequest_builder{}.Build()
			request.SetFilter(fmt.Sprintf("this.spec.lifecycle == %d",
				int32(publicv1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE)))
			response, err := server.List(ctx, request)
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(1))
			Expect(response.GetItems()[0].GetId()).To(Equal("image-obsolete"))
		})

		It("Returns DEPRECATED and OBSOLETE when user filter references lifecycle", func() {
			request := publicv1.DiskImagesListRequest_builder{}.Build()
			request.SetFilter(fmt.Sprintf("this.spec.lifecycle != %d",
				int32(publicv1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE)))
			response, err := server.List(ctx, request)
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(2))
			names := make([]string, len(response.GetItems()))
			for i, item := range response.GetItems() {
				names[i] = item.GetId()
			}
			Expect(names).To(ConsistOf("image-deprecated", "image-obsolete"))
		})
	})
})
