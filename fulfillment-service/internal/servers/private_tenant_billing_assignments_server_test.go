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
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// assignmentListRaceTx simulates a row disappearing between the DAO's real count and page queries.
// QueryRow sees the seeded row; the first Query returns an empty page.
type assignmentListRaceTx struct {
	database.Tx
	returnedEmptyPage bool
}

func (t *assignmentListRaceTx) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	if !t.returnedEmptyPage {
		t.returnedEmptyPage = true
		// The List DAO binds its page limit as the final parameter.
		args[len(args)-1] = int32(0)
	}
	return t.Tx.Query(ctx, query, args...)
}

// hangingCorrelationAdapter blocks the carrier walk until its context is done - it reproduces a
// provider that accepts requests but never answers, so the assign deadline is the only way out.
type hangingCorrelationAdapter struct {
	*billingfake.Fake
}

// writePoisonAdapter deliberately advertises the obsolete write surface as extra methods. Runtime resolution
// must remain read-only even if an adapter happens to implement those methods.
type writePoisonAdapter struct {
	*billingfake.Fake
	writes int
}

func (a *writePoisonAdapter) SupportsCorrelationWrite() bool { return true }

func (a *writePoisonAdapter) SetCorrelationValue(context.Context, billing.AccountRef, string) error {
	a.writes++
	return errors.New("provider writes are forbidden")
}

func (a *hangingCorrelationAdapter) ListAccounts(ctx context.Context, _ billing.AccountFilter, _ billing.Page) (billing.AccountPage, error) {
	<-ctx.Done()
	return billing.AccountPage{}, billing.NewError(billing.ReasonTimeout, "ListAccounts", true, 0)
}

