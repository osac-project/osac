/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"log/slog"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// loadSSHKeySecretData returns an SSH-key Secret's data, loading backend-managed
// values when the database object contains metadata only. Reference authorization,
// type checks, key validation, and canonicalization remain the caller's policy.
func loadSSHKeySecretData(
	ctx context.Context,
	logger *slog.Logger,
	secretStore vault.SecretStore,
	hubSecretFetcher HubSecretFetcher,
	secret *privatev1.Secret,
	field string,
) (map[string][]byte, error) {
	data := secret.GetData()
	if len(data) != 0 {
		return data, nil
	}

	switch secret.GetBackend() {
	case privatev1.SecretBackend_SECRET_BACKEND_VAULT:
		if secretStore == nil {
			logger.ErrorContext(ctx, "Failed to load SSH key Secret: secret store isn't configured")
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to resolve %s reference", field)
		}
		metadata := secret.GetMetadata()
		data, err := secretStore.Fetch(
			ctx, metadata.GetTenant(), metadata.GetProject(), metadata.GetName(),
		)
		if err != nil {
			logger.ErrorContext(ctx, "Failed to load SSH key Secret from store", "error", err)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to resolve %s reference", field)
		}
		return data, nil

	case privatev1.SecretBackend_SECRET_BACKEND_HUB:
		if hubSecretFetcher == nil {
			return nil, grpcstatus.Error(grpccodes.FailedPrecondition, "hub secret retrieval is not configured")
		}
		data, err := hubSecretFetcher.Fetch(ctx, secret.GetCoordinates())
		if err != nil {
			logger.ErrorContext(ctx, "Failed to load SSH key Secret from HUB", "error", err)
			code := grpccodes.Internal
			if statusErr, ok := grpcstatus.FromError(err); ok {
				code = statusErr.Code()
			}
			return nil, grpcstatus.Errorf(code, "failed to resolve %s reference", field)
		}
		return data, nil
	}

	return data, nil
}
