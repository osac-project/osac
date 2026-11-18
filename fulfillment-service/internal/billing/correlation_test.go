/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package billing_test

import (
	"context"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
)

func noDuplicate(context.Context, string, string) (bool, error) { return false, nil }

// poisonWriteAdapter exposes legacy-shaped methods outside the read-only Adapter contract. Neither may be called.
type poisonWriteAdapter struct {
	*billingfake.Fake
	capabilityQueries int
	writes            int
}

func (a *poisonWriteAdapter) SupportsCorrelationWrite() bool {
	a.capabilityQueries++
	return true
}

func (a *poisonWriteAdapter) SetCorrelationValue(context.Context, billing.AccountRef, string) error {
	a.writes++
	return errors.New("unexpected provider mutation")
}

// carrierPagesAdapter scripts only the provider's list observations; account reads use the native fake.
type carrierPagesAdapter struct {
	*billingfake.Fake
	pages   []billing.AccountPage
	filters []billing.AccountFilter
	tokens  []string
	errAt   int
	err     error
	cancel  context.CancelFunc
}

func (a *carrierPagesAdapter) ListAccounts(ctx context.Context, filter billing.AccountFilter, page billing.Page) (
	billing.AccountPage, error) {
	a.filters = append(a.filters, filter)
	a.tokens = append(a.tokens, page.Token)
	if len(a.filters) == a.errAt {
		if a.cancel != nil {
			a.cancel()
			return billing.AccountPage{}, ctx.Err()
		}
		return billing.AccountPage{}, a.err
	}
	return a.pages[min(len(a.filters)-1, len(a.pages)-1)], nil
}

