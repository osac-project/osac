/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"time"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/provisioners"
	"github.com/osac-project/osac/fulfillment-service/internal/serviceaccounts"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// These are component tests: real PostgreSQL, OPA, authorization/JIT
// interceptors, default tenancy and public/private handlers. JWT signature
// validation and Keycloak cleanup are outside this boundary.
var _ = Describe("Deleted tenant authorization", func() {
	var logic *auth.DefaultTenancyLogic
	var evaluator *auth.OPAAuthorizationEvaluator
	var interceptor *auth.GrpcAuthzInterceptor
	var tenants *PrivateTenantsServer
	var accounts *ServiceAccountsServer
	const accountID = "d3c4e3db-95b8-481d-8b5c-963da477da12"

	BeforeEach(func() {
		var err error
		logic, err = auth.NewDefaultTenancyLogic().SetLogger(logger).
			SetTenantResolver(database.ResolveLiveTenantNames).Build()
		Expect(err).ToNot(HaveOccurred())
		evaluator, err = auth.NewEvaluator().SetTenantResolver(database.ResolveLiveTenantNames).
			SetManagedServiceAccountResolver(serviceaccounts.ResolveAuthorization).Build()
		Expect(err).ToNot(HaveOccurred())
		interceptor, err = auth.NewGrpcAuthzInterceptor().SetLogger(logger).SetEvaluator(evaluator).Build()
		Expect(err).ToNot(HaveOccurred())
		tenants, err = NewPrivateTenantsServer().SetLogger(logger).SetTenancyLogic(tenancy).
			SetAttributionLogic(attribution).Build()
		Expect(err).ToNot(HaveOccurred())
		accounts, err = NewServiceAccountsServer().SetLogger(logger).SetTenancyLogic(logic).
			SetAttributionLogic(attribution).Build()
		Expect(err).ToNot(HaveOccurred())
		seedAuthorizationTenant(ctx, "tenant-a")
		seedAuthorizationTenant(ctx, "tenant-b")
		seedAuthorizationBillingAssignment(ctx, "tenant-a")
		// Leave the account and credential fully live while deleting only the
		// tenant, proving that account/credential liveness alone isn't enough.
		_, err = suiteTx.Exec(ctx, `insert into secrets
			(id, name, tenant, annotations, data) values
			('credential', 'credential', 'tenant-a',
			'{"osac.openshift.io/tenant":"tenant-a","osac.openshift.io/owner-reference":"d3c4e3db-95b8-481d-8b5c-963da477da12"}', '{}')`)
		Expect(err).ToNot(HaveOccurred())
		_, err = suiteTx.Exec(ctx, `insert into service_accounts
			(id, name, tenant, annotations, data) values
			($1, 'robot', 'tenant-a', '{"osac.openshift.io/tenant":"tenant-a"}',
			'{"status":{"state":"SERVICE_ACCOUNT_STATE_READY","client_secret":{"id":"credential"},"keycloak_client_uuid":"00000000-0000-4000-8000-000000000001"}}')`, accountID)
		Expect(err).ToNot(HaveOccurred())
		_, err = suiteTx.Exec(ctx, `insert into role_bindings (id, name, tenant, data) values
			('grant', 'grant', 'tenant-a',
			'{"spec":{"role":{"name":"tenant-automation"},"service_accounts":[{"id":"d3c4e3db-95b8-481d-8b5c-963da477da12"}]}}')`)
		Expect(err).ToNot(HaveOccurred())
	})

	humanToken := func(names ...string) *jwt.Token {
		orgs := map[string]any{}
		for _, name := range names {
			orgs[name] = map[string]any{"groups": []any{"/project/system:managers"}}
		}
		return &jwt.Token{Claims: jwt.MapClaims{
			"preferred_username": "alice", "organization": orgs,
			"realm_access": map[string]any{"roles": []any{"tenant-admin"}},
			"exp":          time.Now().Add(time.Hour).Unix(),
		}}
	}
	deleteTenant := func() {
		_, err := tenants.Delete(ctx, privatev1.TenantsDeleteRequest_builder{Id: "tenant-a"}.Build())
		Expect(err).ToNot(HaveOccurred())
		var retained bool
		Expect(suiteTx.QueryRow(ctx, `select deletion_timestamp <> 'epoch' from tenants where name='tenant-a'`).Scan(&retained)).To(Succeed())
		Expect(retained).To(BeTrue())
		var closed bool
		Expect(suiteTx.QueryRow(ctx, `select effective_to is not null from tenant_billing_assignments where tenant_id='tenant-a'`).Scan(&closed)).To(Succeed())
		Expect(closed).To(BeTrue())
	}
	call := func(token *jwt.Token, method string, request any, handler grpc.UnaryHandler) (any, error) {
		return interceptor.UnaryServer(auth.ContextWithToken(ctx, token), request,
			&grpc.UnaryServerInfo{FullMethod: method}, handler)
	}

	It("removes stale human scope before JIT and rejects reads and writes while retaining shared access", func() {
		token := humanToken("tenant-a") // Reuse this same unexpired token after deletion.
		get := publicv1.ServiceAccountsGetRequest_builder{Id: accountID}.Build()
		_, err := call(token, "/osac.public.v1.ServiceAccounts/Get", get, func(c context.Context, _ any) (any, error) {
			return accounts.Get(c, get)
		})
		Expect(err).ToNot(HaveOccurred())
		deleteTenant()
		// A provisioner without expectations fails if stale claims reach JIT.
		jit, err := auth.NewGrpcJitProvisioningInterceptor().SetLogger(logger).
			SetProvisioner(auth.NewMockUserProvisioner(ctrl)).Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = call(token, "/osac.public.v1.ServiceAccounts/Get", get, func(c context.Context, _ any) (any, error) {
			Expect(auth.SubjectFromContext(c).Tenants.Empty()).To(BeTrue())
			return jit.UnaryServer(c, get, &grpc.UnaryServerInfo{}, func(c context.Context, _ any) (any, error) {
				return accounts.Get(c, get)
			})
		})
		Expect(status.Code(err)).To(Equal(codes.NotFound))
		update := publicv1.ServiceAccountsUpdateRequest_builder{
			Object: publicv1.ServiceAccount_builder{Id: accountID,
				Metadata: publicv1.Metadata_builder{Labels: map[string]string{"forbidden": "true"}}.Build()}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
		}.Build()
		_, err = call(token, "/osac.public.v1.ServiceAccounts/Update", update, func(c context.Context, _ any) (any, error) {
			return accounts.Update(c, update)
		})
		Expect(status.Code(err)).To(Equal(codes.NotFound))
		_, err = call(token, "/osac.public.v1.ServiceAccounts/List", &publicv1.ServiceAccountsListRequest{}, func(c context.Context, _ any) (any, error) {
			visibility, err := logic.DetermineVisibility(c)
			Expect(err).ToNot(HaveOccurred())
			Expect(visibility.IsTenantVisible(auth.SharedTenant)).To(BeTrue())
			Expect(visibility.IsProjectVisible("tenant-a", "project")).To(BeFalse())
			return accounts.List(c, &publicv1.ServiceAccountsListRequest{})
		})
		Expect(err).ToNot(HaveOccurred())
	})

	It("keeps other live memberships and cannot reintroduce deleted scope through JWT project groups", func() {
		deleteTenant()
		_, err := call(humanToken("tenant-a", "tenant-b"), "/osac.public.v1.ServiceAccounts/List", nil,
			func(c context.Context, _ any) (any, error) {
				Expect(auth.SubjectFromContext(c).Tenants.Inclusions()).To(ConsistOf("tenant-b"))
				assignable, err := logic.DetermineAssignableTenants(c)
				Expect(err).ToNot(HaveOccurred())
				Expect(assignable.Inclusions()).To(ConsistOf("tenant-b"))
				visibility, err := logic.DetermineVisibility(c)
				Expect(err).ToNot(HaveOccurred())
				Expect(visibility.IsProjectVisible("tenant-a", "project")).To(BeFalse())
				Expect(visibility.IsProjectVisible("tenant-b", "project")).To(BeTrue())
				Expect(visibility.IsTenantVisible(auth.SystemTenant)).To(BeFalse())
				return nil, nil
			})
		Expect(err).ToNot(HaveOccurred())
	})

	It("denies managed credentials as soon as their tenant is deleted", func() {
		clientID := serviceaccounts.ClientID("tenant-a", accountID)
		identity, err := serviceaccounts.ResolveAuthorization(ctx, clientID, "tenant-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(identity).ToNot(BeNil())
		token := &jwt.Token{Claims: jwt.MapClaims{
			"azp": clientID, "osac_tenant": "tenant-a", "preferred_username": "service-account-" + clientID,
		}}
		before, err := auth.ExtractAuthContext(token)
		Expect(err).ToNot(HaveOccurred())
		decision, err := evaluator.Evaluate(ctx, before, "/osac.public.v1.ComputeInstances/List")
		Expect(err).ToNot(HaveOccurred())
		Expect(decision.Allowed).To(BeTrue())
		deleteTenant()
		identity, err = serviceaccounts.ResolveAuthorization(ctx, clientID, "tenant-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(identity).To(BeNil())
		_, err = call(token, "/osac.public.v1.ComputeInstances/List", nil, func(context.Context, any) (any, error) {
			Fail("deleted tenant managed credentials must not reach the handler")
			return nil, nil
		})
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied))
	})

	It("preserves platform administrator and controller access to retained tenant data", func() {
		deleteTenant()
		for _, username := range []string{"service-account-osac-admin", "service-account-osac-controller"} {
			token := &jwt.Token{Claims: jwt.MapClaims{"preferred_username": username}}
			_, err := call(token, "/osac.public.v1.ServiceAccounts/Get", nil, func(c context.Context, _ any) (any, error) {
				Expect(auth.SubjectFromContext(c).Tenants.Finite()).To(BeFalse())
				return accounts.Get(c, publicv1.ServiceAccountsGetRequest_builder{Id: accountID}.Build())
			})
			Expect(err).ToNot(HaveOccurred())
		}
	})

	It("distinguishes onboarding from retained and archived deletion", func() {
		deleteTenant()
		_, err := suiteTx.Exec(ctx, `insert into archived_tenants
			(id, name, tenant, creation_timestamp, deletion_timestamp, data)
			values ('archived', 'archived', 'archived', now(), now(), '{}')`)
		Expect(err).ToNot(HaveOccurred())
		names, err := database.ResolveLiveTenantNames(ctx, []string{"new-tenant", "tenant-a", "archived", "tenant-b"})
		Expect(err).ToNot(HaveOccurred())
		Expect(names).To(Equal([]string{"new-tenant", "tenant-b"}))
	})

	It("does not provision or regain tenant access from an old JWT after complete archival", func() {
		const name = "fully-archived"
		seedAuthorizationTenant(ctx, name)
		token := humanToken(name)
		_, err := tenants.Delete(ctx, privatev1.TenantsDeleteRequest_builder{Id: name}.Build())
		Expect(err).ToNot(HaveOccurred())
		// Complete the ordinary non-billed cleanup: archive the default project,
		// then remove the retained tenant's finalizer using its current version.
		projects, err := dao.NewGenericDAO[*privatev1.Project]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		var projectID string
		Expect(suiteTx.QueryRow(ctx, `select id from projects where tenant=$1`, name).Scan(&projectID)).To(Succeed())
		_, err = projects.Delete().SetId(projectID).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		current, err := tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: name}.Build())
		Expect(err).ToNot(HaveOccurred())
		current.GetObject().GetMetadata().SetFinalizers(nil)
		_, err = tenants.Update(ctx, privatev1.TenantsUpdateRequest_builder{
			Object: current.GetObject(), Lock: true,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil
		managerCtx := database.TxManagerIntoContext(context.Background(), tm)
		var live, archived int
		Expect(testPool.QueryRow(managerCtx, `select count(*) from tenants where name=$1`, name).Scan(&live)).To(Succeed())
		Expect(testPool.QueryRow(managerCtx, `select count(*) from archived_tenants where name=$1`, name).Scan(&archived)).To(Succeed())
		Expect(live).To(BeZero())
		Expect(archived).To(Equal(1))
		users, err := NewPrivateUsersServer().SetLogger(logger).SetTenancyLogic(logic).SetAttributionLogic(attribution).Build()
		Expect(err).ToNot(HaveOccurred())
		provisioner, err := provisioners.NewUserProvisioner().SetLogger(logger).SetUsersServer(users).Build()
		Expect(err).ToNot(HaveOccurred())
		jit, err := auth.NewGrpcJitProvisioningInterceptor().SetLogger(logger).SetProvisioner(provisioner).Build()
		Expect(err).ToNot(HaveOccurred())
		txInterceptor, err := database.NewTxInterceptor().SetLogger(logger).SetManager(tm).Build()
		Expect(err).ToNot(HaveOccurred())
		info := &grpc.UnaryServerInfo{FullMethod: "/osac.public.v1.ServiceAccounts/List"}
		_, err = txInterceptor.UnaryServer(auth.ContextWithToken(managerCtx, token), nil, info,
			func(c context.Context, request any) (any, error) {
				return interceptor.UnaryServer(c, request, info, func(c context.Context, request any) (any, error) {
					return jit.UnaryServer(c, request, info, func(c context.Context, _ any) (any, error) {
						Expect(auth.SubjectFromContext(c).Tenants.Empty()).To(BeTrue())
						visibility, err := logic.DetermineVisibility(c)
						Expect(err).ToNot(HaveOccurred())
						Expect(visibility.IsProjectVisible(name, "project")).To(BeFalse())
						return accounts.List(c, &publicv1.ServiceAccountsListRequest{})
					})
				})
			})
		Expect(err).ToNot(HaveOccurred())
		Expect(testPool.QueryRow(managerCtx, `select count(*) from tenants where name=$1`, name).Scan(&live)).To(Succeed())
		Expect(live).To(BeZero())
		var provisioned int
		Expect(testPool.QueryRow(managerCtx, `select count(*) from users where tenant=$1`, name).Scan(&provisioned)).To(Succeed())
		Expect(provisioned).To(BeZero())
	})

	It("rechecks direct tenancy decisions made with a subject obtained before deletion", func() {
		stale := auth.ContextWithSubject(ctx, &auth.Subject{User: "alice", Tenants: collections.NewSet("tenant-a", "tenant-b")})
		deleteTenant()
		assignable, err := logic.DetermineAssignableTenants(stale)
		Expect(err).ToNot(HaveOccurred())
		Expect(assignable.Inclusions()).To(ConsistOf("tenant-b"))
		visibility, err := logic.DetermineVisibility(stale)
		Expect(err).ToNot(HaveOccurred())
		Expect(visibility.IsTenantVisible("tenant-a")).To(BeFalse())
	})

	It("revokes old JWT access across independent committed RPC transactions", func() {
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil
		managerCtx := database.TxManagerIntoContext(context.Background(), tm)
		txInterceptor, err := database.NewTxInterceptor().SetLogger(logger).SetManager(tm).Build()
		Expect(err).ToNot(HaveOccurred())
		token := humanToken("tenant-a")
		invoke := func(method string, request any, handler grpc.UnaryHandler) (any, error) {
			info := &grpc.UnaryServerInfo{FullMethod: method}
			return txInterceptor.UnaryServer(auth.ContextWithToken(managerCtx, token), request, info,
				func(c context.Context, request any) (any, error) {
					return interceptor.UnaryServer(c, request, info, handler)
				})
		}
		get := publicv1.ServiceAccountsGetRequest_builder{Id: accountID}.Build()
		_, err = invoke("/osac.public.v1.ServiceAccounts/Get", get, func(c context.Context, _ any) (any, error) {
			return accounts.Get(c, get)
		})
		Expect(err).ToNot(HaveOccurred())
		_, err = database.WithNewTx(managerCtx, func(c context.Context) (struct{}, error) {
			_, err := tenants.Delete(c, privatev1.TenantsDeleteRequest_builder{Id: "tenant-a"}.Build())
			return struct{}{}, err
		})
		Expect(err).ToNot(HaveOccurred())
		_, err = invoke("/osac.public.v1.ServiceAccounts/Get", get, func(c context.Context, _ any) (any, error) {
			return accounts.Get(c, get)
		})
		Expect(status.Code(err)).To(Equal(codes.NotFound))
		update := publicv1.ServiceAccountsUpdateRequest_builder{
			Object: publicv1.ServiceAccount_builder{Id: accountID,
				Metadata: publicv1.Metadata_builder{Labels: map[string]string{"forbidden": "true"}}.Build()}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
		}.Build()
		_, err = invoke("/osac.public.v1.ServiceAccounts/Update", update, func(c context.Context, _ any) (any, error) {
			return accounts.Update(c, update)
		})
		Expect(status.Code(err)).To(Equal(codes.NotFound))
		// Verify denial was not merely a response projection: no mutation committed.
		var forbidden bool
		Expect(testPool.QueryRow(managerCtx, `select labels ? 'forbidden' from service_accounts where id=$1`, accountID).
			Scan(&forbidden)).To(Succeed())
		Expect(forbidden).To(BeFalse())
	})
})

func seedAuthorizationTenant(c context.Context, name string) {
	tenants, err := dao.NewGenericDAO[*privatev1.Tenant]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	_, err = tenants.Create().SetObject(privatev1.Tenant_builder{
		Id: name, Metadata: privatev1.Metadata_builder{Name: name, Tenant: name, Finalizers: []string{"hold-for-cleanup"}}.Build(),
	}.Build()).Do(c)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
}

func seedAuthorizationBillingAssignment(c context.Context, tenant string) {
	tx, err := database.TxFromContext(c)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	_, err = tx.Exec(c, `insert into billing_accounts (id, provider_key, provider_account_reference, state)
		values ('authorization-billing-account', 'fixture', 'fixture-account', 'ACTIVE')`)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	_, err = tx.Exec(c, `insert into tenant_billing_assignments
		(id, name, tenant_id, billing_account_id, connection_state)
		values ('authorization-assignment', 'authorization-assignment', $1, 'authorization-billing-account', 'CONNECTED')`, tenant)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
}
