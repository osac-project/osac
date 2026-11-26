/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"log/slog"

	grpccodes "google.golang.org/grpc/codes"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const diskImagePullSecretField = "spec.pull_secret"

// isEmptySecretReference reports whether a SecretReference is unset or has no
// identity selectors. Empty references clear an optional pull_secret.
func isEmptySecretReference(ref *privatev1.SecretReference) bool {
	if ref == nil {
		return true
	}
	return ref.GetId() == "" && ref.GetName() == ""
}

// resolveAndCanonicalizeDiskImagePullSecret resolves an optional pull-secret
// reference on a DiskImage, requires SECRET_TYPE_PULL_SECRET, and stores the
// canonical SecretReference. A nil or empty reference clears the field.
func resolveAndCanonicalizeDiskImagePullSecret(
	ctx context.Context,
	secretsDao *dao.GenericDAO[*privatev1.Secret],
	image *privatev1.DiskImage,
) error {
	if image == nil || image.GetSpec() == nil {
		return nil
	}
	ref := image.GetSpec().GetPullSecret()
	if isEmptySecretReference(ref) {
		image.GetSpec().ClearPullSecret()
		return nil
	}

	secret, err := resolveAndCanonicalizeReference(
		ctx,
		secretsDao,
		image.GetMetadata(),
		ref,
		"pull secret",
		grpccodes.InvalidArgument,
	)
	if err != nil {
		return err
	}
	return validateResolvedSecretLifecycleAndType(
		secret,
		refKey(ref),
		diskImagePullSecretField,
		privatev1.SecretType_SECRET_TYPE_PULL_SECRET,
	)
}

// redactDiskImagePullSecret hides pull_secret from callers who cannot discover
// the referenced Secret. Matches the design rule that protected ids must not
// appear on Get/List responses.
func redactDiskImagePullSecret(
	ctx context.Context,
	logger *slog.Logger,
	secretsDao *dao.GenericDAO[*privatev1.Secret],
	image *privatev1.DiskImage,
) {
	if image == nil || image.GetSpec() == nil {
		return
	}
	ref := image.GetSpec().GetPullSecret()
	if isEmptySecretReference(ref) {
		image.GetSpec().ClearPullSecret()
		return
	}
	id := ref.GetId()
	if id == "" {
		image.GetSpec().ClearPullSecret()
		return
	}
	_, err := secretsDao.Get().SetId(id).Do(ctx)
	if err == nil {
		return
	}
	var deniedErr *dao.ErrDenied
	var nf interface{ IsNotFound() bool }
	if errors.As(err, &deniedErr) || (errors.As(err, &nf) && nf.IsNotFound()) {
		image.GetSpec().ClearPullSecret()
		return
	}
	if logger != nil {
		logger.ErrorContext(ctx, "Failed to check pull_secret discoverability",
			slog.String("disk_image_id", image.GetId()),
			slog.String("secret_id", id),
			slog.Any("error", err),
		)
	}
	// Fail closed: never leak a Secret id when discoverability cannot be confirmed.
	image.GetSpec().ClearPullSecret()
}

func redactDiskImagePullSecrets(
	ctx context.Context,
	logger *slog.Logger,
	secretsDao *dao.GenericDAO[*privatev1.Secret],
	images []*privatev1.DiskImage,
) {
	for _, image := range images {
		redactDiskImagePullSecret(ctx, logger, secretsDao, image)
	}
}
