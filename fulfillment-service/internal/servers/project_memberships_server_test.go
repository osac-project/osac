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

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/serviceaccounts"
	itesting "github.com/osac-project/osac/fulfillment-service/internal/testing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Public ProjectMemberships Create authorization", func() {
	const (
		otherTenant    = "other-tenant"
		deletedTenant  = "deleting-tenant"
		project        = "parent.child"
		managerGroup   = "/parent/child/system:managers"
		membershipName = "membership-authorization"
	)
	manager := jwt.MapClaims{"username": "manager", "organization": map[string]any{
		testTenant:  map[string]any{"groups": []any{managerGroup}},
		otherTenant: map[string]any{"groups": []any{}},
	}}
	platform := jwt.MapClaims{"username": "platform", "groups": []any{"admins"}}
	tenantAdmin := jwt.MapClaims{"username": "tenant-admin", "organization": []any{testTenant},
		"realm_access": map[string]any{"roles": []any{"tenant-admin"}}}
	managed := jwt.MapClaims{"azp": serviceaccounts.ManagedClientIDPrefix + "fixture-client", "osac_tenant": testTenant}

	var server *ProjectMembershipsServer
	var interceptor *auth.GrpcAuthzInterceptor
	BeforeEach(func() {
		_, err := suiteTx.Exec(ctx, `insert into tenants (id, name, tenant, data) values
			($1, $1, $1, '{}'), ($2, $2, $2, '{}')`, otherTenant, deletedTenant)
		Expect(err).NotTo(HaveOccurred())
		for _, tenant := range []string{testTenant, otherTenant, deletedTenant} {
			_, err = suiteTx.Exec(ctx, `insert into projects (id, tenant, name, project, data) values
				(uuidv7(), $1, 'parent', '', '{}'), (uuidv7(), $1, 'parent.child', 'parent', '{}'),
				(uuidv7(), $1, 'deleting-project', '', '{}')`, tenant)
			Expect(err).NotTo(HaveOccurred())
		}
		_, err = suiteTx.Exec(ctx, `update projects set deletion_timestamp = now() where name = 'deleting-project'`)
		Expect(err).NotTo(HaveOccurred())
		_, err = suiteTx.Exec(ctx, `update tenants set deletion_timestamp = now() where name = $1`, deletedTenant)
		Expect(err).NotTo(HaveOccurred())

		realTenancy, err := auth.NewDefaultTenancyLogic().SetLogger(logger).
			SetTenantResolver(database.ResolveLiveTenantNames).Build()
		Expect(err).NotTo(HaveOccurred())
		server, err = NewProjectMembershipsServer().SetLogger(logger).SetAttributionLogic(attribution).
			SetTenancyLogic(realTenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		evaluator, err := auth.NewEvaluator().SetTenantResolver(database.ResolveLiveTenantNames).
			SetManagedServiceAccountResolver(func(_ context.Context, id, tenant string) (*serviceaccounts.AuthorizationIdentity, error) {
				return &serviceaccounts.AuthorizationIdentity{ClientID: id, Tenant: tenant, Roles: []string{"tenant-admin"}}, nil
			}).Build()
		Expect(err).NotTo(HaveOccurred())
		interceptor, err = auth.NewGrpcAuthzInterceptor().SetLogger(logger).SetEvaluator(evaluator).Build()
		Expect(err).NotTo(HaveOccurred())
	})

	DescribeTable("checks the exact live placement before writing a membership",
		func(claims jwt.MapClaims, tenant, project string, expected codes.Code) {
			request := publicv1.ProjectMembershipsCreateRequest_builder{
				Object: publicv1.ProjectMembership_builder{
					Metadata: publicv1.Metadata_builder{Name: membershipName, Tenant: tenant, Project: project}.Build(),
					Spec: publicv1.ProjectMembershipSpec_builder{
						Role: publicv1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER,
					}.Build(),
				}.Build(),
			}.Build()
			var response any
			var err error
			if claims == nil {
				requestCtx := auth.ContextWithSubject(ctx, &auth.Subject{User: "fixture", Tenants: auth.AllTenants})
				response, err = server.Create(requestCtx, request)
			} else {
				requestCtx := auth.ContextWithToken(ctx, itesting.MakeTokenObject(nil, claims))
				response, err = interceptor.UnaryServer(requestCtx, request,
					&grpc.UnaryServerInfo{FullMethod: publicv1.ProjectMemberships_Create_FullMethodName},
					func(ctx context.Context, _ any) (any, error) { return server.Create(ctx, request) })
			}
			Expect(status.Code(err)).To(Equal(expected), "%v", err)
			var count int
			Expect(suiteTx.QueryRow(ctx, `select count(*) from project_memberships where name = $1`, membershipName).
				Scan(&count)).To(Succeed())
			if expected != codes.OK {
				Expect(count).To(BeZero())
				return
			}
			Expect(count).To(Equal(1))
			if tenant == "" {
				tenant = testTenant
			}
			created := response.(*publicv1.ProjectMembershipsCreateResponse).GetObject()
			Expect(created.GetMetadata().GetTenant()).To(Equal(tenant))
			Expect(created.GetMetadata().GetProject()).To(Equal(project))
		},
		Entry("manager in the same tenant", manager, testTenant, project, codes.OK),
		Entry("manager group scoped to a different tenant", manager, otherTenant, project, codes.PermissionDenied),
		Entry("tenant admin with the default placement", tenantAdmin, "", project, codes.OK),
		Entry("managed tenant admin", managed, testTenant, project, codes.OK),
		Entry("platform admin", platform, otherTenant, project, codes.OK),
		Entry("deleting tenant", platform, deletedTenant, project, codes.PermissionDenied),
		Entry("missing tenant", platform, "missing-tenant", project, codes.PermissionDenied),
		Entry("deleting project", platform, testTenant, "deleting-project", codes.PermissionDenied),
		Entry("missing project", platform, testTenant, "missing-project", codes.PermissionDenied),
		Entry("missing authorization context", nil, testTenant, project, codes.PermissionDenied),
	)
})
