/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Tool calls include token exchange and at most one public API request. Do not
// keep an HTTP request open indefinitely if either dependency stalls.
const toolCallTimeout = 30 * time.Second

func toolCallContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, toolCallTimeout)
}

type toolError struct {
	RequestID string   `json:"request_id"`
	Category  string   `json:"category"`
	GRPCCode  string   `json:"grpc_code"`
	Message   string   `json:"message"`
	Retryable bool     `json:"retryable"`
	Details   []string `json:"details"`
}

func (e *toolError) Error() string {
	encoded, err := json.Marshal(e)
	if err != nil {
		return `{"category":"upstream_failure","grpc_code":"Unknown","message":"The OSAC request failed.","retryable":false,"details":[]}`
	}
	return string(encoded)
}

func newToolError(category, grpcCode, message string, retryable bool) *toolError {
	return &toolError{
		RequestID: uuid.NewString(),
		Category:  category,
		GRPCCode:  grpcCode,
		Message:   message,
		Retryable: retryable,
		Details:   []string{},
	}
}

func safeTokenExchangeError(err error) error {
	if errors.Is(err, errOAuthTokenExchangeUnavailable) {
		return newToolError(
			"service_unavailable",
			"Unavailable",
			"The authentication service is currently unavailable. Retry after checking service status.",
			true,
		)
	}
	return newToolError(
		"authentication",
		"Unauthenticated",
		"MCP authentication failed. Sign in again and retry.",
		false,
	)
}

func safeInvalidRequestError(err error) error {
	envelope := newToolError(
		"invalid_request",
		"InvalidArgument",
		"The request arguments were rejected. Correct the request and try again.",
		false,
	)
	envelope.Details = []string{err.Error()}
	return envelope
}

func safePublicAPIError(err error) error {
	switch status.Code(err) {
	case codes.InvalidArgument:
		return newToolError("invalid_request", "InvalidArgument", "The request arguments were rejected. Correct the request and try again.", false)
	case codes.Unauthenticated:
		return newToolError("authentication", "Unauthenticated", "OSAC authentication failed. Sign in again and retry.", false)
	case codes.PermissionDenied:
		return newToolError("authorization", "PermissionDenied", "The caller is not authorized for this operation. Request the needed access.", false)
	case codes.NotFound:
		return newToolError("not_found", "NotFound", "The requested OSAC resource was not found.", false)
	case codes.AlreadyExists:
		return newToolError("conflict", "AlreadyExists", "The resource already exists or conflicts with current state. Resolve the conflict and retry.", false)
	case codes.FailedPrecondition:
		return newToolError("protocol_incompatible", "FailedPrecondition", "The request is incompatible with the current OSAC service. Update the client and retry.", false)
	case codes.ResourceExhausted:
		return newToolError("rate_limited", "ResourceExhausted", "The OSAC service is busy. Wait and retry.", true)
	case codes.Canceled:
		return newToolError("service_unavailable", "Canceled", "The OSAC request was canceled before completion.", false)
	case codes.DeadlineExceeded:
		return newToolError("service_unavailable", "DeadlineExceeded", "The OSAC request timed out. Retry after checking service status.", true)
	case codes.Unavailable:
		return newToolError("service_unavailable", "Unavailable", "The OSAC service is currently unavailable. Retry after checking service status.", true)
	default:
		if errors.Is(err, context.DeadlineExceeded) {
			return newToolError("service_unavailable", "DeadlineExceeded", "The OSAC request timed out. Retry after checking service status.", true)
		}
		return newToolError("upstream_failure", "Unknown", "The OSAC request failed. Use the request ID when reporting the issue.", false)
	}
}
