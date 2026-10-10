/*
Copyright (c) 2025 Red Hat Inc.

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
	"encoding/json"
	"time"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

var _ = Describe("Default tenancy logic", Ordered, func() {
	var (
		server *database.Container
	)

	// withDb is for the AroundNode decorator. It starts a shared database container on first use, then gives each
	// test a fresh instance, pool, transaction manager, and a transaction in the context.
	withDb := func(ctx context.Context, body func(ctx context.Context)) {
		var err error

		// Start the database server if needed. This is shared by all the tests.
		if server == nil {
			server, err = database.NewContainer().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
			err = server.Start(ctx)
			Expect(err).ToNot(HaveOccurred())
		}

		// Create the database instance, database connection pool and transaction manager:
		db, err := server.NewInstance().
			Build()
		Expect(err).ToNot(HaveOccurred())
		defer func() {
			err := db.Close(ctx)
			Expect(err).ToNot(HaveOccurred())
		}()
		pool, err := db.Pool(ctx)
		Expect(err).ToNot(HaveOccurred())
		defer func() {
			pool.Close()
		}()
		tm, err := database.NewTxManager().
			SetLogger(logger).
			SetPool(pool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create a new transaction and add it to the context:
		tx, err := tm.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		defer func() {
			err := tx.End(ctx)
			Expect(err).ToNot(HaveOccurred())
		}()
		ctx = database.TxIntoContext(ctx, tx)
		body(ctx)
	}

	AfterAll(func(ctx context.Context) {
		// Stop the database server:
		if server != nil {
			err := server.Stop(ctx)
			Expect(err).ToNot(HaveOccurred())
		}
	})

	Describe("Creation", func() {
		It("Fails if logger is not set", func() {
			logic, err := NewDefaultTenancyLogic().
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger is mandatory"))
			Expect(logic).To(BeNil())
		})
	})

	Describe("Determine assignable tenants", func() {
		var logic *DefaultTenancyLogic

		BeforeEach(func(ctx context.Context) {
			var err error
			logic, err = NewDefaultTenancyLogic().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Returns the tenants from the subject", func(ctx context.Context) {
			subject := &Subject{
				User:    "my_user",
				Tenants: collections.NewSet("tenant-a", "tenant-b"),
			}
			ctx = ContextWithSubject(ctx, subject)
			result, err := logic.DetermineAssignableTenants(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Equal(collections.NewSet("tenant-a", "tenant-b"))).To(BeTrue())
		})

		It("Returns multiple tenants when present", func(ctx context.Context) {
			subject := &Subject{
				User:    "my_user",
				Tenants: collections.NewSet("tenant-a", "tenant-b", "tenant-c"),
			}
			ctx = ContextWithSubject(ctx, subject)
			result, err := logic.DetermineAssignableTenants(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Equal(collections.NewSet("tenant-a", "tenant-b", "tenant-c"))).To(BeTrue())
		})

		It("Returns universal set when tenants is universal", func(ctx context.Context) {
			subject := &Subject{
				User:    "my_user",
				Tenants: AllTenants,
			}
			ctx = ContextWithSubject(ctx, subject)
			result, err := logic.DetermineAssignableTenants(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Equal(AllTenants)).To(BeTrue())
		})

		It("Fails if the subject has an empty tenants set", func(ctx context.Context) {
			subject := &Subject{
				User:    "my_user",
				Tenants: collections.NewSet[string](),
			}
			ctx = ContextWithSubject(ctx, subject)
			_, err := logic.DetermineAssignableTenants(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least one tenant"))
		})

		It("Returns an empty set when there is an error", func(ctx context.Context) {
			subject := &Subject{
				User: "my_user",
			}
			ctx = ContextWithSubject(ctx, subject)
			result, err := logic.DetermineAssignableTenants(ctx)
			Expect(err).To(HaveOccurred())
			Expect(result.Empty()).To(BeTrue())
		})
	})

	Describe("Determine default tenant", func() {
		var logic *DefaultTenancyLogic

		BeforeEach(func(ctx context.Context) {
			var err error
			logic, err = NewDefaultTenancyLogic().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Returns a tenant from the subject", func(ctx context.Context) {
			subject := &Subject{
				User:    "my_user",
				Tenants: collections.NewSet("tenant-a", "tenant-b"),
			}
			ctx = ContextWithSubject(ctx, subject)
			result, err := logic.DetermineDefaultTenant(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(BeElementOf("tenant-a", "tenant-b"))
		})

		It("Returns shared when tenants is universal", func(ctx context.Context) {
			subject := &Subject{
				User:    "my_user",
				Tenants: AllTenants,
			}
			ctx = ContextWithSubject(ctx, subject)
			result, err := logic.DetermineDefaultTenant(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(SharedTenant))
		})

		It("Fails if the subject has an empty tenants set", func(ctx context.Context) {
			subject := &Subject{
				User:    "my_user",
				Tenants: collections.NewSet[string](),
			}
			ctx = ContextWithSubject(ctx, subject)
			_, err := logic.DetermineDefaultTenant(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least one tenant"))
		})
	})

	Describe("Determine visibility", func() {
		var logic *DefaultTenancyLogic

		BeforeEach(func(ctx context.Context) {
			var err error
			logic, err = NewDefaultTenancyLogic().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Returns total visibility when the subject has universal tenants", func(ctx context.Context) {
			subject := &Subject{
				User:    "my_admin",
				Tenants: AllTenants,
			}
			ctx = ContextWithSubject(ctx, subject)
			result, err := logic.DetermineVisibility(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.IsTotal()).To(BeTrue())
		})

		It("Total visibility grants access to any tenant and project", func(ctx context.Context) {
			subject := &Subject{
				User:    "my_admin",
				Tenants: AllTenants,
			}
			ctx = ContextWithSubject(ctx, subject)
			result, err := logic.DetermineVisibility(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.IsTenantVisible("any-tenant")).To(BeTrue())
			Expect(result.IsProjectVisible("any-tenant", "any-project")).To(BeTrue())
		})

		It("Total visibility does not require a database transaction", func(ctx context.Context) {
			subject := &Subject{
				User:    "my_admin",
				Tenants: AllTenants,
			}
			ctx = ContextWithSubject(ctx, subject)
			result, err := logic.DetermineVisibility(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.IsTotal()).To(BeTrue())
		})

		It("Fails when there is no transaction in the context", func(ctx context.Context) {
			subject := &Subject{
				User:    "my_user",
				Tenants: collections.NewSet("tenant-a"),
			}
			ctx = ContextWithSubject(ctx, subject)
			_, err := logic.DetermineVisibility(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("transaction"))
		})

		Describe("With a database", AroundNode(withDb), func() {
			// createTenant inserts a tenant row. The database trigger automatically creates the default project
			// for the tenant.
			createTenant := func(ctx context.Context, name string) {
				tx, err := database.TxFromContext(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(
					ctx,
					`
					insert into tenants (
						id,
						tenant,
						name,
						data
					)
					values (
						$1,
						$2,
						$3,
						'{}'
					)`,
					name, name, name,
				)
				Expect(err).ToNot(HaveOccurred())
			}

			// createProject inserts a project under the tenant's default project.
			createProject := func(ctx context.Context, tenant, name string) {
				tx, err := database.TxFromContext(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(
					ctx,
					`
					insert into projects (
						id,
						tenant,
						project,
						name,
						creator,
						data
					)
					values (
						$1,
						$2,
						'',
						$3,
						'system',
						'{}'
					)`,
					tenant+"-"+name, tenant, name,
				)
				Expect(err).ToNot(HaveOccurred())
			}

			// createMembership inserts a project_membership row. The database trigger automatically populates
			// the project_membership_subjects helper table.
			createMembership := func(ctx context.Context, id, tenantName, project, user string) {
				tx, err := database.TxFromContext(ctx)
				Expect(err).ToNot(HaveOccurred())
				dataMap := map[string]any{
					"spec": map[string]any{
						"role":  "PROJECT_MEMBERSHIP_ROLE_VIEWER",
						"users": []string{user},
					},
				}
				dataJson, err := json.Marshal(dataMap)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(
					ctx,
					`
					insert into project_memberships (
						id,
						name,
						creator,
						tenant,
						project,
						data
					)
					values (
						$1,
						$2,
						'system',
						$3,
						$4,
						$5
					)`,
					id, id, tenantName, project, dataJson,
				)
				Expect(err).ToNot(HaveOccurred())
			}

			It("Includes the shared tenant with the default project", func(ctx context.Context) {
				subject := &Subject{
					User:    "my_user",
					Tenants: collections.NewSet("tenant-a"),
				}
				ctx = ContextWithSubject(ctx, subject)
				result, err := logic.DetermineVisibility(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.IsTotal()).To(BeFalse())
				Expect(result.IsTenantVisible(SharedTenant)).To(BeTrue())
				Expect(result.IsProjectVisible(SharedTenant, "")).To(BeTrue())
				Expect(result.VisibleTenants()).To(Equal([]string{SharedTenant, "tenant-a"}))
			})

			It("Does not grant access to non-default projects of the shared tenant", func(ctx context.Context) {
				subject := &Subject{
					User:    "my_user",
					Tenants: collections.NewSet("tenant-a"),
				}
				ctx = ContextWithSubject(ctx, subject)
				result, err := logic.DetermineVisibility(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.IsProjectVisible(SharedTenant, "alpha")).To(BeFalse())
			})

			It("Includes project memberships from the database", func(ctx context.Context) {
				createTenant(ctx, "tenant-a")
				createTenant(ctx, "tenant-b")
				createProject(ctx, "tenant-a", "alpha")
				createProject(ctx, "tenant-b", "beta")
				createMembership(ctx, "pm-1", "tenant-a", "alpha", "my_user")
				createMembership(ctx, "pm-2", "tenant-b", "beta", "my_user")
				subject := &Subject{
					User:    "my_user",
					Tenants: collections.NewSet("tenant-a", "tenant-b"),
				}
				ctx = ContextWithSubject(ctx, subject)
				result, err := logic.DetermineVisibility(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.IsProjectVisible("tenant-a", "alpha")).To(BeTrue())
				Expect(result.IsProjectVisible("tenant-b", "beta")).To(BeTrue())
				Expect(result.VisibleTenants()).To(Equal([]string{SharedTenant, "tenant-a", "tenant-b"}))
			})

			It("Does not grant access to projects that have no membership", func(ctx context.Context) {
				createTenant(ctx, "tenant-a")
				createProject(ctx, "tenant-a", "alpha")
				createMembership(ctx, "pm-1", "tenant-a", "alpha", "my_user")
				subject := &Subject{
					User:    "my_user",
					Tenants: collections.NewSet("tenant-a"),
				}
				ctx = ContextWithSubject(ctx, subject)
				result, err := logic.DetermineVisibility(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.IsProjectVisible("tenant-a", "beta")).To(BeFalse())
				Expect(result.IsTenantVisible("tenant-b")).To(BeFalse())
				Expect(result.IsProjectVisible("tenant-b", "alpha")).To(BeFalse())
			})

			It("Does not include memberships belonging to other users", func(ctx context.Context) {
				createTenant(ctx, "tenant-a")
				createProject(ctx, "tenant-a", "alpha")
				createMembership(ctx, "pm-1", "tenant-a", "alpha", "other_user")
				subject := &Subject{
					User:    "my_user",
					Tenants: collections.NewSet("tenant-a"),
				}
				ctx = ContextWithSubject(ctx, subject)
				result, err := logic.DetermineVisibility(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.IsTenantVisible("tenant-a")).To(BeTrue())
				Expect(result.IsProjectVisible("tenant-a", "alpha")).To(BeFalse())
			})

			It("Includes all tenants from the subject even without project memberships", func(ctx context.Context) {
				createTenant(ctx, "tenant-a")
				createTenant(ctx, "tenant-b")
				createProject(ctx, "tenant-a", "alpha")
				createMembership(ctx, "pm-1", "tenant-a", "alpha", "my_user")
				subject := &Subject{
					User:    "my_user",
					Tenants: collections.NewSet("tenant-a", "tenant-b"),
				}
				ctx = ContextWithSubject(ctx, subject)
				result, err := logic.DetermineVisibility(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.IsTenantVisible("tenant-a")).To(BeTrue())
				Expect(result.IsTenantVisible("tenant-b")).To(BeTrue())
				Expect(result.IsProjectVisible("tenant-a", "alpha")).To(BeTrue())
				Expect(result.IsProjectVisible("tenant-a", "")).To(BeTrue())
				Expect(result.IsProjectVisible("tenant-b", "")).To(BeTrue())
				Expect(result.IsProjectVisible("tenant-b", "alpha")).To(BeFalse())
				Expect(result.VisibleTenants()).To(Equal([]string{SharedTenant, "tenant-a", "tenant-b"}))
			})

			It("Grants visibility of descendant projects", func(ctx context.Context) {
				createTenant(ctx, "tenant-a")
				createProject(ctx, "tenant-a", "parent")
				createMembership(ctx, "pm-1", "tenant-a", "parent", "my_user")
				subject := &Subject{
					User:    "my_user",
					Tenants: collections.NewSet("tenant-a"),
				}
				ctx = ContextWithSubject(ctx, subject)
				result, err := logic.DetermineVisibility(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.IsProjectVisible("tenant-a", "parent")).To(BeTrue())
				Expect(result.IsProjectVisible("tenant-a", "parent.child")).To(BeTrue())
				Expect(result.IsProjectVisible("tenant-a", "other")).To(BeFalse())
			})

			It("Includes projects from Keycloak groups in JWT token", func(ctx context.Context) {
				// Create a JWT token with organization claim containing project groups
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
					"preferred_username": "my_user",
					"organization": map[string]any{
						"tenant-a": map[string]any{
							"groups": []any{
								"/my-project/system:managers",
								"/other-project/system:viewers",
							},
						},
					},
					"exp": time.Now().Add(time.Hour).Unix(),
				})

				subject := &Subject{
					User:    "my_user",
					Tenants: collections.NewSet("tenant-a"),
				}
				ctx = ContextWithSubject(ctx, subject)
				ctx = ContextWithToken(ctx, token)

				result, err := logic.DetermineVisibility(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.IsProjectVisible("tenant-a", "my-project")).To(BeTrue())
				Expect(result.IsProjectVisible("tenant-a", "other-project")).To(BeTrue())
			})

			It("Includes nested projects from Keycloak groups", func(ctx context.Context) {
				// Create a JWT token with nested project groups (slash notation)
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
					"preferred_username": "my_user",
					"organization": map[string]any{
						"tenant-a": map[string]any{
							"groups": []any{
								"/parent-project/system:managers",
								"/parent-project/child-project/system:viewers",
							},
						},
					},
					"exp": time.Now().Add(time.Hour).Unix(),
				})

				subject := &Subject{
					User:    "my_user",
					Tenants: collections.NewSet("tenant-a"),
				}
				ctx = ContextWithSubject(ctx, subject)
				ctx = ContextWithToken(ctx, token)

				result, err := logic.DetermineVisibility(ctx)
				Expect(err).ToNot(HaveOccurred())
				// Parent project should be visible
				Expect(result.IsProjectVisible("tenant-a", "parent-project")).To(BeTrue())
				// Nested project should be converted from slash to dot notation
				Expect(result.IsProjectVisible("tenant-a", "parent-project.child-project")).To(BeTrue())
			})

			It("Combines Keycloak groups and ProjectMembership objects", func(ctx context.Context) {
				createTenant(ctx, "tenant-a")
				createProject(ctx, "tenant-a", "db-project")
				createMembership(ctx, "pm-1", "tenant-a", "db-project", "my_user")

				// Create a JWT token with additional project groups
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
					"preferred_username": "my_user",
					"organization": map[string]any{
						"tenant-a": map[string]any{
							"groups": []any{
								"/keycloak-project/system:managers",
							},
						},
					},
					"exp": time.Now().Add(time.Hour).Unix(),
				})

				subject := &Subject{
					User:    "my_user",
					Tenants: collections.NewSet("tenant-a"),
				}
				ctx = ContextWithSubject(ctx, subject)
				ctx = ContextWithToken(ctx, token)

				result, err := logic.DetermineVisibility(ctx)
				Expect(err).ToNot(HaveOccurred())
				// Both sources should be visible
				Expect(result.IsProjectVisible("tenant-a", "db-project")).To(BeTrue())
				Expect(result.IsProjectVisible("tenant-a", "keycloak-project")).To(BeTrue())
			})

			It("Handles multiple tenants in Keycloak groups", func(ctx context.Context) {
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
					"preferred_username": "my_user",
					"organization": map[string]any{
						"tenant-a": map[string]any{
							"groups": []any{
								"/project-a/system:managers",
							},
						},
						"tenant-b": map[string]any{
							"groups": []any{
								"/project-b/system:viewers",
							},
						},
					},
					"exp": time.Now().Add(time.Hour).Unix(),
				})

				subject := &Subject{
					User:    "my_user",
					Tenants: collections.NewSet("tenant-a", "tenant-b"),
				}
				ctx = ContextWithSubject(ctx, subject)
				ctx = ContextWithToken(ctx, token)

				result, err := logic.DetermineVisibility(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(result.IsProjectVisible("tenant-a", "project-a")).To(BeTrue())
				Expect(result.IsProjectVisible("tenant-b", "project-b")).To(BeTrue())
			})

			It("Ignores malformed Keycloak groups", func(ctx context.Context) {
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
					"preferred_username": "my_user",
					"organization": map[string]any{
						"tenant-a": map[string]any{
							"groups": []any{
								"invalid-group-format",           // No leading slash
								"/",                              // Too short
								"/project",                       // Missing role suffix
								"/valid-project/system:managers", // Valid
							},
						},
					},
					"exp": time.Now().Add(time.Hour).Unix(),
				})

				subject := &Subject{
					User:    "my_user",
					Tenants: collections.NewSet("tenant-a"),
				}
				ctx = ContextWithSubject(ctx, subject)
				ctx = ContextWithToken(ctx, token)

				result, err := logic.DetermineVisibility(ctx)
				Expect(err).ToNot(HaveOccurred())
				// Only the valid group should result in a visible project
				Expect(result.IsProjectVisible("tenant-a", "valid-project")).To(BeTrue())
			})

			It("Works without a JWT token in context", func(ctx context.Context) {
				createTenant(ctx, "tenant-a")
				createProject(ctx, "tenant-a", "alpha")
				createMembership(ctx, "pm-1", "tenant-a", "alpha", "my_user")

				subject := &Subject{
					User:    "my_user",
					Tenants: collections.NewSet("tenant-a"),
				}
				ctx = ContextWithSubject(ctx, subject)
				// No token in context

				result, err := logic.DetermineVisibility(ctx)
				Expect(err).ToNot(HaveOccurred())
				// Should still work with database memberships only
				Expect(result.IsProjectVisible("tenant-a", "alpha")).To(BeTrue())
			})

			It("Handles JWT token with malformed organization claim", func(ctx context.Context) {
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
					"preferred_username": "my_user",
					"organization":       "invalid-string-instead-of-object",
					"exp":                time.Now().Add(time.Hour).Unix(),
				})

				subject := &Subject{
					User:    "my_user",
					Tenants: collections.NewSet("tenant-a"),
				}
				ctx = ContextWithSubject(ctx, subject)
				ctx = ContextWithToken(ctx, token)

				result, err := logic.DetermineVisibility(ctx)
				Expect(err).ToNot(HaveOccurred())
				// Should still succeed, just without Keycloak group projects
				Expect(result.IsTenantVisible("tenant-a")).To(BeTrue())
			})

			It("Grants descendant visibility for Keycloak group projects", func(ctx context.Context) {
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
					"preferred_username": "my_user",
					"organization": map[string]any{
						"tenant-a": map[string]any{
							"groups": []any{
								"/parent/system:managers",
							},
						},
					},
					"exp": time.Now().Add(time.Hour).Unix(),
				})

				subject := &Subject{
					User:    "my_user",
					Tenants: collections.NewSet("tenant-a"),
				}
				ctx = ContextWithSubject(ctx, subject)
				ctx = ContextWithToken(ctx, token)

				result, err := logic.DetermineVisibility(ctx)
				Expect(err).ToNot(HaveOccurred())
				// Parent project and descendants should be visible
				Expect(result.IsProjectVisible("tenant-a", "parent")).To(BeTrue())
				Expect(result.IsProjectVisible("tenant-a", "parent.child")).To(BeTrue())
				Expect(result.IsProjectVisible("tenant-a", "parent.child.grandchild")).To(BeTrue())
				// Unrelated projects should not be visible
				Expect(result.IsProjectVisible("tenant-a", "other")).To(BeFalse())
			})

			It("Does not grant visibility to top-level projects without explicit membership", func(ctx context.Context) {
				createTenant(ctx, "tenant-a")
				createProject(ctx, "tenant-a", "alpha")
				createProject(ctx, "tenant-a", "beta")

				// User with no Keycloak groups and no ProjectMemberships
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
					"preferred_username": "my_user",
					"organization": map[string]any{
						"tenant-a": map[string]any{
							"groups": []any{},
						},
					},
					"exp": time.Now().Add(time.Hour).Unix(),
				})

				subject := &Subject{
					User:    "my_user",
					Tenants: collections.NewSet("tenant-a"),
				}
				ctx = ContextWithSubject(ctx, subject)
				ctx = ContextWithToken(ctx, token)

				result, err := logic.DetermineVisibility(ctx)
				Expect(err).ToNot(HaveOccurred())
				// User should only see the default project, not top-level projects
				Expect(result.IsProjectVisible("tenant-a", "")).To(BeTrue())
				Expect(result.IsProjectVisible("tenant-a", "alpha")).To(BeFalse())
				Expect(result.IsProjectVisible("tenant-a", "beta")).To(BeFalse())
			})
		})
	})
})
