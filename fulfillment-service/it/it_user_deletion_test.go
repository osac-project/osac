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
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("User deletion with a cached Keycloak ID", Label("user-deletion"), func() {
	DescribeTable("removes the persisted User without deleting other identities",
		func(ctx context.Context, alreadyAbsent bool) {
			tenants := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
			users := privatev1.NewUsersClient(tool.InternalView().AdminConn())
			suffix := uuid.New()[24:]
			tenantA, tenantB := "delete-a-"+suffix, "delete-b-"+suffix
			for _, tenant := range []string{tenantA, tenantB} {
				id := createTenant(ctx, tenants, tenant)
				waitForTenantSynced(ctx, tenants, id)
			}

			// Reuse the existing Keycloak/account fixture and its cleanup.
			createUser := func(tenant, name, username string) (string, string) {
				id, keycloakID := createRoleBindingTestUser(ctx, users, username,
					privatev1.Metadata_builder{Name: name, Tenant: tenant}.Build())
				Expect(tool.ensureUserInOrg(ctx, username, tenant)).To(Succeed())
				_, err := users.Update(ctx, privatev1.UsersUpdateRequest_builder{
					Object: privatev1.User_builder{
						Id:   id,
						Spec: privatev1.UserSpec_builder{Username: username}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.username"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				return id, keycloakID
			}
			username := "original-" + suffix
			originalID, cachedID := createUser(tenantA, username, username)
			otherID, otherKeycloakID := createUser(tenantB, "other-"+suffix, "other-"+suffix)

			By("waiting for the cached ID and controller finalizer to be persisted")
			Eventually(ctx, func(g Gomega) {
				response, err := users.Get(ctx, privatev1.UsersGetRequest_builder{Id: originalID}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(response.GetObject().GetStatus().GetKeycloakUserId()).To(Equal(cachedID))
				g.Expect(response.GetObject().GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
			}, time.Minute, 200*time.Millisecond).Should(Succeed())

			preserved := map[string]string{otherID: otherKeycloakID}
			if alreadyAbsent {
				By("removing only the exact cached account before requesting User deletion")
				code, _, err := tool.KeycloakAdminRequest(ctx, http.MethodDelete, "/users/"+cachedID, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(code).To(Equal(http.StatusNoContent))
				code, body, err := tool.KeycloakAdminRequest(ctx, http.MethodGet, "/users/"+cachedID, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(code).To(Equal(http.StatusNotFound))
				var failure struct {
					Error string `json:"error"`
				}
				Expect(json.Unmarshal(body, &failure)).To(Succeed())
				Expect(failure.Error).To(Equal("User not found"))

				By("creating a different stable identity with the original username")
				replacementID, replacementKeycloakID := createUser(tenantA, "replacement-"+suffix, username)
				Expect(replacementKeycloakID).ToNot(Equal(cachedID))
				preserved[replacementID] = replacementKeycloakID
			} else {
				code, _, err := tool.KeycloakAdminRequest(ctx, http.MethodGet, "/users/"+cachedID, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(code).To(Equal(http.StatusOK))
			}

			By("requesting normal deletion and waiting for the stored User to disappear")
			_, err := users.Delete(ctx, privatev1.UsersDeleteRequest_builder{Id: originalID}.Build())
			Expect(err).ToNot(HaveOccurred())
			Eventually(ctx, func() codes.Code {
				_, err := users.Get(ctx, privatev1.UsersGetRequest_builder{Id: originalID}.Build())
				return status.Code(err)
			}, time.Minute, 200*time.Millisecond).Should(Equal(codes.NotFound))
			code, _, err := tool.KeycloakAdminRequest(ctx, http.MethodGet, "/users/"+cachedID, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(code).To(Equal(http.StatusNotFound))

			By("checking the replacement and second tenant through both real APIs")
			for id, keycloakID := range preserved {
				response, err := users.Get(ctx, privatev1.UsersGetRequest_builder{Id: id}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse())
				Expect(response.GetObject().GetStatus().GetKeycloakUserId()).To(Equal(keycloakID))
				code, body, err := tool.KeycloakAdminRequest(ctx, http.MethodGet, "/users/"+keycloakID, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(code).To(Equal(http.StatusOK))
				var account struct {
					ID string `json:"id"`
				}
				Expect(json.Unmarshal(body, &account)).To(Succeed())
				Expect(account.ID).To(Equal(keycloakID), fmt.Sprintf("identity for User %s must survive", id))
			}
		},
		Entry("initially present account", false),
		Entry("already-absent account with a same-username replacement", true),
	)
})
