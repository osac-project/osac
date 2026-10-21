/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

// Package sshkey holds the shared rule for a usable registered SSH-key
// Secret. Fulfillment validates the rule when a reference is created and
// when the BMI reconciler resolves key material; the operator validates it
// again when resolving a Cluster's key for AAP launches and InfraEnvs. Both
// enforcement paths call these helpers so they cannot drift apart.
package sshkey

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// PublicKeyDataKey is the Secret data entry holding the OpenSSH public key.
const PublicKeyDataKey = "public_key"

// SecretsGetter is the narrow Secrets.Get seam, satisfied by the generated
// privatev1.SecretsClient and by test doubles.
type SecretsGetter interface {
	Get(ctx context.Context, in *privatev1.SecretsGetRequest, opts ...grpc.CallOption) (*privatev1.SecretsGetResponse, error)
}

// Error classifies a Fetch failure for the caller's retry logic. Permanent
// failures cannot succeed on retry; Reason names the failure class
// ("SecretNotFound" or "SecretInvalid") when set. Err is never nil and never
// carries key material.
type Error struct {
	Permanent bool
	Reason    string
	Err       error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// ValidateOpenSSHPublicKey checks that the provided string is a valid
// OpenSSH public key.
func ValidateOpenSSHPublicKey(key string) error {
	_, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(key))
	if err != nil {
		return fmt.Errorf("invalid OpenSSH public key: %w", err)
	}
	if len(rest) > 0 {
		return fmt.Errorf("invalid OpenSSH public key: unexpected trailing content")
	}
	return nil
}

// ValidateSecretData checks that a loaded Secret is usable as a registered
// SSH key: type SECRET_TYPE_SSH_PUBLIC_KEY and a non-empty, well-formed
// public_key entry in its data. The identifier names the Secret in error
// messages; key material is never included in errors.
func ValidateSecretData(secret *privatev1.Secret, data map[string][]byte, identifier string) error {
	if secret.GetType() != privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY {
		return fmt.Errorf("SSH-key Secret '%s' has type %s; expected %s",
			identifier, secret.GetType(), privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY)
	}
	publicKey := string(data[PublicKeyDataKey])
	if publicKey == "" {
		return fmt.Errorf("SSH-key Secret '%s' has no non-empty %s entry", identifier, PublicKeyDataKey)
	}
	if err := ValidateOpenSSHPublicKey(publicKey); err != nil {
		return fmt.Errorf("SSH-key Secret '%s' has an unusable %s entry: %w", identifier, PublicKeyDataKey, err)
	}
	return nil
}

// Fetch loads the registered SSH-key Secret material for id, enforcing the
// shared usability rule and the expected tenant. The returned error is
// always an *Error: a client NotFound or InvalidArgument failure, a deleted
// Secret, or a tenant mismatch is permanent (SecretNotFound or SecretInvalid);
// other client failures are transient. Callers map the classification onto
// their own retry semantics.
func Fetch(ctx context.Context, client SecretsGetter, id, expectedTenant string) (string, error) {
	if id == "" {
		return "", &Error{Permanent: true, Reason: "SecretInvalid",
			Err: errors.New("registered SSH-key Secret reference has no canonical ID")}
	}
	response, err := client.Get(ctx, privatev1.SecretsGetRequest_builder{Id: id}.Build())
	if err != nil {
		fetchErr := &Error{Err: err}
		switch grpcstatus.Code(err) {
		case grpccodes.NotFound:
			fetchErr.Permanent = true
			fetchErr.Reason = "SecretNotFound"
		case grpccodes.InvalidArgument:
			fetchErr.Permanent = true
			fetchErr.Reason = "SecretInvalid"
		}
		return "", fetchErr
	}
	secret := response.GetObject()
	if secret == nil {
		return "", &Error{Err: errors.New("Secrets.Get returned no object for SSH-key Secret '" + id + "'")}
	}
	metadata := secret.GetMetadata()
	if metadata == nil {
		return "", &Error{Err: errors.New("SSH-key Secret '" + id + "' has no metadata")}
	}
	// Secrets.Get returns soft-deleted rows: fail closed so deleting a key
	// revokes it for instances that have not materialized it yet.
	if metadata.HasDeletionTimestamp() {
		return "", &Error{Permanent: true, Reason: "SecretNotFound",
			Err: errors.New("registered SSH-key Secret '" + id + "' has been deleted")}
	}
	// The read is controller-authorized, so tenant scoping is not enforced by
	// the API call itself: fail closed on a cross-tenant Secret.
	if metadata.GetTenant() != expectedTenant {
		return "", &Error{Permanent: true, Reason: "SecretNotFound",
			Err: fmt.Errorf("registered SSH-key Secret '%s' does not belong to tenant '%s'", id, expectedTenant)}
	}
	if err := ValidateSecretData(secret, secret.GetData(), id); err != nil {
		return "", &Error{Permanent: true, Reason: "SecretInvalid", Err: err}
	}
	return string(secret.GetData()[PublicKeyDataKey]), nil
}
