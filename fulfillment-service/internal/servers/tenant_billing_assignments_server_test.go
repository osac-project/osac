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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Tenant billing assignments server", func() {
	BeforeEach(func() {
		// Independent assignment transactions must see the committed tenant fixture.
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil
		ctx = database.TxManagerIntoContext(context.Background(), tm)
	})

	seedAccounts := func(ids ...string) {
		_, err := database.WithNewTx(ctx, func(ctx context.Context) (struct{}, error) {
			tx, err := database.TxFromContext(ctx)
			if err != nil {
				return struct{}{}, err
			}
			for _, id := range ids {
				_, err = database.UpsertBillingAccount(ctx, tx, database.BillingAccountUpsert{
					ID: id, ProviderKey: billingfake.Key, ProviderAccountReference: id, State: "ACTIVE",
				})
				if err != nil {
					return struct{}{}, err
				}
			}
			return struct{}{}, nil
		})
		Expect(err).ToNot(HaveOccurred())
	}

	build := func(p *billing.Provider) *TenantBillingAssignmentsServer {
		private, err := NewPrivateTenantBillingAssignmentsServer().SetLogger(logger).SetProvider(p).Build()
		Expect(err).ToNot(HaveOccurred())
		server, err := NewTenantBillingAssignmentsServer().SetLogger(logger).SetDelegate(private).Build()
		Expect(err).ToNot(HaveOccurred())
		return server
	}
	It("keeps Signal and its messages out of the public descriptor", func() {
		privateFile := (&privatev1.TenantBillingAssignmentsSignalRequest{}).ProtoReflect().Descriptor().ParentFile()
		publicFile := (&publicv1.TenantBillingAssignmentsListRequest{}).ProtoReflect().Descriptor().ParentFile()
		Expect(privateFile.Services().ByName("TenantBillingAssignments").Methods().ByName("Signal")).ToNot(BeNil())
		Expect(publicFile.Services().ByName("TenantBillingAssignments").Methods().ByName("Signal")).To(BeNil())
		Expect(publicFile.Messages().ByName("TenantBillingAssignmentsSignalRequest")).To(BeNil())
		Expect(publicFile.Messages().ByName("TenantBillingAssignmentsSignalResponse")).To(BeNil())
		for _, method := range publicv1.TenantBillingAssignments_ServiceDesc.Methods {
			Expect(method.MethodName).ToNot(Equal("Signal"))
		}
	})

	It("requires a logger and a delegate", func() {
		_, err := NewTenantBillingAssignmentsServer().Build()
		Expect(err).To(MatchError("logger is mandatory"))
		_, err = NewTenantBillingAssignmentsServer().SetLogger(logger).Build()
		Expect(err).To(MatchError("delegate is mandatory"))
	})

	It("propagates FAILED_PRECONDITION when billing is disabled", func() {
		fake := billingfake.New(billingfake.Scenario{})
		adminCtx := auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: auth.AllTenants})
		_, err := build(billing.NewProvider("", fake, nil)).List(adminCtx, &publicv1.TenantBillingAssignmentsListRequest{})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.FailedPrecondition))
	})

	DescribeTable("creates, gets, and lists an assignment end to end through the public API", func(tenants collections.Set[string]) {
		id := uuid.New()
		fake := billingfake.New(billingfake.Scenario{
			CorrelationWrite: true,
			Accounts:         []billingfake.AccountSpec{{ID: id, State: billing.AccountActive}},
		})
		server := build(fake.Provider())
		adminCtx := auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: tenants})

		seedAccounts(id)

		created, err := server.Create(adminCtx, publicv1.TenantBillingAssignmentsCreateRequest_builder{
			Object: publicv1.TenantBillingAssignment_builder{
				Metadata: publicv1.Metadata_builder{Name: "asg-1"}.Build(),
				Spec: publicv1.TenantBillingAssignmentSpec_builder{
					TenantId: testTenant, BillingAccountId: id,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(created.GetObject().GetStatus().GetConnectionState()).To(Equal(
			publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))

		got, err := database.WithNewTx(adminCtx, func(ctx context.Context) (*publicv1.TenantBillingAssignmentsGetResponse, error) {
			return server.Get(ctx, publicv1.TenantBillingAssignmentsGetRequest_builder{
				Id: created.GetObject().GetId(),
			}.Build())
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(got.GetObject().GetSpec().GetTenantId()).To(Equal(testTenant))

		listed, err := database.WithNewTx(adminCtx, func(ctx context.Context) (*publicv1.TenantBillingAssignmentsListResponse, error) {
			return server.List(ctx, &publicv1.TenantBillingAssignmentsListRequest{})
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(listed.GetTotal()).To(Equal(int32(1)))
	}, Entry("universal", auth.AllTenants), Entry("finite", collections.NewSet(testTenant)),
		Entry("finite multi-tenant", collections.NewSet(testTenant, "another-tenant")))

	DescribeTable("updates an assignment to another account through the public API", func(tenants collections.Set[string]) {
		id1, id2 := uuid.New(), uuid.New()
		fake := billingfake.New(billingfake.Scenario{
			CorrelationWrite: true,
			Accounts: []billingfake.AccountSpec{
				{ID: id1, State: billing.AccountActive}, {ID: id2, State: billing.AccountActive},
			},
		})
		server := build(fake.Provider())
		adminCtx := auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: tenants})

		seedAccounts(id1, id2)

		created, err := server.Create(adminCtx, publicv1.TenantBillingAssignmentsCreateRequest_builder{
			Object: publicv1.TenantBillingAssignment_builder{
				Metadata: publicv1.Metadata_builder{Name: "asg-1"}.Build(),
				Spec:     publicv1.TenantBillingAssignmentSpec_builder{TenantId: testTenant, BillingAccountId: id1}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// The public Update converts the object, passes the update mask through, and converts the response
		// back: reassigning to a second account must close the first interval and open a new one.
		updated, err := server.Update(adminCtx, publicv1.TenantBillingAssignmentsUpdateRequest_builder{
			Object: publicv1.TenantBillingAssignment_builder{
				Id:   created.GetObject().GetId(),
				Spec: publicv1.TenantBillingAssignmentSpec_builder{TenantId: testTenant, BillingAccountId: id2}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.billing_account_id"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.GetObject().GetSpec().GetBillingAccountId()).To(Equal(id2))
		Expect(updated.GetObject().GetStatus().GetConnectionState()).To(Equal(
			publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))

		listed, err := database.WithNewTx(adminCtx, func(ctx context.Context) (*publicv1.TenantBillingAssignmentsListResponse, error) {
			return server.List(ctx, &publicv1.TenantBillingAssignmentsListRequest{})
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(listed.GetTotal()).To(Equal(int32(2)), "the closed interval and the new one are both listed")
		current := 0
		for _, item := range listed.GetItems() {
			if item.GetStatus().GetEffectiveTo() == nil {
				current++
				Expect(item.GetSpec().GetBillingAccountId()).To(Equal(id2))
			}
		}
		Expect(current).To(Equal(1), "exactly one current interval remains after the reassignment")
	}, Entry("universal", auth.AllTenants), Entry("finite", collections.NewSet(testTenant)))

	Describe("trusted tenant scope through the public delegate", func() {
		const foreignTenant = "foreign-tenant"
		var (
			server  *TenantBillingAssignmentsServer
			fake    *billingfake.Fake
			account string
		)
		callerCtx := func(tenants collections.Set[string]) context.Context {
			return auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: tenants})
		}
		BeforeEach(func() {
			_, err := testPool.Exec(ctx, `insert into tenants (id, name, tenant, creator, data)
				values ($1, $1, $1, 'system', '{}')`, foreignTenant)
			Expect(err).ToNot(HaveOccurred())
			account = uuid.New()
			seedAccounts(account)
			fake = billingfake.New(billingfake.Scenario{
				CorrelationWrite: true,
				Accounts:         []billingfake.AccountSpec{{ID: account, State: billing.AccountActive}},
			})
			server = build(fake.Provider())
		})
		seedHistory := func() {
			for i, tenantID := range []string{foreignTenant, testTenant, foreignTenant, testTenant} {
				_, err := testPool.Exec(ctx, `insert into tenant_billing_assignments
					(id, name, tenant_id, billing_account_id, effective_to, creator)
					values ($1, $1, $2, $3, now(), 'admin')`, string(rune('a'+i)), tenantID, account)
				Expect(err).ToNot(HaveOccurred())
			}
		}
		DescribeTable("preserves trusted scope and query totals across public pagination",
			func(tenants collections.Set[string], expected int32) {
				seedHistory()
				seen := map[string]bool{}
				for offset := int32(0); offset <= expected; offset++ {
					response, err := database.WithNewTx(callerCtx(tenants), func(txCtx context.Context) (*publicv1.TenantBillingAssignmentsListResponse, error) {
						return server.List(txCtx, publicv1.TenantBillingAssignmentsListRequest_builder{
							Offset: &offset, Limit: new(int32(1)), Order: new("id"),
						}.Build())
					})
					Expect(err).ToNot(HaveOccurred())
					Expect(response.GetTotal()).To(Equal(expected))
					if offset == expected {
						Expect(response.GetItems()).To(BeEmpty())
						Expect(response.GetSize()).To(BeZero())
						continue
					}
					Expect(response.GetSize()).To(Equal(int32(1)))
					item := response.GetItems()[0]
					Expect(tenants.Contains(item.GetSpec().GetTenantId())).To(BeTrue())
					Expect(seen[item.GetId()]).To(BeFalse())
					seen[item.GetId()] = true
				}
				Expect(seen).To(HaveLen(int(expected)))
				Expect(fake.Calls()).To(BeEmpty())
			},
			Entry("finite", collections.NewSet(testTenant), int32(2)),
			Entry("finite multi-tenant", collections.NewSet(testTenant, foreignTenant), int32(4)),
			Entry("universal", auth.AllTenants, int32(4)),
			Entry("empty", collections.NewSet[string](), int32(0)),
		)

		It("cannot widen trusted scope with a public tenant filter", func() {
			seedHistory()
			response, err := database.WithNewTx(callerCtx(collections.NewSet(testTenant)), func(txCtx context.Context) (*publicv1.TenantBillingAssignmentsListResponse, error) {
				return server.List(txCtx, publicv1.TenantBillingAssignmentsListRequest_builder{
					Filter: new(`this.spec.tenant_id == "foreign-tenant" && has(this.status.effective_to)`),
				}.Build())
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetTotal()).To(BeZero())
			Expect(response.GetItems()).To(BeEmpty())
		})

		DescribeTable("rejects foreign reads and tenant-spoofed writes without provider calls", func(empty bool) {
			seedHistory()
			tenants := collections.NewSet(testTenant)
			if empty {
				tenants = collections.NewSet[string]()
			}
			_, err := database.WithNewTx(callerCtx(tenants), func(txCtx context.Context) (*publicv1.TenantBillingAssignmentsGetResponse, error) {
				return server.Get(txCtx, publicv1.TenantBillingAssignmentsGetRequest_builder{Id: "a"}.Build())
			})
			Expect(status.Code(err)).To(Equal(codes.NotFound))
			_, err = server.Update(callerCtx(tenants), publicv1.TenantBillingAssignmentsUpdateRequest_builder{
				Object: publicv1.TenantBillingAssignment_builder{
					Id: "a", Metadata: publicv1.Metadata_builder{Name: "a", Tenant: testTenant}.Build(),
					Spec: publicv1.TenantBillingAssignmentSpec_builder{TenantId: testTenant, BillingAccountId: account}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
			_, err = server.Create(callerCtx(tenants), publicv1.TenantBillingAssignmentsCreateRequest_builder{
				Object: publicv1.TenantBillingAssignment_builder{
					Metadata: publicv1.Metadata_builder{Name: "spoofed", Tenant: testTenant}.Build(),
					Spec:     publicv1.TenantBillingAssignmentSpec_builder{TenantId: foreignTenant, BillingAccountId: account}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.PermissionDenied))
			Expect(fake.Calls()).To(BeEmpty())
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(Equal(4))
			var correlation string
			Expect(testPool.QueryRow(ctx, `select coalesce(correlation_id, '') from billing_accounts where id = $1`, account).
				Scan(&correlation)).To(Succeed())
			Expect(correlation).To(BeEmpty())
		}, Entry("finite", false), Entry("empty", true))

		DescribeTable("rejects reserved targets without provider I/O or persistence", func(tenantID string) {
			_, err := server.Create(callerCtx(auth.AllTenants), publicv1.TenantBillingAssignmentsCreateRequest_builder{
				Object: publicv1.TenantBillingAssignment_builder{
					Metadata: publicv1.Metadata_builder{Name: "reserved"}.Build(),
					Spec:     publicv1.TenantBillingAssignmentSpec_builder{TenantId: tenantID, BillingAccountId: account}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(fake.Calls()).To(BeEmpty())
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
			var correlation string
			Expect(testPool.QueryRow(ctx, `select coalesce(correlation_id, '') from billing_accounts where id = $1`, account).
				Scan(&correlation)).To(Succeed())
			Expect(correlation).To(BeEmpty())
		}, Entry("shared", auth.SharedTenant), Entry("system", auth.SystemTenant))
	})
})
