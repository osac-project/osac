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
	"strings"

	"buf.build/go/protovalidate"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Test catalog item",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "test-template"}.Build(),
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
					Name:   id,
					Tenant: testTenant,
				}.Build(),
				Parameters: params,
			}.Build()

			_, err = templatesDao.Create().SetObject(template).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
		}

		createCatalogItemWithTemplate := func(templateID string) string {
			catalogResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Catalog with template params",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: templateID}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			return catalogResp.GetObject().GetId()
		}

		It("Creates object with minimal spec", func() {
			response, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetId()).ToNot(BeEmpty())
			Expect(response.GetObject().GetSpec().GetCatalogItem().GetId()).To(Equal(catalogItemID))
		})

		It("Creates object with valid SSH key", func() {
			response, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:  privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: "does-not-exist"}.Build(),
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
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "test-template"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: "my-named-catalog-item"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Unpublished item",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "test-template"}.Build(),
					Published: false,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			unpubID := unpubResp.GetObject().GetId()

			_, err = server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: unpubID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:  privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
						UserData:    new(exactData),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects PATCH that changes catalog_item", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-baremetal-instance",
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			secondResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Second catalog item",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "test-template"}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			secondID := secondResp.GetObject().GetId()

			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id: object.GetId(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: secondID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: "test-baremetal-instance",
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:  privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
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
						CatalogItem:  privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: "test-baremetal-instance",
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
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
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: "test-baremetal-instance",
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
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
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
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
					Name:   "image-default-template",
					Tenant: testTenant,
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Catalog with image default",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "image-default-template"}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catID := catResp.GetObject().GetId()

			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catID}.Build(),
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
					Name:   "image-override-template",
					Tenant: testTenant,
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Catalog with image default override",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "image-override-template"}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catID := catResp.GetObject().GetId()

			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catID}.Build(),
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
					Name:   "image-partial-merge-template",
					Tenant: testTenant,
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Catalog with partial merge",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "image-partial-merge-template"}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catID := catResp.GetObject().GetId()

			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: "test-baremetal-instance",
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
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
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: "test-baremetal-instance",
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: "test-baremetal-instance",
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			name := object.GetMetadata().GetName()

			_, err = server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id:       object.GetId(),
					Metadata: privatev1.Metadata_builder{Name: name}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Signals object", func() {
			createResponse, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: "test-baremetal-instance",
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catID}.Build(),
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
						CatalogItem:        privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: "test-baremetal-instance",
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Catalog with both constraints",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "combo-template"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        privatev1.BareMetalInstanceCatalogItemReference_builder{Id: comboCatID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Override + template params",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "override-combo-template"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Editable + template params",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "editable-combo-template"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "FD fail + valid TP",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "fd-fail-template"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Valid FD + TP fail",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "tp-fail-template"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:              privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: "test-baremetal-instance",
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:              privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemID}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:        privatev1.BareMetalInstanceCatalogItemReference_builder{Id: noTemplateCatID}.Build(),
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
					Tenant: testTenant,
					Name:   fmt.Sprintf("test-%s", uuid.NewString()[:8]),
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
					Tenant: testTenant,
					Name:   fmt.Sprintf("test-%s", uuid.NewString()[:8]),
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create a template WITHOUT host_type.
			_, err = templatesDao.Create().SetObject(privatev1.BareMetalInstanceTemplate_builder{
				Id:    "template-no-ht",
				Title: "Template without HostType",
				Metadata: privatev1.Metadata_builder{
					Tenant: testTenant,
					Name:   fmt.Sprintf("test-%s", uuid.NewString()[:8]),
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create catalog items referencing the templates.
			catResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Catalog with HT",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "template-with-ht"}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catIDWithHT = catResp.GetObject().GetId()

			catResp2, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Catalog no HT",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "template-no-ht"}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catIDNoHT = catResp2.GetObject().GetId()
		})

		It("Accepts single attachment without interface", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								Interface: strPtr("data-0"),
								Primary:   boolPtr(true),
							}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-2"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								Interface: strPtr("data-0"),
								Primary:   boolPtr(true),
							}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-2"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:  privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								Primary: boolPtr(true),
							}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-2"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{Subnet: privatev1.SubnetLocalReference_builder{Id: "s1"}.Build(), Interface: strPtr("data-0"), Primary: boolPtr(true)}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{Subnet: privatev1.SubnetLocalReference_builder{Id: "s2"}.Build(), Interface: strPtr("data-1")}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{Subnet: privatev1.SubnetLocalReference_builder{Id: "s3"}.Build(), Interface: strPtr("mgmt-0")}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{Subnet: privatev1.SubnetLocalReference_builder{Id: "s4"}.Build(), Interface: strPtr("extra")}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								Interface: strPtr("data-0"),
							}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-2"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								Interface: strPtr("data-0"),
								Primary:   boolPtr(true),
							}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-2"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDNoHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDNoHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								Interface: strPtr("nic-0"),
								Primary:   boolPtr(true),
							}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-2"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Accepts update that changes only security_groups", func() {
			createResp, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "baremetal-instance-1",
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:         privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								Interface:      strPtr("data-0"),
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "sg-1"}.Build()},
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
								Subnet:         privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								Interface:      strPtr("data-0"),
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "sg-1"}.Build(), privatev1.SecurityGroupLocalReference_builder{Id: "sg-2"}.Build()},
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
					Metadata: privatev1.Metadata_builder{
						Name: "baremetal-instance-1",
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: "baremetal-instance-1",
					}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
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
							privatev1.BareMetalNetworkAttachment_builder{Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(), Interface: strPtr("data-0")}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-2"}.Build(), Interface: strPtr("data-1")}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
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
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "different-subnet"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
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
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
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
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								Interface: strPtr("data-0"),
								Primary:   boolPtr(true),
							}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-2"}.Build(),
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
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								Interface: strPtr("data-0"),
							}.Build(),
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet:    privatev1.SubnetLocalReference_builder{Id: "subnet-2"}.Build(),
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
				CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: "some-catalog-item"}.Build(),
			}.Build()
			err := validator.Validate(spec)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Accepts single attachment without primary", func() {
			spec := privatev1.BareMetalInstanceSpec_builder{
				CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: "some-catalog-item"}.Build(),
				NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
					}.Build(),
				},
			}.Build()
			err := validator.Validate(spec)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Accepts single attachment with primary true", func() {
			spec := privatev1.BareMetalInstanceSpec_builder{
				CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: "some-catalog-item"}.Build(),
				NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet:  privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
						Primary: boolPtr(true),
					}.Build(),
				},
			}.Build()
			err := validator.Validate(spec)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Accepts multiple attachments with exactly one primary", func() {
			spec := privatev1.BareMetalInstanceSpec_builder{
				CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: "some-catalog-item"}.Build(),
				NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet:  privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
						Primary: boolPtr(true),
					}.Build(),
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-2"}.Build(),
					}.Build(),
				},
			}.Build()
			err := validator.Validate(spec)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects multiple attachments with no primary", func() {
			spec := privatev1.BareMetalInstanceSpec_builder{
				CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: "some-catalog-item"}.Build(),
				NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
					}.Build(),
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-2"}.Build(),
					}.Build(),
				},
			}.Build()
			err := validator.Validate(spec)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("primary"))
		})

		It("Rejects multiple attachments with more than one primary", func() {
			spec := privatev1.BareMetalInstanceSpec_builder{
				CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: "some-catalog-item"}.Build(),
				NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet:  privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
						Primary: boolPtr(true),
					}.Build(),
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet:  privatev1.SubnetLocalReference_builder{Id: "subnet-2"}.Build(),
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
				CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: "some-catalog-item"}.Build(),
				NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
					}.Build(),
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-2"}.Build(),
					}.Build(),
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-3"}.Build(),
					}.Build(),
				},
			}.Build()
			err := validator.Validate(spec)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("primary"))
		})

		It("Accepts multiple attachments with primary false on non-primary NICs", func() {
			spec := privatev1.BareMetalInstanceSpec_builder{
				CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: "some-catalog-item"}.Build(),
				NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet:  privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
						Primary: boolPtr(true),
					}.Build(),
					privatev1.BareMetalNetworkAttachment_builder{
						Subnet:  privatev1.SubnetLocalReference_builder{Id: "subnet-2"}.Build(),
						Primary: boolPtr(false),
					}.Build(),
				},
			}.Build()
			err := validator.Validate(spec)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Fabric manager validation for network_attachments", func() {
		var (
			server          *PrivateBareMetalInstancesServer
			catalogServer   *PrivateBareMetalInstanceCatalogItemsServer
			networkClassDao *dao.GenericDAO[*privatev1.NetworkClass]
			vnDao           *dao.GenericDAO[*privatev1.VirtualNetwork]
			subnetDao       *dao.GenericDAO[*privatev1.Subnet]
			catID           string
		)

		BeforeEach(func() {
			var err error

			server, err = NewPrivateBareMetalInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			catalogServer, err = NewPrivateBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			networkClassDao, err = dao.NewGenericDAO[*privatev1.NetworkClass]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			vnDao, err = dao.NewGenericDAO[*privatev1.VirtualNetwork]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			subnetDao, err = dao.NewGenericDAO[*privatev1.Subnet]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// A template with no host_type, so interface-against-HostType validation is skipped
			// and this Describe block can focus on the fabric_manager check.
			templatesDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceTemplate]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = templatesDao.Create().SetObject(privatev1.BareMetalInstanceTemplate_builder{
				Id:    "template-no-ht-fabric-manager-test",
				Title: "Template without HostType",
				Metadata: privatev1.Metadata_builder{
					Tenant: testTenant,
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			catResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Catalog for fabric manager validation",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "template-no-ht-fabric-manager-test"}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catID = catResp.GetObject().GetId()
		})

		// createSubnet creates a NetworkClass with the given managers, a VirtualNetwork referencing
		// it, and a Subnet referencing that VirtualNetwork, via the DAOs directly.
		createSubnet := func(fabricManager, k8sManager *string) string {
			ncResp, err := networkClassDao.Create().SetObject(
				privatev1.NetworkClass_builder{
					ImplementationStrategy: "test-strategy",
					FabricManager:          fabricManager,
					K8SManager:             k8sManager,
					Metadata: privatev1.Metadata_builder{
						Tenant: testTenant,
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			vnResp, err := vnDao.Create().SetObject(
				privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.VirtualNetworkSpec_builder{
						NetworkClass: privatev1.NetworkClassReference_builder{Id: ncResp.GetObject().GetId()}.Build(),
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			subnetResp, err := subnetDao.Create().SetObject(
				privatev1.Subnet_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.SubnetSpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vnResp.GetObject().GetId()}.Build(),
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			return subnetResp.GetObject().GetId()
		}

		It("rejects Create when the attachment's NetworkClass has no fabric_manager", func() {
			subnetID := createSubnet(nil, new("cudn_localnet"))
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catID}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: subnetID}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("fabric_manager"))
		})

		It("allows Create when the attachment's NetworkClass has a fabric_manager", func() {
			subnetID := createSubnet(new("netris"), nil)
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catID}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: subnetID}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("allows Create with no network_attachments regardless of fabric manager availability", func() {
			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catID}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Default network_attachments population", func() {
		var (
			server        *PrivateBareMetalInstancesServer
			catalogServer *PrivateBareMetalInstanceCatalogItemsServer
			catIDWithHT   string
			catIDNoHT     string
			defaultSubnet *privatev1.Subnet
			defaultSG     *privatev1.SecurityGroup
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

			// Create a HostType with fabric + management + lifecycle interfaces.
			hostTypesDao, err := dao.NewGenericDAO[*privatev1.HostType]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = hostTypesDao.Create().SetObject(privatev1.HostType_builder{
				Id:    "default-test-host-type",
				Title: "Default Test Host Type",
				Metadata: privatev1.Metadata_builder{
					Tenant: testTenant,
					Name:   fmt.Sprintf("test-%s", uuid.NewString()[:8]),
				}.Build(),
				Interfaces: []*privatev1.NetworkInterface{
					privatev1.NetworkInterface_builder{Name: "data-0", Role: "fabric"}.Build(),
					privatev1.NetworkInterface_builder{Name: "data-1", Role: "fabric"}.Build(),
					privatev1.NetworkInterface_builder{Name: "mgmt-0", Role: "management"}.Build(),
					privatev1.NetworkInterface_builder{Name: "bmc-0", Role: "lifecycle"}.Build(),
				},
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create templates.
			templatesDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceTemplate]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = templatesDao.Create().SetObject(privatev1.BareMetalInstanceTemplate_builder{
				Id:       "default-template-with-ht",
				Title:    "Template with HostType",
				HostType: "default-test-host-type",
				Metadata: privatev1.Metadata_builder{
					Tenant: testTenant,
					Name:   fmt.Sprintf("test-%s", uuid.NewString()[:8]),
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = templatesDao.Create().SetObject(privatev1.BareMetalInstanceTemplate_builder{
				Id:    "default-template-no-ht",
				Title: "Template without HostType",
				Metadata: privatev1.Metadata_builder{
					Tenant: testTenant,
					Name:   fmt.Sprintf("test-%s", uuid.NewString()[:8]),
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create catalog items.
			catResp, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Catalog with HT for defaults",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "default-template-with-ht"}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catIDWithHT = catResp.GetObject().GetId()

			catResp2, err := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Catalog no HT for defaults",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "default-template-no-ht"}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catIDNoHT = catResp2.GetObject().GetId()

			// Create a NetworkClass with fabric_manager for the fabric manager validation.
			ncDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			fabricMgr := "netris"
			ncResp, err := ncDao.Create().SetObject(privatev1.NetworkClass_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "default-nc",
					Tenant: "system",
				}.Build(),
				FabricManager:          &fabricMgr,
				ImplementationStrategy: "netris",
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			ncID := ncResp.GetObject().GetId()

			// Create a VirtualNetwork.
			vnDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			vnResp, err := vnDao.Create().SetObject(privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "default",
					Tenant: testTenant,
					Labels: map[string]string{
						"osac.openshift.io/default": "true",
					},
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: ncID}.Build(),
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			vnID := vnResp.GetObject().GetId()

			// Create default subnet with proper VN reference.
			subnetDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			ipv4Cidr := "10.0.1.0/24"
			subnetResp, err := subnetDao.Create().SetObject(privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "default-ipv4",
					Tenant: testTenant,
					Labels: map[string]string{
						"osac.openshift.io/default": "true",
					},
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					Ipv4Cidr:       &ipv4Cidr,
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vnID}.Build(),
				}.Build(),
				Status: privatev1.SubnetStatus_builder{
					State: privatev1.SubnetState_SUBNET_STATE_READY,
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			defaultSubnet = subnetResp.GetObject()

			sgDao, err := dao.NewGenericDAO[*privatev1.SecurityGroup]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			sgResp, err := sgDao.Create().SetObject(privatev1.SecurityGroup_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "default",
					Tenant: testTenant,
					Labels: map[string]string{
						"osac.openshift.io/default": "true",
					},
				}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vnID}.Build(),
				}.Build(),
				Status: privatev1.SecurityGroupStatus_builder{
					State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			defaultSG = sgResp.GetObject()
		})

		It("Populates default network_attachments when omitted", func() {
			response, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			attachments := response.GetObject().GetSpec().GetNetworkAttachments()
			Expect(attachments).To(HaveLen(1))
			Expect(attachments[0].GetSubnet().GetId()).To(Equal(defaultSubnet.GetId()))
			Expect(attachments[0].GetSecurityGroups()).To(HaveLen(1))
			Expect(attachments[0].GetSecurityGroups()[0].GetId()).To(Equal(defaultSG.GetId()))
			Expect(attachments[0].GetInterface()).To(Equal("data-0"))
		})

		It("Does not override explicitly provided network_attachments", func() {
			response, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
						NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
							privatev1.BareMetalNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "custom-subnet"}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			attachments := response.GetObject().GetSpec().GetNetworkAttachments()
			Expect(attachments).To(HaveLen(1))
			Expect(attachments[0].GetSubnet().GetId()).To(Equal("custom-subnet"))
		})

		It("Omits interface when template has no HostType", func() {
			response, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDNoHT}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			attachments := response.GetObject().GetSpec().GetNetworkAttachments()
			Expect(attachments).To(HaveLen(1))
			Expect(attachments[0].GetSubnet().GetId()).To(Equal(defaultSubnet.GetId()))
			Expect(attachments[0].GetInterface()).To(BeEmpty())
		})

		It("Skips defaults when no default subnet exists", func() {
			// Delete the default subnet — Create should succeed with no network_attachments.
			subnetDao, sdErr := dao.NewGenericDAO[*privatev1.Subnet]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(sdErr).ToNot(HaveOccurred())
			_, sdErr = subnetDao.Delete().SetId(defaultSubnet.GetId()).Do(ctx)
			Expect(sdErr).ToNot(HaveOccurred())

			response, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetNetworkAttachments()).To(BeEmpty())
		})

		It("Skips defaults when no default security group exists", func() {
			// Delete the default security group — Create should succeed with no network_attachments.
			sgDao, sgErr := dao.NewGenericDAO[*privatev1.SecurityGroup]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(sgErr).ToNot(HaveOccurred())
			_, sgErr = sgDao.Delete().SetId(defaultSG.GetId()).Do(ctx)
			Expect(sgErr).ToNot(HaveOccurred())

			response, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catIDWithHT}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetNetworkAttachments()).To(BeEmpty())
		})

		It("Fails when HostType has no fabric interface", func() {
			// Create a HostType with only management and lifecycle interfaces.
			htDao, htErr := dao.NewGenericDAO[*privatev1.HostType]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(htErr).ToNot(HaveOccurred())
			_, htErr = htDao.Create().SetObject(privatev1.HostType_builder{
				Id:    "no-fabric-host-type",
				Title: "No Fabric Host Type",
				Metadata: privatev1.Metadata_builder{
					Tenant: testTenant,
				}.Build(),
				Interfaces: []*privatev1.NetworkInterface{
					privatev1.NetworkInterface_builder{Name: "mgmt-0", Role: "management"}.Build(),
					privatev1.NetworkInterface_builder{Name: "bmc-0", Role: "lifecycle"}.Build(),
				},
			}.Build()).Do(ctx)
			Expect(htErr).ToNot(HaveOccurred())

			// Create template and catalog item referencing this host type.
			tmplDao, tmplErr := dao.NewGenericDAO[*privatev1.BareMetalInstanceTemplate]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(tmplErr).ToNot(HaveOccurred())
			_, tmplErr = tmplDao.Create().SetObject(privatev1.BareMetalInstanceTemplate_builder{
				Id:       "no-fabric-template",
				Title:    "No Fabric Template",
				HostType: "no-fabric-host-type",
				Metadata: privatev1.Metadata_builder{
					Tenant: testTenant,
				}.Build(),
			}.Build()).Do(ctx)
			Expect(tmplErr).ToNot(HaveOccurred())

			catResp, catErr := catalogServer.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:     "Catalog no fabric",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: "no-fabric-template"}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(catErr).ToNot(HaveOccurred())

			_, err := server.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catResp.GetObject().GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(status.Message()).To(ContainSubstring("fabric"))
		})
	})
})
