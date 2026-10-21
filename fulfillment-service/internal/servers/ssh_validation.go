/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"log/slog"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"github.com/osac-project/osac/proto/sshkey"
)

// validateOpenSSHPublicKey checks that the provided string is a valid OpenSSH public key.
var validateOpenSSHPublicKey = sshkey.ValidateOpenSSHPublicKey

// validateAndCanonicalizeSSHKeyReference validates a full SSH-key Secret reference and
// loads its public-key material only for optimistic create/update-time validation. The
// material is deliberately not copied into the referencing resource.
//
// The reference may select any SSH-key Secret in the resource's tenant: an empty
// reference project selects the tenant's default (root) project, and an explicit
// reference project selects that project. Shared Secrets are rejected.
func validateAndCanonicalizeSSHKeyReference(
	ctx context.Context,
	logger *slog.Logger,
	secretsDao *dao.GenericDAO[*privatev1.Secret],
	secretStore vault.SecretStore,
	metadata *privatev1.Metadata,
	ref *privatev1.SecretReference,
	field string,
) (*privatev1.SecretReference, error) {
	if ref == nil {
		return nil, nil
	}
	if ref.GetId() == "" && ref.GetName() == "" {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%s must specify id or name", field)
	}
	if metadata == nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument,
			"cannot resolve "+field+" reference without resource metadata")
	}
	if ref.GetShared() {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%s must not reference a shared secret", field)
	}
	tenant := metadata.GetTenant()
	if tenant == "" {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument,
			"cannot resolve "+field+" reference without resource tenant")
	}
	secret, err := resolveResourceInScope(ctx, secretsDao,
		referenceScope{tenant: tenant, project: ref.GetProject()},
		ref.GetId(), ref.GetName(), "secret", " in "+field, grpccodes.NotFound)
	if err != nil {
		return nil, err
	}
	if err := validateResolvedSecretLifecycleAndType(secret, refKey(ref), field,
		privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY); err != nil {
		return nil, err
	}

	data, err := fetchSecretData(ctx, logger, secretStore, secret, field)
	if err != nil {
		return nil, err
	}
	if err := sshkey.ValidateSecretData(secret, data, refKey(ref)); err != nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, err.Error())
	}

	return canonicalSecretReference(secret), nil
}
