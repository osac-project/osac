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
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/billing/m360"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingstub"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// These specs point the real M360 adapter at the contract-shaped HTTP stub and run assignment against real
// PostgreSQL. The fake-based specs in the sibling file own the assignment logic; these prove that a provider-only
// state change is normalized and enforced through the actual M360 wire contract, without a live bearer token or
// tenant, and that the adapter still performs no M360 account write.
var _ = Describe("Private tenant billing assignments with the real M360 adapter", Label("billing"), func() {
	const (
		credential = "contract-token"
		nativeOne  = "m360-native-1"
		nativeTwo  = "m360-native-2"
	)

	var (
		stub      *billingstub.Stub
		server    *PrivateTenantBillingAssignmentsServer
		accountID string
		seededAt  time.Time
	)

	// activeAccount is the contract-shaped M360 row: the readable state names sit in the __r objects.
	activeAccount := func(nativeID, name, correlation string) billingfake.AccountSpec {
		return billingfake.AccountSpec{
			ID: nativeID, Name: name, ExternalID: correlation,
			RawStatus: "Active", ApprovalStatus: "Approved",
		}
	}

	// startStub serves the scenario and rebuilds the server over the real adapter.
	startStub := func(scenario billingfake.Scenario) {
		stub = billingstub.New(scenario)
		Expect(stub.Start("127.0.0.1:0")).To(Succeed())
		DeferCleanup(stub.Close)
		adapter, err := m360.New(billing.Config{
			BaseURL: stub.URL() + "/api/v1/external", Credential: credential, RetryCount: 0,
			BreakerThreshold: 5, BreakerWindow: time.Minute, BreakerOpen: time.Minute,
		}).SetLogger(logger).Build()
		Expect(err).ToNot(HaveOccurred())
		server, err = NewPrivateTenantBillingAssignmentsServer().SetLogger(logger).
			SetProvider(billing.NewProvider(billing.KeyM360, adapter, nil)).Build()
		Expect(err).ToNot(HaveOccurred())
	}

	// seedAccount stores the local ACTIVE row discovered earlier, so only the provider changes in these specs. It
	// returns the stored observation time so a spec can prove whether the assignment read refreshed it.
	seedAccount := func(id, nativeID, correlation string) time.Time {
		observedAt := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
		err := tm.Run(ctx, func(tx database.Tx) error {
			_, err := database.UpsertBillingAccount(ctx, tx, database.BillingAccountUpsert{
				ID: id, ProviderKey: billing.KeyM360, ProviderAccountReference: nativeID,
				CorrelationID: correlation, State: "ACTIVE", StatusObservedAt: observedAt,
			})
			return err
		})
		Expect(err).ToNot(HaveOccurred())
		return observedAt
	}

	adminCtx := func() context.Context {
		return auth.ContextWithSubject(database.TxManagerIntoContext(context.Background(), tm),
			&auth.Subject{User: "admin", Tenants: auth.AllTenants})
	}

	createRequest := func(tenantID, account, name string) *privatev1.TenantBillingAssignmentsCreateRequest {
		return privatev1.TenantBillingAssignmentsCreateRequest_builder{
			Object: privatev1.TenantBillingAssignment_builder{
				Metadata: privatev1.Metadata_builder{Name: name}.Build(),
				Spec: privatev1.TenantBillingAssignmentSpec_builder{
					TenantId: tenantID, BillingAccountId: account,
				}.Build(),
			}.Build(),
		}.Build()
	}

	updateRequest := func(id, tenantID, account string) *privatev1.TenantBillingAssignmentsUpdateRequest {
		return privatev1.TenantBillingAssignmentsUpdateRequest_builder{
			Object: privatev1.TenantBillingAssignment_builder{
				Id: id,
				Spec: privatev1.TenantBillingAssignmentSpec_builder{
					TenantId: tenantID, BillingAccountId: account,
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

	storedObservation := func(id string) (state string, observedAt time.Time) {
		Expect(testPool.QueryRow(ctx,
			`select state, status_observed_at from billing_accounts where id = $1`, id).
			Scan(&state, &observedAt)).To(Succeed())
		return state, observedAt
	}

	BeforeEach(func() {
		// Commit the shared fixture's tenant, then keep a fresh assertion transaction, as the fake-backed specs do.
		Expect(suiteTx.End(ctx)).To(Succeed())
		var err error
		suiteTx, err = tm.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		ctx = database.TxIntoContext(ctx, suiteTx)
		accountID = uuid.New()
	})

	DescribeTable("rejects a provider-only state change without a discovery List",
		func(rawStatus, approval, wantState, wantReason string) {
			accounts := []billingfake.AccountSpec{{
				ID: nativeOne, Name: "Acme", ExternalID: "corr-one",
				RawStatus: rawStatus, ApprovalStatus: approval,
			}}
			seededAt = seedAccount(accountID, nativeOne, "corr-one")
			startStub(billingfake.Scenario{Accounts: accounts})

			_, err := server.Create(adminCtx(), createRequest(testTenant, accountID, "asg-1"))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal(wantReason))
			Expect(stub.Count("GetAccount")).To(BeNumerically(">", 0),
				"eligibility must use the selected account read through the real adapter")
			Expect(stub.Count("ListAccounts")).To(BeZero(),
				"an ineligible account must be rejected before correlation resolution")
			Expect(stub.Writes()).To(BeEmpty(), "M360 correlation writes must remain unsupported")

			tx, txErr := database.TxFromContext(ctx)
			Expect(txErr).ToNot(HaveOccurred())
			_, total, listErr := database.ListTenantBillingAssignments(ctx, tx, 0, 10,
				`this.spec.tenant_id == "`+testTenant+`"`, "")
			Expect(listErr).ToNot(HaveOccurred())
			Expect(total).To(BeZero())

			// The normalized observation is persisted without another List, and its time moves forward.
			state, observedAt := storedObservation(accountID)
			Expect(state).To(Equal(wantState))
			Expect(observedAt).To(BeTemporally(">", seededAt))
		},
		Entry("deactivated account", "Inactive", "Approved", "INACTIVE", "ACCOUNT_INACTIVE"),
		Entry("unknown account", "Active", "", "UNKNOWN", "ACCOUNT_STATE_UNKNOWN"),
	)

	DescribeTable("rejects then recovers provider-only ineligible states during Update through the real adapter",
		func(replace bool, rawStatus, approval, wantState, wantReason string) {
			accounts := []billingfake.AccountSpec{
				activeAccount(nativeOne, "Acme", "corr-one"),
				activeAccount(nativeTwo, "Beta", "corr-two"),
			}
			seededAt = seedAccount(accountID, nativeOne, "corr-one")
			secondID := uuid.New()
			seedAccount(secondID, nativeTwo, "corr-two")
			startStub(billingfake.Scenario{Accounts: accounts})

			created, err := server.Create(adminCtx(), createRequest(testTenant, accountID, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(created.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
			_, observedAt := storedObservation(accountID)
			Expect(observedAt).To(BeTemporally(">", seededAt))

			// Same-account revalidation reads the current provider state and creates no new interval.
			before := len(stub.Requests())
			revalidated, err := server.Update(adminCtx(),
				updateRequest(created.GetObject().GetId(), testTenant, accountID))
			Expect(err).ToNot(HaveOccurred())
			Expect(revalidated.GetObject().GetId()).To(Equal(created.GetObject().GetId()))
			Expect(stub.Requests()[before:]).To(ContainElement(SatisfyAll(
				HaveField("Op", "GetAccount"),
				HaveField("Path", "/api/v1/external/accounts/"+nativeOne),
			)))

			// Only the provider changes the selected account; no discovery List runs. Both a replacement and
			// same-account revalidation must reject UNKNOWN just as they reject INACTIVE.
			target, index := accountID, 0
			if replace {
				target, index = secondID, 1
			}
			accounts[index].RawStatus, accounts[index].ApprovalStatus = rawStatus, approval
			stub.SetScenario(billingfake.Scenario{Accounts: accounts})
			listsBefore := stub.Count("ListAccounts")
			_, err = server.Update(adminCtx(), updateRequest(created.GetObject().GetId(), testTenant, target))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal(wantReason))
			Expect(stub.Count("ListAccounts")).To(Equal(listsBefore), "reject before correlation resolution")
			state, _ := storedObservation(target)
			Expect(state).To(Equal(wantState))

			tx, txErr := database.TxFromContext(ctx)
			Expect(txErr).ToNot(HaveOccurred())
			prior, getErr := database.GetTenantBillingAssignment(ctx, tx, created.GetObject().GetId())
			Expect(getErr).ToNot(HaveOccurred())
			Expect(prior.EffectiveTo).To(BeNil(), "the prior interval must remain current after a rejected replacement")
			Expect(prior.BillingAccountID).To(Equal(accountID))
			Expect(prior.EffectiveFrom).To(BeTemporally("==", created.GetObject().GetStatus().GetEffectiveFrom().AsTime()))
			_, total, listErr := database.ListTenantBillingAssignments(ctx, tx, 0, 10,
				`this.spec.tenant_id == "`+testTenant+`"`, "")
			Expect(listErr).ToNot(HaveOccurred())
			Expect(total).To(Equal(int32(1)), "a rejection must not add or close history")

			// Reactivation is observed by the retried Update; the persisted ineligible state must not veto recovery.
			accounts[index].RawStatus, accounts[index].ApprovalStatus = "Active", "Approved"
			stub.SetScenario(billingfake.Scenario{Accounts: accounts})
			recovered, err := server.Update(adminCtx(),
				updateRequest(created.GetObject().GetId(), testTenant, target))
			Expect(err).ToNot(HaveOccurred())
			Expect(recovered.GetObject().GetSpec().GetBillingAccountId()).To(Equal(target))
			Expect(recovered.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))

			_, total, listErr = database.ListTenantBillingAssignments(ctx, tx, 0, 10,
				`this.spec.tenant_id == "`+testTenant+`"`, "")
			Expect(listErr).ToNot(HaveOccurred())
			prior, getErr = database.GetTenantBillingAssignment(ctx, tx, created.GetObject().GetId())
			Expect(getErr).ToNot(HaveOccurred())
			if replace {
				Expect(recovered.GetObject().GetId()).ToNot(Equal(created.GetObject().GetId()))
				Expect(total).To(Equal(int32(2)), "the successful replacement retains contiguous history")
				Expect(prior.EffectiveTo).ToNot(BeNil())
				Expect(*prior.EffectiveTo).To(BeTemporally("==", recovered.GetObject().GetStatus().GetEffectiveFrom().AsTime()))
			} else {
				Expect(recovered.GetObject().GetId()).To(Equal(created.GetObject().GetId()))
				Expect(recovered.GetObject().GetStatus().GetEffectiveFrom()).To(Equal(
					created.GetObject().GetStatus().GetEffectiveFrom()))
				Expect(total).To(Equal(int32(1)), "same-account recovery creates no new history")
				Expect(prior.EffectiveTo).To(BeNil())
			}
			Expect(stub.Writes()).To(BeEmpty(), "reassignment must never attempt an M360 account write")
		},
		Entry("INACTIVE same-account revalidation", false, "Inactive", "Approved", "INACTIVE", "ACCOUNT_INACTIVE"),
		Entry("INACTIVE replacement", true, "Inactive", "Approved", "INACTIVE", "ACCOUNT_INACTIVE"),
		Entry("UNKNOWN same-account revalidation", false, "Active", "", "UNKNOWN", "ACCOUNT_STATE_UNKNOWN"),
		Entry("UNKNOWN replacement", true, "Active", "", "UNKNOWN", "ACCOUNT_STATE_UNKNOWN"),
	)

	It("rejects a foreign account in a selected-account HTTP response before any observation or correlation mutation", func() {
		const foreignID = "m360-foreign-native"
		seededAt = seedAccount(accountID, nativeOne, "")
		_, err := testPool.Exec(ctx, `update billing_accounts set state = 'INACTIVE' where id = $1`, accountID)
		Expect(err).ToNot(HaveOccurred())
		// The selected account is locally INACTIVE. An inconsistent Get returns a different ACTIVE account,
		// whose private correlation must never be adopted. The fallback would allow resolution to succeed if
		// the server ignored that mismatched reference, making this a behavioral regression rather than an HTTP error.
		fallback := billingstub.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			activeAccount(nativeOne, "Private Account Payload", "foreign-private-correlation"),
		}})
		requests := make(chan string, 10)
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests <- r.Method + " " + r.URL.Path
			if r.Method == http.MethodGet && r.URL.Path == "/api/v1/external/accounts/"+nativeOne {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"success":true,"errors":[],"data":{"Id":"m360-foreign-native",
					"Name":"Private Account Payload","ExternalId":"foreign-private-correlation",
					"Status__r":{"Name":"Active"},"ApprovalStatus__r":{"Name":"Approved"}}}`)
				return
			}
			fallback.ServeHTTP(w, r)
		}))
		DeferCleanup(endpoint.Close)
		var logs bytes.Buffer
		privateLogger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		adapter, err := m360.New(billing.Config{
			BaseURL: endpoint.URL + "/api/v1/external", Credential: credential, RetryCount: 0,
			BreakerThreshold: 5, BreakerWindow: time.Minute, BreakerOpen: time.Minute,
		}).SetLogger(privateLogger).Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(adapter.SupportsCorrelationWrite()).To(BeFalse())
		server, err = NewPrivateTenantBillingAssignmentsServer().SetLogger(privateLogger).
			SetProvider(billing.NewProvider(billing.KeyM360, adapter, nil)).Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = server.Create(adminCtx(), createRequest(testTenant, accountID, "asg-1"))
		Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
		Expect(reason(err)).To(Equal(string(billing.ReasonMalformed)))
		for _, sensitive := range []string{nativeOne, foreignID, "Private Account Payload", "foreign-private-correlation", credential} {
			Expect(err.Error()).NotTo(ContainSubstring(sensitive))
			Expect(logs.String()).NotTo(ContainSubstring(sensitive))
		}
		Expect(requests).To(Receive(Equal("GET /api/v1/external/accounts/" + nativeOne)))
		Expect(requests).NotTo(Receive(), "reject before any correlation reads, scans or provider writes")
		state, observedAt := storedObservation(accountID)
		Expect(state).To(Equal("INACTIVE"), "never apply a foreign account's ACTIVE state")
		Expect(observedAt).To(BeTemporally("==", seededAt))
		var correlation string
		Expect(testPool.QueryRow(ctx, `select coalesce(correlation_id, '') from billing_accounts where id = $1`, accountID).
			Scan(&correlation)).To(Succeed())
		Expect(correlation).To(BeEmpty())
		var count int
		Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero(), "no assignment history may be created")
		Expect(testPool.QueryRow(ctx, `select count(*) from billing_operation_leases`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	})

	It("persists no assignment and no observation when the real adapter read fails", func() {
		seededAt = seedAccount(accountID, nativeOne, "corr-one")
		startStub(billingfake.Scenario{
			Accounts: []billingfake.AccountSpec{activeAccount(nativeOne, "Acme", "corr-one")},
			Failures: map[string]billingfake.Failure{"GetAccount": {Reason: billing.ReasonUnavailable}},
		})

		_, err := server.Create(adminCtx(), createRequest(testTenant, accountID, "asg-1"))
		Expect(status.Code(err)).To(Equal(codes.Unavailable))
		Expect(stub.Count("GetAccount")).To(Equal(1), "a non-retryable provider read must not be retried")
		Expect(stub.Writes()).To(BeEmpty())

		tx, txErr := database.TxFromContext(ctx)
		Expect(txErr).ToNot(HaveOccurred())
		_, total, listErr := database.ListTenantBillingAssignments(ctx, tx, 0, 10,
			`this.spec.tenant_id == "`+testTenant+`"`, "")
		Expect(listErr).ToNot(HaveOccurred())
		Expect(total).To(BeZero())

		state, observedAt := storedObservation(accountID)
		Expect(state).To(Equal("ACTIVE"), "a failed read must not invent an observation")
		Expect(observedAt).To(BeTemporally("==", seededAt))
	})
})
