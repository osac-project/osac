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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const diskImageAPISourceRef = "quay.io/containerdisks/fedora:41"

func createPublicDiskImageFixture(ctx context.Context, client publicv1.DiskImagesClient, tenant string) *publicv1.DiskImage {
	GinkgoHelper()
	response, err := client.Create(ctx, publicv1.DiskImagesCreateRequest_builder{
		Object: publicv1.DiskImage_builder{
			Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: tenant}.Build(),
			Spec: publicv1.DiskImageSpec_builder{
				SourceType:    publicv1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:     diskImageAPISourceRef,
				GuestOsFamily: publicv1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
				Architecture:  []publicv1.Architecture{publicv1.Architecture_ARCHITECTURE_AMD64},
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	image := response.GetObject()
	Expect(image.GetId()).NotTo(BeEmpty())
	DeferCleanup(func(ctx context.Context) {
		_, err := client.Delete(ctx, publicv1.DiskImagesDeleteRequest_builder{Id: image.GetId()}.Build())
		Expect(err == nil || status.Code(err) == codes.NotFound).To(BeTrue(), "DiskImage cleanup failed: %v", err)
	})
	return image
}

var _ = Describe("DiskImage API", Label("disk-images"), func() {
	DescribeTable("public CRUD", func(ctx context.Context, persona string) {
		client := publicv1.NewDiskImagesClient(tool.ExternalView().UserConn())
		tenant := usersGroup
		requestedTenant := ""
		switch persona {
		case "provider admin":
			client = publicv1.NewDiskImagesClient(tool.ExternalView().AdminConn())
			tenant = "shared"
			requestedTenant = tenant
		case "tenant admin":
			name, conn := createCatalogItemTenantAdminFixture(ctx)
			tenant = name
			client = publicv1.NewDiskImagesClient(conn)
		}
		image := createPublicDiskImageFixture(ctx, client, requestedTenant)
		got, err := client.Get(ctx, publicv1.DiskImagesGetRequest_builder{Id: image.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(got.GetObject().GetMetadata().GetTenant()).To(Equal(tenant))
		spec := got.GetObject().GetSpec()
		Expect(spec.GetSourceRef()).To(Equal(diskImageAPISourceRef))
		Expect(spec.GetGuestOsFamily()).To(Equal(publicv1.GuestOSFamily_GUEST_OS_FAMILY_LINUX))
		Expect(spec.GetArchitecture()).To(ContainElement(publicv1.Architecture_ARCHITECTURE_AMD64))
		Expect(spec.GetLifecycle()).To(Equal(publicv1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE))
		listed, err := client.List(ctx, publicv1.DiskImagesListRequest_builder{
			Filter: new(fmt.Sprintf("this.metadata.name == %q", image.GetMetadata().GetName())),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(containsId(listed.GetItems(), image.GetId())).To(BeTrue())

		_, err = client.Delete(ctx, publicv1.DiskImagesDeleteRequest_builder{Id: image.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		_, err = client.Get(ctx, publicv1.DiskImagesGetRequest_builder{Id: image.GetId()}.Build())
		expectCatalogItemStatusCode(err, codes.NotFound)
	},
		Entry("provider admin registers a shared image", "provider admin"),
		Entry("tenant admin registers an own-tenant image", "tenant admin"),
		Entry("tenant user registers an own-tenant image", "tenant user"),
	)

	It("deprecates, hides obsolete images, filters explicitly, and reactivates", func(ctx context.Context) {
		client := publicv1.NewDiskImagesClient(tool.ExternalView().AdminConn())
		available := createPublicDiskImageFixture(ctx, client, "shared")
		image := createPublicDiskImageFixture(ctx, client, "shared")
		filter := fmt.Sprintf("this.metadata.name in [%q, %q]", available.GetMetadata().GetName(), image.GetMetadata().GetName())
		setLifecycle := func(lifecycle publicv1.DiskImageLifecycle) *publicv1.DiskImage {
			GinkgoHelper()
			_, err := client.Update(ctx, publicv1.DiskImagesUpdateRequest_builder{
				Object: publicv1.DiskImage_builder{
					Id: image.GetId(), Spec: publicv1.DiskImageSpec_builder{Lifecycle: lifecycle}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.lifecycle"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			got, err := client.Get(ctx, publicv1.DiskImagesGetRequest_builder{Id: image.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.GetObject().GetSpec().GetLifecycle()).To(Equal(lifecycle))
			return got.GetObject()
		}
		listImages := func(expression string) []*publicv1.DiskImage {
			GinkgoHelper()
			listed, err := client.List(ctx, publicv1.DiskImagesListRequest_builder{Filter: new(expression)}.Build())
			Expect(err).NotTo(HaveOccurred())
			return listed.GetItems()
		}

		deprecated := setLifecycle(publicv1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED)
		Expect(deprecated.GetSpec().GetDeprecation().GetDeprecationTimestamp()).NotTo(BeNil())
		Expect(containsId(listImages(filter), image.GetId())).To(BeTrue())

		obsolete := setLifecycle(publicv1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE)
		Expect(obsolete.GetSpec().GetDeprecation().GetObsolescenceTimestamp()).NotTo(BeNil())
		visible := listImages(filter)
		Expect(containsId(visible, available.GetId())).To(BeTrue())
		Expect(containsId(visible, image.GetId())).To(BeFalse())
		Expect(containsId(listImages(filter+" && this.spec.lifecycle == 3"), image.GetId())).To(BeTrue())

		reactivated := setLifecycle(publicv1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE)
		Expect(reactivated.GetSpec().GetDeprecation().GetDeprecationTimestamp()).To(BeNil())
		Expect(reactivated.GetSpec().GetDeprecation().GetObsolescenceTimestamp()).To(BeNil())
		Expect(containsId(listImages(filter), image.GetId())).To(BeTrue())
	})

	It("resolves template image defaults by name and protects the image until the template is deleted", func(ctx context.Context) {
		client := publicv1.NewDiskImagesClient(tool.ExternalView().AdminConn())
		image := createPublicDiskImageFixture(ctx, client, "shared")
		templateID := createCatalogItemComputeInstanceTemplateFixture(ctx, privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			DiskImage: privatev1.DiskImageReference_builder{Name: image.GetMetadata().GetName(), Shared: true}.Build(),
		}.Build(), nil)
		templates := privatev1.NewComputeInstanceTemplatesClient(tool.InternalView().AdminConn())
		got, err := templates.Get(ctx, privatev1.ComputeInstanceTemplatesGetRequest_builder{Id: templateID}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(got.GetObject().GetSpecDefaults().GetDiskImage().GetId()).To(Equal(image.GetId()))

		_, err = client.Delete(ctx, publicv1.DiskImagesDeleteRequest_builder{Id: image.GetId()}.Build())
		expectCatalogItemStatusCode(err, codes.FailedPrecondition)
		Expect(status.Convert(err).Message()).To(ContainSubstring("compute instance template"))
		_, err = templates.Delete(ctx, privatev1.ComputeInstanceTemplatesDeleteRequest_builder{Id: templateID}.Build())
		Expect(err).NotTo(HaveOccurred())
		_, err = client.Delete(ctx, publicv1.DiskImagesDeleteRequest_builder{Id: image.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		_, err = client.Get(ctx, publicv1.DiskImagesGetRequest_builder{Id: image.GetId()}.Build())
		expectCatalogItemStatusCode(err, codes.NotFound)
	})
})
