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
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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

// ListAccounts returns one account on the first call and a 503 error on every call after that.
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

// endlessDiscoveryAdapter serves the same single-account page with a following page forever - it reproduces a
// provider that never reports a last page.
type endlessDiscoveryAdapter struct {
	billing.Adapter
}

// ListAccounts always returns one account and a non-empty Next token.
func (a *endlessDiscoveryAdapter) ListAccounts(_ context.Context, _ billing.AccountFilter, _ billing.Page) (
	billing.AccountPage, error) {
	return billing.AccountPage{
		Accounts: []billing.Account{{Ref: billing.NewAccountRef("acct-1"), State: billing.AccountActive}},
		Next:     "always-more",
	}, nil
}

// hangingDiscoveryAdapter blocks every ListAccounts call until its context is done - it reproduces a provider
// that accepts requests but never answers, so the discovery deadline is the only way out.
type hangingDiscoveryAdapter struct {
	billing.Adapter
}

// ListAccounts never answers before the caller's context is done.
func (a *hangingDiscoveryAdapter) ListAccounts(ctx context.Context, _ billing.AccountFilter, _ billing.Page) (
	billing.AccountPage, error) {
	<-ctx.Done()
	return billing.AccountPage{}, billing.NewError(billing.ReasonTimeout, "ListAccounts", true, 0)
}

// gatedDiscoveryAdapter blocks its ListAccounts calls until released - it reproduces a discovery walk that is
// in flight while another List arrives. entered closes once the walk has reached the provider, so the suite can
// observe the walk started without sleeping.
type gatedDiscoveryAdapter struct {
	billing.Adapter
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	calls   int
}

// ListAccounts signals that a walk has reached the provider and then blocks until released.
func (a *gatedDiscoveryAdapter) ListAccounts(_ context.Context, _ billing.AccountFilter, _ billing.Page) (
	billing.AccountPage, error) {
	a.calls++
	a.once.Do(func() { close(a.entered) })
	<-a.release
	return billing.AccountPage{
		Accounts: []billing.Account{{Ref: billing.NewAccountRef("acct-1"), State: billing.AccountActive}},
	}, nil
}

// gatedTwoPageAdapter serves the first page immediately and holds the second page until released - it
// reproduces a multi-page walk that is in flight, waiting on the provider, while the suite checks that the
// walk holds no database connection. enteredSecondPage closes once the walk has asked for page two.
type gatedTwoPageAdapter struct {
	billing.Adapter
	enteredSecondPage chan struct{}
	release           chan struct{}
	once              sync.Once
	calls             int
}

