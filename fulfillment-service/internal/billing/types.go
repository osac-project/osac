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

import "log/slog"

const redacted = "[redacted]"

// opaque hides a value in every default rendering: String, GoString (%#v) and structured logging. Types holding
// provider ids or credentials embed it so that adding a type cannot silently leave one rendering unredacted.
type opaque struct{}

// String redacts the value in string renderings.
func (opaque) String() string { return redacted }

// GoString redacts the value in %#v renderings.
func (opaque) GoString() string { return redacted }

// LogValue redacts the value in structured logs.
func (opaque) LogValue() slog.Value { return slog.StringValue(redacted) }

// AccountRef identifies a provider account. The native id is only reachable through ID so that accidental
// formatting or logging cannot leak it.
type AccountRef struct {
	opaque
	id string
}

// NewAccountRef wraps a provider account id.
func NewAccountRef(id string) AccountRef { return AccountRef{id: id} }

// ID returns the native provider id.
func (r AccountRef) ID() string { return r.id }

// ServiceRef identifies a provider service. It follows the same redaction rules as AccountRef.
type ServiceRef struct {
	opaque
	id string
}

// NewServiceRef wraps a provider service id.
func NewServiceRef(id string) ServiceRef { return ServiceRef{id: id} }

// ID returns the native provider id.
func (r ServiceRef) ID() string { return r.id }

type AccountState string

const (
	AccountActive   AccountState = "ACTIVE"
	AccountInactive AccountState = "INACTIVE"
	AccountUnknown  AccountState = "UNKNOWN"
)

// Account keeps the raw provider status values next to the normalized state. Its correlation value is
// ExternalID, which must never reach logs or formatted output. Adapters leave State UNKNOWN until the status
// mapping rule is defined.
type Account struct {
	opaque
	Ref            AccountRef
	Name           string
	ExternalID     string
	State          AccountState
	RawStatus      string
	ApprovalStatus string
}

// AccountFilter narrows ListAccounts to exact matches; zero fields are ignored. When both are set an account must
// match both.
type AccountFilter struct {
	ExternalID string
	Name       string
}

type Page struct {
	Token string
	Size  int
}

// AccountPage.Next is empty on the last page.
type AccountPage struct {
	Accounts []Account
	Next     string
}

// PriceableAttribute is a provider charge dimension. Rate rows reference it by ID.
type PriceableAttribute struct {
	ID   string
	Name string
}

type AttributeValue struct {
	Name  string
	Value string
}

// RateRow is one row of a rate card as the provider sent it. PriceMethod is the provider's raw value: callers
// decide which methods they support.
type RateRow struct {
	DimensionID     string // ID of the PriceableAttribute the row prices
	PriceMethod     string
	UnitRate        string // decimal text exactly as received; empty when the provider sends none
	AttributeValues []AttributeValue
}

type RateCard struct {
	Currency string
	Rows     []RateRow
}
