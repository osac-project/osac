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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
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
			accounts, err := dao.NewGenericDAO[*privatev1.BillingAccount]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			if err != nil {
				return struct{}{}, err
			}
			for _, id := range ids {
				name, nameErr := billingAccountName(id)
				if nameErr != nil {
					return struct{}{}, nameErr
				}
				_, err = accounts.Create().SetObject(privatev1.BillingAccount_builder{Id: id,
					Metadata: privatev1.Metadata_builder{Name: name, Tenant: auth.SystemTenant, Creator: "system"}.Build(),
					Spec:     &privatev1.BillingAccountSpec{}, Status: privatev1.BillingAccountStatus_builder{
						ProviderKey: billingfake.Key, ProviderAccountReference: id,
						State:                     privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE,
						StateObservationTimestamp: timestamppb.Now(),
					}.Build(),
				}.Build()).Do(ctx)
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
		_, err := billingRPC(adminCtx, build(billing.NewProvider("", fake, nil)).List, &publicv1.TenantBillingAssignmentsListRequest{})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.FailedPrecondition))
	})

	It("keeps assignment Delete an embedded unimplemented scaffold", func() {
		fake := billingfake.New(billingfake.Scenario{})
		server := build(fake.Provider())
		_, err := server.Delete(ctx, publicv1.TenantBillingAssignmentsDeleteRequest_builder{Id: "unused"}.Build())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		_, err = server.private.Delete(ctx, privatev1.TenantBillingAssignmentsDeleteRequest_builder{Id: "unused"}.Build())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		Expect(fake.Calls()).To(BeEmpty())
	})

	It("rejects reserved public metadata without converting the validation error to Internal", func() {
		fake := billingfake.New(billingfake.Scenario{})
		caller := auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: auth.AllTenants})
		_, err := billingRPC(caller, build(fake.Provider()).Create, publicv1.TenantBillingAssignmentsCreateRequest_builder{
			Object: publicv1.TenantBillingAssignment_builder{Metadata: publicv1.Metadata_builder{
				Name: "reserved", Labels: map[string]string{"admin.osac.openshift.io/private": "forbidden"},
			}.Build()}.Build(),
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(fake.Calls()).To(BeEmpty())
	})

	DescribeTable("rolls private assignment writes back when the actual public output mapper fails", func(replace bool) {
		first, second := uuid.New(), uuid.New()
		seedAccounts(first, second)
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: first, ExternalID: "first-provider-token", State: billing.AccountActive},
			{ID: second, ExternalID: "second-provider-token", State: billing.AccountActive},
		}})
		server := build(fake.Provider())
		caller := auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: auth.AllTenants})
		object := publicv1.TenantBillingAssignment_builder{
			Metadata: publicv1.Metadata_builder{Name: "mapping-rollback"}.Build(),
			Spec: publicv1.TenantBillingAssignmentSpec_builder{
				Tenant:         publicv1.TenantReference_builder{Id: testTenant}.Build(),
				BillingAccount: publicv1.BillingAccountReference_builder{Id: first}.Build(),
			}.Build(),
		}.Build()
		if replace {
			created, err := billingRPC(caller, server.Create, publicv1.TenantBillingAssignmentsCreateRequest_builder{Object: object}.Build())
			Expect(err).NotTo(HaveOccurred())
			object = created.GetObject()
			object.GetSpec().SetBillingAccount(publicv1.BillingAccountReference_builder{Id: second}.Build())
		}
		rows := func(table string) string {
			var value string
			// Table names are fixed by this fixture, never request input.
			Expect(testPool.QueryRow(ctx, `select coalesce(jsonb_agg(to_jsonb(b) order by id),'[]'::jsonb)::text from `+table+` b`).Scan(&value)).To(Succeed())
			return value
		}
		accountsBefore, intervalsBefore := rows("billing_accounts"), rows("tenant_billing_assignments")
		server.outMapper.strict = true
		var err error
		if replace {
			_, err = billingRPC(caller, server.Update, publicv1.TenantBillingAssignmentsUpdateRequest_builder{Object: object}.Build())
		} else {
			_, err = billingRPC(caller, server.Create, publicv1.TenantBillingAssignmentsCreateRequest_builder{Object: object}.Build())
		}
		Expect(status.Code(err)).To(Equal(codes.Internal))
		Expect(rows("billing_accounts")).To(Equal(accountsBefore), "token and observation must roll back after private success")
		Expect(rows("tenant_billing_assignments")).To(Equal(intervalsBefore), "the old current interval and history must be unchanged")
		Expect(fake.Count("SetCorrelationValue")).To(BeZero())
	}, Entry("Create", false), Entry("replacement Update", true))

	DescribeTable("creates, gets, and lists an assignment end to end through the public API", func(tenants collections.Set[string]) {
		id := uuid.New()
		fake := billingfake.New(billingfake.Scenario{
			Accounts: []billingfake.AccountSpec{{ID: id, State: billing.AccountActive}},
		})
		server := build(fake.Provider())
		adminCtx := auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: tenants})

		seedAccounts(id)

		created, err := billingRPC(adminCtx, server.Create, publicv1.TenantBillingAssignmentsCreateRequest_builder{
			Object: publicv1.TenantBillingAssignment_builder{
				Metadata: publicv1.Metadata_builder{Name: "asg-1"}.Build(),
				Spec: publicv1.TenantBillingAssignmentSpec_builder{
					Tenant: publicv1.TenantReference_builder{Id: testTenant}.Build(), BillingAccount: publicv1.BillingAccountReference_builder{Id: id}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(created.GetObject().GetStatus().GetConnectionState()).To(Equal(
			publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING))

		got, err := billingRPC(adminCtx, server.Get, publicv1.TenantBillingAssignmentsGetRequest_builder{
			Id: created.GetObject().GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(got.GetObject().GetSpec().GetTenant().GetId()).To(Equal(testTenant))

		listed, err := billingRPC(adminCtx, server.List, &publicv1.TenantBillingAssignmentsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(listed.GetTotal()).To(Equal(int32(1)))
	}, Entry("universal", auth.AllTenants), Entry("finite", collections.NewSet(testTenant)),
		Entry("finite multi-tenant", collections.NewSet(testTenant, "another-tenant")))

	DescribeTable("updates an assignment to another account through the public API", func(tenants collections.Set[string]) {
		id1, id2 := uuid.New(), uuid.New()
		fake := billingfake.New(billingfake.Scenario{
			Accounts: []billingfake.AccountSpec{
				{ID: id1, State: billing.AccountActive}, {ID: id2, State: billing.AccountActive},
			},
		})
		server := build(fake.Provider())
		adminCtx := auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: tenants})

		seedAccounts(id1, id2)

		created, err := billingRPC(adminCtx, server.Create, publicv1.TenantBillingAssignmentsCreateRequest_builder{
			Object: publicv1.TenantBillingAssignment_builder{
				Metadata: publicv1.Metadata_builder{Name: "asg-1"}.Build(),
				Spec:     publicv1.TenantBillingAssignmentSpec_builder{Tenant: publicv1.TenantReference_builder{Id: testTenant}.Build(), BillingAccount: publicv1.BillingAccountReference_builder{Id: id1}.Build()}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// The public Update converts the object, passes the update mask through, and converts the response
		// back: reassigning to a second account must close the first interval and open a new one.
		updated, err := billingRPC(adminCtx, server.Update, publicv1.TenantBillingAssignmentsUpdateRequest_builder{
			Object: publicv1.TenantBillingAssignment_builder{
				Id:   created.GetObject().GetId(),
				Spec: publicv1.TenantBillingAssignmentSpec_builder{Tenant: publicv1.TenantReference_builder{Id: testTenant}.Build(), BillingAccount: publicv1.BillingAccountReference_builder{Id: id2}.Build()}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.billing_account"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.GetObject().GetSpec().GetBillingAccount().GetId()).To(Equal(id2))
		Expect(updated.GetObject().GetStatus().GetConnectionState()).To(Equal(
			publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING))

		listed, err := billingRPC(adminCtx, server.List, &publicv1.TenantBillingAssignmentsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(listed.GetTotal()).To(Equal(int32(2)), "the closed interval and the new one are both listed")
		Expect(listed.GetItems()[0].GetId()).To(Equal(updated.GetObject().GetId()), "default order is latest interval first")
		current := 0
		for _, item := range listed.GetItems() {
			if item.GetStatus().GetEffectiveToTimestamp() == nil {
				current++
				Expect(item.GetSpec().GetBillingAccount().GetId()).To(Equal(id2))
			}
		}
		Expect(current).To(Equal(1), "exactly one current interval remains after the reassignment")
	}, Entry("universal", auth.AllTenants), Entry("finite", collections.NewSet(testTenant)))

	DescribeTable("constrains public query paths and pagination", func(filter, order string, offset, limit int32) {
		fake := billingfake.New(billingfake.Scenario{})
		caller := auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: auth.AllTenants})
		_, err := billingRPC(caller, build(fake.Provider()).List, publicv1.TenantBillingAssignmentsListRequest_builder{
			Filter: &filter, Order: &order, Offset: &offset, Limit: &limit,
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(fake.Calls()).To(BeEmpty())
	}, Entry("old scalar filter", `this.spec.tenant_id == "x"`, "", int32(0), int32(0)),
		Entry("status object order", "", "status", int32(0), int32(0)),
		Entry("private finalizers", "", "metadata.finalizers", int32(0), int32(0)),
		Entry("negative offset", "", "", int32(-1), int32(0)),
		Entry("negative limit", "", "", int32(0), int32(-1)))

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
				Accounts: []billingfake.AccountSpec{{ID: account, State: billing.AccountActive}},
			})
			server = build(fake.Provider())
		})
		seedHistory := func() {
			for i, tenantID := range []string{foreignTenant, testTenant, foreignTenant, testTenant} {
				_, err := database.WithNewTx(ctx, func(c context.Context) (*dao.CreateResponse[*privatev1.TenantBillingAssignment], error) {
					d, err := dao.NewGenericDAO[*privatev1.TenantBillingAssignment]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
					if err != nil {
						return nil, err
					}
					name, err := billingAccountName(account)
					if err != nil {
						return nil, err
					}
					at := time.Now().UTC().Truncate(time.Microsecond)
					return d.Create().SetObject(privatev1.TenantBillingAssignment_builder{Id: string(rune('a' + i)),
						Metadata: privatev1.Metadata_builder{Name: string(rune('a' + i)), Tenant: tenantID, Creator: "admin",
							Annotations: map[string]string{"osac.openshift.io/tenant": tenantID}}.Build(),
						Spec: privatev1.TenantBillingAssignmentSpec_builder{
							Tenant:         privatev1.TenantReference_builder{Id: tenantID, Name: tenantID}.Build(),
							BillingAccount: privatev1.BillingAccountReference_builder{Id: account, Name: name}.Build(),
						}.Build(), Status: privatev1.TenantBillingAssignmentStatus_builder{
							EffectiveFromTimestamp: timestamppb.New(at), EffectiveToTimestamp: timestamppb.New(at),
							ConnectionState: privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED,
						}.Build(),
					}.Build()).Do(c)
				})
				Expect(err).ToNot(HaveOccurred())
			}
		}
		DescribeTable("preserves trusted scope and query totals across public pagination",
			func(tenants collections.Set[string], expected int32) {
				seedHistory()
				seen := map[string]bool{}
				for offset := int32(0); offset <= expected; offset++ {
					response, err := billingRPC(callerCtx(tenants), server.List, publicv1.TenantBillingAssignmentsListRequest_builder{
						Offset: &offset, Limit: new(int32(1)), Order: new("id"),
					}.Build())
					Expect(err).ToNot(HaveOccurred())
					Expect(response.GetTotal()).To(Equal(expected))
					if offset == expected {
						Expect(response.GetItems()).To(BeEmpty())
						Expect(response.GetSize()).To(BeZero())
						continue
					}
					Expect(response.GetSize()).To(Equal(int32(1)))
					item := response.GetItems()[0]
					Expect(tenants.Contains(item.GetSpec().GetTenant().GetId())).To(BeTrue())
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
			response, err := billingRPC(callerCtx(collections.NewSet(testTenant)), server.List, publicv1.TenantBillingAssignmentsListRequest_builder{
				Filter: new(`this.spec.tenant.id == "foreign-tenant" && has(this.status.effective_to_timestamp)`),
			}.Build())
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
			_, err := billingRPC(callerCtx(tenants), server.Get, publicv1.TenantBillingAssignmentsGetRequest_builder{Id: "a"}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
			_, err = billingRPC(callerCtx(tenants), server.Update, publicv1.TenantBillingAssignmentsUpdateRequest_builder{
				Object: publicv1.TenantBillingAssignment_builder{
					Id: "a", Metadata: publicv1.Metadata_builder{Name: "a", Tenant: testTenant}.Build(),
					Spec: publicv1.TenantBillingAssignmentSpec_builder{Tenant: publicv1.TenantReference_builder{Id: testTenant}.Build(), BillingAccount: publicv1.BillingAccountReference_builder{Id: account}.Build()}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
			_, err = billingRPC(callerCtx(tenants), server.Create, publicv1.TenantBillingAssignmentsCreateRequest_builder{
				Object: publicv1.TenantBillingAssignment_builder{
					Metadata: publicv1.Metadata_builder{Name: "spoofed", Tenant: testTenant}.Build(),
					Spec:     publicv1.TenantBillingAssignmentSpec_builder{Tenant: publicv1.TenantReference_builder{Id: foreignTenant}.Build(), BillingAccount: publicv1.BillingAccountReference_builder{Id: account}.Build()}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.PermissionDenied))
			Expect(fake.Calls()).To(BeEmpty())
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(Equal(4))
			var correlation string
			Expect(testPool.QueryRow(ctx, `select coalesce(external_billing_account_id, '') from billing_accounts where id = $1`, account).
				Scan(&correlation)).To(Succeed())
			Expect(correlation).To(BeEmpty())
		}, Entry("finite", false), Entry("empty", true))

		DescribeTable("rejects reserved targets without provider I/O or persistence", func(tenantID string) {
			_, err := billingRPC(callerCtx(auth.AllTenants), server.Create, publicv1.TenantBillingAssignmentsCreateRequest_builder{
				Object: publicv1.TenantBillingAssignment_builder{
					Metadata: publicv1.Metadata_builder{Name: "reserved"}.Build(),
					Spec:     publicv1.TenantBillingAssignmentSpec_builder{Tenant: publicv1.TenantReference_builder{Id: tenantID}.Build(), BillingAccount: publicv1.BillingAccountReference_builder{Id: account}.Build()}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(fake.Calls()).To(BeEmpty())
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
			var correlation string
			Expect(testPool.QueryRow(ctx, `select coalesce(external_billing_account_id, '') from billing_accounts where id = $1`, account).
				Scan(&correlation)).To(Succeed())
			Expect(correlation).To(BeEmpty())
		}, Entry("shared", auth.SharedTenant), Entry("system", auth.SystemTenant))
	})
})
