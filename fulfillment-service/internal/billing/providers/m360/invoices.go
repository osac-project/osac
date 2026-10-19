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
	"regexp"
	"strconv"
	"strings"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

const invoicePageSize = 20

const maxInvoiceTotal = 1<<31 - 1

var (
	_                   billing.InvoiceProvider = (*Client)(nil)
	validDecimalPattern                         = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)
)

// ListInvoices returns provider-neutral invoice headers for one provider account.
func (c *Client) ListInvoices(ctx context.Context, query billing.InvoiceQuery) (billing.InvoicePage, error) {
	if strings.TrimSpace(query.AccountReference) == "" {
		return billing.InvoicePage{}, errors.New("M360 invoice account reference is required")
	}
	if query.Offset < 0 {
		return billing.InvoicePage{}, errors.New("M360 invoice offset cannot be negative")
	}
	limit := query.Limit
	if limit <= 0 {
		limit = invoicePageSize
	}

	page := int(query.Offset / invoicePageSize)
	skip := int(query.Offset % invoicePageSize)
	remaining := int(limit)
	result := billing.InvoicePage{Items: make([]billing.Invoice, 0, limit)}
	for remaining > 0 {
		response, err := c.listInvoiceHeaders(ctx, query.AccountReference, query.FromDate, query.ToDate, "", page)
		if err != nil {
			return billing.InvoicePage{}, err
		}
		if response.Total < 0 || response.Total > maxInvoiceTotal {
			return billing.InvoicePage{}, errors.New("M360 invoice list contained an invalid total")
		}
		result.Total = int32(response.Total)
		if skip < len(response.Content) {
			for _, invoice := range response.Content[skip:] {
				if invoice.ID == "" || invoice.Number == "" {
					return billing.InvoicePage{}, errors.New("M360 invoice list contained an incomplete invoice")
				}
				result.Items = append(result.Items, normalizeInvoiceHeader(invoice))
				remaining--
				if remaining == 0 {
					break
				}
			}
		}
		if response.Last || len(response.Content) == 0 || remaining == 0 {
			break
		}
		page++
		skip = 0
	}
	return result, nil
}

// GetInvoice resolves the public invoice number to M360's private record ID,
// then retrieves the itemized invoice using that provider ID internally.
func (c *Client) GetInvoice(ctx context.Context, accountReference, invoiceNumber string) (billing.Invoice, error) {
	if strings.TrimSpace(accountReference) == "" {
		return billing.Invoice{}, errors.New("M360 invoice account reference is required")
	}
	if strings.TrimSpace(invoiceNumber) == "" {
		return billing.Invoice{}, errors.New("invoice number is required")
	}
	response, err := c.listInvoiceHeaders(ctx, accountReference, "", "", invoiceNumber, 0)
	if err != nil {
		return billing.Invoice{}, err
	}
	var providerID string
	for _, invoice := range response.Content {
		if invoice.Number != invoiceNumber {
			continue
		}
		if providerID != "" {
			return billing.Invoice{}, errors.New("M360 invoice lookup was ambiguous")
		}
		providerID = invoice.ID
	}
	if providerID == "" {
		return billing.Invoice{}, fmt.Errorf("%w: M360 invoice not found", errM360NotFound)
	}
	if !validPathSegment(providerID) {
		return billing.Invoice{}, errors.New("M360 invoice response contained an invalid identifier")
	}
	var detail m360InvoiceDetail
	if err := c.requestJSON(ctx, http.MethodGet, c.apiURL("invoices", pathSegment(providerID)), nil, &detail); err != nil {
		if errors.Is(err, errM360NotFound) {
			return billing.Invoice{}, fmt.Errorf("%w: M360 invoice not found", billing.ErrInvoiceNotFound)
		}
		return billing.Invoice{}, err
	}
	if detail.Number == "" || detail.ID == "" {
		return billing.Invoice{}, errors.New("M360 invoice response did not contain an ID and invoice number")
	}
	return normalizeInvoiceDetail(detail), nil
}

func (c *Client) listInvoiceHeaders(
	ctx context.Context,
	accountReference, fromDate, toDate, search string,
	page int,
) (m360InvoicePage, error) {
	endpoint := c.apiURL("invoices")
	query := endpoint.Query()
	query.Set("page", strconv.Itoa(page))
	query.Set("size", strconv.Itoa(invoicePageSize))
	query.Set("accountId", accountReference)
	if fromDate != "" {
		query.Set("fromDate", fromDate)
	}
	if toDate != "" {
		query.Set("toDate", toDate)
	}
	if search != "" {
		query.Set("q", search)
	}
	endpoint.RawQuery = query.Encode()
	var response m360InvoicePage
	if err := c.requestJSON(ctx, http.MethodGet, endpoint, nil, &response); err != nil {
		return m360InvoicePage{}, err
	}
	return response, nil
}

