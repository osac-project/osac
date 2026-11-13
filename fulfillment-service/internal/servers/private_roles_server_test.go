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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/authz/catalog"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Private roles server", func() {
	var rolesServer *PrivateRolesServer

	BeforeEach(func() {
		var err error

		permissionCatalog, err := catalog.Build(protoregistry.GlobalFiles)
		Expect(err).ToNot(HaveOccurred())

		rolesServer, err = NewPrivateRolesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetPermissionCatalog(permissionCatalog).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Creation", func() {
		It("Can be built if all required parameters are set", func() {
			permissionCatalog, err := catalog.Build(protoregistry.GlobalFiles)
			Expect(err).ToNot(HaveOccurred())
			server, err := NewPrivateRolesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetPermissionCatalog(permissionCatalog).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			permissionCatalog, err := catalog.Build(protoregistry.GlobalFiles)
			Expect(err).ToNot(HaveOccurred())
			server, err := NewPrivateRolesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetPermissionCatalog(permissionCatalog).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			permissionCatalog, err := catalog.Build(protoregistry.GlobalFiles)
			Expect(err).ToNot(HaveOccurred())
			server, err := NewPrivateRolesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetPermissionCatalog(permissionCatalog).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if permission catalog is not set", func() {
			server, err := NewPrivateRolesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("permission catalog is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		It("Creates a role", func() {
			response, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "admin-role",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			Expect(response.GetObject()).ToNot(BeNil())
			Expect(response.GetObject().GetId()).ToNot(BeEmpty())
			Expect(response.GetObject().GetMetadata().GetName()).To(Equal("admin-role"))
		})

		It("Lists roles", func() {
			_, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "role-a",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "role-b",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			listResponse, err := rolesServer.List(ctx, privatev1.RolesListRequest_builder{
				Filter: new("this.metadata.name in ['role-a', 'role-b']"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse.GetSize()).To(Equal(int32(2)))
			Expect(listResponse.GetItems()).To(HaveLen(2))
		})

		It("Gets a role", func() {
			createResponse, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "admin-role",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := rolesServer.Get(ctx, privatev1.RolesGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetId()).To(Equal(createResponse.GetObject().GetId()))
			Expect(getResponse.GetObject().GetMetadata().GetName()).To(Equal("admin-role"))
		})

		It("Updates a role", func() {
			createResponse, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "admin-role",
					}.Build(),
					Spec: privatev1.RoleSpec_builder{
						Title:       "Original title",
						Description: "Original description.",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := rolesServer.Update(ctx, privatev1.RolesUpdateRequest_builder{
				Object: privatev1.Role_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.RoleSpec_builder{
						Title: "Updated title",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{
						"spec.title",
					},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetTitle()).To(Equal("Updated title"))
		})

		It("Ignores fields not included in the update mask", func() {
			createResponse, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "admin-role",
					}.Build(),
					Spec: privatev1.RoleSpec_builder{
						Title:       "Original title",
						Description: "Original description.",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := rolesServer.Update(ctx, privatev1.RolesUpdateRequest_builder{
				Object: privatev1.Role_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.RoleSpec_builder{
						Title:       "Updated title",
						Description: "Updated description.",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{
						"spec.title",
					},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetTitle()).To(Equal("Updated title"))
			Expect(updateResponse.GetObject().GetSpec().GetDescription()).To(Equal("Original description."))
		})

		It("Deletes a role", func() {
			createResponse, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "admin-role",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = rolesServer.Delete(ctx, privatev1.RolesDeleteRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Signals a role", func() {
			createResponse, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "admin-role",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = rolesServer.Signal(ctx, privatev1.RolesSignalRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Permission rule validation", func() {
		It("Creates a role with valid permission rules and stores them verbatim", func() {
			response, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "cluster-viewer",
					}.Build(),
					Spec: privatev1.RoleSpec_builder{
						Permissions: []*privatev1.PermissionRule{
							privatev1.PermissionRule_builder{
								Service: "osac.public.clusters",
								Methods: []string{"get", "list"},
							}.Build(),
							privatev1.PermissionRule_builder{
								Service: "osac.private.volumes",
								Methods: []string{"*"},
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			permissions := response.GetObject().GetSpec().GetPermissions()
			Expect(permissions).To(HaveLen(2))
			Expect(permissions[0].GetService()).To(Equal("osac.public.clusters"))
			Expect(permissions[0].GetMethods()).To(Equal([]string{"get", "list"}))
			Expect(permissions[1].GetService()).To(Equal("osac.private.volumes"))
			Expect(permissions[1].GetMethods()).To(Equal([]string{"*"}))
		})

		DescribeTable("Rejects the creation of roles with invalid permission rules",
			func(rule *privatev1.PermissionRule_builder, expectedError string) {
				_, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
					Object: privatev1.Role_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "invalid-role",
						}.Build(),
						Spec: privatev1.RoleSpec_builder{
							Permissions: []*privatev1.PermissionRule{rule.Build()},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring(expectedError))
				Expect(err.Error()).ToNot(ContainSubstring(rule.Service), "the error must not echo the rule values")
				for _, method := range rule.Methods {
					Expect(err.Error()).ToNot(ContainSubstring(method), "the error must not echo the rule values")
				}

				listResponse, err := rolesServer.List(ctx, privatev1.RolesListRequest_builder{
					Filter: new("this.metadata.name == 'invalid-role'"),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(listResponse.GetSize()).To(Equal(int32(0)), "no role row should be created")
			},
			Entry("Unknown service",
				&privatev1.PermissionRule_builder{
					Service: "osac.public.nope",
					Methods: []string{"create"},
				}, `invalid permissions rule 1: unknown service`),
			Entry("Unknown method",
				&privatev1.PermissionRule_builder{
					Service: "osac.public.clusters",
					Methods: []string{"fake"},
				}, `invalid permissions rule 1: unknown method`),
			Entry("Malformed service wildcard",
				&privatev1.PermissionRule_builder{
					Service: "osac.public.clus*",
					Methods: []string{"get"},
				}, `invalid permissions rule 1: unknown service`),
			Entry("Malformed method wildcard",
				&privatev1.PermissionRule_builder{
					Service: "osac.public.clusters",
					Methods: []string{"crea*"},
				}, `invalid permissions rule 1: unknown method`),
			Entry("Capitalized method",
				&privatev1.PermissionRule_builder{
					Service: "osac.public.clusters",
					Methods: []string{"Get"},
				}, `invalid permissions rule 1: unknown method`),
		)

		It("Rejects the creation of roles with an empty method list", func() {
			_, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "invalid-role",
					}.Build(),
					Spec: privatev1.RoleSpec_builder{
						Permissions: []*privatev1.PermissionRule{
							privatev1.PermissionRule_builder{
								Service: "osac.public.clusters",
								Methods: []string{},
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("invalid permissions rule 1"))
		})

		It("Rejects the creation of roles with an unknown method in a later rule", func() {
			_, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "invalid-role",
					}.Build(),
					Spec: privatev1.RoleSpec_builder{
						Permissions: []*privatev1.PermissionRule{
							privatev1.PermissionRule_builder{
								Service: "osac.public.clusters",
								Methods: []string{"get"},
							}.Build(),
							privatev1.PermissionRule_builder{
								Service: "osac.public.clusters",
								Methods: []string{"fake"},
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("invalid permissions rule 2"))
		})

		It("Updates the permission rules of a role", func() {
			createResponse, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "cluster-viewer",
					}.Build(),
					Spec: privatev1.RoleSpec_builder{
						Permissions: []*privatev1.PermissionRule{
							privatev1.PermissionRule_builder{
								Service: "osac.public.clusters",
								Methods: []string{"get"},
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := rolesServer.Update(ctx, privatev1.RolesUpdateRequest_builder{
				Object: privatev1.Role_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.RoleSpec_builder{
						Permissions: []*privatev1.PermissionRule{
							privatev1.PermissionRule_builder{
								Service: "osac.public.clusters",
								Methods: []string{"get", "list"},
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.permissions"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			permissions := updateResponse.GetObject().GetSpec().GetPermissions()
			Expect(permissions).To(HaveLen(1))
			Expect(permissions[0].GetMethods()).To(Equal([]string{"get", "list"}))
		})

		It("Rejects updating the permission rules with an unknown service, keeping the role unchanged", func() {
			createResponse, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "cluster-viewer",
					}.Build(),
					Spec: privatev1.RoleSpec_builder{
						Permissions: []*privatev1.PermissionRule{
							privatev1.PermissionRule_builder{
								Service: "osac.public.clusters",
								Methods: []string{"get"},
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = rolesServer.Update(ctx, privatev1.RolesUpdateRequest_builder{
				Object: privatev1.Role_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.RoleSpec_builder{
						Permissions: []*privatev1.PermissionRule{
							privatev1.PermissionRule_builder{
								Service: "osac.public.nope",
								Methods: []string{"get"},
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.permissions"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring(`unknown service`))
			Expect(err.Error()).ToNot(ContainSubstring(`osac.public.nope`), "the error must not echo the rule values")

			getResponse, err := rolesServer.Get(ctx, privatev1.RolesGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			permissions := getResponse.GetObject().GetSpec().GetPermissions()
			Expect(permissions).To(HaveLen(1))
			Expect(permissions[0].GetService()).To(Equal("osac.public.clusters"))
		})

		It("Allows updates that don't touch the permission rules", func() {
			createResponse, err := rolesServer.Create(ctx, privatev1.RolesCreateRequest_builder{
				Object: privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "cluster-viewer",
					}.Build(),
					Spec: privatev1.RoleSpec_builder{
						Title: "Original title",
						Permissions: []*privatev1.PermissionRule{
							privatev1.PermissionRule_builder{
								Service: "osac.public.clusters",
								Methods: []string{"get"},
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := rolesServer.Update(ctx, privatev1.RolesUpdateRequest_builder{
				Object: privatev1.Role_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.RoleSpec_builder{
						Title: "Updated title",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.title"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetTitle()).To(Equal("Updated title"))
			Expect(updateResponse.GetObject().GetSpec().GetPermissions()).To(HaveLen(1))
		})
	})
})
