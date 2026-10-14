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

var ErrInvoiceNotFound = errors.New("billing invoice not found")

// InvoiceQuery selects invoices belonging to one provider account.
type InvoiceQuery struct {
	AccountReference string
	FromDate         string
	ToDate           string
	Offset           int32
	Limit            int32
}

// InvoicePage contains one provider-neutral page of invoice headers.
type InvoicePage struct {
	Items []Invoice
	Total int32
}

// Invoice uses the business invoice number as its OSAC identifier so provider
// record identifiers do not cross the API boundary.
type Invoice struct {
	ID             string
	Number         string
	Date           string
	Status         string
	Type           string
	Amount         string
	TaxAmount      string
	Balance        string
	CurrencyCode   string
	DueDate        string
	PeriodStart    string
	PeriodEnd      string
	PaymentStatus  string
	ApprovalStatus string
	Items          []InvoiceLineItem
}

// InvoiceLineItem is a provider-neutral view of one billed item.
type InvoiceLineItem struct {
	Description  string
	ServiceName  string
	ServiceCode  string
	Volume       string
	RatePerUnit  string
	Amount       string
	TaxAmount    string
	Balance      string
	ChargeType   string
	PriceMethod  string
	PeriodStart  string
	PeriodEnd    string
	CurrencyCode string
}

// InvoiceProvider reads invoices from the configured billing provider.
type InvoiceProvider interface {
	ListInvoices(context.Context, InvoiceQuery) (InvoicePage, error)
	GetInvoice(context.Context, string, string) (Invoice, error)
}
