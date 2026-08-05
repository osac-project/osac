/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package idp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/fulfillment-service/internal/auth"
)

// staticTokenSource is a trivial TokenSource for tests.
type staticTokenSource struct{}

func (s *staticTokenSource) Token(_ context.Context) (*auth.Token, error) {
	return &auth.Token{Access: "test-token"}, nil
}

func (s *staticTokenSource) Invalidate(_ context.Context) error {
	return nil
}

var _ = Describe("Client conflict handling", func() {
	var (
		ctx    context.Context
		server *httptest.Server
		client *Client
		mux    *http.ServeMux
	)

	BeforeEach(func() {
		ctx = context.Background()
		mux = http.NewServeMux()
		server = httptest.NewServer(mux)

		var err error
		client, err = NewClient().
			SetLogger(logger).
			SetBaseURL(server.URL).
			SetTokenSource(&staticTokenSource{}).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		server.Close()
	})

	Describe("CreateTenant", func() {
		It("returns the existing tenant on 409 conflict", func() {
			mux.HandleFunc("POST /admin/realms/osac/organizations", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]string{
					"errorMessage": "A organization with the same name already exists.",
				})
			})
			mux.HandleFunc("GET /admin/realms/osac/organizations", func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Query().Get("search")).To(Equal("shared"))
				Expect(r.URL.Query().Get("exact")).To(Equal("true"))
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode([]keycloakOrganization{
					{ID: "org-123", Name: "shared"},
				})
			})

			tenant, err := client.CreateTenant(ctx, &Tenant{Name: "shared"})
			Expect(err).ToNot(HaveOccurred())
			Expect(tenant).ToNot(BeNil())
			Expect(tenant.ID).To(Equal("org-123"))
			Expect(tenant.Name).To(Equal("shared"))
		})

		It("returns an error for non-conflict failures", func() {
			mux.HandleFunc("POST /admin/realms/osac/organizations", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			})

			tenant, err := client.CreateTenant(ctx, &Tenant{Name: "shared"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to create organization"))
			Expect(tenant).To(BeNil())
		})
	})

	Describe("CreateUserInRealm", func() {
		It("returns the existing user on 409 conflict", func() {
			mux.HandleFunc("POST /admin/realms/osac/users", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]string{
					"errorMessage": "User exists with same username",
				})
			})
			mux.HandleFunc("GET /admin/realms/osac/users", func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Query().Get("username")).To(Equal("shared-osac-break-glass"))
				Expect(r.URL.Query().Get("exact")).To(Equal("true"))
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode([]keycloakUser{
					{ID: "user-456", Username: "shared-osac-break-glass", Email: "break-glass@shared.osac.local"},
				})
			})

			user, err := client.CreateUserInRealm(ctx, &User{
				Username: "shared-osac-break-glass",
				Email:    "break-glass@shared.osac.local",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(user).ToNot(BeNil())
			Expect(user.ID).To(Equal("user-456"))
			Expect(user.Username).To(Equal("shared-osac-break-glass"))
		})

		It("returns an error when user exists but lookup fails", func() {
			mux.HandleFunc("POST /admin/realms/osac/users", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]string{
					"errorMessage": "User exists with same username",
				})
			})
			mux.HandleFunc("GET /admin/realms/osac/users", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			})

			user, err := client.CreateUserInRealm(ctx, &User{Username: "shared-osac-break-glass"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("already exists but failed to look up"))
			Expect(user).To(BeNil())
		})
	})

	Describe("AddUserToOrganization", func() {
		It("succeeds on 409 conflict (user already a member)", func() {
			mux.HandleFunc("GET /admin/realms/osac/organizations", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode([]keycloakOrganization{
					{ID: "org-123", Name: "shared"},
				})
			})
			mux.HandleFunc("POST /admin/realms/osac/organizations/org-123/members", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
			})

			err := client.AddUserToOrganization(ctx, "shared", "user-456")
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns an error for non-conflict failures", func() {
			mux.HandleFunc("GET /admin/realms/osac/organizations", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode([]keycloakOrganization{
					{ID: "org-123", Name: "shared"},
				})
			})
			mux.HandleFunc("POST /admin/realms/osac/organizations/org-123/members", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			})

			err := client.AddUserToOrganization(ctx, "shared", "user-456")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to add user to organization"))
		})
	})

	Describe("AddUserToGroup", func() {
		It("succeeds on 409 conflict (user already member of group)", func() {
			mux.HandleFunc("GET /admin/realms/osac/organizations", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode([]keycloakOrganization{
					{ID: "org-123", Name: "shared"},
				})
			})
			mux.HandleFunc("POST /admin/realms/osac/organizations/org-123/members", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
			})
			mux.HandleFunc("PUT /admin/realms/osac/organizations/org-123/groups/group-456/members/user-789", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
			})

			err := client.AddUserToGroup(ctx, "shared", "user-789", "group-456")
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns an error for non-conflict failures", func() {
			mux.HandleFunc("GET /admin/realms/osac/organizations", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode([]keycloakOrganization{
					{ID: "org-123", Name: "shared"},
				})
			})
			mux.HandleFunc("POST /admin/realms/osac/organizations/org-123/members", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
			})
			mux.HandleFunc("PUT /admin/realms/osac/organizations/org-123/groups/group-456/members/user-789", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			})

			err := client.AddUserToGroup(ctx, "shared", "user-789", "group-456")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to add user to organization group"))
		})
	})

	Describe("CreateTenant end-to-end race simulation", func() {
		It("succeeds when all IdP resources already exist from a concurrent create", func() {
			// Simulate the race: another goroutine already created the org, user,
			// added user to org, and assigned roles — all return 409 or succeed.
			orgCreated := false
			mux.HandleFunc("POST /admin/realms/osac/organizations", func(w http.ResponseWriter, r *http.Request) {
				if !orgCreated {
					orgCreated = true
					w.WriteHeader(http.StatusConflict)
					json.NewEncoder(w).Encode(map[string]string{
						"errorMessage": "A organization with the same name already exists.",
					})
					return
				}
				w.WriteHeader(http.StatusCreated)
			})
			mux.HandleFunc("GET /admin/realms/osac/organizations", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode([]keycloakOrganization{
					{ID: "org-123", Name: "shared"},
				})
			})
			mux.HandleFunc("POST /admin/realms/osac/users", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]string{
					"errorMessage": "User exists with same username",
				})
			})
			mux.HandleFunc("GET /admin/realms/osac/users", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode([]keycloakUser{
					{ID: "user-456", Username: "shared-osac-break-glass", Email: "break-glass@shared.osac.local"},
				})
			})
			mux.HandleFunc("POST /admin/realms/osac/organizations/org-123/members", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
			})
			mux.HandleFunc("GET /admin/realms/osac/roles/", func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/tenant-idp-manager") {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(keycloakRole{ID: "role-1", Name: "tenant-idp-manager"})
					return
				}
				w.WriteHeader(http.StatusNotFound)
			})
			mux.HandleFunc("POST /admin/realms/osac/users/user-456/role-mappings/realm", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})

			tenantManager, err := NewTenantManager().
				SetLogger(logger).
				SetClient(client).
				Build()
			Expect(err).ToNot(HaveOccurred())

			config := &TenantConfig{
				Name:               "shared",
				Enabled:            new(true),
				BreakGlassPassword: "temp-password",
			}

			credentials, err := tenantManager.CreateTenant(ctx, config)
			Expect(err).ToNot(HaveOccurred())
			Expect(credentials).ToNot(BeNil())
			Expect(credentials.UserID).To(Equal("user-456"))
			Expect(credentials.Username).To(Equal("shared-osac-break-glass"))
		})
	})
})