// ListAccounts returns page one on the first call; every later call signals the walk has reached the
// provider's slow page and then blocks until released.
func (a *gatedTwoPageAdapter) ListAccounts(_ context.Context, _ billing.AccountFilter, _ billing.Page) (
	billing.AccountPage, error) {
	a.calls++
	if a.calls == 1 {
		return billing.AccountPage{
			Accounts: []billing.Account{{Ref: billing.NewAccountRef("acct-1"), State: billing.AccountActive}},
			Next:     "page-2",
		}, nil
	}
	a.once.Do(func() { close(a.enteredSecondPage) })
	<-a.release
	return billing.AccountPage{}, nil
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
	// storedCount reads the billing_accounts row count on the suite pool's own connection, outside any
	// transaction: like a later request in another process, it can only observe durably committed rows, never
	// an ambient request transaction's uncommitted state.
	storedCount := func() int {
		var count int
		Expect(testPool.QueryRow(ctx, `select count(*) from billing_accounts`).Scan(&count)).To(Succeed())
		return count
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

	It("sanitizes a provider display name carrying control characters before storing it", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: "acct-1", Name: "Acme\x1b]52;x;wg\x07 production", State: billing.AccountActive},
		}})
		response, err := list(build(fake.Provider()), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(1))
		// The List serves the stored row, so this asserts the ingest boundary itself: the ESC/BEL pair
		// must not survive the upsert, whatever later reader or render path touches the stored name.
		Expect(response.GetItems()[0].GetDisplayName()).To(Equal("Acme]52;x;wg production"))
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

	It("marks every known account UNKNOWN when the provider's list becomes empty", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: "acct-1", State: billing.AccountActive},
		}})
		server := build(fake.Provider())
		_, err := list(server, nil)
		Expect(err).ToNot(HaveOccurred())

		// The provider's list becomes empty: the sweep must still run, marking the stored row UNKNOWN.
		// This is the nil-seen regression: an empty walk collects no references, and pgx encodes a nil
		// slice as SQL NULL, which the sweep's not-in predicate never matches.
		empty := billingfake.New(billingfake.Scenario{})
		server2 := build(empty.Provider())
		response, err := list(server2, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetTotal()).To(Equal(int32(1)), "the vanished account's row must be retained, not deleted")
		Expect(response.GetItems()[0].GetState()).
			To(Equal(privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_UNKNOWN))
	})

	It("leaves no row stored when discovery fails partway through pagination", func() {
		adapter := &twoPageThenFailAdapter{}
		server := build(billing.NewProvider("m360", adapter, nil))
		_, err := list(server, nil)
		Expect(err).To(HaveOccurred())

		// The walk failed on page two, before any store transaction ran. Read back outside any request
		// transaction, the way a later request would: nothing from page one may have been stored.
		Expect(storedCount()).To(BeZero(), "the page-1 accounts must not be stored by a failed walk")
	})

	It("stores the whole walk or nothing, and a failed store does not open the freshness window", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: "acct-1", State: billing.AccountActive},
			{ID: "acct-2", State: billing.AccountActive},
		}})
		server := build(fake.Provider())
		server.discoverFailureBackoff = 10 * time.Millisecond

		// Reject only the second account's write, the same trigger-injection pattern as
		// service_account_create_test.go: the first upsert succeeds inside the store transaction, so only a
		// real rollback - not failing before any write - can keep it out of the table.
		_, err := testPool.Exec(ctx, `create function reject_second_billing_account() returns trigger language plpgsql as $$
		begin
			if new.provider_account_reference = 'acct-2' then
				raise exception 'test write failure';
			end if;
			return new;
		end $$;
		create trigger reject_second_billing_account before insert or update on billing_accounts
		for each row execute function reject_second_billing_account()`)
		Expect(err).ToNot(HaveOccurred())

		_, err = list(server, nil)
		Expect(err).To(HaveOccurred())
		Expect(storedCount()).To(BeZero(),
			"the store transaction must roll back its first upsert together with the failed second")

		_, err = testPool.Exec(ctx, `drop trigger reject_second_billing_account on billing_accounts;
		drop function reject_second_billing_account()`)
		Expect(err).ToNot(HaveOccurred())

		// The failed store must not open the freshness window: once the failure backoff has elapsed, the
		// next List runs a second full walk instead of serving a stored page.
		var response *privatev1.BillingAccountsListResponse
		Eventually(func() error {
			var listErr error
			response, listErr = list(server, nil)
			return listErr
		}, 5*time.Second).Should(Succeed())
		Expect(response.GetTotal()).To(Equal(int32(2)))
		Expect(fake.Count("ListAccounts")).To(Equal(2),
			"only a durably committed walk may open the freshness window")
	})

	It("commits discovery durably: a later List inside the freshness window still reads the stored rows", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: "acct-1", State: billing.AccountActive},
		}})
		server := build(fake.Provider())
		first, err := list(server, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(first.GetTotal()).To(Equal(int32(1)))
		// A connection outside any request transaction sees the row: the store committed durably rather
		// than riding on this test's ambient suite transaction.
		Expect(storedCount()).To(Equal(1))

		// An invalid filter fails after discovery already succeeded, without disturbing the stored rows:
		req := &privatev1.BillingAccountsListRequest{}
		req.SetFilter(`this.state != "ACTIVE"`)
		_, err = list(server, req)
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.InvalidArgument))

		// ...and a second List inside the freshness window still reads the committed rows through its own
		// short read transaction, without re-reading the provider.
		second, err := list(server, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(second.GetTotal()).To(Equal(int32(1)))
		Expect(fake.Count("ListAccounts")).To(Equal(1), "still inside the freshness window")
	})

	It("holds no database connection while the provider is slow: an unrelated query still runs on a one-connection pool",
		func() {
			adapter := &gatedTwoPageAdapter{
				enteredSecondPage: make(chan struct{}),
				release:           make(chan struct{}),
			}

			// A pool with a single connection, and the server's transaction manager over that pool: if the
			// discovery walk held a connection (or an open transaction) while waiting on the provider,
			// nothing else could use the pool.
			config, err := pgxpool.ParseConfig(testPool.Config().ConnString())
			Expect(err).ToNot(HaveOccurred())
			config.MaxConns = 1
			singlePool, err := pgxpool.NewWithConfig(ctx, config)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(singlePool.Close)
			singlePoolTm, err := database.NewTxManager().
				SetLogger(logger).
				SetPool(singlePool).
				Build()
			Expect(err).ToNot(HaveOccurred())
			singlePoolCtx := database.TxManagerIntoContext(ctx, singlePoolTm)

			type listOutcome struct {
				response *privatev1.BillingAccountsListResponse
				err      error
			}
			server := build(billing.NewProvider("m360", adapter, nil))
			result := make(chan listOutcome, 1)
			go func() {
				response, listErr := server.List(singlePoolCtx, &privatev1.BillingAccountsListRequest{})
				result <- listOutcome{response: response, err: listErr}
			}()
			Eventually(adapter.enteredSecondPage, 5*time.Second).
				Should(BeClosed(), "the walk must reach the provider's second page")

			// The pool's only connection must be free while the provider is being read:
			unrelated := make(chan error, 1)
			go func() {
				_, execErr := singlePool.Exec(context.Background(), `select 1`)
				unrelated <- execErr
			}()
			Eventually(unrelated, 5*time.Second).Should(Receive(Succeed()),
				"the walk must not pin the pool's only connection while the provider is waiting")

			close(adapter.release)
			var outcome listOutcome
			Eventually(result, 5*time.Second).Should(Receive(&outcome))
			Expect(outcome.err).ToNot(HaveOccurred())
			Expect(outcome.response.GetTotal()).To(Equal(int32(1)))
		})

	It("fails with FAILED_PRECONDITION/MALFORMED when the provider never reports a last page", func() {
		server := build(billing.NewProvider("m360", &endlessDiscoveryAdapter{}, nil))
		_, err := list(server, nil)
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.FailedPrecondition))
		Expect(st.Details()[0].(*errdetails.ErrorInfo).GetReason()).To(Equal("MALFORMED"))
	})

	It("fails as UNAVAILABLE when a hanging provider exceeds the discovery deadline", func() {
		server := build(billing.NewProvider("m360", &hangingDiscoveryAdapter{}, nil))
		server.discoverDeadline = 50 * time.Millisecond

		result := make(chan error, 1)
		go func() {
			_, err := list(server, nil)
			result <- err
		}()
		var err error
		Eventually(result, 5*time.Second).Should(Receive(&err))
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unavailable))
		Expect(st.Details()[0].(*errdetails.ErrorInfo).GetReason()).To(Equal(billingProviderUnavailableReason))
	})

	It("serves the stored page without re-reading the provider within the freshness window", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: "acct-1", State: billing.AccountActive}, {ID: "acct-2", State: billing.AccountActive},
		}})
		server := build(fake.Provider())
		first, err := list(server, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(first.GetTotal()).To(Equal(int32(2)))
		Expect(fake.Count("ListAccounts")).To(Equal(1))

		second, err := list(server, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(second.GetTotal()).To(Equal(int32(2)), "the stored page must still be served")
		Expect(fake.Count("ListAccounts")).To(Equal(1), "a List inside the window must not re-read the provider")
	})

	It("fails fast inside the failure backoff and retries the walk once it has elapsed", func() {
		fake := billingfake.New(billingfake.Scenario{
			Accounts: []billingfake.AccountSpec{{ID: "acct-1", State: billing.AccountActive}},
			Failures: map[string]billingfake.Failure{"ListAccounts": {Reason: billing.ReasonUnavailable, Times: 1}},
		})
		server := build(fake.Provider())
		server.discoverFailureBackoff = 10 * time.Millisecond
		_, err := list(server, nil)
		Expect(err).To(HaveOccurred())
		Expect(fake.Count("ListAccounts")).To(Equal(1))

		// A List inside the backoff window fails fast as UNAVAILABLE rather than stacking another walk:
		_, err = list(server, nil)
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unavailable), "never an unmarked stale list")
		Expect(fake.Count("ListAccounts")).To(Equal(1), "the backoff window must not re-read the provider")

		// Once the backoff has elapsed, the next List retries the walk and only a success opens the window:
		var response *privatev1.BillingAccountsListResponse
		Eventually(func() error {
			var listErr error
			response, listErr = list(server, nil)
			return listErr
		}, 5*time.Second).Should(Succeed())
		Expect(response.GetTotal()).To(Equal(int32(1)))
		Expect(fake.Count("ListAccounts")).To(Equal(2))
	})

	It("runs at most one discovery walk at a time, failing a concurrent stale List fast", func() {
		adapter := &gatedDiscoveryAdapter{entered: make(chan struct{}), release: make(chan struct{})}
		server := build(billing.NewProvider("m360", adapter, nil))

		first := make(chan error, 1)
		go func() {
			_, err := list(server, nil)
			first <- err
		}()
		Eventually(adapter.entered, 5*time.Second).Should(BeClosed(), "the first List must start the walk")

		// A second stale List while the walk is in flight fails fast as UNAVAILABLE, without starting a
		// second walk and without stacking behind the first:
		_, err := list(server, nil)
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unavailable))
		Expect(adapter.calls).To(Equal(1), "the concurrent List must not start a second walk")

		close(adapter.release)
		var firstErr error
		Eventually(first, 5*time.Second).Should(Receive(&firstErr))
		Expect(firstErr).ToNot(HaveOccurred())

		// The completed walk opened the freshness window: the next List serves the stored page.
		response, err := list(server, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetTotal()).To(Equal(int32(1)))
		Expect(adapter.calls).To(Equal(1), "the freshness window must serve without starting a new walk")
	})

	It("re-discovers once the freshness window has passed", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: "acct-1", State: billing.AccountActive},
		}})
		server := build(fake.Provider())
		_, err := list(server, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(fake.Count("ListAccounts")).To(Equal(1))

		server.discoverMu.Lock()
		server.lastDiscovery = time.Now().Add(-time.Hour)
		server.discoverMu.Unlock()

		_, err = list(server, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(fake.Count("ListAccounts")).To(Equal(2), "a List after the window must re-read the provider")
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
