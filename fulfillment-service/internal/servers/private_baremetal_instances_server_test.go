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
	"strings"

	"buf.build/go/protovalidate"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

// A real ed25519 public key in OpenSSH authorized_keys format for testing.
const testSSHPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG8K1ZuSC7tmzxD5LJJXwkCfStVEjzXWYCFhJaLBxWAn test@example.com"

var _ = Describe("Private bare metal instances server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateBareMetalInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateBareMetalInstancesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewPrivateBareMetalInstancesServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateBareMetalInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var (
			server        *PrivateBareMetalInstancesServer
			catalogServer *PrivateBareMetalInstanceCatalogItemsServer
			catalogItemID string
		)

		BeforeEach(func() {
			var err error

			catalogServer, err = NewPrivateBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			server, err = NewPrivateBareMetalInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create a published catalog item for use in tests.
			catalogResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:     "Test catalog item",
					Template:  "test-template",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catalogItemID = catalogResp.GetObject().GetId()
		})

		createTemplate := func(id string, params []*privatev1.BareMetalInstanceTemplateParameterDefinition) {
			templatesDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceTemplate]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			template := privatev1.BareMetalInstanceTemplate_builder{
				Id:          id,
				Title:       "Test Template",
				Description: "Template with parameters",
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				Parameters: params,
			}.Build()

			_, err = templatesDao.Create().SetObject(template).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
		}

		createCatalogItemWithTemplate := func(templateID string) string {
			catalogResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:     "Catalog with template params",
					Template:  templateID,
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			return catalogResp.GetObject().GetId()
		}

		It("Creates object with minimal spec", func() {
			response, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetId()).ToNot(BeEmpty())
			Expect(response.GetObject().GetSpec().GetCatalogItem()).To(Equal(catalogItemID))
		})

		It("Creates object with valid SSH key", func() {
			response, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:  catalogItemID,
						SshPublicKey: new(testSSHPublicKey),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetSshPublicKey()).To(Equal(testSSHPublicKey))
		})

		It("Rejects nonexistent catalog item", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: "does-not-exist",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
			Expect(status.Message()).To(ContainSubstring("does-not-exist"))
		})

		It("Rejects catalog item referenced by name instead of ID", func() {
			namedResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-named-catalog-item",
					}.Build(),
					Title:     "Named catalog item",
					Template:  "test-template",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			namedID := namedResp.GetObject().GetId()
			DeferCleanup(func() {
				_, err := catalogServer.Delete(ctx, privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
					Id: namedID,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
			Expect(namedResp.GetObject().GetMetadata().GetName()).To(Equal("my-named-catalog-item"))

			_, err = server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: "my-named-catalog-item",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
			Expect(status.Message()).To(ContainSubstring("my-named-catalog-item"))
		})

		It("Rejects unpublished catalog item", func() {
			unpubResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:     "Unpublished item",
					Template:  "test-template",
					Published: false,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			unpubID := unpubResp.GetObject().GetId()

			_, err = server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: unpubID,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
			Expect(status.Message()).To(ContainSubstring("not published"))
		})

		// validateSpec runs before catalog item lookup, so invalid SSH key/user data
		// fail with InvalidArgument before the catalog item is checked.
		It("Rejects invalid SSH key at create time", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:  catalogItemID,
						SshPublicKey: new("not-a-valid-ssh-key"),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("spec.ssh_public_key"))
		})

		It("Rejects user data exceeding 64 KB at create time", func() {
			bigData := strings.Repeat("x", bareMetalInstanceUserDataMaxBytes+1)
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
						UserData:    new(bigData),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("spec.user_data"))
			Expect(status.Message()).To(ContainSubstring("exceeds the maximum"))
		})

		It("Accepts user data at exactly 64 KB", func() {
			exactData := strings.Repeat("x", bareMetalInstanceUserDataMaxBytes)
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
						UserData:    new(exactData),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects PATCH that changes catalog_item", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			secondResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:     "Second catalog item",
					Template:  "test-template",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			secondID := secondResp.GetObject().GetId()

			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id: object.GetId(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: secondID,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.catalog_item"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("catalog_item is immutable"))
		})

		It("Rejects PATCH that changes ssh_public_key", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:  catalogItemID,
						SshPublicKey: new(testSSHPublicKey),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			otherKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBe5EVW4cHjAFNa8jMJQqLGBJENvJRfH+Q2lOjFr93vd other@example.com"
			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id: object.GetId(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:  catalogItemID,
						SshPublicKey: new(otherKey),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.ssh_public_key"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("ssh_public_key is immutable"))
		})

		It("Rejects PATCH that changes user_data", func() {
			userData := "original user data"
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
						UserData:    new(userData),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			newData := "changed user data"
			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id: object.GetId(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
						UserData:    new(newData),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.user_data"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("user_data is immutable"))
		})

		It("Rejects PATCH that changes image", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
						Image: privatev1.BareMetalInstanceImage_builder{
							SourceType: "registry",
							SourceRef:  "quay.io/test:latest",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id: object.GetId(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
						Image: privatev1.BareMetalInstanceImage_builder{
							SourceType: "registry",
							SourceRef:  "quay.io/other:latest",
						}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.image"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("image is immutable"))
		})

		It("Creates object with image and persists it", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
						Image: privatev1.BareMetalInstanceImage_builder{
							SourceType: "registry",
							SourceRef:  "quay.io/test:latest",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			Expect(object.GetSpec().GetImage().GetSourceType()).To(Equal("registry"))
			Expect(object.GetSpec().GetImage().GetSourceRef()).To(Equal("quay.io/test:latest"))

			getResponse, err := server.Get(ctx, privatev1.BareMetalInstancesGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			fetched := getResponse.GetObject()
			Expect(fetched.GetSpec().GetImage().GetSourceType()).To(Equal("registry"))
			Expect(fetched.GetSpec().GetImage().GetSourceRef()).To(Equal("quay.io/test:latest"))
		})

		It("Rejects image with missing source_type", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
						Image: privatev1.BareMetalInstanceImage_builder{
							SourceRef: "quay.io/test:latest",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("image.source_type"))
		})

		It("Rejects image with missing source_ref", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
						Image: privatev1.BareMetalInstanceImage_builder{
							SourceType: "registry",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("image.source_ref"))
		})

		It("Applies image defaults from template spec_defaults", func() {
			templatesDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceTemplate]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			template := privatev1.BareMetalInstanceTemplate_builder{
				Id:          "image-default-template",
				Title:       "Template with image default",
				Description: "Has default image in spec_defaults",
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				SpecDefaults: privatev1.BareMetalInstanceTemplateSpecDefaults_builder{
					Image: privatev1.BareMetalInstanceImage_builder{
						SourceType: "registry",
						SourceRef:  "quay.io/default:latest",
					}.Build(),
				}.Build(),
			}.Build()

			_, err = templatesDao.Create().SetObject(template).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			catResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:     "Catalog with image default",
					Template:  "image-default-template",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catID := catResp.GetObject().GetId()

			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catID,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			spec := createResponse.GetObject().GetSpec()
			Expect(spec.GetImage().GetSourceType()).To(Equal("registry"))
			Expect(spec.GetImage().GetSourceRef()).To(Equal("quay.io/default:latest"))
		})

		It("User-provided image overrides template default", func() {
			templatesDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceTemplate]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			template := privatev1.BareMetalInstanceTemplate_builder{
				Id:          "image-override-template",
				Title:       "Template with image default",
				Description: "Has default image in spec_defaults",
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				SpecDefaults: privatev1.BareMetalInstanceTemplateSpecDefaults_builder{
					Image: privatev1.BareMetalInstanceImage_builder{
						SourceType: "registry",
						SourceRef:  "quay.io/default:latest",
					}.Build(),
				}.Build(),
			}.Build()

			_, err = templatesDao.Create().SetObject(template).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			catResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:     "Catalog with image default override",
					Template:  "image-override-template",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catID := catResp.GetObject().GetId()

			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catID,
						Image: privatev1.BareMetalInstanceImage_builder{
							SourceType: "registry",
							SourceRef:  "quay.io/user-chosen:v2",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			spec := createResponse.GetObject().GetSpec()
			Expect(spec.GetImage().GetSourceType()).To(Equal("registry"))
			Expect(spec.GetImage().GetSourceRef()).To(Equal("quay.io/user-chosen:v2"))
		})

		It("Merges user-provided source_type with template default source_ref", func() {
			templatesDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceTemplate]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			template := privatev1.BareMetalInstanceTemplate_builder{
				Id:          "image-partial-merge-template",
				Title:       "Template with image default",
				Description: "Has default image in spec_defaults",
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				SpecDefaults: privatev1.BareMetalInstanceTemplateSpecDefaults_builder{
					Image: privatev1.BareMetalInstanceImage_builder{
						SourceType: "registry",
						SourceRef:  "quay.io/default:latest",
					}.Build(),
				}.Build(),
			}.Build()

			_, err = templatesDao.Create().SetObject(template).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			catResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:     "Catalog with partial merge",
					Template:  "image-partial-merge-template",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catID := catResp.GetObject().GetId()

			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catID,
						Image: privatev1.BareMetalInstanceImage_builder{
							SourceType: "custom-source",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			spec := createResponse.GetObject().GetSpec()
			Expect(spec.GetImage().GetSourceType()).To(Equal("custom-source"))
			Expect(spec.GetImage().GetSourceRef()).To(Equal("quay.io/default:latest"))
		})

		It("Allows PATCH with same image value", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
						Image: privatev1.BareMetalInstanceImage_builder{
							SourceType: "registry",
							SourceRef:  "quay.io/test:latest",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id: object.GetId(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
						Image: privatev1.BareMetalInstanceImage_builder{
							SourceType: "registry",
							SourceRef:  "quay.io/test:latest",
						}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.image"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows PATCH that does not touch immutable fields", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
						RunStrategy: new(privatev1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id: object.GetId(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						RunStrategy: new(privatev1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.run_strategy"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows PATCH with no update mask (full replace) preserving same immutable fields", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id: object.GetId(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Signals object", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catalogItemID,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			_, err = server.Signal(ctx, privatev1.BareMetalInstancesSignalRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object with valid template_parameters", func() {
			diskDefault, err := anypb.New(wrapperspb.String("single"))
			Expect(err).ToNot(HaveOccurred())
			createTemplate("tp-template", []*privatev1.BareMetalInstanceTemplateParameterDefinition{
				{Name: "os_version", Required: true, Type: "type.googleapis.com/google.protobuf.StringValue"},
				{Name: "disk_layout", Required: false, Type: "type.googleapis.com/google.protobuf.StringValue", Default: diskDefault},
			})
			catID := createCatalogItemWithTemplate("tp-template")

			osParam, err := anypb.New(wrapperspb.String("rhel9.4"))
			Expect(err).ToNot(HaveOccurred())

			response, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        catID,
						TemplateParameters: map[string]*anypb.Any{"os_version": osParam},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetTemplateParameters()).To(HaveKey("os_version"))
			Expect(response.GetObject().GetSpec().GetTemplateParameters()).To(HaveKey("disk_layout"))
		})

		It("Applies default values for optional template parameters", func() {
			diskDefault, err := anypb.New(wrapperspb.String("single"))
			Expect(err).ToNot(HaveOccurred())
			createTemplate("defaults-template", []*privatev1.BareMetalInstanceTemplateParameterDefinition{
				{Name: "os_version", Required: true, Type: "type.googleapis.com/google.protobuf.StringValue"},
				{Name: "disk_layout", Required: false, Type: "type.googleapis.com/google.protobuf.StringValue", Default: diskDefault},
			})
			catID := createCatalogItemWithTemplate("defaults-template")

			osParam, err := anypb.New(wrapperspb.String("rhel9.4"))
			Expect(err).ToNot(HaveOccurred())

			response, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        catID,
						TemplateParameters: map[string]*anypb.Any{"os_version": osParam},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			params := response.GetObject().GetSpec().GetTemplateParameters()
			Expect(params).To(HaveKey("disk_layout"))
			var diskValue wrapperspb.StringValue
			Expect(params["disk_layout"].UnmarshalTo(&diskValue)).To(Succeed())
			Expect(diskValue.GetValue()).To(Equal("single"))
		})

		It("Rejects unknown template parameter", func() {
			createTemplate("unknown-param-template", []*privatev1.BareMetalInstanceTemplateParameterDefinition{
				{Name: "os_version", Required: true, Type: "type.googleapis.com/google.protobuf.StringValue"},
			})
			catID := createCatalogItemWithTemplate("unknown-param-template")

			osParam, err := anypb.New(wrapperspb.String("rhel9.4"))
			Expect(err).ToNot(HaveOccurred())
			unknownParam, err := anypb.New(wrapperspb.String("bogus"))
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catID,
						TemplateParameters: map[string]*anypb.Any{
							"os_version": osParam,
							"bogus":      unknownParam,
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("bogus"))
			Expect(status.Message()).To(ContainSubstring("doesn't exist"))
		})

		It("Rejects missing required template parameter", func() {
			createTemplate("required-param-template", []*privatev1.BareMetalInstanceTemplateParameterDefinition{
				{Name: "os_version", Required: true, Type: "type.googleapis.com/google.protobuf.StringValue"},
			})
			catID := createCatalogItemWithTemplate("required-param-template")

			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        catID,
						TemplateParameters: map[string]*anypb.Any{},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("os_version"))
			Expect(status.Message()).To(ContainSubstring("mandatory"))
		})

		It("Rejects wrong template parameter type", func() {
			createTemplate("wrong-type-template", []*privatev1.BareMetalInstanceTemplateParameterDefinition{
				{Name: "os_version", Required: true, Type: "type.googleapis.com/google.protobuf.StringValue"},
			})
			catID := createCatalogItemWithTemplate("wrong-type-template")

			wrongType, err := anypb.New(wrapperspb.Int32(42))
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        catID,
						TemplateParameters: map[string]*anypb.Any{"os_version": wrongType},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("type"))
		})

		It("Rejects PATCH that changes template_parameters", func() {
			createTemplate("immutable-tp-template", []*privatev1.BareMetalInstanceTemplateParameterDefinition{
				{Name: "os_version", Required: true, Type: "type.googleapis.com/google.protobuf.StringValue"},
			})
			catID := createCatalogItemWithTemplate("immutable-tp-template")

			osParam, err := anypb.New(wrapperspb.String("rhel9.4"))
			Expect(err).ToNot(HaveOccurred())

			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        catID,
						TemplateParameters: map[string]*anypb.Any{"os_version": osParam},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			newOsParam, err := anypb.New(wrapperspb.String("rhel10"))
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id: id,
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        catID,
						TemplateParameters: map[string]*anypb.Any{"os_version": newOsParam},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.template_parameters"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("template parameters are immutable"))
		})

		It("Allows PATCH that does not touch template_parameters", func() {
			createTemplate("mutable-fields-template", []*privatev1.BareMetalInstanceTemplateParameterDefinition{
				{Name: "os_version", Required: true, Type: "type.googleapis.com/google.protobuf.StringValue"},
			})
			catID := createCatalogItemWithTemplate("mutable-fields-template")

			osParam, err := anypb.New(wrapperspb.String("rhel9.4"))
			Expect(err).ToNot(HaveOccurred())

			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        catID,
						TemplateParameters: map[string]*anypb.Any{"os_version": osParam},
						RunStrategy:        new(privatev1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id: id,
					Spec: privatev1.BareMetalInstanceSpec_builder{
						RunStrategy: new(privatev1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.run_strategy"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object with field_definitions and template_parameters", func() {
			createTemplate("combo-template", []*privatev1.BareMetalInstanceTemplateParameterDefinition{
				{Name: "os_version", Required: true, Type: "type.googleapis.com/google.protobuf.StringValue"},
			})

			comboCatResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:     "Catalog with both constraints",
					Template:  "combo-template",
					Published: true,
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:     "ssh_public_key",
							Editable: false,
							Default:  structpb.NewStringValue(testSSHPublicKey),
						}.Build(),
						privatev1.FieldDefinition_builder{
							Path:     "template_parameters.os_version",
							Editable: true,
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			comboCatID := comboCatResp.GetObject().GetId()

			osParam, err := anypb.New(wrapperspb.String("rhel9.4"))
			Expect(err).ToNot(HaveOccurred())

			response, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        comboCatID,
						TemplateParameters: map[string]*anypb.Any{"os_version": osParam},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetSshPublicKey()).To(Equal(testSSHPublicKey))
			Expect(response.GetObject().GetSpec().GetTemplateParameters()).To(HaveKey("os_version"))
		})

		It("Rejects user value for non-editable field_definition alongside template_parameters", func() {
			createTemplate("override-combo-template", []*privatev1.BareMetalInstanceTemplateParameterDefinition{
				{Name: "os_version", Required: true, Type: "type.googleapis.com/google.protobuf.StringValue"},
			})

			catResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:     "Override + template params",
					Template:  "override-combo-template",
					Published: true,
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:     "ssh_public_key",
							Editable: false,
							Default:  structpb.NewStringValue(testSSHPublicKey),
						}.Build(),
						privatev1.FieldDefinition_builder{
							Path:     "template_parameters.os_version",
							Editable: true,
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catID := catResp.GetObject().GetId()

			osParam, err := anypb.New(wrapperspb.String("rhel9.4"))
			Expect(err).ToNot(HaveOccurred())

			userKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIUserProvidedKeyThatShouldBeOverridden user@test"
			_, err = server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        catID,
						SshPublicKey:       &userKey,
						TemplateParameters: map[string]*anypb.Any{"os_version": osParam},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring("not editable"))
		})

		It("Accepts editable field_definition alongside template_parameters", func() {
			createTemplate("editable-combo-template", []*privatev1.BareMetalInstanceTemplateParameterDefinition{
				{Name: "os_version", Required: true, Type: "type.googleapis.com/google.protobuf.StringValue"},
			})

			catResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:     "Editable + template params",
					Template:  "editable-combo-template",
					Published: true,
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:     "ssh_public_key",
							Editable: true,
						}.Build(),
						privatev1.FieldDefinition_builder{
							Path:     "template_parameters.os_version",
							Editable: true,
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catID := catResp.GetObject().GetId()

			osParam, err := anypb.New(wrapperspb.String("rhel9.4"))
			Expect(err).ToNot(HaveOccurred())

			response, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        catID,
						SshPublicKey:       new(testSSHPublicKey),
						TemplateParameters: map[string]*anypb.Any{"os_version": osParam},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetSshPublicKey()).To(Equal(testSSHPublicKey))
			Expect(response.GetObject().GetSpec().GetTemplateParameters()).To(HaveKey("os_version"))
		})

		It("Rejects missing required field_definition even with valid template_parameters", func() {
			createTemplate("fd-fail-template", []*privatev1.BareMetalInstanceTemplateParameterDefinition{
				{Name: "os_version", Required: true, Type: "type.googleapis.com/google.protobuf.StringValue"},
			})

			catResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:     "FD fail + valid TP",
					Template:  "fd-fail-template",
					Published: true,
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:     "ssh_public_key",
							Editable: true,
						}.Build(),
						privatev1.FieldDefinition_builder{
							Path:     "template_parameters.os_version",
							Editable: true,
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catID := catResp.GetObject().GetId()

			osParam, err := anypb.New(wrapperspb.String("rhel9.4"))
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        catID,
						TemplateParameters: map[string]*anypb.Any{"os_version": osParam},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("ssh_public_key"))
		})

		It("Rejects missing required template_parameter even with valid field_definitions", func() {
			createTemplate("tp-fail-template", []*privatev1.BareMetalInstanceTemplateParameterDefinition{
				{Name: "os_version", Required: true, Type: "type.googleapis.com/google.protobuf.StringValue"},
			})

			catResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:     "Valid FD + TP fail",
					Template:  "tp-fail-template",
					Published: true,
					FieldDefinitions: []*privatev1.FieldDefinition{
						privatev1.FieldDefinition_builder{
							Path:     "ssh_public_key",
							Editable: false,
							Default:  structpb.NewStringValue(testSSHPublicKey),
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catID := catResp.GetObject().GetId()

			_, err = server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        catID,
						TemplateParameters: map[string]*anypb.Any{},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("os_version"))
		})

		It("Creates object with auto_external_ip_attachment and persists it", func() {
			response, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:              catalogItemID,
						AutoExternalIpAttachment: true,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetAutoExternalIpAttachment()).To(BeTrue())

			getResponse, err := server.Get(ctx, privatev1.BareMetalInstancesGetRequest_builder{
				Id: response.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetSpec().GetAutoExternalIpAttachment()).To(BeTrue())
		})

		It("Rejects PATCH that changes auto_external_ip_attachment", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:              catalogItemID,
						AutoExternalIpAttachment: true,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id: object.GetId(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						AutoExternalIpAttachment: false,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.auto_external_ip_attachment"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("auto_external_ip_attachment is immutable"))
		})

		It("Rejects template_parameters when catalog item has no template", func() {
			noTemplateResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:     "No template item",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			noTemplateCatID := noTemplateResp.GetObject().GetId()

			osParam, err := anypb.New(wrapperspb.String("rhel9.4"))
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        noTemplateCatID,
						TemplateParameters: map[string]*anypb.Any{"os_version": osParam},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("no template"))
		})
	})

	Describe("Network attachment server validation", func() {
		var (
			server        *PrivateBareMetalInstancesServer
			catalogServer *PrivateBareMetalInstanceCatalogItemsServer
			catIDWithHT   string
			catIDNoHT     string
		)

		boolPtr := func(v bool) *bool { return &v }
		strPtr := func(v string) *string { return &v }

		BeforeEach(func() {
			var err error

			catalogServer, err = NewPrivateBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			server, err = NewPrivateBareMetalInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create a HostType with known interfaces.
			hostTypesDao, err := dao.NewGenericDAO[*privatev1.HostType]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = hostTypesDao.Create().SetObject(privatev1.HostType_builder{
				Id:    "test-host-type",
				Title: "Test Host Type",
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				Interfaces: []*privatev1.NetworkInterface{
					privatev1.NetworkInterface_builder{Name: "data-0", Role: "fabric"}.Build(),
					privatev1.NetworkInterface_builder{Name: "data-1", Role: "fabric"}.Build(),
					privatev1.NetworkInterface_builder{Name: "mgmt-0", Role: "management"}.Build(),
					privatev1.NetworkInterface_builder{Name: "bmc-0", Role: "lifecycle"}.Build(),
				},
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create a template WITH host_type.
			templatesDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceTemplate]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = templatesDao.Create().SetObject(privatev1.BareMetalInstanceTemplate_builder{
				Id:       "template-with-ht",
				Title:    "Template with HostType",
				HostType: "test-host-type",
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create a template WITHOUT host_type.
			_, err = templatesDao.Create().SetObject(privatev1.BareMetalInstanceTemplate_builder{
				Id:    "template-no-ht",
				Title: "Template without HostType",
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create catalog items referencing the templates.
			catResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:     "Catalog with HT",
					Template:  "template-with-ht",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catIDWithHT = catResp.GetObject().GetId()

			catResp2, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Title:     "Catalog no HT",
					Template:  "template-no-ht",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catIDNoHT = catResp2.GetObject().GetId()
		})

		It("Accepts single attachment without interface", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDWithHT,
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet: "subnet-1",
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Accepts single attachment with valid interface", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDWithHT,
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-1",
								Interface: strPtr("data-0"),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Accepts multiple attachments with distinct valid interfaces and one primary", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDWithHT,
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-1",
								Interface: strPtr("data-0"),
								Primary:   boolPtr(true),
							}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-2",
								Interface: strPtr("data-1"),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects interface not in HostType interfaces list", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDWithHT,
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-1",
								Interface: strPtr("nonexistent-port"),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("nonexistent-port"))
			Expect(status.Message()).To(ContainSubstring("not found in host type"))
		})

		It("Rejects duplicate interface across attachments", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDWithHT,
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-1",
								Interface: strPtr("data-0"),
								Primary:   boolPtr(true),
							}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-2",
								Interface: strPtr("data-0"),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("duplicate interface"))
			Expect(status.Message()).To(ContainSubstring("data-0"))
		})

		It("Rejects interface with lifecycle role", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDWithHT,
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-1",
								Interface: strPtr("bmc-0"),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("lifecycle"))
			Expect(status.Message()).To(ContainSubstring("bmc-0"))
		})

		It("Rejects multiple attachments without explicit interface", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDWithHT,
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:  "subnet-1",
								Primary: boolPtr(true),
							}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet: "subnet-2",
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("interface is required"))
		})

		It("Rejects attachment count exceeding available interfaces", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDWithHT,
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{Subnet: "s1", Interface: strPtr("data-0"), Primary: boolPtr(true)}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{Subnet: "s2", Interface: strPtr("data-1")}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{Subnet: "s3", Interface: strPtr("mgmt-0")}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{Subnet: "s4", Interface: strPtr("extra")}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("exceeds available interfaces"))
		})

		It("Rejects multiple attachments with no primary", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDWithHT,
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-1",
								Interface: strPtr("data-0"),
							}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-2",
								Interface: strPtr("data-1"),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("primary"))
		})

		It("Rejects multiple attachments with more than one primary", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDWithHT,
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-1",
								Interface: strPtr("data-0"),
								Primary:   boolPtr(true),
							}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-2",
								Interface: strPtr("data-1"),
								Primary:   boolPtr(true),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("primary"))
		})

		It("Skips interface-against-HostType validation when template has no host_type", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDNoHT,
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-1",
								Interface: strPtr("any-interface-name"),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Still validates structural rules when template has no host_type", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDNoHT,
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-1",
								Interface: strPtr("nic-0"),
								Primary:   boolPtr(true),
							}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-2",
								Interface: strPtr("nic-0"),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("duplicate interface"))
		})

		It("Accepts no network attachments", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDWithHT,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Accepts update that changes only security_groups", func() {
			createResp, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDWithHT,
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:         "subnet-1",
								Interface:      strPtr("data-0"),
								SecurityGroups: []string{"sg-1"},
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResp.GetObject().GetId()

			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id: id,
					Spec: privatev1.BareMetalInstanceSpec_builder{
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:         "subnet-1",
								Interface:      strPtr("data-0"),
								SecurityGroups: []string{"sg-1", "sg-2"},
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.network_attachments"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Accepts update that does not touch network_attachments", func() {
			createResp, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDWithHT,
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-1",
								Interface: strPtr("data-0"),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResp.GetObject().GetId()

			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id: id,
					Spec: privatev1.BareMetalInstanceSpec_builder{
						RunStrategy: privatev1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED.Enum(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.run_strategy"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects update that changes array size", func() {
			createResp, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDWithHT,
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-1",
								Interface: strPtr("data-0"),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResp.GetObject().GetId()

			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id: id,
					Spec: privatev1.BareMetalInstanceSpec_builder{
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{Subnet: "subnet-1", Interface: strPtr("data-0")}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{Subnet: "subnet-2", Interface: strPtr("data-1")}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.network_attachments"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("cannot change number of network attachments"))
		})

		It("Rejects update that changes subnet", func() {
			createResp, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDWithHT,
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-1",
								Interface: strPtr("data-0"),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResp.GetObject().GetId()

			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id: id,
					Spec: privatev1.BareMetalInstanceSpec_builder{
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "different-subnet",
								Interface: strPtr("data-0"),
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.network_attachments"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("subnet is immutable"))
		})

		It("Rejects update that changes interface", func() {
			createResp, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDWithHT,
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-1",
								Interface: strPtr("data-0"),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResp.GetObject().GetId()

			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id: id,
					Spec: privatev1.BareMetalInstanceSpec_builder{
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-1",
								Interface: strPtr("data-1"),
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.network_attachments"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("interface is immutable"))
		})

		It("Rejects update that changes primary", func() {
			createResp, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: catIDWithHT,
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-1",
								Interface: strPtr("data-0"),
								Primary:   boolPtr(true),
							}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-2",
								Interface: strPtr("data-1"),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResp.GetObject().GetId()

			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id: id,
					Spec: privatev1.BareMetalInstanceSpec_builder{
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-1",
								Interface: strPtr("data-0"),
							}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    "subnet-2",
								Interface: strPtr("data-1"),
								Primary:   boolPtr(true),
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.network_attachments"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("primary is immutable"))
		})
	})

	Describe("Network attachment primary validation", func() {
		var validator protovalidate.Validator

		BeforeEach(func() {
			var err error
			validator, err = protovalidate.New()
			Expect(err).ToNot(HaveOccurred())
		})

		boolPtr := func(v bool) *bool { return &v }

		It("Accepts no network attachments", func() {
			spec := privatev1.BareMetalInstanceSpec_builder{
				CatalogItem: "some-catalog-item",
			}.Build()
			err := validator.Validate(spec)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Accepts single attachment without primary", func() {
			spec := privatev1.BareMetalInstanceSpec_builder{
				CatalogItem: "some-catalog-item",
				NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet: "subnet-1",
					}.Build(),
				},
			}.Build()
			err := validator.Validate(spec)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Accepts single attachment with primary true", func() {
			spec := privatev1.BareMetalInstanceSpec_builder{
				CatalogItem: "some-catalog-item",
				NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet:  "subnet-1",
						Primary: boolPtr(true),
					}.Build(),
				},
			}.Build()
			err := validator.Validate(spec)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Accepts multiple attachments with exactly one primary", func() {
			spec := privatev1.BareMetalInstanceSpec_builder{
				CatalogItem: "some-catalog-item",
				NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet:  "subnet-1",
						Primary: boolPtr(true),
					}.Build(),
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet: "subnet-2",
					}.Build(),
				},
			}.Build()
			err := validator.Validate(spec)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects multiple attachments with no primary", func() {
			spec := privatev1.BareMetalInstanceSpec_builder{
				CatalogItem: "some-catalog-item",
				NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet: "subnet-1",
					}.Build(),
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet: "subnet-2",
					}.Build(),
				},
			}.Build()
			err := validator.Validate(spec)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("primary"))
		})

		It("Rejects multiple attachments with more than one primary", func() {
			spec := privatev1.BareMetalInstanceSpec_builder{
				CatalogItem: "some-catalog-item",
				NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet:  "subnet-1",
						Primary: boolPtr(true),
					}.Build(),
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet:  "subnet-2",
						Primary: boolPtr(true),
					}.Build(),
				},
			}.Build()
			err := validator.Validate(spec)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("primary"))
		})

		It("Rejects three attachments with zero primary", func() {
			spec := privatev1.BareMetalInstanceSpec_builder{
				CatalogItem: "some-catalog-item",
				NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet: "subnet-1",
					}.Build(),
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet: "subnet-2",
					}.Build(),
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet: "subnet-3",
					}.Build(),
				},
			}.Build()
			err := validator.Validate(spec)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("primary"))
		})

		It("Accepts multiple attachments with primary false on non-primary NICs", func() {
			spec := privatev1.BareMetalInstanceSpec_builder{
				CatalogItem: "some-catalog-item",
				NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet:  "subnet-1",
						Primary: boolPtr(true),
					}.Build(),
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet:  "subnet-2",
						Primary: boolPtr(false),
					}.Build(),
				},
			}.Build()
			err := validator.Validate(spec)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
