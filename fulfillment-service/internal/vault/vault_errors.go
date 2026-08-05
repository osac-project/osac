/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package vault

import (
	"errors"
	"net"
	"net/http"

	vaultapi "github.com/hashicorp/vault/api"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

// ToGrpcError converts a vault error to an appropriate gRPC status error.
func ToGrpcError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, vaultapi.ErrSecretNotFound) {
		return grpcstatus.Error(grpccodes.NotFound,
			"secret not found in vault")
	}

	var responseErr *vaultapi.ResponseError
	if errors.As(err, &responseErr) {
		switch responseErr.StatusCode {
		case http.StatusBadRequest:
			return grpcstatus.Error(grpccodes.InvalidArgument,
				"invalid vault request")
		case http.StatusUnauthorized:
			return grpcstatus.Error(grpccodes.Unauthenticated,
				"vault authentication failed")
		case http.StatusForbidden:
			return grpcstatus.Error(grpccodes.PermissionDenied,
				"vault access denied")
		case http.StatusNotFound:
			return grpcstatus.Error(grpccodes.NotFound,
				"secret not found in vault")
		case http.StatusTooManyRequests:
			return grpcstatus.Error(grpccodes.ResourceExhausted,
				"vault rate limit exceeded")
		case http.StatusServiceUnavailable:
			return grpcstatus.Error(grpccodes.Unavailable,
				"vault unavailable")
		case http.StatusInternalServerError:
			return grpcstatus.Error(grpccodes.Internal,
				"vault internal error")
		}
		return grpcstatus.Error(grpccodes.Internal,
			"vault operation failed")
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return grpcstatus.Error(grpccodes.Unavailable,
			"vault operation failed")
	}

	return grpcstatus.Error(grpccodes.Internal,
		"vault operation failed")
}
