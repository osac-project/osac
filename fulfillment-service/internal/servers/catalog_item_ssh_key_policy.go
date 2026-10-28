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

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// validateCatalogItemSSHKeyPolicy checks a locked SSH key or editable default in the Catalog
// Item's tenant and reference project, then stores its canonical reference.
func validateCatalogItemSSHKeyPolicy(
	ctx context.Context,
	scope referenceScope,
	policy *privatev1.FullSecretReferenceFieldPolicy,
	secretsDao *dao.GenericDAO[*privatev1.Secret],
) error {
	if policy == nil {
		return nil
	}
	state, err := decodeFullSecretReferencePolicy(policy)
	if err != nil {
		return catalogItemPolicyError("fields.ssh_key", err.Error())
	}
	if err := validateSharedCatalogItemLocalReferencePolicy(scope, "fields.ssh_key", state.hasLocked, state.hasDefault); err != nil {
		return err
	}
	resolve := func(ref *privatev1.SecretReference) (*privatev1.SecretReference, error) {
		if ref == nil {
			return nil, nil
		}
		if err := validateTenantScopedSSHKeyReference(ref); err != nil {
			return nil, err
		}
		resolved, resolveErr := resolveLockedResourceInScope(
			ctx,
			secretsDao,
			referenceScope{tenant: scope.tenant, project: ref.GetProject()},
			ref.GetId(),
			ref.GetName(),
			"secret",
			" in fields.ssh_key",
			grpccodes.NotFound,
		)
		if resolveErr != nil {
			if grpcstatus.Code(resolveErr) == grpccodes.NotFound {
				return nil, grpcstatus.Error(grpccodes.NotFound, sshKeyReferenceNotFoundMessage)
			}
			return nil, resolveErr
		}
		if err := validateResolvedSecretLifecycleAndType(
			resolved,
			refKey(ref),
			"fields.ssh_key",
			privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
		); err != nil {
			return nil, err
		}
		return canonicalSSHKeySecretReference(resolved), nil
	}
	if state.hasLocked {
		canonical, resolveErr := resolve(state.lockedValue)
		if resolveErr != nil {
			return resolveErr
		}
		policy.SetLocked(canonical)
	}
	if state.hasDefault {
		canonical, resolveErr := resolve(state.defaultValue)
		if resolveErr != nil {
			return resolveErr
		}
		policy.GetEditable().SetDefaultValue(canonical)
	}
	return nil
}
