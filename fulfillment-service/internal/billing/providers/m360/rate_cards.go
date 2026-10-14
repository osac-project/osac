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

package m360

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

var _ billing.RateCardProvider = (*Client)(nil)

var ErrUnsupportedRateCard = errors.New("M360 rate card contains an unsupported row")
var errM360NotFound = errors.New("M360 resource not found")

var osacRateCardDimensions = map[string]map[string]struct{}{
	billing.ServiceComputeInstance: {
		"image_ref": {}, "instance_type": {}, "boot_disk_size_gib": {}, "storage_tier": {},
		"additional_disk_size_gib": {}, "additional_storage_tier": {},
	},
	billing.ServiceCluster: {
		"cluster_template": {}, "release_image": {}, "baremetal_instance_type": {}, "node_count": {},
	},
	billing.ServiceBareMetalInstance: {
		"catalog_item": {}, "bm_instance_type": {}, "image_ref": {},
	},
}

var m360DimensionAliases = map[string]map[string]string{
	billing.ServiceComputeInstance: {
		"vm_image_ref":         "image_ref",
		"boot_disk_size":       "boot_disk_size_gib",
		"additional_disk_size": "additional_disk_size_gib",
	},
	billing.ServiceCluster: {
		"bare_metal_instance_type": "baremetal_instance_type",
	},
	billing.ServiceBareMetalInstance: {
		"baremetal_instance_type": "bm_instance_type",
		"vm_image_ref":            "image_ref",
	},
}

// ListRateCardItems returns current service rates whose M360 service code
// matches the requested OSAC service. With an account reference, it first
// resolves an active account contract and uses that contract's service pricing.
func (c *Client) ListRateCardItems(ctx context.Context, query billing.RateCardQuery) ([]billing.RateCardItem, error) {
	services, err := c.listServices(ctx)
	if err != nil {
		return nil, err
	}
	currency := strings.ToUpper(strings.TrimSpace(query.CurrencyCode))
	if query.AccountReference != "" && currency == "" {
		account, accountErr := c.GetAccount(ctx, query.AccountReference)
		if accountErr != nil {
			return nil, accountErr
		}
		currency = strings.ToUpper(strings.TrimSpace(account.Currency))
		if currency == "" {
			return nil, errors.New("M360 account does not have a base currency")
		}
	}
	var contract *m360Contract
	now := time.Now().UTC()
	if query.AccountReference != "" {
		contract, err = c.findApplicableContract(ctx, query.AccountReference, now)
		if err != nil {
			return nil, err
		}
	}
	items := make([]billing.RateCardItem, 0)
	for _, service := range services {
		if query.Service != "" && service.Code != query.Service {
			continue
		}
		if _, supported := osacRateCardDimensions[service.Code]; !supported {
			continue
		}
		if !validPathSegment(service.ID) {
			return nil, errors.New("M360 service response contained an invalid ID")
		}
		var details m360Service
		if err = c.requestJSON(ctx, http.MethodGet, c.apiURL("services", pathSegment(service.ID)), nil, &details); err != nil {
			return nil, err
		}
		if details.ID == "" || details.Code == "" {
			return nil, errors.New("M360 service response did not contain an ID and code")
		}
		source := billing.RateCardSourceDefault
		cards := details.RateCards
		if contract != nil {
			contractService, found, serviceErr := c.findContractService(ctx, contract, details.ID, now)
			if serviceErr != nil {
				return nil, serviceErr
			}
			if !found {
				continue
			}
			source = billing.RateCardSourceContract
			if contractService.ExceptionPricing {
				exceptionCard, foundCard, cardErr := c.getContractRateCard(ctx, contract.ID, contractService.ID, currency)
				if cardErr != nil {
					return nil, cardErr
				}
				if foundCard {
					cards = []m360RateCard{exceptionCard}
				} else {
					// An exception-priced contract with no rate card for this
					// currency is uncovered. Do not substitute the public catalog
					// price for a contract price.
					cards = nil
				}
			}
		}
		for _, card := range cards {
			if !card.IsCurrent || !matchesCurrency(currency, card.Currency) {
				continue
			}
			for _, row := range card.Rows {
				item, itemErr := normalizeRateCardItem(details.Code, card.Currency, source, row)
				if itemErr != nil {
					return nil, itemErr
				}
				items = append(items, item)
			}
		}
	}
	return items, nil
}

