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

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
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

// gatedDiscoveryAdapter blocks ListAccounts until released, reproducing a walk in flight while another List
// arrives. entered closes once the walk reaches the provider, so the suite observes the start without sleeping.
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

// gatedTwoPageAdapter serves the first page immediately and holds the second until released, reproducing a
// multi-page walk waiting on the provider while the suite checks that no database connection is held.
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

// canceledCallerAdapter blocks its first ListAccounts call until that walk's context is done, reproducing a caller
// that cancels its own walk, and serves one active-account page on later calls. entered and calls let the suite
// observe both without sleeping.
type canceledCallerAdapter struct {
	billing.Adapter
	entered chan struct{}
	once    sync.Once
	calls   int
}

// ListAccounts waits for the walk's context to end on the first call and serves one active account on every
// later call.
func (a *canceledCallerAdapter) ListAccounts(ctx context.Context, _ billing.AccountFilter, _ billing.Page) (
	billing.AccountPage, error) {
	a.calls++
	if a.calls == 1 {
		a.once.Do(func() { close(a.entered) })
		<-ctx.Done()
		return billing.AccountPage{}, billing.NewError(billing.ReasonTimeout, "ListAccounts", true, 0)
	}
	return billing.AccountPage{
		Accounts: []billing.Account{{Ref: billing.NewAccountRef("acct-1"), State: billing.AccountActive}},
	}, nil
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
	// storedCount reads the billing_accounts row count on the suite pool's own connection, so it observes only
	// durably committed rows.
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

	It("maps provider_key, display_name, state, external_billing_account_id and status_observed_at", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: "acct-1", Name: "Acme", ExternalID: "ext-1", State: billing.AccountActive},
		}})
		response, err := list(build(fake.Provider()), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(1))
		item := response.GetItems()[0]
		Expect(item.GetStatus().GetProviderKey()).To(Equal("fake"))
		Expect(item.GetStatus().GetDisplayName()).To(Equal("Acme"))
		Expect(item.GetStatus().GetState()).To(Equal(privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE))
		Expect(item.GetStatus().GetExternalBillingAccountId()).To(Equal("ext-1"))
		Expect(item.GetStatus().HasStateObservationTimestamp()).To(BeTrue())
		Expect(item.GetMetadata().GetTenant()).To(Equal("system"))
		Expect(item.GetMetadata().GetProject()).To(BeEmpty())
		Expect(item.GetMetadata().GetCreator()).To(Equal("system"))
		Expect(item.GetMetadata().GetCreationTimestamp()).ToNot(BeNil())
		Expect(item.GetMetadata().GetVersion()).To(BeZero())
		name, err := billingAccountName(item.GetId())
		Expect(err).ToNot(HaveOccurred())
		Expect(item.GetMetadata().GetName()).To(Equal(name))
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
		Expect(response.GetItems()[0].GetStatus().GetDisplayName()).To(Equal("Acme]52;x;wg production"))
	})

	It("leaves base_currency empty: the fake, like M360, supplies none by default", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{{ID: "acct-1"}}})
		response, err := list(build(fake.Provider()), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()[0].GetStatus().GetBaseCurrency()).To(BeEmpty())
	})

	It("stores an initial lowercase currency in uppercase and refreshes later case variants", func() {
		accounts := []billingfake.AccountSpec{{ID: "acct-1", BaseCurrency: "usd", State: billing.AccountActive}}
		var id string
		for _, currency := range []string{"usd", "USD", "Usd"} {
			accounts[0].BaseCurrency = currency
			response, err := list(build(billingfake.New(billingfake.Scenario{Accounts: accounts}).Provider()), nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(1))
			item := response.GetItems()[0]
			Expect(item.GetStatus().GetBaseCurrency()).To(Equal("USD"))
			if id == "" {
				id = item.GetId()
			}
			Expect(item.GetId()).To(Equal(id))
			var stored string
			Expect(testPool.QueryRow(ctx, `select base_currency from billing_accounts where id = $1`, id).
				Scan(&stored)).To(Succeed())
			Expect(stored).To(Equal("USD"))
		}
	})

	It("maps an unknown or inactive state and never maps a raw provider value to ACTIVE", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: "a1", State: billing.AccountUnknown}, {ID: "a2", State: billing.AccountInactive},
		}})
		response, err := list(build(fake.Provider()), &privatev1.BillingAccountsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		states := map[privatev1.BillingAccountState]bool{}
		for _, item := range response.GetItems() {
			states[item.GetStatus().GetState()] = true
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
			if item.GetStatus().GetState() == privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_UNKNOWN {
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

		// The provider's list becomes empty: the sweep must still run and mark the stored row UNKNOWN. This is the
		// nil-seen regression, since pgx encodes a nil slice as SQL NULL, which the not-in predicate never matches.
		empty := billingfake.New(billingfake.Scenario{})
		server2 := build(empty.Provider())
		response, err := list(server2, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetTotal()).To(Equal(int32(1)), "the vanished account's row must be retained, not deleted")
		Expect(response.GetItems()[0].GetStatus().GetState()).
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
		// service_account_create_test.go: the first upsert succeeds inside the store transaction, so only a real
		// rollback keeps it out of the table.
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
			// Release the walk whatever happens below: a failed assertion while the provider is still gated
			// would leave the walk blocked, and the pool cleanup below waits for the connection it holds.
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(adapter.release) }) }

			// A pool with a single connection, and the server's transaction manager over that pool: if the
			// discovery walk held a connection (or an open transaction) while waiting on the provider,
			// nothing else could use the pool.
			config, err := pgxpool.ParseConfig(testPool.Config().ConnString())
			Expect(err).ToNot(HaveOccurred())
			config.MaxConns = 1
			singlePool, err := pgxpool.NewWithConfig(ctx, config)
			Expect(err).ToNot(HaveOccurred())
			// Cleanups run last-in-first-out, so this one must release the walk itself before closing the
			// pool: closing the pool waits for the walk's checked-out connection to come back.
			DeferCleanup(func() {
				release()
				singlePool.Close()
			})
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
			probeCtx, probeCancel := context.WithTimeout(ctx, 5*time.Second)
			defer probeCancel()
			_, err = singlePool.Exec(probeCtx, `select 1`)
			Expect(err).ToNot(HaveOccurred(),
				"the walk must not pin the pool's only connection while the provider is waiting")

			release()
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

	It("does not open the failure backoff when the caller cancels the walk", func() {
		adapter := &canceledCallerAdapter{entered: make(chan struct{})}
		server := build(billing.NewProvider("m360", adapter, nil))

		callerCtx, cancel := context.WithCancel(ctx)
		DeferCleanup(cancel)
		first := make(chan error, 1)
		go func() {
			_, err := server.List(callerCtx, &privatev1.BillingAccountsListRequest{})
			first <- err
		}()
		Eventually(adapter.entered, 5*time.Second).Should(BeClosed(),
			"the first List must start the walk and reach the provider")

		// The caller gives up on its own walk: that says nothing about the provider, so it must not open the
		// server-wide failure backoff for the Lists of every other caller.
		cancel()
		var firstErr error
		Eventually(first, 5*time.Second).Should(Receive(&firstErr))
		Expect(firstErr).To(HaveOccurred())

		response, err := server.List(ctx, &privatev1.BillingAccountsListRequest{})
		Expect(err).ToNot(HaveOccurred(), "a live List must walk the provider again, not fail fast")
		Expect(response.GetTotal()).To(Equal(int32(1)))
		Expect(adapter.calls).To(Equal(2), "the canceled walk must not have opened the failure backoff")
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

	It("stores canonical JSON and preserves identity, metadata and version for stale or identical observations", func() {
		server := build(billingfake.New(billingfake.Scenario{}).Provider())
		start := time.Now().UTC().Truncate(time.Microsecond)
		accounts := []billing.Account{{Ref: billing.NewAccountRef("native"), Name: "Known", BaseCurrency: "usd",
			ExternalID: " raw token ", State: billing.AccountActive}}
		Expect(server.storeAccounts(ctx, accounts, start)).To(Succeed())
		var id, name, data string
		var version int32
		Expect(testPool.QueryRow(ctx, `select id, name, data::text, version from billing_accounts`).
			Scan(&id, &name, &data, &version)).To(Succeed())
		Expect(data).To(ContainSubstring(`"BILLING_ACCOUNT_STATE_ACTIVE"`))
		Expect(data).To(ContainSubstring(`"state_observation_timestamp"`))
		Expect(data).ToNot(ContainSubstring(`"metadata"`))
		Expect(server.storeAccounts(ctx, accounts, start)).To(Succeed())
		accounts[0].Name, accounts[0].ExternalID, accounts[0].State = "stale", "replacement", billing.AccountInactive
		Expect(server.storeAccounts(ctx, accounts, start.Add(-time.Second))).To(Succeed())
		var gotID, gotName, token, label, state string
		var gotVersion int32
		Expect(testPool.QueryRow(ctx, `select id, name, external_billing_account_id, display_name, state, version
			from billing_accounts`).Scan(&gotID, &gotName, &token, &label, &state, &gotVersion)).To(Succeed())
		Expect(gotID).To(Equal(id))
		Expect(gotName).To(Equal(name))
		Expect(gotVersion).To(Equal(version))
		Expect(token).To(Equal(" raw token "))
		Expect(label).To(Equal("Known"))
		Expect(state).To(Equal("ACTIVE"))
	})

	It("does not enqueue account creates, observation updates or sweeps while retaining nonbilling enqueue", func() {
		var before, after int
		Expect(testPool.QueryRow(ctx, `select count(*) from changes`).Scan(&before)).To(Succeed())
		server := build(billingfake.New(billingfake.Scenario{}).Provider())
		start := time.Now().UTC().Truncate(time.Microsecond)
		accounts := []billing.Account{{Ref: billing.NewAccountRef("a"), State: billing.AccountActive}}
		Expect(server.storeAccounts(ctx, accounts, start)).To(Succeed())
		accounts[0].ExternalID, accounts[0].Name = "new-raw-token", "Refreshed"
		Expect(server.storeAccounts(ctx, accounts, start.Add(time.Second))).To(Succeed())
		Expect(server.storeAccounts(ctx, nil, start.Add(2*time.Second))).To(Succeed())
		Expect(testPool.QueryRow(ctx, `select count(*) from changes`).Scan(&after)).To(Succeed())
		Expect(after).To(Equal(before), "no publisher runs in this PostgreSQL fixture to hide billing changes")
		control, err := dao.NewGenericDAO[*privatev1.ServiceAccount]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = database.WithNewTx(ctx, func(ctx context.Context) (struct{}, error) {
			_, err := control.Create().SetObject(privatev1.ServiceAccount_builder{Id: "enqueue-control",
				Metadata: privatev1.Metadata_builder{Name: "enqueue-control", Tenant: "system"}.Build(),
			}.Build()).Do(ctx)
			return struct{}{}, err
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(testPool.QueryRow(ctx, `select count(*) from changes`).Scan(&after)).To(Succeed())
		Expect(after).To(Equal(before + 1))
	})

	It("recovers token insert and adoption collisions using savepoints without losing valid observations", func() {
		server := build(billingfake.New(billingfake.Scenario{}).Provider())
		start := time.Now().UTC().Truncate(time.Microsecond)
		_, err := testPool.Exec(ctx, `create sequence token_insert_attempts;
		create sequence token_candidate_identity minvalue 0;
		create function check_token_retry_identity() returns trigger language plpgsql as $$
		declare attempt bigint; identity_hash bigint;
		begin
			if new.data->'status'->>'provider_account_reference' = 'b' then
				attempt := nextval('token_insert_attempts');
				identity_hash := hashtextextended(new.id || '/' || new.name, 0) & 9223372036854775807;
				if attempt = 1 then
					perform setval('token_candidate_identity', identity_hash);
				elsif (select last_value from token_candidate_identity) <> identity_hash then
					raise exception 'retry changed id or name';
				end if;
			end if;
			return new;
		end $$;
		create trigger check_token_retry_identity before insert on billing_accounts
		for each row execute function check_token_retry_identity()`)
		Expect(err).ToNot(HaveOccurred())
		accounts := []billing.Account{
			{Ref: billing.NewAccountRef("a"), ExternalID: "same", State: billing.AccountActive},
			{Ref: billing.NewAccountRef("b"), ExternalID: "same", State: billing.AccountActive},
		}
		Expect(server.storeAccounts(ctx, accounts, start)).To(Succeed())
		var attempts int
		Expect(testPool.QueryRow(ctx, `select last_value from token_insert_attempts`).Scan(&attempts)).To(Succeed())
		Expect(attempts).To(Equal(2), "one token collision permits exactly one same-id/name tokenless insert retry")
		var id, name string
		Expect(testPool.QueryRow(ctx, `select id, name from billing_accounts where provider_account_reference = 'b'`).
			Scan(&id, &name)).To(Succeed())
		accounts[1].Name, accounts[1].State = "Observed", billing.AccountInactive
		Expect(server.storeAccounts(ctx, accounts, start.Add(time.Second))).To(Succeed())
		var gotID, gotName, label, state string
		var token *string
		Expect(testPool.QueryRow(ctx, `select id, name, display_name, state, external_billing_account_id
			from billing_accounts where provider_account_reference = 'b'`).
			Scan(&gotID, &gotName, &label, &state, &token)).To(Succeed())
		Expect(gotID).To(Equal(id))
		Expect(gotName).To(Equal(name))
		Expect(token).To(BeNil())
		Expect(label).To(Equal("Observed"))
		Expect(state).To(Equal("INACTIVE"))
		Expect(storedCount()).To(Equal(2))
	})

	It("aborts the whole batch on an unexpected native-key conflict without a late-winner retry", func() {
		server := build(billingfake.New(billingfake.Scenario{}).Provider())
		start := time.Now().UTC().Truncate(time.Microsecond)
		Expect(server.storeAccounts(ctx, []billing.Account{{Ref: billing.NewAccountRef("a"), State: billing.AccountActive}}, start)).To(Succeed())
		// Deliberately violate the guard protocol at the SQL boundary. This is a real named native-key index
		// conflict, not an error-string fake. The previously merged unseen observation must roll back too.
		_, err := testPool.Exec(ctx, `create sequence native_insert_attempts;
		create function inject_native_conflict() returns trigger language plpgsql as $$
		begin
			perform nextval('native_insert_attempts');
			new.data := jsonb_set(new.data, '{status,provider_account_reference}', '"a"'::jsonb);
			return new;
		end $$;
		create trigger a_inject_native_conflict before insert on billing_accounts
		for each row execute function inject_native_conflict()`)
		Expect(err).ToNot(HaveOccurred())
		err = server.storeAccounts(ctx, []billing.Account{{Ref: billing.NewAccountRef("b"), State: billing.AccountActive}}, start.Add(time.Second))
		Expect(status.Code(err)).To(Equal(codes.Aborted))
		var state string
		var version, attempts int
		Expect(testPool.QueryRow(ctx, `select state, version from billing_accounts`).Scan(&state, &version)).To(Succeed())
		Expect(state).To(Equal("ACTIVE"))
		Expect(version).To(BeZero())
		Expect(testPool.QueryRow(ctx, `select last_value from native_insert_attempts`).Scan(&attempts)).To(Succeed())
		Expect(attempts).To(Equal(1))
		Expect(storedCount()).To(Equal(1))
	})

	It("rejects duplicate native keys before storing any batch", func() {
		server := build(billingfake.New(billingfake.Scenario{}).Provider())
		err := server.storeAccounts(ctx, []billing.Account{
			{Ref: billing.NewAccountRef("same")}, {Ref: billing.NewAccountRef("same")},
		}, time.Now())
		var classified *billing.Error
		Expect(errors.As(err, &classified)).To(BeTrue())
		Expect(classified.Reason).To(Equal(billing.ReasonMalformed))
		Expect(storedCount()).To(BeZero())
	})

	It("round trips NULL, present-empty, whitespace and raw tokens through GenericDAO and an unseen sweep", func() {
		server := build(billingfake.New(billingfake.Scenario{}).Provider())
		start := time.Now().UTC().Truncate(time.Microsecond)
		tokens := []*string{nil, proto.String(""), proto.String(" \t "), proto.String(" padded ")}
		for i, token := range tokens {
			id := fmt.Sprintf("raw-%d", i)
			name, err := billingAccountName(id)
			Expect(err).ToNot(HaveOccurred())
			_, err = database.WithNewTx(ctx, func(ctx context.Context) (struct{}, error) {
				_, err := server.dao.Create().SetObject(privatev1.BillingAccount_builder{
					Id: id, Metadata: privatev1.Metadata_builder{Name: name, Tenant: "system", Creator: "system",
						Labels: map[string]string{"preserved": "label"}, Annotations: map[string]string{"note": "retained"},
					}.Build(), Status: privatev1.BillingAccountStatus_builder{
						ProviderKey: "fake", ProviderAccountReference: id, ExternalBillingAccountId: token,
						State:                     privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE,
						StateObservationTimestamp: timestamppb.New(start),
					}.Build(),
				}.Build()).Do(ctx)
				return struct{}{}, err
			})
			Expect(err).ToNot(HaveOccurred())
		}
		Expect(server.storeAccounts(ctx, nil, start.Add(time.Second))).To(Succeed())
		// Mark discovery fresh to inspect the exact completed sweep without another provider observation.
		server.lastDiscovery = time.Now()
		response, err := list(server, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(4))
		for i, item := range response.GetItems() {
			Expect(item.GetId()).To(Equal(fmt.Sprintf("raw-%d", i)))
			Expect(item.GetStatus().HasExternalBillingAccountId()).To(Equal(tokens[i] != nil))
			if tokens[i] != nil {
				Expect(item.GetStatus().GetExternalBillingAccountId()).To(Equal(*tokens[i]))
			}
			Expect(item.GetStatus().GetState()).To(Equal(privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_UNKNOWN))
			Expect(item.GetMetadata().GetLabels()).To(HaveKeyWithValue("preserved", "label"))
			Expect(item.GetMetadata().GetAnnotations()).To(HaveKeyWithValue("note", "retained"))
			Expect(item.GetMetadata().GetVersion()).To(Equal(int32(1)))
		}
	})

	It("fully locks the SQL-id-ordered provider union before its first mutation", func() {
		server := build(billingfake.New(billingfake.Scenario{}).Provider())
		start := time.Now().UTC().Truncate(time.Microsecond)
		accounts := []billing.Account{{Ref: billing.NewAccountRef("a"), State: billing.AccountActive},
			{Ref: billing.NewAccountRef("b"), State: billing.AccountActive}}
		Expect(server.storeAccounts(ctx, accounts, start)).To(Succeed())
		_, err := testPool.Exec(ctx, `create sequence discovery_mutations;
		create function count_discovery_mutations() returns trigger language plpgsql as $$
		begin perform nextval('discovery_mutations'); return new; end $$;
		create trigger count_discovery_mutations before update on billing_accounts
		for each row execute function count_discovery_mutations()`)
		Expect(err).ToNot(HaveOccurred())
		gate, err := tm.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		ended := false
		DeferCleanup(func() {
			if !ended {
				Expect(gate.End(ctx)).To(Succeed())
			}
		})
		var id string
		Expect(gate.QueryRow(ctx, `select id from billing_accounts order by id desc limit 1 for update`).Scan(&id)).To(Succeed())
		var pid int
		Expect(gate.QueryRow(ctx, `select pg_backend_pid()`).Scan(&pid)).To(Succeed())
		waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		DeferCleanup(cancel)
		result := make(chan error, 1)
		go func() { result <- server.storeAccounts(waitCtx, nil, start.Add(time.Second)) }()
		Eventually(func() int {
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from pg_stat_activity
				where datname = current_database() and $1 = any(pg_blocking_pids(pid))`, pid).Scan(&count)).To(Succeed())
			return count
		}, 5*time.Second, 10*time.Millisecond).Should(Equal(1))
		var mutated bool
		Expect(testPool.QueryRow(ctx, `select is_called from discovery_mutations`).Scan(&mutated)).To(Succeed())
		Expect(mutated).To(BeFalse(), "even a lower-id row cannot be updated before the full union is locked")
		Expect(gate.End(ctx)).To(Succeed())
		ended = true
		Eventually(result, 5*time.Second).Should(Receive(Succeed()))
		var mutations int
		Expect(testPool.QueryRow(ctx, `select last_value from discovery_mutations`).Scan(&mutations)).To(Succeed())
		Expect(mutations).To(Equal(2))
	})

	It("preserves billing default-100 and maximum-1000 limits with trusted count and offset", func() {
		server := build(billingfake.New(billingfake.Scenario{}).Provider())
		accounts := make([]billing.Account, 1000)
		for i := range accounts {
			accounts[i] = billing.Account{Ref: billing.NewAccountRef(fmt.Sprintf("bounded-%04d", i)), State: billing.AccountActive}
		}
		Expect(server.storeAccounts(ctx, accounts, time.Now().UTC())).To(Succeed())
		server.lastDiscovery = time.Now()
		request := privatev1.BillingAccountsListRequest_builder{Limit: proto.Int32(0)}.Build()
		response, err := list(server, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetSize()).To(Equal(int32(100)))
		Expect(response.GetTotal()).To(Equal(int32(1000)))
		request.SetLimit(2000)
		response, err = list(server, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetSize()).To(Equal(int32(1000)))
		request.SetOffset(990)
		response, err = list(server, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetSize()).To(Equal(int32(10)))
		Expect(response.GetTotal()).To(Equal(int32(1000)))
	})

	DescribeTable("serializes independent batches before row locks and refreshes the complete provider union after waiting", func(waitingRef, expectedState string, expectedCount int, waitingAge time.Duration) {
		first := build(billingfake.New(billingfake.Scenario{}).Provider())
		second := build(billingfake.New(billingfake.Scenario{}).Provider())
		start := time.Now().UTC().Truncate(time.Microsecond)
		gate, err := tm.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		ended := false
		DeferCleanup(func() {
			if !ended {
				Expect(gate.End(ctx)).To(Succeed())
			}
		})
		_, err = gate.Exec(ctx, `select pg_advisory_xact_lock(216004::bigint)`)
		Expect(err).ToNot(HaveOccurred())
		_, err = testPool.Exec(ctx, `create function gate_first_discovery() returns trigger language plpgsql as $$
		begin
			if new.data->'status'->>'provider_account_reference' = 'a' then
				perform pg_advisory_xact_lock(216004::bigint);
			end if;
			return new;
		end $$;
		create trigger gate_first_discovery before insert on billing_accounts
		for each row execute function gate_first_discovery()`)
		Expect(err).ToNot(HaveOccurred())
		waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		DeferCleanup(cancel)
		firstResult, result := make(chan error, 1), make(chan error, 1)
		go func() {
			firstResult <- first.storeAccounts(waitCtx, []billing.Account{{Ref: billing.NewAccountRef("a"),
				State: billing.AccountActive}}, start)
		}()
		Eventually(func() int {
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from pg_locks where locktype = 'advisory'
				and not granted and objid = 216004 and database = (select oid from pg_database where datname = current_database())`).
				Scan(&count)).To(Succeed())
			return count
		}, 5*time.Second, 10*time.Millisecond).Should(Equal(1))
		go func() {
			result <- second.storeAccounts(waitCtx, []billing.Account{{Ref: billing.NewAccountRef(waitingRef),
				State: billing.AccountActive}}, start.Add(waitingAge))
		}()
		Eventually(func() int {
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from pg_locks
				where locktype = 'advisory' and not granted and objid <> 216004
				and database = (select oid from pg_database where datname = current_database())`).
				Scan(&count)).To(Succeed())
			return count
		}, 5*time.Second, 10*time.Millisecond).Should(Equal(1))
		var rowLocks int
		Expect(testPool.QueryRow(ctx, `select count(*) from pg_locks where locktype in ('tuple','relation')
			and relation in ('tenants'::regclass, 'billing_accounts'::regclass)
			and pid in (select pid from pg_locks where locktype = 'advisory' and not granted and objid <> 216004)`).Scan(&rowLocks)).To(Succeed())
		Expect(rowLocks).To(BeZero(), "provider guard must precede tenant/account access")
		Expect(gate.End(ctx)).To(Succeed())
		ended = true
		Eventually(firstResult, 5*time.Second).Should(Receive(Succeed()))
		Eventually(result, 5*time.Second).Should(Receive(Succeed()))
		var state string
		Expect(testPool.QueryRow(ctx, `select state from billing_accounts where provider_account_reference = 'a'`).Scan(&state)).To(Succeed())
		Expect(state).To(Equal(expectedState))
		Expect(storedCount()).To(Equal(expectedCount))
	},
		Entry("new unseen member joins the union", "b", "UNKNOWN", 2, time.Second),
		Entry("older walk cannot demote newer union member", "b", "ACTIVE", 2, -time.Second),
		Entry("same absent native key retains one identity", "a", "ACTIVE", 1, time.Second),
	)
})

var _ = Describe("Billing account observation merge", func() {
	DescribeTable("classifies local failures without provider fallback or private details",
		func(err error, code codes.Code) {
			mapped := mapBillingDiscoveryError(err)
			Expect(status.Code(mapped)).To(Equal(code))
			Expect(mapped.Error()).ToNot(ContainSubstring("private-detail"))
		},
		Entry("deadlock", &pgconn.PgError{Code: "40P01", Message: "private-detail"}, codes.Aborted),
		Entry("serialization", &pgconn.PgError{Code: "40001", Message: "private-detail"}, codes.Aborted),
		Entry("unplanned native conflict", status.Error(codes.Aborted, "private-detail"), codes.Aborted),
		Entry("caller cancellation", context.Canceled, codes.Canceled),
		Entry("local deadline", context.DeadlineExceeded, codes.DeadlineExceeded),
		Entry("unexpected SQL", errors.New("private-detail"), codes.Internal),
	)

	It("hashes the UTF-8 OSAC id and rejects an empty id", func() {
		name, err := billingAccountName("abc")
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("ba-ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f200"))
		_, err = billingAccountName("")
		Expect(err).To(HaveOccurred())
	})

	It("preserves raw optional token presence and usable fields, accepting equal-start state observations", func() {
		at := timestamppb.New(time.Now().UTC())
		for _, token := range []*string{nil, proto.String(""), proto.String(" \t "), proto.String(" raw ")} {
			current := privatev1.BillingAccount_builder{Id: "osac", Metadata: privatev1.Metadata_builder{
				Name: "stable", Creator: "", Version: 7,
			}.Build(), Status: privatev1.BillingAccountStatus_builder{
				ProviderKey: "p", ProviderAccountReference: "native", DisplayName: "Known", BaseCurrency: "USD",
				State:                     privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE,
				StateObservationTimestamp: at, ExternalBillingAccountId: token,
			}.Build()}.Build()
			before := proto.Clone(current).(*privatev1.BillingAccount)
			observed := privatev1.BillingAccountStatus_builder{
				State:                     privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_INACTIVE,
				StateObservationTimestamp: at, BaseCurrency: "invalid",
			}.Build()
			merged, changed := mergeBillingAccountObservation(current, observed)
			Expect(changed).To(BeTrue())
			Expect(proto.Equal(current, before)).To(BeTrue(), "merge must not mutate its caller's snapshot")
			Expect(proto.Equal(merged.GetMetadata(), current.GetMetadata())).To(BeTrue())
			Expect(merged.GetStatus().GetProviderAccountReference()).To(Equal("native"))
			Expect(merged.GetStatus().GetDisplayName()).To(Equal("Known"))
			Expect(merged.GetStatus().GetBaseCurrency()).To(Equal("USD"))
			Expect(merged.GetStatus().HasExternalBillingAccountId()).To(Equal(token != nil))
			Expect(merged.GetStatus().GetExternalBillingAccountId()).To(Equal(current.GetStatus().GetExternalBillingAccountId()))
			_, changed = mergeBillingAccountObservation(merged, observed)
			Expect(changed).To(BeFalse())
		}
	})
})
