/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package controllers

import (
	"fmt"

	"golang.org/x/crypto/ssh"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// ValidateOpenSSHPublicKey checks that the provided string is a valid OpenSSH public key.
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

// ValidateSSHKeySecretData checks that a loaded Secret is usable as a
// registered SSH key: type SECRET_TYPE_SSH_PUBLIC_KEY and a non-empty,
// well-formed public_key entry in its data. The identifier names the Secret
// in error messages. Both the Create-time validation and the reconciler
// enforce this rule through this helper so the two paths cannot drift apart.
func ValidateSSHKeySecretData(secret *privatev1.Secret, data map[string][]byte, identifier string) error {
	if secret.GetType() != privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"SSH-key Secret '%s' has type %s; expected %s",
			identifier, secret.GetType(), privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY)
	}
	publicKey := string(data["public_key"])
	if publicKey == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"SSH-key Secret '%s' has no non-empty public_key entry", identifier)
	}
	if err := ValidateOpenSSHPublicKey(publicKey); err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"SSH-key Secret '%s' has an unusable public_key entry: %s", identifier, err)
	}
	return nil
}
