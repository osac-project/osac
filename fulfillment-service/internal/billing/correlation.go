/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package billing

import (
	"context"
	"fmt"
	"strings"

	osacuuid "github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

// maxGenerateAttempts bounds retries when a generated value collides with another local billing_account row.
const maxGenerateAttempts = 5

// Stable reasons ResolveAccountCorrelationID reports; callers map them to FAILED_PRECONDITION/<reason> statuses.
const (
	// ReasonBillingCorrelationIDConflict: both sides already have a value and they differ.
	ReasonBillingCorrelationIDConflict = "BILLING_CORRELATION_ID_CONFLICT"
	// ReasonAccountAmbiguous: the resolved value is duplicated or carried by a different provider account.
	ReasonAccountAmbiguous = "ACCOUNT_AMBIGUOUS"
	// ReasonAccountCorrelationConflict: another billing_account row already stores the resolved value.
	ReasonAccountCorrelationConflict = "ACCOUNT_CORRELATION_CONFLICT"
	// ReasonProviderCorrelationWriteUnsupported is the static PENDING manual-remediation reason. Resolution never
	// attempts a provider write or queries a write capability.
	ReasonProviderCorrelationWriteUnsupported = "BILLING_PROVIDER_CORRELATION_WRITE_UNSUPPORTED"
)

// CorrelationResolutionError reports why ResolveAccountCorrelationID could not produce a value. Reason is a
// stable, loggable token that never contains the correlation value.
type CorrelationResolutionError struct {
	Reason string
}

func (e *CorrelationResolutionError) Error() string {
	return fmt.Sprintf("billing correlation resolution: %s", e.Reason)
}

// NewCorrelationValue returns a new, provider-agnostic correlation value, never derived from an OSAC or provider
// identifier. Generating it here keeps the value format out of each adapter, which owns the native read mapping.
func NewCorrelationValue() string {
	return osacuuid.New()
}

// LocalCorrelationLookup reports whether value is already stored on another billing_account row, excluding
// excludeAccountID. A lookup is not a reservation: callers must coordinate concurrent resolutions and recheck
// uniqueness on commit.
type LocalCorrelationLookup func(ctx context.Context, value string, excludeAccountID string) (conflict bool, err error)

// AccountCorrelationInput is everything ResolveAccountCorrelationID needs for one account.
type AccountCorrelationInput struct {
	// Adapter is the billing provider adapter used to read correlation evidence.
	Adapter Adapter
	// AccountID is billing_account.id of the account being resolved, excluded from the local-duplicate search so
	// its own stored value is not a conflict.
	AccountID string
	// Ref is the provider-native account reference used for adapter reads.
	Ref AccountRef
	// LocalValue is billing_account.external_billing_account_id as currently stored in OSAC; "" when unset.
	LocalValue string
	// LocalDuplicate is mandatory: it backs the local-uniqueness check against billing_account.
	LocalDuplicate LocalCorrelationLookup
}

// Resolution is the outcome of ResolveAccountCorrelationID. The caller persists Value in its own transaction;
// this function never writes billing_account.external_billing_account_id.
type Resolution struct {
	// Value is the correlation value to persist in OSAC. Always set on success.
	Value string
	// ProviderMatches is 1 only when the selected account reports Value and is its sole carrier in the complete
	// exact-value scan. Zero means the scan found no carrier, so the caller keeps the assignment PENDING.
	ProviderMatches int
}

// ResolveAccountCorrelationID applies the correlation-id precedence for one account: generate, adopt, or report a
// conflict. It performs only provider reads; the caller commits Resolution.Value itself under database uniqueness.
//
// Every successful outcome makes one correlation-value read and one complete exact-value carrier walk, bounded by
// MaxListPages and the caller's context deadline. A provider-present value must have the selected native identity as
// its sole carrier to count as a match. When the selected account's read was absent, any carrier contradicts that
// observation and fails with ACCOUNT_AMBIGUOUS. Zero carriers leave the resolved value PENDING for manual remediation.
//
// A nonblank established value is never trimmed or rotated. LocalDuplicate is only a check, not a reservation;
// final persistence remains the caller's responsibility. Generated local collisions permit at most five attempts.
func ResolveAccountCorrelationID(ctx context.Context, account AccountCorrelationInput) (Resolution, error) {
	providerRaw, providerHasValue, err := account.Adapter.CorrelationValue(ctx, account.Ref)
	if err != nil {
		return Resolution{}, err
	}
	// An empty or whitespace-only provider value is treated as absent; a real value is never trimmed.
	providerPresent := providerHasValue && strings.TrimSpace(providerRaw) != ""
	localPresent := strings.TrimSpace(account.LocalValue) != ""

	switch {
	case providerPresent && localPresent:
		if providerRaw != account.LocalValue {
			return Resolution{}, &CorrelationResolutionError{Reason: ReasonBillingCorrelationIDConflict}
		}
		matches, err := account.providerPresentMatches(ctx, account.LocalValue)
		if err != nil {
			return Resolution{}, err
		}
		return Resolution{
			Value: account.LocalValue, ProviderMatches: matches,
		}, nil

	case providerPresent:
		if err := account.checkLocalDuplicate(ctx, providerRaw); err != nil {
			return Resolution{}, err
		}
		matches, err := account.providerPresentMatches(ctx, providerRaw)
		if err != nil {
			return Resolution{}, err
		}
		return Resolution{
			Value: providerRaw, ProviderMatches: matches,
		}, nil

	case localPresent:
		if err := account.checkLocalDuplicate(ctx, account.LocalValue); err != nil {
			return Resolution{}, err
		}
		return account.providerAbsentResolution(ctx, account.LocalValue)

	default:
		return account.generate(ctx)
	}
}

// generate produces a new value absent on both sides, retrying on a local collision up to maxGenerateAttempts.
func (account AccountCorrelationInput) generate(ctx context.Context) (Resolution, error) {
	var candidate string
	for attempt := 0; attempt < maxGenerateAttempts; attempt++ {
		candidate = NewCorrelationValue()
		conflict, err := account.LocalDuplicate(ctx, candidate, account.AccountID)
		if err != nil {
			return Resolution{}, err
		}
		if !conflict {
			return account.providerAbsentResolution(ctx, candidate)
		}
	}
	return Resolution{}, &CorrelationResolutionError{Reason: ReasonAccountCorrelationConflict}
}

// checkLocalDuplicate fails with ACCOUNT_CORRELATION_CONFLICT when value is stored on a different local row.
// Generated values regenerate on collision instead (see generate).
func (account AccountCorrelationInput) checkLocalDuplicate(ctx context.Context, value string) error {
	conflict, err := account.LocalDuplicate(ctx, value, account.AccountID)
	if err != nil {
		return err
	}
	if conflict {
		return &CorrelationResolutionError{Reason: ReasonAccountCorrelationConflict}
	}
	return nil
}

// scanProviderCarriers runs one bounded exact-value provider scan, reporting how many accounts carry value and
// whether the selected account is one of them. The adapter's q parameter only narrows candidates; the adapter's own
// equality filter decides each match, and the walk stays bounded by MaxListPages, so a provider that never ends the
// match list is MALFORMED rather than merely ambiguous.
//
// The carrier identity is the point of the scan: the selected account's own read and the match list are two provider
// observations, and a provider that reports the value on the account but only ever lists a different carrier makes
// them disagree. Counting alone would report that foreign account as this account's match.
func (account AccountCorrelationInput) scanProviderCarriers(ctx context.Context, value string) (int, bool, error) {
	carriers := 0
	selected := false
	err := ForEachAccount(ctx, account.Adapter, AccountFilter{ExternalID: value}, Page{}, func(carrier Account) error {
		carriers++
		if carrier.Ref.ID() == account.Ref.ID() {
			selected = true
		}
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	return carriers, selected, nil
}

// providerPresentMatches validates a branch whose selected account already reports value in its own provider read.
// The scan must find value on exactly that account: more than one carrier is ACCOUNT_AMBIGUOUS, and so is a single
// carrier that is a different account, because adopting it would attribute the link to another provider account. No
// carrier leaves the count at zero, so the caller stays PENDING rather than claiming a match the scan did not show.
func (account AccountCorrelationInput) providerPresentMatches(ctx context.Context, value string) (int, error) {
	carriers, selected, err := account.scanProviderCarriers(ctx, value)
	if err != nil {
		return 0, err
	}
	if carriers > 1 || (carriers == 1 && !selected) {
		return 0, &CorrelationResolutionError{Reason: ReasonAccountAmbiguous}
	}
	return carriers, nil
}

// providerAbsentResolution retains a local or generated value only if no provider account carries it. Even a
// sole listed carrier claiming the selected identity contradicts its absent account read, so cannot prove a match.
func (account AccountCorrelationInput) providerAbsentResolution(ctx context.Context, candidate string) (Resolution, error) {
	carriers, _, err := account.scanProviderCarriers(ctx, candidate)
	if err != nil {
		return Resolution{}, err
	}
	if carriers > 0 {
		return Resolution{}, &CorrelationResolutionError{Reason: ReasonAccountAmbiguous}
	}
	return Resolution{Value: candidate}, nil
}
