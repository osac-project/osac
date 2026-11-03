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

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

// pagedAccount builds an account with the given provider id and correlation value.
func pagedAccount(id, externalID string) billing.Account {
	return billing.Account{Ref: billing.NewAccountRef(id), ExternalID: externalID, State: billing.AccountActive}
}

// pagedAdapter serves fixed pages in order, one per ListAccounts call, and counts the pages it served - it
// reproduces a provider that pages its account list, for the account-walk specs.
type pagedAdapter struct {
	billing.Adapter
	pages  []billing.AccountPage
	served int
}

// ListAccounts returns the next fixed page, narrowed to the accounts that exactly match filter.ExternalID the
// way the Adapter contract requires, and ignoring the requested page token.
func (a *pagedAdapter) ListAccounts(_ context.Context, filter billing.AccountFilter, _ billing.Page) (
	billing.AccountPage, error) {
	page := a.pages[a.served]
	a.served++
	if filter.ExternalID == "" {
		return page, nil
	}
	matched := billing.AccountPage{Next: page.Next}
	for _, account := range page.Accounts {
		if account.ExternalID == filter.ExternalID {
			matched.Accounts = append(matched.Accounts, account)
		}
	}
	return matched, nil
}

// endlessAdapter always reports a following page - it reproduces a provider that never ends its account list.
type endlessAdapter struct {
	billing.Adapter
}

// ListAccounts always returns one account and a non-empty Next token.
func (a *endlessAdapter) ListAccounts(_ context.Context, _ billing.AccountFilter, _ billing.Page) (
	billing.AccountPage, error) {
	return billing.AccountPage{Accounts: []billing.Account{pagedAccount("acct-1", "")}, Next: "always-more"}, nil
}

var _ = Describe("ForEachAccount", func() {
	It("walks every account of every page in order and ends at the provider's last page", func() {
		adapter := &pagedAdapter{pages: []billing.AccountPage{
			{Accounts: []billing.Account{pagedAccount("acct-1", ""), pagedAccount("acct-2", "")}, Next: "page-2"},
			{Accounts: []billing.Account{pagedAccount("acct-3", "")}},
		}}
		var ids []string
		err := billing.ForEachAccount(context.Background(), adapter, billing.AccountFilter{}, billing.Page{},
			func(account billing.Account) error {
				ids = append(ids, account.Ref.ID())
				return nil
			})
		Expect(err).ToNot(HaveOccurred())
		Expect(ids).To(Equal([]string{"acct-1", "acct-2", "acct-3"}))
		Expect(adapter.served).To(Equal(2))
	})

	It("stops at the callback's error without reading further pages", func() {
		adapter := &pagedAdapter{pages: []billing.AccountPage{
			{Accounts: []billing.Account{pagedAccount("acct-1", ""), pagedAccount("acct-2", "")}, Next: "page-2"},
			{Accounts: []billing.Account{pagedAccount("acct-3", "")}},
		}}
		stop := errors.New("stop the walk")
		var ids []string
		err := billing.ForEachAccount(context.Background(), adapter, billing.AccountFilter{}, billing.Page{},
			func(account billing.Account) error {
				ids = append(ids, account.Ref.ID())
				if account.Ref.ID() == "acct-2" {
					return stop
				}
				return nil
			})
		Expect(err).To(MatchError(stop))
		Expect(ids).To(Equal([]string{"acct-1", "acct-2"}))
		Expect(adapter.served).To(Equal(1), "the walk must not read page 2 after the callback failed")
	})

	It("fails with a MALFORMED provider error when the provider never reports a last page", func() {
		err := billing.ForEachAccount(context.Background(), &endlessAdapter{}, billing.AccountFilter{}, billing.Page{},
			func(billing.Account) error { return nil })
		var providerErr *billing.Error
		Expect(errors.As(err, &providerErr)).To(BeTrue())
		Expect(providerErr.Reason).To(Equal(billing.ReasonMalformed))
	})
})

var _ = Describe("CountAccountsWithExternalID", func() {
	It("counts a single match and no match exactly", func() {
		one := &pagedAdapter{pages: []billing.AccountPage{
			{Accounts: []billing.Account{pagedAccount("acct-1", "ext-1"), pagedAccount("acct-2", "")}},
		}}
		count, err := billing.CountAccountsWithExternalID(context.Background(), one, "ext-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))

		none := &pagedAdapter{pages: []billing.AccountPage{{Accounts: []billing.Account{}}}}
		count, err = billing.CountAccountsWithExternalID(context.Background(), none, "ext-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(BeZero())
	})

	It("counts every match across pages, so an endless match list is MALFORMED rather than merely ambiguous", func() {
		adapter := &pagedAdapter{pages: []billing.AccountPage{
			{Accounts: []billing.Account{pagedAccount("acct-1", "ext-1")}, Next: "page-2"},
			{Accounts: []billing.Account{pagedAccount("acct-2", "ext-1")}, Next: "page-3"},
			{Accounts: []billing.Account{pagedAccount("acct-3", "ext-1")}},
		}}
		count, err := billing.CountAccountsWithExternalID(context.Background(), adapter, "ext-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(3), "the count is exact: a caller must be able to tell one match from many")
		Expect(adapter.served).To(Equal(3))
	})
})
