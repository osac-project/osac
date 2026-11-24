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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/billing/m360"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
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

	buildServer := func(endpoint string) {
		adapter, err := m360.New(billing.Config{
			BaseURL: endpoint + "/api/v1/external", Credential: credential, RetryCount: 0,
			BreakerThreshold: 5, BreakerWindow: time.Minute, BreakerOpen: time.Minute,
		}).SetLogger(logger).Build()
		Expect(err).ToNot(HaveOccurred())
		server, err = NewPrivateTenantBillingAssignmentsServer().SetLogger(logger).
			SetProvider(billing.NewProvider(billing.KeyM360, adapter, nil)).Build()
		Expect(err).ToNot(HaveOccurred())
	}

	// startStub serves the scenario and rebuilds the server over the real adapter.
	startStub := func(scenario billingfake.Scenario) {
		stub = billingstub.New(scenario)
		Expect(stub.Start("127.0.0.1:0")).To(Succeed())
		DeferCleanup(stub.Close)
		buildServer(stub.URL())
	}

	// seedAccount stores the local ACTIVE row discovered earlier, so only the provider changes in these specs. It
	// returns the stored observation time so a spec can prove whether the assignment read refreshed it.
	seedAccount := func(id, nativeID, correlation string) time.Time {
		observedAt := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
		err := tm.Run(ctx, func(tx database.Tx) error {
			d, err := dao.NewGenericDAO[*privatev1.BillingAccount]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			if err != nil {
				return err
			}
			name, err := billingAccountName(id)
			if err != nil {
				return err
			}
			observed := privatev1.BillingAccountStatus_builder{ProviderKey: billing.KeyM360, ProviderAccountReference: nativeID,
				State: privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE, StateObservationTimestamp: timestamppb.New(observedAt)}.Build()
			if correlation != "" {
				observed.SetExternalBillingAccountId(correlation)
			}
			_, err = d.Create().SetObject(privatev1.BillingAccount_builder{Id: id,
				Metadata: privatev1.Metadata_builder{Name: name, Tenant: auth.SystemTenant, Creator: "system"}.Build(),
				Spec:     &privatev1.BillingAccountSpec{}, Status: observed,
			}.Build()).Do(database.TxIntoContext(ctx, tx))
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
				Metadata: privatev1.Metadata_builder{Name: name, Tenant: tenantID}.Build(),
				Spec: privatev1.TenantBillingAssignmentSpec_builder{
					BillingAccount: privatev1.BillingAccountReference_builder{Id: account}.Build(),
				}.Build(),
			}.Build(),
		}.Build()
	}

	updateRequest := func(id, tenantID, account string) *privatev1.TenantBillingAssignmentsUpdateRequest {
		return privatev1.TenantBillingAssignmentsUpdateRequest_builder{
			Object: privatev1.TenantBillingAssignment_builder{
				Id:       id,
				Metadata: privatev1.Metadata_builder{Tenant: tenantID}.Build(),
				Spec: privatev1.TenantBillingAssignmentSpec_builder{
					BillingAccount: privatev1.BillingAccountReference_builder{Id: account}.Build(),
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
			`select data->'status'->>'state', (data->'status'->>'state_observation_timestamp')::timestamptz from billing_accounts where id = $1`, id).
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

			_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, accountID, "asg-1"))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal(wantReason))
			Expect(stub.Count("GetAccount")).To(BeNumerically(">", 0),
				"eligibility must use the selected account read through the real adapter")
			Expect(stub.Count("ListAccounts")).To(BeZero(),
				"an ineligible account must be rejected before correlation resolution")
			Expect(stub.Writes()).To(BeEmpty(), "M360 correlation writes must remain unsupported")

			page, listErr := server.bindings.List().SetLimit(10).SetFilter(`this.metadata.tenant == "` + testTenant + `"`).Do(ctx)
			Expect(listErr).ToNot(HaveOccurred())
			Expect(page.GetTotal()).To(BeZero())

			// A rejected RPC must not persist a separate eligibility observation.
			state, observedAt := storedObservation(accountID)
			Expect(state).To(Equal(mapBillingAccountState("ACTIVE").String()))
			Expect(observedAt).To(BeTemporally("==", seededAt))
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

			created, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, accountID, "asg-1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(created.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
			_, observedAt := storedObservation(accountID)
			Expect(observedAt).To(BeTemporally(">", seededAt))

			// Same-account revalidation reads the current provider state and creates no new interval.
			before := len(stub.Requests())
			revalidated, err := billingRPC(adminCtx(), server.Update,
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
			_, err = billingRPC(adminCtx(), server.Update, updateRequest(created.GetObject().GetId(), testTenant, target))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal(wantReason))
			Expect(stub.Count("ListAccounts")).To(Equal(listsBefore), "reject before correlation resolution")
			state, _ := storedObservation(target)
			Expect(state).To(Equal(mapBillingAccountState("ACTIVE").String()), "failed revalidation rolls its observation back")

			prior, getErr := server.bindings.Get().SetId(created.GetObject().GetId()).Do(ctx)
			Expect(getErr).ToNot(HaveOccurred())
			Expect(prior.GetObject().GetStatus().GetEffectiveToTimestamp()).To(BeNil(), "the prior interval must remain current after a rejected replacement")
			Expect(prior.GetObject().GetSpec().GetBillingAccount().GetId()).To(Equal(accountID))
			Expect(prior.GetObject().GetStatus().GetEffectiveFromTimestamp().AsTime()).To(BeTemporally("==", created.GetObject().GetStatus().GetEffectiveFromTimestamp().AsTime()))
			page, listErr := server.bindings.List().SetLimit(10).SetFilter(`this.metadata.tenant == "` + testTenant + `"`).Do(ctx)
			Expect(listErr).ToNot(HaveOccurred())
			Expect(page.GetTotal()).To(Equal(int32(1)), "a rejection must not add or close history")

			// Reactivation is observed by the retried Update; the persisted ineligible state must not veto recovery.
			accounts[index].RawStatus, accounts[index].ApprovalStatus = "Active", "Approved"
			stub.SetScenario(billingfake.Scenario{Accounts: accounts})
			recovered, err := billingRPC(adminCtx(), server.Update,
				updateRequest(created.GetObject().GetId(), testTenant, target))
			Expect(err).ToNot(HaveOccurred())
			Expect(recovered.GetObject().GetSpec().GetBillingAccount().GetId()).To(Equal(target))
			Expect(recovered.GetObject().GetStatus().GetConnectionState()).To(Equal(
				privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))

			page, listErr = server.bindings.List().SetLimit(10).SetFilter(`this.metadata.tenant == "` + testTenant + `"`).Do(ctx)
			Expect(listErr).ToNot(HaveOccurred())
			prior, getErr = server.bindings.Get().SetId(created.GetObject().GetId()).Do(ctx)
			Expect(getErr).ToNot(HaveOccurred())
			if replace {
				Expect(recovered.GetObject().GetId()).ToNot(Equal(created.GetObject().GetId()))
				Expect(page.GetTotal()).To(Equal(int32(2)), "the successful replacement retains contiguous history")
				Expect(prior.GetObject().GetStatus().GetEffectiveToTimestamp()).ToNot(BeNil())
				Expect(prior.GetObject().GetStatus().GetEffectiveToTimestamp().AsTime()).To(BeTemporally("==", recovered.GetObject().GetStatus().GetEffectiveFromTimestamp().AsTime()))
			} else {
				Expect(recovered.GetObject().GetId()).To(Equal(created.GetObject().GetId()))
				Expect(proto.Equal(recovered.GetObject().GetStatus().GetEffectiveFromTimestamp(),
					created.GetObject().GetStatus().GetEffectiveFromTimestamp())).To(BeTrue())
				Expect(page.GetTotal()).To(Equal(int32(1)), "same-account recovery creates no new history")
				Expect(prior.GetObject().GetStatus().GetEffectiveToTimestamp()).To(BeNil())
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
		Expect(tm.Run(ctx, func(tx database.Tx) error {
			c := database.TxIntoContext(ctx, tx)
			d, err := dao.NewGenericDAO[*privatev1.BillingAccount]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			if err != nil {
				return err
			}
			r, err := d.Get().SetId(accountID).Do(c)
			if err != nil {
				return err
			}
			r.GetObject().GetStatus().SetState(privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_INACTIVE)
			_, err = d.Update().SetObject(r.GetObject()).Do(c)
			return err
		})).To(Succeed())
		// The selected account is locally INACTIVE. An inconsistent Get returns a different ACTIVE account,
		// whose private correlation must never be adopted. The fallback would allow resolution to succeed if
		// the server ignored that mismatched reference, making this a behavioral regression rather than an HTTP error.
		fallback := billingstub.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			activeAccount(nativeOne, "Private Account Payload", "foreign-private-correlation"),
		}})
		const foreignAccountPayload = `{"success":true,"errors":[],"data":{"Id":"m360-foreign-native",` +
			`"Name":"Private Account Payload","ExternalId":"foreign-private-correlation",` +
			`"Status__r":{"Name":"Active"},"ApprovalStatus__r":{"Name":"Approved"}}}`
		// The fixture's write result reaches the spec, so a truncated payload cannot masquerade as the MALFORMED
		// rejection this spec expects.
		type fixtureWrite struct {
			Written int
			Err     error
		}
		requests := make(chan string, 10)
		writes := make(chan fixtureWrite, 10)
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests <- r.Method + " " + r.URL.Path
			if r.Method == http.MethodGet && r.URL.Path == "/api/v1/external/accounts/"+nativeOne {
				w.Header().Set("Content-Type", "application/json")
				written, err := io.WriteString(w, foreignAccountPayload)
				writes <- fixtureWrite{Written: written, Err: err}
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
		server, err = NewPrivateTenantBillingAssignmentsServer().SetLogger(privateLogger).
			SetProvider(billing.NewProvider(billing.KeyM360, adapter, nil)).Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = billingRPC(adminCtx(), server.Create, createRequest(testTenant, accountID, "asg-1"))
		Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
		Expect(reason(err)).To(Equal(string(billing.ReasonMalformed)))
		for _, sensitive := range []string{nativeOne, foreignID, "Private Account Payload", "foreign-private-correlation", credential} {
			Expect(err.Error()).NotTo(ContainSubstring(sensitive))
			Expect(logs.String()).NotTo(ContainSubstring(sensitive))
		}
		Expect(writes).To(Receive(SatisfyAll(
			HaveField("Written", len(foreignAccountPayload)),
			HaveField("Err", BeNil()),
		)), "the fixture must write its complete response before the adapter can classify it")
		Expect(requests).To(Receive(Equal("GET /api/v1/external/accounts/" + nativeOne)))
		Expect(requests).NotTo(Receive(), "reject before any correlation reads, scans or provider writes")
		state, observedAt := storedObservation(accountID)
		Expect(state).To(Equal(mapBillingAccountState("INACTIVE").String()), "never apply a foreign account's ACTIVE state")
		Expect(observedAt).To(BeTemporally("==", seededAt))
		var correlation string
		Expect(testPool.QueryRow(ctx, `select coalesce(data->'status'->>'external_billing_account_id', '') from billing_accounts where id = $1`, accountID).
			Scan(&correlation)).To(Succeed())
		Expect(correlation).To(BeEmpty())
		var count int
		Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero(), "no assignment history may be created")
		Expect(server.assignSem).To(BeEmpty())
	})

	DescribeTable("rejects a later ineligible state on the selected exact carrier without changing stored rows",
		func(rawState, approval, wantReason string) {
			seededAt = seedAccount(accountID, nativeOne, "")
			active := activeAccount(nativeOne, "Acme", "corr-one")
			selected := billingstub.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{active}})
			active.RawStatus, active.ApprovalStatus = rawState, approval
			scan := billingstub.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{active}})
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/external/accounts/"+nativeOne {
					selected.ServeHTTP(w, r)
					return
				}
				scan.ServeHTTP(w, r)
			}))
			DeferCleanup(endpoint.Close)
			buildServer(endpoint.URL)

			_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, accountID, "later-state"))
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(reason(err)).To(Equal(wantReason))
			Expect(selected.Count("GetAccount")).To(Equal(1), "reuse the verified selected read for the token")
			Expect(scan.Count("ListAccounts")).To(Equal(1))
			Expect(selected.Writes()).To(BeEmpty())
			Expect(scan.Writes()).To(BeEmpty())
			state, observedAt := storedObservation(accountID)
			Expect(state).To(Equal(mapBillingAccountState("ACTIVE").String()))
			Expect(observedAt).To(BeTemporally("==", seededAt))
			var retained *string
			Expect(testPool.QueryRow(ctx, `select data->'status'->>'external_billing_account_id' from billing_accounts where id = $1`, accountID).
				Scan(&retained)).To(Succeed())
			Expect(retained).To(BeNil())
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
		},
		Entry("INACTIVE carrier", "Inactive", "Approved", "ACCOUNT_INACTIVE"),
		Entry("UNKNOWN carrier", "Active", "", "ACCOUNT_STATE_UNKNOWN"),
	)

	It("adopts only the verified corrected token after an ambiguous assignment attempt", func() {
		scenario := billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			activeAccount(nativeOne, "Acme", "duplicate"), activeAccount(nativeTwo, "Beta", "duplicate"),
		}}
		startStub(scenario)
		discovery, err := NewPrivateBillingAccountsServer().SetLogger(logger).SetProvider(server.provider).Build()
		Expect(err).NotTo(HaveOccurred())
		old := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
		Expect(billingStore(adminCtx(), discovery, []billing.Account{
			{Ref: billing.NewAccountRef(nativeOne), ExternalID: "duplicate", State: billing.AccountActive},
			{Ref: billing.NewAccountRef(nativeTwo), ExternalID: "duplicate", State: billing.AccountActive},
		}, old)).To(Succeed())
		storedTokens := func() (observed string, retained *string) {
			Expect(testPool.QueryRow(ctx, `select id, data->'status'->>'provider_external_id', data->'status'->>'external_billing_account_id'
				from billing_accounts where data->'status'->>'provider_account_reference' = $1`, nativeOne).
				Scan(&accountID, &observed, &retained)).To(Succeed())
			return observed, retained
		}
		observed, retained := storedTokens()
		Expect(observed).To(Equal("duplicate"))
		Expect(retained).To(BeNil())
		request := createRequest(testTenant, accountID, "corrected-token")
		_, err = billingRPC(adminCtx(), server.Create, request)
		Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
		Expect(reason(err)).To(Equal(billing.ReasonAccountAmbiguous))
		_, retained = storedTokens()
		Expect(retained).To(BeNil())
		var count int
		Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())

		// Only the provider changes. The last discovered token must neither conflict nor be silently adopted.
		scenario.Accounts[0].ExternalID = " corrected "
		stub.SetScenario(scenario)
		before := stub.Count("GetAccount")
		created, err := billingRPC(adminCtx(), server.Create, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.GetObject().GetStatus().GetConnectionState()).To(Equal(mapTenantBillingAssignmentConnectionState(tbaStateConnected)))
		observed, retained = storedTokens()
		Expect(observed).To(Equal("duplicate"), "assignment does not rewrite the discovery token")
		Expect(retained).To(Equal(proto.String(" corrected ")))
		Expect(stub.Count("GetAccount") - before).To(Equal(1))
		Expect(stub.Writes()).To(BeEmpty())
	})

	It("persists no assignment and no observation when the real adapter read fails", func() {
		seededAt = seedAccount(accountID, nativeOne, "corr-one")
		startStub(billingfake.Scenario{
			Accounts: []billingfake.AccountSpec{activeAccount(nativeOne, "Acme", "corr-one")},
			Failures: map[string]billingfake.Failure{"GetAccount": {Reason: billing.ReasonUnavailable}},
		})

		_, err := billingRPC(adminCtx(), server.Create, createRequest(testTenant, accountID, "asg-1"))
		Expect(status.Code(err)).To(Equal(codes.Unavailable))
		Expect(stub.Count("GetAccount")).To(Equal(1), "a non-retryable provider read must not be retried")
		Expect(stub.Writes()).To(BeEmpty())

		page, listErr := server.bindings.List().SetLimit(10).SetFilter(`this.metadata.tenant == "` + testTenant + `"`).Do(ctx)
		Expect(listErr).ToNot(HaveOccurred())
		Expect(page.GetTotal()).To(BeZero())

		state, observedAt := storedObservation(accountID)
		Expect(state).To(Equal(mapBillingAccountState("ACTIVE").String()), "a failed read must not invent an observation")
		Expect(observedAt).To(BeTemporally("==", seededAt))
	})
})
