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

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Private bare metal instance catalog items server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateBareMetalInstanceCatalogItemsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewPrivateBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *PrivateBareMetalInstanceCatalogItemsServer

		BeforeEach(func() {
			var err error
			server, err = NewPrivateBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(seedBareMetalCatalogItemTemplate(ctx, testTenant, "", "my-template-id")).To(Succeed())
			Expect(seedBareMetalCatalogItemTemplate(ctx, auth.SharedTenant, "", "my-shared-template-id")).To(Succeed())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						Tenant: testTenant,
					}.Build(),
					Title:       "My catalog item",
					Description: "A test catalog item.",
					Template:    privatev1.BareMetalInstanceTemplateReference_builder{Id: "my-shared-template-id"}.Build(),
					Published:   true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetTitle()).To(Equal("My catalog item"))
			Expect(object.GetTemplate().GetId()).To(Equal("my-shared-template-id"))
			Expect(object.GetTemplate().GetShared()).To(BeTrue())
			Expect(object.GetPublished()).To(BeTrue())
			Expect(object.GetMetadata().GetTenant()).To(Equal(testTenant))
		})

		It("reports DiskImage deprecation on Create and skips revalidation for title-only PATCH", func() {
			images, err := dao.NewGenericDAO[*privatev1.DiskImage]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = images.Create().SetObject(privatev1.DiskImage_builder{
				Id: "deprecated-policy-image", Metadata: privatev1.Metadata_builder{Name: "deprecated-policy-image", Tenant: testTenant}.Build(),
				Spec: privatev1.DiskImageSpec_builder{Lifecycle: privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			response, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{Object: privatev1.BareMetalInstanceCatalogItem_builder{
				Metadata: privatev1.Metadata_builder{Name: "deprecated-policy-catalog"}.Build(),
				Template: privatev1.BareMetalInstanceTemplateReference_builder{Id: "my-template-id"}.Build(),
				Fields: privatev1.BareMetalInstanceCatalogItemFields_builder{DiskImage: privatev1.DiskImageReferenceFieldPolicy_builder{
					Editable: privatev1.EditableDiskImageReferenceField_builder{DefaultValue: privatev1.DiskImageReference_builder{Id: "deprecated-policy-image"}.Build()}.Build(),
				}.Build()}.Build(),
			}.Build()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetWarnings()).To(ConsistOf(ContainSubstring("deprecated")))
			updated, err := server.Update(ctx, privatev1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object:     privatev1.BareMetalInstanceCatalogItem_builder{Id: response.GetObject().GetId(), Title: "Updated title"}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetWarnings()).To(BeEmpty())
		})

		It("Lists objects", func() {
			const count = 5
			for i := range count {
				_, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
					Object: privatev1.BareMetalInstanceCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:    fmt.Sprintf("Catalog item %d", i),
						Template: privatev1.BareMetalInstanceTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := server.List(ctx, privatev1.BareMetalInstanceCatalogItemsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(count))
		})

		It("Updates object", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:    "Original title",
					Template: privatev1.BareMetalInstanceTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			name := object.GetMetadata().GetName()
			updateResponse, err := server.Update(ctx, privatev1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Id:       object.GetId(),
					Metadata: privatev1.Metadata_builder{Name: name}.Build(),
					Title:    "Updated title",
					Template: privatev1.BareMetalInstanceTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetTitle()).To(Equal("Updated title"))
		})

		It("rejects changing the template on update", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-baremetal-catalog-template-immutable"}.Build(),
					Title:    "Catalog item",
					Template: privatev1.BareMetalInstanceTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Update(ctx, privatev1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Id:       createResponse.GetObject().GetId(),
					Template: privatev1.BareMetalInstanceTemplateReference_builder{Id: "my-shared-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))

			getResponse, err := server.Get(ctx, privatev1.BareMetalInstanceCatalogItemsGetRequest_builder{Id: createResponse.GetObject().GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetTemplate().GetId()).To(Equal("my-template-id"))
		})

		It("Deletes object without references", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Finalizers: []string{"keep"},
						Name:       "test-baremetal-catalog-delete",
					}.Build(),
					Title:    "My catalog item",
					Template: privatev1.BareMetalInstanceTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			_, err = server.Delete(ctx, privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := server.Get(ctx, privatev1.BareMetalInstanceCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		It("Allows delete when referenced by a bare metal instance", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Referenced catalog item",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published: true,
					Fields: privatev1.BareMetalInstanceCatalogItemFields_builder{
						SshPublicKey: privatev1.StringFieldPolicy_builder{Editable: privatev1.EditableStringField_builder{}.Build()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catalogItem := createResponse.GetObject()

			bmiDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstance]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = bmiDao.Create().SetObject(
				privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "my-bmi",
						Tenant: "system",
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItem.GetId()}.Build(),
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Delete(ctx, privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
				Id: catalogItem.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object with typed fields", func() {
			response, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:    "Catalog item with fields",
					Template: privatev1.BareMetalInstanceTemplateReference_builder{Id: "my-template-id"}.Build(),
					Fields: privatev1.BareMetalInstanceCatalogItemFields_builder{
						RunStrategy: privatev1.BareMetalInstanceRunStrategyFieldPolicy_builder{Editable: privatev1.EditableBareMetalInstanceRunStrategyField_builder{}.Build()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetFields().GetRunStrategy()).ToNot(BeNil())
		})

		DescribeTable("validates SSH public key policy on Create", func(policy *privatev1.StringFieldPolicy, invalid bool) {
			response, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:    "SSH key policy",
					Template: privatev1.BareMetalInstanceTemplateReference_builder{Id: "my-template-id"}.Build(),
					Fields: privatev1.BareMetalInstanceCatalogItemFields_builder{
						SshPublicKey: policy,
					}.Build(),
				}.Build(),
			}.Build())
			if invalid {
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("ssh_public_key"))
				Expect(status.Message()).To(ContainSubstring("no behavior"))
				return
			}
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
		},
			Entry("rejects a policy without behavior", privatev1.StringFieldPolicy_builder{}.Build(), true),
			Entry("accepts a locked value", privatev1.StringFieldPolicy_builder{Locked: proto.String(testSSHPublicKey)}.Build(), false),
			Entry("accepts editable input without a default", privatev1.StringFieldPolicy_builder{Editable: privatev1.EditableStringField_builder{}.Build()}.Build(), false),
		)

		It("Rejects update that introduces non-editable field without default", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:    "Valid catalog item",
					Template: privatev1.BareMetalInstanceTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
					Id: id,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			_, err = server.Update(ctx, privatev1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Id: id,
					Fields: privatev1.BareMetalInstanceCatalogItemFields_builder{
						SshPublicKey: privatev1.StringFieldPolicy_builder{}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"fields"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("ssh_public_key"))
			Expect(status.Message()).To(ContainSubstring("oneof"))
		})

	})
})

var _ = Describe("Bare Metal Instance Catalog Item policy application", func() {
	It("applies every Bare Metal policy and deep-clones lists and messages", func() {
		instanceType := privatev1.BareMetalInstanceTypeLocalReference_builder{Id: "host-id", Name: "host"}.Build()
		diskImage := privatev1.DiskImageReference_builder{Id: "image-id", Name: "disk-image"}.Build()
		attachment := privatev1.BareMetalNetworkAttachment_builder{
			Subnet:         policyTestSubnet("bare-metal-subnet"),
			SecurityGroups: []*privatev1.SecurityGroupLocalReference{policyTestSecurityGroup("bare-metal-security-group")},
		}.Build()
		sshKey := "ssh-ed25519 bare-metal"
		userData := "bare-metal-user-data"
		runStrategy := privatev1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS
		autoExternalIP := false

		fields := privatev1.BareMetalInstanceCatalogItemFields_builder{
			SshPublicKey: privatev1.StringFieldPolicy_builder{Locked: &sshKey}.Build(),
			UserData:     privatev1.StringFieldPolicy_builder{Locked: &userData}.Build(),
			RunStrategy:  privatev1.BareMetalInstanceRunStrategyFieldPolicy_builder{Locked: &runStrategy}.Build(),
			NetworkAttachments: privatev1.BareMetalNetworkAttachmentListFieldPolicy_builder{
				Locked: privatev1.BareMetalNetworkAttachmentList_builder{Items: []*privatev1.BareMetalNetworkAttachment{nil, attachment}}.Build(),
			}.Build(),
			AutoExternalIpAttachment: privatev1.BoolFieldPolicy_builder{Locked: &autoExternalIP}.Build(),
			InstanceType:             privatev1.BareMetalInstanceTypeLocalReferenceFieldPolicy_builder{Locked: instanceType}.Build(),
			DiskImage:                privatev1.DiskImageReferenceFieldPolicy_builder{Locked: diskImage}.Build(),
		}.Build()
		item := privatev1.BareMetalInstanceCatalogItem_builder{Fields: fields}.Build()
		spec := &privatev1.BareMetalInstanceSpec{}

		Expect(applyBareMetalInstanceCatalogItemPolicies(spec, item.GetFields())).To(Succeed())
		Expect(spec.GetSshPublicKey()).To(Equal(sshKey))
		Expect(spec.GetUserData()).To(Equal(userData))
		Expect(spec.GetRunStrategy()).To(Equal(runStrategy))
		Expect(spec.GetNetworkAttachments()).To(HaveLen(2))
		Expect(spec.GetNetworkAttachments()[0]).To(BeNil())
		Expect(spec.GetNetworkAttachments()[1]).NotTo(BeIdenticalTo(attachment))
		Expect(spec.GetAutoExternalIpAttachment()).To(BeFalse())
		Expect(spec.GetInstanceType()).NotTo(BeIdenticalTo(instanceType))
		Expect(spec.GetDiskImage()).NotTo(BeIdenticalTo(diskImage))

		spec.GetNetworkAttachments()[1].GetSubnet().SetName("changed")
		spec.GetNetworkAttachments()[1].GetSecurityGroups()[0].SetName("changed")
		spec.GetInstanceType().SetName("changed")
		spec.GetDiskImage().SetName("changed")
		Expect(attachment.GetSubnet().GetName()).To(Equal("bare-metal-subnet"))
		Expect(attachment.GetSecurityGroups()[0].GetName()).To(Equal("bare-metal-security-group"))
		Expect(instanceType.GetName()).To(Equal("host"))
		Expect(diskImage.GetName()).To(Equal("disk-image"))
	})
})