func (c *Client) listServices(ctx context.Context) ([]m360ServiceSummary, error) {
	services := make([]m360ServiceSummary, 0)
	for page := 0; ; page++ {
		endpoint := c.apiURL("services")
		query := endpoint.Query()
		query.Set("page", fmt.Sprint(page))
		query.Set("size", fmt.Sprint(accountPageSize))
		endpoint.RawQuery = query.Encode()
		var response m360ServicePage
		if err := c.requestJSON(ctx, http.MethodGet, endpoint, nil, &response); err != nil {
			return nil, err
		}
		for _, service := range response.Content {
			if service.ID == "" || service.Code == "" {
				return nil, errors.New("M360 service list contained a service without an ID or code")
			}
			services = append(services, service)
		}
		if response.Last || len(response.Content) == 0 || (response.TotalPages > 0 && page+1 >= response.TotalPages) {
			return services, nil
		}
	}
}

func (c *Client) findApplicableContract(ctx context.Context, accountReference string, now time.Time) (*m360Contract, error) {
	contracts := make([]m360Contract, 0)
	for page := 0; ; page++ {
		endpoint := c.apiURL("contracts")
		query := endpoint.Query()
		query.Set("page", fmt.Sprint(page))
		query.Set("size", fmt.Sprint(accountPageSize))
		endpoint.RawQuery = query.Encode()
		var response m360ContractPage
		if err := c.requestJSON(ctx, http.MethodGet, endpoint, nil, &response); err != nil {
			return nil, err
		}
		for _, contract := range response.Content {
			if contract.accountReference() != accountReference || !contractApproved(contract) {
				continue
			}
			active, err := contractActive(contract.startDate(), contract.endDate(), now)
			if err != nil {
				return nil, err
			}
			if active {
				contracts = append(contracts, contract)
			}
		}
		if response.Last {
			break
		}
		if len(response.Content) == 0 {
			return nil, errors.New("M360 contract list pagination ended before completion")
		}
		if response.TotalPages <= 0 {
			return nil, errors.New("M360 contract list response did not report pagination completion")
		}
		if page+1 >= response.TotalPages {
			break
		}
	}
	if len(contracts) > 1 {
		return nil, errors.New("M360 returned multiple applicable account contracts")
	}
	if len(contracts) == 0 {
		return nil, nil
	}
	if !validPathSegment(contracts[0].id()) {
		return nil, errors.New("M360 contract response contained an invalid ID")
	}
	contracts[0].ID = contracts[0].id()
	return &contracts[0], nil
}

func (c *Client) findContractService(ctx context.Context, contract *m360Contract, serviceID string, now time.Time) (m360ContractService, bool, error) {
	if !validPathSegment(contract.ID) || !validPathSegment(serviceID) {
		return m360ContractService{}, false, errors.New("M360 contract response contained an invalid ID")
	}
	services := make([]m360ContractService, 0)
	for page := 0; ; page++ {
		endpoint := c.apiURL("contracts", contract.ID, "services")
		query := endpoint.Query()
		query.Set("page", fmt.Sprint(page))
		query.Set("size", fmt.Sprint(accountPageSize))
		endpoint.RawQuery = query.Encode()
		var response m360ContractServicePage
		if err := c.requestJSON(ctx, http.MethodGet, endpoint, nil, &response); err != nil {
			return m360ContractService{}, false, err
		}
		for _, service := range response.Content {
			if service.ServiceID != serviceID {
				continue
			}
			active, err := contractActive(service.StartDate, service.EndDate, now)
			if err != nil {
				return m360ContractService{}, false, err
			}
			if active {
				services = append(services, service)
			}
		}
		if response.Last || len(response.Content) == 0 || (response.TotalPages > 0 && page+1 >= response.TotalPages) {
			break
		}
	}
	if len(services) > 1 {
		return m360ContractService{}, false, errors.New("M360 returned multiple applicable contract services")
	}
	if len(services) == 0 {
		return m360ContractService{}, false, nil
	}
	if !validPathSegment(services[0].ID) {
		return m360ContractService{}, false, errors.New("M360 contract service response contained an invalid ID")
	}
	return services[0], true, nil
}

