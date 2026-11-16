/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package authz

import (
	"context"
	"errors"
	"fmt"
	"slices"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/authz/catalog"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// stubStore is an in-memory Store. It ignores the tenant argument of ListBindings so that the
// resolver's own tenant filtering is exercised, and it records Role fetches and page reads.
type stubStore struct {
	bindings       []*privatev1.RoleBinding
	roles          map[string]*privatev1.Role
	users          map[string]*privatev1.User
	fetchedRoleIDs []string
	listCalls      int
	listErr        error
	failListAfter  int
	roleErr        error
	failRoleID     string
	userErr        error
}

func (s *stubStore) ListBindings(_ context.Context, _ string, offset, limit int32) ([]*privatev1.RoleBinding, error) {
	s.listCalls++
	if s.listErr != nil {
		return nil, s.listErr
	}
	if s.failListAfter > 0 && s.listCalls > s.failListAfter {
		return nil, errors.New("database failure")
	}
	if offset >= int32(len(s.bindings)) {
		return nil, nil
	}
	end := min(offset+limit, int32(len(s.bindings)))
	return s.bindings[offset:end], nil
}

func (s *stubStore) GetRole(_ context.Context, id string) (*privatev1.Role, error) {
	s.fetchedRoleIDs = append(s.fetchedRoleIDs, id)
	if s.roleErr != nil && (s.failRoleID == "" || s.failRoleID == id) {
		return nil, s.roleErr
	}
	if role := s.roles[id]; role != nil {
		return role, nil
	}
	return nil, &dao.ErrNotFound{IDs: []string{id}}
}

func (s *stubStore) GetUser(_ context.Context, id string) (*privatev1.User, error) {
	if s.userErr != nil {
		return nil, s.userErr
	}
	if user := s.users[id]; user != nil {
		return user, nil
	}
	return nil, &dao.ErrNotFound{IDs: []string{id}}
}

func (s *stubStore) GetUserByName(_ context.Context, tenant, name string) (*privatev1.User, error) {
	if s.userErr != nil {
		return nil, s.userErr
	}
	for _, user := range s.users {
		if user.GetMetadata().GetTenant() == tenant && user.GetMetadata().GetName() == name &&
			user.GetMetadata().GetDeletionTimestamp() == nil {
			return user, nil
		}
	}
	return nil, &dao.ErrNotFound{IDs: []string{name}}
}

// testRole creates a Role with one permission rule. The reference name recorded by testBinding is
// deliberately wrong so that tests prove resolution goes through the Role ID.
func testRole(tenant, id, name, service string, methods ...string) *privatev1.Role {
	return privatev1.Role_builder{Id: id,
		Metadata: privatev1.Metadata_builder{Name: name, Tenant: tenant}.Build(),
		Spec: privatev1.RoleSpec_builder{Permissions: []*privatev1.PermissionRule{
			privatev1.PermissionRule_builder{Service: service, Methods: methods}.Build(),
		}}.Build(),
	}.Build()
}

// testUser creates a user row with the given row name, tenant, and login username.
func testUser(tenant, name, username string) *privatev1.User {
	return privatev1.User_builder{
		Metadata: privatev1.Metadata_builder{Name: name, Tenant: tenant}.Build(),
		Spec:     privatev1.UserSpec_builder{Username: username}.Build(),
	}.Build()
}

func testBinding(id, tenant, roleID string, users []*privatev1.UserReference,
	accounts []*privatev1.ServiceAccountReference) *privatev1.RoleBinding {
	return privatev1.RoleBinding_builder{Id: id,
		Metadata: privatev1.Metadata_builder{Tenant: tenant}.Build(),
		Spec: privatev1.RoleBindingSpec_builder{
			Role:  privatev1.RoleReference_builder{Id: roleID, Name: "misleading-name"}.Build(),
			Users: users, ServiceAccounts: accounts,
		}.Build(),
	}.Build()
}

func userRef(id string) *privatev1.UserReference {
	return privatev1.UserReference_builder{Id: id}.Build()
}

func namedRef(name string) *privatev1.UserReference {
	return privatev1.UserReference_builder{Name: name}.Build()
}

func serviceRef(id string) *privatev1.ServiceAccountReference {
	return privatev1.ServiceAccountReference_builder{Id: id}.Build()
}

var permissionCatalog *catalog.Catalog

var _ = BeforeSuite(func() {
	var err error
	permissionCatalog, err = catalog.Build(protoregistry.GlobalFiles)
	Expect(err).ToNot(HaveOccurred())
})

var _ = Describe("Resolver", func() {
	var store *stubStore

	var alice = Subject{Username: "alice", Tenants: collections.NewSet("tenant-a")}

	resolve := func(tenant string, subject Subject) (*Grants, error) {
		resolver, err := NewResolver(store, permissionCatalog)
		Expect(err).ToNot(HaveOccurred())
		return resolver.Resolve(context.Background(), tenant, subject)
	}

	// seedUserGrants seeds the standard user fixture: alice matches b1, b2, and b6, the Role of
	// b4 is missing, b3 belongs to another tenant, and b5 only matches service accounts.
	seedUserGrants := func() {
		store.roles = map[string]*privatev1.Role{
			"read": testRole("shared", "read", "reader", "osac.public.clusters", "get"),
			"all":  testRole("shared", "all", "creator", "osac.public.clusters", "*"),
		}
		store.users = map[string]*privatev1.User{
			"user-id": testUser("tenant-a", "alice", "alice"),
		}
		store.bindings = []*privatev1.RoleBinding{
			testBinding("b1", "tenant-a", "read", []*privatev1.UserReference{userRef("user-id")}, nil),
			testBinding("b2", "tenant-a", "all", []*privatev1.UserReference{userRef("user-id")}, nil),
			testBinding("b3", "tenant-b", "read", []*privatev1.UserReference{userRef("user-id")}, nil),
			testBinding("b4", "tenant-a", "missing", []*privatev1.UserReference{userRef("user-id")}, nil),
			testBinding("b5", "tenant-a", "all", nil, []*privatev1.ServiceAccountReference{serviceRef("service-id")}),
			testBinding("b6", "tenant-a", "read", []*privatev1.UserReference{namedRef("alice")}, nil),
		}
	}

	BeforeEach(func() {
		store = &stubStore{roles: map[string]*privatev1.Role{}, users: map[string]*privatev1.User{}}
	})

	Describe("Subject and tenant validation", func() {
		DescribeTable("rejects the request without grants",
			func(tenant string, subject Subject) {
				grants, err := resolve(tenant, subject)
				Expect(err).To(HaveOccurred())
				Expect(grants).To(BeNil())
			},
			Entry("empty tenant", "", Subject{Username: "alice", Tenants: collections.NewSet("tenant-a")}),
			Entry("tenant outside the verified set", "tenant-b",
				Subject{Username: "alice", Tenants: collections.NewSet("tenant-a")}),
			Entry("tenant outside the verified set for a service account", "tenant-b",
				Subject{ServiceAccountID: "service-id", Tenants: collections.NewSet("tenant-a")}),
			Entry("no subject identifier", "tenant-a", Subject{Tenants: collections.NewSet("tenant-a")}),
			Entry("both subject identifiers", "tenant-a",
				Subject{Username: "alice", ServiceAccountID: "service-id", Tenants: collections.NewSet("tenant-a")}),
		)
	})

	Describe("Computing grants", func() {
		It("grants sorted methods from matching bindings with provenance", func() {
			seedUserGrants()
			grants, err := resolve("tenant-a", alice)
			Expect(err).ToNot(HaveOccurred())
			Expect(grants.Tenant).To(Equal("tenant-a"))
			Expect(grants.Bindings).To(HaveLen(3))
			Expect(grants.Methods).To(ContainElements(
				"osac.public.v1.Clusters/Get", "osac.public.v1.Clusters/Create"))
			Expect(slices.IsSorted(grants.Methods)).To(BeTrue())
			first := grants.Bindings[0]
			Expect(first.BindingID).To(Equal("b1"))
			Expect(first.BindingTenant).To(Equal("tenant-a"))
			Expect(first.RoleID).To(Equal("read"))
			Expect(first.RoleName).To(Equal("reader"))
			Expect(first.RoleTenant).To(Equal("shared"))
			Expect(grants.Bindings[2].BindingID).To(Equal("b6"))
		})

		It("fetches each distinct Role once per resolution", func() {
			seedUserGrants()
			_, err := resolve("tenant-a", alice)
			Expect(err).ToNot(HaveOccurred())
			Expect(store.fetchedRoleIDs).To(Equal([]string{"read", "all", "missing"}))
		})

		It("grants service accounts through matching references only", func() {
			seedUserGrants()
			grants, err := resolve("tenant-a", Subject{
				ServiceAccountID: "service-id", Tenants: collections.NewSet("tenant-a"),
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(grants.Bindings).To(HaveLen(1))
			Expect(grants.Bindings[0].BindingID).To(Equal("b5"))
			other, err := resolve("tenant-a", Subject{
				ServiceAccountID: "other", Tenants: collections.NewSet("tenant-a"),
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(other.Bindings).To(BeEmpty())
		})

		It("reflects Role edits on the next resolution", func() {
			seedUserGrants()
			_, err := resolve("tenant-a", alice)
			Expect(err).ToNot(HaveOccurred())
			store.roles["read"].GetSpec().SetPermissions([]*privatev1.PermissionRule{
				privatev1.PermissionRule_builder{Service: "osac.public.clusters", Methods: []string{"list"}}.Build(),
			})
			store.roles["all"].GetSpec().SetPermissions(nil)
			grants, err := resolve("tenant-a", alice)
			Expect(err).ToNot(HaveOccurred())
			Expect(grants.Methods).To(ContainElement("osac.public.v1.Clusters/List"))
			Expect(grants.Methods).ToNot(ContainElement("osac.public.v1.Clusters/Get"))
		})
	})

	Describe("Error handling", func() {
		DescribeTable("discards the result and returns the error",
			func(fail func()) {
				seedUserGrants()
				fail()
				grants, err := resolve("tenant-a", alice)
				Expect(err).To(HaveOccurred())
				Expect(grants).To(BeNil())
			},
			Entry("listing bindings fails", func() { store.listErr = errors.New("database failure") }),
			Entry("fetching a Role fails", func() {
				store.roleErr = errors.New("database failure")
				store.failRoleID = "missing"
			}),
			Entry("looking up a user fails", func() { store.userErr = errors.New("database failure") }),
		)

		It("wraps catalog expansion failures", func() {
			store.roles["bogus"] = privatev1.Role_builder{Id: "bogus",
				Metadata: privatev1.Metadata_builder{Name: "bogus", Tenant: "shared"}.Build(),
				Spec: privatev1.RoleSpec_builder{Permissions: []*privatev1.PermissionRule{
					privatev1.PermissionRule_builder{Service: "no.such.service", Methods: []string{"get"}}.Build(),
				}}.Build(),
			}.Build()
			store.users["user-id"] = testUser("tenant-a", "alice", "alice")
			store.bindings = []*privatev1.RoleBinding{
				testBinding("b1", "tenant-a", "bogus", []*privatev1.UserReference{userRef("user-id")}, nil),
			}
			grants, err := resolve("tenant-a", alice)
			Expect(err).To(MatchError(ContainSubstring(`expand Role "bogus"`)))
			Expect(grants).To(BeNil())
		})
	})

	Describe("Tenant isolation", func() {
		BeforeEach(func() {
			store.roles = map[string]*privatev1.Role{
				"read": testRole("shared", "read", "reader", "osac.public.clusters", "get"),
			}
			store.users = map[string]*privatev1.User{
				"a": testUser("tenant-a", "alice-row", "alice"),
				"b": testUser("tenant-b", "b-row", "alice"),
			}
			store.bindings = []*privatev1.RoleBinding{
				testBinding("foreign-binding", "tenant-b", "read", []*privatev1.UserReference{userRef("b")}, nil),
				testBinding("foreign-user", "tenant-a", "read", []*privatev1.UserReference{userRef("b")}, nil),
				testBinding("foreign-scope", "tenant-a", "read", []*privatev1.UserReference{
					privatev1.UserReference_builder{Name: "alice", Tenant: "tenant-b"}.Build(),
				}, nil),
				testBinding("shared-scope", "tenant-a", "read", []*privatev1.UserReference{
					privatev1.UserReference_builder{Name: "alice", Shared: true}.Build(),
				}, nil),
				testBinding("valid", "tenant-a", "read", []*privatev1.UserReference{userRef("a")}, nil),
			}
		})

		It("matches only users of the resolved tenant", func() {
			grants, err := resolve("tenant-a", alice)
			Expect(err).ToNot(HaveOccurred())
			Expect(grants.Bindings).To(HaveLen(1))
			Expect(grants.Bindings[0].BindingID).To(Equal("valid"))
		})

		It("ignores Roles from other tenants", func() {
			store.roles["foreign"] = testRole("tenant-b", "foreign", "foreign", "osac.public.clusters", "get")
			store.bindings = []*privatev1.RoleBinding{
				testBinding("foreign-role", "tenant-a", "foreign", []*privatev1.UserReference{userRef("a")}, nil),
			}
			grants, err := resolve("tenant-a", alice)
			Expect(err).ToNot(HaveOccurred())
			Expect(grants.Bindings).To(BeEmpty())
			Expect(grants.Methods).To(BeEmpty())
		})
	})

	Describe("Deletion", func() {
		// seedLiveGrant seeds one binding that grants alice the "read" Role.
		seedLiveGrant := func() {
			store.roles = map[string]*privatev1.Role{
				"read": testRole("shared", "read", "reader", "osac.public.clusters", "get"),
			}
			store.users = map[string]*privatev1.User{"a": testUser("tenant-a", "alice", "alice")}
			store.bindings = []*privatev1.RoleBinding{
				testBinding("live", "tenant-a", "read", []*privatev1.UserReference{userRef("a")}, nil),
			}
		}

		It("stops granting from deleted bindings", func() {
			seedLiveGrant()
			grants, err := resolve("tenant-a", alice)
			Expect(err).ToNot(HaveOccurred())
			Expect(grants.Bindings).To(HaveLen(1))
			store.bindings[0].GetMetadata().SetDeletionTimestamp(timestamppb.Now())
			grants, err = resolve("tenant-a", alice)
			Expect(err).ToNot(HaveOccurred())
			Expect(grants.Bindings).To(BeEmpty())
			Expect(grants.Methods).To(BeEmpty())
		})

		It("stops granting from deleted Roles", func() {
			seedLiveGrant()
			store.roles["read"].GetMetadata().SetDeletionTimestamp(timestamppb.Now())
			grants, err := resolve("tenant-a", alice)
			Expect(err).ToNot(HaveOccurred())
			Expect(grants.Bindings).To(BeEmpty())
		})

		It("stops granting from deleted users", func() {
			seedLiveGrant()
			store.users["a"].GetMetadata().SetDeletionTimestamp(timestamppb.Now())
			grants, err := resolve("tenant-a", alice)
			Expect(err).ToNot(HaveOccurred())
			Expect(grants.Bindings).To(BeEmpty())
		})
	})

	Describe("Name-only user references", func() {
		It("match an active tenant row", func() {
			store.roles["read"] = testRole("shared", "read", "reader", "osac.public.clusters", "get")
			store.users["alice-id"] = testUser("tenant-a", "alice-row", "alice")
			store.bindings = []*privatev1.RoleBinding{
				testBinding("name-only", "tenant-a", "read", []*privatev1.UserReference{namedRef("alice-row")}, nil),
			}
			grants, err := resolve("tenant-a", alice)
			Expect(err).ToNot(HaveOccurred())
			Expect(grants.Bindings).To(HaveLen(1))
		})

		It("do not match without an active row", func() {
			user := testUser("tenant-a", "alice-row", "alice")
			user.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
			store.roles["read"] = testRole("shared", "read", "reader", "osac.public.clusters", "get")
			store.users["alice-id"] = user
			store.bindings = []*privatev1.RoleBinding{
				testBinding("name-only", "tenant-a", "read", []*privatev1.UserReference{namedRef("alice-row")}, nil),
			}
			grants, err := resolve("tenant-a", alice)
			Expect(err).ToNot(HaveOccurred())
			Expect(grants.Bindings).To(BeEmpty())
		})
	})

	Describe("Shared tenant", func() {
		It("matches shared Users only in the shared tenant", func() {
			store.roles["read"] = testRole("shared", "read", "reader", "osac.public.clusters", "get")
			store.users["shared-user"] = testUser("shared", "alice-row", "alice")
			store.bindings = []*privatev1.RoleBinding{
				testBinding("tenant-binding", "tenant-a", "read", []*privatev1.UserReference{userRef("shared-user")}, nil),
				testBinding("shared-binding", "shared", "read", []*privatev1.UserReference{
					privatev1.UserReference_builder{Name: "alice-row", Shared: true}.Build(),
				}, nil),
			}
			subject := Subject{Username: "alice", Tenants: collections.NewSet("tenant-a", "shared")}
			tenantGrants, err := resolve("tenant-a", subject)
			Expect(err).ToNot(HaveOccurred())
			Expect(tenantGrants.Bindings).To(BeEmpty())
			sharedGrants, err := resolve("shared", subject)
			Expect(err).ToNot(HaveOccurred())
			Expect(sharedGrants.Bindings).To(HaveLen(1))
			Expect(sharedGrants.Bindings[0].BindingID).To(Equal("shared-binding"))
		})
	})

	Describe("Pagination", func() {
		// seedPages seeds count bindings that all grant alice the "read" Role.
		seedPages := func(count int) {
			store.roles["read"] = testRole("shared", "read", "reader", "osac.public.clusters", "get")
			store.users["user-id"] = testUser("tenant-a", "alice", "alice")
			for i := range count {
				store.bindings = append(store.bindings, testBinding(
					fmt.Sprintf("b%d", i), "tenant-a", "read",
					[]*privatev1.UserReference{userRef("user-id")}, nil))
			}
		}

		It("reads every page when bindings exceed the page size", func() {
			seedPages(int(pageSize)*2 + 50)
			grants, err := resolve("tenant-a", alice)
			Expect(err).ToNot(HaveOccurred())
			Expect(grants.Bindings).To(HaveLen(int(pageSize)*2 + 50))
			Expect(grants.Methods).To(ContainElement("osac.public.v1.Clusters/Get"))
			Expect(store.listCalls).To(Equal(3))
		})

		It("stops on the empty page after an exact multiple", func() {
			seedPages(int(pageSize) * 2)
			grants, err := resolve("tenant-a", alice)
			Expect(err).ToNot(HaveOccurred())
			Expect(grants.Bindings).To(HaveLen(int(pageSize) * 2))
			Expect(store.listCalls).To(Equal(3))
		})

		It("aborts when a later page fails", func() {
			seedPages(int(pageSize) + 50)
			store.failListAfter = 1
			grants, err := resolve("tenant-a", alice)
			Expect(err).To(HaveOccurred())
			Expect(grants).To(BeNil())
		})
	})
})
