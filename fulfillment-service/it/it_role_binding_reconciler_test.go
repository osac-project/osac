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
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Role binding reconciler", func() {
	var (
		ctx            context.Context
		client         privatev1.RoleBindingsClient
		rolesClient    privatev1.RolesClient
		usersClient    privatev1.UsersClient
		testRoleName   string
		testUserID     string
		testKeycloakID string
	)

	BeforeEach(func() {
		ctx = context.Background()
		client = privatev1.NewRoleBindingsClient(tool.InternalView().AdminConn())
		rolesClient = privatev1.NewRolesClient(tool.InternalView().AdminConn())
		usersClient = privatev1.NewUsersClient(tool.InternalView().AdminConn())
		testRoleName = fmt.Sprintf("test-role-%s", uuid.New())

		// Create a role in OSAC that will also be created in Keycloak
		roleResponse, err := rolesClient.Create(ctx, privatev1.RolesCreateRequest_builder{
			Object: privatev1.Role_builder{
				Metadata: privatev1.Metadata_builder{
					Name: testRoleName,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Clean up the role after the test
		DeferCleanup(func() {
			_, _ = rolesClient.Delete(ctx, privatev1.RolesDeleteRequest_builder{
				Id: roleResponse.GetObject().GetId(),
			}.Build())
		})

		// Create the corresponding role in Keycloak
		rolePayload := map[string]any{
			"name":        testRoleName,
			"description": fmt.Sprintf("Test role %s", testRoleName),
		}
		code, _, err := tool.KeycloakAdminRequest(ctx, "POST", "/roles", rolePayload)
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(201)) // HTTP 201 Created

		// Clean up the Keycloak role after the test
		DeferCleanup(func() {
			_, _, _ = tool.KeycloakAdminRequest(ctx, "DELETE", fmt.Sprintf("/roles/%s", testRoleName), nil)
		})

		// Create a test user in Keycloak
		testUserPayload := map[string]any{
			"username":      "my-user",
			"enabled":       true,
			"email":         "my-user@test.local",
			"emailVerified": true,
			"firstName":     "My",
			"lastName":      "Test",
			"credentials": []map[string]any{{
				"type":      "password",
				"value":     usersPassword,
				"temporary": false,
			}},
		}
		code, _, err = tool.KeycloakAdminRequest(ctx, "POST", "/users", testUserPayload)
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(201)) // HTTP 201 Created

		// Get the user ID (UUID) from Keycloak
		getUserCode, getUserBody, err := tool.KeycloakAdminRequest(ctx, "GET", "/users?username=my-user&exact=true", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(getUserCode).To(Equal(200))
		var users []map[string]any
		err = json.Unmarshal(getUserBody, &users)
		Expect(err).ToNot(HaveOccurred())
		Expect(users).To(HaveLen(1))
		var ok bool
		testKeycloakID, ok = users[0]["id"].(string)
		Expect(ok).To(BeTrue())
		Expect(testKeycloakID).ToNot(BeEmpty())

		// Clean up the Keycloak user after the test
		DeferCleanup(func() {
			_, _, _ = tool.KeycloakAdminRequest(ctx, "DELETE", fmt.Sprintf("/users/%s", testKeycloakID), nil)
		})

		// Create a User object in OSAC with the Keycloak ID in status
		userResponse, err := usersClient.Create(ctx, privatev1.UsersCreateRequest_builder{
			Object: privatev1.User_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("my-user-%s", uuid.New()[24:32]),
				}.Build(),
				Status: privatev1.UserStatus_builder{
					KeycloakUserId: testKeycloakID,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		testUserID = userResponse.GetObject().GetId()

		// Clean up the OSAC user after the test
		DeferCleanup(func() {
			_, _ = usersClient.Delete(ctx, privatev1.UsersDeleteRequest_builder{
				Id: testUserID,
			}.Build())
		})
	})

	It("Adds the finalizer and sets the default state when a role binding is created", func() {
		// Create the role binding and remember to delete it after the test:
		createResponse, err := client.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
			Object: privatev1.RoleBinding_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("my-%s", uuid.New()),
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role: privatev1.RoleReference_builder{Name: testRoleName}.Build(),
					Users: []*privatev1.UserReference{
						privatev1.UserReference_builder{Id: testUserID}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := createResponse.GetObject().GetId()
		bindingDeleted := false
		deleteRoleBinding := func(requestCtx context.Context) {
			if bindingDeleted {
				return
			}
			_, err := client.Delete(requestCtx, privatev1.RoleBindingsDeleteRequest_builder{
				Id: id,
			}.Build())
			if err != nil {
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
			}
			Eventually(func(g Gomega) {
				_, getErr := client.Get(requestCtx, privatev1.RoleBindingsGetRequest_builder{
					Id: id,
				}.Build())
				g.Expect(getErr).To(HaveOccurred())
				g.Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
			}, time.Minute, time.Second).Should(Succeed())
			bindingDeleted = true
		}
		DeferCleanup(deleteRoleBinding)

		// Verify that reconciler eventually adds the finalizer and sets the state to READY after syncing:
		Eventually(
			func(g Gomega) {
				getResponse, err := client.Get(ctx, privatev1.RoleBindingsGetRequest_builder{
					Id: id,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				object := getResponse.GetObject()
				g.Expect(object.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
				g.Expect(object.GetStatus().GetState()).To(
					Equal(privatev1.RoleBindingState_ROLE_BINDING_STATE_READY),
				)
			},
			time.Minute,
			time.Second,
		).Should(Succeed())
	})

	It("Removes the finalizer and deletes the role binding when it is deleted", func() {
		// Create the role binding:
		createResponse, err := client.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
			Object: privatev1.RoleBinding_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("my-%s", uuid.New()),
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role: privatev1.RoleReference_builder{Name: testRoleName}.Build(),
					Users: []*privatev1.UserReference{
						privatev1.UserReference_builder{Id: testUserID}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := createResponse.GetObject().GetId()

		// Wait for the reconciler to add the finalizer before deleting, as otherwise it would be deleted
		// immediately by the server without going through the reconciler.
		Eventually(
			func(g Gomega) {
				getResponse, err := client.Get(ctx, privatev1.RoleBindingsGetRequest_builder{
					Id: id,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(getResponse.GetObject().GetMetadata().GetFinalizers()).To(
					ContainElement(finalizers.Controller),
				)
			},
			time.Minute,
			time.Second,
		).Should(Succeed())

		// Delete the role binding:
		_, err = client.Delete(ctx, privatev1.RoleBindingsDeleteRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Verify the role binding eventually disappears:
		Eventually(
			func(g Gomega) {
				_, err := client.Get(ctx, privatev1.RoleBindingsGetRequest_builder{
					Id: id,
				}.Build())
				g.Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				g.Expect(ok).To(BeTrue())
				g.Expect(status.Code()).To(Equal(grpccodes.NotFound))
			},
			time.Minute,
			time.Second,
		).Should(Succeed())
	})

	DescribeTable("keeps existing tokens unchanged and applies reader roles to new tokens",
		func(roleName, tenant string) {
			_, err := tool.keycloakEnsureUserReady(ctx, "my-user")
			Expect(err).ToNot(HaveOccurred())
			Expect(tool.ensureUserInOrg(ctx, "my-user", tenant)).To(Succeed())

			assets := createReaderTestAssets(ctx)
			oldTokenSource, err := tool.makeKeycloakTokenSource(ctx, "my-user", usersPassword)
			Expect(err).ToNot(HaveOccurred())
			oldToken, err := oldTokenSource.Token(ctx)
			Expect(err).ToNot(HaveOccurred())
			staticOldTokenSource, err := auth.NewStaticTokenSource().
				SetLogger(tool.logger).
				SetToken(oldToken).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(readerTokenRoles(ctx, oldTokenSource)).ToNot(ContainElement(roleName))
			oldConn, err := tool.makeGrpcConn(externalServiceAddr, staticOldTokenSource)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() {
				Expect(oldConn.Close()).To(Succeed())
			})
			oldClient := publicv1.NewClustersClient(oldConn)

			original := createReaderTestCluster(ctx, oldConn, tenant,
				"role-sync-original-"+uuid.New()[24:], assets)
			getResponse, err := oldClient.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: original.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetId()).To(Equal(original.GetId()))

			createReaderRoleBinding(ctx, testUserID, roleName)
			Expect(readerTokenRoles(ctx, staticOldTokenSource)).ToNot(ContainElement(roleName))

			getResponse, err = oldClient.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: original.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetId()).To(Equal(original.GetId()))
			oldTokenCluster := createReaderTestCluster(ctx, oldConn, tenant,
				"role-sync-old-token-create-"+uuid.New()[24:], assets)

			newTokenSource, err := tool.makeKeycloakTokenSource(ctx, "my-user", usersPassword)
			Expect(err).ToNot(HaveOccurred())
			Expect(readerTokenRoles(ctx, newTokenSource)).To(ContainElement(roleName))
			newConn, err := tool.makeGrpcConn(externalServiceAddr, newTokenSource)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() {
				Expect(newConn.Close()).To(Succeed())
			})
			newClient := publicv1.NewClustersClient(newConn)

			for _, cluster := range []*publicv1.Cluster{original, oldTokenCluster} {
				getResponse, err := newClient.Get(ctx, publicv1.ClustersGetRequest_builder{
					Id: cluster.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(getResponse.GetObject().GetId()).To(Equal(cluster.GetId()))
			}

			createdName := "role-sync-new-token-create-" + uuid.New()[24:]
			createResponse, err := newClient.Create(ctx, readerTestClusterCreateRequest(createdName, tenant, assets))
			if err == nil {
				Expect(createResponse).ToNot(BeNil())
				Expect(createResponse.GetObject()).ToNot(BeNil())
				deferClusterDeletion(createResponse.GetObject().GetId(), tool.InternalView().AdminConn())
			}
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))

			candidate := proto.Clone(original).(*publicv1.Cluster)
			candidate.GetSpec().GetNodeSets()["my-node-set"].SetSize(4)
			_, err = newClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{Object: candidate}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))

			_, err = newClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{Id: original.GetId()}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))

			adminClient := publicv1.NewClustersClient(tool.InternalView().AdminConn())
			persisted, err := adminClient.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: original.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(persisted.GetObject().GetSpec().GetNodeSets()["my-node-set"].GetSize()).To(Equal(int32(3)))
			Expect(persisted.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse())

			filter := fmt.Sprintf("this.metadata.name == %q", createdName)
			persistedList, err := adminClient.List(ctx, publicv1.ClustersListRequest_builder{
				Filter: &filter,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(persistedList.GetItems()).To(BeEmpty())
		},
		Entry("tenant-reader", "tenant-reader", "users"),
		Entry("cloud-provider-reader", "cloud-provider-reader", "sales"),
	)
	It("Assigns and removes the Keycloak role mapping with a role binding", func() {
		roleMappingPath := fmt.Sprintf("/users/%s/role-mappings/realm", testKeycloakID)
		expectRoleMapping := func(g Gomega, wantPresent bool) {
			code, body, err := tool.KeycloakAdminRequest(ctx, "GET", roleMappingPath, nil)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(code).To(Equal(200))

			var roleMappings []map[string]any
			g.Expect(json.Unmarshal(body, &roleMappings)).To(Succeed())

			mapped := false
			for _, mapping := range roleMappings {
				if mapping["name"] == testRoleName {
					mapped = true
					break
				}
			}
			g.Expect(mapped).To(Equal(wantPresent))
		}

		createResponse, err := client.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
			Object: privatev1.RoleBinding_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("my-%s", uuid.New()),
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role: privatev1.RoleReference_builder{Name: testRoleName}.Build(),
					Users: []*privatev1.UserReference{
						privatev1.UserReference_builder{Id: testUserID}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := createResponse.GetObject().GetId()
		bindingDeleted := false
		deleteRoleBinding := func(requestCtx context.Context) {
			if bindingDeleted {
				return
			}
			_, err := client.Delete(requestCtx, privatev1.RoleBindingsDeleteRequest_builder{
				Id: id,
			}.Build())
			if err != nil {
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
			}
			Eventually(func(g Gomega) {
				_, getErr := client.Get(requestCtx, privatev1.RoleBindingsGetRequest_builder{
					Id: id,
				}.Build())
				g.Expect(getErr).To(HaveOccurred())
				g.Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
			}, time.Minute, time.Second).Should(Succeed())
			bindingDeleted = true
		}
		DeferCleanup(deleteRoleBinding)

		Eventually(
			func(g Gomega) {
				getResponse, err := client.Get(ctx, privatev1.RoleBindingsGetRequest_builder{
					Id: id,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				object := getResponse.GetObject()
				g.Expect(object.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
				g.Expect(object.GetStatus().GetState()).To(
					Equal(privatev1.RoleBindingState_ROLE_BINDING_STATE_READY),
				)
			},
			time.Minute,
			time.Second,
		).Should(Succeed())

		Eventually(func(g Gomega) {
			expectRoleMapping(g, true)
		}, time.Minute, time.Second).Should(Succeed())

		deleteRoleBinding(ctx)
		Eventually(func(g Gomega) {
			expectRoleMapping(g, false)
		}, time.Minute, time.Second).Should(Succeed())
	})
})

var _ = Describe("RoleBinding references", func() {
	It("resolves a global role by name for a tenant RoleBinding", func() {
		ctx := context.Background()
		conn := tool.InternalView().AdminConn()
		tenantClient := privatev1.NewTenantsClient(conn)
		projectsClient := privatev1.NewProjectsClient(conn)
		client := privatev1.NewRoleBindingsClient(conn)
		tenantName := "role-binding-reference-" + uuid.New()
		tenantResponse, err := tenantClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{Name: tenantName}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		tenantID := tenantResponse.GetObject().GetId()

		bindingID := ""
		DeferCleanup(func(cleanupCtx context.Context) {
			if bindingID != "" {
				_, err := client.Delete(cleanupCtx, privatev1.RoleBindingsDeleteRequest_builder{Id: bindingID}.Build())
				Expect(err).ToNot(HaveOccurred())
				Eventually(func(g Gomega) {
					_, err := client.Get(cleanupCtx, privatev1.RoleBindingsGetRequest_builder{Id: bindingID}.Build())
					status, ok := grpcstatus.FromError(err)
					g.Expect(ok).To(BeTrue())
					g.Expect(status.Code()).To(Equal(grpccodes.NotFound))
				}, time.Minute, time.Second).Should(Succeed())
			}
			deleteTenant(cleanupCtx, tenantClient, projectsClient, tenantID, tenantName)
		})

		name := "tenant-admin-binding-" + uuid.New()
		response, err := client.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
			Object: privatev1.RoleBinding_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   name,
					Tenant: tenantName,
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role: privatev1.RoleReference_builder{Name: "tenant-admin"}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		bindingID = response.GetObject().GetId()
		Expect(response.GetObject().GetSpec().GetRole().GetId()).ToNot(BeEmpty())
	})
})