// gatedAccountReadAdapter snapshots the selected account before delaying its response, reproducing an older provider
// observation whose completion races a newer discovery. entered closes only after the result has been sampled.
type gatedAccountReadAdapter struct {
	*billingfake.Fake
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (a *gatedAccountReadAdapter) GetAccount(ctx context.Context, ref billing.AccountRef) (billing.Account, error) {
	account, err := a.Fake.GetAccount(ctx, ref)
	a.once.Do(func() { close(a.entered) })
	select {
	case <-a.release:
		return account, err
	case <-ctx.Done():
		return billing.Account{}, billing.NewError(billing.ReasonTimeout, "GetAccount", true, 0)
	}
}

// gatedCorrelationAdapter holds selected-account reads until released or their context ends, reproducing an
// in-flight assign whose provider call is slow, so a concurrent assign must fail fast.
type gatedCorrelationAdapter struct {
	*billingfake.Fake
	entered    chan struct{}
	release    chan struct{}
	once       sync.Once
	blockedRef string
}

func (a *gatedCorrelationAdapter) GetAccount(ctx context.Context, ref billing.AccountRef) (billing.Account, error) {
	if a.blockedRef != "" && ref != billing.NewAccountRef(a.blockedRef) {
		return a.Fake.GetAccount(ctx, ref)
	}
	a.once.Do(func() { close(a.entered) })
	select {
	case <-a.release:
		return a.Fake.GetAccount(ctx, ref)
	case <-ctx.Done():
		return billing.Account{}, billing.NewError(billing.ReasonTimeout, "GetAccount", true, 0)
	}
}

type gatedAccountScanAdapter struct {
	*billingfake.Fake
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (a *gatedAccountScanAdapter) ListAccounts(ctx context.Context, filter billing.AccountFilter,
	page billing.Page,
) (billing.AccountPage, error) {
	if filter.ExternalID != "" {
		a.once.Do(func() { close(a.entered) })
		select {
		case <-a.release:
		case <-ctx.Done():
			return billing.AccountPage{}, ctx.Err()
		}
	}
	return a.Fake.ListAccounts(ctx, filter, page)
}

type billingProviderBudgetAdapter struct {
	*billingfake.Fake
	deadlines []time.Time
}

func (a *billingProviderBudgetAdapter) GetAccount(ctx context.Context, ref billing.AccountRef) (billing.Account, error) {
	deadline, _ := ctx.Deadline()
	a.deadlines = append(a.deadlines, deadline)
	return a.Fake.GetAccount(ctx, ref)
}

func (a *billingProviderBudgetAdapter) CorrelationValue(ctx context.Context, ref billing.AccountRef) (string, bool, error) {
	deadline, _ := ctx.Deadline()
	a.deadlines = append(a.deadlines, deadline)
	return a.Fake.CorrelationValue(ctx, ref)
}

func (a *billingProviderBudgetAdapter) ListAccounts(ctx context.Context, filter billing.AccountFilter, page billing.Page) (billing.AccountPage, error) {
	deadline, _ := ctx.Deadline()
	a.deadlines = append(a.deadlines, deadline)
	return a.Fake.ListAccounts(ctx, filter, page)
}

var _ = Describe("Private tenant billing assignments server", func() {
	const otherTenant = "other-tenant"

	BeforeEach(func() {
		// Commit the shared fixture's tenant. Each RPC below uses an ordinary outer interceptor transaction,
		// independently of the read-only assertion transaction.
		Expect(suiteTx.End(ctx)).To(Succeed())
		var err error
		suiteTx, err = tm.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		ctx = database.TxIntoContext(ctx, suiteTx)
	})

	build := func(p *billing.Provider) *PrivateTenantBillingAssignmentsServer {
		server, err := NewPrivateTenantBillingAssignmentsServer().SetLogger(logger).SetProvider(p).Build()
		Expect(err).ToNot(HaveOccurred())
		return server
	}

	adminCtx := func() context.Context {
		return auth.ContextWithSubject(database.TxManagerIntoContext(context.Background(), tm),
			&auth.Subject{User: "admin", Tenants: auth.AllTenants})
	}

	// seedAccountRow inserts a billing_accounts row with a caller-chosen id, so it can line up with a
	// billingfake.AccountSpec using the same id as its provider reference.
	seedAccountRow := func(id, correlationID, state string) {
		err := tm.Run(ctx, func(tx database.Tx) error {
			accounts, err := dao.NewGenericDAO[*privatev1.BillingAccount]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			if err != nil {
				return err
			}
			name, err := billingAccountName(id)
			if err != nil {
				return err
			}
			observed := privatev1.BillingAccountStatus_builder{ProviderKey: billingfake.Key, ProviderAccountReference: id,
				State: mapBillingAccountState(state), StateObservationTimestamp: timestamppb.Now()}.Build()
			if correlationID != "" {
				observed.SetExternalBillingAccountId(correlationID)
			}
			_, err = accounts.Create().SetObject(privatev1.BillingAccount_builder{Id: id,
				Metadata: privatev1.Metadata_builder{Name: name, Tenant: auth.SystemTenant, Creator: "system"}.Build(),
				Spec:     &privatev1.BillingAccountSpec{}, Status: observed,
			}.Build()).Do(database.TxIntoContext(ctx, tx))
			return err
		})
		Expect(err).ToNot(HaveOccurred())
	}
	// newAccount creates a billing_accounts row and a matching fake provider account sharing one id.
	// providerExternalID is the value already set on the provider side ("" for none).
	newAccount := func(fakeAccounts *[]billingfake.AccountSpec, providerExternalID, osacCorrelationID, state string) string {
		id := uuid.New()
		*fakeAccounts = append(*fakeAccounts,
			billingfake.AccountSpec{ID: id, ExternalID: providerExternalID, State: billing.AccountActive})
		seedAccountRow(id, osacCorrelationID, state)
		return id
	}
	createOtherTenant := func() {
		_, err := testPool.Exec(ctx, `insert into tenants (id, name, tenant, creator, data) values ($1, $1, $1, 'system', '{}')
			on conflict do nothing`, otherTenant)
		Expect(err).ToNot(HaveOccurred())
	}

	createRequest := func(tenantID, accountID, name string) *privatev1.TenantBillingAssignmentsCreateRequest {
		return privatev1.TenantBillingAssignmentsCreateRequest_builder{
			Object: privatev1.TenantBillingAssignment_builder{
				Metadata: privatev1.Metadata_builder{Name: name}.Build(),
				Spec: privatev1.TenantBillingAssignmentSpec_builder{
					Tenant: privatev1.TenantReference_builder{Id: tenantID}.Build(), BillingAccount: privatev1.BillingAccountReference_builder{Id: accountID}.Build(),
				}.Build(),
			}.Build(),
		}.Build()
	}
	updateRequest := func(id, tenantID, accountID string) *privatev1.TenantBillingAssignmentsUpdateRequest {
		return privatev1.TenantBillingAssignmentsUpdateRequest_builder{
			Object: privatev1.TenantBillingAssignment_builder{
				Id: id,
				Spec: privatev1.TenantBillingAssignmentSpec_builder{
					Tenant: privatev1.TenantReference_builder{Id: tenantID}.Build(), BillingAccount: privatev1.BillingAccountReference_builder{Id: accountID}.Build(),
				}.Build(),
			}.Build(),
		}.Build()
	}

	reason := func(err error) string {
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Details()).ToNot(BeEmpty())
		return st.Details()[0].(*errdetails.ErrorInfo).GetReason()
	}
	seedHistoryInterval := func(id, tenantID, accountID string, from time.Time, to *time.Time) {
		Expect(tm.Run(ctx, func(tx database.Tx) error {
			c := database.TxIntoContext(ctx, tx)
			d, err := dao.NewGenericDAO[*privatev1.TenantBillingAssignment]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			if err != nil {
				return err
			}
			name, err := billingAccountName(accountID)
			if err != nil {
				return err
			}
			observed := privatev1.TenantBillingAssignmentStatus_builder{
				ConnectionState:        privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED,
				EffectiveFromTimestamp: timestamppb.New(from),
			}.Build()
			if to != nil {
				observed.SetEffectiveToTimestamp(timestamppb.New(*to))
			}
			_, err = d.Create().SetObject(privatev1.TenantBillingAssignment_builder{Id: id,
				Metadata: privatev1.Metadata_builder{Name: id, Tenant: tenantID, Creator: "original-actor",
					Annotations: map[string]string{"osac.openshift.io/tenant": tenantID}}.Build(),
				Spec: privatev1.TenantBillingAssignmentSpec_builder{
					Tenant:         privatev1.TenantReference_builder{Id: tenantID, Name: tenantID}.Build(),
					BillingAccount: privatev1.BillingAccountReference_builder{Id: accountID, Name: name}.Build(),
				}.Build(), Status: observed,
			}.Build()).Do(c)
			return err
		})).To(Succeed())
	}

	It("requires a logger and a provider", func() {
		_, err := NewPrivateTenantBillingAssignmentsServer().SetProvider(billing.NewProvider("", nil, nil)).Build()
		Expect(err).To(MatchError("logger is mandatory"))
		_, err = NewPrivateTenantBillingAssignmentsServer().SetLogger(logger).Build()
		Expect(err).To(MatchError("provider is mandatory"))
	})

	Describe("disabled mode", func() {
		It("fails every method with BILLING_NOT_CONFIGURED and makes no provider call", func() {
			fake := billingfake.New(billingfake.Scenario{})
			server := build(billing.NewProvider("", fake, nil))

			_, err := billingRPC(adminCtx(), server.List, &privatev1.TenantBillingAssignmentsListRequest{})
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("BILLING_NOT_CONFIGURED"))

			_, err = billingRPC(adminCtx(), server.Create, createRequest(testTenant, "any", "asg"))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("BILLING_NOT_CONFIGURED"))

			Expect(fake.Calls()).To(BeEmpty())
		})

		DescribeTable("reports the final early RPC error to the ambient transaction", func(method string) {
			fake := billingfake.New(billingfake.Scenario{})
			private := build(billing.NewProvider("", fake, nil))
			public, err := NewTenantBillingAssignmentsServer().SetLogger(logger).SetDelegate(private).Build()
			Expect(err).NotTo(HaveOccurred())
			committed := 0
			_, err = billingRPC(adminCtx(), func(c context.Context, _ struct{}) (struct{}, error) {
				tx, txErr := database.TxFromContext(c)
				if txErr != nil {
					return struct{}{}, txErr
				}
				if _, txErr = tx.Exec(c, `insert into tenants (id,name,tenant,creator,data)
					values ('early-error','early-error','early-error','system','{}')`); txErr != nil {
					return struct{}{}, txErr
				}
				tx.OnCommit(func() { committed++ })
				var rpcErr error
				switch method {
				case "private/List":
					_, rpcErr = private.List(c, &privatev1.TenantBillingAssignmentsListRequest{})
				case "private/Get":
					_, rpcErr = private.Get(c, &privatev1.TenantBillingAssignmentsGetRequest{})
				case "private/Create":
					_, rpcErr = private.Create(c, &privatev1.TenantBillingAssignmentsCreateRequest{})
				case "private/Update":
					_, rpcErr = private.Update(c, &privatev1.TenantBillingAssignmentsUpdateRequest{})
				case "private/Signal":
					_, rpcErr = private.Signal(c, &privatev1.TenantBillingAssignmentsSignalRequest{})
				case "public/List":
					_, rpcErr = public.List(c, &publicv1.TenantBillingAssignmentsListRequest{})
				case "public/Get":
					_, rpcErr = public.Get(c, &publicv1.TenantBillingAssignmentsGetRequest{})
				case "public/Create":
					_, rpcErr = public.Create(c, publicv1.TenantBillingAssignmentsCreateRequest_builder{Object: &publicv1.TenantBillingAssignment{}}.Build())
				case "public/Update":
					_, rpcErr = public.Update(c, &publicv1.TenantBillingAssignmentsUpdateRequest{})
				}
				return struct{}{}, rpcErr
			}, struct{}{})
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("BILLING_NOT_CONFIGURED"))
			Expect(committed).To(BeZero())
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenants where id = 'early-error'`).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
			Expect(fake.Calls()).To(BeEmpty())
		},
			Entry("private List", "private/List"), Entry("private Get", "private/Get"),
			Entry("private Create", "private/Create"), Entry("private Update", "private/Update"),
			Entry("private Signal", "private/Signal"), Entry("public List", "public/List"),
			Entry("public Get", "public/Get"), Entry("public Create", "public/Create"),
			Entry("public Update", "public/Update"),
		)
	})

	DescribeTable("compares raw local duplicate tokens during assignment", func(method string, equal bool) {
		const providerValue = `path\to`
		retainedValue := `path\\to`
		if equal {
			retainedValue = providerValue
		}
		var conforming string
		Expect(testPool.QueryRow(ctx, `show standard_conforming_strings`).Scan(&conforming)).To(Succeed())
		Expect(conforming).To(Equal("on"))
		var accounts []billingfake.AccountSpec
		selected := newAccount(&accounts, providerValue, "", "ACTIVE")
		// The other account retains a local token but is not a provider-side carrier of the selected token.
		other := newAccount(&accounts, "other-provider-value", retainedValue, "ACTIVE")
		initial := ""
		if method == "Update" {
			initial = newAccount(&accounts, "initial-token", "initial-token", "ACTIVE")
		}
		fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
		adapter := &writePoisonAdapter{Fake: fake}
		server := build(billing.NewProvider(billingfake.Key, adapter, nil))
		var prior *privatev1.TenantBillingAssignment
		if method == "Update" {
			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, initial, "initial"))
			Expect(err).NotTo(HaveOccurred())
			prior = created.GetObject()
		}
		carrierScans := fake.Count("ListAccounts")
		var object *privatev1.TenantBillingAssignment
		var err error
		if method == "Create" {
			var response *privatev1.TenantBillingAssignmentsCreateResponse
			response, err = billingRPC(adminCtx(), server.Create, createRequest(testTenant, selected, "raw-duplicate"))
			object = response.GetObject()
		} else {
			var response *privatev1.TenantBillingAssignmentsUpdateResponse
			response, err = billingRPC(adminCtx(), server.Update, updateRequest(prior.GetId(), testTenant, selected))
			object = response.GetObject()
		}
		if equal {
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal(billing.ReasonAccountCorrelationConflict))
			Expect(fake.Count("ListAccounts")).To(Equal(carrierScans), "the local duplicate must fail before the provider carrier walk or SQL uniqueness")
		} else {
			Expect(err).NotTo(HaveOccurred(), "one and two backslashes are different retained tokens")
			Expect(fake.Count("ListAccounts")).To(Equal(carrierScans + 1))
			Expect(object.GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
			Expect(object.GetSpec().GetBillingAccount().GetId()).To(Equal(selected))
		}
		for _, id := range []string{selected, other} {
			stored, readErr := database.WithNewTx(adminCtx(), func(c context.Context) (*dao.GetResponse[*privatev1.BillingAccount], error) {
				return server.accounts.Get().SetId(id).Do(c)
			})
			Expect(readErr).NotTo(HaveOccurred())
			if id == other {
				Expect(stored.GetObject().GetStatus().GetExternalBillingAccountId()).To(Equal(retainedValue))
			} else if equal {
				Expect(stored.GetObject().GetStatus().HasExternalBillingAccountId()).To(BeFalse())
			} else {
				Expect(stored.GetObject().GetStatus().GetExternalBillingAccountId()).To(Equal(providerValue))
			}
		}
		current, readErr := billingRPC(adminCtx(), server.List, &privatev1.TenantBillingAssignmentsListRequest{})
		Expect(readErr).NotTo(HaveOccurred())
		if equal && method == "Create" {
			Expect(current.GetItems()).To(BeEmpty())
		} else if equal {
			Expect(current.GetItems()).To(HaveLen(1))
			Expect(proto.Equal(current.GetItems()[0], prior)).To(BeTrue(), "rejected Update preserves the current interval")
		} else {
			var ids []string
			for _, item := range current.GetItems() {
				ids = append(ids, item.GetId())
			}
			Expect(ids).To(ContainElement(object.GetId()))
		}
		Expect(adapter.writes).To(BeZero())
		Expect(fake.Count("SetCorrelationValue")).To(BeZero())
		value, present, readErr := fake.CorrelationValue(ctx, billing.NewAccountRef(selected))
		Expect(readErr).NotTo(HaveOccurred())
		Expect(present).To(BeTrue())
		Expect(value).To(Equal(providerValue))
	},
		Entry("Create accepts a distinct two-backslash retained token", "Create", false),
		Entry("Update accepts a distinct two-backslash retained token", "Update", false),
		Entry("Create rejects an actually equal one-backslash retained token", "Create", true),
		Entry("Update rejects an actually equal one-backslash retained token", "Update", true),
	)

	DescribeTable("handles a row disappearing between count and page without provider I/O", func(method string, code codes.Code) {
		const assignmentID = "disappearing-assignment"
		var accounts []billingfake.AccountSpec
		accountID := newAccount(&accounts, "exact", "exact", "ACTIVE")
		fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
		server := build(fake.Provider())
		caller := adminCtx()
		if method != "Create" {
			seedHistoryInterval(assignmentID, testTenant, accountID, time.Now().UTC().Truncate(time.Microsecond), nil)
		}
		if method == "Get" {
			caller = auth.ContextWithSubject(caller, &auth.Subject{
				User: "admin", Tenants: auth.AllTenants.Difference(collections.NewSet(otherTenant)),
			})
		}
		_, err := billingRPC(caller, func(ctx context.Context, _ struct{}) (struct{}, error) {
			tx, err := database.TxFromContext(ctx)
			Expect(err).NotTo(HaveOccurred())
			raceTx := &assignmentListRaceTx{Tx: tx}
			ctx = database.TxIntoContext(ctx, raceTx)
			switch method {
			case "Create":
				request := createRequest(testTenant, accountID, "disappearing-tenant")
				request.GetObject().GetSpec().SetTenant(privatev1.TenantReference_builder{Name: testTenant}.Build())
				_, err = server.Create(ctx, request)
			case "Signal":
				_, err = server.Signal(ctx, privatev1.TenantBillingAssignmentsSignalRequest_builder{Id: assignmentID}.Build())
			case "Get":
				_, err = server.Get(ctx, privatev1.TenantBillingAssignmentsGetRequest_builder{Id: assignmentID}.Build())
			}
			Expect(raceTx.returnedEmptyPage).To(BeTrue())
			return struct{}{}, err
		}, struct{}{})
		Expect(status.Code(err)).To(Equal(code))
		if method == "Signal" {
			Expect(reason(err)).To(Equal(billingOperationRetryReason))
		}
		Expect(fake.Calls()).To(BeEmpty())
	},
		Entry("Create with a by-name tenant", "Create", codes.InvalidArgument),
		Entry("Signal with an active assignment", "Signal", codes.Aborted),
		Entry("Get with complementary tenant scope", "Get", codes.NotFound),
	)

	Describe("Create", func() {
		It("rejects a dry-run before provider calls or any database change and still allows an ordinary Create", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "dry-run-external", "dry-run-external", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())

			rows := func(table string) string {
				var value string
				// Table names are fixed by this fixture, never request input.
				Expect(testPool.QueryRow(ctx, `select coalesce(jsonb_agg(to_jsonb(r) order by id),'[]'::jsonb)::text from `+table+` r`).Scan(&value)).To(Succeed())
				return value
			}
			accountsBefore, intervalsBefore := rows("billing_accounts"), rows("tenant_billing_assignments")

			dry := auth.ContextWithSubject(dryRunCtx(), &auth.Subject{User: "admin", Tenants: auth.AllTenants})
			_, err := billingRPC(dry, server.Create, createRequest(testTenant, account, "dry-run-create"))
			Expect(status.Code(err)).To(Equal(codes.Unimplemented))
			Expect(err.Error()).To(ContainSubstring("dry-run is not supported"))
			Expect(fake.Calls()).To(BeEmpty(), "a dry-run must not call the provider")
			Expect(rows("billing_accounts")).To(Equal(accountsBefore),
				"a dry-run must not change attribution, state or observation evidence")
			Expect(rows("tenant_billing_assignments")).To(Equal(intervalsBefore),
				"a dry-run must not insert an interval or close an existing one")

			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "ordinary-create"))
			Expect(err).NotTo(HaveOccurred())
			Expect(created.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
			Expect(fake.Count("GetAccount")).To(Equal(1), "the ordinary control still reads provider evidence")
			page, listErr := server.bindings.List().SetLimit(10).Do(ctx)
			Expect(listErr).NotTo(HaveOccurred())
			Expect(page.GetTotal()).To(Equal(int32(1)), "the ordinary control still persists one assignment")
		})

		It("shares one provider child deadline across state, value and the exact-carrier walk", func() {
			var accounts []billingfake.AccountSpec
			id := newAccount(&accounts, "exact", "exact", "ACTIVE")
			adapter := &billingProviderBudgetAdapter{Fake: billingfake.New(billingfake.Scenario{Accounts: accounts})}
			server := build(billing.NewProvider(billingfake.Key, adapter, nil))
			_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, id, "shared-budget"))
			Expect(err).NotTo(HaveOccurred())
			Expect(adapter.deadlines).To(HaveLen(2))
			Expect(adapter.deadlines[0]).NotTo(BeZero())
			Expect(adapter.deadlines[1]).To(Equal(adapter.deadlines[0]))
		})
		DescribeTable("preserves raw token presence and exact bytes through resolution", func(local *string, providerValue string) {
			var accounts []billingfake.AccountSpec
			id := newAccount(&accounts, providerValue, "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			if local != nil {
				Expect(tm.Run(ctx, func(tx database.Tx) error {
					c := database.TxIntoContext(ctx, tx)
					r, err := server.accounts.Get().SetId(id).Do(c)
					if err != nil {
						return err
					}
					r.GetObject().GetStatus().SetExternalBillingAccountId(*local)
					_, err = server.accounts.Update().SetObject(r.GetObject()).Do(c)
					return err
				})).To(Succeed())
			}
			_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, id, "raw-token"))
			Expect(err).NotTo(HaveOccurred())
			stored, err := database.WithNewTx(adminCtx(), func(c context.Context) (*dao.GetResponse[*privatev1.BillingAccount], error) {
				return server.accounts.Get().SetId(id).Do(c)
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(stored.GetObject().GetStatus().HasExternalBillingAccountId()).To(BeTrue())
			Expect(stored.GetObject().GetStatus().GetExternalBillingAccountId()).To(Equal(providerValue))
		}, Entry("absent", (*string)(nil), "provider"), Entry("present empty", new(""), "provider"),
			Entry("present whitespace", new(" \t "), "provider"), Entry("padded established", new(" padded "), " padded "))
		DescribeTable("canonicalizes typed id/name references without borrowing a project scope", func(byName bool) {
			var accounts []billingfake.AccountSpec
			id := newAccount(&accounts, "exact", "exact", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			request := createRequest(testTenant, id, "canonical")
			if byName {
				name, err := billingAccountName(id)
				Expect(err).NotTo(HaveOccurred())
				request.GetObject().GetSpec().SetTenant(privatev1.TenantReference_builder{Name: testTenant}.Build())
				request.GetObject().GetSpec().SetBillingAccount(privatev1.BillingAccountReference_builder{Name: name}.Build())
			}
			created, err := billingRPC(adminCtx(), build(fake.Provider()).Create, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(created.GetObject().GetSpec().GetTenant().GetId()).To(Equal(testTenant))
			Expect(created.GetObject().GetSpec().GetTenant().GetName()).To(Equal(testTenant))
			Expect(created.GetObject().GetSpec().GetBillingAccount().GetId()).To(Equal(id))
			Expect(created.GetObject().GetSpec().GetBillingAccount().GetName()).NotTo(BeEmpty())
			Expect(created.GetObject().GetMetadata().GetCreator()).To(Equal("admin"))
			Expect(created.GetObject().GetMetadata().GetCreationTimestamp()).NotTo(BeNil())
			Expect(created.GetObject().GetMetadata().GetAnnotations()).To(HaveKeyWithValue("osac.openshift.io/tenant", testTenant))
		}, Entry("id", false), Entry("name", true))

		DescribeTable("rejects contradictory references and ownership before provider I/O", func(change string) {
			var accounts []billingfake.AccountSpec
			id := newAccount(&accounts, "exact", "exact", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			request := createRequest(testTenant, id, "bad-reference")
			switch change {
			case "tenant":
				request.GetObject().GetSpec().GetTenant().SetName("contradiction")
			case "account":
				request.GetObject().GetSpec().GetBillingAccount().SetName("contradiction")
			case "project":
				request.GetObject().GetMetadata().SetProject("forbidden")
			case "metadata":
				request.GetObject().GetMetadata().SetTenant("contradiction")
			case "annotation":
				request.GetObject().GetMetadata().SetAnnotations(map[string]string{"osac.openshift.io/tenant": "contradiction"})
			}
			_, err := billingRPC(adminCtx(), build(fake.Provider()).Create, request)
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(fake.Calls()).To(BeEmpty())
		}, Entry("tenant id/name", "tenant"), Entry("account id/name", "account"), Entry("project", "project"),
			Entry("metadata tenant", "metadata"), Entry("tenant annotation", "annotation"))

		It("clamps creation to closed-only history and samples the start after provider I/O", func() {
			var accounts []billingfake.AccountSpec
			oldID := newAccount(&accounts, "old", "old", "ACTIVE")
			id := newAccount(&accounts, "new", "new", "ACTIVE")
			floor := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
			seedHistoryInterval("closed-history", testTenant, oldID, floor.Add(-time.Hour), &floor)
			response, err := billingRPC(adminCtx(), build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider()).Create, createRequest(testTenant, id, "after-history"))
			Expect(err).NotTo(HaveOccurred())
			Expect(response.GetObject().GetStatus().GetEffectiveFromTimestamp().AsTime()).To(BeTemporally("==", floor))
		})
		It("starts the first interval at a tenant creation timestamp ahead of the clock", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "future-tenant", "future-tenant", "ACTIVE")
			var clockBefore, tenantCreation time.Time
			Expect(testPool.QueryRow(ctx, `select clock_timestamp()`).Scan(&clockBefore)).To(Succeed())
			Expect(testPool.QueryRow(ctx, `select clock_timestamp() + interval '1 hour'`).Scan(&tenantCreation)).To(Succeed())
			_, err := testPool.Exec(ctx, `update tenants set creation_timestamp = $1 where id = $2`, tenantCreation, testTenant)
			Expect(err).NotTo(HaveOccurred())

			created, err := billingRPC(adminCtx(), build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider()).Create,
				createRequest(testTenant, account, "tenant-floor"))
			Expect(err).NotTo(HaveOccurred())
			start := created.GetObject().GetStatus().GetEffectiveFromTimestamp().AsTime()
			var stored time.Time
			Expect(testPool.QueryRow(ctx, `select effective_from from tenant_billing_assignments where id = $1`,
				created.GetObject().GetId()).Scan(&stored)).To(Succeed())
			Expect(stored).To(BeTemporally("==", start))
			Expect(tenantCreation).To(BeTemporally(">", clockBefore), "the tenant must be ahead of the sampled clock")
			Expect(start).To(BeTemporally(">=", tenantCreation), "the first interval must not predate its owning tenant")
			Expect(start).To(BeTemporally("==", tenantCreation), "a tenant ahead of the clock wins the floor")
		})
		It("keeps the sampled clock for the first interval of an earlier tenant", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "past-tenant", "past-tenant", "ACTIVE")
			var tenantCreation, clockBefore time.Time
			Expect(testPool.QueryRow(ctx, `select clock_timestamp() - interval '1 hour'`).Scan(&tenantCreation)).To(Succeed())
			_, err := testPool.Exec(ctx, `update tenants set creation_timestamp = $1 where id = $2`, tenantCreation, testTenant)
			Expect(err).NotTo(HaveOccurred())
			Expect(testPool.QueryRow(ctx, `select clock_timestamp()`).Scan(&clockBefore)).To(Succeed())

			created, err := billingRPC(adminCtx(), build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider()).Create,
				createRequest(testTenant, account, "ordinary-floor"))
			Expect(err).NotTo(HaveOccurred())
			start := created.GetObject().GetStatus().GetEffectiveFromTimestamp().AsTime()
			Expect(start).To(BeTemporally(">", tenantCreation), "an earlier tenant must not pull the start back")
			Expect(start).To(BeTemporally(">=", clockBefore), "the sampled database clock still sets an ordinary start")
		})
		It("keeps replacement intervals contiguous at a tenant creation floor ahead of the clock", func() {
			var accounts []billingfake.AccountSpec
			first := newAccount(&accounts, "first", "first", "ACTIVE")
			second := newAccount(&accounts, "second", "second", "ACTIVE")
			var tenantCreation time.Time
			Expect(testPool.QueryRow(ctx, `select clock_timestamp() + interval '1 hour'`).Scan(&tenantCreation)).To(Succeed())
			_, err := testPool.Exec(ctx, `update tenants set creation_timestamp = $1 where id = $2`, tenantCreation, testTenant)
			Expect(err).NotTo(HaveOccurred())
			server := build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider())
			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, first, "asg-1"))
			Expect(err).NotTo(HaveOccurred())

			replacement, err := billingRPC(adminCtx(), server.Update,
				updateRequest(created.GetObject().GetId(), testTenant, second))
			Expect(err).NotTo(HaveOccurred())
			prior, err := server.bindings.Get().SetId(created.GetObject().GetId()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			oldEnd := prior.GetObject().GetStatus().GetEffectiveToTimestamp().AsTime()
			newStart := replacement.GetObject().GetStatus().GetEffectiveFromTimestamp().AsTime()
			Expect(oldEnd).To(BeTemporally("==", newStart), "replacement intervals must stay contiguous")
			Expect(oldEnd).To(BeTemporally(">=", tenantCreation))
			Expect(newStart).To(BeTemporally("==", tenantCreation), "both boundaries share the clamped floor")
		})
		It("never invokes an advertised setter and retains PENDING until manual remediation", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			adapter := &writePoisonAdapter{Fake: fake}
			server := build(billing.NewProvider(billingfake.Key, adapter, nil))
			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(err).NotTo(HaveOccurred())
			Expect(created.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING))
			Expect(adapter.writes).To(BeZero())
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(Equal(1))
		})

		It("generates a local correlation id without writing the provider", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())

			resp, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING))
			Expect(resp.GetObject().GetStatus().GetMessage()).To(ContainSubstring("set externalId on the account in the provider"))
			Expect(resp.GetObject().GetSpec().GetTenant().GetId()).To(Equal(testTenant))
			Expect(resp.GetObject().GetSpec().GetBillingAccount().GetId()).To(Equal(account))
			Expect(resp.GetObject().GetId()).ToNot(BeEmpty())
		})

		It("rejects Create and Update for a tenant the platform has deleted", func() {
			var accounts []billingfake.AccountSpec
			account1 := newAccount(&accounts, "", "", "ACTIVE")
			account2 := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())

			// Create while the tenant is live, so there is a current interval to Update afterwards.
			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account1, "asg-1"))
			Expect(err).ToNot(HaveOccurred())

			// A second tenant with no assignment, so Create hits the dead-tenant rejection rather than
			// TENANT_ALREADY_BOUND. It is marked deleted without removing its row, the tombstone state that keeps
			// the tenant FK satisfied; only the explicit liveness check can reject it.
			_, err = testPool.Exec(ctx, `insert into tenants (id, name, tenant, creator, data)
				values ('dead-tenant', 'dead-tenant', 'dead-tenant', 'system', '{}') on conflict do nothing`)
			Expect(err).ToNot(HaveOccurred())
			_, err = testPool.Exec(ctx, `update tenants set deletion_timestamp = now() where name = 'dead-tenant'`)
			Expect(err).ToNot(HaveOccurred())

			_, err = billingRPC(adminCtx(), server.Create, createRequest("dead-tenant", account2, "asg-2"))
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.InvalidArgument), "%v", err)
			Expect(st.Message()).To(ContainSubstring("spec.tenant does not reference an existing tenant"))

			// Tenant deletion closes the active interval; a later update must reject that historical interval.
			_, err = testPool.Exec(ctx, `update tenants set deletion_timestamp = now() where name = $1`, testTenant)
			Expect(err).ToNot(HaveOccurred())
			_, err = billingRPC(adminCtx(), server.Update, updateRequest(created.GetObject().GetId(), testTenant, account2))
			st, ok = status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.FailedPrecondition), "%v", err)
			Expect(st.Message()).To(ContainSubstring("is no longer the current interval"))
		})

		It("generates a correlation id and stays PENDING with the stable manual-remediation reason", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())

			resp, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING))
			Expect(resp.GetObject().GetStatus().GetMessage()).To(ContainSubstring(billing.ReasonProviderCorrelationWriteUnsupported))
		})

		It("adopts a provider-only value and connects when exactly one account carries it", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "ext-preset", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())

			resp, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
		})

		It("retains an OSAC-only value as PENDING without writing the provider", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "osac-corr", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())

			resp, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING))
			Expect(fake.Count("SetCorrelationValue")).To(BeZero())
		})

		It("proceeds when both sides already agree", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "corr-equal", "corr-equal", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())

			resp, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
		})

		It("rejects differing values with BILLING_CORRELATION_ID_CONFLICT and creates no row", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "provider-side", "osac-side", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())

			_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("BILLING_CORRELATION_ID_CONFLICT"))
			Expect(err.Error()).ToNot(ContainSubstring("osac-side"))
			Expect(err.Error()).ToNot(ContainSubstring("provider-side"))

			page, listErr := server.bindings.List().SetLimit(10).SetFilter(`this.spec.billing_account.id == "` + account + `"`).Do(ctx)
			Expect(listErr).ToNot(HaveOccurred())
			Expect(page.GetTotal()).To(BeZero())
		})

		It("rejects a discovered duplicate with ACCOUNT_AMBIGUOUS and allows assignment after the provider corrects it", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "corr-dup", "", "ACTIVE")
			accounts = append(accounts, billingfake.AccountSpec{ID: "decoy", ExternalID: "corr-dup", State: billing.AccountActive})
			refresh := func() *billing.Provider {
				provider := billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider()
				inventory, err := NewPrivateBillingAccountsServer().SetLogger(logger).SetProvider(provider).Build()
				Expect(err).NotTo(HaveOccurred())
				_, err = billingRPC(adminCtx(), inventory.List, &privatev1.BillingAccountsListRequest{})
				Expect(err).NotTo(HaveOccurred())
				return provider
			}
			server := build(refresh())

			_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("ACCOUNT_AMBIGUOUS"))

			page, listErr := server.bindings.List().SetLimit(10).SetFilter(`this.spec.billing_account.id == "` + account + `"`).Do(ctx)
			Expect(listErr).ToNot(HaveOccurred())
			Expect(page.GetTotal()).To(BeZero())

			accounts[1].ExternalID = "corrected"
			server = build(refresh())
			row, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(err).NotTo(HaveOccurred())
			Expect(row.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
		})

		It("rejects a foreign provider carrier without creating an assignment", func() {
			var accounts []billingfake.AccountSpec
			// The target has no provider value, so even one pre-existing carrier
			// is foreign. Reject instead of adopting a duplicate attribution value.
			account := newAccount(&accounts, "", "osac-dup", "ACTIVE")
			accounts = append(accounts, billingfake.AccountSpec{ID: "decoy", ExternalID: "osac-dup", State: billing.AccountActive})
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())

			_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("ACCOUNT_AMBIGUOUS"))
			Expect(fake.Count("SetCorrelationValue")).To(BeZero())
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
		})

		It("persists no assignment when the provider read fails", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{
				Accounts: accounts,
				Failures: map[string]billingfake.Failure{"ListAccounts": {Reason: billing.ReasonUnavailable}},
			})
			server := build(fake.Provider())

			_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(status.Code(err)).To(Equal(codes.Unavailable))

			page, listErr := server.bindings.List().SetLimit(10).SetFilter(`this.spec.billing_account.id == "` + account + `"`).Do(ctx)
			Expect(listErr).ToNot(HaveOccurred())
			Expect(page.GetTotal()).To(BeZero())
		})

		It("fails as UNAVAILABLE/TIMEOUT and releases admission when the provider hangs", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(billing.NewProvider(billingfake.Key, &hangingCorrelationAdapter{Fake: fake}, nil))
			server.assignProviderDeadline = 50 * time.Millisecond

			result := make(chan error, 1)
			go func() {
				_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
				result <- err
			}()
			var err error
			Eventually(result, 5*time.Second).Should(Receive(&err))
			Expect(status.Code(err)).To(Equal(codes.Unavailable))
			Expect(reason(err)).To(Equal("TIMEOUT"))
			Expect(server.assignSem).To(BeEmpty())
			probeCtx, cancel := context.WithTimeout(adminCtx(), 5*time.Second)
			defer cancel()
			_, err = testPool.Exec(probeCtx, `select 1`)
			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with RESOURCE_EXHAUSTED when the concurrent-assign window is full", func() {
			createOtherTenant()
			var accounts []billingfake.AccountSpec
			blocked := newAccount(&accounts, "", "", "ACTIVE")
			turnedAway := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			adapter := &gatedCorrelationAdapter{
				Fake: fake, entered: make(chan struct{}), release: make(chan struct{}),
			}
			server := build(billing.NewProvider(billingfake.Key, adapter, nil))
			server.assignProviderDeadline = 30 * time.Second

			// Occupy one of the maxConcurrentAssigns slots the way a second in-flight assign would; the
			// blocked assign below takes the other, so the window is full for the request that follows.
			server.assignSem <- struct{}{}

			inFlight := make(chan error, 1)
			go func() {
				_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, blocked, "asg-1"))
				inFlight <- err
			}()
			Eventually(adapter.entered, 5*time.Second).Should(BeClosed(),
				"the in-flight assign must reach the provider window")

			_, err := billingRPC(adminCtx(), server.Create, createRequest(otherTenant, turnedAway, "asg-2"))
			Expect(status.Code(err)).To(Equal(codes.ResourceExhausted))
			Expect(err.Error()).To(ContainSubstring("too many concurrent billing assignment"))

			close(adapter.release)
			var firstErr error
			Eventually(inFlight, 15*time.Second).Should(Receive(&firstErr))
			Expect(firstErr).ToNot(HaveOccurred(),
				"the in-flight assign must still complete once the provider answers")
		})

		It("rejects a tenant that already has an active assignment, leaving both records unchanged", func() {
			var accounts []billingfake.AccountSpec
			account1 := newAccount(&accounts, "", "", "ACTIVE")
			account2 := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())

			_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account1, "asg-1"))
			Expect(err).ToNot(HaveOccurred())

			_, err = billingRPC(adminCtx(), server.Create, createRequest(testTenant, account2, "asg-2"))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("TENANT_ALREADY_BOUND"))

			active, getErr := server.bindings.List().SetLimit(1).SetFilter(`this.spec.tenant.id == "test-tenant" && !has(this.status.effective_to_timestamp)`).Do(ctx)
			Expect(getErr).ToNot(HaveOccurred())
			Expect(active.GetItems()[0].GetSpec().GetBillingAccount().GetId()).To(Equal(account1), "the original binding must be unchanged")
		})

		It("rejects an account already linked to another tenant", func() {
			createOtherTenant()
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())

			_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())

			_, err = billingRPC(adminCtx(), server.Create, createRequest(otherTenant, account, "asg-2"))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("ACCOUNT_ALREADY_BOUND"))
		})

		DescribeTable("does not let a stale stored state veto a provider-active account",
			func(storedState string) {
				// Discovery stored a non-ACTIVE state earlier. The provider reports the account active now, so the
				// assignment read must decide on that observation and refresh the stored one.
				id := uuid.New()
				seedAccountRow(id, "corr-live", storedState)
				fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{{
					ID: id, ExternalID: "corr-live", State: billing.AccountActive,
				}}})
				server := build(fake.Provider())

				resp, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, id, "asg-1"))
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.GetObject().GetStatus().GetConnectionState()).To(Equal(
					privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
				Expect(fake.Count("GetAccount")).To(BeNumerically(">", 0),
					"a stale stored state must be revalidated against the provider, not trusted")
				var state string
				Expect(testPool.QueryRow(ctx, `select state from billing_accounts where id = $1`, id).
					Scan(&state)).To(Succeed())
				Expect(state).To(Equal("ACTIVE"), "the deciding observation must replace the stale stored state")
			},
			Entry("stored INACTIVE", "INACTIVE"),
			Entry("stored UNKNOWN", "UNKNOWN"),
		)

		DescribeTable("rejects a provider-side deactivation or unknown state observed during Create",
			func(state billing.AccountState, expectedReason string) {
				var accounts []billingfake.AccountSpec
				account := newAccount(&accounts, "corr-live", "", "ACTIVE")
				// Make the stored observation old enough that the assignment read's refresh is observable.
				seedObservedAt := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
				Expect(tm.Run(ctx, func(tx database.Tx) error {
					c := database.TxIntoContext(ctx, tx)
					d, err := dao.NewGenericDAO[*privatev1.BillingAccount]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
					if err != nil {
						return err
					}
					r, err := d.Get().SetId(account).Do(c)
					if err != nil {
						return err
					}
					r.GetObject().GetStatus().SetStateObservationTimestamp(timestamppb.New(seedObservedAt))
					_, err = d.Update().SetObject(r.GetObject()).Do(c)
					return err
				})).To(Succeed())
				// Only the provider changes: the stored row stays ACTIVE and no BillingAccounts.List runs.
				accounts[0].State = state
				fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
				server := build(fake.Provider())

				_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
				Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
				Expect(reason(err)).To(Equal(expectedReason))
				Expect(fake.Count("GetAccount")).To(BeNumerically(">", 0),
					"assignment must read the selected account's current provider state")
				Expect(fake.Count("CorrelationValue")).To(BeZero(),
					"an ineligible account must be rejected before correlation resolution")
				Expect(fake.Count("SetCorrelationValue")).To(BeZero())
				var count int
				Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
				Expect(count).To(BeZero())

				// The failed RPC cannot persist its observation independently of the rejected assignment.
				var storedState string
				var observedAt time.Time
				Expect(testPool.QueryRow(ctx,
					`select state, status_observed_at from billing_accounts where id = $1`, account).
					Scan(&storedState, &observedAt)).To(Succeed())
				Expect(storedState).To(Equal("ACTIVE"))
				Expect(observedAt).To(BeTemporally("==", seedObservedAt.Truncate(time.Microsecond)))
			},
			Entry("provider reports INACTIVE", billing.AccountInactive, "ACCOUNT_INACTIVE"),
			Entry("provider reports UNKNOWN", billing.AccountUnknown, "ACCOUNT_STATE_UNKNOWN"),
		)

		It("recovers and connects when the provider reactivates the account", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "corr-live", "", "ACTIVE")
			accounts[0].State = billing.AccountInactive
			server := build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider())
			_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("ACCOUNT_INACTIVE"))

			accounts[0].State = billing.AccountActive
			server = build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider())
			resp, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
			var state string
			Expect(testPool.QueryRow(ctx, `select state from billing_accounts where id = $1`, account).Scan(&state)).To(Succeed())
			Expect(state).To(Equal("ACTIVE"))
		})

		It("persists no assignment when the current provider-state read fails", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "corr-live", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{
				Accounts: accounts,
				Failures: map[string]billingfake.Failure{"GetAccount": {Reason: billing.ReasonUnavailable}},
			})
			server := build(fake.Provider())

			_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(status.Code(err)).To(Equal(codes.Unavailable))
			Expect(fake.Count("CorrelationValue")).To(BeZero())
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
			var state string
			Expect(testPool.QueryRow(ctx, `select state from billing_accounts where id = $1`, account).Scan(&state)).To(Succeed())
			Expect(state).To(Equal("ACTIVE"), "a failed read must not invent an observation")
		})

		DescribeTable("keeps a newer concurrent observation that finished after the assignment read",
			func(injectedState string, expectedCode codes.Code, expectedReason string) {
				var accounts []billingfake.AccountSpec
				account := newAccount(&accounts, "corr-live", "", "ACTIVE")
				fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
				adapter := &gatedAccountScanAdapter{Fake: fake, entered: make(chan struct{}),
					release: make(chan struct{})}
				server := build(billing.NewProvider(billingfake.Key, adapter, nil))
				result := make(chan error, 1)
				go func() {
					_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
					result <- err
				}()
				DeferCleanup(func() {
					select {
					case <-adapter.release:
					default:
						close(adapter.release)
					}
				})
				Eventually(adapter.entered, 5*time.Second).Should(BeClosed())
				// Commit a later discovery observation while the carrier walk is in flight, using the same
				// real clock and upsert boundary as discovery, not a future timestamp. Keep full precision:
				// freshness compares nanoseconds, and only the SQL projection truncates.
				concurrentAt := time.Now().UTC()
				Expect(tm.Run(ctx, func(tx database.Tx) error {
					c := database.TxIntoContext(ctx, tx)
					r, err := server.accounts.Get().SetId(account).Do(c)
					if err != nil {
						return err
					}
					r.GetObject().GetStatus().SetState(mapBillingAccountState(injectedState))
					r.GetObject().GetStatus().SetStateObservationTimestamp(timestamppb.New(concurrentAt))
					_, err = server.accounts.Update().SetObject(r.GetObject()).Do(c)
					return err
				})).To(Succeed())
				Expect(adapter.release).NotTo(BeClosed())
				Expect(result).NotTo(Receive())
				close(adapter.release)

				var finalErr error
				Eventually(result, 5*time.Second).Should(Receive(&finalErr))
				Expect(status.Code(finalErr)).To(Equal(expectedCode))
				if expectedReason != "" {
					Expect(reason(finalErr)).To(Equal(expectedReason))
				}
				var state string
				var observedAt time.Time
				Expect(testPool.QueryRow(ctx,
					`select state, status_observed_at from billing_accounts where id = $1`, account).
					Scan(&state, &observedAt)).To(Succeed())
				Expect(state).To(Equal(injectedState),
					"the older assignment read must not overwrite the newer observation")
				Expect(observedAt.Equal(concurrentAt.Truncate(time.Microsecond))).To(BeTrue(),
					"the observation time must never move backwards")
				Expect(fake.Count("SetCorrelationValue")).To(BeZero())
				var correlation string
				Expect(testPool.QueryRow(ctx, `select coalesce(external_billing_account_id, '') from billing_accounts where id = $1`, account).
					Scan(&correlation)).To(Succeed())
				var count int
				Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
				if expectedCode != codes.OK {
					Expect(fake.Count("ListAccounts")).To(Equal(1), "all provider reads precede authoritative locks")
					Expect(correlation).To(BeEmpty(), "reject before adopting a provider correlation")
					Expect(count).To(BeZero(), "reject without opening an assignment interval")
				} else {
					Expect(correlation).To(Equal("corr-live"))
					Expect(count).To(Equal(1))
				}
			},
			Entry("newer concurrent ACTIVE observation", "ACTIVE", codes.OK, ""),
			Entry("newer concurrent INACTIVE observation", "INACTIVE", codes.FailedPrecondition, "ACCOUNT_INACTIVE"),
			Entry("newer concurrent UNKNOWN observation", "UNKNOWN", codes.FailedPrecondition, "ACCOUNT_STATE_UNKNOWN"),
		)

		It("rejects a nonexistent billing account", func() {
			fake := billingfake.New(billingfake.Scenario{})
			server := build(fake.Provider())
			_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, "no-such-account", "asg-1"))
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})
	})

	Describe("Update", func() {
		It("validates a partial atomic account mask against stored state and attributes only the new interval", func() {
			var accounts []billingfake.AccountSpec
			first := newAccount(&accounts, "first", "first", "ACTIVE")
			second := newAccount(&accounts, "second", "second", "ACTIVE")
			server := build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider())
			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, first, "masked"))
			Expect(err).NotTo(HaveOccurred())
			original := proto.Clone(created.GetObject()).(*privatev1.TenantBillingAssignment)
			actor := auth.ContextWithSubject(adminCtx(), &auth.Subject{User: "replacement-actor", Tenants: auth.AllTenants})
			updated, err := billingRPC(actor, server.Update, privatev1.TenantBillingAssignmentsUpdateRequest_builder{
				Object: privatev1.TenantBillingAssignment_builder{Id: original.GetId(), Spec: privatev1.TenantBillingAssignmentSpec_builder{
					BillingAccount: privatev1.BillingAccountReference_builder{Id: second}.Build(),
				}.Build()}.Build(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.billing_account"}},
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.GetObject().GetMetadata().GetCreator()).To(Equal("replacement-actor"))
			var storedCreator string
			var storedCreation time.Time
			var storedVersion int32
			Expect(testPool.QueryRow(ctx, `select creator, creation_timestamp, version from tenant_billing_assignments where id = $1`,
				updated.GetObject().GetId()).Scan(&storedCreator, &storedCreation, &storedVersion)).To(Succeed())
			Expect(storedCreator).To(Equal("replacement-actor"))
			Expect(storedCreation).To(BeTemporally("==", updated.GetObject().GetMetadata().GetCreationTimestamp().AsTime()))
			Expect(storedVersion).To(Equal(updated.GetObject().GetMetadata().GetVersion()))
			Expect(updated.GetObject().GetMetadata().GetTenant()).To(Equal(testTenant))
			Expect(updated.GetObject().GetMetadata().GetProject()).To(BeEmpty())
			Expect(updated.GetObject().GetMetadata().GetAnnotations()).To(HaveKeyWithValue("osac.openshift.io/tenant", testTenant))
			Expect(updated.GetObject().GetSpec().GetTenant().GetId()).To(Equal(testTenant))
			Expect(updated.GetObject().GetSpec().GetBillingAccount().GetName()).NotTo(BeEmpty())
			old, err := billingRPC(adminCtx(), server.Get, privatev1.TenantBillingAssignmentsGetRequest_builder{Id: original.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(old.GetObject().GetMetadata().GetCreator()).To(Equal(original.GetMetadata().GetCreator()))
			Expect(proto.Equal(old.GetObject().GetMetadata().GetCreationTimestamp(), original.GetMetadata().GetCreationTimestamp())).To(BeTrue())
			Expect(proto.Equal(old.GetObject().GetStatus().GetEffectiveToTimestamp(), updated.GetObject().GetStatus().GetEffectiveFromTimestamp())).To(BeTrue())
		})

		It("treats an explicit empty mask as a validated no-provider no-version-change operation", func() {
			var accounts []billingfake.AccountSpec
			id := newAccount(&accounts, "equal", "equal", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, id, "noop"))
			Expect(err).NotTo(HaveOccurred())
			calls := len(fake.Calls())
			response, err := billingRPC(adminCtx(), server.Update, privatev1.TenantBillingAssignmentsUpdateRequest_builder{
				Object:     privatev1.TenantBillingAssignment_builder{Id: created.GetObject().GetId()}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{},
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(proto.Equal(response.GetObject(), created.GetObject())).To(BeTrue())
			Expect(fake.Calls()).To(HaveLen(calls))
		})

		DescribeTable("rejects non-atomic, identity, and output mask paths before provider I/O", func(path string) {
			var accounts []billingfake.AccountSpec
			id := newAccount(&accounts, "equal", "equal", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, id, "immutable"))
			Expect(err).NotTo(HaveOccurred())
			calls := len(fake.Calls())
			_, err = billingRPC(adminCtx(), server.Update, privatev1.TenantBillingAssignmentsUpdateRequest_builder{
				Object:     proto.Clone(created.GetObject()).(*privatev1.TenantBillingAssignment),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}},
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(fake.Calls()).To(HaveLen(calls))
		}, Entry("reference leaf", "spec.billing_account.id"), Entry("tenant", "spec.tenant"),
			Entry("metadata", "metadata"), Entry("status", "status"), Entry("status leaf", "status.message"))

		It("revalidates an unchanged interval without rewriting its metadata or version", func() {
			var accounts []billingfake.AccountSpec
			id := newAccount(&accounts, "equal", "equal", "ACTIVE")
			server := build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider())
			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, id, "stable"))
			Expect(err).NotTo(HaveOccurred())
			response, err := billingRPC(adminCtx(), server.Update, updateRequest(created.GetObject().GetId(), testTenant, id))
			Expect(err).NotTo(HaveOccurred())
			Expect(proto.Equal(response.GetObject(), created.GetObject())).To(BeTrue())
		})
		It("rejects a deleted tenant even when revalidating a CONNECTED same-account interval", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "connected", "connected", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			_, err = testPool.Exec(ctx, `update tenants set deletion_timestamp = now() where name = $1`, testTenant)
			Expect(err).ToNot(HaveOccurred())
			reads := fake.Count("GetAccount")
			_, err = billingRPC(adminCtx(), server.Update, updateRequest(created.GetObject().GetId(), testTenant, account))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition), "%v", err)
			Expect(fake.Count("GetAccount")).To(Equal(reads))
		})

		DescribeTable("fully revalidates a CONNECTED same-account interval",
			func(externalID string, duplicate bool, expectedReason string) {
				var accounts []billingfake.AccountSpec
				account := newAccount(&accounts, "connected", "connected", "ACTIVE")
				server := build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider())
				created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
				Expect(err).ToNot(HaveOccurred())
				accounts[0].ExternalID = externalID
				if duplicate {
					accounts = append(accounts, billingfake.AccountSpec{ID: "foreign", ExternalID: "connected", State: billing.AccountActive})
				}
				fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
				server = build(fake.Provider())
				_, err = billingRPC(adminCtx(), server.Update, updateRequest(created.GetObject().GetId(), testTenant, account))
				Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
				Expect(reason(err)).To(Equal(expectedReason))
				Expect(fake.Count("SetCorrelationValue")).To(BeZero())
				var count int
				Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments where effective_to is null`).Scan(&count)).To(Succeed())
				Expect(count).To(Equal(1))
			},
			Entry("detects provider mismatch", "changed", false, "BILLING_CORRELATION_ID_CONFLICT"),
			Entry("detects duplicate foreign carrier", "connected", true, "ACCOUNT_AMBIGUOUS"),
		)
		It("closes the old interval and opens a new one atomically on reassignment", func() {
			var accounts []billingfake.AccountSpec
			account1 := newAccount(&accounts, "", "", "ACTIVE")
			account2 := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account1, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			firstID := created.GetObject().GetId()

			updated, err := billingRPC(adminCtx(), server.Update, updateRequest(firstID, testTenant, account2))
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetId()).ToNot(Equal(firstID))
			Expect(updated.GetObject().GetSpec().GetBillingAccount().GetId()).To(Equal(account2))

			closedRow, getErr := server.bindings.Get().SetId(firstID).Do(ctx)
			Expect(getErr).ToNot(HaveOccurred())
			Expect(closedRow.GetObject().GetStatus().GetEffectiveToTimestamp()).ToNot(BeNil())

			active, getErr := server.bindings.List().SetLimit(1).SetFilter(`this.spec.tenant.id == "test-tenant" && !has(this.status.effective_to_timestamp)`).Do(ctx)
			Expect(getErr).ToNot(HaveOccurred())
			Expect(active.GetItems()[0].GetId()).To(Equal(updated.GetObject().GetId()))
			Expect(active.GetItems()[0].GetStatus().GetEffectiveFromTimestamp().AsTime().Equal(closedRow.GetObject().GetStatus().GetEffectiveToTimestamp().AsTime())).To(BeTrue())
		})

		It("re-runs resolution for a PENDING assignment resubmitted for the same tenant and account", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(created.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING))
			pendingID := created.GetObject().GetId()

			// The admin sets the provider's externalId to the OSAC-stored value and resubmits Update. The fake
			// is rebuilt with that value preset, standing in for the out-of-band provider change.
			accountRow, getErr := server.accounts.Get().SetId(account).Do(ctx)
			Expect(getErr).ToNot(HaveOccurred())
			fake2 := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
				{ID: account, ExternalID: accountRow.GetObject().GetStatus().GetExternalBillingAccountId(), State: billing.AccountActive},
			}})
			server2 := build(fake2.Provider())

			updated, err := billingRPC(adminCtx(), server2.Update, updateRequest(pendingID, testTenant, account))
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetId()).To(Equal(pendingID))
			Expect(proto.Equal(updated.GetObject().GetStatus().GetEffectiveFromTimestamp(), created.GetObject().GetStatus().GetEffectiveFromTimestamp())).To(BeTrue())
			Expect(updated.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))

			closedRow, getErr := server.bindings.Get().SetId(pendingID).Do(ctx)
			Expect(getErr).ToNot(HaveOccurred())
			Expect(closedRow.GetObject().GetStatus().GetEffectiveToTimestamp()).To(BeNil())
		})

		It("revalidates the same account on a CONNECTED assignment without changing history", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "connected", "connected", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			connectedID := created.GetObject().GetId()
			reads := fake.Count("GetAccount")

			updated, err := billingRPC(adminCtx(), server.Update, updateRequest(connectedID, testTenant, account))
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetId()).To(Equal(connectedID), "no new interval must be created")
			Expect(fake.Count("GetAccount")).To(Equal(reads + 1))
			Expect(proto.Equal(updated.GetObject().GetStatus().GetEffectiveFromTimestamp(), created.GetObject().GetStatus().GetEffectiveFromTimestamp())).To(BeTrue())

			page, listErr := server.bindings.List().SetLimit(10).SetFilter(`this.spec.tenant.id == "` + testTenant + `"`).Do(ctx)
			Expect(listErr).ToNot(HaveOccurred())
			Expect(page.GetTotal()).To(Equal(int32(1)))
		})

		It("reads the selected account's current provider state on same-account revalidation", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "corr-live", "corr-live", "ACTIVE")
			created, err := billingRPC(adminCtx(), build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider()).Create,
				createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())

			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			updated, err := billingRPC(adminCtx(), build(fake.Provider()).Update,
				updateRequest(created.GetObject().GetId(), testTenant, account))
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetId()).To(Equal(created.GetObject().GetId()))
			Expect(fake.Count("GetAccount")).To(BeNumerically(">", 0),
				"revalidation must observe the provider state, not only the stored one")
		})

		DescribeTable("rejects a provider-side ineligible state and keeps the prior interval current then recovers",
			func(replacement bool, providerState billing.AccountState, expectedReason string) {
				var accounts []billingfake.AccountSpec
				first := newAccount(&accounts, "corr-first", "corr-first", "ACTIVE")
				second := newAccount(&accounts, "corr-second", "corr-second", "ACTIVE")
				created, err := billingRPC(adminCtx(), build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider()).Create,
					createRequest(testTenant, first, "asg-1"))
				Expect(err).ToNot(HaveOccurred())
				priorID := created.GetObject().GetId()

				target, index := first, 0
				if replacement {
					target, index = second, 1
				}
				accounts[index].State = providerState
				fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
				server := build(fake.Provider())
				_, err = billingRPC(adminCtx(), server.Update, updateRequest(priorID, testTenant, target))
				Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
				Expect(reason(err)).To(Equal(expectedReason))
				Expect(fake.Count("CorrelationValue")).To(BeZero())
				Expect(fake.Count("SetCorrelationValue")).To(BeZero())

				prior, getErr := server.bindings.Get().SetId(priorID).Do(ctx)
				Expect(getErr).ToNot(HaveOccurred())
				Expect(prior.GetObject().GetStatus().GetEffectiveToTimestamp()).To(BeNil(), "the prior interval must remain the current one")
				Expect(prior.GetObject().GetSpec().GetBillingAccount().GetId()).To(Equal(first))
				Expect(prior.GetObject().GetStatus().GetEffectiveFromTimestamp().AsTime()).To(BeTemporally("==", created.GetObject().GetStatus().GetEffectiveFromTimestamp().AsTime()))
				page, listErr := server.bindings.List().SetLimit(10).SetFilter(`this.spec.tenant.id == "` + testTenant + `"`).Do(ctx)
				Expect(listErr).ToNot(HaveOccurred())
				Expect(page.GetTotal()).To(Equal(int32(1)), "a rejected assignment must not add history")

				accounts[index].State = billing.AccountActive
				recovered, err := billingRPC(adminCtx(), build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider()).Update,
					updateRequest(priorID, testTenant, target))
				Expect(err).ToNot(HaveOccurred())
				Expect(recovered.GetObject().GetSpec().GetBillingAccount().GetId()).To(Equal(target))
				Expect(recovered.GetObject().GetStatus().GetConnectionState()).To(Equal(
					privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
				prior, getErr = server.bindings.Get().SetId(priorID).Do(ctx)
				Expect(getErr).ToNot(HaveOccurred())
				page, listErr = server.bindings.List().SetLimit(10).SetFilter(`this.spec.tenant.id == "` + testTenant + `"`).Do(ctx)
				Expect(listErr).ToNot(HaveOccurred())
				if replacement {
					Expect(recovered.GetObject().GetId()).NotTo(Equal(priorID))
					Expect(prior.GetObject().GetStatus().GetEffectiveToTimestamp()).NotTo(BeNil())
					Expect(prior.GetObject().GetStatus().GetEffectiveToTimestamp().AsTime()).To(BeTemporally("==", recovered.GetObject().GetStatus().GetEffectiveFromTimestamp().AsTime()))
					Expect(page.GetTotal()).To(Equal(int32(2)))
				} else {
					Expect(recovered.GetObject().GetId()).To(Equal(priorID))
					Expect(prior.GetObject().GetStatus().GetEffectiveToTimestamp()).To(BeNil())
					Expect(proto.Equal(recovered.GetObject().GetStatus().GetEffectiveFromTimestamp(),
						created.GetObject().GetStatus().GetEffectiveFromTimestamp())).To(BeTrue())
					Expect(page.GetTotal()).To(Equal(int32(1)))
				}
			},
			Entry("INACTIVE same-account revalidation", false, billing.AccountInactive, "ACCOUNT_INACTIVE"),
			Entry("INACTIVE replacement of the current account", true, billing.AccountInactive, "ACCOUNT_INACTIVE"),
			Entry("UNKNOWN same-account revalidation", false, billing.AccountUnknown, "ACCOUNT_STATE_UNKNOWN"),
			Entry("UNKNOWN replacement of the current account", true, billing.AccountUnknown, "ACCOUNT_STATE_UNKNOWN"),
		)

		It("leaves the prior interval current when the provider-state read fails", func() {
			var accounts []billingfake.AccountSpec
			first := newAccount(&accounts, "corr-first", "corr-first", "ACTIVE")
			second := newAccount(&accounts, "corr-second", "corr-second", "ACTIVE")
			created, err := billingRPC(adminCtx(), build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider()).Create,
				createRequest(testTenant, first, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			priorID := created.GetObject().GetId()

			fake := billingfake.New(billingfake.Scenario{
				Accounts: accounts,
				Failures: map[string]billingfake.Failure{"GetAccount": {Reason: billing.ReasonUnavailable}},
			})
			server := build(fake.Provider())
			_, err = billingRPC(adminCtx(), server.Update, updateRequest(priorID, testTenant, second))
			Expect(status.Code(err)).To(Equal(codes.Unavailable))

			prior, getErr := server.bindings.Get().SetId(priorID).Do(ctx)
			Expect(getErr).ToNot(HaveOccurred())
			Expect(prior.GetObject().GetStatus().GetEffectiveToTimestamp()).To(BeNil())
			Expect(prior.GetObject().GetSpec().GetBillingAccount().GetId()).To(Equal(first))
			page, listErr := server.bindings.List().SetLimit(10).SetFilter(`this.spec.tenant.id == "` + testTenant + `"`).Do(ctx)
			Expect(listErr).ToNot(HaveOccurred())
			Expect(page.GetTotal()).To(Equal(int32(1)))
		})

		It("hazard: fails with BILLING_CORRELATION_ID_CONFLICT when the provider value was set to something else, "+
			"leaving both sides unchanged", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			pendingID := created.GetObject().GetId()

			fake2 := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
				{ID: account, ExternalID: "a-different-value", State: billing.AccountActive},
			}})
			server2 := build(fake2.Provider())

			_, err = billingRPC(adminCtx(), server2.Update, updateRequest(pendingID, testTenant, account))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("BILLING_CORRELATION_ID_CONFLICT"))

			unchanged, getErr := server.bindings.Get().SetId(pendingID).Do(ctx)
			Expect(getErr).ToNot(HaveOccurred())
			Expect(unchanged.GetObject().GetStatus().GetEffectiveToTimestamp()).To(BeNil(), "the PENDING interval must remain the current one")
		})

		It("rejects changing spec.tenant_id", func() {
			createOtherTenant()
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())

			request := privatev1.TenantBillingAssignmentsUpdateRequest_builder{
				Object: privatev1.TenantBillingAssignment_builder{
					Id: created.GetObject().GetId(),
					Spec: privatev1.TenantBillingAssignmentSpec_builder{
						Tenant: privatev1.TenantReference_builder{Id: otherTenant}.Build(), BillingAccount: privatev1.BillingAccountReference_builder{Id: account}.Build(),
					}.Build(),
				}.Build(),
			}.Build()
			_, err = billingRPC(adminCtx(), server.Update, request)
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("returns NotFound for an unknown id", func() {
			fake := billingfake.New(billingfake.Scenario{})
			server := build(fake.Provider())
			_, err := billingRPC(adminCtx(), server.Update, updateRequest("no-such-id", testTenant, "any"))
			Expect(status.Code(err)).To(Equal(codes.NotFound))
		})
	})

	Describe("ordinary ambient transactions and locked finalization", func() {
		It("keeps the unlocked preflight valid in a read-only PostgreSQL transaction", func() {
			var accounts []billingfake.AccountSpec
			id := newAccount(&accounts, "exact", "exact", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			_, err := database.WithNewTx(adminCtx(), func(c context.Context) (tenantBillingAssignmentSnapshot, error) {
				tx, err := database.TxFromContext(c)
				if err != nil {
					return tenantBillingAssignmentSnapshot{}, err
				}
				if _, err = tx.Exec(c, `set transaction read only`); err != nil {
					return tenantBillingAssignmentSnapshot{}, err
				}
				return server.assignmentSnapshot(c, id, "", tenantBillingAssignmentAssignInput{TenantID: testTenant}, nil)
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(fake.Calls()).To(BeEmpty())
		})

		It("locks the previous account before the interval and waits without changing history", func() {
			var accounts []billingfake.AccountSpec
			first := newAccount(&accounts, "first", "first", "ACTIVE")
			second := newAccount(&accounts, "second", "second", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			created, err := billingRPC(adminCtx(), build(fake.Provider()).Create, createRequest(testTenant, first, "lock-set"))
			Expect(err).NotTo(HaveOccurred())
			adapter := &gatedCorrelationAdapter{Fake: fake, entered: make(chan struct{}), release: make(chan struct{})}
			server := build(billing.NewProvider(billingfake.Key, adapter, nil))
			lockCtx, cancelLocks := context.WithTimeout(ctx, 20*time.Second)
			DeferCleanup(cancelLocks)
			blocker, err := tm.Begin(lockCtx)
			if err == nil {
				DeferCleanup(func() {
					if blocker != nil {
						cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						Expect(blocker.End(cleanupCtx)).To(Succeed())
					}
				})
			}
			Expect(err).NotTo(HaveOccurred())

			requestCtx, cancelRequest := context.WithTimeout(adminCtx(), 20*time.Second)
			result := make(chan error, 1)
			done := make(chan struct{})
			// Cancel and join the request before the blocker or the suite pool is cleaned up.
			DeferCleanup(func() {
				cancelRequest()
				<-done
			})
			go func() {
				defer close(done)
				_, err := billingRPC(requestCtx, server.Update, updateRequest(created.GetObject().GetId(), testTenant, second))
				result <- err
			}()
			Eventually(adapter.entered, 5*time.Second).Should(BeClosed())
			var blockerPID int32
			Expect(blocker.QueryRow(lockCtx, `select pg_backend_pid()`).Scan(&blockerPID)).To(Succeed())
			_, err = blocker.Exec(lockCtx, `select id from billing_accounts where id = $1 for update`, first)
			Expect(err).NotTo(HaveOccurred())
			close(adapter.release)
			probeCtx, cancelProbe := context.WithTimeout(lockCtx, 5*time.Second)
			defer cancelProbe()
			Eventually(func() (bool, error) {
				var waiting bool
				err := testPool.QueryRow(probeCtx, `select exists(select 1 from pg_stat_activity
					where datname = current_database() and wait_event_type = 'Lock'
					and query like '%from billing_accounts%order by id for update%'
					and $1 = any(pg_blocking_pids(pid)))`, blockerPID).Scan(&waiting)
				return waiting, err
			}, 5*time.Second).Should(BeTrue(), "finalization must lock the previous account too")
			Expect(result).NotTo(Receive())
			Expect(tm.Run(lockCtx, func(tx database.Tx) error {
				_, err := tx.Exec(lockCtx, `select id from tenant_billing_assignments where id = $1 for update nowait`, created.GetObject().GetId())
				return err
			})).To(Succeed(), "account locks precede interval locks")
			var afterLockWait time.Time
			Expect(testPool.QueryRow(lockCtx, `select clock_timestamp()`).Scan(&afterLockWait)).To(Succeed())
			err = blocker.End(lockCtx)
			blocker = nil
			Expect(err).NotTo(HaveOccurred())
			var updateErr error
			Eventually(result, 5*time.Second).Should(Receive(&updateErr))
			Expect(updateErr).NotTo(HaveOccurred())
			var oldEnd, newStart time.Time
			Expect(testPool.QueryRow(ctx, `select effective_to from tenant_billing_assignments where id = $1`, created.GetObject().GetId()).Scan(&oldEnd)).To(Succeed())
			Expect(testPool.QueryRow(ctx, `select effective_from from tenant_billing_assignments where effective_to is null`).Scan(&newStart)).To(Succeed())
			Expect(oldEnd).To(Equal(newStart))
			Expect(newStart).To(BeTemporally(">=", afterLockWait), "the clock must be sampled after both provider I/O and lock waits")
		})

		DescribeTable("rejects changed current-interval evidence without a late previous-account lock", func(replaceCurrent bool) {
			const first, selected, lower = "b-original", "c-selected", "a-replacement"
			accounts := []billingfake.AccountSpec{
				{ID: first, ExternalID: "first", State: billing.AccountActive},
				{ID: selected, ExternalID: "selected", State: billing.AccountActive},
				{ID: lower, ExternalID: "lower", State: billing.AccountActive},
			}
			seedAccountRow(first, "first", "ACTIVE")
			seedAccountRow(selected, "", "ACTIVE")
			seedAccountRow(lower, "lower", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			created, err := billingRPC(adminCtx(), build(fake.Provider()).Create, createRequest(testTenant, first, "original"))
			Expect(err).NotTo(HaveOccurred())
			adapter := &gatedAccountScanAdapter{Fake: fake, entered: make(chan struct{}), release: make(chan struct{})}
			server := build(billing.NewProvider(billingfake.Key, adapter, nil))
			result := make(chan error, 1)
			go func() {
				_, err := billingRPC(adminCtx(), server.Update, updateRequest(created.GetObject().GetId(), testTenant, selected))
				result <- err
			}()
			var once sync.Once
			release := func() { once.Do(func() { close(adapter.release) }) }
			DeferCleanup(release)
			Eventually(adapter.entered, 5*time.Second).Should(BeClosed())
			if replaceCurrent {
				_, err = billingRPC(adminCtx(), build(fake.Provider()).Update, updateRequest(created.GetObject().GetId(), testTenant, lower))
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(tm.Run(ctx, func(tx database.Tx) error {
					c := database.TxIntoContext(adminCtx(), tx)
					stored, err := server.assignments.Get().SetId(created.GetObject().GetId()).Do(c)
					if err != nil {
						return err
					}
					stored.GetObject().GetStatus().SetMessage("concurrent revalidation")
					_, err = server.assignments.Update().SetObject(stored.GetObject()).Do(c)
					return err
				})).To(Succeed())
			}
			blocker, err := tm.Begin(ctx)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { Expect(blocker.End(ctx)).To(Succeed()) })
			_, err = blocker.Exec(ctx, `select id from billing_accounts where id = $1 for update`, lower)
			Expect(err).NotTo(HaveOccurred())
			release()
			var finalErr error
			Eventually(result, 5*time.Second).Should(Receive(&finalErr), "changed evidence must abort, not lock the newly discovered lower-id account")
			Expect(status.Code(finalErr)).To(Equal(codes.Aborted))
			Expect(reason(finalErr)).To(Equal(billingOperationRetryReason))
			var token *string
			Expect(testPool.QueryRow(ctx, `select external_billing_account_id from billing_accounts where id = $1`, selected).Scan(&token)).To(Succeed())
			Expect(token).To(BeNil())
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments where effective_to is null`).Scan(&count)).To(Succeed())
			Expect(count).To(Equal(1))
			Expect(fake.Count("SetCorrelationValue")).To(BeZero())
		}, Entry("current interval version changed", false), Entry("current interval replaced with a lower-id previous account", true))
		DescribeTable("samples finalization after provider I/O and honors caller cancellation", func(cancelRequest bool) {
			var accounts []billingfake.AccountSpec
			id := newAccount(&accounts, "exact", "exact", "ACTIVE")
			adapter := &gatedAccountReadAdapter{Fake: billingfake.New(billingfake.Scenario{Accounts: accounts}),
				entered: make(chan struct{}), release: make(chan struct{})}
			server := build(billing.NewProvider(billingfake.Key, adapter, nil))
			c, cancel := context.WithCancel(adminCtx())
			DeferCleanup(cancel)
			result := make(chan error, 1)
			var created *privatev1.TenantBillingAssignment
			go func() {
				r, err := billingRPC(c, server.Create, createRequest(testTenant, id, "clock-and-cancel"))
				if r != nil {
					created = r.GetObject()
				}
				result <- err
			}()
			Eventually(adapter.entered, 5*time.Second).Should(BeClosed())
			var afterProviderStarted time.Time
			Expect(testPool.QueryRow(ctx, `select clock_timestamp()`).Scan(&afterProviderStarted)).To(Succeed())
			if cancelRequest {
				cancel()
			} else {
				close(adapter.release)
			}
			var err error
			Eventually(result, 5*time.Second).Should(Receive(&err))
			if cancelRequest {
				Expect(status.Code(err)).To(Equal(codes.Canceled))
				var count int
				Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
				Expect(count).To(BeZero())
			} else {
				Expect(err).NotTo(HaveOccurred())
				Expect(created.GetStatus().GetEffectiveFromTimestamp().AsTime()).To(BeTemporally(">=", afterProviderStarted))
			}
			Expect(server.assignSem).To(BeEmpty())
		}, Entry("post-I/O database clock", false), Entry("caller cancellation", true))

		It("rejects a current interval appearing after an initially absent snapshot without adopting the selected token", func() {
			var accounts []billingfake.AccountSpec
			id := newAccount(&accounts, "selected", "", "ACTIVE")
			other := newAccount(&accounts, "other", "other", "ACTIVE")
			adapter := &gatedCorrelationAdapter{Fake: billingfake.New(billingfake.Scenario{Accounts: accounts}),
				entered: make(chan struct{}), release: make(chan struct{})}
			server := build(billing.NewProvider(billingfake.Key, adapter, nil))
			result := make(chan error, 1)
			go func() {
				_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, id, "absence-race"))
				result <- err
			}()
			DeferCleanup(func() {
				select {
				case <-adapter.release:
				default:
					close(adapter.release)
				}
			})
			Eventually(adapter.entered, 5*time.Second).Should(BeClosed())
			seedHistoryInterval("concurrent-current", testTenant, other, time.Now().UTC().Truncate(time.Microsecond), nil)
			close(adapter.release)
			var err error
			Eventually(result, 5*time.Second).Should(Receive(&err))
			Expect(status.Code(err)).To(Equal(codes.Aborted))
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(Equal(1))
			var token *string
			Expect(testPool.QueryRow(ctx, `select external_billing_account_id from billing_accounts where id = $1`, id).Scan(&token)).To(Succeed())
			Expect(token).To(BeNil())
		})
		It("commits Tenant Delete during gated provider I/O and rejects assignment at the liveness commit fence", func() {
			const tenantName = "gated-delete-tenant"
			tenantsServer, err := NewPrivateTenantsServer().SetLogger(logger).
				SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = database.WithNewTx(adminCtx(), func(txCtx context.Context) (*privatev1.TenantsCreateResponse, error) {
				return tenantsServer.Create(txCtx, privatev1.TenantsCreateRequest_builder{
					Object: privatev1.Tenant_builder{
						Metadata: privatev1.Metadata_builder{
							Name: tenantName, Finalizers: []string{finalizers.TenantLifecycle},
						}.Build(),
					}.Build(),
				}.Build())
			})
			Expect(err).ToNot(HaveOccurred())
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "provider-correlation", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			adapter := &gatedCorrelationAdapter{Fake: fake, entered: make(chan struct{}), release: make(chan struct{})}
			server := build(billing.NewProvider(billingfake.Key, adapter, nil))

			// The ordinary request occupies a connection but holds no business locks during provider I/O.
			rpcCtx := adminCtx()
			var once sync.Once
			release := func() { once.Do(func() { close(adapter.release) }) }
			DeferCleanup(release)
			result := make(chan error, 1)
			go func() {
				_, err := billingRPC(rpcCtx, server.Create, createRequest(tenantName, account, "raced-assignment"))
				result <- err
			}()
			Eventually(adapter.entered, 5*time.Second).Should(BeClosed())

			deleteCtx, cancel := context.WithTimeout(rpcCtx, 5*time.Second)
			defer cancel()
			_, err = database.WithNewTx(deleteCtx, func(txCtx context.Context) (*privatev1.TenantsDeleteResponse, error) {
				return tenantsServer.Delete(txCtx, privatev1.TenantsDeleteRequest_builder{Id: tenantName}.Build())
			})
			Expect(err).ToNot(HaveOccurred(), "Tenant Delete must commit before the provider gate opens")
			var deletedAt time.Time
			Expect(testPool.QueryRow(ctx, `select deletion_timestamp from tenants where name = $1`, tenantName).
				Scan(&deletedAt)).To(Succeed())
			Expect(deletedAt.After(time.Unix(1, 0))).To(BeTrue(), "an independent connection must see the committed delete")
			Expect(adapter.release).NotTo(BeClosed())
			Expect(result).NotTo(Receive())

			release()
			var finalErr error
			Eventually(result, 5*time.Second).Should(Receive(&finalErr))
			Expect(status.Code(finalErr)).To(Equal(codes.InvalidArgument))
			Expect(status.Convert(finalErr).Message()).To(ContainSubstring("spec.tenant does not reference an existing tenant"))
			Expect(fake.Count("ListAccounts")).To(Equal(1), "provider resolution completed after the delete")
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
			var correlation string
			Expect(testPool.QueryRow(ctx, `select coalesce(external_billing_account_id, '') from billing_accounts where id = $1`, account).
				Scan(&correlation)).To(Succeed())
			Expect(correlation).To(BeEmpty(), "rejected finalization must not persist provider correlation adoption")
			Expect(server.assignSem).To(BeEmpty())
		})

		It("rolls back token, observation and interval changes when the outer interceptor commit fails", func() {
			var accounts []billingfake.AccountSpec
			first := newAccount(&accounts, "", "", "ACTIVE")
			second := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, first, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			_, err = testPool.Exec(ctx, `
				create function reject_billing_assignment_commit() returns trigger language plpgsql as $$
				begin raise exception 'injected billing commit failure'; end;
				$$;
				create constraint trigger reject_billing_assignment_commit
				after insert on tenant_billing_assignments deferrable initially deferred
				for each row execute function reject_billing_assignment_commit();`)
			Expect(err).ToNot(HaveOccurred())
			var beforeObservation time.Time
			Expect(testPool.QueryRow(ctx, `select status_observed_at from billing_accounts where id = $1`, second).Scan(&beforeObservation)).To(Succeed())
			_, err = billingRPC(adminCtx(), server.Update, updateRequest(created.GetObject().GetId(), testTenant, second))
			Expect(status.Code(err)).To(Equal(codes.Internal))
			Expect(err.Error()).ToNot(ContainSubstring("injected"))
			var activeID string
			Expect(testPool.QueryRow(ctx, `select id from tenant_billing_assignments where effective_to is null`).Scan(&activeID)).To(Succeed())
			Expect(activeID).To(Equal(created.GetObject().GetId()))
			var correlation string
			Expect(testPool.QueryRow(ctx, `select coalesce(external_billing_account_id, '') from billing_accounts where id = $1`, second).Scan(&correlation)).To(Succeed())
			Expect(correlation).To(BeEmpty())
			var afterObservation time.Time
			Expect(testPool.QueryRow(ctx, `select status_observed_at from billing_accounts where id = $1`, second).Scan(&afterObservation)).To(Succeed())
			Expect(afterObservation).To(BeTemporally("==", beforeObservation), "active evidence must roll back with token and interval changes")
			providerValue, present, err := fake.CorrelationValue(ctx, billing.NewAccountRef(second))
			Expect(err).ToNot(HaveOccurred())
			Expect(present).To(BeFalse())
			Expect(providerValue).To(BeEmpty())
			Expect(fake.Count("SetCorrelationValue")).To(BeZero())
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(Equal(1))
			Expect(server.assignSem).To(BeEmpty())
		})

		DescribeTable("aborts atomically without retrying provider reads on PostgreSQL conflict errors", func(sqlstate string) {
			var accounts []billingfake.AccountSpec
			id := newAccount(&accounts, "provider-token", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			var before string
			Expect(testPool.QueryRow(ctx, `select to_jsonb(b)::text from billing_accounts b where id = $1`, id).Scan(&before)).To(Succeed())
			// Inject a real PostgreSQL SQLSTATE at the insert boundary, after account observation/token mutation.
			// The row-lock ordering races are separate fixtures; this deliberately does not claim a real deadlock cycle.
			_, err := testPool.Exec(ctx, fmt.Sprintf(`create function reject_assignment_conflict() returns trigger language plpgsql as $$
			begin raise exception 'private conflict detail' using errcode = '%s'; end $$;
			create trigger reject_assignment_conflict before insert on tenant_billing_assignments
			for each row execute function reject_assignment_conflict()`, sqlstate))
			Expect(err).NotTo(HaveOccurred())
			_, err = billingRPC(adminCtx(), server.Create, createRequest(testTenant, id, "sql-conflict"))
			Expect(status.Code(err)).To(Equal(codes.Aborted))
			Expect(reason(err)).To(Equal(billingOperationRetryReason))
			Expect(err.Error()).NotTo(ContainSubstring("private conflict detail"))
			var after string
			Expect(testPool.QueryRow(ctx, `select to_jsonb(b)::text from billing_accounts b where id = $1`, id).Scan(&after)).To(Succeed())
			Expect(after).To(Equal(before))
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
			Expect(fake.Count("GetAccount")).To(Equal(1))
			Expect(fake.Count("CorrelationValue")).To(BeZero())
			Expect(fake.Count("ListAccounts")).To(Equal(1))
			Expect(server.assignSem).To(BeEmpty())
		}, Entry("deadlock SQLSTATE", "40P01"), Entry("serialization SQLSTATE", "40001"))

		It("settles cross-account correlation races through final SQL uniqueness across server instances", func() {
			createOtherTenant()
			var accounts []billingfake.AccountSpec
			first := newAccount(&accounts, "shared-candidate", "", "ACTIVE")
			second := newAccount(&accounts, "shared-candidate", "", "ACTIVE")
			// Each provider view is individually consistent but stale relative to the other. Both must finish their
			// read-only evidence; the final unique index, not a pre-I/O reservation, decides adoption.
			firstFake := billingfake.New(billingfake.Scenario{Accounts: accounts[:1]})
			secondFake := billingfake.New(billingfake.Scenario{Accounts: accounts[1:]})
			adapter := &gatedAccountScanAdapter{Fake: firstFake, entered: make(chan struct{}), release: make(chan struct{})}
			server1 := build(billing.NewProvider(billingfake.Key, adapter, nil))
			server2 := build(secondFake.Provider())
			result := make(chan error, 1)
			go func() {
				_, err := billingRPC(adminCtx(), server1.Create, createRequest(testTenant, first, "asg-1"))
				result <- err
			}()
			DeferCleanup(func() {
				select {
				case <-adapter.release:
				default:
					close(adapter.release)
				}
			})
			Eventually(adapter.entered, 5*time.Second).Should(BeClosed())
			_, err := billingRPC(adminCtx(), server2.Create, createRequest(otherTenant, second, "asg-2"))
			Expect(err).NotTo(HaveOccurred())
			Expect(secondFake.Count("ListAccounts")).To(Equal(1))
			Expect(secondFake.Count("SetCorrelationValue")).To(BeZero())
			close(adapter.release)
			var firstErr error
			Eventually(result, 5*time.Second).Should(Receive(&firstErr))
			Expect(status.Code(firstErr)).To(Equal(codes.FailedPrecondition))
			Expect(reason(firstErr)).To(Equal("ACCOUNT_CORRELATION_CONFLICT"))
			var token *string
			Expect(testPool.QueryRow(ctx, `select external_billing_account_id from billing_accounts where id = $1`, first).Scan(&token)).To(Succeed())
			Expect(token).To(BeNil())
		})

		DescribeTable("releases the ordinary connection and admission after bounded provider cancellation", func(gate, cutoff string) {
			childTimeout := cutoff == "provider"
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			config := testPool.Config()
			config.MaxConns = 1
			config.MinConns = 0
			pool, err := pgxpool.NewWithConfig(ctx, config)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(pool.Close)
			manager, err := database.NewTxManager().SetLogger(logger).SetPool(pool).Build()
			Expect(err).ToNot(HaveOccurred())
			rpcCtx := auth.ContextWithSubject(database.TxManagerIntoContext(context.Background(), manager),
				&auth.Subject{User: "admin", Tenants: auth.AllTenants})
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			entered, release := make(chan struct{}), make(chan struct{})
			var adapter billing.Adapter
			switch gate {
			case "state-read":
				adapter = &gatedAccountReadAdapter{Fake: fake, entered: entered, release: release}
			case "scan":
				adapter = &gatedAccountScanAdapter{Fake: fake, entered: entered, release: release}
			default:
				Fail("unknown provider cancellation gate: " + gate)
			}
			server := build(billing.NewProvider(billingfake.Key, adapter, nil))
			if childTimeout {
				server.assignProviderDeadline = 250 * time.Millisecond
			}
			var cancelRPC context.CancelFunc
			if cutoff == "caller" {
				rpcCtx, cancelRPC = context.WithTimeout(rpcCtx, 2*time.Second)
			} else {
				rpcCtx, cancelRPC = context.WithCancel(rpcCtx)
			}
			DeferCleanup(cancelRPC)
			result := make(chan error, 1)
			go func() {
				_, err := billingRPC(rpcCtx, server.Create, createRequest(testTenant, account, "asg-1"))
				result <- err
			}()
			DeferCleanup(func() {
				select {
				case <-release:
				default:
					close(release)
				}
			})
			Eventually(entered, 5*time.Second).Should(BeClosed())
			Expect(pool.Stat().AcquiredConns()).To(Equal(int32(1)), "ordinary preflight has checked out the connection")
			// Provider reads must not hold tenant or account business locks, even though the connection is occupied.
			Expect(tm.Run(ctx, func(tx database.Tx) error {
				_, err := tx.Exec(ctx, `select id from tenants where id = $1 for update nowait`, testTenant)
				if err != nil {
					return err
				}
				_, err = tx.Exec(ctx, `select id from billing_accounts where id = $1 for update nowait`, account)
				return err
			})).To(Succeed())
			if cutoff == "cancel" {
				cancelRPC()
			}
			var createErr error
			Eventually(result, 5*time.Second).Should(Receive(&createErr))
			if childTimeout {
				Expect(status.Code(createErr)).To(Equal(codes.Unavailable))
				Expect(reason(createErr)).To(Equal("TIMEOUT"))
			} else if cutoff == "caller" {
				Expect(status.Code(createErr)).To(Equal(codes.DeadlineExceeded))
			} else {
				Expect(status.Code(createErr)).To(Equal(codes.Canceled))
			}
			Expect(server.assignSem).To(BeEmpty())
			readCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var one int
			Expect(pool.QueryRow(readCtx, `select 1`).Scan(&one)).To(Succeed())
			Expect(one).To(Equal(1))
			server.provider = fake.Provider()
			live := auth.ContextWithSubject(database.TxManagerIntoContext(readCtx, manager), &auth.Subject{User: "admin", Tenants: auth.AllTenants})
			_, err = billingRPC(live, server.Create, createRequest(testTenant, account, "retry"))
			Expect(err).NotTo(HaveOccurred())
		},
			Entry("during the selected-account state read", "state-read", "cancel"),
			Entry("during the exact carrier walk", "scan", "cancel"),
			Entry("provider-child state timeout", "state-read", "provider"),
			Entry("provider-child exact-carrier timeout", "scan", "provider"),
			Entry("caller deadline, not a provider-child timeout", "scan", "caller"),
		)

		It("keeps bounded provider admission independent of cross-instance resource contention", func() {
			createOtherTenant()
			var accounts []billingfake.AccountSpec
			first := newAccount(&accounts, "", "", "ACTIVE")
			second := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			adapter := &gatedCorrelationAdapter{Fake: fake, entered: make(chan struct{}), release: make(chan struct{}), blockedRef: first}
			provider := billing.NewProvider(billingfake.Key, adapter, nil)
			server1, server2 := build(provider), build(provider)
			result := make(chan error, 1)
			go func() {
				_, err := billingRPC(adminCtx(), server1.Create, createRequest(testTenant, first, "asg-1"))
				result <- err
			}()
			DeferCleanup(func() {
				select {
				case <-adapter.release:
				default:
					close(adapter.release)
				}
			})
			Eventually(adapter.entered, 5*time.Second).Should(BeClosed())
			// There is no resource lease before provider work. A full local provider window rejects all contenders.
			for range maxConcurrentAssigns {
				server2.assignSem <- struct{}{}
			}
			for _, request := range []*privatev1.TenantBillingAssignmentsCreateRequest{
				createRequest(otherTenant, first, "account-contender"),
				createRequest(testTenant, second, "tenant-contender"),
			} {
				contender := make(chan error, 1)
				go func() { _, err := billingRPC(adminCtx(), server2.Create, request); contender <- err }()
				var err error
				Eventually(contender, time.Second).Should(Receive(&err))
				Expect(status.Code(err)).To(Equal(codes.ResourceExhausted))
				Expect(server2.assignSem).To(HaveLen(maxConcurrentAssigns))
			}
			for range maxConcurrentAssigns {
				<-server2.assignSem
			}
			_, err := billingRPC(adminCtx(), server2.Create, createRequest(otherTenant, second, "unrelated"))
			Expect(err).ToNot(HaveOccurred())
			close(adapter.release)
			var firstErr error
			Eventually(result, 5*time.Second).Should(Receive(&firstErr))
			Expect(firstErr).ToNot(HaveOccurred())
		})

		It("rejects a delayed read when another instance commits the same tenant and account first", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			adapter := &gatedCorrelationAdapter{Fake: fake, entered: make(chan struct{}), release: make(chan struct{})}
			server := build(billing.NewProvider(billingfake.Key, adapter, nil))
			result := make(chan error, 1)
			go func() {
				_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
				result <- err
			}()
			DeferCleanup(func() {
				select {
				case <-adapter.release:
				default:
					close(adapter.release)
				}
			})
			Eventually(adapter.entered, 5*time.Second).Should(BeClosed())
			_, err := billingRPC(adminCtx(), build(fake.Provider()).Create, createRequest(testTenant, account, "winner"))
			Expect(err).NotTo(HaveOccurred())
			close(adapter.release)
			var staleErr error
			Eventually(result, 5*time.Second).Should(Receive(&staleErr))
			Expect(status.Code(staleErr)).To(Equal(codes.Aborted))
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(Equal(1))
			Expect(fake.Count("SetCorrelationValue")).To(BeZero())
		})

		DescribeTable("revalidates authoritative local state after provider I/O", func(change string, expected codes.Code) {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			adapter := &gatedCorrelationAdapter{Fake: fake, entered: make(chan struct{}), release: make(chan struct{})}
			server := build(billing.NewProvider(billingfake.Key, adapter, nil))
			result := make(chan error, 1)
			go func() {
				_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
				result <- err
			}()
			DeferCleanup(func() {
				select {
				case <-adapter.release:
				default:
					close(adapter.release)
				}
			})
			Eventually(adapter.entered, 5*time.Second).Should(BeClosed())
			if change == "tenant" {
				_, err := testPool.Exec(ctx, `update tenants set deletion_timestamp = now() where name = 'test-tenant'`)
				Expect(err).ToNot(HaveOccurred())
			} else {
				Expect(tm.Run(ctx, func(tx database.Tx) error {
					c := database.TxIntoContext(ctx, tx)
					r, err := server.accounts.Get().SetId(account).Do(c)
					if err != nil {
						return err
					}
					object := r.GetObject()
					switch change {
					case "INACTIVE", "UNKNOWN":
						object.GetStatus().SetState(mapBillingAccountState(change))
						object.GetStatus().SetStateObservationTimestamp(timestamppb.Now())
					case "reference":
						if _, err = server.accounts.Delete().SetId(account).Do(c); err != nil {
							return err
						}
						object.GetStatus().SetProviderAccountReference("changed")
						_, err = server.accounts.Create().SetObject(object).Do(c)
						return err
					case "token":
						object.GetStatus().SetExternalBillingAccountId("changed")
					case "empty-token":
						object.GetStatus().SetExternalBillingAccountId("")
					case "whitespace-token":
						object.GetStatus().SetExternalBillingAccountId(" \t")
					case "display":
						object.GetStatus().SetDisplayName("harmless display update")
					}
					_, err = server.accounts.Update().SetObject(object).Do(c)
					return err
				})).To(Succeed())
			}
			close(adapter.release)
			var finalErr error
			Eventually(result, 5*time.Second).Should(Receive(&finalErr))
			Expect(status.Code(finalErr)).To(Equal(expected))
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			if expected == codes.OK {
				Expect(count).To(Equal(1))
			} else {
				Expect(count).To(BeZero())
			}
		},
			Entry("tenant deletion", "tenant", codes.InvalidArgument),
			Entry("account inactive", "INACTIVE", codes.FailedPrecondition),
			Entry("account unknown", "UNKNOWN", codes.FailedPrecondition),
			Entry("provider reference changed", "reference", codes.Aborted),
			Entry("correlation changed", "token", codes.Aborted),
			Entry("token absence changed to present-empty", "empty-token", codes.Aborted),
			Entry("token absence changed to whitespace", "whitespace-token", codes.Aborted),
			Entry("display/version-only update is not material", "display", codes.OK),
		)
	})

	Describe("Signal", func() {
		It("connects a PENDING current interval in place", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			server := build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider())
			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			var correlation string
			Expect(testPool.QueryRow(ctx, `select external_billing_account_id from billing_accounts where id = $1`, account).Scan(&correlation)).To(Succeed())
			accounts[0].ExternalID = correlation
			server = build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider())
			_, err = billingRPC(adminCtx(), server.Signal, privatev1.TenantBillingAssignmentsSignalRequest_builder{Id: created.GetObject().GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			var state, id string
			var from time.Time
			Expect(testPool.QueryRow(ctx, `select id, effective_from, connection_state from tenant_billing_assignments
				where effective_to is null`).Scan(&id, &from, &state)).To(Succeed())
			Expect(id).To(Equal(created.GetObject().GetId()))
			Expect(from.Equal(created.GetObject().GetStatus().GetEffectiveFromTimestamp().AsTime())).To(BeTrue())
			Expect(state).To(Equal(tbaStateConnected))
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(Equal(1))
		})

		It("rejects empty, unknown, historical and disabled requests", func() {
			var accounts []billingfake.AccountSpec
			first := newAccount(&accounts, "", "", "ACTIVE")
			second := newAccount(&accounts, "", "", "ACTIVE")
			server := build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider())
			_, err := billingRPC(adminCtx(), server.Signal, &privatev1.TenantBillingAssignmentsSignalRequest{})
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			_, err = billingRPC(adminCtx(), server.Signal, privatev1.TenantBillingAssignmentsSignalRequest_builder{Id: "unknown"}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, first, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			_, err = billingRPC(adminCtx(), server.Update, updateRequest(created.GetObject().GetId(), testTenant, second))
			Expect(err).ToNot(HaveOccurred())
			_, err = billingRPC(adminCtx(), server.Signal, privatev1.TenantBillingAssignmentsSignalRequest_builder{Id: created.GetObject().GetId()}.Build())
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			disabled := build(billing.NewProvider("", nil, nil))
			_, err = billingRPC(adminCtx(), disabled.Signal, privatev1.TenantBillingAssignmentsSignalRequest_builder{Id: "any"}.Build())
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("BILLING_NOT_CONFIGURED"))
		})
	})

	Describe("trusted tenant scope", func() {
		scopedCtx := func(tenants collections.Set[string]) context.Context {
			return auth.ContextWithSubject(adminCtx(), &auth.Subject{User: "admin", Tenants: tenants})
		}
		// These contexts stand for already-authorized admin requests, not a tenant self-service grant.
		readCtx := func(tenants collections.Set[string]) context.Context {
			return auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: tenants})
		}
		seedInterval := func(id, tenantID, accountID string, historical bool) {
			from := time.Now().UTC().Truncate(time.Microsecond)
			var to *time.Time
			if historical {
				to = &from
			}
			seedHistoryInterval(id, tenantID, accountID, from, to)
		}

		DescribeTable("scopes list totals and every page, including historical rows",
			func(tenants collections.Set[string], expected int32) {
				createOtherTenant()
				var accounts []billingfake.AccountSpec
				first := newAccount(&accounts, "", "", "ACTIVE")
				second := newAccount(&accounts, "", "", "ACTIVE")
				seedInterval("a-foreign-history", otherTenant, second, true)
				seedInterval("b-own-history", testTenant, first, true)
				seedInterval("c-foreign-current", otherTenant, second, false)
				seedInterval("d-own-current", testTenant, first, false)
				fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
				server := build(fake.Provider())
				seen := map[string]bool{}
				for offset := int32(0); offset <= expected; offset++ {
					response, err := billingRPC(readCtx(tenants), server.List, privatev1.TenantBillingAssignmentsListRequest_builder{
						Offset: &offset, Limit: new(int32(1)), Order: new("id"),
					}.Build())
					Expect(err).ToNot(HaveOccurred())
					Expect(response.GetTotal()).To(Equal(expected))
					if offset == expected {
						Expect(response.GetSize()).To(BeZero())
						Expect(response.GetItems()).To(BeEmpty())
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
			Entry("finite single tenant", collections.NewSet(testTenant), int32(2)),
			Entry("finite multiple tenants", collections.NewSet(testTenant, otherTenant), int32(4)),
			Entry("universal administrator/controller", auth.AllTenants, int32(4)),
			Entry("empty tenant set", collections.NewSet[string](), int32(0)),
			Entry("zero-value tenant set", collections.Set[string]{}, int32(0)),
		)

		It("cannot widen list scope with caller-supplied tenant filters", func() {
			createOtherTenant()
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			seedInterval("foreign-history", otherTenant, account, true)
			server := build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider())
			response, err := billingRPC(readCtx(collections.NewSet(testTenant)), server.List,
				privatev1.TenantBillingAssignmentsListRequest_builder{
					Filter: new(`this.spec.tenant.id == "other-tenant" && has(this.status.effective_to_timestamp)`),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetTotal()).To(BeZero())
			Expect(response.GetItems()).To(BeEmpty())
		})

		DescribeTable("hides foreign current and historical rows before disclosing state or doing provider I/O",
			func(historical bool, tenants collections.Set[string]) {
				createOtherTenant()
				var accounts []billingfake.AccountSpec
				account := newAccount(&accounts, "", "", "ACTIVE")
				seedInterval("foreign", otherTenant, account, historical)
				fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
				server := build(fake.Provider())
				_, err := billingRPC(readCtx(tenants), server.Get, privatev1.TenantBillingAssignmentsGetRequest_builder{Id: "foreign"}.Build())
				Expect(status.Code(err)).To(Equal(codes.NotFound))
				// An authorized tenant in the submitted spec/metadata cannot authorize the foreign stored row.
				request := updateRequest("foreign", testTenant, account)
				request.GetObject().SetMetadata(privatev1.Metadata_builder{Name: "foreign", Tenant: testTenant}.Build())
				_, err = billingRPC(scopedCtx(tenants), server.Update, request)
				Expect(status.Code(err)).To(Equal(codes.NotFound))
				_, err = billingRPC(scopedCtx(tenants), server.Signal, privatev1.TenantBillingAssignmentsSignalRequest_builder{Id: "foreign"}.Build())
				Expect(status.Code(err)).To(Equal(codes.NotFound))
				Expect(fake.Calls()).To(BeEmpty())
				var count int
				Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
				Expect(count).To(Equal(1))
				var closed bool
				Expect(testPool.QueryRow(ctx, `select effective_to is not null from tenant_billing_assignments where id = 'foreign'`).
					Scan(&closed)).To(Succeed())
				Expect(closed).To(Equal(historical))
			},
			Entry("finite/current", false, collections.NewSet(testTenant)),
			Entry("finite/history", true, collections.NewSet(testTenant)),
			Entry("empty/current", false, collections.NewSet[string]()),
			Entry("empty/history", true, collections.NewSet[string]()),
			Entry("complement/current", false, auth.AllTenants.Difference(collections.NewSet(otherTenant))),
			Entry("complement/history", true, auth.AllTenants.Difference(collections.NewSet(otherTenant))),
		)

		It("constrains complementary reads while preserving internal inventory", func() {
			createOtherTenant()
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			seedInterval("excluded-current", otherTenant, account, false)
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			tenants := auth.AllTenants.Difference(collections.NewSet(otherTenant))
			response, err := billingRPC(scopedCtx(tenants), server.List, &privatev1.TenantBillingAssignmentsListRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(response.GetTotal()).To(BeZero())
			Expect(response.GetItems()).To(BeEmpty())
			page, err := database.WithNewTx(scopedCtx(tenants), func(c context.Context) (*dao.ListResponse[*privatev1.TenantBillingAssignment], error) {
				return server.bindings.List().SetLimit(1).Do(c)
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(page.GetTotal()).To(Equal(int32(1)), "occupancy checks must still see foreign current bindings")
			Expect(fake.Calls()).To(BeEmpty())
		})

		DescribeTable("reads an authorized current target under complementary scope without changing the subject", func(method string) {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			seedInterval("own-current", testTenant, account, false)
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			tenants := auth.AllTenants.Difference(collections.NewSet(otherTenant, "absent"))
			caller := scopedCtx(tenants)
			if method == "Get" {
				got, err := billingRPC(caller, server.Get, privatev1.TenantBillingAssignmentsGetRequest_builder{Id: "own-current"}.Build())
				Expect(err).NotTo(HaveOccurred())
				Expect(got.GetObject().GetId()).To(Equal("own-current"))
			} else {
				request := updateRequest("own-current", testTenant, account)
				request.SetUpdateMask(&fieldmaskpb.FieldMask{})
				updated, err := billingRPC(caller, server.Update, request)
				Expect(err).NotTo(HaveOccurred())
				Expect(updated.GetObject().GetId()).To(Equal("own-current"))
			}
			Expect(auth.SubjectFromContext(caller).Tenants.Equal(tenants)).To(BeTrue())
			Expect(fake.Calls()).To(BeEmpty())
		}, Entry("Get", "Get"), Entry("empty-mask Update", "Update"))

		DescribeTable("rejects unauthorized Create without persisting or calling the provider",
			func(tenants collections.Set[string]) {
				createOtherTenant()
				var accounts []billingfake.AccountSpec
				account := newAccount(&accounts, "", "", "ACTIVE")
				fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
				request := createRequest(otherTenant, account, "spoofed")
				request.GetObject().GetMetadata().SetTenant(testTenant)
				server := build(fake.Provider())
				_, err := billingRPC(scopedCtx(tenants), server.Create, request)
				Expect(status.Code(err)).To(Equal(codes.PermissionDenied))
				Expect(fake.Calls()).To(BeEmpty())
				var count int
				Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
				Expect(count).To(BeZero())
				var correlation string
				Expect(testPool.QueryRow(ctx, `select coalesce(external_billing_account_id, '') from billing_accounts where id = $1`, account).
					Scan(&correlation)).To(Succeed())
				Expect(correlation).To(BeEmpty())
				Expect(server.assignSem).To(BeEmpty())
			},
			Entry("finite tenant set", collections.NewSet(testTenant)),
			Entry("empty tenant set", collections.NewSet[string]()),
		)

		DescribeTable("allows scoped assignment, reassignment, Get and Signal for an authorized tenant", func(tenants collections.Set[string]) {
			var accounts []billingfake.AccountSpec
			first := newAccount(&accounts, "", "", "ACTIVE")
			second := newAccount(&accounts, "", "", "ACTIVE")
			server := build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider())
			created, err := billingRPC(scopedCtx(tenants), server.Create, createRequest(testTenant, first, "allowed"))
			Expect(err).ToNot(HaveOccurred())
			updated, err := billingRPC(scopedCtx(tenants), server.Update, updateRequest(created.GetObject().GetId(), testTenant, second))
			Expect(err).ToNot(HaveOccurred())
			_, err = billingRPC(scopedCtx(tenants), server.Signal, privatev1.TenantBillingAssignmentsSignalRequest_builder{Id: updated.GetObject().GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			got, err := billingRPC(readCtx(tenants), server.Get, privatev1.TenantBillingAssignmentsGetRequest_builder{Id: created.GetObject().GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(got.GetObject().GetStatus().GetEffectiveToTimestamp()).ToNot(BeNil())
		}, Entry("finite", collections.NewSet(testTenant, otherTenant)),
			Entry("complement", auth.AllTenants.Difference(collections.NewSet(otherTenant))))

		DescribeTable("rejects reserved tenant targets before provider I/O or correlation generation", func(tenantID string) {
			_, err := testPool.Exec(ctx, `insert into tenants (id, name, tenant, creator, data)
				values ($1, $1, $1, 'system', '{}') on conflict do nothing`, tenantID)
			Expect(err).ToNot(HaveOccurred())
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			_, err = billingRPC(adminCtx(), server.Create, createRequest(tenantID, account, "reserved"))
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
			// Even a legacy reserved-tenant row must not be revalidated or create a correlation ID.
			seedInterval("legacy-reserved", tenantID, account, false)
			_, err = billingRPC(adminCtx(), server.Update, updateRequest("legacy-reserved", tenantID, account))
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			_, err = billingRPC(adminCtx(), server.Signal, privatev1.TenantBillingAssignmentsSignalRequest_builder{Id: "legacy-reserved"}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(fake.Calls()).To(BeEmpty())
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(Equal(1))
			var correlation string
			Expect(testPool.QueryRow(ctx, `select coalesce(external_billing_account_id, '') from billing_accounts where id = $1`, account).
				Scan(&correlation)).To(Succeed())
			Expect(correlation).To(BeEmpty())
			Expect(server.assignSem).To(BeEmpty())
		}, Entry("shared", auth.SharedTenant), Entry("system", auth.SystemTenant))
	})

	// Method authorization remains admin-only; universal administrators retain deployment-wide reads.
	Describe("List and Get", func() {
		It("uses the billing zero-limit default and maximum page size", func() {
			var accounts []billingfake.AccountSpec
			id := newAccount(&accounts, "exact", "exact", "ACTIVE")
			server := build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider())
			name, err := billingAccountName(id)
			Expect(err).NotTo(HaveOccurred())
			from := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
			_, err = database.WithNewTx(adminCtx(), func(c context.Context) (struct{}, error) {
				for i := range 1002 {
					at := timestamppb.New(from.Add(time.Duration(i) * time.Microsecond))
					_, err := server.bindings.Create().SetObject(privatev1.TenantBillingAssignment_builder{Id: fmt.Sprintf("page-%04d", i),
						Metadata: privatev1.Metadata_builder{Name: "page-history", Tenant: testTenant, Creator: "admin",
							Annotations: map[string]string{"osac.openshift.io/tenant": testTenant}}.Build(),
						Spec: privatev1.TenantBillingAssignmentSpec_builder{
							Tenant:         privatev1.TenantReference_builder{Id: testTenant, Name: testTenant}.Build(),
							BillingAccount: privatev1.BillingAccountReference_builder{Id: id, Name: name}.Build(),
						}.Build(), Status: privatev1.TenantBillingAssignmentStatus_builder{
							EffectiveFromTimestamp: at, EffectiveToTimestamp: at,
							ConnectionState: privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED,
						}.Build(),
					}.Build()).Do(c)
					if err != nil {
						return struct{}{}, err
					}
				}
				return struct{}{}, nil
			})
			Expect(err).NotTo(HaveOccurred())
			read := auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: auth.AllTenants})
			page, err := billingRPC(read, server.List, privatev1.TenantBillingAssignmentsListRequest_builder{Limit: new(int32(0))}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(page.GetTotal()).To(Equal(int32(1002)))
			Expect(page.GetSize()).To(Equal(int32(100)))
			page, err = billingRPC(read, server.List, privatev1.TenantBillingAssignmentsListRequest_builder{Limit: new(int32(1500))}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(page.GetTotal()).To(Equal(int32(1002)))
			Expect(page.GetSize()).To(Equal(int32(1000)))
			page, err = billingRPC(read, server.List, privatev1.TenantBillingAssignmentsListRequest_builder{
				Filter: new(`this.status.connection_state == 2`),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(page.GetTotal()).To(Equal(int32(1002)))
			Expect(page.GetSize()).To(Equal(int32(100)))
		})

		It("reads stored canonical history without a live tenant row", func() {
			var accounts []billingfake.AccountSpec
			id := newAccount(&accounts, "exact", "exact", "ACTIVE")
			at := time.Now().UTC().Truncate(time.Microsecond)
			seedHistoryInterval("retained-history", testTenant, id, at, &at)
			_, err := testPool.Exec(ctx, `delete from projects where tenant = $1`, testTenant)
			Expect(err).NotTo(HaveOccurred())
			_, err = testPool.Exec(ctx, `delete from tenants where name = $1`, testTenant)
			Expect(err).NotTo(HaveOccurred())
			var live int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenants where name = $1`, testTenant).Scan(&live)).To(Succeed())
			Expect(live).To(BeZero())
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			read := auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: collections.NewSet(testTenant)})
			got, err := billingRPC(read, server.Get, privatev1.TenantBillingAssignmentsGetRequest_builder{Id: "retained-history"}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.GetObject().GetSpec().GetTenant().GetName()).To(Equal(testTenant))
			listed, err := billingRPC(read, server.List, &privatev1.TenantBillingAssignmentsListRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetTotal()).To(Equal(int32(1)))
			Expect(fake.Calls()).To(BeEmpty())
		})
		It("lists every assignment", func() {
			createOtherTenant()
			var accounts []billingfake.AccountSpec
			account1 := newAccount(&accounts, "", "", "ACTIVE")
			account2 := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account1, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			_, err = billingRPC(adminCtx(), server.Create, createRequest(otherTenant, account2, "asg-2"))
			Expect(err).ToNot(HaveOccurred())

			resp, err := billingRPC(adminCtx(), server.List, &privatev1.TenantBillingAssignmentsListRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetTotal()).To(Equal(int32(2)))
		})

		It("gets one assignment by id", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())

			resp, err := billingRPC(adminCtx(), server.Get,
				privatev1.TenantBillingAssignmentsGetRequest_builder{Id: created.GetObject().GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetObject().GetId()).To(Equal(created.GetObject().GetId()))
		})
	})

	// Final partial unique indexes remain authoritative across instances. These cases test sanitized error mapping;
	// real SQL contention and material snapshot revalidation are covered above.
	Describe("mapTenantBillingAssignmentError", func() {
		var server *PrivateTenantBillingAssignmentsServer
		BeforeEach(func() {
			server = build(billingfake.New(billingfake.Scenario{}).Provider())
		})

		It("maps a raced tenant_billing_assignments_unique_active_tenant violation to TENANT_ALREADY_BOUND, not Internal", func() {
			err := server.mapTenantBillingAssignmentError(ctx, &pgconn.PgError{ConstraintName: "tenant_billing_assignments_unique_active_tenant"})
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("TENANT_ALREADY_BOUND"))
		})

		It("maps a raced tenant_billing_assignments_unique_active_account violation to ACCOUNT_ALREADY_BOUND, not Internal", func() {
			err := server.mapTenantBillingAssignmentError(ctx, &pgconn.PgError{ConstraintName: "tenant_billing_assignments_unique_active_account"})
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("ACCOUNT_ALREADY_BOUND"))
		})

		It("maps a tenant_billing_assignments_tenant_id_fkey violation to INVALID_ARGUMENT", func() {
			err := server.mapTenantBillingAssignmentError(ctx, &pgconn.PgError{ConstraintName: "tenant_billing_assignments_tenant_id_fkey"})
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec.tenant"))
		})

		It("returns a fixed Internal message for an unmapped error, without echoing its detail", func() {
			err := server.mapTenantBillingAssignmentError(ctx, errors.New("pq: constraint xyz on table t: secret detail"))
			Expect(status.Code(err)).To(Equal(codes.Internal))
			Expect(err.Error()).ToNot(ContainSubstring("secret detail"))
			Expect(err.Error()).ToNot(ContainSubstring("pq:"))
		})
	})
})
