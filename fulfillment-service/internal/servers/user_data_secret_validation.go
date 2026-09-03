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
	"errors"
	"log/slog"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/references"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
)

const userDataSecretDataKey = "userdata"

func validateUserDataSecret(
	ctx context.Context,
	logger *slog.Logger,
	secretsDao *dao.GenericDAO[*privatev1.Secret],
	secretStore vault.SecretStore,
	ref *privatev1.SecretLocalReference,
) (*privatev1.SecretLocalReference, error) {
	if ref == nil {
		return nil, nil
	}
	if ref.GetId() == "" && ref.GetName() == "" {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "user_data_secret must specify id or name")
	}

	resolved, err := references.NewDAOLookupFunc(secretsDao)(ctx, "", "", ref.GetId(), ref.GetName())
	if err != nil {
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "%s", deniedErr.Reason)
		}
		var notFound interface{ IsNotFound() bool }
		if errors.As(err, &notFound) && notFound.IsNotFound() {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
				"there is no secret with identifier or name '%s'", refKey(ref))
		}
		logger.ErrorContext(ctx, "Failed to resolve user_data_secret reference", "error", err)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to resolve user_data_secret reference")
	}
	if resolved.Tenant == auth.SharedTenant {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"shared secrets cannot be used as user_data_secret references")
	}

	secretResponse, err := secretsDao.Get().SetId(resolved.ID).Do(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to load user_data_secret reference", "error", err)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to resolve user_data_secret reference")
	}
	secret := secretResponse.GetObject()
	data := secret.GetData()
	if len(data) == 0 && secretStore != nil &&
		secret.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_VAULT {
		metadata := secret.GetMetadata()
		data, err = secretStore.Fetch(ctx, metadata.GetTenant(), metadata.GetProject(), metadata.GetName())
		if err != nil {
			logger.ErrorContext(ctx, "Failed to load user_data_secret value from store", "error", err)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to resolve user_data_secret reference")
		}
	}
	if value, ok := data[userDataSecretDataKey]; !ok || len(value) == 0 {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"secret '%s' referenced by user_data_secret must contain a non-empty '%s' entry",
			refKey(ref), userDataSecretDataKey)
	}

	result := &privatev1.SecretLocalReference{}
	result.SetId(resolved.ID)
	result.SetName(resolved.Name)
	return result, nil
}
