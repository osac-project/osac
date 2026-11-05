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
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// hangingCorrelationAdapter blocks every CorrelationValue call until its context is done - it reproduces a
// provider that accepts requests but never answers, so the assign deadline is the only way out.
type hangingCorrelationAdapter struct {
	*billingfake.Fake
}

func (a *hangingCorrelationAdapter) CorrelationValue(ctx context.Context, _ billing.AccountRef) (string, bool, error) {
	<-ctx.Done()
	return "", false, billing.NewError(billing.ReasonTimeout, "CorrelationValue", true, 0)
}

// gatedCorrelationAdapter holds every CorrelationValue call until its release channel closes (or its context
// is done), then answers normally - it reproduces an in-flight assign whose provider call is slow but
// eventually succeeds, so a concurrent assign has to fail fast instead of queueing behind it.
type gatedCorrelationAdapter struct {
	*billingfake.Fake
	entered    chan struct{}
	release    chan struct{}
	once       sync.Once
	blockedRef string
}

func (a *gatedCorrelationAdapter) CorrelationValue(ctx context.Context, ref billing.AccountRef) (string, bool, error) {
	if a.blockedRef != "" && ref != billing.NewAccountRef(a.blockedRef) {
		return a.Fake.CorrelationValue(ctx, ref)
	}
	a.once.Do(func() { close(a.entered) })
	select {
	case <-a.release:
		return a.Fake.CorrelationValue(ctx, ref)
	case <-ctx.Done():
		return "", false, billing.NewError(billing.ReasonTimeout, "CorrelationValue", true, 0)
	}
}

