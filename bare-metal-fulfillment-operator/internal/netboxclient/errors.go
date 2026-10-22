/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package netboxclient

import (
	"context"
	"net/http"
	"strconv"
)

// ErrorKind identifies a sanitized NetBox API failure.
type ErrorKind string

const (
	ErrorKindInvalidRequest  ErrorKind = "invalid_request"
	ErrorKindUnauthorized    ErrorKind = "unauthorized"
	ErrorKindForbidden       ErrorKind = "forbidden"
	ErrorKindNotFound        ErrorKind = "not_found"
	ErrorKindConflict        ErrorKind = "conflict"
	ErrorKindRateLimited     ErrorKind = "rate_limited"
	ErrorKindUnavailable     ErrorKind = "unavailable"
	ErrorKindRedirect        ErrorKind = "redirect"
	ErrorKindTransport       ErrorKind = "transport"
	ErrorKindInvalidResponse ErrorKind = "invalid_response"
	ErrorKindRequestRejected ErrorKind = "request_rejected"
)

// APIError contains only a failure category and HTTP status. It deliberately
// does not retain the SDK error, response body, or request URL.
type APIError struct {
	Kind       ErrorKind
	StatusCode int
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}

	switch e.Kind {
	case ErrorKindInvalidRequest:
		return "invalid NetBox API request"
	case ErrorKindUnauthorized:
		return "NetBox rejected the configured API token (HTTP 401)"
	case ErrorKindForbidden:
		return "NetBox denied the API request (HTTP 403)"
	case ErrorKindNotFound:
		return "NetBox device was not found (HTTP 404)"
	case ErrorKindConflict:
		return "NetBox device changed since it was read (HTTP 412)"
	case ErrorKindRateLimited:
		return "NetBox API rate limit was reached (HTTP 429)"
	case ErrorKindUnavailable:
		if e.StatusCode != 0 {
			return "NetBox API is temporarily unavailable (HTTP " + strconv.Itoa(e.StatusCode) + ")"
		}
		return "NetBox API is temporarily unavailable"
	case ErrorKindRedirect:
		return "NetBox API redirects are not followed"
	case ErrorKindTransport:
		return "could not connect to NetBox"
	case ErrorKindInvalidResponse:
		return "NetBox returned an invalid API response"
	case ErrorKindRequestRejected:
		if e.StatusCode != 0 {
			return "NetBox rejected the request (HTTP " + strconv.Itoa(e.StatusCode) + ")"
		}
		return "NetBox rejected the request"
	default:
		return "NetBox API request failed"
	}
}

func classifyError(ctx context.Context, response *http.Response, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if response == nil {
		return &APIError{Kind: ErrorKindTransport}
	}

	status := response.StatusCode
	apiErr := &APIError{StatusCode: status}
	switch {
	case status == http.StatusUnauthorized:
		apiErr.Kind = ErrorKindUnauthorized
	case status == http.StatusForbidden:
		apiErr.Kind = ErrorKindForbidden
	case status == http.StatusNotFound:
		apiErr.Kind = ErrorKindNotFound
	case status == http.StatusPreconditionFailed:
		apiErr.Kind = ErrorKindConflict
	case status == http.StatusTooManyRequests:
		apiErr.Kind = ErrorKindRateLimited
	case status == http.StatusRequestTimeout:
		apiErr.Kind = ErrorKindUnavailable
	case status >= 300 && status < 400:
		apiErr.Kind = ErrorKindRedirect
	case status >= 500:
		apiErr.Kind = ErrorKindUnavailable
	case status >= 400:
		apiErr.Kind = ErrorKindRequestRejected
	default:
		apiErr.Kind = ErrorKindInvalidResponse
	}
	return apiErr
}
