/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// This file contains tests for the functions that extract authentication information from
// contexts.

package auth

import (
	"context"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Subject inside context", func() {
	It("Returns the same subject that was added", func() {
		subject := &Subject{}
		ctx := ContextWithSubject(context.Background(), subject)
		extracted := SubjectFromContext(ctx)
		Expect(extracted).To(BeIdenticalTo(subject))
	})

	It("Panics if there is no subject", func() {
		ctx := context.Background()
		Expect(func() {
			SubjectFromContext(ctx)
		}).To(PanicWith("failed to get subject from context"))
	})

	Describe("controller service account detection", func() {
		It("recognizes the fulfillment controller identity", func() {
			ctx := ContextWithSubject(context.Background(), &Subject{User: "service-account-osac-controller"})
			Expect(IsControllerServiceAccount(ctx)).To(BeTrue())
		})

		It("recognizes a Kubernetes service-account subject", func() {
			ctx := ContextWithSubject(context.Background(), &Subject{
				User: "system:serviceaccount:osac:service-account-osac-controller",
			})
			Expect(IsControllerServiceAccount(ctx)).To(BeTrue())
		})

		It("recognizes the operator only when authenticated as its Kubernetes service account", func() {
			ctx := ContextWithSubject(context.Background(), &Subject{User: "osac-operator"})
			ctx = ContextWithAuthenticationMethod(ctx, "serviceaccount")
			Expect(IsServiceAccount(ctx, "osac-operator")).To(BeTrue())
		})

		It("does not treat a JWT user with the operator name as a service account", func() {
			ctx := ContextWithSubject(context.Background(), &Subject{User: "osac-operator"})
			ctx = ContextWithAuthenticationMethod(ctx, "jwt")
			Expect(IsServiceAccount(ctx, "osac-operator")).To(BeFalse())
		})

		It("does not grant controller lifecycle access to other callers", func() {
			Expect(IsControllerServiceAccount(context.Background())).To(BeFalse())
			ctx := ContextWithSubject(context.Background(), &Subject{User: "service-account-osac-admin"})
			Expect(IsControllerServiceAccount(ctx)).To(BeFalse())
		})
	})

	Describe("tenant admin detection", func() {
		It("recognizes the tenant-admin realm role", func() {
			token := &jwt.Token{Claims: jwt.MapClaims{
				"realm_access": map[string]any{"roles": []any{"tenant-admin"}},
			}}
			Expect(IsTenantAdmin(ContextWithToken(context.Background(), token))).To(BeTrue())
		})

		It("does not grant tenant-admin access to another role or missing token", func() {
			token := &jwt.Token{Claims: jwt.MapClaims{
				"realm_access": map[string]any{"roles": []any{"is_client"}},
			}}
			Expect(IsTenantAdmin(ContextWithToken(context.Background(), token))).To(BeFalse())
			Expect(IsTenantAdmin(context.Background())).To(BeFalse())
		})
	})
})

var _ = Describe("Managed service-account JWT claims", func() {
	It("recognizes a generated client ID with one tenant claim", func() {
		authContext, err := ExtractAuthContext(&jwt.Token{Claims: jwt.MapClaims{
			"azp":         "osac-sa-tenant-a-d3c4e3db-95b8-481d-8b5c-963da477da12",
			"osac_tenant": "tenant-a",
			"username":    "osac-sa-user",
		}})
		Expect(err).ToNot(HaveOccurred())
		Expect(authContext.ManagedServiceAccountCandidate).To(BeTrue())
		Expect(authContext.ManagedServiceAccountClaimsValid).To(BeTrue())
		Expect(authContext.ManagedServiceAccountClientID).To(Equal(
			"osac-sa-tenant-a-d3c4e3db-95b8-481d-8b5c-963da477da12"))
		Expect(authContext.ManagedServiceAccountTenantClaim).To(Equal("tenant-a"))
	})

	It("marks malformed managed claims as a candidate that must fail closed", func() {
		authContext, err := ExtractAuthContext(&jwt.Token{Claims: jwt.MapClaims{
			"azp":         "osac-sa-tenant-a-d3c4e3db-95b8-481d-8b5c-963da477da12",
			"osac_tenant": []any{"tenant-a", "tenant-b"},
		}})
		Expect(err).ToNot(HaveOccurred())
		Expect(authContext.ManagedServiceAccountCandidate).To(BeTrue())
		Expect(authContext.ManagedServiceAccountClaimsValid).To(BeFalse())
	})

	It("does not classify ordinary client tokens as managed service accounts", func() {
		authContext, err := ExtractAuthContext(&jwt.Token{Claims: jwt.MapClaims{
			"azp": "osac-web-client",
		}})
		Expect(err).ToNot(HaveOccurred())
		Expect(authContext.ManagedServiceAccountCandidate).To(BeFalse())
	})
})
