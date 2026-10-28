/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Bare metal architecture admission", func() {
	var (
		privateServer    *PrivateBareMetalInstancesServer
		publicServer     *BareMetalInstancesServer
		images           *dao.GenericDAO[*privatev1.DiskImage]
		templates        *dao.GenericDAO[*privatev1.BareMetalInstanceTemplate]
		catalogServer    *PrivateBareMetalInstanceCatalogItemsServer
		toPublic         *GenericMapper[*privatev1.BareMetalInstance, *publicv1.BareMetalInstance]
		toPrivate        *GenericMapper[*publicv1.BareMetalInstance, *privatev1.BareMetalInstance]
		parameterDefault *anypb.Any
	)
	BeforeEach(func() {
		var err error
		privateServer, err = NewPrivateBareMetalInstancesServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		publicServer, err = NewBareMetalInstancesServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		catalogServer, err = NewPrivateBareMetalInstanceCatalogItemsServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		toPublic, err = NewGenericMapper[*privatev1.BareMetalInstance, *publicv1.BareMetalInstance]().SetLogger(logger).SetStrict(false).Build()
		Expect(err).NotTo(HaveOccurred())
		toPrivate, err = NewGenericMapper[*publicv1.BareMetalInstance, *privatev1.BareMetalInstance]().SetLogger(logger).SetStrict(false).Build()
		Expect(err).NotTo(HaveOccurred())
		images, err = dao.NewGenericDAO[*privatev1.DiskImage]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		templates, err = dao.NewGenericDAO[*privatev1.BareMetalInstanceTemplate]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		types, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceType]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		for i, cpu := range []string{"amd64", "arm64", "s390x"} {
			instanceType := bareMetalInstanceTypeFixture("type-"+cpu, bareMetalNetworkPort("data-0", "fabric"))
			instanceType.GetSpec().GetHardware().GetCpu().SetArchitecture(cpu)
			_, err = types.Create().SetObject(instanceType).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			_, err = images.Create().SetObject(privatev1.DiskImage_builder{
				Id:       "image-" + cpu,
				Metadata: privatev1.Metadata_builder{Name: "image-" + cpu, Tenant: auth.SharedTenant}.Build(),
				Spec: privatev1.DiskImageSpec_builder{
					Architecture: []privatev1.Architecture{[]privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64, privatev1.Architecture_ARCHITECTURE_ARM64, privatev1.Architecture_ARCHITECTURE_S390X}[i]},
					SourceRef:    "oci://private.example/secret-image",
					Lifecycle:    privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE,
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
		}
		seedTenantDefaultNetworking(testTenant, "", new("netris"))
		parameterDefault, err = anypb.New(wrapperspb.String("default-layout"))
		Expect(err).NotTo(HaveOccurred())
		_, err = templates.Create().SetObject(privatev1.BareMetalInstanceTemplate_builder{
			Id: "architecture-template", Title: "Architecture template",
			Metadata:     privatev1.Metadata_builder{Name: "architecture-template", Tenant: testTenant}.Build(),
			InstanceType: bareMetalInstanceTypeReference("type-amd64"),
			Parameters: []*privatev1.BareMetalInstanceTemplateParameterDefinition{
				{Name: "layout", Type: "type.googleapis.com/google.protobuf.StringValue", Default: parameterDefault},
			},
		}.Build()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
	})

	object := func(name, cpu, image string, byName bool) *privatev1.BareMetalInstance {
		typeRef := bareMetalInstanceTypeReference("type-" + cpu)
		imageRef := privatev1.DiskImageReference_builder{Id: "image-" + image, Shared: true}.Build()
		if byName {
			typeRef.SetName(typeRef.GetId())
			typeRef.SetId("")
			imageRef.SetName(imageRef.GetId())
			imageRef.SetId("")
		}
		return privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{Name: name}.Build(),
			Spec: privatev1.BareMetalInstanceSpec_builder{
				Template:     privatev1.BareMetalInstanceTemplateReference_builder{Id: "architecture-template"}.Build(),
				InstanceType: typeRef, DiskImage: imageRef, SshPublicKey: new(testSSHPublicKey),
			}.Build(),
		}.Build()
	}
	create := func(callCtx context.Context, obj *privatev1.BareMetalInstance, public bool) (*privatev1.BareMetalInstance, []string, error) {
		if !public {
			response, err := privateServer.Create(callCtx, privatev1.BareMetalInstancesCreateRequest_builder{Object: obj}.Build())
			return response.GetObject(), response.GetWarnings(), err
		}
		publicObject := &publicv1.BareMetalInstance{}
		Expect(toPublic.Copy(callCtx, obj, publicObject)).To(Succeed())
		response, err := publicServer.Create(callCtx, publicv1.BareMetalInstancesCreateRequest_builder{Object: publicObject}.Build())
		if err != nil {
			return nil, nil, err
		}
		result := &privatev1.BareMetalInstance{}
		Expect(toPrivate.Copy(callCtx, response.GetObject(), result)).To(Succeed())
		return result, response.GetWarnings(), nil
	}
	assertAbsent := func(name string) {
		response, err := privateServer.List(ctx, privatev1.BareMetalInstancesListRequest_builder{Filter: new(fmt.Sprintf("this.metadata.name == '%s'", name))}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(response.GetItems()).To(BeEmpty())
	}
	assertMismatch := func(err error) {
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(And(
			HavePrefix("INCOMPATIBLE_DISK_IMAGE_ARCHITECTURE: "),
			ContainSubstring("supports ["), ContainSubstring("requires "),
			ContainSubstring("Select a compatible DiskImage or BareMetalInstanceType"),
			Not(ContainSubstring("resourceClass")), Not(ContainSubstring("oci://")),
		))
	}
	catalogObject := func(typeMode, imageMode, image string, byName bool) *privatev1.BareMetalInstanceCatalogItem {
		typeRef := bareMetalInstanceTypeReference("type-amd64")
		imageRef := privatev1.DiskImageReference_builder{Id: "image-" + image, Shared: true}.Build()
		if byName {
			typeRef.SetName(typeRef.GetId())
			typeRef.SetId("")
			imageRef.SetName(imageRef.GetId())
			imageRef.SetId("")
		}
		fields := &privatev1.BareMetalInstanceCatalogItemFields{}
		switch typeMode {
		case "locked":
			fields.SetInstanceType(privatev1.BareMetalInstanceTypeReferenceFieldPolicy_builder{Locked: typeRef}.Build())
		case "default":
			fields.SetInstanceType(privatev1.BareMetalInstanceTypeReferenceFieldPolicy_builder{Editable: privatev1.EditableBareMetalInstanceTypeReferenceField_builder{DefaultValue: typeRef}.Build()}.Build())
		case "input":
			fields.SetInstanceType(privatev1.BareMetalInstanceTypeReferenceFieldPolicy_builder{Editable: privatev1.EditableBareMetalInstanceTypeReferenceField_builder{}.Build()}.Build())
		}
		switch imageMode {
		case "locked":
			fields.SetDiskImage(privatev1.DiskImageReferenceFieldPolicy_builder{Locked: imageRef}.Build())
		case "default":
			fields.SetDiskImage(privatev1.DiskImageReferenceFieldPolicy_builder{Editable: privatev1.EditableDiskImageReferenceField_builder{DefaultValue: imageRef}.Build()}.Build())
		case "input":
			fields.SetDiskImage(privatev1.DiskImageReferenceFieldPolicy_builder{Editable: privatev1.EditableDiskImageReferenceField_builder{}.Build()}.Build())
		}
		return privatev1.BareMetalInstanceCatalogItem_builder{
			Metadata: privatev1.Metadata_builder{Name: "architecture-catalog"}.Build(), Title: "Architecture catalog", Published: true,
			Template: privatev1.BareMetalInstanceTemplateReference_builder{Id: "architecture-template"}.Build(), Fields: fields,
		}.Build()
	}

	Describe("Template API", func() {
		var service *PrivateBareMetalInstanceTemplatesServer
		var original *privatev1.BareMetalInstanceTemplate
		BeforeEach(func() {
			var err error
			service, err = NewPrivateBareMetalInstanceTemplatesServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).NotTo(HaveOccurred())
			response, err := service.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Id: "architecture_update_template", Title: "Architecture template",
					InstanceType: bareMetalInstanceTypeReference("type-amd64"),
				}.Build(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			original = response.GetObject()
		})
		DescribeTable("validates inherited catalog images before changing the type", func(typeMode, imageMode string, multiarch, deleting, published, masked, byName, rejected bool) {
			if multiarch {
				image, err := images.Get().SetId("image-amd64").Do(ctx)
				Expect(err).NotTo(HaveOccurred())
				image.GetObject().GetSpec().SetArchitecture([]privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64, privatev1.Architecture_ARCHITECTURE_ARM64})
				_, err = images.Update().SetObject(image.GetObject()).Do(ctx)
				Expect(err).NotTo(HaveOccurred())
			}
			item := catalogObject(typeMode, imageMode, "amd64", false)
			item.SetTemplate(privatev1.BareMetalInstanceTemplateReference_builder{Id: original.GetId()}.Build())
			item.SetPublished(published)
			if deleting {
				item.GetMetadata().SetFinalizers([]string{"fixture.test/hold"})
			}
			catalog, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{Object: item}.Build())
			Expect(err).NotTo(HaveOccurred())
			if deleting {
				_, err = catalogServer.Delete(ctx, privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{Id: catalog.GetObject().GetId()}.Build())
				Expect(err).NotTo(HaveOccurred())
			}
			candidate := proto.Clone(original).(*privatev1.BareMetalInstanceTemplate)
			ref := bareMetalInstanceTypeReference("type-arm64")
			if byName {
				ref.SetName("type-arm64")
				ref.SetId("")
			}
			candidate.SetInstanceType(ref)
			candidate.SetTitle("Updated title")
			request := privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{Object: candidate}.Build()
			if masked {
				request.SetUpdateMask(&fieldmaskpb.FieldMask{Paths: []string{"instance_type", "title"}})
			}
			response, err := service.Update(ctx, request)
			stored, getErr := service.Get(ctx, privatev1.BareMetalInstanceTemplatesGetRequest_builder{Id: original.GetId()}.Build())
			Expect(getErr).NotTo(HaveOccurred())
			if rejected {
				Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
				Expect(status.Convert(err).Message()).To(And(HavePrefix("INCOMPATIBLE_DISK_IMAGE_ARCHITECTURE:"),
					Not(ContainSubstring(catalog.GetObject().GetId())), Not(ContainSubstring("oci://"))))
				Expect(proto.Equal(stored.GetObject(), original)).To(BeTrue(), "rejected changes must preserve all fields and the version")
			} else {
				Expect(err).NotTo(HaveOccurred())
				Expect(response.GetObject().GetInstanceType().GetId()).To(Equal("type-arm64"))
				Expect(proto.Equal(stored.GetObject(), response.GetObject())).To(BeTrue())
			}
		},
			Entry("inherited type and locked image", "absent", "locked", false, false, true, false, false, true),
			Entry("masked inherited type and image default", "absent", "default", false, false, true, true, false, true),
			Entry("type resolved by name", "absent", "locked", false, false, true, true, true, true),
			Entry("unpublished catalog is protected", "absent", "locked", false, false, false, true, false, true),
			Entry("compatible multiarch image", "absent", "locked", true, false, true, true, false, false),
			Entry("compatible name reference is canonicalized", "absent", "default", true, false, true, true, true, false),
			Entry("locked type overrides template", "locked", "locked", false, false, true, true, false, false),
			Entry("default type overrides template", "default", "default", false, false, true, true, false, false),
			Entry("open type overrides template", "input", "locked", false, false, true, true, false, false),
			Entry("open image is selected at admission", "absent", "input", false, false, true, true, false, false),
			Entry("deleted catalog is ignored", "absent", "locked", false, true, true, true, false, false))

		It("allows descriptive edits without revalidating legacy incompatible catalogs", func() {
			item := catalogObject("absent", "locked", "arm64", false)
			item.GetMetadata().SetTenant(testTenant)
			item.SetTemplate(privatev1.BareMetalInstanceTemplateReference_builder{Id: original.GetId()}.Build())
			_, err := catalogServer.generic.dao.Create().SetObject(item).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			response, err := service.Update(ctx, privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{
				Object:     privatev1.BareMetalInstanceTemplate_builder{Id: original.GetId(), Title: "Updated title"}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(response.GetObject().GetTitle()).To(Equal("Updated title"))
			Expect(proto.Equal(response.GetObject().GetInstanceType(), original.GetInstanceType())).To(BeTrue())
		})

		It("protects inherited catalogs in tenants the template owner cannot list", func() {
			createTenant("hidden-tenant")
			shared := proto.Clone(original).(*privatev1.BareMetalInstanceTemplate)
			shared.SetId("shared_architecture_template")
			shared.SetMetadata(privatev1.Metadata_builder{Name: "shared-architecture-template", Tenant: auth.SharedTenant}.Build())
			response, err := service.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{Object: shared}.Build())
			Expect(err).NotTo(HaveOccurred())
			shared = response.GetObject()
			item := catalogObject("absent", "locked", "amd64", false)
			item.GetMetadata().SetTenant("hidden-tenant")
			item.SetTemplate(privatev1.BareMetalInstanceTemplateReference_builder{Id: shared.GetId(), Shared: true}.Build())
			catalog, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{Object: item}.Build())
			Expect(err).NotTo(HaveOccurred())
			visibility, err := auth.NewVisibility().AddVisibleTenants(testTenant, auth.SharedTenant).Build()
			Expect(err).NotTo(HaveOccurred())
			restricted := auth.NewMockTenancyLogic(ctrl)
			restricted.EXPECT().DetermineVisibility(gomock.Any()).Return(visibility, nil).AnyTimes()
			restricted.EXPECT().DetermineAssignableTenants(gomock.Any()).Return(auth.AllTenants, nil).AnyTimes()
			restrictedService, err := NewPrivateBareMetalInstanceTemplatesServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(restricted).Build()
			Expect(err).NotTo(HaveOccurred())
			_, err = restrictedService.Update(ctx, privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{
				Object:     privatev1.BareMetalInstanceTemplate_builder{Id: shared.GetId(), InstanceType: bareMetalInstanceTypeReference("type-arm64")}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"instance_type"}},
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(status.Convert(err).Message()).NotTo(Or(ContainSubstring("hidden-tenant"), ContainSubstring(catalog.GetObject().GetId()), ContainSubstring("oci://")))
			stored, err := service.Get(ctx, privatev1.BareMetalInstanceTemplatesGetRequest_builder{Id: shared.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(proto.Equal(stored.GetObject(), shared)).To(BeTrue())
		})

		It("rechecks image architectures after waiting for a concurrent image update", func() {
			image, err := images.Get().SetId("image-amd64").Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			image.GetObject().GetSpec().SetArchitecture([]privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64, privatev1.Architecture_ARCHITECTURE_ARM64})
			_, err = images.Update().SetObject(image.GetObject()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			item := catalogObject("absent", "locked", "amd64", false)
			item.SetTemplate(privatev1.BareMetalInstanceTemplateReference_builder{Id: original.GetId()}.Build())
			_, err = catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{Object: item}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(suiteTx.End(ctx)).To(Succeed())
			suiteTx = nil
			callCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			DeferCleanup(cancel)
			writer, err := tm.Begin(callCtx)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				cancel()
				if writer != nil {
					Expect(writer.End(context.Background())).To(Succeed())
				}
			})
			imageService, err := NewPrivateDiskImagesServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).NotTo(HaveOccurred())
			_, err = imageService.Update(database.TxIntoContext(callCtx, writer), privatev1.DiskImagesUpdateRequest_builder{
				Object: privatev1.DiskImage_builder{Id: "image-amd64", Spec: privatev1.DiskImageSpec_builder{
					Architecture: []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64},
				}.Build()}.Build(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.architecture"}},
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			reader, err := tm.Begin(callCtx)
			Expect(err).NotTo(HaveOccurred())
			readerCtx := database.TxIntoContext(callCtx, reader)
			var pid int
			Expect(reader.QueryRow(readerCtx, "select pg_backend_pid()").Scan(&pid)).To(Succeed())
			result := make(chan error, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer GinkgoRecover()
				_, updateErr := service.Update(readerCtx, privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{
					Object:     privatev1.BareMetalInstanceTemplate_builder{Id: original.GetId(), InstanceType: bareMetalInstanceTypeReference("type-arm64")}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"instance_type"}},
				}.Build())
				reader.ReportError(&updateErr)
				Expect(reader.End(context.Background())).To(Succeed())
				result <- updateErr
			}()
			DeferCleanup(func() {
				cancel()
				Eventually(done, 5*time.Second).Should(BeClosed())
			})
			Eventually(func() (bool, error) {
				var waiting bool
				err := testPool.QueryRow(callCtx, "select exists (select 1 from pg_stat_activity where pid=$1 and wait_event_type='Lock')", pid).Scan(&waiting)
				return waiting, err
			}, 10*time.Second, 10*time.Millisecond).Should(BeTrue())
			Expect(writer.End(callCtx)).To(Succeed())
			writer = nil
			var updateErr error
			Eventually(result, 10*time.Second).Should(Receive(&updateErr))
			Expect(status.Code(updateErr)).To(Equal(codes.FailedPrecondition))
			managerCtx := database.TxManagerIntoContext(callCtx, tm)
			_, err = database.WithNewTx(managerCtx, func(checkCtx context.Context) (bool, error) {
				stored, err := service.Get(checkCtx, privatev1.BareMetalInstanceTemplatesGetRequest_builder{Id: original.GetId()}.Build())
				if err != nil {
					return false, err
				}
				Expect(proto.Equal(stored.GetObject(), original)).To(BeTrue())
				return true, nil
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	DescribeTableSubtree("Catalog API", func(public bool) {
		createCatalog := func(callCtx context.Context, item *privatev1.BareMetalInstanceCatalogItem) error {
			if !public {
				_, err := catalogServer.Create(callCtx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{Object: item}.Build())
				return err
			}
			server, err := NewBareMetalInstanceCatalogItemsServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).NotTo(HaveOccurred())
			mapper, err := NewGenericMapper[*privatev1.BareMetalInstanceCatalogItem, *publicv1.BareMetalInstanceCatalogItem]().SetLogger(logger).SetStrict(false).Build()
			Expect(err).NotTo(HaveOccurred())
			mapped := &publicv1.BareMetalInstanceCatalogItem{}
			Expect(mapper.Copy(callCtx, item, mapped)).To(Succeed())
			_, err = server.Create(callCtx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{Object: mapped}.Build())
			return err
		}
		DescribeTable("validates concrete catalog combinations", func(typeMode, imageMode, image string, byName, dryRun bool) {
			item := catalogObject(typeMode, imageMode, image, byName)
			before := proto.Clone(item)
			callCtx := ctx
			if dryRun {
				callCtx = dryRunCtx()
			}
			err := createCatalog(callCtx, item)
			rejected := image != "amd64" && typeMode != "input" && imageMode != "input" && imageMode != "absent"
			if rejected {
				assertMismatch(err)
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
			stored, err := catalogServer.List(ctx, privatev1.BareMetalInstanceCatalogItemsListRequest_builder{}.Build())
			Expect(err).NotTo(HaveOccurred())
			if rejected || dryRun {
				Expect(stored.GetItems()).To(BeEmpty())
			} else {
				Expect(stored.GetItems()).To(HaveLen(1))
			}
			Expect(proto.Equal(item, before)).To(BeTrue())
		},
			Entry("locked match", "locked", "locked", "amd64", false, false),
			Entry("default match", "default", "default", "amd64", false, false),
			Entry("locked mismatch", "locked", "locked", "arm64", false, false),
			Entry("default mismatch", "default", "default", "arm64", false, false),
			Entry("locked type and default image mismatch", "locked", "default", "s390x", false, false),
			Entry("default type and locked image mismatch", "default", "locked", "arm64", false, false),
			Entry("template type match", "template", "default", "amd64", false, false),
			Entry("template type mismatch", "template", "locked", "arm64", false, false),
			Entry("name match", "locked", "locked", "amd64", true, false),
			Entry("name mismatch", "default", "default", "arm64", true, false),
			Entry("dry-run match", "locked", "locked", "amd64", false, true),
			Entry("dry-run mismatch", "locked", "locked", "arm64", false, true),
			Entry("caller selects type", "input", "locked", "arm64", false, false),
			Entry("caller selects image", "locked", "input", "arm64", false, false),
			Entry("image omitted", "locked", "absent", "arm64", false, false),
		)
		It("rejects an incompatible unpublished catalog item", func() {
			item := catalogObject("locked", "locked", "arm64", false)
			item.SetPublished(false)
			assertMismatch(createCatalog(ctx, item))
		})
		DescribeTable("uses the catalog type instead of the template type", func(compatible bool) {
			image := "amd64"
			if compatible {
				image = "arm64"
			}
			item := catalogObject("locked", "locked", image, false)
			item.GetFields().GetInstanceType().SetLocked(bareMetalInstanceTypeReference("type-arm64"))
			err := createCatalog(ctx, item)
			if compatible {
				Expect(err).NotTo(HaveOccurred())
			} else {
				assertMismatch(err)
			}
		}, Entry("matching catalog type", true), Entry("mismatching catalog type", false))
		It("accepts a matching member of a multi-architecture image", func() {
			image, err := images.Get().SetId("image-arm64").Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			image.GetObject().GetSpec().SetArchitecture([]privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_ARM64, privatev1.Architecture_ARCHITECTURE_AMD64})
			_, err = images.Update().SetObject(image.GetObject()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(createCatalog(ctx, catalogObject("locked", "default", "arm64", false))).To(Succeed())
		})
	}, Entry("private", false), Entry("public", true))

	DescribeTable("checks merged catalog updates and preserves the stored item on rejection", func(field string) {
		created, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{Object: catalogObject("locked", "default", "amd64", false)}.Build())
		Expect(err).NotTo(HaveOccurred())
		update := privatev1.BareMetalInstanceCatalogItem_builder{Id: created.GetObject().GetId(), Fields: &privatev1.BareMetalInstanceCatalogItemFields{}}.Build()
		if field == "fields.instance_type" {
			update.GetFields().SetInstanceType(privatev1.BareMetalInstanceTypeReferenceFieldPolicy_builder{Locked: bareMetalInstanceTypeReference("type-arm64")}.Build())
		} else {
			update.GetFields().SetDiskImage(privatev1.DiskImageReferenceFieldPolicy_builder{Locked: privatev1.DiskImageReference_builder{Id: "image-arm64", Shared: true}.Build()}.Build())
		}
		_, err = catalogServer.Update(ctx, privatev1.BareMetalInstanceCatalogItemsUpdateRequest_builder{Object: update, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{field}}}.Build())
		assertMismatch(err)
		stored, err := catalogServer.Get(ctx, privatev1.BareMetalInstanceCatalogItemsGetRequest_builder{Id: created.GetObject().GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(stored.GetObject(), created.GetObject())).To(BeTrue())
	}, Entry("instance type only", "fields.instance_type"), Entry("disk image only", "fields.disk_image"))

	DescribeTableSubtree("API", func(public bool) {
		DescribeTable("compares canonical architectures", func(cpu, image string, dryRun bool) {
			name := "architecture-request"
			obj := object(name, cpu, image, false)
			before := proto.Clone(obj)
			callCtx := ctx
			if dryRun {
				callCtx = dryRunCtx()
			}
			result, _, err := create(callCtx, obj, public)
			if cpu != image {
				assertMismatch(err)
				Expect(result).To(BeNil())
				assertAbsent(name)
			} else {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.GetSpec().GetInstanceType().GetId()).To(Equal("type-" + cpu))
				Expect(result.GetSpec().GetDiskImage().GetId()).To(Equal("image-" + image))
				Expect(proto.Equal(result.GetSpec().GetTemplateParameters()["layout"], parameterDefault)).To(BeTrue())
				if dryRun {
					assertAbsent(name)
				} else {
					stored, readErr := privateServer.Get(ctx, privatev1.BareMetalInstancesGetRequest_builder{Id: result.GetId()}.Build())
					Expect(readErr).NotTo(HaveOccurred())
					Expect(stored.GetObject().GetSpec().GetInstanceType().GetId()).To(Equal("type-" + cpu))
					Expect(stored.GetObject().GetSpec().GetDiskImage().GetId()).To(Equal("image-" + image))
					Expect(proto.Equal(stored.GetObject().GetSpec().GetTemplateParameters()["layout"], parameterDefault)).To(BeTrue())
				}
			}
			Expect(proto.Equal(obj, before)).To(BeTrue())
		},
			Entry("amd64 with amd64", "amd64", "amd64", false),
			Entry("amd64 with amd64, dry-run", "amd64", "amd64", true),
			Entry("amd64 with arm64", "amd64", "arm64", false),
			Entry("amd64 with arm64, dry-run", "amd64", "arm64", true),
			Entry("amd64 with s390x", "amd64", "s390x", false),
			Entry("amd64 with s390x, dry-run", "amd64", "s390x", true),
			Entry("arm64 with amd64", "arm64", "amd64", false),
			Entry("arm64 with amd64, dry-run", "arm64", "amd64", true),
			Entry("arm64 with arm64", "arm64", "arm64", false),
			Entry("arm64 with arm64, dry-run", "arm64", "arm64", true),
			Entry("arm64 with s390x", "arm64", "s390x", false),
			Entry("arm64 with s390x, dry-run", "arm64", "s390x", true),
			Entry("s390x with amd64", "s390x", "amd64", false),
			Entry("s390x with amd64, dry-run", "s390x", "amd64", true),
			Entry("s390x with arm64", "s390x", "arm64", false),
			Entry("s390x with arm64, dry-run", "s390x", "arm64", true),
			Entry("s390x with s390x", "s390x", "s390x", false),
			Entry("s390x with s390x, dry-run", "s390x", "s390x", true),
		)
		DescribeTable("validates template-provided types and names", func(byName, explicitType, compatible bool) {
			image := "arm64"
			if compatible {
				image = "amd64"
			}
			obj := object("reference-request", "amd64", image, byName)
			if !explicitType {
				obj.GetSpec().ClearInstanceType()
			}
			_, _, err := create(ctx, obj, public)
			if compatible {
				Expect(err).NotTo(HaveOccurred())
			} else {
				assertMismatch(err)
				assertAbsent("reference-request")
			}
		},
			Entry("type IDs", false, true, false), Entry("type names", true, true, false),
			Entry("template ID mismatch", false, false, false), Entry("template name mismatch", true, false, false),
			Entry("template ID match", false, false, true), Entry("template name match", true, false, true),
		)
		DescribeTable("checks every member of a multi-architecture image", func(cpu string, reverse bool) {
			image, err := images.Get().SetId("image-amd64").Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			members := []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64, privatev1.Architecture_ARCHITECTURE_ARM64}
			if reverse {
				members[0], members[1] = members[1], members[0]
			}
			image.GetObject().GetSpec().SetArchitecture(members)
			_, err = images.Update().SetObject(image.GetObject()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			_, _, err = create(ctx, object("multi-request", cpu, "amd64", false), public)
			if cpu == "s390x" {
				assertMismatch(err)
				assertAbsent("multi-request")
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		}, Entry("amd64", "amd64", false), Entry("arm64", "arm64", false), Entry("s390x", "s390x", false),
			Entry("amd64 reversed", "amd64", true), Entry("arm64 reversed", "arm64", true), Entry("s390x reversed", "s390x", true))

		DescribeTable("compares effective catalog defaults and overrides", func(locked, override, compatible bool) {
			image := "arm64"
			if compatible {
				image = "amd64"
			}
			typePolicy := &privatev1.BareMetalInstanceTypeReferenceFieldPolicy{}
			imagePolicy := &privatev1.DiskImageReferenceFieldPolicy{}
			if locked {
				typePolicy.SetLocked(bareMetalInstanceTypeReference("type-amd64"))
				imagePolicy.SetLocked(privatev1.DiskImageReference_builder{Id: "image-" + image, Shared: true}.Build())
			} else {
				typePolicy.SetEditable(privatev1.EditableBareMetalInstanceTypeReferenceField_builder{DefaultValue: bareMetalInstanceTypeReference("type-amd64")}.Build())
				imagePolicy.SetEditable(privatev1.EditableDiskImageReferenceField_builder{DefaultValue: privatev1.DiskImageReference_builder{Id: "image-" + image, Shared: true}.Build()}.Build())
			}
			// Bypass catalog admission to retain coverage for legacy incompatible offerings.
			catalog, err := catalogServer.generic.dao.Create().SetObject(privatev1.BareMetalInstanceCatalogItem_builder{
				Metadata: privatev1.Metadata_builder{Name: "architecture-catalog", Tenant: testTenant}.Build(), Title: "Architecture catalog", Published: true,
				Template: privatev1.BareMetalInstanceTemplateReference_builder{Id: "architecture-template"}.Build(),
				Fields:   privatev1.BareMetalInstanceCatalogItemFields_builder{InstanceType: typePolicy, DiskImage: imagePolicy}.Build(),
			}.Build()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			obj := object("catalog-request", "amd64", image, false)
			obj.GetSpec().ClearTemplate()
			obj.GetSpec().SetCatalogItem(privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalog.GetObject().GetId()}.Build())
			obj.GetSpec().ClearInstanceType()
			obj.GetSpec().ClearDiskImage()
			if override {
				obj.GetSpec().SetInstanceType(bareMetalInstanceTypeReference("type-arm64"))
				obj.GetSpec().SetDiskImage(privatev1.DiskImageReference_builder{Id: "image-" + image, Shared: true}.Build())
				compatible = image == "arm64"
			}
			result, _, err := create(ctx, obj, public)
			if compatible {
				Expect(err).NotTo(HaveOccurred())
				expectedType := "type-amd64"
				if override {
					expectedType = "type-arm64"
				}
				Expect(result.GetSpec().GetInstanceType().GetId()).To(Equal(expectedType))
				Expect(result.GetSpec().GetDiskImage().GetId()).To(Equal("image-" + image))
				Expect(proto.Equal(result.GetSpec().GetTemplateParameters()["layout"], parameterDefault)).To(BeTrue())
			} else {
				assertMismatch(err)
				assertAbsent("catalog-request")
			}
		}, Entry("locked matching", true, false, true), Entry("locked mismatching", true, false, false),
			Entry("editable matching", false, false, true), Entry("editable mismatching", false, false, false),
			Entry("editable compatible override", false, true, false), Entry("editable incompatible override", false, true, true))

		DescribeTable("preserves lifecycle errors and deprecated warnings", func(lifecycle privatev1.DiskImageLifecycle, compatible, deleting bool) {
			imageName := "arm64"
			if compatible {
				imageName = "amd64"
			}
			image, err := images.Get().SetId("image-" + imageName).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			image.GetObject().GetSpec().SetLifecycle(lifecycle)
			if deleting {
				image.GetObject().GetMetadata().SetFinalizers([]string{"fixture.test/hold"})
			}
			_, err = images.Update().SetObject(image.GetObject()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			if deleting {
				_, err = images.Delete().SetId(image.GetObject().GetId()).Do(ctx)
				Expect(err).NotTo(HaveOccurred())
			}
			_, warnings, err := create(ctx, object("lifecycle-request", "amd64", imageName, false), public)
			switch {
			case deleting:
				Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("deleted"))
				Expect(err.Error()).NotTo(ContainSubstring("INCOMPATIBLE_DISK_IMAGE_ARCHITECTURE"))
				assertAbsent("lifecycle-request")
			case lifecycle == privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE:
				Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
				Expect(err.Error()).To(ContainSubstring("obsolete"))
				assertAbsent("lifecycle-request")
			case !compatible:
				assertMismatch(err)
				assertAbsent("lifecycle-request")
			default:
				Expect(err).NotTo(HaveOccurred())
				Expect(warnings).To(HaveLen(1))
				Expect(warnings[0]).To(ContainSubstring("deprecated"))
			}
		}, Entry("obsolete compatible", privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE, true, false),
			Entry("obsolete incompatible", privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE, false, false),
			Entry("deprecated compatible", privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED, true, false),
			Entry("deprecated incompatible", privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED, false, false),
			Entry("deleting compatible", privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE, true, true),
			Entry("deleting incompatible", privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE, false, true))
	}, Entry("private", false), Entry("public", true))

	DescribeTable("keeps compared catalog resources stable through the request transaction", func(byName bool) {
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil
		managerCtx := database.TxManagerIntoContext(context.Background(), tm)
		checkLock := func(table, id string, locked bool) {
			_, err := database.WithNewTx(managerCtx, func(otherCtx context.Context) (bool, error) {
				tx, err := database.TxFromContext(otherCtx)
				if err != nil {
					return false, err
				}
				_, err = tx.Exec(otherCtx, "select id from "+table+" where id=$1 for update nowait", id)
				return true, err
			})
			if locked {
				var pgErr *pgconn.PgError
				Expect(errors.As(err, &pgErr)).To(BeTrue())
				Expect(pgErr.Code).To(Equal("55P03"))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		}
		_, err := database.WithNewTx(managerCtx, func(requestCtx context.Context) (*privatev1.BareMetalInstancesCreateResponse, error) {
			response, err := privateServer.Create(requestCtx, privatev1.BareMetalInstancesCreateRequest_builder{Object: object("locked-request", "amd64", "amd64", byName)}.Build())
			if err != nil {
				return nil, err
			}
			checkLock("bare_metal_instance_types", "type-amd64", true)
			checkLock("disk_images", "image-amd64", true)
			return response, nil
		})
		Expect(err).NotTo(HaveOccurred())
		checkLock("bare_metal_instance_types", "type-amd64", false)
		checkLock("disk_images", "image-amd64", false)
	}, Entry("IDs", false), Entry("names", true))

	DescribeTable("hides unauthorized image architectures", func(byName, compatible bool) {
		createTenant("hidden-tenant")
		image, err := images.Get().SetId("image-arm64").Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		hidden := privatev1.DiskImage_builder{
			Id:       "hidden-image",
			Metadata: privatev1.Metadata_builder{Name: "hidden-image", Tenant: "hidden-tenant"}.Build(),
			Spec:     proto.Clone(image.GetObject().GetSpec()).(*privatev1.DiskImageSpec),
		}.Build()
		if compatible {
			hidden.GetSpec().SetArchitecture([]privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64})
		}
		_, err = images.Create().SetObject(hidden).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		visibility, err := auth.NewVisibility().AddVisibleTenants(testTenant, auth.SharedTenant).Build()
		Expect(err).NotTo(HaveOccurred())
		restricted := auth.NewMockTenancyLogic(ctrl)
		restricted.EXPECT().DetermineAssignableTenants(gomock.Any()).Return(auth.AllTenants, nil).AnyTimes()
		restricted.EXPECT().DetermineDefaultTenant(gomock.Any()).Return(testTenant, nil).AnyTimes()
		restricted.EXPECT().DetermineVisibility(gomock.Any()).Return(visibility, nil).AnyTimes()
		service, err := NewPrivateBareMetalInstancesServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(restricted).Build()
		Expect(err).NotTo(HaveOccurred())
		obj := object("hidden-request", "amd64", "arm64", byName)
		obj.GetSpec().GetDiskImage().SetShared(false)
		if byName {
			obj.GetSpec().GetDiskImage().SetName("hidden-image")
		} else {
			obj.GetSpec().GetDiskImage().SetId("hidden-image")
		}
		_, err = service.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{Object: obj}.Build())
		Expect(status.Code(err)).To(Equal(codes.NotFound))
		Expect(err.Error()).NotTo(ContainSubstring("INCOMPATIBLE_DISK_IMAGE_ARCHITECTURE"))
		Expect(err.Error()).NotTo(ContainSubstring("oci://"))
		assertAbsent("hidden-request")
	}, Entry("ID mismatch", false, false), Entry("ID matching", false, true), Entry("name mismatch", true, false), Entry("name matching", true, true))
	DescribeTable("serializes instance admission and architecture removal", func(createFirst bool) {
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil
		managerCtx, cancel := context.WithTimeout(database.TxManagerIntoContext(context.Background(), tm), 10*time.Second)
		defer cancel()
		first := func(callCtx context.Context) error {
			_, err := privateServer.Create(callCtx, privatev1.BareMetalInstancesCreateRequest_builder{Object: object("concurrent-request", "amd64", "amd64", false)}.Build())
			return err
		}
		imageService, err := NewPrivateDiskImagesServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		second := func(callCtx context.Context) error {
			_, err := imageService.Update(callCtx, privatev1.DiskImagesUpdateRequest_builder{
				Object: privatev1.DiskImage_builder{Id: "image-amd64", Spec: privatev1.DiskImageSpec_builder{
					Architecture: []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_ARM64},
				}.Build()}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.architecture"}},
			}.Build())
			return err
		}
		if !createFirst {
			first, second = second, first
		}
		secondPID := make(chan int, 1)
		result := make(chan error, 1)
		_, err = database.WithNewTx(managerCtx, func(firstCtx context.Context) (bool, error) {
			if err := first(firstCtx); err != nil {
				return false, err
			}
			firstTx, err := database.TxFromContext(firstCtx)
			Expect(err).NotTo(HaveOccurred())
			var firstPID int
			Expect(firstTx.QueryRow(firstCtx, "select pg_backend_pid()").Scan(&firstPID)).To(Succeed())
			go func() {
				defer GinkgoRecover()
				_, err := database.WithNewTx(managerCtx, func(secondCtx context.Context) (bool, error) {
					tx, err := database.TxFromContext(secondCtx)
					if err != nil {
						return false, err
					}
					var pid int
					if err := tx.QueryRow(secondCtx, "select pg_backend_pid()").Scan(&pid); err != nil {
						return false, err
					}
					secondPID <- pid
					return true, second(secondCtx)
				})
				result <- err
			}()
			var pid int
			Eventually(secondPID, 3*time.Second).Should(Receive(&pid))
			Eventually(func() bool {
				blocked, err := database.WithNewTx(managerCtx, func(probeCtx context.Context) (bool, error) {
					tx, err := database.TxFromContext(probeCtx)
					if err != nil {
						return false, err
					}
					var blocked bool
					err = tx.QueryRow(probeCtx, "select $1::int = any(pg_blocking_pids($2))", firstPID, pid).Scan(&blocked)
					return blocked, err
				})
				Expect(err).NotTo(HaveOccurred())
				return blocked
			}, 3*time.Second, 10*time.Millisecond).Should(BeTrue(), fmt.Sprintf("transaction %d must wait for %d", pid, firstPID))
			return true, nil
		})
		Expect(err).NotTo(HaveOccurred())
		var secondErr error
		Eventually(result, 3*time.Second).Should(Receive(&secondErr))
		if createFirst {
			Expect(status.Code(secondErr)).To(Equal(codes.FailedPrecondition))
		} else {
			Expect(status.Code(secondErr)).To(Equal(codes.InvalidArgument))
			Expect(secondErr.Error()).To(ContainSubstring("INCOMPATIBLE_DISK_IMAGE_ARCHITECTURE"))
		}
	}, Entry("instance admission commits first", true), Entry("image update commits first", false))
})
