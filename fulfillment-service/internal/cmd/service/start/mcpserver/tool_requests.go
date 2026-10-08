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
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Tool calls include token exchange and at most one public API request. Do not
// keep an HTTP request open indefinitely if either dependency stalls.
const toolCallTimeout = 30 * time.Second

func toolCallContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, toolCallTimeout)
}

// The downstream status message can contain internal hostnames or customer data.
// Give MCP clients only the status category, never the downstream description.
func safePublicAPIError(err error) error {
	switch status.Code(err) {
	case codes.InvalidArgument:
		return errors.New("public API rejected the request")
	case codes.Unauthenticated:
		return errors.New("public API authentication failed")
	case codes.PermissionDenied:
		return errors.New("permission denied")
	case codes.NotFound:
		return errors.New("resource not found")
	case codes.AlreadyExists:
		return errors.New("resource already exists")
	case codes.FailedPrecondition:
		return errors.New("public API precondition failed")
	case codes.ResourceExhausted:
		return errors.New("public API is busy")
	case codes.Canceled:
		return errors.New("public API request was canceled")
	case codes.DeadlineExceeded:
		return errors.New("public API request timed out")
	case codes.Unavailable:
		return errors.New("public API is unavailable")
	default:
		if errors.Is(err, context.DeadlineExceeded) {
			return errors.New("public API request timed out")
		}
		return errors.New("public API request failed")
	}
}
