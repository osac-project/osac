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
	"errors"
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
	// ReasonBillingCorrelationReadbackMismatch: the provider acknowledged a correlation write, but a follow-up read
	// did not return exactly the written value, or the value is not carried by exactly one provider account. The
	// acknowledgement alone never establishes a match, so no assignment may be recorded.
	ReasonBillingCorrelationReadbackMismatch = "BILLING_CORRELATION_READBACK_MISMATCH"
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
	// ProviderWritten reports that this call sent a correlation write to the provider and the provider acknowledged
	// it. An acknowledgement alone proves nothing: see ProviderMatches, which is set from a read-back.
	ProviderWritten bool
	// ProviderWriteSkippedReason is ReasonProviderCorrelationWriteUnsupported when a needed provider write was
	// unsupported, and "" otherwise.
	ProviderWriteSkippedReason string
	// ProviderMatches is the number of provider accounts that carried Value in the last uniqueness scan, so 1 means
	// exactly one provider account holds it and 0 means none does. A count of 1 is only reported once the scan shows
	// that account is the selected one: a foreign account that is the sole carrier is never counted as this account's
	// match. A write is never counted from its acknowledgement: when this call wrote the value, ProviderMatches is
	// measured after a read-back that returned it exactly.
	ProviderMatches int
}

// ResolveAccountCorrelationID applies the correlation-id precedence for one account: generate, adopt, or report a
// conflict. It performs provider reads and, where supported, one provider write; the caller commits
// Resolution.Value itself.
//
// Every successful outcome runs the same bounded uniqueness scan, so an account sharing its value with another
// provider account fails with ACCOUNT_AMBIGUOUS. That scan keeps the carrier identity, because a count alone cannot
// tell the selected account's match from a sole carrier that is a different account when the provider's account read
// and its match list disagree. A provider write is proven, not assumed: after the provider acknowledges it the
// account is read back and must return exactly the written value, and a second uniqueness scan must find that value
// on exactly that account and no other. Resolution.Value is only reported once both hold.
//
// One resolution makes a bounded number of provider calls: one value read, one pre-write uniqueness scan when a
// write is needed, the write itself, and then one read-back plus one post-write uniqueness scan. Each scan is
// bounded by MaxListPages and every call by the caller's context deadline, so the budget is a small constant and
// never grows with the account inventory. The function is idempotent: a repeat call observes the provider write and
// performs no further write.
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
//
// A provider may advertise the capability yet reject this one account, for example a deployment that disables the
// account update endpoint or an account configured read-only. A classified UNSUPPORTED is therefore a skip, not a
// failure: the caller stores the value on the OSAC side and keeps the assignment PENDING with the same stable
// remediation as an adapter that cannot write at all. Any other failure is returned unchanged.
func (account AccountCorrelationInput) writeIfSupported(ctx context.Context, value string) (
	written bool, skipReason string, err error) {
	if !account.Adapter.SupportsCorrelationWrite() {
		return false, ReasonProviderCorrelationWriteUnsupported, nil
	}
	if err := account.Adapter.SetCorrelationValue(ctx, account.Ref, value); err != nil {
		var adapterErr *Error
		if errors.As(err, &adapterErr) && adapterErr.Reason == ReasonUnsupported {
			return false, ReasonProviderCorrelationWriteUnsupported, nil
		}
		return false, "", err
	}
	return true, "", nil
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

// countProviderMatches counts provider accounts carrying value for the pre-write check, failing with
// ACCOUNT_AMBIGUOUS when more than one does. It is deliberately identity-blind: commitValue runs only when the
// selected account has no value, so every pre-write match is foreign and any match must reject the write.
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
// writes it where supported, and proves the write before reporting a match.
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
	if !written {
		// Nothing reached the provider, so the count stays 0 and the caller keeps the assignment PENDING.
		return Resolution{
			Value: candidate, ProviderMatches: matches, ProviderWriteSkippedReason: skipReason,
		}, nil
	}
	matches, err = account.verifyWritten(ctx, candidate)
	if err != nil {
		return Resolution{}, err
	}
	return Resolution{
		Value: candidate, ProviderMatches: matches, ProviderWritten: true,
	}, nil
}

// verifyWritten proves that a provider write took effect, never trusting its acknowledgement: it reads the account
// back and requires the exact written value, then re-runs the bounded global uniqueness scan and requires the value
// to be carried by exactly one account, which must be the assigned one. A read-back that is absent or differs, or a
// final scan that does not show the assigned account as the sole carrier, fails with
// BILLING_CORRELATION_READBACK_MISMATCH; a duplicated value still fails with ACCOUNT_AMBIGUOUS from the scan itself.
func (account AccountCorrelationInput) verifyWritten(ctx context.Context, value string) (int, error) {
	readBack, present, err := account.Adapter.CorrelationValue(ctx, account.Ref)
	if err != nil {
		return 0, err
	}
	if !present || readBack != value {
		return 0, &CorrelationResolutionError{Reason: ReasonBillingCorrelationReadbackMismatch}
	}
	carriers, selected, err := account.scanProviderCarriers(ctx, value)
	if err != nil {
		return 0, err
	}
	if carriers > 1 {
		return 0, &CorrelationResolutionError{Reason: ReasonAccountAmbiguous}
	}
	// Zero carriers, or a sole carrier that is a different account, means the read-back and the match list disagree
	// about who holds the value; only this account as the unique carrier proves the write.
	if carriers != 1 || !selected {
		return 0, &CorrelationResolutionError{Reason: ReasonBillingCorrelationReadbackMismatch}
	}
	return carriers, nil
}
