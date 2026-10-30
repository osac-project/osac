/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/sshkey"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	sshKeyResolutionReasonSecretInvalid  = "SecretInvalid"
	sshKeyResolutionReasonSecretNotFound = "SecretNotFound"
)

// SSHKeyResolutionError carries failure details and whether it should mark reconciliation failed.
type SSHKeyResolutionError struct {
	Permanent bool
	Reason    string
	Err       error
}

func (e *SSHKeyResolutionError) Error() string {
	return fmt.Sprintf("failed to resolve SSH public key Secret: %v", e.Err)
}

func (e *SSHKeyResolutionError) Unwrap() error {
	return e.Err
}

// ResolveSSHKey fetches and validates the SSH key Secret referenced by an instance.
func ResolveSSHKey(
	ctx context.Context,
	secretsClient privatev1.SecretsClient,
	reference *privatev1.SecretReference,
) (string, *SSHKeyResolutionError) {
	if reference == nil {
		return "", nil
	}
	if reference.GetId() == "" {
		return "", &SSHKeyResolutionError{
			Permanent: true,
			Reason:    sshKeyResolutionReasonSecretInvalid,
			Err:       status.Error(codes.InvalidArgument, "SSH key Secret reference has no id"),
		}
	}

	response, err := secretsClient.Get(ctx, privatev1.SecretsGetRequest_builder{
		Id: reference.GetId(),
	}.Build())
	if err != nil {
		resolutionErr := &SSHKeyResolutionError{Err: err}
		switch status.Code(err) {
		case codes.NotFound:
			// NotFound may be temporary while the referenced Secret becomes visible.
			resolutionErr.Reason = sshKeyResolutionReasonSecretNotFound
		case codes.InvalidArgument:
			resolutionErr.Permanent = true
			resolutionErr.Reason = sshKeyResolutionReasonSecretInvalid
		}
		return "", resolutionErr
	}
	secret := response.GetObject()
	if err := sshkey.ValidateSSHKeySecretData(secret.GetType(), secret.GetData()); err != nil {
		return "", &SSHKeyResolutionError{
			Permanent: true,
			Reason:    sshKeyResolutionReasonSecretInvalid,
			Err: status.Errorf(
				codes.InvalidArgument,
				"SSH key Secret %q is invalid: %v",
				reference.GetId(),
				err,
			),
		}
	}
	return string(secret.GetData()["public_key"]), nil
}
