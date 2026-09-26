/*
Copyright (c) 2025 Red Hat Inc.

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
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"google.golang.org/protobuf/proto"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

var _ = Describe("Bare metal instance catalog items server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewBareMetalInstanceCatalogItemsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *BareMetalInstanceCatalogItemsServer

		BeforeEach(func() {
			var err error
			server, err = NewBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(seedBareMetalCatalogItemTemplate(ctx, testTenant, "", "my-bmi-template-id")).To(Succeed())
			Expect(err).ToNot(HaveOccurred())
		})

		DescribeTable("preserves private metadata on mask-less public updates", func(omitMetadata, lock, stale bool) {
			created, err := server.delegate.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{Name: "preserve-finalizers", Finalizers: []string{"cleanup"}}.Build(),
					Template: privatev1.BareMetalInstanceTemplateReference_builder{Id: "my-bmi-template-id"}.Build(),
					Title:    "Original", Published: true,
				}.Build(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			object := publicv1.BareMetalInstanceCatalogItem_builder{
				Id: created.GetObject().GetId(), Title: "Updated", Published: false,
				Template: publicv1.BareMetalInstanceTemplateReference_builder{Id: "my-bmi-template-id"}.Build(),
			}.Build()
			if !omitMetadata {
				version := created.GetObject().GetMetadata().GetVersion()
				if stale {
					version--
				}
				object.SetMetadata(publicv1.Metadata_builder{Name: "preserve-finalizers", Version: version}.Build())
			}
			_, err = server.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{Object: object, Lock: lock}.Build())
			rejected := lock && (stale || omitMetadata)
			if rejected {
				Expect(status.Code(err)).To(Equal(codes.Aborted))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
			stored, err := server.delegate.Get(ctx, privatev1.BareMetalInstanceCatalogItemsGetRequest_builder{Id: object.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(stored.GetObject().GetMetadata().GetFinalizers()).To(Equal([]string{"cleanup"}))
			if rejected {
				Expect(stored.GetObject().GetTitle()).To(Equal("Original"))
				Expect(stored.GetObject().GetPublished()).To(BeTrue())
			} else {
				Expect(stored.GetObject().GetTitle()).To(Equal("Updated"))
				Expect(stored.GetObject().GetPublished()).To(BeFalse())
			}
		},
			Entry("metadata supplied", false, false, false),
			Entry("metadata omitted", true, false, false),
			Entry("matching version", false, true, false),
			Entry("stale version", false, true, true),
			Entry("missing version", true, true, false),
		)

		It("Creates object", func() {
			response, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Title:       "My BMI catalog item",
					Description: "My description.",
					Template:    publicv1.BareMetalInstanceTemplateReference_builder{Id: "my-bmi-template-id"}.Build(),
					Published:   true,
					Metadata: publicv1.Metadata_builder{
						Name: "test-bmi-catalog-item",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetTitle()).To(Equal("My BMI catalog item"))
			Expect(object.GetTemplate().GetId()).To(Equal("my-bmi-template-id"))
			Expect(object.GetPublished()).To(BeTrue())
		})
		It("Updates object through the public API", func() {
			createResponse, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Metadata:  publicv1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.NewString()[:8])}.Build(),
					Title:     "Original title",
					Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: "my-bmi-template-id"}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := server.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Id:    createResponse.GetObject().GetId(),
					Title: "Updated title",
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetTitle()).To(Equal("Updated title"))
			Expect(updateResponse.GetObject().GetTemplate().GetId()).To(Equal("my-bmi-template-id"))
		})

		It("returns DiskImage deprecation warnings from Create and fields updates", func() {
			createDiskImageWithLifecycle("deprecated-public-create", privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED, nil)
			createDiskImageWithLifecycle("deprecated-public-update", privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED, nil)
			createResponse, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Metadata: publicv1.Metadata_builder{Name: "public-deprecated-policy-catalog"}.Build(),
					Template: publicv1.BareMetalInstanceTemplateReference_builder{Id: "my-bmi-template-id"}.Build(),
					Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{
						Editable: publicv1.EditableDiskImageReferenceField_builder{DefaultValue: publicv1.DiskImageReference_builder{Id: "deprecated-public-create"}.Build()}.Build(),
					}.Build()}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResponse.GetWarnings()).To(ConsistOf(ContainSubstring("deprecated")))

			item := proto.Clone(createResponse.GetObject()).(*publicv1.BareMetalInstanceCatalogItem)
			item.GetFields().GetDiskImage().GetEditable().SetDefaultValue(publicv1.DiskImageReference_builder{Id: "deprecated-public-update"}.Build())
			updateResponse, err := server.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object:     item,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"fields"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetWarnings()).To(ConsistOf(ContainSubstring("deprecated")))
		})

		It("Fails to create without an object", func() {
			_, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.InvalidArgument))
		})

		It("Lists drafts and published items with optional published filter", func() {
			const publishedCount = 3
			for i := range publishedCount {
				_, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
					Object: publicv1.BareMetalInstanceCatalogItem_builder{
						Title:     fmt.Sprintf("Published item %d", i),
						Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: "my-bmi-template-id"}.Build(),
						Published: true,
						Metadata: publicv1.Metadata_builder{
							Name: fmt.Sprintf("published-item-%d", i),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}
			_, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Title:     "Unpublished item",
					Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: "my-bmi-template-id"}.Build(),
					Published: false,
					Metadata: publicv1.Metadata_builder{
						Name: "unpublished-item",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			response, err := server.List(ctx, publicv1.BareMetalInstanceCatalogItemsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(publishedCount + 1))
			response, err = server.List(ctx, publicv1.BareMetalInstanceCatalogItemsListRequest_builder{Filter: new("this.published")}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(publishedCount))
			for _, item := range response.GetItems() {
				Expect(item.GetPublished()).To(BeTrue())
			}
		})

		It("Rejects an invalid filter", func() {
			_, err := server.List(ctx, publicv1.BareMetalInstanceCatalogItemsListRequest_builder{
				Filter: new("!!!invalid!!!"),
			}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.InvalidArgument))
		})

		It("Gets a published object", func() {
			createResponse, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Title:     "Published item",
					Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: "my-bmi-template-id"}.Build(),
					Published: true,
					Metadata: publicv1.Metadata_builder{
						Name: "published-item",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			getResponse, err := server.Get(ctx, publicv1.BareMetalInstanceCatalogItemsGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetId()).To(Equal(id))
		})

		It("Reads unpublished object within normal visibility", func() {
			createResponse, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Title:     "Unpublished item",
					Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: "my-bmi-template-id"}.Build(),
					Published: false,
					Metadata: publicv1.Metadata_builder{
						Name: "unpublished-item-get",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			_, err = server.Get(ctx, publicv1.BareMetalInstanceCatalogItemsGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects update of the name of BareMetalInstanceCatalogItem", func() {
			createResponse, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Title:     "Original title",
					Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: "my-bmi-template-id"}.Build(),
					Published: true,
					Metadata: publicv1.Metadata_builder{
						Name: "original-item",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			_, err = server.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Id:        id,
					Title:     "Updated title",
					Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: "my-bmi-template-id"}.Build(),
					Published: true,
					Metadata: publicv1.Metadata_builder{
						Name: "test-bmi-catalog-item-update",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("immutable"))
		})

		It("Updates an object", func() {
			createResponse, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Title:     "Original title",
					Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: "my-bmi-template-id"}.Build(),
					Published: true,
					Metadata: publicv1.Metadata_builder{
						Name: "original-item",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()
			name := createResponse.GetObject().GetMetadata().GetName()
			updateResponse, err := server.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Id:        id,
					Title:     "Updated title",
					Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: "my-bmi-template-id"}.Build(),
					Published: true,
					Metadata: publicv1.Metadata_builder{
						Name: name,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetTitle()).To(Equal("Updated title"))
		})

		It("Fails to update without an object", func() {
			_, err := server.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.InvalidArgument))
		})

		It("Fails to update without an object identifier", func() {
			_, err := server.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.InvalidArgument))
		})

		It("Deletes an object", func() {
			createResponse, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Title:     "Item to delete",
					Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: "my-bmi-template-id"}.Build(),
					Published: true,
					Metadata: publicv1.Metadata_builder{
						Name: "item-to-delete",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			_, err = server.Delete(ctx, publicv1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Get(ctx, publicv1.BareMetalInstanceCatalogItemsGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.NotFound))
		})

		It("Deletes an object that is referenced by a bare metal instance", func() {
			createResponse, err := server.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Title:     "Referenced item",
					Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: "my-bmi-template-id"}.Build(),
					Published: true,
					Metadata: publicv1.Metadata_builder{
						Name: "referenced-item",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catalogItemID := createResponse.GetObject().GetId()

			instancesServer, err := NewPrivateBareMetalInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			createDiskImageWithLifecycle("default-bmi-disk-image",
				privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE, nil)
			seedTenantDefaultNetworking(testTenant, "", new("netris"))
			_, err = instancesServer.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						DiskImage:    privatev1.DiskImageReference_builder{Id: "default-bmi-disk-image"}.Build(),
						CatalogItem:  privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
						SshPublicKey: new(testSSHPublicKey),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Delete(ctx, publicv1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
				Id: catalogItemID,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Returns not found for a nonexistent object", func() {
			_, err := server.Get(ctx, publicv1.BareMetalInstanceCatalogItemsGetRequest_builder{
				Id: "00000000-0000-0000-0000-000000000000",
			}.Build())
			Expect(err).To(HaveOccurred())
			s, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(s.Code()).To(Equal(codes.NotFound))
		})
	})
})

var _ = Describe("Catalog publication and references", func() {
	It("BareMetalInstance: unpublishes unusable offerings and validates republishing", func() {
		Expect(seedBareMetalCatalogItemTemplate(ctx, auth.SharedTenant, "", "template-id")).To(Succeed())
		Expect(seedBareMetalCatalogItemTemplate(ctx, testTenant, "", "own-template-id")).To(Succeed())
		dependencyDAO, err := dao.NewGenericDAO[*privatev1.DiskImage]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		dependencyResponse, err := dependencyDAO.Create().SetObject(privatev1.DiskImage_builder{
			Metadata: privatev1.Metadata_builder{Name: "dependency", Tenant: auth.SharedTenant}.Build(), Spec: privatev1.DiskImageSpec_builder{Lifecycle: privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		dependency := dependencyResponse.GetObject()
		server, err := NewBareMetalInstanceCatalogItemsServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		request := publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{Object: publicv1.BareMetalInstanceCatalogItem_builder{
			Metadata: publicv1.Metadata_builder{Name: "offering"}.Build(), Title: "Offering", Published: true,
			Template: publicv1.BareMetalInstanceTemplateReference_builder{Name: "my-bare-metal-template", Shared: true}.Build(),
			Fields:   publicv1.BareMetalInstanceCatalogItemFields_builder{DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{Locked: publicv1.DiskImageReference_builder{Id: dependency.GetId()}.Build()}.Build()}.Build(),
		}.Build()}.Build()
		original := proto.Clone(request)
		result, err := server.Create(ctx, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Equal(request, original)).To(BeTrue())
		item := result.GetObject()
		Expect(item.GetTemplate().GetId()).To(Equal("template-id"))
		dependency.GetSpec().SetLifecycle(privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE)
		_, err = dependencyDAO.Update().SetObject(dependency).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		update := func(object *publicv1.BareMetalInstanceCatalogItem, paths ...string) error {
			request := publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: paths}}.Build()
			_, err := server.Update(ctx, request)
			return err
		}
		mixed := proto.Clone(item).(*publicv1.BareMetalInstanceCatalogItem)
		mixed.SetPublished(false)
		mixed.GetFields().GetDiskImage().SetLocked(publicv1.DiskImageReference_builder{Id: "missing"}.Build())
		Expect(update(mixed, "published", "fields")).ToNot(Succeed())
		item.SetPublished(false)
		// References outside the mask must not affect the merged candidate.
		partial := publicv1.BareMetalInstanceCatalogItem_builder{Id: item.GetId(), Published: false, Template: publicv1.BareMetalInstanceTemplateReference_builder{Id: "missing"}.Build()}.Build()
		Expect(update(partial, "published")).To(Succeed())
		item.SetTitle("Edited draft")
		Expect(update(item, "title")).To(Succeed())
		item.SetPublished(true)
		Expect(status.Code(update(item, "published"))).To(Equal(codes.FailedPrecondition))
		item.SetPublished(false)
		Expect(status.Code(update(item, "published", "fields"))).To(Equal(codes.OK))
		item.GetFields().GetDiskImage().SetLocked(publicv1.DiskImageReference_builder{Id: "missing"}.Build())
		Expect(update(item, "published", "fields")).ToNot(Succeed())
	})
})
