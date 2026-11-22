/*
Copyright (c) 2026 Red Hat, Inc.

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
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("RoleBinding subject filters", func() {
	It("filters repeated references before pagination and preserves tenant isolation", func(ctx context.Context) {
		By("accepting a standard CEL exists filter through the public API")
		_, err := publicv1.NewRoleBindingsClient(tool.ExternalView().AdminConn()).List(ctx,
			publicv1.RoleBindingsListRequest_builder{
				Filter: new(`this.spec.users.exists(u, u.id == 'no-such-user')`),
			}.Build())
		Expect(err).ToNot(HaveOccurred())

		privateBindings := privatev1.NewRoleBindingsClient(tool.InternalView().AdminConn())
		users := privatev1.NewUsersClient(tool.InternalView().AdminConn())
		username := "filter-" + uuid.New()[24:]
		userID, keycloakID := createRoleBindingTestUser(ctx, users, username,
			privatev1.Metadata_builder{Name: username, Tenant: usersGroup}.Build())
		Expect(tool.ensureUserInOrg(ctx, username, usersGroup)).To(Succeed())
		assignKeycloakRealmRole(ctx, keycloakID, getOrCreateKeycloakTenantAdminRole(ctx))
		identity, err := metadataIdentityForExistingUser(ctx, username, usersPassword, usersGroup)
		Expect(err).ToNot(HaveOccurred())
		bindings := publicv1.NewRoleBindingsClient(identity.Conn)

		accounts := publicv1.NewServiceAccountsClient(identity.Conn)
		account, err := accounts.Create(ctx, serviceAccountRequest("", "filter-"+uuid.New()[24:]))
		Expect(err).ToNot(HaveOccurred())
		accountID := account.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) { deleteServiceAccount(ctx, accounts, accountID) })
		Eventually(ctx, func(g Gomega) {
			response, err := accounts.Get(ctx, publicv1.ServiceAccountsGetRequest_builder{Id: accountID}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(publicv1.ServiceAccountState_SERVICE_ACCOUNT_STATE_READY))
		}, 2*time.Minute, time.Second).Should(Succeed())

		createBinding := func(tenant string, spec *privatev1.RoleBindingSpec) string {
			GinkgoHelper()
			response, err := privateBindings.Create(ctx, privatev1.RoleBindingsCreateRequest_builder{
				Object: privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{Name: "filter-" + uuid.New()[24:], Tenant: tenant}.Build(),
					Spec:     spec,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := response.GetObject().GetId()
			DeferCleanup(func(ctx context.Context) {
				_, err := privateBindings.Delete(ctx, privatev1.RoleBindingsDeleteRequest_builder{Id: id}.Build())
				Expect(status.Code(err)).To(BeElementOf(codes.OK, codes.NotFound))
				Eventually(ctx, func(g Gomega) bool {
					_, err := privateBindings.Get(ctx, privatev1.RoleBindingsGetRequest_builder{Id: id}.Build())
					if status.Code(err) == codes.NotFound {
						return true
					}
					g.Expect(err).ToNot(HaveOccurred())
					_, err = privateBindings.Signal(ctx, privatev1.RoleBindingsSignalRequest_builder{Id: id}.Build())
					g.Expect(status.Code(err)).To(BeElementOf(codes.OK, codes.NotFound))
					return false
				}, time.Minute, time.Second).Should(BeTrue())
			})
			return id
		}
		userSpec := privatev1.RoleBindingSpec_builder{
			Role:  privatev1.RoleReference_builder{Name: "tenant-automation"}.Build(),
			Users: []*privatev1.UserReference{privatev1.UserReference_builder{Id: userID}.Build()},
		}.Build()
		first := createBinding(identity.TenantID, userSpec)
		second := createBinding(identity.TenantID, userSpec)
		accountBinding := createBinding(identity.TenantID, privatev1.RoleBindingSpec_builder{
			Role:            privatev1.RoleReference_builder{Name: "tenant-automation"}.Build(),
			ServiceAccounts: []*privatev1.ServiceAccountReference{privatev1.ServiceAccountReference_builder{Id: accountID}.Build()},
		}.Build())
		empty := createBinding(identity.TenantID, privatev1.RoleBindingSpec_builder{Role: privatev1.RoleReference_builder{Name: "tenant-automation"}.Build()}.Build())
		// An unrelated tenant's valid subject must match the broad predicate below,
		// but remain outside the public caller's visibility.
		foreignUserID, _ := createRoleBindingTestUser(ctx, users, "filter-foreign-"+uuid.New()[24:],
			privatev1.Metadata_builder{Name: "filter-foreign-" + uuid.New()[24:], Tenant: "engineering"}.Build())
		foreign := createBinding("engineering", privatev1.RoleBindingSpec_builder{
			Role:  privatev1.RoleReference_builder{Name: "tenant-automation"}.Build(),
			Users: []*privatev1.UserReference{privatev1.UserReference_builder{Id: foreignUserID}.Build()},
		}.Build())
		ids := fmt.Sprintf(`this.id in [%q, %q, %q, %q, %q]`, first, second, accountBinding, empty, foreign)
		By("matching user references with correct totals, pages, and count-only requests")
		userFilter := fmt.Sprintf(`%s && this.spec.users.exists(u, u.id == %q)`, ids, userID)
		list, err := bindings.List(ctx, publicv1.RoleBindingsListRequest_builder{Filter: &userFilter}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(list.GetTotal()).To(Equal(int32(2)))
		Expect(list.GetItems()).To(HaveLen(2))

		page, err := bindings.List(ctx, publicv1.RoleBindingsListRequest_builder{Filter: &userFilter, Limit: new(int32(1)), Offset: new(int32(1))}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(page.GetTotal()).To(Equal(int32(2)))
		Expect(page.GetItems()).To(HaveLen(1))
		count, err := bindings.List(ctx, publicv1.RoleBindingsListRequest_builder{Filter: &userFilter, Limit: new(int32(0))}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(count.GetTotal()).To(Equal(int32(2)))
		Expect(count.GetItems()).To(BeEmpty())

		By("matching service-account references and excluding nonmatches")
		accountFilter := fmt.Sprintf(`%s && this.spec.service_accounts.exists(sa, sa.id == %q)`, ids, accountID)
		list, err = bindings.List(ctx, publicv1.RoleBindingsListRequest_builder{Filter: &accountFilter}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(list.GetItems()).To(HaveLen(1))
		Expect(list.GetItems()[0].GetId()).To(Equal(accountBinding))
		missing := ids + ` && this.spec.users.exists(u, u.id == 'no-such-user')`
		list, err = bindings.List(ctx, publicv1.RoleBindingsListRequest_builder{Filter: &missing}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(list.GetTotal()).To(BeZero())

		By("excluding an otherwise matching binding in another tenant")
		visible := ids + ` && (this.spec.users.exists(u, true) || this.spec.service_accounts.exists(sa, true))`
		adminVisible, err := privateBindings.List(ctx, privatev1.RoleBindingsListRequest_builder{Filter: &visible}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(adminVisible.GetTotal()).To(Equal(int32(4)))
		var adminIDs []string
		for _, item := range adminVisible.GetItems() {
			adminIDs = append(adminIDs, item.GetId())
		}
		Expect(adminIDs).To(ContainElement(foreign))
		list, err = bindings.List(ctx, publicv1.RoleBindingsListRequest_builder{Filter: &visible}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(list.GetTotal()).To(Equal(int32(3)))
		for _, item := range list.GetItems() {
			Expect(item.GetMetadata().GetTenant()).To(Equal(identity.TenantID))
			Expect(item.GetId()).ToNot(Equal(foreign))
		}

		By("rejecting private fields and reserved metadata even for count-only filters")
		for _, filter := range []string{
			`this.spec.users.exists(u, this.status.users.size() > 0)`,
			`this.spec.users.exists(u, this.metadata.labels['admin.osac.openshift.io/managed'] == 'true')`,
		} {
			_, err := bindings.List(ctx, publicv1.RoleBindingsListRequest_builder{Filter: &filter, Limit: new(int32(0))}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		}

		By("preserving the same filter across the REST gateway")
		request, err := http.NewRequestWithContext(ctx, http.MethodGet,
			"https://"+externalServiceAddr+"/api/fulfillment/v1/role_bindings?"+url.Values{"filter": {accountFilter}}.Encode(), nil)
		Expect(err).ToNot(HaveOccurred())
		response, err := identity.HTTPClient.Do(request)
		Expect(err).ToNot(HaveOccurred())
		defer response.Body.Close()
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		var body struct {
			Total int32 `json:"total"`
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		Expect(json.NewDecoder(response.Body).Decode(&body)).To(Succeed())
		Expect(body.Total).To(Equal(int32(1)))
		Expect(body.Items).To(HaveLen(1))
		Expect(body.Items[0].ID).To(Equal(accountBinding))
	})
})