func (c *Client) getContractRateCard(ctx context.Context, contractID, serviceID, currency string) (m360RateCard, bool, error) {
	if !validPathSegment(contractID) || !validPathSegment(serviceID) || !validCurrency(currency) {
		return m360RateCard{}, false, errors.New("M360 contract rate-card lookup parameters are invalid")
	}
	endpoint := c.apiURL("contracts", contractID, "services", serviceID, "rate-cards", currency)
	var response m360RateCard
	if err := c.requestJSON(ctx, http.MethodGet, endpoint, nil, &response); err != nil {
		if errors.Is(err, errM360NotFound) {
			return m360RateCard{}, false, nil
		}
		return m360RateCard{}, false, err
	}
	if response.Currency == "" {
		return m360RateCard{}, false, errors.New("M360 contract rate-card response did not contain a currency")
	}
	return response, true, nil
}

func contractApproved(contract m360Contract) bool {
	status := contract.ApprovalStatus
	if status == "" {
		status = contract.ApprovalStatusReference.Name
	}
	return strings.EqualFold(strings.TrimSpace(status), "approved")
}

func contractActive(startDate, endDate string, now time.Time) (bool, error) {
	if startDate != "" {
		start, err := time.Parse("2006-01-02", startDate)
		if err != nil {
			return false, errors.New("M360 contract contained an invalid start date")
		}
		if now.Before(start) {
			return false, nil
		}
	}
	if endDate != "" {
		end, err := time.Parse("2006-01-02", endDate)
		if err != nil {
			return false, errors.New("M360 contract contained an invalid end date")
		}
		if now.After(end.Add(24*time.Hour - time.Nanosecond)) {
			return false, nil
		}
	}
	return true, nil
}

func validPathSegment(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("-_.~", r) {
			return false
		}
	}
	return true
}

func validCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, r := range value {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func normalizeRateCardItem(service, currency string, source billing.RateCardSource, row m360RateCardRow) (billing.RateCardItem, error) {
	var amount json.Number
	var unit billing.RateUnit
	switch strings.ToUpper(strings.TrimSpace(row.PriceMethod)) {
	case "FLAT_RATE":
		amount, unit = row.FlatRate, billing.RateUnitMonth
	case "QUANTITY":
		amount, unit = row.UnitRate, billing.RateUnitHour
	default:
		return billing.RateCardItem{}, ErrUnsupportedRateCard
	}
	if amount == "" || strings.TrimSpace(amount.String()) == "" {
		return billing.RateCardItem{}, ErrUnsupportedRateCard
	}
	conditions := make([]billing.RateCondition, 0, len(row.AttributeValues))
	quantityDimension := ""
	for _, condition := range row.AttributeValues {
		if strings.TrimSpace(condition.AttributeName) == "" || strings.TrimSpace(condition.AttributeValue) == "" {
			return billing.RateCardItem{}, ErrUnsupportedRateCard
		}
		dimension, ok := normalizeM360Dimension(service, condition.AttributeName)
		if !ok {
			return billing.RateCardItem{}, ErrUnsupportedRateCard
		}
		if strings.EqualFold(strings.TrimSpace(row.PriceMethod), "QUANTITY") && isM360QuantityDimension(dimension) {
			if quantityDimension != "" && quantityDimension != dimension {
				return billing.RateCardItem{}, ErrUnsupportedRateCard
			}
			quantityDimension = dimension
		}
		operator, value := billing.RateMatchEquals, condition.AttributeValue
		if value == "*" {
			operator, value = billing.RateMatchAny, ""
		}
		conditions = append(conditions, billing.RateCondition{
			Dimension: dimension,
			Operator:  operator,
			Value:     value,
		})
	}
	return billing.RateCardItem{
		Service:           service,
		CurrencyCode:      currency,
		Source:            source,
		Conditions:        conditions,
		Amount:            amount.String(),
		Unit:              unit,
		QuantityDimension: quantityDimension,
	}, nil
}

func isM360QuantityDimension(dimension string) bool {
	switch dimension {
	case "node_count", "boot_disk_size_gib", "additional_disk_size_gib":
		return true
	default:
		return false
	}
}

func normalizeM360Dimension(service, dimension string) (string, bool) {
	dimension = strings.TrimSpace(dimension)
	if canonical, ok := m360DimensionAliases[service][dimension]; ok {
		return canonical, true
	}
	_, ok := osacRateCardDimensions[service][dimension]
	return dimension, ok
}

func matchesCurrency(requested, actual string) bool {
	return requested == "" || strings.EqualFold(strings.TrimSpace(requested), strings.TrimSpace(actual))
}

func pathSegment(value string) string {
	return strings.TrimSpace(value)
}

type m360ServicePage struct {
	Content    []m360ServiceSummary `json:"content"`
	TotalPages int                  `json:"totalPages"`
	Last       bool                 `json:"last"`
}

type m360ServiceSummary struct {
	ID   string `json:"Id"`
	Code string `json:"Code"`
}

type m360Service struct {
	ID        string         `json:"id"`
	Code      string         `json:"code"`
	RateCards []m360RateCard `json:"rateCards"`
}

type m360RateCard struct {
	Currency  string            `json:"currency"`
	IsCurrent bool              `json:"isCurrent"`
	VersionNo int               `json:"versionNo"`
	Rows      []m360RateCardRow `json:"rows"`
}

type m360RateCardRow struct {
	PriceMethod     string              `json:"priceMethod"`
	FlatRate        json.Number         `json:"flatRate"`
	UnitRate        json.Number         `json:"unitRate"`
	AttributeValues []m360RateAttribute `json:"attributeValues"`
}

type m360RateAttribute struct {
	AttributeName  string `json:"attributeName"`
	AttributeValue string `json:"attributeValue"`
}

type m360ContractPage struct {
	Content    []m360Contract `json:"content"`
	TotalPages int            `json:"totalPages"`
	Last       bool           `json:"last"`
}

type m360Contract struct {
	ID                      string             `json:"Id"`
	IDLower                 string             `json:"id"`
	MasterAccountID         string             `json:"MasterAccountId"`
	MasterAccountIDLower    string             `json:"masterAccountId"`
	ApprovalStatus          string             `json:"approvalStatus"`
	ApprovalStatusReference m360NamedReference `json:"ApprovalStatus__r"`
	StartDate               string             `json:"StartDate"`
	StartDateLower          string             `json:"startDate"`
	EndDate                 string             `json:"EndDate"`
	EndDateLower            string             `json:"endDate"`
}

func (c m360Contract) id() string {
	if c.ID != "" {
		return c.ID
	}
	return c.IDLower
}

func (c m360Contract) accountReference() string {
	if c.MasterAccountID != "" {
		return c.MasterAccountID
	}
	return c.MasterAccountIDLower
}

func (c m360Contract) startDate() string {
	if c.StartDate != "" {
		return c.StartDate
	}
	return c.StartDateLower
}

func (c m360Contract) endDate() string {
	if c.EndDate != "" {
		return c.EndDate
	}
	return c.EndDateLower
}

type m360NamedReference struct {
	Name string `json:"Name"`
}

type m360ContractServicePage struct {
	Content    []m360ContractService `json:"content"`
	TotalPages int                   `json:"totalPages"`
	Last       bool                  `json:"last"`
}

type m360ContractService struct {
	ID               string `json:"id"`
	ServiceID        string `json:"serviceId"`
	StartDate        string `json:"startDate"`
	EndDate          string `json:"endDate"`
	ExceptionPricing bool   `json:"exceptionPricing"`
}
