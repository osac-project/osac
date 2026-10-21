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

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/references"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// fetchSecretData returns the Secret's data, loading Vault-backed Secrets from
// the secret store when the database row carries no inline data. The field
// label names the referencing spec field for logs and error messages.
func fetchSecretData(
	ctx context.Context,
	logger *slog.Logger,
	secretStore vault.SecretStore,
	secret *privatev1.Secret,
	field string,
) (map[string][]byte, error) {
	data := secret.GetData()
	if len(data) == 0 && secret.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_VAULT {
		if secretStore == nil {
			logger.ErrorContext(ctx, "Failed to load "+field+" Secret: secret store isn't configured")
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to resolve %s reference", field)
		}
		metadata := secret.GetMetadata()
		fetched, err := secretStore.Fetch(ctx, metadata.GetTenant(), metadata.GetProject(), metadata.GetName())
		if err != nil {
			logger.ErrorContext(ctx, "Failed to load "+field+" Secret from store", "error", err)
			return nil, vault.ToGrpcError(err)
		}
		return fetched, nil
	}
	return data, nil
}

// resolveSecretReferenceOfType resolves a local Secret reference and verifies the
// semantic type required by the consuming API. It only loads Secret metadata and
// type; secret data remains protected by the Secrets server.
func resolveSecretReferenceOfType(
	ctx context.Context,
	logger *slog.Logger,
	secretsDao *dao.GenericDAO[*privatev1.Secret],
	ref *privatev1.SecretLocalReference,
	field string,
	expected privatev1.SecretType,
) (*references.ResolvedRef, error) {
	if ref == nil {
		return nil, nil
	}
	if ref.GetId() == "" && ref.GetName() == "" {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "%s must specify id or name", field)
	}

	resolved, err := references.NewDAOLookupFunc(secretsDao)(ctx, "", "", ref.GetId(), ref.GetName())
	if err != nil {
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			return nil, grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
		}
		var nf interface{ IsNotFound() bool }
		if errors.As(err, &nf) && nf.IsNotFound() {
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
				"there is no secret with identifier or name '%s'", refKey(ref))
		}
		if logger != nil {
			logger.ErrorContext(ctx, "Failed to resolve secret reference", "field", field, "error", err)
		}
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to resolve %s reference", field)
	}

	if err := validateResolvedSecretType(ctx, logger, secretsDao, ref, resolved, field, expected); err != nil {
		return nil, err
	}
	return resolved, nil
}

func validateResolvedSecretType(
	ctx context.Context,
	logger *slog.Logger,
	secretsDao *dao.GenericDAO[*privatev1.Secret],
	ref *privatev1.SecretLocalReference,
	resolved *references.ResolvedRef,
	field string,
	expected privatev1.SecretType,
) error {
	secretResponse, err := secretsDao.Get().SetId(resolved.ID).Do(ctx)
	if err != nil {
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			return grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
		}
		var nf interface{ IsNotFound() bool }
		if errors.As(err, &nf) && nf.IsNotFound() {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"there is no secret with identifier or name '%s'", refKey(ref))
		}
		if logger != nil {
			logger.ErrorContext(ctx, "Failed to load secret reference", "field", field, "error", err)
		}
		return grpcstatus.Errorf(grpccodes.Internal, "failed to resolve %s reference", field)
	}

	return validateSecretType(secretResponse.GetObject(), refKey(ref), field, expected)
}

// validateResolvedSecretLifecycleAndType checks a Secret that has already been loaded.
func validateResolvedSecretLifecycleAndType(secret *privatev1.Secret, identifier, field string, expected privatev1.SecretType) error {
	if err := validateResourceNotDeleted("secret", identifier, " referenced by "+field, secret.GetMetadata()); err != nil {
		return err
	}
	return validateSecretType(secret, identifier, field, expected)
}

func validateSecretType(secret *privatev1.Secret, identifier, field string, expected privatev1.SecretType) error {
	if secret.GetType() != expected {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"secret '%s' referenced by %s has type %s; expected %s",
			identifier, field, secret.GetType(), expected)
	}
	return nil
}
