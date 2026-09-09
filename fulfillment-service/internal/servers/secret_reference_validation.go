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
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/references"
)

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

	secret := secretResponse.GetObject()
	if secret.GetType() != expected {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"secret '%s' referenced by %s has type %s; expected %s",
			refKey(ref), field, secret.GetType(), expected)
	}
	return nil
}
