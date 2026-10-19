/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package controllers

import (
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// ValidateOpenSSHPublicKeySecret validates the semantic type and public key material of an SSH-key Secret.
func ValidateOpenSSHPublicKeySecret(secret *privatev1.Secret, data map[string][]byte, identifier string) error {
	if secret.GetType() != privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY {
		return status.Errorf(codes.InvalidArgument, "secret '%s' has type %s; expected %s", identifier,
			secret.GetType(), privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY)
	}
	publicKey := data["public_key"]
	if len(publicKey) == 0 {
		return status.Errorf(codes.InvalidArgument,
			"secret '%s' must contain a non-empty 'public_key' entry", identifier)
	}
	_, _, _, rest, err := ssh.ParseAuthorizedKey(publicKey)
	if err != nil {
		return status.Errorf(codes.InvalidArgument,
			"secret '%s' contains an invalid OpenSSH public key: %s", identifier, err)
	}
	if len(rest) > 0 {
		return status.Errorf(codes.InvalidArgument,
			"secret '%s' contains an invalid OpenSSH public key: unexpected trailing content", identifier)
	}
	return nil
}