func normalizeInvoiceHeader(invoice m360InvoiceHeader) billing.Invoice {
	return billing.Invoice{
		ID:             invoice.Number,
		Number:         invoice.Number,
		ApprovalStatus: invoice.ApprovalStatus.Name,
	}
}

func normalizeInvoiceDetail(invoice m360InvoiceDetail) billing.Invoice {
	items := make([]billing.InvoiceLineItem, 0, len(invoice.Items))
	for _, item := range invoice.Items {
		items = append(items, billing.InvoiceLineItem{
			Description:  item.Description,
			ServiceName:  item.ServiceName,
			ServiceCode:  item.ServiceCode,
			Volume:       string(item.Volume),
			RatePerUnit:  string(item.RatePerUnit),
			Amount:       string(item.Amount),
			TaxAmount:    string(item.TaxAmount),
			Balance:      string(item.Balance),
			ChargeType:   item.ChargeType,
			PriceMethod:  item.PriceMethod,
			PeriodStart:  item.PeriodStart,
			PeriodEnd:    item.PeriodEnd,
			CurrencyCode: item.Currency,
		})
	}
	return billing.Invoice{
		ID:             invoice.Number,
		Number:         invoice.Number,
		Date:           invoice.Date,
		Status:         invoice.Status,
		Type:           invoice.Type,
		Amount:         string(invoice.Amount),
		TaxAmount:      string(invoice.TaxAmount),
		Balance:        string(invoice.Balance),
		CurrencyCode:   invoice.Currency,
		DueDate:        invoice.DueDate,
		PeriodStart:    invoice.PeriodStart,
		PeriodEnd:      invoice.PeriodEnd,
		PaymentStatus:  invoice.PaymentStatus,
		ApprovalStatus: invoice.ApprovalStatus,
		Items:          items,
	}
}

type m360InvoicePage struct {
	Content []m360InvoiceHeader `json:"content"`
	Total   int                 `json:"totalElements"`
	Last    bool                `json:"last"`
}

type m360InvoiceHeader struct {
	ID             string `json:"Id"`
	Number         string `json:"InvoiceNo"`
	ApprovalStatus struct {
		Name string `json:"Name"`
	} `json:"ApprovalStatus__r"`
}

type m360InvoiceDetail struct {
	ID             string                  `json:"id"`
	Number         string                  `json:"invoiceNo"`
	Date           string                  `json:"invoiceDate"`
	Status         string                  `json:"invoiceStatus"`
	Type           string                  `json:"invoiceType"`
	Amount         m360Decimal             `json:"amount"`
	TaxAmount      m360Decimal             `json:"taxAmount"`
	Balance        m360Decimal             `json:"balance"`
	Currency       string                  `json:"currency"`
	DueDate        string                  `json:"dueDate"`
	PeriodStart    string                  `json:"periodStart"`
	PeriodEnd      string                  `json:"periodEnd"`
	PaymentStatus  string                  `json:"paymentStatus"`
	ApprovalStatus string                  `json:"approvalStatus"`
	Items          []m360InvoiceLineDetail `json:"items"`
}

type m360InvoiceLineDetail struct {
	Description string      `json:"description"`
	ServiceName string      `json:"serviceName"`
	ServiceCode string      `json:"serviceCode"`
	Volume      m360Decimal `json:"volume"`
	RatePerUnit m360Decimal `json:"ratePerUnit"`
	Amount      m360Decimal `json:"amount"`
	TaxAmount   m360Decimal `json:"tax"`
	Balance     m360Decimal `json:"balance"`
	ChargeType  string      `json:"chargeType"`
	PriceMethod string      `json:"priceMethodType"`
	PeriodStart string      `json:"billStartDate"`
	PeriodEnd   string      `json:"billEndDate"`
	Currency    string      `json:"currency"`
}

// m360Decimal preserves decimal strings and JSON number precision.
type m360Decimal string

func (d *m360Decimal) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*d = ""
		return nil
	}
	var value string
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &value); err != nil {
			return errors.New("M360 invoice contained an invalid decimal")
		}
	} else {
		value = string(data)
	}
	if value != "" && !validDecimalPattern.MatchString(value) {
		return errors.New("M360 invoice contained an invalid decimal")
	}
	*d = m360Decimal(value)
	return nil
}
