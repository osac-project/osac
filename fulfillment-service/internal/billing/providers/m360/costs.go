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
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

const (
	costDetailConcurrency = 8
	maxCostHeaders        = 500
	maxCostPages          = maxCostHeaders / invoicePageSize
	maxCostRangeDays      = 366
)

var _ billing.CostProvider = (*Client)(nil)

// ListCosts reads rated transaction details and aggregates them by OSAC service,
// project, and resource for the requested account and period. M360 does not
// provide tenant attribution on rated transaction details, so this adapter
// attributes costs to the linked tenant supplied in the query. This relies on
// OSAC's current one-to-one tenant-to-billing-account mapping.
func (c *Client) ListCosts(ctx context.Context, query billing.CostQuery) ([]billing.Cost, error) {
	if strings.TrimSpace(query.AccountReference) == "" {
		return nil, errors.New("M360 cost account reference is required")
	}
	tenantID := strings.TrimSpace(query.TenantID)
	if tenantID == "" {
		return nil, errors.New("M360 cost tenant id is required")
	}
	if err := validateCostDateRange(query.FromDate, query.ToDate); err != nil {
		return nil, errors.New("M360 cost date range is invalid")
	}
	services, err := c.listServices(ctx)
	if err != nil {
		return nil, err
	}
	osacServices := make(map[string]string, len(services))
	for _, service := range services {
		if _, supported := osacRateCardDimensions[service.Code]; supported {
			osacServices[service.ID] = service.Code
		}
	}
	transactions, err := c.listRatedTransactionHeaders(ctx, query)
	if err != nil {
		return nil, err
	}
	selected := make([]m360RatedTransactionHeader, 0, len(transactions))
	for _, transaction := range transactions {
		if _, supported := osacServices[transaction.ServiceID]; !supported {
			continue
		}
		if !validPathSegment(transaction.ID) {
			return nil, errors.New("M360 rated transaction list contained an invalid identifier")
		}
		selected = append(selected, transaction)
	}
	details, err := c.getRatedTransactionDetails(ctx, selected)
	if err != nil {
		return nil, err
	}
	aggregates := make(map[costGroup]*costAggregate)
	for i, detail := range details {
		header := selected[i]
		if detail.ID == "" || detail.ID != header.ID {
			return nil, errors.New("M360 rated transaction response did not match the requested transaction")
		}
		serviceID := detail.ServiceID
		if serviceID == "" {
			serviceID = header.ServiceID
		}
		service := osacServices[serviceID]
		if service == "" {
			return nil, errors.New("M360 rated transaction referenced an unknown service")
		}
		amount := string(detail.Amount)
		if amount == "" || strings.TrimSpace(detail.Currency) == "" {
			return nil, errors.New("M360 rated transaction omitted its amount or currency")
		}
		projectID := firstPresentString(detail.ProjectID, detail.ProjectIDSnake)
		resourceID := firstPresentString(detail.ResourceID, detail.ResourceIDSnake)
		if projectID != nil {
			*projectID = strings.TrimSpace(*projectID)
		}
		if resourceID != nil {
			*resourceID = strings.TrimSpace(*resourceID)
		}
		currency := strings.ToUpper(strings.TrimSpace(detail.Currency))
		group := costGroup{
			service: service, currency: currency,
			projectID: dereferenceString(projectID), projectKnown: projectID != nil,
			resourceID: dereferenceString(resourceID), resourceKnown: resourceID != nil,
		}
		aggregate := aggregates[group]
		if aggregate == nil {
			aggregate = &costAggregate{amount: new(big.Rat)}
			aggregates[group] = aggregate
		}
		if err := aggregate.add(amount); err != nil {
			return nil, errors.New("M360 rated transaction contained an invalid amount")
		}
	}

	result := make([]billing.Cost, 0, len(aggregates))
	for group, aggregate := range aggregates {
		amount, err := aggregate.string()
		if err != nil {
			return nil, errors.New("M360 rated transaction amounts could not be combined")
		}
		result = append(result, billing.Cost{
			TenantID:     &tenantID,
			ProjectID:    groupStringPointer(group.projectID, group.projectKnown),
			ResourceID:   groupStringPointer(group.resourceID, group.resourceKnown),
			Service:      group.service,
			Amount:       amount,
			CurrencyCode: group.currency,
			PeriodStart:  query.FromDate,
			PeriodEnd:    query.ToDate,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i], result[j]
		if left.Service != right.Service {
			return left.Service < right.Service
		}
		if projectKey(left.ProjectID) != projectKey(right.ProjectID) {
			return projectKey(left.ProjectID) < projectKey(right.ProjectID)
		}
		if resourceKey(left.ResourceID) != resourceKey(right.ResourceID) {
			return resourceKey(left.ResourceID) < resourceKey(right.ResourceID)
		}
		return left.CurrencyCode < right.CurrencyCode
	})
	return result, nil
}

