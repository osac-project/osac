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

// maxGenerateAttempts bounds how many times ResolveAccountCorrelationID regenerates a value that collides with
// another local billing_account row before giving up with a classified error.
const maxGenerateAttempts = 5

// Stable reasons ResolveAccountCorrelationID can report. The caller (OSAC-6884) maps these to
// FAILED_PRECONDITION/<reason> gRPC statuses; ResolveAccountCorrelationID itself has no API surface.
const (
	// ReasonBillingCorrelationIDConflict: both sides already have a value and they differ.
	ReasonBillingCorrelationIDConflict = "BILLING_CORRELATION_ID_CONFLICT"
	// ReasonAccountAmbiguous: the resolved value is duplicated or carried by a different provider account.
	ReasonAccountAmbiguous = "ACCOUNT_AMBIGUOUS"
	// ReasonAccountCorrelationConflict: another billing_account row already stores the resolved value.
	ReasonAccountCorrelationConflict = "ACCOUNT_CORRELATION_CONFLICT"
	// ReasonProviderCorrelationWriteUnsupported: the adapter cannot write the provider's correlation field, so a
	// resolution that would otherwise write one was skipped. This is not a failure; Resolution still carries a
	// value to persist in OSAC.
	ReasonProviderCorrelationWriteUnsupported = "BILLING_PROVIDER_CORRELATION_WRITE_UNSUPPORTED"
)

// CorrelationResolutionError reports why ResolveAccountCorrelationID could not produce a value to persist. Reason
// is a stable, loggable token; it never carries the correlation value itself.
type CorrelationResolutionError struct {
	Reason string
}

func (e *CorrelationResolutionError) Error() string {
	return fmt.Sprintf("billing correlation resolution: %s", e.Reason)
}

// NewCorrelationValue returns a new, provider-agnostic correlation value. It is never derived from the OSAC
// tenant id, the OSAC billing_account id, or a provider's native account id. This is a package-level default
// rather than an Adapter method: no adapter needs a provider-specific generated-value shape (the native field
// name and write support are what design.md §2.3 scopes as adapter-internal, not the generation format).
func NewCorrelationValue() string {
	return osacuuid.New()
}

// LocalCorrelationLookup reports whether value is already stored as another billing_account row's
// correlation_id, excluding excludeAccountID (the account being resolved). The caller must coordinate concurrent
// resolutions of the same value and recheck uniqueness when committing it. A lookup alone is not a reservation.
type LocalCorrelationLookup func(ctx context.Context, value string, excludeAccountID string) (conflict bool, err error)

// AccountCorrelationInput is everything ResolveAccountCorrelationID needs to resolve one account's correlation
// id.
type AccountCorrelationInput struct {
	// Adapter is the billing provider adapter used to read and, where supported, write the correlation value.
	Adapter Adapter
	// AccountID is billing_account.id for the account being resolved. It is excluded from LocalDuplicate's
	// search so the account's own stored value is never reported as a conflict against itself.
	AccountID string
	// Ref is the provider-native account reference used for adapter reads and writes.
	Ref AccountRef
	// LocalValue is billing_account.correlation_id as currently stored in OSAC for this account; "" when unset.
	LocalValue string
	// LocalDuplicate is mandatory: it backs the local-uniqueness check against billing_account.
	LocalDuplicate LocalCorrelationLookup
}

// Resolution is the outcome of ResolveAccountCorrelationID. The caller persists Value to
// billing_account.correlation_id in its own transaction; ResolveAccountCorrelationID never writes it itself.
type Resolution struct {
	// Value is the correlation value to persist in OSAC. Always set on success.
	Value string
	// ProviderWritten is true only when this call wrote Value to the provider and the write succeeded.
	ProviderWritten bool
	// ProviderWriteSkippedReason is ReasonProviderCorrelationWriteUnsupported when a provider write would have
	// been needed but the adapter does not support it, and "" otherwise (including when no write was needed at
	// all, as when only the provider had a value).
	ProviderWriteSkippedReason string
	// ProviderMatches is how many provider accounts carried Value when this resolution's uniqueness scan
	// measured it, before any provider write this resolution performed. When ProviderWritten is true the
	// current count is ProviderMatches plus one — the write set Value on this account, and the scan already
	// confirmed no other account carries it — so callers can derive the current count without re-listing the
	// provider.
	ProviderMatches int
}

// ResolveAccountCorrelationID applies the day-one correlation-id precedence table (design.md §4.1 step 3) for
// one account: generate, adopt, or report a conflict. It performs provider reads and, where the adapter
// supports it, one provider write; it never writes billing_account.correlation_id itself — the caller commits
// Resolution.Value in its own transaction (OSAC-6884).
//
// Every successful outcome runs the same bounded provider-uniqueness scan, so Resolution.ProviderMatches is
// always measured and an account whose value another provider account also carries fails with
// ACCOUNT_AMBIGUOUS whichever branch produced it.
//
// It is idempotent for the same input: a second call after a successful provider write observes the provider
// now holding the value (adopting it if OSAC has not yet committed, or finding both sides equal if it has) and
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
		// Check provider uniqueness before writing: if another provider account already carries this value
		// (coincidentally, before we ever touch the provider), fail without leaving a write to undo.
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
			// Same reason as the OSAC-only-adopt branch: check uniqueness before writing so a failed check
			// after a successful write cannot leave an orphaned provider write.
			return account.commitValue(ctx, candidate)
		}
	}
	return Resolution{}, &CorrelationResolutionError{Reason: ReasonAccountCorrelationConflict}
}

// checkLocalDuplicate fails with ACCOUNT_CORRELATION_CONFLICT when value is already stored on a different
// local billing_account row. It is only used for adopted values; a generated value's local collision is
// regenerated instead (see generate).
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

// writeIfSupported writes value to the provider when the adapter supports it, and reports the skip reason
// otherwise. It never calls SetCorrelationValue when SupportsCorrelationWrite is false.
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

// countProviderMatches returns how many provider accounts carry value, failing with ACCOUNT_AMBIGUOUS when
// more than one does. The count backs Resolution.ProviderMatches: the caller (OSAC-6884) derives the
// post-write connection state from it instead of re-listing the provider. Whether zero or one match is
// acceptable is a branch decision: the provider-present branches count the assigned account itself, while
// commitValue — reached only when the assigned account does not carry the value — requires zero.
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

// commitValue finishes every write-performing resolution branch: it confirms the candidate is unique among
// provider accounts (failing without a write when it is not), writes it where the adapter supports that, and
// reports the measured pre-write match count together with the write outcome.
func (account AccountCorrelationInput) commitValue(ctx context.Context, candidate string) (Resolution, error) {
	matches, err := account.countProviderMatches(ctx, candidate)
	if err != nil {
		return Resolution{}, err
	}
	// Design §4.1's M360 note requires the assigned account to be the unique carrier. This path runs only
	// when that account has no value, so every pre-write match is foreign and must be rejected before writing.
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
