/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package billingfake is a deterministic, in-memory billing.Adapter for tests. It implements the adapter contract,
// not HTTP, so no endpoint, token or network is involved. Every Fake owns its scenario and call log, so suites that
// run in parallel never see each other's state.
package billingfake

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"sync"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

// Key is the provider key of a Fake's Provider.
const Key = "fake"

// Scenario scripts a Fake. It decodes from JSON, see Load. The zero value is an empty, write-unsupported provider.
type Scenario struct {
	Accounts []AccountSpec `json:"accounts"`
	Services []ServiceSpec `json:"services"`
	// CorrelationWrite makes the provider accept SetCorrelationValue; false mimics M360, which cannot.
	CorrelationWrite bool `json:"correlationWrite"`
	// Failures scripts a failure per adapter method name, such as "ListAccounts".
	Failures map[string]Failure `json:"failures"`
}

// AccountSpec scripts one provider account. ExternalID is its correlation value, empty when it has none.
type AccountSpec struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	ExternalID     string               `json:"externalId"`
	State          billing.AccountState `json:"state"`
	RawStatus      string               `json:"rawStatus"`
	ApprovalStatus string               `json:"approvalStatus"`
}

// ServiceSpec scripts one provider service. Rates are keyed by the requested currency; two services with the same
// name script an ambiguous match.
type ServiceSpec struct {
	ID         string                       `json:"id"`
	Name       string                       `json:"name"`
	Attributes []billing.PriceableAttribute `json:"attributes"`
	Rates      map[string]RateCardSpec      `json:"rates"`
}

// RateCardSpec scripts the card returned for a currency. Currency overrides the card's own currency, which
// scripts a currency mismatch; it defaults to the requested one.
type RateCardSpec struct {
	Currency string            `json:"currency"`
	Rows     []billing.RateRow `json:"rows"`
}

// Failure scripts a classified adapter failure. Times is the number of calls that fail before the method starts
// succeeding; 0 means every call fails.
type Failure struct {
	Reason    billing.Reason `json:"reason"`
	Retryable bool           `json:"retryable"`
	Status    int            `json:"status"`
	Times     int            `json:"times"`
}

// Load decodes a JSON scenario fixture, rejecting unknown fields so that a typo cannot silently script nothing.
func Load(data []byte) (Scenario, error) {
	var s Scenario
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&s)
	return s, err
}

// Call is one recorded adapter call. Failed is true when the call returned an error and so had no effect.
type Call struct {
	Op      string
	Account string
	Value   string
	Failed  bool
}

// Fake is a scripted billing.Adapter that records its calls.
type Fake struct {
	mu       sync.Mutex
	scenario Scenario
	accounts []AccountSpec
	failed   map[string]int
	calls    []Call
}

var _ billing.Adapter = (*Fake)(nil)

// New builds a Fake from a scenario. The scenario is copied, so later changes to it do not reach the Fake.
func New(s Scenario) *Fake {
	return &Fake{
		scenario: s,
		accounts: append([]AccountSpec(nil), s.Accounts...),
		failed:   map[string]int{},
	}
}

// Provider is the injection seam: it describes a configured deployment backed by this Fake.
func (f *Fake) Provider() *billing.Provider { return billing.NewProvider(Key, f, nil) }

// Calls returns a copy of the recorded calls in order.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// Count is the number of recorded calls to op, failed or not.
func (f *Fake) Count(op string) int { return f.count(op, false) }

// Succeeded is the number of calls to op that returned no error.
func (f *Fake) Succeeded(op string) int { return f.count(op, true) }

// count totals the recorded calls to op, optionally only the successful ones.
func (f *Fake) count(op string, onlySucceeded bool) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.Op == op && (!onlySucceeded || !c.Failed) {
			n++
		}
	}
	return n
}

// run records a call, applies the scripted failure and otherwise runs body, all under the lock.
func (f *Fake) run(op, account, value string, body func() error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, Call{Op: op, Account: account, Value: value})
	call := &f.calls[len(f.calls)-1]
	var err error
	if spec, ok := f.scenario.Failures[op]; ok && (spec.Times == 0 || f.failed[op] < spec.Times) {
		f.failed[op]++
		err = billing.NewError(spec.Reason, op, spec.Retryable, spec.Status)
	} else {
		err = body()
	}
	call.Failed = err != nil
	return err
}

// find returns the scenario account with id, or a NOT_FOUND error.
func (f *Fake) find(id string) (*AccountSpec, error) {
	for i := range f.accounts {
		if f.accounts[i].ID == id {
			return &f.accounts[i], nil
		}
	}
	return nil, billing.NewError(billing.ReasonNotFound, "account", false, 404)
}

// toAccount maps a scenario account to billing.Account.
func toAccount(a *AccountSpec) billing.Account {
	return billing.Account{
		Ref: billing.NewAccountRef(a.ID), Name: a.Name, ExternalID: a.ExternalID, State: a.State,
		RawStatus: a.RawStatus, ApprovalStatus: a.ApprovalStatus,
	}
}