func (c *Client) listRatedTransactionHeaders(ctx context.Context, query billing.CostQuery) ([]m360RatedTransactionHeader, error) {
	transactions := make([]m360RatedTransactionHeader, 0, invoicePageSize)
	for page := 0; page < maxCostPages; page++ {
		endpoint := c.apiURL("rated-transactions")
		params := endpoint.Query()
		params.Set("page", strconv.Itoa(page))
		params.Set("size", strconv.Itoa(invoicePageSize))
		params.Set("accountId", query.AccountReference)
		params.Set("fromDate", query.FromDate)
		params.Set("toDate", query.ToDate)
		endpoint.RawQuery = params.Encode()
		var response m360RatedTransactionPage
		if err := c.requestJSON(ctx, http.MethodGet, endpoint, nil, &response); err != nil {
			return nil, err
		}
		if response.Total < 0 || response.TotalPages < 0 {
			return nil, errors.New("M360 rated transaction list contained an invalid total")
		}
		if response.Total > maxCostHeaders || response.TotalPages > maxCostPages {
			return nil, billing.ErrCostHeaderLimitExceeded
		}
		if len(response.Content) > invoicePageSize || len(transactions)+len(response.Content) > maxCostHeaders {
			return nil, billing.ErrCostHeaderLimitExceeded
		}
		if !response.Last && response.TotalPages == 0 {
			return nil, errors.New("M360 rated transaction list omitted its page count")
		}
		transactions = append(transactions, response.Content...)
		if response.Last {
			if response.Total > 0 && len(transactions) != response.Total {
				return nil, errors.New("M360 rated transaction list ended before its declared total")
			}
			return transactions, nil
		}
		if len(response.Content) == 0 || page+1 >= response.TotalPages || (response.Total > 0 && len(transactions) >= response.Total) {
			return nil, errors.New("M360 rated transaction list ended before its last page")
		}
	}
	return nil, billing.ErrCostHeaderLimitExceeded
}

func (c *Client) getRatedTransactionDetails(ctx context.Context,
	transactions []m360RatedTransactionHeader,
) ([]m360RatedTransactionDetail, error) {
	if len(transactions) == 0 {
		return []m360RatedTransactionDetail{}, nil
	}
	details := make([]m360RatedTransactionDetail, len(transactions))
	errorsByIndex := make([]error, len(transactions))
	jobs := make(chan int)
	workers := min(costDetailConcurrency, len(transactions))
	var group sync.WaitGroup
	group.Add(workers)
	for range workers {
		go func() {
			defer group.Done()
			for index := range jobs {
				var detail m360RatedTransactionDetail
				endpoint := c.apiURL("rated-transactions", pathSegment(transactions[index].ID))
				err := c.requestJSON(ctx, http.MethodGet, endpoint, nil, &detail)
				if err == nil {
					details[index] = detail
				} else {
					errorsByIndex[index] = err
				}
			}
		}()
	}
	for index := range transactions {
		jobs <- index
	}
	close(jobs)
	group.Wait()
	for _, err := range errorsByIndex {
		if err != nil {
			return nil, err
		}
	}
	return details, nil
}

func validateCostDateRange(fromDate, toDate string) error {
	const dateFormat = "2006-01-02"
	from, err := time.Parse(dateFormat, fromDate)
	if err != nil {
		return err
	}
	to, err := time.Parse(dateFormat, toDate)
	if err != nil {
		return err
	}
	if from.After(to) {
		return errors.New("from_date must be on or before to_date")
	}
	if to.Sub(from) >= maxCostRangeDays*24*time.Hour {
		return errors.New("cost date range exceeds 366 days")
	}
	return nil
}

type m360RatedTransactionPage struct {
	Content    []m360RatedTransactionHeader `json:"content"`
	Total      int                          `json:"totalElements"`
	TotalPages int                          `json:"totalPages"`
	Last       bool                         `json:"last"`
}

type m360RatedTransactionHeader struct {
	ID        string `json:"Id"`
	ServiceID string `json:"ServiceId"`
}

type m360RatedTransactionDetail struct {
	ID              string      `json:"id"`
	ServiceID       string      `json:"serviceId"`
	Amount          m360Decimal `json:"amount"`
	Currency        string      `json:"currency"`
	ProjectID       *string     `json:"projectId"`
	ProjectIDSnake  *string     `json:"project_id"`
	ResourceID      *string     `json:"resourceId"`
	ResourceIDSnake *string     `json:"resource_id"`
}

type costGroup struct {
	service       string
	currency      string
	projectID     string
	projectKnown  bool
	resourceID    string
	resourceKnown bool
}

type costAggregate struct {
	amount *big.Rat
	scale  int
}

func (a *costAggregate) add(value string) error {
	parsed, ok := new(big.Rat).SetString(value)
	if !ok {
		return errors.New("invalid decimal")
	}
	scale, err := decimalPlaces(value)
	if err != nil {
		return err
	}
	if scale > a.scale {
		a.scale = scale
	}
	a.amount.Add(a.amount, parsed)
	return nil
}

func (a *costAggregate) string() (string, error) {
	result := a.amount.FloatString(a.scale)
	parsed, ok := new(big.Rat).SetString(result)
	if !ok || parsed.Cmp(a.amount) != 0 {
		return "", fmt.Errorf("decimal aggregation lost precision")
	}
	return result, nil
}

func decimalPlaces(value string) (int, error) {
	parts := strings.SplitN(strings.ToLower(value), "e", 2)
	scale := 0
	if dot := strings.IndexByte(parts[0], '.'); dot >= 0 {
		scale = len(parts[0]) - dot - 1
	}
	if len(parts) == 2 {
		exponent, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0, err
		}
		scale -= exponent
	}
	if scale < 0 {
		scale = 0
	}
	if scale > 18 {
		return 0, errors.New("decimal precision exceeds limit")
	}
	return scale, nil
}

func firstPresentString(values ...*string) *string {
	for _, value := range values {
		if value != nil {
			copy := *value
			return &copy
		}
	}
	return nil
}

func dereferenceString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func groupStringPointer(value string, present bool) *string {
	if !present {
		return nil
	}
	return &value
}

func projectKey(value *string) string {
	if value == nil {
		return "\x00"
	}
	return "\x01" + *value
}

func resourceKey(value *string) string {
	if value == nil {
		return "\x00"
	}
	return "\x01" + *value
}
