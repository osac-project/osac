/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"log/slog"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/sshkey"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	sshKeyTenantScopedMessage      = "SSH key reference must be tenant-scoped."
	sshKeyReferenceNotFoundMessage = "SSH key Secret was not found or is not visible in the selected project."
)

// validateTenantScopedSSHKeyReference rejects SSH Secret references marked as shared.
func validateTenantScopedSSHKeyReference(reference *privatev1.SecretReference) error {
	if reference != nil && reference.GetShared() {
		return grpcstatus.Error(grpccodes.InvalidArgument, sshKeyTenantScopedMessage)
	}
	return nil
}

// canonicalSSHKeySecretReference builds a reference from a resolved Secret's ID, name, and project.
func canonicalSSHKeySecretReference(secret *privatev1.Secret) *privatev1.SecretReference {
	return privatev1.SecretReference_builder{
		Id:      secret.GetId(),
		Name:    secret.GetMetadata().GetName(),
		Project: secret.GetMetadata().GetProject(),
	}.Build()
}

type resolvedTenantSSHKey struct {
	reference *privatev1.SecretReference
	publicKey []byte
}

// resolveTenantScopedSSHKey resolves and validates an SSH key Secret for a resource.
// Callers can use the canonical reference for storage and the public key for provisioning.
func resolveTenantScopedSSHKey(
	ctx context.Context,
	logger *slog.Logger,
	secretsDao *dao.GenericDAO[*privatev1.Secret],
	secretStore vault.SecretStore,
	tenant string,
	reference *privatev1.SecretReference,
	field string,
) (*resolvedTenantSSHKey, error) {
	if reference == nil {
		return nil, nil
	}
	if err := validateTenantScopedSSHKeyReference(reference); err != nil {
		return nil, err
	}

	secret, err := resolveResourceInScope(
		ctx,
		secretsDao,
		referenceScope{tenant: tenant, project: reference.GetProject()},
		reference.GetId(),
		reference.GetName(),
		"secret",
		"",
		grpccodes.NotFound,
	)
	if err != nil {
		return nil, err
	}
	if err := validateResolvedSecretLifecycleAndType(
		secret,
		refKey(reference),
		field,
		privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
	); err != nil {
		return nil, err
	}

	data := secret.GetData()
	if len(data) == 0 && secret.GetBackend() == privatev1.SecretBackend_SECRET_BACKEND_VAULT {
		if secretStore == nil {
			if logger != nil {
				logger.ErrorContext(ctx, "Failed to load SSH key Secret: secret store isn't configured")
			}
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to resolve ssh key reference")
		}
		metadata := secret.GetMetadata()
		data, err = secretStore.Fetch(ctx, metadata.GetTenant(), metadata.GetProject(), metadata.GetName())
		if err != nil {
			if logger != nil {
				logger.ErrorContext(ctx, "Failed to load SSH key Secret from store", "error", err)
			}
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to resolve ssh key reference")
		}
	}

	publicKey := data["public_key"]
	if len(publicKey) == 0 {
		return nil, grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"secret '%s' referenced by %s must contain a non-empty 'public_key' entry",
			refKey(reference),
			field,
		)
	}
	if err := sshkey.ValidateSSHKeySecretData(secret.GetType(), data); err != nil {
		return nil, grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"secret '%s' referenced by %s contains an invalid SSH public key: %s",
			refKey(reference),
			field,
			err,
		)
	}

	return &resolvedTenantSSHKey{
		reference: canonicalSSHKeySecretReference(secret),
		publicKey: append([]byte(nil), publicKey...),
	}, nil
}
