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
	"time"
)

var ErrBillingExportNotFound = errors.New("billing export not found")

// BillingExportState is the normalized state of a provider export operation.
type BillingExportState int

const (
	BillingExportStateUnknown BillingExportState = iota
	BillingExportStatePending
	BillingExportStateRunning
	BillingExportStateSucceeded
	BillingExportStateFailed
)

// BillingExportRequest starts an idempotent export for a provider account.
type BillingExportRequest struct {
	AccountReference string
	TenantID         string
	FromDate         string
	ToDate           string
	IdempotencyKey   string
}

// BillingExport is the provider-neutral state of an asynchronous export.
// ID must be opaque to OSAC clients and stable for subsequent status reads.
type BillingExport struct {
	ID          string
	FromDate    string
	ToDate      string
	State       BillingExportState
	CreatedAt   time.Time
	CompletedAt *time.Time
}

// BillingExportProvider starts and reads charge exports when supported.
type BillingExportProvider interface {
	CreateBillingExport(context.Context, BillingExportRequest) (BillingExport, error)
	GetBillingExport(context.Context, string, string) (BillingExport, error)
}
