/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package user

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/apiclient"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/idp"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// This contract test uses a disposable Keycloak realm. Fulfillment persistence
// is a recording double; it does not establish database or cascade coverage.
var _ = Describe("User deletion Keycloak contract", Label("keycloak-contract"), func() {
	It("completes cached-ID deletion while preserving replacement and other tenant accounts", func() {
		baseURL := os.Getenv("OSAC_TEST_KEYCLOAK_URL")
		if baseURL == "" {
			Skip("requires a disposable Keycloak; see docs/INTEGRATION-TESTING.md")
		}
		realm := os.Getenv("OSAC_TEST_KEYCLOAK_REALM")
		Expect(realm).ToNot(BeEmpty())
		tokenBytes, err := os.ReadFile(os.Getenv("OSAC_TEST_KEYCLOAK_TOKEN_FILE"))
		Expect(err).ToNot(HaveOccurred())
		token := strings.TrimSpace(string(tokenBytes))
		Expect(token).ToNot(BeEmpty())
		source, err := auth.NewStaticTokenSource().SetLogger(slog.Default()).SetToken(&auth.Token{Access: token}).Build()
		Expect(err).ToNot(HaveOccurred())
		client, err := idp.NewClient().SetLogger(slog.Default()).SetBaseURL(baseURL).
			SetRealmName(realm).SetTokenSource(source).Build()
		Expect(err).ToNot(HaveOccurred())
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		DeferCleanup(cancel)
		suffix := uuid.NewString()
		tenants := []string{"delete-a-" + suffix, "delete-b-" + suffix}
		for _, tenant := range tenants {
			_, err := client.CreateTenant(ctx, &idp.Tenant{Name: tenant, Enabled: true, Domains: []string{tenant + ".example.test"}})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() {
				Expect(client.DeleteTenant(context.Background(), tenant)).To(Succeed())
			})
		}
		createUser := func(tenant, username string) *idp.User {
			user, err := client.CreateUser(ctx, tenant, &idp.User{Username: username, Email: username + "@" + tenant + ".example.test", Enabled: true})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() {
				Expect(client.DeleteUser(context.Background(), tenant, user.ID)).To(Succeed())
			})
			return user
		}
		original := createUser(tenants[0], "original-"+suffix)
		unaffected := createUser(tenants[1], "unaffected-"+suffix)
		users := &deletionUsersClient{}
		reconciler := deletionFunction(baseURL, realm, token, users)
		deleting := deletingUser(tenants[0], original.ID)
		deleting.GetSpec().SetUsername(original.Username)

		By("deleting an initially present account through the reconciler")
		Expect(reconciler.Run(ctx, proto.Clone(deleting).(*privatev1.User))).To(Succeed())
		expectFinalizerUpdate(users)
		_, err = client.GetUser(ctx, tenants[0], original.ID)
		Expect(err).To(HaveOccurred())
		var apiErr *apiclient.APIError
		Expect(errors.As(err, &apiErr)).To(BeTrue())
		Expect(apiErr.StatusCode).To(Equal(http.StatusNotFound))

		By("replaying the cached-ID deletion after its account is already absent")
		replacement := createUser(tenants[0], original.Username)
		Expect(replacement.ID).ToNot(Equal(original.ID))
		users.updates = nil
		Expect(reconciler.Run(ctx, proto.Clone(deleting).(*privatev1.User))).To(Succeed())
		expectFinalizerUpdate(users)
		for _, remaining := range []struct{ tenant, id string }{
			{tenants[0], replacement.ID}, {tenants[1], unaffected.ID},
		} {
			user, err := client.GetUser(ctx, remaining.tenant, remaining.id)
			Expect(err).ToNot(HaveOccurred())
			Expect(user.ID).To(Equal(remaining.id))
		}

		By("retaining the finalizer when the configured realm does not exist")
		users.updates = nil
		wrongRealm := deletionFunction(baseURL, "missing-"+suffix, token, users)
		retry := proto.Clone(deleting).(*privatev1.User)
		Expect(wrongRealm.Run(ctx, retry)).ToNot(Succeed())
		Expect(users.updates).To(BeEmpty())
		Expect(retry.GetMetadata().GetFinalizers()).To(Equal(deleting.GetMetadata().GetFinalizers()))
	})
})
