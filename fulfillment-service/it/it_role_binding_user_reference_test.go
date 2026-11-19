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
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("RoleBinding UserReference authorization", func() {
	var (
		ctx            context.Context
		usersAdmin     privatev1.UsersClient
		bindingsAdmin  privatev1.RoleBindingsClient
		publicBindings publicv1.RoleBindingsClient
		actorName      string
		actorID        string
	)

	BeforeEach(func(specCtx context.Context) {
		ctx = specCtx
		adminConn := tool.InternalView().AdminConn()
		usersAdmin = privatev1.NewUsersClient(adminConn)
		bindingsAdmin = privatev1.NewRoleBindingsClient(adminConn)

		actorName = "user-ref-actor-" + uuid.New()[24:]
		actorMetadata := privatev1.Metadata_builder{Name: actorName, Tenant: usersGroup}.Build()
		var actorKeycloakID string
		actorID, actorKeycloakID = createRoleBindingTestUser(ctx, usersAdmin, actorName, actorMetadata)
		Expect(tool.ensureUserInOrg(ctx, actorName, usersGroup)).To(Succeed())

		roleCode, roleBody, err := tool.KeycloakAdminRequest(ctx, http.MethodGet, "/roles/tenant-admin", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(roleCode).To(Equal(http.StatusOK))
		var tenantAdminRole map[string]any
		Expect(json.Unmarshal(roleBody, &tenantAdminRole)).To(Succeed())
		roleCode, _, err = tool.KeycloakAdminRequest(
			ctx, http.MethodPost,
			fmt.Sprintf("/users/%s/role-mappings/realm", actorKeycloakID),
			[]map[string]any{tenantAdminRole},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(roleCode).To(Equal(http.StatusNoContent))

		tokenSource, err := tool.makeKeycloakTokenSource(ctx, actorName, usersPassword)
		Expect(err).ToNot(HaveOccurred())
		actorConn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(actorConn.Close()).To(Succeed()) })
		publicBindings = publicv1.NewRoleBindingsClient(actorConn)
		_, err = publicv1.NewUsersClient(actorConn).List(ctx, publicv1.UsersListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred(), "tenant-admin fixture must be authorized to create RoleBindings")

		// A same-tenant OSAC User is a positive control for the public
		// UserReference validation path.
		positiveBinding, err := publicBindings.Create(ctx, publicv1.RoleBindingsCreateRequest_builder{
			Object: publicv1.RoleBinding_builder{
				Metadata: publicv1.Metadata_builder{
					Name:   "user-ref-same-tenant-" + uuid.New()[24:],
					Tenant: usersGroup,
				}.Build(),
				Spec: publicv1.RoleBindingSpec_builder{
					Role: publicv1.RoleReference_builder{Name: "tenant-idp-manager"}.Build(),
					Users: []*publicv1.UserReference{
						publicv1.UserReference_builder{Id: actorID}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(positiveBinding.GetObject().GetSpec().GetRole().GetTenant()).To(Equal("shared"))
		positiveBindingID := positiveBinding.GetObject().GetId()
		DeferCleanup(func(cleanupCtx context.Context) {
			_, deleteErr := bindingsAdmin.Delete(cleanupCtx, privatev1.RoleBindingsDeleteRequest_builder{
				Id: positiveBindingID,
			}.Build())
			if grpcstatus.Code(deleteErr) != grpccodes.NotFound {
				Expect(deleteErr).ToNot(HaveOccurred())
			}
			Eventually(func(g Gomega) {
				_, getErr := bindingsAdmin.Get(cleanupCtx, privatev1.RoleBindingsGetRequest_builder{
					Id: positiveBindingID,
				}.Build())
				g.Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
			}, time.Minute, time.Second).Should(Succeed())
		})
	})

	It("rejects a UserReference to a user in another tenant", func(ctx context.Context) {
		foreignName := "user-ref-foreign-" + uuid.New()[24:]
		foreignMetadata := privatev1.Metadata_builder{Name: foreignName, Tenant: "development"}.Build()
		foreignUserID, _ := createRoleBindingTestUser(ctx, usersAdmin, foreignName, foreignMetadata)

		bindingName := "user-ref-cross-tenant-" + uuid.New()[24:]
		_, err := publicBindings.Create(ctx, publicv1.RoleBindingsCreateRequest_builder{
			Object: publicv1.RoleBinding_builder{
				Metadata: publicv1.Metadata_builder{Name: bindingName, Tenant: usersGroup}.Build(),
				Spec: publicv1.RoleBindingSpec_builder{
					Role: publicv1.RoleReference_builder{Name: "tenant-admin"}.Build(),
					Users: []*publicv1.UserReference{
						publicv1.UserReference_builder{Id: foreignUserID}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		expectRoleBindingAbsent(ctx, bindingsAdmin, bindingName)
	})

	It("rejects a Keycloak user without an OSAC User record", func(ctx context.Context) {
		username := "user-ref-unprovisioned-" + uuid.New()[24:]
		keycloakUser := map[string]any{
			"username":      username,
			"enabled":       true,
			"email":         fmt.Sprintf("%s@test.local", username),
			"emailVerified": true,
			"firstName":     "Not",
			"lastName":      "Provisioned",
			"credentials": []map[string]any{{
				"type":      "password",
				"value":     usersPassword,
				"temporary": false,
			}},
		}
		code, _, err := tool.KeycloakAdminRequest(ctx, http.MethodPost, "/users", keycloakUser)
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(http.StatusCreated))
		DeferCleanup(func(cleanupCtx context.Context) {
			query := url.Values{}
			query.Set("username", username)
			query.Set("exact", "true")
			statusCode, body, getErr := tool.KeycloakAdminRequest(
				cleanupCtx, http.MethodGet, "/users?"+query.Encode(), nil,
			)
			Expect(getErr).ToNot(HaveOccurred())
			Expect(statusCode).To(Equal(http.StatusOK))
			var users []map[string]any
			Expect(json.Unmarshal(body, &users)).To(Succeed())
			Expect(users).To(HaveLen(1))
			userID, ok := users[0]["id"].(string)
			Expect(ok).To(BeTrue())
			statusCode, _, getErr = tool.KeycloakAdminRequest(
				cleanupCtx, http.MethodDelete, fmt.Sprintf("/users/%s", userID), nil,
			)
			Expect(getErr).ToNot(HaveOccurred())
			Expect(statusCode).To(Equal(http.StatusNoContent))
		})
		Expect(tool.ensureUserInOrg(ctx, username, usersGroup)).To(Succeed())

		userFilter := fmt.Sprintf("this.metadata.name == %q && this.metadata.tenant == %q", username, usersGroup)
		usersResponse, err := usersAdmin.List(ctx, privatev1.UsersListRequest_builder{
			Filter: &userFilter,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(usersResponse.GetItems()).To(BeEmpty(), "Keycloak user must not yet have an OSAC User record")

		bindingName := "user-ref-unprovisioned-" + uuid.New()[24:]
		_, err = publicBindings.Create(ctx, publicv1.RoleBindingsCreateRequest_builder{
			Object: publicv1.RoleBinding_builder{
				Metadata: publicv1.Metadata_builder{Name: bindingName, Tenant: usersGroup}.Build(),
				Spec: publicv1.RoleBindingSpec_builder{
					Role: publicv1.RoleReference_builder{Name: "tenant-admin"}.Build(),
					Users: []*publicv1.UserReference{
						publicv1.UserReference_builder{Name: username}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		expectRoleBindingAbsent(ctx, bindingsAdmin, bindingName)
	})
})

func expectRoleBindingAbsent(ctx context.Context, client privatev1.RoleBindingsClient, name string) {
	GinkgoHelper()
	filter := fmt.Sprintf("this.metadata.name == %q && this.metadata.tenant == %q", name, usersGroup)
	response, err := client.List(ctx, privatev1.RoleBindingsListRequest_builder{Filter: &filter}.Build())
	Expect(err).ToNot(HaveOccurred())
	Expect(response.GetItems()).To(BeEmpty())
}
