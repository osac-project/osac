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
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/sshkey"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Reasons classifying an SSH-key Secret resolution failure. Permanent reports
// which of them are terminal.
const (
	SSHKeyResolutionReasonSecretNotFound    = "SecretNotFound"
	SSHKeyResolutionReasonSecretInvalid     = "SecretInvalid"
	SSHKeyResolutionReasonSecretUnavailable = "SecretUnavailable"
	SSHKeyResolutionReasonInternalError     = "InternalError"
)

// SSHKeyResolutionRetryDelay is the initial delay callers should use when
// wrapping a retryable SSH-key Secret resolution failure with RequeueAfter.
// The shared reconciler doubles it after each failed retry, up to one minute.
const SSHKeyResolutionRetryDelay = time.Second

// SSHKeyResolutionError marks an SSH-key Secret resolution failure. Reason
// classifies the failure; the resolver never wraps the error, so the type
// never implements RequeueAfter and an unwrapped error is never requeued.
// Callers mark permanent failures as failed and ask for a bounded retry of
// every other failure by wrapping the error with RequeueAfter.
type SSHKeyResolutionError struct {
	Reason string
	Err    error
}

// Permanent reports whether the failure is terminal: retrying cannot succeed,
// so reconciliation must mark the instance failed instead of wrapping the
// error with RequeueAfter.
func (e *SSHKeyResolutionError) Permanent() bool {
	switch e.Reason {
	case SSHKeyResolutionReasonSecretNotFound, SSHKeyResolutionReasonSecretInvalid:
		return true
	default:
		return false
	}
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
) (string, error) {
	if reference == nil {
		return "", nil
	}
	if reference.GetId() == "" {
		return "", &SSHKeyResolutionError{
			Reason: SSHKeyResolutionReasonSecretInvalid,
			Err:    status.Error(codes.InvalidArgument, "SSH key Secret reference has no id"),
		}
	}

	response, err := secretsClient.Get(ctx, privatev1.SecretsGetRequest_builder{
		Id: reference.GetId(),
	}.Build())
	if err != nil {
		var reason string
		switch status.Code(err) {
		case codes.NotFound:
			reason = SSHKeyResolutionReasonSecretNotFound
		case codes.InvalidArgument:
			reason = SSHKeyResolutionReasonSecretInvalid
		case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
			reason = SSHKeyResolutionReasonSecretUnavailable
		default:
			reason = SSHKeyResolutionReasonInternalError
		}
		return "", &SSHKeyResolutionError{Reason: reason, Err: err}
	}
	secret := response.GetObject()
	if err := sshkey.ValidateSSHKeySecretData(secret.GetType(), secret.GetData()); err != nil {
		return "", &SSHKeyResolutionError{
			Reason: SSHKeyResolutionReasonSecretInvalid,
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