// ListAccounts implements billing.Adapter over the scenario accounts.
func (f *Fake) ListAccounts(_ context.Context, filter billing.AccountFilter, page billing.Page) (
	result billing.AccountPage, err error) {
	err = f.run("ListAccounts", "", "", func() error {
		result.Accounts = []billing.Account{}
		for i := range f.accounts {
			a := &f.accounts[i]
			if (filter.ExternalID == "" || a.ExternalID == filter.ExternalID) &&
				(filter.Name == "" || a.Name == filter.Name) {
				result.Accounts = append(result.Accounts, toAccount(a))
			}
		}
		// The page token is the offset of the first account to return.
		start, _ := strconv.Atoi(page.Token)
		start = min(max(start, 0), len(result.Accounts))
		end := len(result.Accounts)
		if page.Size > 0 && start+page.Size < end {
			end = start + page.Size
			result.Next = strconv.Itoa(end)
		}
		result.Accounts = result.Accounts[start:end]
		return nil
	})
	return
}

// GetAccount implements billing.Adapter.
func (f *Fake) GetAccount(_ context.Context, ref billing.AccountRef) (account billing.Account, err error) {
	err = f.run("GetAccount", ref.ID(), "", func() error {
		a, err := f.find(ref.ID())
		if err == nil {
			account = toAccount(a)
		}
		return err
	})
	return
}

// CorrelationValue implements billing.Adapter.
func (f *Fake) CorrelationValue(_ context.Context, ref billing.AccountRef) (value string, present bool, err error) {
	err = f.run("CorrelationValue", ref.ID(), "", func() error {
		a, err := f.find(ref.ID())
		if err == nil {
			value, present = a.ExternalID, a.ExternalID != ""
		}
		return err
	})
	return
}

// service returns the scenario service with id, or nil.
func (f *Fake) service(id string) *ServiceSpec {
	for i := range f.scenario.Services {
		if f.scenario.Services[i].ID == id {
			return &f.scenario.Services[i]
		}
	}
	return nil
}

// FindServiceByName implements billing.Adapter.
func (f *Fake) FindServiceByName(_ context.Context, name string) (ref billing.ServiceRef, err error) {
	err = f.run("FindServiceByName", "", name, func() error {
		var found []string
		for _, s := range f.scenario.Services {
			if s.Name == name {
				found = append(found, s.ID)
			}
		}
		switch len(found) {
		case 0:
			return billing.ErrServiceNotFound
		case 1:
			ref = billing.NewServiceRef(found[0])
			return nil
		}
		return billing.NewError(billing.ReasonAmbiguous, "FindServiceByName", false, 0)
	})
	return
}

// ListPriceableAttributes implements billing.Adapter.
func (f *Fake) ListPriceableAttributes(_ context.Context, svc billing.ServiceRef) (
	attrs []billing.PriceableAttribute, err error) {
	err = f.run("ListPriceableAttributes", "", svc.ID(), func() error {
		if s := f.service(svc.ID()); s != nil {
			attrs = append([]billing.PriceableAttribute(nil), s.Attributes...)
			return nil
		}
		return billing.NewError(billing.ReasonNotFound, "ListPriceableAttributes", false, 404)
	})
	return
}

// GetServiceRateCard implements billing.Adapter.
func (f *Fake) GetServiceRateCard(_ context.Context, svc billing.ServiceRef, currency string) (
	card billing.RateCard, err error) {
	err = f.run("GetServiceRateCard", "", svc.ID(), func() error {
		s := f.service(svc.ID())
		if s == nil {
			return billing.ErrRateCardNotFound
		}
		spec, ok := s.Rates[currency]
		if !ok {
			return billing.ErrRateCardNotFound
		}
		card = billing.RateCard{Currency: currency, Rows: append([]billing.RateRow(nil), spec.Rows...)}
		if spec.Currency != "" {
			card.Currency = spec.Currency
		}
		return nil
	})
	return
}

// HealthCheck implements billing.Adapter and always succeeds.
func (f *Fake) HealthCheck(context.Context) error {
	return f.run("HealthCheck", "", "", func() error { return nil })
}

// SupportsCorrelationWrite implements billing.Adapter.
func (f *Fake) SupportsCorrelationWrite() bool { return f.scenario.CorrelationWrite }

// SetCorrelationValue records the attempt even when the provider cannot write, so a test can assert that a
// write-unsupported provider was never asked to.
func (f *Fake) SetCorrelationValue(_ context.Context, ref billing.AccountRef, value string) error {
	return f.run("SetCorrelationValue", ref.ID(), value, func() error {
		if !f.scenario.CorrelationWrite {
			return billing.NewError(billing.ReasonUnsupported, "SetCorrelationValue", false, 0)
		}
		a, err := f.find(ref.ID())
		if err == nil {
			a.ExternalID = value
		}
		return err
	})
}
