/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package m360

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

const healthCheckTimeout = 3 * time.Second

// M360 wire shapes for accounts live here so a change in the provider contract touches one place.
type account struct {
	ID                string     `json:"Id"`
	Name              string     `json:"Name"`
	ExternalID        string     `json:"ExternalId"`
	StatusRef         *statusRef `json:"Status__r"`
	ApprovalStatusRef *statusRef `json:"ApprovalStatus__r"`
}

// statusRef is the readable side of Status and ApprovalStatus, whose own values are UUIDs or null.
type statusRef struct {
	Name string `json:"Name"`
}

// name is the readable status text, empty when the reference is null.
func (r *statusRef) name() string {
	if r == nil {
		return ""
	}
	return r.Name
}

// normalize maps a wire account to billing.Account, failing MALFORMED when
// the provider id is missing.
func (a account) normalize(op string) (billing.Account, error) {
	if a.ID == "" {
		return billing.Account{}, &billing.Error{Reason: billing.ReasonMalformed, Op: op}
	}
	rawStatus := a.StatusRef.name()
	approvalStatus := a.ApprovalStatusRef.name()
	return billing.Account{
		Ref:            billing.NewAccountRef(a.ID),
		Name:           a.Name,
		ExternalID:     a.ExternalID,
		State:          mapState(rawStatus, approvalStatus),
		RawStatus:      rawStatus,
		ApprovalStatus: approvalStatus,
		// M360's AccountRecord has no currency field: BaseCurrency stays the empty zero value rather than
		// being invented.
	}, nil
}

// statusSignal and approvalSignal are the only M360 Status__r/ApprovalStatus__r values this adapter recognizes as
// carrying an active or inactive signal. Any other value - including empty, null, or a value not in this list -
// is a neutral/unknown signal. Mapping a provider's native shape onto OSAC's normalized state is this adapter's
// responsibility (not a shared, externally configured table): see design.md §2.3 Assumptions.
//
// ASSUMPTION (flagged for review): the exact set of M360 Status/ApprovalStatus string values is not documented
// anywhere in the OSAC-6883 story, the design doc, or the PRD. The values below reflect only what appears in this
// adapter's own pre-existing test fixtures ("Active", "Inactive", "Approved"). Verify these literals against the
// real M360 picklist values before this mapping reaches production.
var statusSignal = map[string]billing.AccountState{
	"Active":   billing.AccountActive,
	"Inactive": billing.AccountInactive,
}

var approvalSignal = map[string]billing.AccountState{
	"Approved": billing.AccountActive,
	"Rejected": billing.AccountInactive,
}

// mapState normalizes M360's Status and ApprovalStatus to ACTIVE/INACTIVE/UNKNOWN. An account is ACTIVE only
// when both signals agree it is; either signal reporting INACTIVE wins over the other; every other combination -
// unmapped, missing, or disagreeing (ambiguous) - is UNKNOWN. ACTIVE is never returned on a partial signal.
func mapState(rawStatus, approvalStatus string) billing.AccountState {
	status := statusSignal[rawStatus]
	approval := approvalSignal[approvalStatus]
	switch {
	case status == billing.AccountInactive || approval == billing.AccountInactive:
		return billing.AccountInactive
	case status == billing.AccountActive && approval == billing.AccountActive:
		return billing.AccountActive
	default:
		return billing.AccountUnknown
	}
}

// ListAccounts implements billing.Adapter.
func (a *adapter) ListAccounts(ctx context.Context, filter billing.AccountFilter, page billing.Page) (billing.AccountPage, error) {
	const op = "ListAccounts"
	size := page.Size
	if size <= 0 || size > maxPageSize {
		size = maxPageSize
	}
	query := url.Values{"size": {strconv.Itoa(size)}}
	if page.Token != "" {
		query.Set("page", page.Token)
	}
	// q is a case-insensitive substring search over name, number and externalId, so it only narrows the
	// candidates; the exact match below is what decides.
	switch {
	case filter.ExternalID != "":
		query.Set("q", filter.ExternalID)
	case filter.Name != "":
		query.Set("q", filter.Name)
	}
	var resp envelope[pageData[account]]
	if err := a.client.getDiscovery(ctx, op, "/accounts", query, &resp); err != nil {
		return billing.AccountPage{}, err
	}
	result := billing.AccountPage{Accounts: make([]billing.Account, 0, len(resp.Data.Content)), Next: resp.Data.next()}
	for _, item := range resp.Data.Content {
		acct, err := item.normalize(op)
		if err != nil {
			return billing.AccountPage{}, err
		}
		if (filter.ExternalID == "" || acct.ExternalID == filter.ExternalID) &&
			(filter.Name == "" || acct.Name == filter.Name) {
			result.Accounts = append(result.Accounts, acct)
		}
	}
	return result, nil
}

// GetAccount implements billing.Adapter.
func (a *adapter) GetAccount(ctx context.Context, ref billing.AccountRef) (billing.Account, error) {
	const op = "GetAccount"
	var resp envelope[account]
	if err := a.client.getDiscovery(ctx, op, "/accounts/"+url.PathEscape(ref.ID()), nil, &resp); err != nil {
		return billing.Account{}, notFoundOn404(op, err)
	}
	return resp.Data.normalize(op)
}

// CorrelationValue implements billing.Adapter.
func (a *adapter) CorrelationValue(ctx context.Context, ref billing.AccountRef) (string, bool, error) {
	acct, err := a.GetAccount(ctx, ref)
	if err != nil {
		return "", false, err
	}
	return acct.ExternalID, acct.ExternalID != "", nil
}

// M360 accepts externalId only when an account is created, so there is nothing to write afterwards.
func (a *adapter) SupportsCorrelationWrite() bool { return false }

// SetCorrelationValue implements billing.Adapter and always fails UNSUPPORTED.
func (a *adapter) SetCorrelationValue(context.Context, billing.AccountRef, string) error {
	return &billing.Error{Reason: billing.ReasonUnsupported, Op: "SetCorrelationValue"}
}

// HealthCheck implements billing.Adapter with one bounded discovery read.
// The response is decoded into an envelope so a 2xx failure body is rejected.
func (a *adapter) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()
	query := url.Values{"size": {"1"}}
	var resp envelope[pageData[account]]
	return a.client.getDiscovery(ctx, "HealthCheck", "/accounts", query, &resp)
}