var _ = Describe("ResolveAccountCorrelationID", func() {
	ctx := context.Background()
	const acctID = "ba-1"
	const providerAcctID = "acct-1"
	ref := billing.NewAccountRef(providerAcctID)
	newFake := func(externalID string) *billingfake.Fake {
		return billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: providerAcctID, Name: "Acme", ExternalID: externalID, State: billing.AccountActive},
		}})
	}
	input := func(adapter billing.Adapter, localValue string) billing.AccountCorrelationInput {
		return billing.AccountCorrelationInput{
			Adapter: adapter, AccountID: acctID, Ref: ref, LocalValue: localValue, LocalDuplicate: noDuplicate,
		}
	}
	expectReason := func(result billing.Resolution, err error, reason string) {
		GinkgoHelper()
		Expect(result).To(Equal(billing.Resolution{}))
		var resolutionErr *billing.CorrelationResolutionError
		Expect(errors.As(err, &resolutionErr)).To(BeTrue(), "error was %v", err)
		Expect(resolutionErr.Reason).To(Equal(reason))
	}
	carrier := func(id, value string) billing.Account {
		return billing.Account{Ref: billing.NewAccountRef(id), ExternalID: value}
	}

	DescribeTable("the precedence table",
		func(providerExternalID, localValue, expectValue string, expectMatches int) {
			fake := &poisonWriteAdapter{Fake: newFake(providerExternalID)}
			var adapter billing.Adapter = fake
			res, err := billing.ResolveAccountCorrelationID(ctx, input(adapter, localValue))
			Expect(fake.capabilityQueries).To(BeZero(), "resolution must not query a write capability")
			Expect(fake.writes).To(BeZero(), "resolution must never invoke a provider setter")
			Expect(err).ToNot(HaveOccurred())
			if expectValue == "" {
				Expect(res.Value).ToNot(BeEmpty())
				Expect(res.Value).ToNot(Equal(acctID))
				Expect(res.Value).ToNot(Equal(providerAcctID))
			} else {
				Expect(res.Value).To(Equal(expectValue))
			}
			Expect(res.ProviderMatches).To(Equal(expectMatches))
			Expect(fake.Count("GetAccount")).To(Equal(1))
			Expect(fake.Count("CorrelationValue")).To(BeZero())
			Expect(fake.Count("ListAccounts")).To(Equal(1))
			value, _, err := fake.CorrelationValue(ctx, ref)
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal(providerExternalID), "provider bytes must remain unchanged")
		},
		Entry("both absent: generates locally and remains pending", "", "", "", 0),
		Entry("provider-only: adopts the exact provider value", "ext-1", "", "ext-1", 1),
		Entry("local-only: retains the local value and remains pending", "", "osac-1", "osac-1", 0),
		Entry("both equal: retains the established value", "same-1", "same-1", "same-1", 1),
	)

	DescribeTable("preserves raw nonblank bytes and treats whitespace only as absence",
		func(providerValue, localValue, want string, matches int) {
			res, err := billing.ResolveAccountCorrelationID(ctx, input(newFake(providerValue), localValue))
			Expect(err).ToNot(HaveOccurred())
			Expect(res).To(Equal(billing.Resolution{Value: want, ProviderMatches: matches}))
		},
		Entry("adopts padded provider bytes", "  padded\t", "", "  padded\t", 1),
		Entry("retains padded local bytes", "", "  padded\t", "  padded\t", 0),
		Entry("equal padded established bytes", "  padded\t", "  padded\t", "  padded\t", 1),
		Entry("blank provider is absent", " \t\n", "local", "local", 0),
		Entry("blank local is absent", "provider", " \t\n", "provider", 1),
	)

	It("generates fresh values when both sides are blank", func() {
		first, err := billing.ResolveAccountCorrelationID(ctx, input(newFake(" \t"), "\n"))
		Expect(err).ToNot(HaveOccurred())
		second, err := billing.ResolveAccountCorrelationID(ctx, input(newFake(""), ""))
		Expect(err).ToNot(HaveOccurred())
		Expect(first.Value).ToNot(BeEmpty())
		Expect(second.Value).ToNot(Equal(first.Value))
	})

	DescribeTable("rejects unequal established values without scanning or changing either side",
		func(providerValue, localValue string) {
			fake := newFake(providerValue)
			res, err := billing.ResolveAccountCorrelationID(ctx, input(fake, localValue))
			expectReason(res, err, billing.ReasonBillingCorrelationIDConflict)
			Expect(fake.Count("GetAccount")).To(Equal(1))
			Expect(fake.Count("ListAccounts")).To(BeZero())
			value, _, err := fake.CorrelationValue(ctx, ref)
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal(providerValue))
		},
		Entry("different values", "provider-value", "local-value"),
		Entry("padding is not normalized", " value ", "value"),
	)

	DescribeTable("checks local uniqueness for adoption and retention",
		func(providerValue, localValue string) {
			fake := newFake(providerValue)
			account := input(fake, localValue)
			account.LocalDuplicate = func(_ context.Context, value, exclude string) (bool, error) {
				Expect(value).To(Equal(" value "))
				Expect(exclude).To(Equal(acctID))
				return true, nil
			}
			res, err := billing.ResolveAccountCorrelationID(ctx, account)
			expectReason(res, err, billing.ReasonAccountCorrelationConflict)
			Expect(fake.Count("ListAccounts")).To(BeZero())
		},
		Entry("provider-only", " value ", ""),
		Entry("local-only", "", " value "),
	)

	DescribeTable("bounds local generation collisions to five attempts",
		func(collisions int, success bool) {
			fake := newFake("")
			account := input(fake, "")
			candidates := []string{}
			account.LocalDuplicate = func(_ context.Context, value, exclude string) (bool, error) {
				Expect(exclude).To(Equal(acctID))
				Expect(value).ToNot(BeEmpty())
				Expect(candidates).ToNot(ContainElement(value))
				candidates = append(candidates, value)
				return len(candidates) <= collisions, nil
			}
			res, err := billing.ResolveAccountCorrelationID(ctx, account)
			Expect(candidates).To(HaveLen(min(collisions+1, 5)))
			Expect(fake.Count("GetAccount")).To(Equal(1))
			if success {
				Expect(err).ToNot(HaveOccurred())
				Expect(res).To(Equal(billing.Resolution{Value: candidates[len(candidates)-1]}))
				Expect(fake.Count("ListAccounts")).To(Equal(1))
			} else {
				expectReason(res, err, billing.ReasonAccountCorrelationConflict)
				Expect(fake.Count("ListAccounts")).To(BeZero())
			}
		},
		Entry("retries twice", 2, true),
		Entry("succeeds on the fifth", 4, true),
		Entry("exhausts five collisions", 5, false),
	)

	DescribeTable("returns no partial result on a local lookup error",
		func(providerValue, localValue string) {
			fake := newFake(providerValue)
			account := input(fake, localValue)
			lookupErr := errors.New("lookup failed")
			account.LocalDuplicate = func(context.Context, string, string) (bool, error) { return false, lookupErr }
			res, err := billing.ResolveAccountCorrelationID(ctx, account)
			Expect(res).To(Equal(billing.Resolution{}))
			Expect(err).To(MatchError(lookupErr))
			Expect(fake.Count("ListAccounts")).To(BeZero())
		},
		Entry("generated", "", ""),
		Entry("adopted", "provider", ""),
		Entry("retained", "", "local"),
	)

	It("returns the classified provider error and no value when the account read fails", func() {
		fake := billingfake.New(billingfake.Scenario{Failures: map[string]billingfake.Failure{
			"GetAccount": {Reason: billing.ReasonUnavailable, Retryable: true, Status: 503},
		}})
		res, err := billing.ResolveAccountCorrelationID(ctx, input(fake, "local"))
		Expect(res).To(Equal(billing.Resolution{}))
		Expect(contractReasonOf(err)).To(Equal(billing.ReasonUnavailable))
		Expect(fake.Count("ListAccounts")).To(BeZero())
	})

	DescribeTable("requires the selected native identity to be the only established carrier",
		func(localValue, carrierID string, count int) {
			accounts := make([]billing.Account, count)
			for i := range accounts {
				accounts[i] = carrier(carrierID, "value")
			}
			adapter := &carrierPagesAdapter{Fake: newFake("value"), pages: []billing.AccountPage{{Accounts: accounts}}}
			res, err := billing.ResolveAccountCorrelationID(ctx, input(adapter, localValue))
			expectReason(res, err, billing.ReasonAccountAmbiguous)
			Expect(adapter.filters).To(ConsistOf(billing.AccountFilter{ExternalID: "value"}))
			Expect(adapter.Count("GetAccount")).To(Equal(1))
		},
		Entry("adopt: foreign sole carrier", "", "acct-2", 1),
		Entry("equal: foreign sole carrier", "value", "acct-2", 1),
		Entry("adopt: multiple carriers", "", providerAcctID, 2),
		Entry("equal: multiple carriers", "value", providerAcctID, 2),
	)

	DescribeTable("rejects every carrier when the selected account read was absent",
		func(localValue, carrierID string, count int) {
			adapter := &carrierPagesAdapter{Fake: newFake(""), pages: []billing.AccountPage{{}}}
			account := input(adapter, localValue)
			account.LocalDuplicate = func(_ context.Context, value, _ string) (bool, error) {
				for range count {
					adapter.pages[0].Accounts = append(adapter.pages[0].Accounts, carrier(carrierID, value))
				}
				return false, nil
			}
			res, err := billing.ResolveAccountCorrelationID(ctx, account)
			expectReason(res, err, billing.ReasonAccountAmbiguous)
			Expect(adapter.filters).To(HaveLen(1))
			Expect(adapter.Count("GetAccount")).To(Equal(1))
		},
		Entry("local-only: foreign sole carrier", "local", "acct-2", 1),
		Entry("local-only: contradictory selected sole carrier", "local", providerAcctID, 1),
		Entry("local-only: multiple carriers", "local", "acct-2", 2),
		Entry("generated: foreign sole carrier", "", "acct-2", 1),
		Entry("generated: contradictory selected sole carrier", "", providerAcctID, 1),
		Entry("generated: multiple carriers", "", "acct-2", 2),
	)

	DescribeTable("keeps established values pending when the complete list has no carrier",
		func(localValue string) {
			adapter := &carrierPagesAdapter{Fake: newFake("value"), pages: []billing.AccountPage{{}}}
			res, err := billing.ResolveAccountCorrelationID(ctx, input(adapter, localValue))
			Expect(err).ToNot(HaveOccurred())
			Expect(res).To(Equal(billing.Resolution{Value: "value"}))
			Expect(adapter.filters).To(HaveLen(1))
			Expect(adapter.Count("GetAccount")).To(Equal(1))
		},
		Entry("adopted", ""),
		Entry("equal", "value"),
	)

	It("walks empty intermediate pages and keeps exact bytes through one complete carrier scan", func() {
		const value = " padded value\t"
		adapter := &carrierPagesAdapter{Fake: newFake(value), pages: []billing.AccountPage{
			{Next: "second"}, {Accounts: []billing.Account{carrier(providerAcctID, value)}, Next: "third"}, {},
		}}
		res, err := billing.ResolveAccountCorrelationID(ctx, input(adapter, ""))
		Expect(err).ToNot(HaveOccurred())
		Expect(res).To(Equal(billing.Resolution{Value: value, ProviderMatches: 1}))
		Expect(adapter.tokens).To(Equal([]string{"", "second", "third"}))
		Expect(adapter.filters).To(HaveEach(Equal(billing.AccountFilter{ExternalID: value})))
		Expect(adapter.Count("GetAccount")).To(Equal(1))
	})

	DescribeTable("requires exhaustion within the 50-page budget, even after a carrier was seen",
		func(last bool) {
			pages := make([]billing.AccountPage, billing.MaxListPages)
			for i := range pages {
				pages[i].Next = "more"
			}
			pages[0].Accounts = []billing.Account{carrier(providerAcctID, "value")}
			if last {
				pages[len(pages)-1].Next = ""
			}
			adapter := &carrierPagesAdapter{Fake: newFake("value"), pages: pages}
			res, err := billing.ResolveAccountCorrelationID(ctx, input(adapter, ""))
			Expect(adapter.filters).To(HaveLen(billing.MaxListPages))
			Expect(adapter.Count("GetAccount")).To(Equal(1))
			if last {
				Expect(err).ToNot(HaveOccurred())
				Expect(res).To(Equal(billing.Resolution{Value: "value", ProviderMatches: 1}))
			} else {
				Expect(res).To(Equal(billing.Resolution{}))
				Expect(contractReasonOf(err)).To(Equal(billing.ReasonMalformed))
			}
		},
		Entry("page 50 ends the scan", true),
		Entry("page 50 still has a next page", false),
	)

	DescribeTable("discards a match on a late-page error or cancellation",
		func(cancelled bool) {
			callCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			providerErr := billing.NewError(billing.ReasonUnavailable, "ListAccounts", true, 503)
			adapter := &carrierPagesAdapter{Fake: newFake("value"), errAt: 2, err: providerErr,
				pages: []billing.AccountPage{{Accounts: []billing.Account{carrier(providerAcctID, "value")}, Next: "next"}}}
			if cancelled {
				adapter.cancel = cancel
			}
			res, err := billing.ResolveAccountCorrelationID(callCtx, input(adapter, ""))
			Expect(res).To(Equal(billing.Resolution{}))
			if cancelled {
				Expect(errors.Is(err, context.Canceled)).To(BeTrue())
			} else {
				Expect(err).To(MatchError(providerErr))
			}
			Expect(adapter.filters).To(HaveLen(2))
			Expect(adapter.Count("GetAccount")).To(Equal(1))
		},
		Entry("late provider error", false),
		Entry("cancellation", true),
	)

	It("retains the persisted local token on revalidation without assuming external remediation", func() {
		fake := newFake("")
		first, err := billing.ResolveAccountCorrelationID(ctx, input(fake, ""))
		Expect(err).ToNot(HaveOccurred())
		second, err := billing.ResolveAccountCorrelationID(ctx, input(fake, first.Value))
		Expect(err).ToNot(HaveOccurred())
		Expect(second).To(Equal(first))
		remediated, err := billing.ResolveAccountCorrelationID(ctx, input(newFake(first.Value), first.Value))
		Expect(err).ToNot(HaveOccurred())
		Expect(remediated).To(Equal(billing.Resolution{Value: first.Value, ProviderMatches: 1}))
	})

	It("returns the same conflict repeatedly and never embeds either correlation value in errors", func() {
		account := input(newFake("super-secret-correlation-value"), "a-different-value")
		first, err1 := billing.ResolveAccountCorrelationID(ctx, account)
		second, err2 := billing.ResolveAccountCorrelationID(ctx, account)
		expectReason(first, err1, billing.ReasonBillingCorrelationIDConflict)
		Expect(second).To(Equal(billing.Resolution{}))
		Expect(err2).To(Equal(err1))
		Expect(strings.Contains(err1.Error(), "super-secret-correlation-value")).To(BeFalse())
		Expect(strings.Contains(err1.Error(), "a-different-value")).To(BeFalse())
	})
})

var _ = Describe("NewCorrelationValue", func() {
	It("returns a non-empty, unique value each call", func() {
		a := billing.NewCorrelationValue()
		b := billing.NewCorrelationValue()
		Expect(a).ToNot(BeEmpty())
		Expect(a).ToNot(Equal(b))
	})
})
