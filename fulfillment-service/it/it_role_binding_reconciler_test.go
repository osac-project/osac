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
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/fulfillment-service/internal/uuid"
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
			"username": "my-user",
			"enabled":  true,
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
					Name: "my-user",
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
					Role: testRoleName,
					Users: []string{
						testUserID,
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := createResponse.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.RoleBindingsDeleteRequest_builder{
				Id: id,
			}.Build())
		})

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
					Role: testRoleName,
					Users: []string{
						testUserID,
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
})