// afterWriteAdapter pauses after the provider has accepted the write, exposing
// the unavoidable non-atomic boundary between the remote write and local commit.
type afterWriteAdapter struct {
	*billingfake.Fake
	entered chan struct{}
	release chan struct{}
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

func (a *afterWriteAdapter) SetCorrelationValue(ctx context.Context, ref billing.AccountRef, value string) error {
	if err := a.Fake.SetCorrelationValue(ctx, ref, value); err != nil {
		return err
	}
	close(a.entered)
	select {
	case <-a.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ = Describe("Private tenant billing assignments server", func() {
	const otherTenant = "other-tenant"

	BeforeEach(func() {
		// Commit the shared fixture's tenant. Provider-backed RPCs below use
		// independent real transactions, not the suite's assertion transaction.
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
			_, err := database.UpsertBillingAccount(ctx, tx, database.BillingAccountUpsert{
				ID: id, ProviderKey: billingfake.Key, ProviderAccountReference: id,
				CorrelationID: correlationID, State: state, StatusObservedAt: time.Now().UTC(),
			})
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
					TenantId: tenantID, BillingAccountId: accountID,
				}.Build(),
			}.Build(),
		}.Build()
	}
	updateRequest := func(id, tenantID, accountID string) *privatev1.TenantBillingAssignmentsUpdateRequest {
		return privatev1.TenantBillingAssignmentsUpdateRequest_builder{
			Object: privatev1.TenantBillingAssignment_builder{
				Id: id,
				Spec: privatev1.TenantBillingAssignmentSpec_builder{
					TenantId: tenantID, BillingAccountId: accountID,
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

			_, err := server.List(adminCtx(), &privatev1.TenantBillingAssignmentsListRequest{})
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("BILLING_NOT_CONFIGURED"))

			_, err = server.Create(adminCtx(), createRequest(testTenant, "any", "asg"))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("BILLING_NOT_CONFIGURED"))

			Expect(fake.Calls()).To(BeEmpty())
		})
	})

	Describe("Create", func() {
		It("generates a correlation id and connects when the provider supports writing it", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts})
			server := build(fake.Provider())

			resp, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
			Expect(resp.GetObject().GetStatus().GetMessage()).To(BeEmpty())
			Expect(resp.GetObject().GetSpec().GetTenantId()).To(Equal(testTenant))
			Expect(resp.GetObject().GetSpec().GetBillingAccountId()).To(Equal(account))
			Expect(resp.GetObject().GetId()).ToNot(BeEmpty())
		})

		It("rejects Create and Update for a tenant the platform has deleted", func() {
			var accounts []billingfake.AccountSpec
			account1 := newAccount(&accounts, "", "", "ACTIVE")
			account2 := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts})
			server := build(fake.Provider())

			// Create while the tenant is live, so there is a current interval to Update afterwards.
			created, err := server.Create(adminCtx(), createRequest(testTenant, account1, "asg-1"))
			Expect(err).ToNot(HaveOccurred())

			// A second tenant with no assignment, so the dead-tenant rejection is what Create hits rather
			// than TENANT_ALREADY_BOUND. Mark it deleted without removing its row: that is the OSAC-6886
			// tombstone state, and it keeps the tenant FK satisfied — only the explicit liveness check in
			// the shared assign tail can reject it.
			_, err = testPool.Exec(ctx, `insert into tenants (id, name, tenant, creator, data)
				values ('dead-tenant', 'dead-tenant', 'dead-tenant', 'system', '{}') on conflict do nothing`)
			Expect(err).ToNot(HaveOccurred())
			_, err = testPool.Exec(ctx, `update tenants set deletion_timestamp = now() where name = 'dead-tenant'`)
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(adminCtx(), createRequest("dead-tenant", account2, "asg-2"))
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring("spec.tenant_id does not reference an existing tenant"))

			// Update runs through the same shared tail, so reassigning a tenant that was deleted after its
			// assignment exists is rejected the same way:
			_, err = testPool.Exec(ctx, `update tenants set deletion_timestamp = now() where name = $1`, testTenant)
			Expect(err).ToNot(HaveOccurred())
			_, err = server.Update(adminCtx(), updateRequest(created.GetObject().GetId(), testTenant, account2))
			st, ok = status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring("spec.tenant_id does not reference an existing tenant"))
		})

		It("generates a correlation id and stays PENDING with the stable reason when the provider cannot write it", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: false, Accounts: accounts})
			server := build(fake.Provider())

			resp, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING))
			Expect(resp.GetObject().GetStatus().GetMessage()).To(ContainSubstring("BILLING_PROVIDER_CORRELATION_WRITE_UNSUPPORTED"))
		})

		It("adopts a provider-only value and connects when exactly one account carries it", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "ext-preset", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())

			resp, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
		})

		It("adopts an OSAC-only value, writes it to the provider when supported, and connects", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "osac-corr", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts})
			server := build(fake.Provider())

			resp, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
			Expect(fake.Count("SetCorrelationValue")).To(Equal(1))
		})

		It("proceeds when both sides already agree", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "corr-equal", "corr-equal", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())

			resp, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
		})

		It("rejects differing values with BILLING_CORRELATION_ID_CONFLICT and creates no row", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "provider-side", "osac-side", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())

			_, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("BILLING_CORRELATION_ID_CONFLICT"))
			Expect(err.Error()).ToNot(ContainSubstring("osac-side"))
			Expect(err.Error()).ToNot(ContainSubstring("provider-side"))

			tx, txErr := database.TxFromContext(ctx)
			Expect(txErr).ToNot(HaveOccurred())
			_, total, listErr := database.ListTenantBillingAssignments(ctx, tx, 0, 10,
				`this.spec.billing_account_id == "`+account+`"`, "")
			Expect(listErr).ToNot(HaveOccurred())
			Expect(total).To(BeZero())
		})

		It("rejects two provider accounts carrying the same value with ACCOUNT_AMBIGUOUS and creates no row", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "corr-dup", "corr-dup", "ACTIVE")
			// A second, unrelated provider account also carries the same value. Every resolution branch,
			// including the already-consistent one, runs the resolver's bounded uniqueness scan, so this
			// exercises that scan's ACCOUNT_AMBIGUOUS verdict.
			accounts = append(accounts, billingfake.AccountSpec{ID: "decoy", ExternalID: "corr-dup", State: billing.AccountActive})
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())

			_, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("ACCOUNT_AMBIGUOUS"))

			tx, txErr := database.TxFromContext(ctx)
			Expect(txErr).ToNot(HaveOccurred())
			_, total, listErr := database.ListTenantBillingAssignments(ctx, tx, 0, 10,
				`this.spec.billing_account_id == "`+account+`"`, "")
			Expect(listErr).ToNot(HaveOccurred())
			Expect(total).To(BeZero())
		})

		It("rejects a foreign provider carrier before writing or creating an assignment", func() {
			var accounts []billingfake.AccountSpec
			// The target has no provider value, so even one pre-existing carrier
			// is foreign. Reject before writing, not after creating a duplicate.
			account := newAccount(&accounts, "", "osac-dup", "ACTIVE")
			accounts = append(accounts, billingfake.AccountSpec{ID: "decoy", ExternalID: "osac-dup", State: billing.AccountActive})
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts})
			server := build(fake.Provider())

			_, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
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
				Failures: map[string]billingfake.Failure{"CorrelationValue": {Reason: billing.ReasonUnavailable}},
			})
			server := build(fake.Provider())

			_, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
			Expect(status.Code(err)).To(Equal(codes.Unavailable))

			tx, txErr := database.TxFromContext(ctx)
			Expect(txErr).ToNot(HaveOccurred())
			_, total, listErr := database.ListTenantBillingAssignments(ctx, tx, 0, 10,
				`this.spec.billing_account_id == "`+account+`"`, "")
			Expect(listErr).ToNot(HaveOccurred())
			Expect(total).To(BeZero())
		})

		It("fails as UNAVAILABLE/TIMEOUT and releases its durable guard when the provider hangs", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts})
			server := build(billing.NewProvider(billingfake.Key, &hangingCorrelationAdapter{Fake: fake}, nil))
			server.assignProviderDeadline = 50 * time.Millisecond

			result := make(chan error, 1)
			go func() {
				_, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
				result <- err
			}()
			var err error
			Eventually(result, 5*time.Second).Should(Receive(&err))
			Expect(status.Code(err)).To(Equal(codes.Unavailable))
			Expect(reason(err)).To(Equal("TIMEOUT"))
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from billing_operation_leases`).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
		})

		It("fails with RESOURCE_EXHAUSTED when the concurrent-assign window is full", func() {
			createOtherTenant()
			var accounts []billingfake.AccountSpec
			blocked := newAccount(&accounts, "", "", "ACTIVE")
			turnedAway := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts})
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
				_, err := server.Create(adminCtx(), createRequest(testTenant, blocked, "asg-1"))
				inFlight <- err
			}()
			Eventually(adapter.entered, 5*time.Second).Should(BeClosed(),
				"the in-flight assign must reach the provider window")

			_, err := server.Create(adminCtx(), createRequest(otherTenant, turnedAway, "asg-2"))
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
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts})
			server := build(fake.Provider())

			_, err := server.Create(adminCtx(), createRequest(testTenant, account1, "asg-1"))
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(adminCtx(), createRequest(testTenant, account2, "asg-2"))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("TENANT_ALREADY_BOUND"))

			tx, txErr := database.TxFromContext(ctx)
			Expect(txErr).ToNot(HaveOccurred())
			active, getErr := database.GetActiveTenantBillingAssignmentByTenant(ctx, tx, testTenant)
			Expect(getErr).ToNot(HaveOccurred())
			Expect(active.BillingAccountID).To(Equal(account1), "the original binding must be unchanged")
		})

		It("rejects an account already linked to another tenant", func() {
			createOtherTenant()
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts})
			server := build(fake.Provider())

			_, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(adminCtx(), createRequest(otherTenant, account, "asg-2"))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("ACCOUNT_ALREADY_BOUND"))
		})

		It("rejects an INACTIVE account", func() {
			id := uuid.New()
			seedAccountRow(id, "", "INACTIVE")
			fake := billingfake.New(billingfake.Scenario{})
			server := build(fake.Provider())
			_, err := server.Create(adminCtx(), createRequest(testTenant, id, "asg-1"))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("ACCOUNT_INACTIVE"))
		})

		It("rejects an UNKNOWN account", func() {
			id := uuid.New()
			seedAccountRow(id, "", "UNKNOWN")
			fake := billingfake.New(billingfake.Scenario{})
			server := build(fake.Provider())
			_, err := server.Create(adminCtx(), createRequest(testTenant, id, "asg-1"))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("ACCOUNT_STATE_UNKNOWN"))
		})

		It("rejects a nonexistent billing account", func() {
			fake := billingfake.New(billingfake.Scenario{})
			server := build(fake.Provider())
			_, err := server.Create(adminCtx(), createRequest(testTenant, "no-such-account", "asg-1"))
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})
	})

	Describe("Update", func() {
		It("rejects a deleted tenant even when revalidating a CONNECTED same-account interval", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "connected", "connected", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
			server := build(fake.Provider())
			created, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			_, err = testPool.Exec(ctx, `update tenants set deletion_timestamp = now() where name = $1`, testTenant)
			Expect(err).ToNot(HaveOccurred())
			reads := fake.Count("CorrelationValue")
			_, err = server.Update(adminCtx(), updateRequest(created.GetObject().GetId(), testTenant, account))
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(fake.Count("CorrelationValue")).To(Equal(reads))
		})

		DescribeTable("fully revalidates a CONNECTED same-account interval",
			func(externalID string, duplicate bool, expectedReason string) {
				var accounts []billingfake.AccountSpec
				account := newAccount(&accounts, "connected", "connected", "ACTIVE")
				server := build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider())
				created, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
				Expect(err).ToNot(HaveOccurred())
				accounts[0].ExternalID = externalID
				if duplicate {
					accounts = append(accounts, billingfake.AccountSpec{ID: "foreign", ExternalID: "connected", State: billing.AccountActive})
				}
				fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
				server = build(fake.Provider())
				_, err = server.Update(adminCtx(), updateRequest(created.GetObject().GetId(), testTenant, account))
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
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts})
			server := build(fake.Provider())
			created, err := server.Create(adminCtx(), createRequest(testTenant, account1, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			firstID := created.GetObject().GetId()

			updated, err := server.Update(adminCtx(), updateRequest(firstID, testTenant, account2))
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetId()).ToNot(Equal(firstID))
			Expect(updated.GetObject().GetSpec().GetBillingAccountId()).To(Equal(account2))

			tx, txErr := database.TxFromContext(ctx)
			Expect(txErr).ToNot(HaveOccurred())
			closedRow, getErr := database.GetTenantBillingAssignment(ctx, tx, firstID)
			Expect(getErr).ToNot(HaveOccurred())
			Expect(closedRow.EffectiveTo).ToNot(BeNil())

			active, getErr := database.GetActiveTenantBillingAssignmentByTenant(ctx, tx, testTenant)
			Expect(getErr).ToNot(HaveOccurred())
			Expect(active.ID).To(Equal(updated.GetObject().GetId()))
			Expect(active.EffectiveFrom.Equal(*closedRow.EffectiveTo)).To(BeTrue())
		})

		It("re-runs resolution for a PENDING assignment resubmitted for the same tenant and account", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: false, Accounts: accounts})
			server := build(fake.Provider())
			created, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(created.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING))
			pendingID := created.GetObject().GetId()

			// The admin sets the provider's externalId to the OSAC-stored value and resubmits Update. The fake
			// is rebuilt with that value preset, standing in for the out-of-band provider change.
			tx, txErr := database.TxFromContext(ctx)
			Expect(txErr).ToNot(HaveOccurred())
			accountRow, getErr := database.GetBillingAccountForAssignment(ctx, tx, account)
			Expect(getErr).ToNot(HaveOccurred())
			fake2 := billingfake.New(billingfake.Scenario{CorrelationWrite: false, Accounts: []billingfake.AccountSpec{
				{ID: account, ExternalID: accountRow.CorrelationID, State: billing.AccountActive},
			}})
			server2 := build(fake2.Provider())

			updated, err := server2.Update(adminCtx(), updateRequest(pendingID, testTenant, account))
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetId()).To(Equal(pendingID))
			Expect(updated.GetObject().GetStatus().GetEffectiveFrom()).To(Equal(created.GetObject().GetStatus().GetEffectiveFrom()))
			Expect(updated.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))

			closedRow, getErr := database.GetTenantBillingAssignment(ctx, tx, pendingID)
			Expect(getErr).ToNot(HaveOccurred())
			Expect(closedRow.EffectiveTo).To(BeNil())
		})

		It("revalidates the same account on a CONNECTED assignment without changing history", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts})
			server := build(fake.Provider())
			created, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			connectedID := created.GetObject().GetId()
			reads := fake.Count("CorrelationValue")

			updated, err := server.Update(adminCtx(), updateRequest(connectedID, testTenant, account))
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetId()).To(Equal(connectedID), "no new interval must be created")
			Expect(fake.Count("CorrelationValue")).To(BeNumerically(">", reads))
			Expect(updated.GetObject().GetStatus().GetEffectiveFrom()).To(Equal(created.GetObject().GetStatus().GetEffectiveFrom()))

			tx, txErr := database.TxFromContext(ctx)
			Expect(txErr).ToNot(HaveOccurred())
			_, total, listErr := database.ListTenantBillingAssignments(ctx, tx, 0, 10,
				`this.spec.tenant_id == "`+testTenant+`"`, "")
			Expect(listErr).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(1)))
		})

		It("hazard: fails with BILLING_CORRELATION_ID_CONFLICT when the provider value was set to something else, "+
			"leaving both sides unchanged", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: false, Accounts: accounts})
			server := build(fake.Provider())
			created, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			pendingID := created.GetObject().GetId()

			fake2 := billingfake.New(billingfake.Scenario{CorrelationWrite: false, Accounts: []billingfake.AccountSpec{
				{ID: account, ExternalID: "a-different-value", State: billing.AccountActive},
			}})
			server2 := build(fake2.Provider())

			_, err = server2.Update(adminCtx(), updateRequest(pendingID, testTenant, account))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("BILLING_CORRELATION_ID_CONFLICT"))

			tx, txErr := database.TxFromContext(ctx)
			Expect(txErr).ToNot(HaveOccurred())
			unchanged, getErr := database.GetTenantBillingAssignment(ctx, tx, pendingID)
			Expect(getErr).ToNot(HaveOccurred())
			Expect(unchanged.EffectiveTo).To(BeNil(), "the PENDING interval must remain the current one")
		})

		It("rejects changing spec.tenant_id", func() {
			createOtherTenant()
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts})
			server := build(fake.Provider())
			created, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())

			request := privatev1.TenantBillingAssignmentsUpdateRequest_builder{
				Object: privatev1.TenantBillingAssignment_builder{
					Id: created.GetObject().GetId(),
					Spec: privatev1.TenantBillingAssignmentSpec_builder{
						TenantId: otherTenant, BillingAccountId: account,
					}.Build(),
				}.Build(),
			}.Build()
			_, err = server.Update(adminCtx(), request)
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("returns NotFound for an unknown id", func() {
			fake := billingfake.New(billingfake.Scenario{})
			server := build(fake.Provider())
			_, err := server.Update(adminCtx(), updateRequest("no-such-id", testTenant, "any"))
			Expect(status.Code(err)).To(Equal(codes.NotFound))
		})
	})

	Describe("short transactions and durable guards", func() {
		It("rolls back correlation and interval changes together if the final commit fails, not the provider write", func() {
			var accounts []billingfake.AccountSpec
			first := newAccount(&accounts, "", "", "ACTIVE")
			second := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts})
			server := build(fake.Provider())
			created, err := server.Create(adminCtx(), createRequest(testTenant, first, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			_, err = testPool.Exec(ctx, `
				create function reject_billing_assignment_commit() returns trigger language plpgsql as $$
				begin raise exception 'injected billing commit failure'; end;
				$$;
				create constraint trigger reject_billing_assignment_commit
				after insert on tenant_billing_assignments deferrable initially deferred
				for each row execute function reject_billing_assignment_commit();`)
			Expect(err).ToNot(HaveOccurred())
			_, err = server.Update(adminCtx(), updateRequest(created.GetObject().GetId(), testTenant, second))
			Expect(status.Code(err)).To(Equal(codes.Internal))
			Expect(err.Error()).ToNot(ContainSubstring("injected"))
			var activeID string
			Expect(testPool.QueryRow(ctx, `select id from tenant_billing_assignments where effective_to is null`).Scan(&activeID)).To(Succeed())
			Expect(activeID).To(Equal(created.GetObject().GetId()))
			var correlation string
			Expect(testPool.QueryRow(ctx, `select coalesce(correlation_id, '') from billing_accounts where id = $1`, second).Scan(&correlation)).To(Succeed())
			Expect(correlation).To(BeEmpty())
			providerValue, present, err := fake.CorrelationValue(ctx, billing.NewAccountRef(second))
			Expect(err).ToNot(HaveOccurred())
			Expect(present).To(BeTrue())
			Expect(providerValue).ToNot(BeEmpty(), "remote writes are not rolled back by the database")
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(Equal(1))
			Expect(testPool.QueryRow(ctx, `select count(*) from billing_operation_leases`).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
		})

		It("reserves a correlation candidate before external work across different accounts and server instances", func() {
			createOtherTenant()
			var accounts []billingfake.AccountSpec
			first := newAccount(&accounts, "shared-candidate", "", "ACTIVE")
			second := newAccount(&accounts, "shared-candidate", "", "ACTIVE")
			// Each provider view is individually consistent but stale relative to
			// the other. The durable local reservation must still prevent double
			// adoption while neither billing_accounts row has committed a value.
			firstFake := billingfake.New(billingfake.Scenario{Accounts: accounts[:1]})
			secondFake := billingfake.New(billingfake.Scenario{Accounts: accounts[1:]})
			adapter := &gatedAccountScanAdapter{Fake: firstFake, entered: make(chan struct{}), release: make(chan struct{})}
			server1 := build(billing.NewProvider(billingfake.Key, adapter, nil))
			server2 := build(secondFake.Provider())
			result := make(chan error, 1)
			go func() { _, err := server1.Create(adminCtx(), createRequest(testTenant, first, "asg-1")); result <- err }()
			DeferCleanup(func() {
				select {
				case <-adapter.release:
				default:
					close(adapter.release)
				}
			})
			Eventually(adapter.entered, 5*time.Second).Should(BeClosed())
			var reserved bool
			Expect(testPool.QueryRow(ctx, `select exists(select 1 from billing_operation_leases where operation_key = $1)`,
				database.BillingOperationKey("correlation", "shared-candidate")).Scan(&reserved)).To(Succeed())
			Expect(reserved).To(BeTrue())
			_, err := server2.Create(adminCtx(), createRequest(otherTenant, second, "asg-2"))
			Expect(status.Code(err)).To(Equal(codes.Aborted))
			Expect(reason(err)).To(Equal("BILLING_OPERATION_RETRY"))
			Expect(secondFake.Count("ListAccounts")).To(BeZero())
			Expect(secondFake.Count("SetCorrelationValue")).To(BeZero())
			close(adapter.release)
			var firstErr error
			Eventually(result, 5*time.Second).Should(Receive(&firstErr))
			Expect(firstErr).ToNot(HaveOccurred())
			_, err = server2.Create(adminCtx(), createRequest(otherTenant, second, "asg-2"))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("ACCOUNT_CORRELATION_CONFLICT"))
		})

		DescribeTable("leaves a one-connection pool available throughout gated provider I/O", func(afterWrite bool) {
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
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts})
			entered, release := make(chan struct{}), make(chan struct{})
			var adapter billing.Adapter = &gatedCorrelationAdapter{Fake: fake, entered: entered, release: release}
			if afterWrite {
				adapter = &afterWriteAdapter{Fake: fake, entered: entered, release: release}
			}
			server := build(billing.NewProvider(billingfake.Key, adapter, nil))
			result := make(chan error, 1)
			go func() {
				_, err := server.Create(rpcCtx, createRequest(testTenant, account, "asg-1"))
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
			readCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var one int
			Expect(pool.QueryRow(readCtx, `select 1`).Scan(&one)).To(Succeed())
			Expect(one).To(Equal(1))
			close(release)
			var createErr error
			Eventually(result, 5*time.Second).Should(Receive(&createErr))
			Expect(createErr).ToNot(HaveOccurred())
		},
			Entry("during the first provider read", false),
			Entry("after the correlation reservation and external write", true),
		)

		It("fails same-account and tenant contenders across server instances before provider capacity, allowing unrelated work", func() {
			createOtherTenant()
			var accounts []billingfake.AccountSpec
			first := newAccount(&accounts, "", "", "ACTIVE")
			second := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts})
			adapter := &gatedCorrelationAdapter{Fake: fake, entered: make(chan struct{}), release: make(chan struct{}), blockedRef: first}
			provider := billing.NewProvider(billingfake.Key, adapter, nil)
			server1, server2 := build(provider), build(provider)
			result := make(chan error, 1)
			go func() {
				_, err := server1.Create(adminCtx(), createRequest(testTenant, first, "asg-1"))
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
			// Even a full provider window must not hide resource contention:
			// account/tenant guards are tested before semaphore admission.
			for range maxConcurrentAssigns {
				server2.assignSem <- struct{}{}
			}
			for _, request := range []*privatev1.TenantBillingAssignmentsCreateRequest{
				createRequest(otherTenant, first, "account-contender"),
				createRequest(testTenant, second, "tenant-contender"),
			} {
				contender := make(chan error, 1)
				go func() { _, err := server2.Create(adminCtx(), request); contender <- err }()
				var err error
				Eventually(contender, time.Second).Should(Receive(&err))
				Expect(status.Code(err)).To(Equal(codes.Aborted))
				Expect(reason(err)).To(Equal("BILLING_OPERATION_RETRY"))
				Expect(server2.assignSem).To(HaveLen(maxConcurrentAssigns))
			}
			for range maxConcurrentAssigns {
				<-server2.assignSem
			}
			_, err := server2.Create(adminCtx(), createRequest(otherTenant, second, "unrelated"))
			Expect(err).ToNot(HaveOccurred())
			close(adapter.release)
			var firstErr error
			Eventually(result, 5*time.Second).Should(Receive(&firstErr))
			Expect(firstErr).ToNot(HaveOccurred())
		})

		It("rejects stale local finalization after provider acceptance and expired-guard takeover", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts})
			adapter := &afterWriteAdapter{Fake: fake, entered: make(chan struct{}), release: make(chan struct{})}
			server := build(billing.NewProvider(billingfake.Key, adapter, nil))
			result := make(chan error, 1)
			go func() {
				_, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
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
			_, err := testPool.Exec(ctx, `update billing_operation_leases set expires_at = clock_timestamp() - interval '1 second'`)
			Expect(err).ToNot(HaveOccurred())
			successor, err := database.AcquireBillingOperation(adminCtx(), time.Minute,
				database.BillingOperationKey("account", account), database.BillingOperationKey("tenant", testTenant))
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { Expect(successor.Release(adminCtx())).To(Succeed()) })
			close(adapter.release)
			var staleErr error
			Eventually(result, 5*time.Second).Should(Receive(&staleErr))
			Expect(status.Code(staleErr)).To(Equal(codes.Aborted))
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
			Expect(fake.Count("SetCorrelationValue")).To(Equal(1), "the provider write cannot be rolled back by Postgres")
		})

		DescribeTable("revalidates authoritative local state after provider I/O", func(changeSQL string, expected codes.Code) {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts})
			adapter := &gatedCorrelationAdapter{Fake: fake, entered: make(chan struct{}), release: make(chan struct{})}
			server := build(billing.NewProvider(billingfake.Key, adapter, nil))
			result := make(chan error, 1)
			go func() {
				_, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
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
			_, err := testPool.Exec(ctx, changeSQL)
			Expect(err).ToNot(HaveOccurred())
			close(adapter.release)
			var finalErr error
			Eventually(result, 5*time.Second).Should(Receive(&finalErr))
			Expect(status.Code(finalErr)).To(Equal(expected))
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
		},
			Entry("tenant deletion", `update tenants set deletion_timestamp = now() where name = 'test-tenant'`, codes.InvalidArgument),
			Entry("account inactive", `update billing_accounts set state = 'INACTIVE'`, codes.FailedPrecondition),
			Entry("provider reference changed", `update billing_accounts set provider_account_reference = 'changed'`, codes.Aborted),
			Entry("correlation changed", `update billing_accounts set correlation_id = 'changed'`, codes.Aborted),
		)
	})

	Describe("Signal", func() {
		It("connects a PENDING current interval in place", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			server := build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider())
			created, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			var correlation string
			Expect(testPool.QueryRow(ctx, `select correlation_id from billing_accounts where id = $1`, account).Scan(&correlation)).To(Succeed())
			accounts[0].ExternalID = correlation
			server = build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider())
			_, err = server.Signal(adminCtx(), privatev1.TenantBillingAssignmentsSignalRequest_builder{Id: created.GetObject().GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			var state, id string
			var from time.Time
			Expect(testPool.QueryRow(ctx, `select id, effective_from, connection_state from tenant_billing_assignments
				where effective_to is null`).Scan(&id, &from, &state)).To(Succeed())
			Expect(id).To(Equal(created.GetObject().GetId()))
			Expect(from.Equal(created.GetObject().GetStatus().GetEffectiveFrom().AsTime())).To(BeTrue())
			Expect(state).To(Equal(tbaStateConnected))
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(Equal(1))
		})

		It("rejects empty, unknown, historical and disabled requests", func() {
			var accounts []billingfake.AccountSpec
			first := newAccount(&accounts, "", "", "ACTIVE")
			second := newAccount(&accounts, "", "", "ACTIVE")
			server := build(billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts}).Provider())
			_, err := server.Signal(adminCtx(), &privatev1.TenantBillingAssignmentsSignalRequest{})
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			_, err = server.Signal(adminCtx(), privatev1.TenantBillingAssignmentsSignalRequest_builder{Id: "unknown"}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
			created, err := server.Create(adminCtx(), createRequest(testTenant, first, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			_, err = server.Update(adminCtx(), updateRequest(created.GetObject().GetId(), testTenant, second))
			Expect(err).ToNot(HaveOccurred())
			_, err = server.Signal(adminCtx(), privatev1.TenantBillingAssignmentsSignalRequest_builder{Id: created.GetObject().GetId()}.Build())
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			disabled := build(billing.NewProvider("", nil, nil))
			_, err = disabled.Signal(adminCtx(), privatev1.TenantBillingAssignmentsSignalRequest_builder{Id: "any"}.Build())
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal("BILLING_NOT_CONFIGURED"))
		})
	})

	// Only Cloud Provider Admins ever reach List/Get in production (design.md §4.3/§4.7; enforced by authz.rego's
	// is_admin catch-all, see authorization_evaluator_test.go's "tenant billing assignments authorization" table).
	// There is no tenant self-service read path, so this package's tests - like
	// private_billing_accounts_server_test.go's - only exercise the admin behavior.
	Describe("List and Get", func() {
		It("lists every assignment", func() {
			createOtherTenant()
			var accounts []billingfake.AccountSpec
			account1 := newAccount(&accounts, "", "", "ACTIVE")
			account2 := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts})
			server := build(fake.Provider())
			_, err := server.Create(adminCtx(), createRequest(testTenant, account1, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			_, err = server.Create(adminCtx(), createRequest(otherTenant, account2, "asg-2"))
			Expect(err).ToNot(HaveOccurred())

			resp, err := server.List(auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: auth.AllTenants}), &privatev1.TenantBillingAssignmentsListRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetTotal()).To(Equal(int32(2)))
		})

		It("gets one assignment by id", func() {
			var accounts []billingfake.AccountSpec
			account := newAccount(&accounts, "", "", "ACTIVE")
			fake := billingfake.New(billingfake.Scenario{CorrelationWrite: true, Accounts: accounts})
			server := build(fake.Provider())
			created, err := server.Create(adminCtx(), createRequest(testTenant, account, "asg-1"))
			Expect(err).ToNot(HaveOccurred())

			resp, err := server.Get(auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: auth.AllTenants}),
				privatev1.TenantBillingAssignmentsGetRequest_builder{Id: created.GetObject().GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetObject().GetId()).To(Equal(created.GetObject().GetId()))
		})
	})

	// The durable guard coordinates participating RPCs, while the final partial
	// unique indexes remain the authoritative defense against any other writer.
	// These cases test their error mapping; the real guard contention is above.
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
			Expect(err.Error()).To(ContainSubstring("spec.tenant_id"))
		})

		It("returns a fixed Internal message for an unmapped error, without echoing its detail", func() {
			err := server.mapTenantBillingAssignmentError(ctx, errors.New("pq: constraint xyz on table t: secret detail"))
			Expect(status.Code(err)).To(Equal(codes.Internal))
			Expect(err.Error()).ToNot(ContainSubstring("secret detail"))
			Expect(err.Error()).ToNot(ContainSubstring("pq:"))
		})
	})
})
