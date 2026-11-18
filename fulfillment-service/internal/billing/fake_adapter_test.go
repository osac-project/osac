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
	"strings"
	"sync"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

// fakeAdapter is a second provider whose native model differs from M360 and which supports correlation writes.
type fakeAdapter struct {
	mu       sync.Mutex
	accounts map[string]*fakeNativeAccount
	order    []string
	services map[string]string            // native uuid -> display name
	rates    map[string]map[string]string // service uuid -> currency -> unit rate
}

type fakeNativeAccount struct {
	uuid, state, approval, tag string
}

var _ billing.Adapter = (*fakeAdapter)(nil)

// ListAccounts implements billing.Adapter, returning every account in insertion order.
func (f *fakeAdapter) ListAccounts(_ context.Context, _ billing.AccountFilter, _ billing.Page) (billing.AccountPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	page := billing.AccountPage{Accounts: []billing.Account{}}
	for _, id := range f.order {
		page.Accounts = append(page.Accounts, f.toAccount(f.accounts[id]))
	}
	return page, nil
}

// GetAccount implements billing.Adapter.
func (f *fakeAdapter) GetAccount(_ context.Context, ref billing.AccountRef) (billing.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.accounts[ref.ID()]
	if !ok {
		return billing.Account{}, &billing.Error{Reason: billing.ReasonNotFound, Op: "GetAccount"}
	}
	return f.toAccount(a), nil
}

// toAccount maps a native fake account to billing.Account.
func (f *fakeAdapter) toAccount(a *fakeNativeAccount) billing.Account {
	state := billing.AccountUnknown
	switch strings.ToLower(a.state) {
	case "enabled":
		state = billing.AccountActive
	case "disabled":
		state = billing.AccountInactive
	}
	return billing.Account{Ref: billing.NewAccountRef(a.uuid), ExternalID: a.tag, State: state, RawStatus: a.state, ApprovalStatus: a.approval}
}

// CorrelationValue implements billing.Adapter.
func (f *fakeAdapter) CorrelationValue(ctx context.Context, ref billing.AccountRef) (string, bool, error) {
	a, err := f.GetAccount(ctx, ref)
	return a.ExternalID, a.ExternalID != "", err
}

// FindServiceByName implements billing.Adapter.
func (f *fakeAdapter) FindServiceByName(_ context.Context, name string) (billing.ServiceRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var found []string
	for id, n := range f.services {
		if n == name {
			found = append(found, id)
		}
	}
	switch len(found) {
	case 0:
		return billing.ServiceRef{}, billing.ErrServiceNotFound
	case 1:
		return billing.NewServiceRef(found[0]), nil
	}
	return billing.ServiceRef{}, &billing.Error{Reason: billing.ReasonAmbiguous, Op: "FindServiceByName"}
}

// ListPriceableAttributes implements billing.Adapter.
func (f *fakeAdapter) ListPriceableAttributes(context.Context, billing.ServiceRef) ([]billing.PriceableAttribute, error) {
	return []billing.PriceableAttribute{{ID: "dim-1", Name: "gpu_hours"}}, nil
}

// GetServiceRateCard implements billing.Adapter.
func (f *fakeAdapter) GetServiceRateCard(_ context.Context, svc billing.ServiceRef, currency string) (billing.RateCard, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rate, ok := f.rates[svc.ID()][currency]
	if !ok {
		return billing.RateCard{}, billing.ErrRateCardNotFound
	}
	return billing.RateCard{Currency: currency, Rows: []billing.RateRow{{DimensionID: "dim-1", PriceMethod: "QUANTITY", UnitRate: rate}}}, nil
}

// HealthCheck implements billing.Adapter.
func (f *fakeAdapter) HealthCheck(context.Context) error { return nil }

// SupportsCorrelationWrite implements billing.Adapter.
func (f *fakeAdapter) SupportsCorrelationWrite() bool { return true }

// SetCorrelationValue implements billing.Adapter by tagging the account.
func (f *fakeAdapter) SetCorrelationValue(_ context.Context, ref billing.AccountRef, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.accounts[ref.ID()]
	if !ok {
		return &billing.Error{Reason: billing.ReasonMalformed, Op: "SetCorrelationValue"}
	}
	a.tag = value
	return nil
}
