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
	"encoding/json"
	"net/url"
	"strings"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

// M360 wire shapes for services live here so a change in the provider contract touches one place.
type service struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

type priceableAttribute struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// rateCard is not in the ApiResponse envelope: the card sits at the top level.
type rateCard struct {
	Currency string `json:"currency"`
	Rows     []struct {
		PriceDimID      string      `json:"priceDimId"`
		PriceMethod     string      `json:"priceMethod"`
		UnitRate        json.Number `json:"unitRate"` // keeps the provider's text, including exponent form
		AttributeValues []struct {
			Name  string `json:"attributeName"`
			Value string `json:"attributeValue"`
		} `json:"attributeValues"`
	} `json:"rows"`
}

// FindServiceByName implements billing.Adapter.
func (a *adapter) FindServiceByName(ctx context.Context, name string) (billing.ServiceRef, error) {
	const op = "FindServiceByName"
	services, err := getAllPages[service](ctx, a.client, op, "/services", url.Values{"q": {name}})
	if err != nil {
		return billing.ServiceRef{}, err
	}
	var matches []service
	for _, item := range services {
		if item.Name == name {
			matches = append(matches, item)
		}
	}
	switch len(matches) {
	case 0:
		return billing.ServiceRef{}, billing.ErrServiceNotFound
	case 1:
		if matches[0].ID == "" {
			return billing.ServiceRef{}, &billing.Error{Reason: billing.ReasonMalformed, Op: op}
		}
		return billing.NewServiceRef(matches[0].ID), nil
	default:
		return billing.ServiceRef{}, &billing.Error{Reason: billing.ReasonAmbiguous, Op: op}
	}
}

// ListPriceableAttributes implements billing.Adapter.
func (a *adapter) ListPriceableAttributes(ctx context.Context, svc billing.ServiceRef) ([]billing.PriceableAttribute, error) {
	const op = "ListPriceableAttributes"
	items, err := getAllPages[priceableAttribute](ctx, a.client, op, "/services/"+url.PathEscape(svc.ID())+"/priceable-attributes", nil)
	if err != nil {
		return nil, notFoundOn404(op, err)
	}
	attrs := make([]billing.PriceableAttribute, 0, len(items))
	for _, item := range items {
		if item.ID == "" {
			return nil, &billing.Error{Reason: billing.ReasonMalformed, Op: op}
		}
		attrs = append(attrs, billing.PriceableAttribute{ID: item.ID, Name: item.Name})
	}
	return attrs, nil
}

// GetServiceRateCard implements billing.Adapter.
func (a *adapter) GetServiceRateCard(ctx context.Context, svc billing.ServiceRef, currency string) (billing.RateCard, error) {
	const op = "GetServiceRateCard"
	if currency == "" {
		return billing.RateCard{}, &billing.Error{Reason: billing.ReasonMalformed, Op: op}
	}
	var raw rateCard
	path := "/services/" + url.PathEscape(svc.ID()) + "/rate-cards/" + url.PathEscape(currency)
	if err := a.client.get(ctx, op, path, nil, &raw); err != nil {
		if isNotFound(err) {
			return billing.RateCard{}, billing.ErrRateCardNotFound
		}
		return billing.RateCard{}, err
	}
	if raw.Currency != "" && !strings.EqualFold(raw.Currency, currency) {
		return billing.RateCard{}, &billing.Error{Reason: billing.ReasonMalformed, Op: op}
	}
	if len(raw.Rows) == 0 {
		return billing.RateCard{}, billing.ErrRateCardNotFound
	}
	card := billing.RateCard{Currency: currency, Rows: make([]billing.RateRow, 0, len(raw.Rows))}
	for _, row := range raw.Rows {
		if row.PriceDimID == "" || row.PriceMethod == "" {
			return billing.RateCard{}, &billing.Error{Reason: billing.ReasonMalformed, Op: op}
		}
		out := billing.RateRow{DimensionID: row.PriceDimID, PriceMethod: row.PriceMethod, UnitRate: row.UnitRate.String()}
		for _, v := range row.AttributeValues {
			out.AttributeValues = append(out.AttributeValues, billing.AttributeValue{Name: v.Name, Value: v.Value})
		}
		card.Rows = append(card.Rows, out)
	}
	return card, nil
}
