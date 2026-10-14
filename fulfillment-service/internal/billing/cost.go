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

// ErrCostHeaderLimitExceeded means the provider has more rated transactions
// than can be safely aggregated for one cost request.
var ErrCostHeaderLimitExceeded = errors.New("billing cost transaction limit exceeded")

// CostQuery selects provider-calculated costs for one account and date range.
type CostQuery struct {
	AccountReference string
	// TenantID is the authorized OSAC tenant requesting costs. It is separate
	// from any tenant attribution returned by the provider.
	TenantID string
	FromDate string
	ToDate   string
}

// Cost is an aggregate for one OSAC service, project, and resource in a period.
// Missing project or resource attribution is represented by a nil field.
type Cost struct {
	// TenantID is provider row attribution. It may be nil when unavailable.
	TenantID     *string
	ProjectID    *string
	ResourceID   *string
	Service      string
	Amount       string
	CurrencyCode string
	PeriodStart  string
	PeriodEnd    string
}

// CostProvider reads provider-calculated costs into OSAC's provider-neutral model.
type CostProvider interface {
	ListCosts(context.Context, CostQuery) ([]Cost, error)
}
