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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Private cluster catalog items server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateClusterCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateClusterCatalogItemsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewPrivateClusterCatalogItemsServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateClusterCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})

	})

	Describe("Behaviour", func() {
		var server *PrivateClusterCatalogItemsServer

		BeforeEach(func() {
			var err error

			// Create the server:
			server, err = NewPrivateClusterCatalogItemsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(seedClusterCatalogItemTemplate(ctx, testTenant, "", "my-template-id")).To(Succeed())
			Expect(seedClusterCatalogItemTemplate(ctx, auth.SharedTenant, "", "my-shared-template-id")).To(Succeed())
			Expect(err).ToNot(HaveOccurred())
		})

		DescribeTable("checks inherited node sets against concrete network policies", func(hasFabric bool) {
			host := privatev1.HostType_builder{
				Id:       "inherited-host",
				Metadata: privatev1.Metadata_builder{Name: "inherited-host", Tenant: testTenant}.Build(),
			}.Build()
			if hasFabric {
				host.SetInterfaces([]*privatev1.NetworkInterface{
					privatev1.NetworkInterface_builder{Name: "data-0", Role: "fabric"}.Build(),
				})
			}
			_, err := server.hostTypesDao.Create().SetObject(host).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			template := privatev1.ClusterTemplate_builder{
				Metadata: privatev1.Metadata_builder{Tenant: testTenant}.Build(),
				NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
					"workers": privatev1.ClusterTemplateNodeSet_builder{
						HostType: privatev1.HostTypeReference_builder{Id: host.GetId()}.Build(), Size: 1,
					}.Build(),
				},
			}.Build()
			item := privatev1.ClusterCatalogItem_builder{
				Metadata: privatev1.Metadata_builder{Tenant: testTenant}.Build(),
				Fields: privatev1.ClusterCatalogItemFields_builder{
					NetworkAttachment: privatev1.ClusterNetworkAttachmentFieldPolicy_builder{
						Locked: privatev1.ClusterNetworkAttachment_builder{
							Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet"}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()
			err = validateClusterCatalogItemNodeSetPolicy(ctx, item, template, server.hostTypesDao)
			if hasFabric {
				Expect(err).ToNot(HaveOccurred())
			} else {
				Expect(err).To(MatchError(ContainSubstring("has no interface with role 'fabric'")))
			}
		}, Entry("accepts a fabric interface", true), Entry("rejects a missing fabric interface", false))

		It("Creates object", func() {
			response, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   fmt.Sprintf("test-%s", uuid.New()[24:32]),
						Tenant: testTenant,
					}.Build(),
					Title:       "My cluster catalog item",
					Description: "My description.",
					Template:    privatev1.ClusterTemplateReference_builder{Id: "my-shared-template-id"}.Build(),
					Published:   true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetTitle()).To(Equal("My cluster catalog item"))
			Expect(object.GetTemplate().GetId()).To(Equal("my-shared-template-id"))
			Expect(object.GetTemplate().GetShared()).To(BeTrue())
			Expect(object.GetPublished()).To(BeTrue())
			Expect(object.GetMetadata().GetTenant()).To(Equal(testTenant))
		})

		It("List objects", func() {
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
					Object: privatev1.ClusterCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Title:       fmt.Sprintf("Catalog item %d", i),
						Description: fmt.Sprintf("Description %d.", i),
						Template:    privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := server.List(ctx, privatev1.ClusterCatalogItemsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
					Object: privatev1.ClusterCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Title:    fmt.Sprintf("Catalog item %d", i),
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := server.List(ctx, privatev1.ClusterCatalogItemsListRequest_builder{
				Limit: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 1))
		})

		It("List objects with filter", func() {
			const count = 10
			var objects []*privatev1.ClusterCatalogItem
			for i := range count {
				createResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
					Object: privatev1.ClusterCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Title:    fmt.Sprintf("Catalog item %d", i),
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				objects = append(objects, createResponse.GetObject())
			}
			DeferCleanup(func() {
				for _, object := range objects {
					_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
						Id: object.GetId(),
					}.Build())
					Expect(err).ToNot(HaveOccurred())
				}
			})

			for _, object := range objects {
				getResponse, err := server.List(ctx, privatev1.ClusterCatalogItemsListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == '%s'", object.GetId())),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetSize()).To(BeNumerically("==", 1))
				Expect(getResponse.GetItems()[0].GetId()).To(Equal(object.GetId()))
			}
		})

		It("Get object", func() {
			createResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Title:       "My catalog item",
					Description: "My description.",
					Template:    privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published:   true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			getResponse, err := server.Get(ctx, privatev1.ClusterCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("Update object", func() {
			createResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster-catalog-update",
					}.Build(),
					Title:       "Original title",
					Description: "Original description.",
					Template:    privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
			name := object.GetMetadata().GetName()

			updateResponse, err := server.Update(ctx, privatev1.ClusterCatalogItemsUpdateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Id:          object.GetId(),
					Metadata:    privatev1.Metadata_builder{Name: name}.Build(),
					Title:       "Updated title",
					Description: "Updated description.",
					Template:    privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetTitle()).To(Equal("Updated title"))
			Expect(updateResponse.GetObject().GetDescription()).To(Equal("Updated description."))

			getResponse, err := server.Get(ctx, privatev1.ClusterCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetTitle()).To(Equal("Updated title"))
			Expect(getResponse.GetObject().GetDescription()).To(Equal("Updated description."))
		})

		It("rejects changing the template on update", func() {
			createResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster-catalog-template-immutable"}.Build(),
					Title:    "Catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Update(ctx, privatev1.ClusterCatalogItemsUpdateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Id:       createResponse.GetObject().GetId(),
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-shared-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))

			getResponse, err := server.Get(ctx, privatev1.ClusterCatalogItemsGetRequest_builder{Id: createResponse.GetObject().GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetTemplate().GetId()).To(Equal("my-template-id"))
		})

		It("Update published using field mask", func() {
			createResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster-catalog-published",
					}.Build(),
					Title:     "My catalog item",
					Template:  privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published: false,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
			name := object.GetMetadata().GetName()
			updateResponse, err := server.Update(ctx, privatev1.ClusterCatalogItemsUpdateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Id:        object.GetId(),
					Metadata:  privatev1.Metadata_builder{Name: name}.Build(),
					Published: true,
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"published"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetPublished()).To(BeTrue())

			getResponse, err := server.Get(ctx, privatev1.ClusterCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetPublished()).To(BeTrue())
		})

		It("Creates object with typed fields and round-trips them", func() {
			response, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Title:    "Catalog item with fields",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Fields: privatev1.ClusterCatalogItemFields_builder{
						Network: privatev1.ClusterNetworkFieldPolicies_builder{
							PodCidr: privatev1.StringFieldPolicy_builder{Editable: privatev1.EditableStringField_builder{}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			getResponse, err := server.Get(ctx, privatev1.ClusterCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			fetched := getResponse.GetObject()
			Expect(fetched.GetFields().GetNetwork().GetPodCidr().GetEditable()).ToNot(BeNil())
		})

		DescribeTable("Rejects a non-pull Secret in a pull-secret policy", func(locked bool) {
			secretsDao, err := dao.NewGenericDAO[*privatev1.Secret]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = secretsDao.Create().SetObject(privatev1.Secret_builder{
				Id:   "non-pull-secret",
				Type: privatev1.SecretType_SECRET_TYPE_OPAQUE,
				Metadata: privatev1.Metadata_builder{
					Name:   "non-pull-secret",
					Tenant: testTenant,
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			ref := privatev1.SecretLocalReference_builder{Id: "non-pull-secret"}.Build()
			var policy *privatev1.SecretReferenceFieldPolicy
			if locked {
				policy = privatev1.SecretReferenceFieldPolicy_builder{Locked: ref}.Build()
			} else {
				policy = privatev1.SecretReferenceFieldPolicy_builder{
					Editable: privatev1.EditableSecretReferenceField_builder{DefaultValue: ref}.Build(),
				}.Build()
			}

			_, err = server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.New()[24:32])}.Build(),
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Fields: privatev1.ClusterCatalogItemFields_builder{
						PullSecretSecret: policy,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("fields.pull_secret_secret"))
			Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("SECRET_TYPE_OPAQUE"))
		},
			Entry("locked value", true),
			Entry("editable default", false),
		)

		It("Delete object", func() {
			createResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name:       "test-cluster-catalog-delete",
						Finalizers: []string{"a"},
					}.Build(),
					Title:    "My catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			_, err = server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := server.Get(ctx, privatev1.ClusterCatalogItemsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		It("Allows delete when referenced by a cluster", func() {
			createResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "Referenced catalog item",
					Template:  privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			catalogItem := createResponse.GetObject()

			clustersDao, err := dao.NewGenericDAO[*privatev1.Cluster]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = clustersDao.Create().SetObject(
				privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "ref-cluster",
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: catalogItem.GetId()}.Build(),
						Template:    privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
				Id: catalogItem.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects duplicate name within same tenant", func() {
			_, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "dev-sandbox",
					}.Build(),
					Title:    "First catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "dev-sandbox",
					}.Build(),
					Title:    "Second catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.AlreadyExists))
			Expect(status.Message()).To(ContainSubstring("dev-sandbox"))
		})

		It("Allows same name across different tenants", func() {
			_, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "dev-sandbox",
						Tenant: testTenant,
					}.Build(),
					Title:    "Catalog item for test tenant",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "dev-sandbox",
						Tenant: "shared",
					}.Build(),
					Title:    "Catalog item for shared tenant",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-shared-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects update to duplicate name within same tenant", func() {
			_, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "first-item",
					}.Build(),
					Title:    "First catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			secondResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "second-item",
					}.Build(),
					Title:    "Second catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Update(ctx, privatev1.ClusterCatalogItemsUpdateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Id: secondResponse.GetObject().GetId(),
					Metadata: privatev1.Metadata_builder{
						Name: "first-item",
					}.Build(),
					Title:    "Second catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("immutable"))
		})

		DescribeTable("validates SSH public key policy on Create", func(policy *privatev1.StringFieldPolicy, invalid bool) {
			response, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Title:    "SSH key policy",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Fields: privatev1.ClusterCatalogItemFields_builder{
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
				_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
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
			createResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster-catalog-nodefault",
					}.Build(),
					Title:    "Valid catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()
			name := createResponse.GetObject().GetMetadata().GetName()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
					Id: id,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			_, err = server.Update(ctx, privatev1.ClusterCatalogItemsUpdateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Id:       id,
					Metadata: privatev1.Metadata_builder{Name: name}.Build(),
					Fields: privatev1.ClusterCatalogItemFields_builder{
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

		It("Accepts update with valid typed policies", func() {
			createResponse, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster-catalog-validfd",
					}.Build(),
					Title:    "Valid catalog item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()
			DeferCleanup(func() {
				_, err := server.Delete(ctx, privatev1.ClusterCatalogItemsDeleteRequest_builder{
					Id: id,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
			name := createResponse.GetObject().GetMetadata().GetName()

			updateResponse, err := server.Update(ctx, privatev1.ClusterCatalogItemsUpdateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Id:       id,
					Metadata: privatev1.Metadata_builder{Name: name}.Build(),
					Fields: privatev1.ClusterCatalogItemFields_builder{
						SshPublicKey: privatev1.StringFieldPolicy_builder{Locked: proto.String(testSSHPublicKey)}.Build(),
						Network: privatev1.ClusterNetworkFieldPolicies_builder{
							PodCidr: privatev1.StringFieldPolicy_builder{Editable: privatev1.EditableStringField_builder{}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"fields"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetFields()).ToNot(BeNil())
		})

		Describe("ClusterVersion validation on fields", func() {
			var validatedServer *PrivateClusterCatalogItemsServer

			BeforeEach(func() {
				var err error
				validatedServer, err = NewPrivateClusterCatalogItemsServer().
					SetLogger(logger).
					SetAttributionLogic(attribution).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
			})

			It("Rejects create with non-existent version default in fields", func() {
				_, err := validatedServer.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
					Object: privatev1.ClusterCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Title:    "Bad version catalog item",
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						Fields: privatev1.ClusterCatalogItemFields_builder{
							Version: privatev1.ClusterVersionReferenceFieldPolicy_builder{Editable: privatev1.EditableClusterVersionReferenceField_builder{DefaultValue: privatev1.ClusterVersionReference_builder{Name: "does-not-exist"}.Build()}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("cluster version 'does-not-exist' in fields.version not found"))
			})

			It("Rejects create with obsolete version default in fields", func() {
				// Seed an obsolete ClusterVersion:
				cvDao, err := dao.NewGenericDAO[*privatev1.ClusterVersion]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = cvDao.Create().
					SetObject(privatev1.ClusterVersion_builder{
						Id: uuid.New(),
						Metadata: privatev1.Metadata_builder{
							Name:   "4-16-0-obsolete",
							Tenant: testTenant,
						}.Build(),
						Spec: privatev1.ClusterVersionSpec_builder{
							Image:   "quay.io/openshift-release-dev/ocp-release:4.16.0-multi",
							Enabled: proto.Bool(true),
							Version: "4.16.0",
							State:   privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE,
						}.Build(),
					}.Build()).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = validatedServer.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
					Object: privatev1.ClusterCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Title:    "Obsolete version catalog item",
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						Fields: privatev1.ClusterCatalogItemFields_builder{
							Version: privatev1.ClusterVersionReferenceFieldPolicy_builder{Editable: privatev1.EditableClusterVersionReferenceField_builder{DefaultValue: privatev1.ClusterVersionReference_builder{Name: "4-16-0-obsolete"}.Build()}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("is obsolete"))
			})

			It("Rejects update with non-existent version default in fields", func() {
				createResponse, err := validatedServer.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
					Object: privatev1.ClusterCatalogItem_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Title:    "Catalog item for update test",
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				name := createResponse.GetObject().GetMetadata().GetName()
				_, err = validatedServer.Update(ctx, privatev1.ClusterCatalogItemsUpdateRequest_builder{
					Object: privatev1.ClusterCatalogItem_builder{
						Id:       createResponse.GetObject().GetId(),
						Metadata: privatev1.Metadata_builder{Name: name}.Build(),
						Title:    "Catalog item for update test",
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						Fields: privatev1.ClusterCatalogItemFields_builder{
							Version: privatev1.ClusterVersionReferenceFieldPolicy_builder{Editable: privatev1.EditableClusterVersionReferenceField_builder{DefaultValue: privatev1.ClusterVersionReference_builder{Name: "does-not-exist"}.Build()}.Build()}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("cluster version 'does-not-exist' in fields.version not found"))
			})
		})

		It("Rejects empty name on create", func() {
			_, err := server.Create(ctx, privatev1.ClusterCatalogItemsCreateRequest_builder{
				Object: privatev1.ClusterCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{}.Build(),
					Title:    "Unnamed item",
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("metadata.name"))
		})
	})
})

var _ = Describe("Cluster Catalog Item policy application", func() {
	It("applies every Cluster policy and keeps node sets atomic", func() {
		version := privatev1.ClusterVersionReference_builder{Id: "version-id", Name: "version"}.Build()
		pullSecret := privatev1.SecretLocalReference_builder{Id: "secret-id", Name: "pull-secret"}.Build()
		clusterAttachment := privatev1.ClusterNetworkAttachment_builder{
			Subnet:         policyTestSubnet("cluster-subnet"),
			SecurityGroups: []*privatev1.SecurityGroupLocalReference{policyTestSecurityGroup("cluster-security-group")},
		}.Build()
		workerSize := int32(3)
		nodeMap := privatev1.ClusterNodeSetMap_builder{
			Items: map[string]*privatev1.ClusterTemplateNodeSet{
				"workers": privatev1.ClusterTemplateNodeSet_builder{Size: workerSize}.Build(),
				"empty":   nil,
			},
		}.Build()
		sshKey := "ssh-ed25519 cluster"
		podCIDR := "10.0.0.0/16"
		serviceCIDR := "172.30.0.0/16"
		autoExternalIP := false

		fields := privatev1.ClusterCatalogItemFields_builder{
			Version:                  privatev1.ClusterVersionReferenceFieldPolicy_builder{Locked: version}.Build(),
			SshPublicKey:             privatev1.StringFieldPolicy_builder{Locked: &sshKey}.Build(),
			PullSecretSecret:         privatev1.SecretReferenceFieldPolicy_builder{Locked: pullSecret}.Build(),
			Network:                  privatev1.ClusterNetworkFieldPolicies_builder{PodCidr: privatev1.StringFieldPolicy_builder{Locked: &podCIDR}.Build(), ServiceCidr: privatev1.StringFieldPolicy_builder{Locked: &serviceCIDR}.Build()}.Build(),
			NodeSets:                 privatev1.ClusterNodeSetMapPolicy_builder{Locked: nodeMap}.Build(),
			AutoExternalIpAttachment: privatev1.BoolFieldPolicy_builder{Locked: &autoExternalIP}.Build(),
			NetworkAttachment:        privatev1.ClusterNetworkAttachmentFieldPolicy_builder{Locked: clusterAttachment}.Build(),
		}.Build()
		item := privatev1.ClusterCatalogItem_builder{Fields: fields}.Build()
		spec := &privatev1.ClusterSpec{}

		Expect(applyClusterCatalogItemPolicies(spec, item.GetFields())).To(Succeed())
		Expect(spec.GetVersion()).NotTo(BeIdenticalTo(version))
		Expect(spec.GetVersion().GetName()).To(Equal("version"))
		Expect(spec.GetSshPublicKey()).To(Equal(sshKey))
		Expect(spec.GetPullSecretSecret().GetName()).To(Equal("pull-secret"))
		Expect(spec.GetNetwork().GetPodCidr()).To(Equal(podCIDR))
		Expect(spec.GetNetwork().GetServiceCidr()).To(Equal(serviceCIDR))
		Expect(spec.GetAutoExternalIpAttachment()).To(BeFalse())
		Expect(spec.GetNetworkAttachment()).NotTo(BeIdenticalTo(clusterAttachment))
		Expect(spec.GetNodeSets()).To(HaveLen(2))
		Expect(spec.GetNodeSets()["workers"].GetSize()).To(Equal(workerSize))
		Expect(spec.GetNodeSets()["empty"]).To(BeNil())

		spec.GetVersion().SetName("changed")
		spec.GetNetworkAttachment().GetSubnet().SetName("changed")
		spec.GetNodeSets()["workers"].SetSize(5)
		Expect(version.GetName()).To(Equal("version"))
		Expect(clusterAttachment.GetSubnet().GetName()).To(Equal("cluster-subnet"))
		Expect(nodeMap.GetItems()["workers"].GetSize()).To(Equal(workerSize))
	})
})
