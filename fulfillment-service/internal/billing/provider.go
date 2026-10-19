/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package billing

import (
	"context"
	"errors"
)

var ErrAccountExternalIDChanged = errors.New("billing account external ID changed before update")

// ErrProviderUnavailable marks a provider API that could not satisfy an
// operation. Billing gates may allow a launch on this error, but must still
// reject invalid provider data.
var ErrProviderUnavailable = errors.New("billing provider unavailable")

// ErrProviderReadRetryable marks a provider read failure that may succeed if
// retried. Provider adapters should wrap it only for timeouts, rate limits, or
// server errors; callers must not infer retryability from ErrProviderUnavailable.
var ErrProviderReadRetryable = errors.New("billing provider read is retryable")

// AccountState is OSAC's normalized account state, independent of a billing
// provider's status vocabulary.
type AccountState int

const (
	AccountStateUnknown AccountState = iota
	AccountStateActive
	AccountStateInactive
)

// Account is the provider-neutral subset of a billing account used by OSAC.
type Account struct {
	Reference   string
	DisplayName string
	ExternalID  string
	Currency    string
	State       AccountState
}

// ProviderState is OSAC's normalized reachability state for the provider API
// probed by GetStatus. It does not describe usage delivery or unprobed APIs.
type ProviderState int

const (
	ProviderStateUnknown ProviderState = iota
	ProviderStateNotConfigured
	ProviderStateAvailable
	ProviderStateUnavailable
)

// UsageDeliveryState describes whether metering usage is reaching the billing
// provider. Fulfillment cannot observe the adapter's Kafka delivery lag or DLQ,
// so the state remains unknown unless a future integration supplies that data.
type UsageDeliveryState int

const (
	UsageDeliveryStateUnknown UsageDeliveryState = iota
	UsageDeliveryStateAvailable
	UsageDeliveryStateUnavailable
)

// ProviderStatus contains normalized reachability information for the API
// probed by the provider adapter, plus separate usage delivery state.
type ProviderStatus struct {
	State              ProviderState
	UsageDeliveryState UsageDeliveryState
	ProviderKey        string
	Message            string
	Capabilities       []ProviderCapability
}

// ProviderCapability identifies a billing operation implemented by the
// configured provider adapter. Its presence in a ProviderStatus describes
// support only; it does not indicate that the operation's API is reachable.
type ProviderCapability string

const (
	ProviderCapabilityPricing     ProviderCapability = "PRICING"
	ProviderCapabilityCosts       ProviderCapability = "COSTS"
	ProviderCapabilityInvoices    ProviderCapability = "INVOICES"
	ProviderCapabilityDraftCharge ProviderCapability = "DRAFT_CHARGES"
	ProviderCapabilityExports     ProviderCapability = "EXPORTS"
)

// Provider exposes the billing operations required by the first account-link
// slice. Implementations translate provider-native formats into these types.
type Provider interface {
	GetStatus(context.Context) (ProviderStatus, error)
	ListAccounts(context.Context) ([]Account, error)
	GetAccount(context.Context, string) (Account, error)
	UpdateAccountExternalID(ctx context.Context, reference, expectedExternalID, newExternalID string) error
}

// AccountStatusProvider reads the current status of one provider account. Billing
// gates use it to avoid authorizing publication or launch from stale OSAC state.
type AccountStatusProvider interface {
	GetAccount(context.Context, string) (Account, error)
}

// ResolveProviderStatus returns a safe, provider-neutral status for the
// deployment. Provider error text is deliberately not returned to clients.
func ResolveProviderStatus(ctx context.Context, providerKey string, provider Provider) ProviderStatus {
	if provider == nil {
		return ProviderStatus{State: ProviderStateNotConfigured}
	}
	status, err := RetryProviderRead(ctx, provider.GetStatus)
	status.ProviderKey = providerKey
	if err != nil {
		return ProviderStatus{
			State:       ProviderStateUnavailable,
			ProviderKey: providerKey,
			Message:     "provider health check failed",
		}
	}
	return status
}
