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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// twoPageThenFailAdapter serves one successful page of one account, then fails every call after that - it
// reproduces a provider that fails partway through a multi-page discovery.
type twoPageThenFailAdapter struct {
	billing.Adapter
	calls int
}

func (a *twoPageThenFailAdapter) ListAccounts(_ context.Context, _ billing.AccountFilter, _ billing.Page) (
	billing.AccountPage, error) {
	a.calls++
	if a.calls == 1 {
		return billing.AccountPage{
			Accounts: []billing.Account{{Ref: billing.NewAccountRef("acct-1"), State: billing.AccountActive}},
			Next:     "page-2",
		}, nil
	}
	return billing.AccountPage{}, billing.NewError(billing.ReasonUnavailable, "ListAccounts", false, 503)
}

var _ = Describe("Private billing accounts server", func() {
	build := func(p *billing.Provider) *PrivateBillingAccountsServer {
		server, err := NewPrivateBillingAccountsServer().SetLogger(logger).SetProvider(p).Build()
		Expect(err).ToNot(HaveOccurred())
		return server
	}
	list := func(s *PrivateBillingAccountsServer, req *privatev1.BillingAccountsListRequest) (
		*privatev1.BillingAccountsListResponse, error) {
		if req == nil {
			req = &privatev1.BillingAccountsListRequest{}
		}
		return s.List(ctx, req)
	}

	It("requires a logger and a provider", func() {
		_, err := NewPrivateBillingAccountsServer().SetProvider(billing.NewProvider("", nil, nil)).Build()
		Expect(err).To(MatchError("logger is mandatory"))
		_, err = NewPrivateBillingAccountsServer().SetLogger(logger).Build()
		Expect(err).To(MatchError("provider is mandatory"))
	})

	It("fails with FAILED_PRECONDITION/BILLING_NOT_CONFIGURED when billing is off, without calling the provider",
		func() {
			fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{{ID: "a1"}}})
			server := build(billing.NewProvider("", fake, nil))
			_, err := list(server, nil)
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(codes.FailedPrecondition))
			Expect(st.Details()[0].(*errdetails.ErrorInfo).GetReason()).To(Equal("BILLING_NOT_CONFIGURED"))
			Expect(fake.Calls()).To(BeEmpty())
		})

	It("fails with UNAVAILABLE and never serves a stale list when the provider cannot be reached", func() {
		fake := billingfake.New(billingfake.Scenario{
			Accounts: []billingfake.AccountSpec{{ID: "a1", State: billing.AccountActive}},
			Failures: map[string]billingfake.Failure{"ListAccounts": {Reason: billing.ReasonUnavailable}},
		})
		server := build(fake.Provider())
		_, err := list(server, nil)
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unavailable))
		Expect(st.Details()[0].(*errdetails.ErrorInfo).GetReason()).To(Equal("BILLING_PROVIDER_UNAVAILABLE"))
	})

	It("discovers, upserts, and lists accounts with an empty items list meaning none discovered, not disabled",
		func() {
			fake := billingfake.New(billingfake.Scenario{})
			response, err := list(build(fake.Provider()), nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(BeEmpty())
			Expect(response.GetTotal()).To(BeZero())
		})

	It("maps provider_key, display_name, state, correlation_id and status_observed_at", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: "acct-1", Name: "Acme", ExternalID: "ext-1", State: billing.AccountActive},
		}})
		response, err := list(build(fake.Provider()), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(1))
		item := response.GetItems()[0]
		Expect(item.GetProviderKey()).To(Equal("fake"))
		Expect(item.GetDisplayName()).To(Equal("Acme"))
		Expect(item.GetState()).To(Equal(privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE))
		Expect(item.GetCorrelationId()).To(Equal("ext-1"))
		Expect(item.HasStatusObservedAt()).To(BeTrue())
		Expect(item.GetId()).ToNot(BeEmpty())
		Expect(item.GetId()).ToNot(Equal("acct-1"), "id must be an OSAC-generated id, not the provider reference")
	})

	It("leaves base_currency empty: the fake, like M360, supplies none by default", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{{ID: "acct-1"}}})
		response, err := list(build(fake.Provider()), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()[0].GetBaseCurrency()).To(BeEmpty())
	})

	It("maps an unknown or inactive state and never maps a raw provider value to ACTIVE", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: "a1", State: billing.AccountUnknown}, {ID: "a2", State: billing.AccountInactive},
		}})
		response, err := list(build(fake.Provider()), &privatev1.BillingAccountsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		states := map[privatev1.BillingAccountState]bool{}
		for _, item := range response.GetItems() {
			states[item.GetState()] = true
		}
		Expect(states).To(HaveKey(privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_UNKNOWN))
		Expect(states).To(HaveKey(privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_INACTIVE))
	})

	It("pages through more accounts than the discovery page size", func() {
		var accounts []billingfake.AccountSpec
		for i := range 45 {
			accounts = append(accounts, billingfake.AccountSpec{ID: fmt.Sprintf("acct-%02d", i), State: billing.AccountActive})
		}
		fake := billingfake.New(billingfake.Scenario{Accounts: accounts})
		req := &privatev1.BillingAccountsListRequest{}
		req.SetLimit(100)
		response, err := list(build(fake.Provider()), req)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetTotal()).To(Equal(int32(45)))
		Expect(fake.Count("ListAccounts")).To(BeNumerically(">=", 3), "45 accounts at page size 20 needs 3 pages")
	})

	It("retains an account that disappears from the provider and marks it UNKNOWN on the next List", func() {
		scenario := billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: "acct-1", State: billing.AccountActive}, {ID: "acct-2", State: billing.AccountActive},
		}}
		fake := billingfake.New(scenario)
		server := build(fake.Provider())
		_, err := list(server, nil)
		Expect(err).ToNot(HaveOccurred())

		// acct-2 disappears from the provider's next read:
		fake2 := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: "acct-1", State: billing.AccountActive},
		}})
		server2 := build(fake2.Provider())
		response, err := list(server2, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetTotal()).To(Equal(int32(2)), "the vanished account's row must be retained, not deleted")
		var sawUnknown bool
		for _, item := range response.GetItems() {
			if item.GetState() == privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_UNKNOWN {
				sawUnknown = true
			}
		}
		Expect(sawUnknown).To(BeTrue())
	})

	It("leaves no row committed when discovery fails partway through pagination", func() {
		adapter := &twoPageThenFailAdapter{}
		server := build(billing.NewProvider("m360", adapter, nil))
		_, err := list(server, nil)
		Expect(err).To(HaveOccurred())

		// Reproduce what the production TxInterceptor does after a handler returns (see
		// database_tx_interceptor.go): it ends the transaction directly, with no call of its own to
		// tx.ReportError. Only the handler itself can mark the transaction for rollback; if List never does
		// that, End sees nothing reported and commits anyway.
		tx, txErr := database.TxFromContext(ctx)
		Expect(txErr).ToNot(HaveOccurred())
		Expect(tx.End(ctx)).To(Succeed())

		// Read back from a new transaction against the same database, the way a later request would.
		freshTx, err := tm.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		defer func() { Expect(freshTx.End(ctx)).To(Succeed()) }()
		_, total, err := database.ListBillingAccounts(ctx, freshTx, 0, 10, "", "")
		Expect(err).ToNot(HaveOccurred())
		Expect(total).To(BeZero(), "the page-1 upsert must not survive a failed discovery call")

		suiteTx = nil // already ended above; stop the suite's DeferCleanup from ending it again
	})

	It("rejects an unsupported filter with INVALID_ARGUMENT", func() {
		fake := billingfake.New(billingfake.Scenario{})
		req := &privatev1.BillingAccountsListRequest{}
		req.SetFilter(`this.state != "ACTIVE"`)
		_, err := list(build(fake.Provider()), req)
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.InvalidArgument))
	})
})
