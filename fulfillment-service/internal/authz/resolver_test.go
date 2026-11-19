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
	"iter"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/osac-project/osac/fulfillment-service/internal/authz/catalog"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type stubStore struct {
	grants     []BindingGrant
	err        error
	calls      []string
	closed     bool
	afterYield func()
}

func (s *stubStore) ListUserGrants(_ context.Context, tenant, username string) iter.Seq2[BindingGrant, error] {
	s.calls = append(s.calls, "user:"+tenant+":"+username)
	return s.stream()
}

func (s *stubStore) ListServiceAccountGrants(_ context.Context, tenant, id string) iter.Seq2[BindingGrant, error] {
	s.calls = append(s.calls, "service:"+tenant+":"+id)
	return s.stream()
}

func (s *stubStore) stream() iter.Seq2[BindingGrant, error] {
	return func(yield func(BindingGrant, error) bool) {
		defer func() { s.closed = true }()
		for _, grant := range s.grants {
			if !yield(grant, nil) {
				return
			}
			if s.afterYield != nil {
				s.afterYield()
			}
		}
		if s.err != nil {
			yield(BindingGrant{}, s.err)
		}
	}
}

var _ = Describe("Resolver", func() {
	var store *stubStore
	var resolver *Resolver
	var alice Subject
	var permissionCatalog *catalog.Catalog

	rule := func(service string, methods ...string) []*privatev1.PermissionRule {
		return []*privatev1.PermissionRule{privatev1.PermissionRule_builder{Service: service, Methods: methods}.Build()}
	}

	BeforeEach(func() {
		var err error
		permissionCatalog, err = catalog.Build(protoregistry.GlobalFiles)
		Expect(err).ToNot(HaveOccurred())
		store = &stubStore{grants: []BindingGrant{
			{BindingID: "b1", RoleID: "read", RoleName: "reader", RoleTenant: "shared",
				Rules: rule("osac.public.clusters", "list", "get", "get")},
			{BindingID: "b2", RoleID: "read", RoleName: "reader", RoleTenant: "shared",
				Rules: rule("osac.public.clusters", "list", "get")},
		}}
		resolver, err = NewResolver(store, permissionCatalog)
		Expect(err).ToNot(HaveOccurred())
		alice = Subject{Username: "alice", Tenants: collections.NewSet("tenant-a")}
	})

	It("requires a store and catalog", func() {
		_, err := NewResolver(nil, permissionCatalog)
		Expect(err).To(HaveOccurred())
		_, err = NewResolver(store, nil)
		Expect(err).To(HaveOccurred())
	})

	DescribeTable("rejects invalid subjects without reading the store", func(tenant, username, id string) {
		grants, err := resolver.Resolve(context.Background(), tenant, Subject{
			Username: username, ServiceAccountID: id, Tenants: collections.NewSet("tenant-a"),
		})
		Expect(err).To(HaveOccurred())
		Expect(grants).To(BeNil())
		Expect(store.calls).To(BeEmpty())
	},
		Entry("empty tenant", "", "alice", ""),
		Entry("unverified tenant", "tenant-b", "alice", ""),
		Entry("unverified service tenant", "tenant-b", "", "account"),
		Entry("neither subject", "tenant-a", "", ""),
		Entry("both subjects", "tenant-a", "alice", "account"),
	)

	It("dispatches users and preserves provenance with sorted unique methods", func() {
		grants, err := resolver.Resolve(context.Background(), "tenant-a", alice)
		Expect(err).ToNot(HaveOccurred())
		Expect(store.calls).To(Equal([]string{"user:tenant-a:alice"}))
		Expect(grants.Tenant).To(Equal("tenant-a"))
		Expect(grants.Bindings).To(Equal(store.grants))
		Expect(grants.Methods).To(Equal([]string{"osac.public.v1.Clusters/Get", "osac.public.v1.Clusters/List"}))
		Expect(store.closed).To(BeTrue())
	})

	It("unions wildcard permissions from distinct Roles", func() {
		store.grants[1].RoleID = "all"
		store.grants[1].Rules = rule("osac.public.clusters", "*")
		grants, err := resolver.Resolve(context.Background(), "tenant-a", alice)
		Expect(err).ToNot(HaveOccurred())
		Expect(grants.Methods).To(ContainElements("osac.public.v1.Clusters/Get", "osac.public.v1.Clusters/Create"))
	})

	It("dispatches service accounts", func() {
		_, err := resolver.Resolve(context.Background(), "tenant-a", Subject{
			ServiceAccountID: "account", Tenants: alice.Tenants,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(store.calls).To(Equal([]string{"service:tenant-a:account"}))
	})

	It("expands a Role once per call and observes edits on the next call", func() {
		// An invalid second copy would fail if the same Role were expanded twice.
		store.grants[1].Rules = rule("unknown.service", "get")
		_, err := resolver.Resolve(context.Background(), "tenant-a", alice)
		Expect(err).ToNot(HaveOccurred())
		store.grants[0].Rules = rule("osac.public.clusters", "create")
		grants, err := resolver.Resolve(context.Background(), "tenant-a", alice)
		Expect(err).ToNot(HaveOccurred())
		Expect(grants.Methods).To(Equal([]string{"osac.public.v1.Clusters/Create"}))
	})

	It("discards all grants after a stream error", func() {
		store.err = errors.New("database failure")
		grants, err := resolver.Resolve(context.Background(), "tenant-a", alice)
		Expect(err).To(MatchError(store.err))
		Expect(grants).To(BeNil())
		Expect(store.closed).To(BeTrue())
	})

	It("discards grants and stops the stream on expansion failure", func() {
		store.grants[1].RoleID = "invalid"
		store.grants[1].Rules = rule("unknown.service", "get")
		grants, err := resolver.Resolve(context.Background(), "tenant-a", alice)
		Expect(err).To(MatchError(ContainSubstring(`expand Role "invalid"`)))
		Expect(grants).To(BeNil())
		Expect(store.closed).To(BeTrue())
	})

	It("checks cancellation before dispatch", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		grants, err := resolver.Resolve(ctx, "tenant-a", alice)
		Expect(err).To(MatchError(context.Canceled))
		Expect(grants).To(BeNil())
		Expect(store.calls).To(BeEmpty())
	})

	It("discards grants when canceled during streaming", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		store.afterYield = cancel
		grants, err := resolver.Resolve(ctx, "tenant-a", alice)
		Expect(err).To(MatchError(context.Canceled))
		Expect(grants).To(BeNil())
		Expect(store.closed).To(BeTrue())
	})

	It("checks cancellation after the final row", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		store.grants = store.grants[:1]
		store.afterYield = cancel
		grants, err := resolver.Resolve(ctx, "tenant-a", alice)
		Expect(err).To(MatchError(context.Canceled))
		Expect(grants).To(BeNil())
	})
})
