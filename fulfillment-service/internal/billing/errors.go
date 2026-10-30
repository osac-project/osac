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

import "fmt"

// Reason is a stable failure code that is safe to log and return to callers.
type Reason string

const (
	ReasonNotConfigured Reason = "NOT_CONFIGURED"
	ReasonUnavailable   Reason = "UNAVAILABLE"
	ReasonTimeout       Reason = "TIMEOUT"
	ReasonUnauthorized  Reason = "UNAUTHORIZED"
	ReasonMalformed     Reason = "MALFORMED"
	ReasonConflict      Reason = "CONFLICT"
	ReasonUnsupported   Reason = "UNSUPPORTED"
	ReasonAmbiguous     Reason = "AMBIGUOUS"
	ReasonNotFound      Reason = "NOT_FOUND" // the addressed provider resource does not exist
)

// Error is a classified adapter failure. It deliberately carries no provider body or native identifier.
type Error struct {
	Reason    Reason
	Op        string
	Retryable bool

	// status is the HTTP status that produced the error, 0 when none; kept private so callers branch on Reason.
	status int
}

// Error renders the classified failure without any provider detail.
func (e *Error) Error() string {
	return fmt.Sprintf("billing %s: %s", e.Op, e.Reason)
}

// NewError builds a classified failure that records the HTTP status which produced it. Provider adapters
// construct their errors with it; callers branch on Reason.
func NewError(reason Reason, op string, retryable bool, status int) *Error {
	return &Error{Reason: reason, Op: op, Retryable: retryable, status: status}
}

// HTTPStatus reports the HTTP status that produced the error, 0 when none. Provider adapters use it to
// reclassify failures; callers should branch on Reason instead.
func (e *Error) HTTPStatus() int { return e.status }

// ErrServiceNotFound reports that no provider service matched the requested name exactly. It is a NOT_FOUND
// *Error, so callers can match it with errors.Is or branch on Reason like any other adapter failure.
var ErrServiceNotFound = &Error{Reason: ReasonNotFound, Op: "FindServiceByName"}

// ErrRateCardNotFound reports that the provider has no rate card for the requested service and currency.
var ErrRateCardNotFound = &Error{Reason: ReasonNotFound, Op: "GetServiceRateCard"}
