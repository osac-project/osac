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
	// ReasonProviderCorrelationWriteUnsupported: the adapter cannot write the provider's correlation field, so the
	// write was skipped. Not a failure: Resolution still carries a value to persist.
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
// identifier. Generating it here keeps the value format out of each adapter, which owns only the native field and
// write support.
func NewCorrelationValue() string {
	return osacuuid.New()
}

// LocalCorrelationLookup reports whether value is already stored on another billing_account row, excluding
// excludeAccountID. A lookup is not a reservation: callers must coordinate concurrent resolutions and recheck
// uniqueness on commit.
type LocalCorrelationLookup func(ctx context.Context, value string, excludeAccountID string) (conflict bool, err error)

// AccountCorrelationInput is everything ResolveAccountCorrelationID needs for one account.
type AccountCorrelationInput struct {
	// Adapter is the billing provider adapter used to read and, where supported, write the correlation value.
	Adapter Adapter
	// AccountID is billing_account.id of the account being resolved, excluded from the local-duplicate search so
	// its own stored value is not a conflict.
	AccountID string
	// Ref is the provider-native account reference used for adapter reads and writes.
	Ref AccountRef
	// LocalValue is billing_account.correlation_id as currently stored in OSAC for this account; "" when unset.
	LocalValue string
	// LocalDuplicate is mandatory: it backs the local-uniqueness check against billing_account.
	LocalDuplicate LocalCorrelationLookup
}

// Resolution is the outcome of ResolveAccountCorrelationID. The caller persists Value in its own transaction;
// this function never writes billing_account.correlation_id.
type Resolution struct {
	// Value is the correlation value to persist in OSAC. Always set on success.
	Value string
	// ProviderWritten is true only when this call wrote Value to the provider and the write succeeded.
	ProviderWritten bool
	// ProviderWriteSkippedReason is ReasonProviderCorrelationWriteUnsupported when a needed provider write was
	// unsupported, and "" otherwise.
	ProviderWriteSkippedReason string
	// ProviderMatches is how many provider accounts carried Value when the uniqueness scan ran, before any write.
	// When ProviderWritten is true the current count is ProviderMatches plus one.
	ProviderMatches int
}

// ResolveAccountCorrelationID applies the correlation-id precedence for one account: generate, adopt, or report a
// conflict. It performs provider reads and, where supported, one provider write; the caller commits
// Resolution.Value itself.
//
// Every successful outcome runs the same bounded uniqueness scan, so an account sharing its value with another
// provider account fails with ACCOUNT_AMBIGUOUS. The function is idempotent: a repeat call observes the provider
// write and performs no further write.
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
		matches, err := account.countProviderMatches(ctx, account.LocalValue)
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
		matches, err := account.countProviderMatches(ctx, providerRaw)
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
		// Check uniqueness before writing, so a failed check cannot leave an orphaned provider write.
		return account.commitValue(ctx, account.LocalValue)

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
			// As above, check uniqueness before writing so a failed check cannot leave an orphaned write.
			return account.commitValue(ctx, candidate)
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

// writeIfSupported writes value to the provider when the adapter supports it, reporting the skip reason
// otherwise; it never calls SetCorrelationValue when SupportsCorrelationWrite is false.
func (account AccountCorrelationInput) writeIfSupported(ctx context.Context, value string) (
	written bool, skipReason string, err error) {
	if !account.Adapter.SupportsCorrelationWrite() {
		return false, ReasonProviderCorrelationWriteUnsupported, nil
	}
	if err := account.Adapter.SetCorrelationValue(ctx, account.Ref, value); err != nil {
		return false, "", err
	}
	return true, "", nil
}

// countProviderMatches counts provider accounts carrying value, failing with ACCOUNT_AMBIGUOUS when more than one
// does. It backs Resolution.ProviderMatches. Provider-present branches count the assigned account itself;
// commitValue requires zero because that account does not carry the value.
func (account AccountCorrelationInput) countProviderMatches(ctx context.Context, value string) (int, error) {
	matches, err := CountAccountsWithExternalID(ctx, account.Adapter, value)
	if err != nil {
		return 0, err
	}
	if matches > 1 {
		return 0, &CorrelationResolutionError{Reason: ReasonAccountAmbiguous}
	}
	return matches, nil
}

// commitValue finishes a write-performing branch: it confirms the candidate is unique among provider accounts,
// writes it where supported, and reports the pre-write match count and write outcome.
func (account AccountCorrelationInput) commitValue(ctx context.Context, candidate string) (Resolution, error) {
	matches, err := account.countProviderMatches(ctx, candidate)
	if err != nil {
		return Resolution{}, err
	}
	// The assigned account must be the unique carrier. This path runs only when it has no value, so every pre-write
	// match is foreign and must be rejected before writing.
	if matches > 0 {
		return Resolution{}, &CorrelationResolutionError{Reason: ReasonAccountAmbiguous}
	}
	written, skipReason, err := account.writeIfSupported(ctx, candidate)
	if err != nil {
		return Resolution{}, err
	}
	return Resolution{
		Value: candidate, ProviderMatches: matches,
		ProviderWritten: written, ProviderWriteSkippedReason: skipReason,
	}, nil
}
