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

// opaque hides a value in every default rendering: String, GoString (%#v), structured logging and
// JSON. Types holding provider ids or credentials embed it so that adding a type cannot silently
// leave one rendering unredacted.
type opaque struct{}

// String redacts the value in string renderings.
func (opaque) String() string { return redacted }

// GoString redacts the value in %#v renderings.
func (opaque) GoString() string { return redacted }

// LogValue redacts the value in structured logs.
func (opaque) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalJSON redacts the value in JSON, so marshalling an embedding type cannot serialize the
// credential or provider id it holds.
func (opaque) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

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
// ExternalID, which must never reach logs or formatted output. Adapters leave State UNKNOWN until their status
// mapping is implemented; the M360 rule is in the m360 package README.
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

// PriceableAttribute is a provider charge dimension: something the provider meters and prices. Rate rows refer to
// it by ID; the resolver and the APIs refer to it by Name.
type PriceableAttribute struct {
	// ID is the provider's own identifier for the dimension, which the M360 adapter reads from the API's id
	// field. It stays inside the billing boundary: no API response carries it.
	ID string
	// Name is the provider's name for the dimension, matched by exact equality against the OSAC dimension names
	// that key a configuration in ComponentRequest.Values (for example "instance_type"). There is no OSAC-side
	// dimension mapping, so a provider name that differs from OSAC's vocabulary never resolves.
	Name string
}

// AttributeValue is one condition of a rate row: a dimension name and the value a configuration must carry for the
// row to apply, for example {Name: "instance_type", Value: "large-gpu"} for a row that prices only large GPU
// instances.
//
// Both strings are passed through from the provider as received; the M360 adapter copies its attributeName and
// attributeValue unchanged. Resolution compares them by exact equality, Name against the OSAC dimension names that
// key a configuration in ComponentRequest.Values and Value against the configured value, and nothing translates or
// normalizes either one. A provider name that differs from OSAC's vocabulary therefore yields a condition that can
// never match. Aligning the provider's names with OSAC's is the administrator's responsibility, so the pass-through
// is intended, not a translation layer OSAC is missing. Neither field carries an operator: a dimension a row omits is
// unconstrained (ANY), and empty is reserved for ANY, so an empty Value is invalid.
type AttributeValue struct {
	// Name is the provider's dimension name, matched by exact equality as in PriceableAttribute.Name.
	Name string
	// Value is the value a configuration must have for that dimension, compared as a raw string against
	// ComponentRequest.Values: an instance type name such as "large-gpu", a decimal size such as "100", or a
	// storage tier name. It must use the same form the caller supplies, so a provider id only matches when the
	// caller's value is that same id.
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
