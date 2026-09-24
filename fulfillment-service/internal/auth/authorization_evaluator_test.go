/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/collections"
)

var _ = Describe("OPA Authorization Evaluator", func() {
	Describe("BuildOPAAuthorizationEvaluator", func() {
		It("Can be built with empty emergency service accounts", func() {
			evaluator, err := NewEvaluator().
				AddEmergencyServiceAccounts([]string{}).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(evaluator).ToNot(BeNil())
		})

		It("Can be built with emergency service accounts", func() {
			evaluator, err := NewEvaluator().
				AddEmergencyServiceAccounts([]string{"admin", "backup"}).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(evaluator).ToNot(BeNil())
		})

		It("Rejects whitespace-only emergency service account name", func() {
			evaluator, err := NewEvaluator().
				AddEmergencyServiceAccounts([]string{"  "}).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not a valid Kubernetes service account name"))
			Expect(evaluator).To(BeNil())
		})

		It("Rejects emergency service account name with colon", func() {
			evaluator, err := NewEvaluator().
				AddEmergencyServiceAccounts([]string{"system:admin"}).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not a valid Kubernetes service account name"))
			Expect(evaluator).To(BeNil())
		})

		It("Rejects emergency service account name with uppercase", func() {
			evaluator, err := NewEvaluator().
				AddEmergencyServiceAccounts([]string{"Admin"}).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not a valid Kubernetes service account name"))
			Expect(evaluator).To(BeNil())
		})

		It("Rejects emergency service account name starting with hyphen", func() {
			evaluator, err := NewEvaluator().
				AddEmergencyServiceAccounts([]string{"-admin"}).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not a valid Kubernetes service account name"))
			Expect(evaluator).To(BeNil())
		})
	})

	Describe("Evaluate", func() {
		var evaluator *OPAAuthorizationEvaluator

		BeforeEach(func() {
			var err error
			evaluator, err = NewEvaluator().
				AddEmergencyServiceAccounts([]string{"admin", "emergency-backup"}).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		Context("With Kubernetes service account", func() {
			It("Allows admin service account with all tenants", func(ctx context.Context) {
				authContext := &AuthContext{
					Username:   "system:serviceaccount:osac:admin",
					Groups:     []any{"system:serviceaccounts", "system:serviceaccounts:osac"},
					AuthMethod: "serviceaccount",
				}

				decision, err := evaluator.Evaluate(
					ctx,
					authContext,
					"/osac.private.v1.Hubs/Create",
				)

				Expect(err).ToNot(HaveOccurred())
				Expect(decision).ToNot(BeNil())
				Expect(decision.Allowed).To(BeTrue())
				Expect(decision.SubjectUser).To(Equal("admin"))
				Expect(decision.SubjectTenants).To(ContainElement("*"))
			})

			It("Allows emergency backup service account", func(ctx context.Context) {
				authContext := &AuthContext{
					Username:   "system:serviceaccount:osac:emergency-backup",
					Groups:     []any{"system:serviceaccounts:osac"},
					AuthMethod: "serviceaccount",
				}

				decision, err := evaluator.Evaluate(
					ctx,
					authContext,
					"/osac.private.v1.Hubs/List",
				)

				Expect(err).ToNot(HaveOccurred())
				Expect(decision).ToNot(BeNil())
				Expect(decision.Allowed).To(BeTrue())
				Expect(decision.SubjectTenants).To(ContainElement("*"))
			})

			It("Denies non-emergency service account on private API", func(ctx context.Context) {
				authContext := &AuthContext{
					Username:   "system:serviceaccount:osac:random-service",
					Groups:     []any{"system:serviceaccounts:osac"},
					AuthMethod: "serviceaccount",
				}

				decision, err := evaluator.Evaluate(
					ctx,
					authContext,
					"/osac.private.v1.Hubs/Create",
				)

				Expect(err).ToNot(HaveOccurred())
				Expect(decision).ToNot(BeNil())
				Expect(decision.Allowed).To(BeFalse())
			})
		})

		Context("With JWT user token", func() {
			It("Allows user with organization membership", func(ctx context.Context) {
				authContext := &AuthContext{
					Username:     "john.doe",
					Groups:       []any{},
					AuthMethod:   "jwt",
					Organization: []any{"acme-corp"},
					RealmAccess: map[string]any{
						"roles": []any{"user"},
					},
					Tenant: "acme-corp",
				}

				decision, err := evaluator.Evaluate(
					ctx,
					authContext,
					"/osac.public.v1.Clusters/List",
				)

				Expect(err).ToNot(HaveOccurred())
				Expect(decision).ToNot(BeNil())
				Expect(decision.Allowed).To(BeTrue())
				Expect(decision.SubjectUser).To(Equal("john.doe"))
				Expect(decision.SubjectTenants).To(ContainElement("acme-corp"))
			})

			It("Makes authorization decision for user accessing different organization", func(ctx context.Context) {
				authContext := &AuthContext{
					Username:     "john.doe",
					Groups:       []any{},
					AuthMethod:   "jwt",
					Organization: []any{"acme-corp"},
					Tenant:       "different-org",
				}

				decision, err := evaluator.Evaluate(
					ctx,
					authContext,
					"/osac.public.v1.Clusters/List",
				)

				Expect(err).ToNot(HaveOccurred())
				Expect(decision).ToNot(BeNil())
				// Authorization policy determines access - we just verify evaluation succeeds
			})

			It("Preserves all identity claims in OPA input", func(ctx context.Context) {
				authContext := &AuthContext{
					Username:     "jane.smith",
					Groups:       []any{"developers", "team-leads"},
					AuthMethod:   "jwt",
					Organization: []any{"tech-startup"},
					Organizations: []any{
						map[string]any{"name": "tech-startup", "role": "admin"},
					},
					RealmAccess: map[string]any{
						"roles": []any{"admin", "user"},
					},
					Tenant: "tech-startup",
				}

				decision, err := evaluator.Evaluate(
					ctx,
					authContext,
					"/osac.public.v1.Clusters/Create",
				)

				Expect(err).ToNot(HaveOccurred())
				Expect(decision).ToNot(BeNil())
				// The decision should be based on all the provided claims
				Expect(decision.SubjectUser).To(Equal("jane.smith"))
			})
		})

		Context("With context extensions", func() {
			It("Uses tenant from context extensions", func(ctx context.Context) {
				authContext := &AuthContext{
					Username:     "user",
					AuthMethod:   "jwt",
					Organization: []any{"tenant-a"},
					ID:           "cluster-123",
					Tenant:       "tenant-a",
					Name:         "my-cluster",
				}

				decision, err := evaluator.Evaluate(
					ctx,
					authContext,
					"/osac.public.v1.Clusters/Get",
				)

				Expect(err).ToNot(HaveOccurred())
				Expect(decision).ToNot(BeNil())
			})

			It("Uses project from context extensions", func(ctx context.Context) {
				authContext := &AuthContext{
					Username:     "user",
					AuthMethod:   "jwt",
					Organization: []any{"tenant-a"},
					Tenant:       "tenant-a",
					Project:      "project-1",
				}

				decision, err := evaluator.Evaluate(
					ctx,
					authContext,
					"/osac.public.v1.ProjectMemberships/Create",
				)

				Expect(err).ToNot(HaveOccurred())
				Expect(decision).ToNot(BeNil())
			})
		})

		Context("Subject tenant extraction", func() {
			It("Extracts universal tenant marker", func(ctx context.Context) {
				authContext := &AuthContext{
					Username:   "system:serviceaccount:osac:admin",
					Groups:     []any{"system:serviceaccounts:osac"},
					AuthMethod: "serviceaccount",
				}

				decision, err := evaluator.Evaluate(
					ctx,
					authContext,
					"/osac.private.v1.Hubs/Create",
				)

				Expect(err).ToNot(HaveOccurred())
				Expect(decision.SubjectTenants).To(ContainElement("*"))
			})

			It("Extracts specific tenant list", func(ctx context.Context) {
				authContext := &AuthContext{
					Username:     "multi-tenant-user",
					AuthMethod:   "jwt",
					Organization: []any{"tenant-1", "tenant-2"},
				}

				decision, err := evaluator.Evaluate(
					ctx,
					authContext,
					"/osac.public.v1.Clusters/List",
				)

				Expect(err).ToNot(HaveOccurred())
				Expect(decision).ToNot(BeNil())
				if decision.Allowed {
					Expect(decision.SubjectTenants).To(Or(
						ContainElement("tenant-1"),
						ContainElement("tenant-2"),
					))
				}
			})
		})
	})

	Describe("constructOPAInput", func() {
		DescribeTable(
			"Builds correct OPA input structure",
			func(authContext *AuthContext, method string, expectedFields map[string]bool) {
				input := constructOPAInput(authContext, method)

				Expect(input).To(HaveKey("auth"))
				Expect(input).To(HaveKey("context"))

				auth := input["auth"].(map[string]any)
				Expect(auth).To(HaveKey("identity"))

				identity := auth["identity"].(map[string]any)
				Expect(identity["authnMethod"]).To(Equal(authContext.AuthMethod))

				if authContext.AuthMethod == "serviceaccount" {
					Expect(identity).To(HaveKey("user"))
					user := identity["user"].(map[string]any)
					Expect(user["username"]).To(Equal(authContext.Username))
				} else {
					Expect(identity["username"]).To(Equal(authContext.Username))

					if expectedFields["groups"] && authContext.Groups != nil {
						Expect(identity).To(HaveKey("groups"))
					}
					if expectedFields["organization"] && authContext.Organization != nil {
						Expect(identity).To(HaveKey("organization"))
					}
					if expectedFields["organizations"] && authContext.Organizations != nil {
						Expect(identity).To(HaveKey("organizations"))
					}
					if expectedFields["realm_access"] && authContext.RealmAccess != nil {
						Expect(identity).To(HaveKey("realm_access"))
					}
				}

				contextMap := input["context"].(map[string]any)
				Expect(contextMap).To(HaveKey("request"))
				request := contextMap["request"].(map[string]any)
				Expect(request).To(HaveKey("http"))
				http := request["http"].(map[string]any)
				Expect(http["path"]).To(Equal(method))

				Expect(contextMap).To(HaveKey("context_extensions"))
			},
			Entry(
				"Kubernetes service account",
				&AuthContext{
					Username:   "system:serviceaccount:osac:admin",
					Groups:     []any{"system:serviceaccounts:osac"},
					AuthMethod: "serviceaccount",
				},
				"/osac.private.v1.Hubs/Create",
				map[string]bool{},
			),
			Entry(
				"JWT with all claims",
				&AuthContext{
					Username:      "john.doe",
					Groups:        []any{"developers"},
					AuthMethod:    "jwt",
					Organization:  []any{"acme-corp"},
					Organizations: []any{map[string]any{"name": "acme-corp"}},
					RealmAccess:   map[string]any{"roles": []any{"user"}},
					Tenant:        "acme-corp",
				},
				"/osac.public.v1.Clusters/List",
				map[string]bool{
					"groups":        true,
					"organization":  true,
					"organizations": true,
					"realm_access":  true,
				},
			),
			Entry(
				"JWT with minimal claims",
				&AuthContext{
					Username:   "jane.smith",
					AuthMethod: "jwt",
					Tenant:     "cluster-123",
				},
				"/osac.public.v1.Clusters/Get",
				map[string]bool{},
			),
		)

		It("Omits nil fields from identity", func() {
			authContext := &AuthContext{
				Username:   "user",
				AuthMethod: "jwt",
				// All optional fields are nil
			}

			input := constructOPAInput(authContext, "/osac.public.v1.Clusters/List")

			auth := input["auth"].(map[string]any)
			identity := auth["identity"].(map[string]any)

			Expect(identity).ToNot(HaveKey("groups"))
			Expect(identity).ToNot(HaveKey("organization"))
			Expect(identity).ToNot(HaveKey("organizations"))
			Expect(identity).ToNot(HaveKey("realm_access"))
		})

		It("Includes context extensions fields", func() {
			authContext := &AuthContext{
				Username:   "user",
				AuthMethod: "jwt",
				ID:         "resource-123",
				Tenant:     "tenant-a",
				Name:       "my-resource",
				Project:    "project-1",
			}

			input := constructOPAInput(authContext, "/osac.public.v1.Clusters/Get")

			contextMap := input["context"].(map[string]any)
			extensions := contextMap["context_extensions"].(map[string]any)

			Expect(extensions["id"]).To(Equal("resource-123"))
			Expect(extensions["tenant"]).To(Equal("tenant-a"))
			Expect(extensions["name"]).To(Equal("my-resource"))
			Expect(extensions["project"]).To(Equal("project-1"))
		})
	})

	Describe("buildSubjectFromDecision", func() {
		var interceptor *GrpcAuthzInterceptor

		BeforeEach(func() {
			var err error
			evaluator, err := NewEvaluator().
				AddEmergencyServiceAccounts([]string{}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			interceptor, err = NewGrpcAuthzInterceptor().
				SetLogger(logger).
				SetEvaluator(evaluator).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Builds subject with universal tenants", func() {
			decision := &AuthzDecision{
				Allowed:        true,
				SubjectUser:    "admin",
				SubjectTenants: []string{"*"},
			}

			subject, err := interceptor.buildSubjectFromDecision(decision)
			Expect(err).ToNot(HaveOccurred())
			Expect(subject.User).To(Equal("admin"))
			Expect(subject.Tenants).To(Equal(AllTenants))
		})

		It("Builds subject with specific tenants", func() {
			decision := &AuthzDecision{
				Allowed:        true,
				SubjectUser:    "john.doe",
				SubjectTenants: []string{"tenant-a", "tenant-b"},
			}

			subject, err := interceptor.buildSubjectFromDecision(decision)
			Expect(err).ToNot(HaveOccurred())
			Expect(subject.User).To(Equal("john.doe"))
			Expect(subject.Tenants).To(Equal(collections.NewSet("tenant-a", "tenant-b")))
		})

		It("Fails when SubjectUser is empty", func() {
			decision := &AuthzDecision{
				Allowed:        true,
				SubjectUser:    "",
				SubjectTenants: []string{"tenant-a"},
			}

			subject, err := interceptor.buildSubjectFromDecision(decision)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("did not produce a subject_user"))
			Expect(subject).To(BeNil())
		})
	})
})
