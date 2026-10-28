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

// Package sshkey validates SSH public keys and their Secret data.
package sshkey

import (
	"fmt"

	"golang.org/x/crypto/ssh"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// ValidationError identifies invalid SSH key material without coupling callers to an API transport.
type ValidationError struct {
	reason string
	cause  error
}

func (e *ValidationError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.reason, e.cause)
	}
	return e.reason
}

func (e *ValidationError) Unwrap() error {
	return e.cause
}

// ValidateOpenSSHPublicKey checks that key contains one authorized OpenSSH public key.
func ValidateOpenSSHPublicKey(key string) error {
	_, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(key))
	if err != nil {
		return &ValidationError{reason: "invalid OpenSSH public key", cause: err}
	}
	if len(rest) > 0 {
		return &ValidationError{reason: "invalid OpenSSH public key: unexpected trailing content"}
	}
	return nil
}

// ValidateSSHKeySecretData checks the Secret type, required data, and OpenSSH key syntax.
func ValidateSSHKeySecretData(secretType privatev1.SecretType, data map[string][]byte) error {
	if secretType != privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY {
		return &ValidationError{reason: fmt.Sprintf(
			"secret type %s is invalid for an SSH public-key Secret; expected %s",
			secretType,
			privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
		)}
	}

	publicKey := data["public_key"]
	if len(publicKey) == 0 {
		return &ValidationError{reason: "SSH public-key Secret must contain a non-empty 'public_key' entry"}
	}
	return ValidateOpenSSHPublicKey(string(publicKey))
}
