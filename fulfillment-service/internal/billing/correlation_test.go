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

// noDuplicate is a LocalCorrelationLookup that never reports a conflict.
func noDuplicate(context.Context, string, string) (bool, error) { return false, nil }

// alwaysDuplicate is a LocalCorrelationLookup that always reports a conflict.
func alwaysDuplicate(context.Context, string, string) (bool, error) { return true, nil }

// duplicateTimes returns a LocalCorrelationLookup that reports a conflict for the first n calls, then none.
func duplicateTimes(n int) billing.LocalCorrelationLookup {
	calls := 0
	return func(context.Context, string, string) (bool, error) {
		calls++
		return calls <= n, nil
	}
}

// endlessPagesAdapter wraps a fake whose listings always report a following page, reproducing a provider that
// never ends a correlation-value match list.
type endlessPagesAdapter struct {
	*billingfake.Fake
}

func (a *endlessPagesAdapter) ListAccounts(ctx context.Context, filter billing.AccountFilter, page billing.Page) (billing.AccountPage, error) {
	result, err := a.Fake.ListAccounts(ctx, filter, page)
	if err != nil {
		return result, err
	}
	result.Next = "always-more"
	return result, nil
}

var _ = Describe("ResolveAccountCorrelationID", func() {
	ctx := context.Background()
	const acctID = "ba-1"
	const providerAcctID = "acct-1"
	ref := billing.NewAccountRef(providerAcctID)

	newFake := func(correlationWrite bool, externalID string) *billingfake.Fake {
		return billingfake.New(billingfake.Scenario{
			CorrelationWrite: correlationWrite,
			Accounts: []billingfake.AccountSpec{
				{ID: providerAcctID, Name: "Acme", ExternalID: externalID, State: billing.AccountActive},
			},
		})
	}

	DescribeTable("the precedence table",
		func(correlationWrite bool, providerExternalID, localValue string,
			expectValue string, expectWrite bool, expectSkipReason string,
			expectMatches int) {
			fake := newFake(correlationWrite, providerExternalID)
			res, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
				Adapter:        fake,
				AccountID:      acctID,
				Ref:            ref,
				LocalValue:     localValue,
				LocalDuplicate: noDuplicate,
			})
			Expect(err).ToNot(HaveOccurred())
			if expectValue == "" {
				// Generated values are random; every other precedence case returns a known value.
				Expect(res.Value).ToNot(BeEmpty())
			} else {
				Expect(res.Value).To(Equal(expectValue))
			}
			Expect(res.ProviderWritten).To(Equal(expectWrite))
			Expect(res.ProviderWriteSkippedReason).To(Equal(expectSkipReason))
			// ProviderMatches is the pre-write measurement; the write branch entries add one on top.
			Expect(res.ProviderMatches).To(Equal(expectMatches))
			if expectWrite {
				Expect(fake.Succeeded("SetCorrelationValue")).To(Equal(1))
			} else {
				Expect(fake.Count("SetCorrelationValue")).To(Equal(0))
			}
		},
		Entry("both absent, write supported: adopts the generated value and writes it",
			true, "", "", "", true, "", 0),
		Entry("both absent, write unsupported: still generates, reports the write skipped",
			false, "", "", "", false, billing.ReasonProviderCorrelationWriteUnsupported, 0),
		Entry("provider-only present: adopts the provider value, no write needed",
			true, "ext-1", "", "ext-1", false, "", 1),
		Entry("OSAC-only present, write supported: adopts the local value and writes it",
			true, "", "osac-1", "osac-1", true, "", 0),
		Entry("OSAC-only present, write unsupported: adopts locally, reports the write skipped",
			false, "", "osac-1", "osac-1", false, billing.ReasonProviderCorrelationWriteUnsupported, 0),
		Entry("both present and equal: returns it, no write",
			true, "same-1", "same-1", "same-1", false, "", 1),
	)

	It("generates a value that is not the OSAC account id, the provider account id, or empty", func() {
		fake := newFake(true, "")
		res, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
			Adapter: fake, AccountID: acctID, Ref: ref, LocalDuplicate: noDuplicate,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Value).ToNot(BeEmpty())
		Expect(res.Value).ToNot(Equal(acctID))
		Expect(res.Value).ToNot(Equal(providerAcctID))
	})

	It("generates a unique value on every call", func() {
		fake1, fake2 := newFake(true, ""), newFake(true, "")
		res1, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
			Adapter: fake1, AccountID: acctID, Ref: ref, LocalDuplicate: noDuplicate,
		})
		Expect(err).ToNot(HaveOccurred())
		res2, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
			Adapter: fake2, AccountID: acctID, Ref: ref, LocalDuplicate: noDuplicate,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(res1.Value).ToNot(Equal(res2.Value))
	})

	It("fails with BILLING_CORRELATION_ID_CONFLICT when both sides disagree, changing neither side", func() {
		fake := newFake(true, "provider-value")
		res, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
			Adapter: fake, AccountID: acctID, Ref: ref, LocalValue: "local-value", LocalDuplicate: noDuplicate,
		})
		Expect(res).To(Equal(billing.Resolution{}))
		var correlationErr *billing.CorrelationResolutionError
		Expect(errors.As(err, &correlationErr)).To(BeTrue())
		Expect(correlationErr.Reason).To(Equal(billing.ReasonBillingCorrelationIDConflict))
		Expect(fake.Count("SetCorrelationValue")).To(Equal(0))
	})

	It("treats an empty or whitespace-only provider value as absent without trimming a real value", func() {
		fake := newFake(true, "   ")
		res, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
			Adapter: fake, AccountID: acctID, Ref: ref, LocalValue: "osac-1", LocalDuplicate: noDuplicate,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Value).To(Equal("osac-1"))
	})

	It("returns the classified provider error and no value when the correlation read fails", func() {
		fake := billingfake.New(billingfake.Scenario{
			Accounts: []billingfake.AccountSpec{{ID: providerAcctID}},
			Failures: map[string]billingfake.Failure{
				"CorrelationValue": {Reason: billing.ReasonUnavailable, Retryable: true, Status: 503},
			},
		})
		res, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
			Adapter: fake, AccountID: acctID, Ref: ref, LocalValue: "osac-1", LocalDuplicate: noDuplicate,
		})
		Expect(res).To(Equal(billing.Resolution{}))
		var adapterErr *billing.Error
		Expect(errors.As(err, &adapterErr)).To(BeTrue())
		Expect(adapterErr.Reason).To(Equal(billing.ReasonUnavailable))
	})

	It("returns the classified provider error and writes nothing locally when the provider write fails", func() {
		fake := billingfake.New(billingfake.Scenario{
			CorrelationWrite: true,
			Accounts:         []billingfake.AccountSpec{{ID: providerAcctID}},
			Failures: map[string]billingfake.Failure{
				"SetCorrelationValue": {Reason: billing.ReasonUnavailable, Retryable: true, Status: 503},
			},
		})
		res, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
			Adapter: fake, AccountID: acctID, Ref: ref, LocalValue: "osac-1", LocalDuplicate: noDuplicate,
		})
		Expect(res).To(Equal(billing.Resolution{}))
		var adapterErr *billing.Error
		Expect(errors.As(err, &adapterErr)).To(BeTrue())
		Expect(adapterErr.Reason).To(Equal(billing.ReasonUnavailable))
	})

	It("fails with ACCOUNT_CORRELATION_CONFLICT when adopting a value another local account already stores", func() {
		fake := newFake(true, "ext-1")
		res, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
			Adapter: fake, AccountID: acctID, Ref: ref, LocalDuplicate: alwaysDuplicate,
		})
		Expect(res).To(Equal(billing.Resolution{}))
		var correlationErr *billing.CorrelationResolutionError
		Expect(errors.As(err, &correlationErr)).To(BeTrue())
		Expect(correlationErr.Reason).To(Equal(billing.ReasonAccountCorrelationConflict))
	})

	It("regenerates a generated value that collides locally, then succeeds", func() {
		fake := newFake(true, "")
		res, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
			Adapter: fake, AccountID: acctID, Ref: ref, LocalDuplicate: duplicateTimes(2),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Value).ToNot(BeEmpty())
	})

	It("fails with a classified error after exhausting generation retries against a persistent local collision", func() {
		fake := newFake(true, "")
		res, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
			Adapter: fake, AccountID: acctID, Ref: ref, LocalDuplicate: alwaysDuplicate,
		})
		Expect(res).To(Equal(billing.Resolution{}))
		var correlationErr *billing.CorrelationResolutionError
		Expect(errors.As(err, &correlationErr)).To(BeTrue())
		Expect(correlationErr.Reason).To(Equal(billing.ReasonAccountCorrelationConflict))
	})

	It("fails with ACCOUNT_AMBIGUOUS when more than one provider account carries the resolved value", func() {
		fake := billingfake.New(billingfake.Scenario{
			CorrelationWrite: true,
			Accounts: []billingfake.AccountSpec{
				{ID: providerAcctID, ExternalID: "shared-1"},
				{ID: "acct-2", ExternalID: "shared-1"},
			},
		})
		res, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
			Adapter: fake, AccountID: acctID, Ref: ref, LocalDuplicate: noDuplicate,
		})
		Expect(res).To(Equal(billing.Resolution{}))
		var correlationErr *billing.CorrelationResolutionError
		Expect(errors.As(err, &correlationErr)).To(BeTrue())
		Expect(correlationErr.Reason).To(Equal(billing.ReasonAccountAmbiguous))
	})

	It("fails with ACCOUNT_AMBIGUOUS when another provider account carries an already-consistent value", func() {
		fake := billingfake.New(billingfake.Scenario{
			CorrelationWrite: true,
			Accounts: []billingfake.AccountSpec{
				{ID: providerAcctID, ExternalID: "same-1"},
				{ID: "acct-2", ExternalID: "same-1"},
			},
		})
		res, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
			Adapter: fake, AccountID: acctID, Ref: ref, LocalValue: "same-1", LocalDuplicate: noDuplicate,
		})
		Expect(res).To(Equal(billing.Resolution{}))
		var correlationErr *billing.CorrelationResolutionError
		Expect(errors.As(err, &correlationErr)).To(BeTrue())
		Expect(correlationErr.Reason).To(Equal(billing.ReasonAccountAmbiguous))
	})

	It("fails with a MALFORMED provider error when the uniqueness scan never reaches a last page", func() {
		fake := &endlessPagesAdapter{Fake: newFake(true, "ext-1")}
		res, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
			Adapter: fake, AccountID: acctID, Ref: ref, LocalDuplicate: noDuplicate,
		})
		Expect(res).To(Equal(billing.Resolution{}))
		var adapterErr *billing.Error
		Expect(errors.As(err, &adapterErr)).To(BeTrue())
		Expect(adapterErr.Reason).To(Equal(billing.ReasonMalformed))
	})

	It("checks provider uniqueness before writing an adopted OSAC-only value, so an ambiguous match leaves no orphaned write", func() {
		fake := billingfake.New(billingfake.Scenario{
			CorrelationWrite: true,
			Accounts: []billingfake.AccountSpec{
				{ID: providerAcctID}, // this account: no correlation value yet
				{ID: "acct-2", ExternalID: "osac-1"},
				{ID: "acct-3", ExternalID: "osac-1"},
			},
		})
		res, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
			Adapter: fake, AccountID: acctID, Ref: ref, LocalValue: "osac-1", LocalDuplicate: noDuplicate,
		})
		Expect(res).To(Equal(billing.Resolution{}))
		var correlationErr *billing.CorrelationResolutionError
		Expect(errors.As(err, &correlationErr)).To(BeTrue())
		Expect(correlationErr.Reason).To(Equal(billing.ReasonAccountAmbiguous))
		Expect(fake.Count("SetCorrelationValue")).To(Equal(0), "must not write before the uniqueness check passes")
	})

	It("is idempotent: a second call after a successful write adopts the now-consistent value without writing again", func() {
		fake := newFake(true, "")
		first, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
			Adapter: fake, AccountID: acctID, Ref: ref, LocalDuplicate: noDuplicate,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(fake.Succeeded("SetCorrelationValue")).To(Equal(1))

		// OSAC has not committed first.Value yet: the provider already has it (self-heal path).
		second, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
			Adapter: fake, AccountID: acctID, Ref: ref, LocalDuplicate: noDuplicate,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(second.Value).To(Equal(first.Value))
		Expect(second.ProviderMatches).To(Equal(1))
		Expect(fake.Succeeded("SetCorrelationValue")).To(Equal(1), "second call must not write again")

		// Once OSAC commits the value, a third call sees both sides equal and still does not write.
		third, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
			Adapter: fake, AccountID: acctID, Ref: ref, LocalValue: first.Value, LocalDuplicate: noDuplicate,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(third.Value).To(Equal(first.Value))
		Expect(third.ProviderMatches).To(Equal(1))
		Expect(fake.Succeeded("SetCorrelationValue")).To(Equal(1))
	})

	It("is idempotent on a repeated conflict: the same error is returned and nothing changes", func() {
		fake := newFake(true, "provider-value")
		input := billing.AccountCorrelationInput{
			Adapter: fake, AccountID: acctID, Ref: ref, LocalValue: "local-value", LocalDuplicate: noDuplicate,
		}
		_, err1 := billing.ResolveAccountCorrelationID(ctx, input)
		_, err2 := billing.ResolveAccountCorrelationID(ctx, input)
		Expect(err1).To(Equal(err2))
	})

	It("never embeds the correlation value in an error string", func() {
		fake := newFake(true, "super-secret-correlation-value")
		_, err := billing.ResolveAccountCorrelationID(ctx, billing.AccountCorrelationInput{
			Adapter: fake, AccountID: acctID, Ref: ref, LocalValue: "a-different-value", LocalDuplicate: noDuplicate,
		})
		Expect(err).To(HaveOccurred())
		Expect(strings.Contains(err.Error(), "super-secret-correlation-value")).To(BeFalse())
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
