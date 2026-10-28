/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package it

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Role binding authorization", func() {
	It("grants system-wide tenant listing while a tenant admin is in admins", func(ctx context.Context) {
		const roleName = "tenant-admin"
		username := fmt.Sprintf("role-binding-auth-%s", uuid.New()[24:32])

		rolesClient := privatev1.NewRolesClient(tool.InternalView().AdminConn())
		usersClient := privatev1.NewUsersClient(tool.InternalView().AdminConn())
		roleBindingClient := privatev1.NewRoleBindingsClient(tool.InternalView().AdminConn())

		roleResponse, err := rolesClient.List(ctx, privatev1.RolesListRequest_builder{
			Filter: new("this.metadata.name == 'tenant-admin'"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(roleResponse.GetItems()).To(HaveLen(1))
		tenantAdminRoleID := roleResponse.GetItems()[0].GetId()
		Expect(tenantAdminRoleID).ToNot(BeEmpty())

		keycloakRoleCode, _, err := tool.KeycloakAdminRequest(
			ctx, http.MethodGet, "/roles/tenant-admin", nil,
		)
		Expect(err).ToNot(HaveOccurred())
		if keycloakRoleCode == http.StatusNotFound {
			keycloakRoleCode, _, err = tool.KeycloakAdminRequest(
				ctx, http.MethodPost, "/roles", map[string]any{"name": roleName},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(keycloakRoleCode).To(Equal(http.StatusCreated))
			DeferCleanup(func(cleanupCtx context.Context) {
				code, _, cleanupErr := tool.KeycloakAdminRequest(
					cleanupCtx, http.MethodDelete, "/roles/tenant-admin", nil,
				)
				Expect(cleanupErr).ToNot(HaveOccurred())
				Expect(code).To(Equal(http.StatusNoContent))
			})
			keycloakRoleCode, _, err = tool.KeycloakAdminRequest(
				ctx, http.MethodGet, "/roles/tenant-admin", nil,
			)
			Expect(err).ToNot(HaveOccurred())
		}
		Expect(keycloakRoleCode).To(Equal(http.StatusOK))

		groupsCode, groupsBody, err := tool.KeycloakAdminRequest(
			ctx, http.MethodGet, "/groups?search=admins", nil,
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(groupsCode).To(Equal(http.StatusOK))
		var groups []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Path string `json:"path"`
		}
		Expect(json.Unmarshal(groupsBody, &groups)).To(Succeed())
		var adminsGroupID string
		for _, group := range groups {
			if group.Name == "admins" && (group.Path == "" || group.Path == "/admins") {
				adminsGroupID = group.ID
				break
			}
		}
		Expect(adminsGroupID).ToNot(BeEmpty(), "Keycloak realm must contain the admins group")

		// Create a dedicated Keycloak user and give it the same tenant membership
		// as the suite's regular user so its token has a tenant organization claim.
		userCode, _, err := tool.KeycloakAdminRequest(ctx, http.MethodPost, "/users", map[string]any{
			"username":  username,
			"enabled":   true,
			"firstName": username,
			"lastName":  "Test",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(userCode).To(Equal(http.StatusCreated))

		var keycloakUserID string
		findKeycloakUserID := func(requestCtx context.Context) string {
			_, body, err := tool.KeycloakAdminRequest(
				requestCtx, http.MethodGet, fmt.Sprintf("/users?username=%s&exact=true", username), nil,
			)
			Expect(err).ToNot(HaveOccurred())
			var users []struct {
				ID string `json:"id"`
			}
			Expect(json.Unmarshal(body, &users)).To(Succeed())
			Expect(users).To(HaveLen(1))
			return users[0].ID
		}
		DeferCleanup(func(cleanupCtx context.Context) {
			if keycloakUserID == "" {
				keycloakUserID = findKeycloakUserID(cleanupCtx)
			}
			code, _, cleanupErr := tool.KeycloakAdminRequest(
				cleanupCtx, http.MethodDelete, fmt.Sprintf("/users/%s", keycloakUserID), nil,
			)
			Expect(cleanupErr).ToNot(HaveOccurred())
			Expect(code).To(Or(Equal(http.StatusNoContent), Equal(http.StatusNotFound)))
		})
		keycloakUserID, err = tool.keycloakEnsureUserReady(ctx, username)
		Expect(err).ToNot(HaveOccurred())
		Expect(keycloakUserID).ToNot(BeEmpty())
		Expect(tool.ensureUserInOrg(ctx, username, usersGroup)).To(Succeed())

		realmRoleMappingPresent := func() bool {
			code, body, err := tool.KeycloakAdminRequest(
				ctx, http.MethodGet, fmt.Sprintf("/users/%s/role-mappings/realm", keycloakUserID), nil,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(code).To(Equal(http.StatusOK))
			var roleMappings []map[string]any
			Expect(json.Unmarshal(body, &roleMappings)).To(Succeed())
			for _, mapping := range roleMappings {
				if mapping["name"] == roleName {
					return true
				}
			}
			return false
		}
		adminsGroupMembershipPresent := func() bool {
			code, body, err := tool.KeycloakAdminRequest(
				ctx, http.MethodGet, fmt.Sprintf("/users/%s/groups", keycloakUserID), nil,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(code).To(Equal(http.StatusOK))
			var userGroups []struct {
				ID string `json:"id"`
			}
			Expect(json.Unmarshal(body, &userGroups)).To(Succeed())
			for _, group := range userGroups {
				if group.ID == adminsGroupID {
					return true
				}
			}
			return false
		}
		Expect(adminsGroupMembershipPresent()).To(BeFalse())

		userResponse, err := usersClient.Create(ctx, privatev1.UsersCreateRequest_builder{
			Object: privatev1.User_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   username,
					Tenant: usersGroup,
				}.Build(),
				Status: privatev1.UserStatus_builder{
					KeycloakUserId: keycloakUserID,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		userID := userResponse.GetObject().GetId()
		DeferCleanup(func(cleanupCtx context.Context) {
			_, cleanupErr := usersClient.Delete(cleanupCtx, privatev1.UsersDeleteRequest_builder{
				Id: userID,
			}.Build())
			if cleanupErr != nil && grpcstatus.Code(cleanupErr) != grpccodes.NotFound {
				Expect(cleanupErr).ToNot(HaveOccurred())
			}
			Eventually(func(g Gomega) {
				_, getErr := usersClient.Get(cleanupCtx, privatev1.UsersGetRequest_builder{
					Id: userID,
				}.Build())
				g.Expect(getErr).To(HaveOccurred())
				g.Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
			}, time.Minute, time.Second).Should(Succeed())
		})

		// A new token source for each phase forces a fresh login after role or
		// group changes; an existing token keeps its old authorization claims.
		newInternalUserConnection := func() *grpc.ClientConn {
			tokenSource, err := tool.makeKeycloakTokenSource(ctx, username, usersPassword)
			Expect(err).ToNot(HaveOccurred())
			connection, err := tool.makeGrpcConn(internalServiceAddr, tokenSource)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() {
				Expect(connection.Close()).To(Succeed())
			})
			return connection
		}
		expectTenantListDenied := func(connection *grpc.ClientConn) {
			_, err := privatev1.NewTenantsClient(connection).List(ctx, privatev1.TenantsListRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))
		}

		bindingResponse, err := roleBindingClient.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
			Object: privatev1.RoleBinding_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("tenant-admin-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.RoleBindingSpec_builder{
					Role: privatev1.RoleReference_builder{Id: tenantAdminRoleID}.Build(),
					Users: []*privatev1.UserReference{
						privatev1.UserReference_builder{Id: userID}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		bindingID := bindingResponse.GetObject().GetId()
		bindingDeleted := false
		deleteRoleBinding := func(requestCtx context.Context) {
			if bindingID == "" || bindingDeleted {
				return
			}
			_, err := roleBindingClient.Delete(requestCtx, privatev1.RoleBindingsDeleteRequest_builder{
				Id: bindingID,
			}.Build())
			if err != nil {
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
			}
			Eventually(func(g Gomega) {
				_, getErr := roleBindingClient.Get(requestCtx, privatev1.RoleBindingsGetRequest_builder{
					Id: bindingID,
				}.Build())
				g.Expect(getErr).To(HaveOccurred())
				g.Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
			}, time.Minute, time.Second).Should(Succeed())
			bindingDeleted = true
		}
		DeferCleanup(deleteRoleBinding)

		Eventually(func(g Gomega) {
			binding, err := roleBindingClient.Get(ctx, privatev1.RoleBindingsGetRequest_builder{
				Id: bindingID,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(binding.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.RoleBindingState_ROLE_BINDING_STATE_READY),
			)
		}, time.Minute, time.Second).Should(Succeed())
		Eventually(realmRoleMappingPresent, time.Minute, time.Second).Should(BeTrue())

		By("Tenant-admin alone cannot list every tenant")
		expectTenantListDenied(newInternalUserConnection())

		groupMembershipAdded := false
		removeAdminsMembership := func(requestCtx context.Context) {
			if !groupMembershipAdded {
				return
			}
			code, _, err := tool.KeycloakAdminRequest(
				requestCtx, http.MethodDelete,
				fmt.Sprintf("/users/%s/groups/%s", keycloakUserID, adminsGroupID), nil,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(code).To(Equal(http.StatusNoContent))
			groupMembershipAdded = false
		}
		DeferCleanup(removeAdminsMembership)

		By("Adding the user to Keycloak's admins group")
		code, _, err := tool.KeycloakAdminRequest(
			ctx, http.MethodPut,
			fmt.Sprintf("/users/%s/groups/%s", keycloakUserID, adminsGroupID), nil,
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(http.StatusNoContent))
		groupMembershipAdded = true
		Eventually(adminsGroupMembershipPresent, time.Minute, time.Second).Should(BeTrue())

		By("A fresh login with both grants can list every tenant")
		groupAdminConnection := newInternalUserConnection()
		groupTenantResponse, err := privatev1.NewTenantsClient(groupAdminConnection).List(
			ctx, privatev1.TenantsListRequest_builder{}.Build(),
		)
		Expect(err).ToNot(HaveOccurred())
		platformTenantResponse, err := privatev1.NewTenantsClient(tool.InternalView().AdminConn()).List(
			ctx, privatev1.TenantsListRequest_builder{}.Build(),
		)
		Expect(err).ToNot(HaveOccurred())
		platformTenantIDs := make([]string, 0, len(platformTenantResponse.GetItems()))
		for _, tenant := range platformTenantResponse.GetItems() {
			platformTenantIDs = append(platformTenantIDs, tenant.GetId())
		}
		groupTenantIDs := make([]string, 0, len(groupTenantResponse.GetItems()))
		for _, tenant := range groupTenantResponse.GetItems() {
			groupTenantIDs = append(groupTenantIDs, tenant.GetId())
		}
		Expect(groupTenantIDs).To(ConsistOf(platformTenantIDs))

		By("Removing both grants")
		deleteRoleBinding(ctx)
		Eventually(realmRoleMappingPresent, time.Minute, time.Second).Should(BeFalse())
		removeAdminsMembership(ctx)
		Eventually(adminsGroupMembershipPresent, time.Minute, time.Second).Should(BeFalse())

		By("A fresh login after both grants are removed is denied")
		expectTenantListDenied(newInternalUserConnection())
	})
})
