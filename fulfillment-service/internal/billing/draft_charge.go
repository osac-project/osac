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

import "context"

// DraftChargeQuery selects draft charges for one account and billing period.
type DraftChargeQuery struct {
	AccountReference string
	FromDate         string
	ToDate           string
	Offset           int32
	Limit            int32
}

// DraftCharge is a provider-neutral draft charge row.
type DraftCharge struct {
	ID           string
	Description  string
	Service      string
	ProjectID    *string
	ResourceID   *string
	Amount       string
	CurrencyCode string
	PeriodStart  string
	PeriodEnd    string
}

// DraftChargePage contains one provider-neutral page of draft charges.
type DraftChargePage struct {
	Items []DraftCharge
	Total int32
}

// DraftChargeProvider reads draft charges from a provider that supports them.
type DraftChargeProvider interface {
	ListDraftCharges(context.Context, DraftChargeQuery) (DraftChargePage, error)
}
