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

import "context"

// Adapter is the only boundary through which OSAC talks to a billing provider. It exposes normalized values only.
type Adapter interface {
	// ListAccounts pages through accounts that exactly match filter. A page may hold fewer accounts than its size,
	// or none, while Next is still set, so callers must keep paging until Next is empty.
	ListAccounts(ctx context.Context, filter AccountFilter, page Page) (AccountPage, error)
	// GetAccount and CorrelationValue fail with a NOT_FOUND *Error when the account does not exist.
	GetAccount(ctx context.Context, ref AccountRef) (Account, error)

	// CorrelationValue returns the provider-side correlation value of an account; present is false when the
	// account has none.
	CorrelationValue(ctx context.Context, ref AccountRef) (value string, present bool, err error)

	// FindServiceByName returns the single service whose name matches exactly, ErrServiceNotFound when none
	// does, and an AMBIGUOUS *Error when several do.
	FindServiceByName(ctx context.Context, name string) (ServiceRef, error)
	// ListPriceableAttributes returns every priceable attribute of the service. Names must equal OSAC dimension
	// names exactly; rate rows reference an attribute by ID.
	ListPriceableAttributes(ctx context.Context, svc ServiceRef) ([]PriceableAttribute, error)
	GetServiceRateCard(ctx context.Context, svc ServiceRef, currency string) (RateCard, error)
	HealthCheck(ctx context.Context) error

	// SupportsCorrelationWrite reports whether SetCorrelationValue is implemented by the provider.
	SupportsCorrelationWrite() bool

	// SetCorrelationValue fails with an UNSUPPORTED *Error when SupportsCorrelationWrite is false.
	SetCorrelationValue(ctx context.Context, ref AccountRef, value string) error
}
