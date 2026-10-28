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

package sshkey

import (
	"errors"
	"testing"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const validPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG8K1ZuSC7tmzxD5LJJXwkCfStVEjzXWYCFhJaLBxWAn test@example.com"

func TestValidateOpenSSHPublicKey(t *testing.T) {
	for _, test := range []struct {
		name    string
		key     string
		wantErr bool
	}{
		{name: "valid", key: validPublicKey},
		{name: "invalid", key: "not-an-ssh-key", wantErr: true},
		{name: "trailing key", key: validPublicKey + "\n" + validPublicKey, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateOpenSSHPublicKey(test.key)
			if test.wantErr {
				var validationErr *ValidationError
				if !errors.As(err, &validationErr) {
					t.Fatalf("expected ValidationError, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected valid key, got %v", err)
			}
		})
	}
}

func TestValidateSSHKeySecretData(t *testing.T) {
	for _, test := range []struct {
		name       string
		secretType privatev1.SecretType
		data       map[string][]byte
		wantErr    bool
	}{
		{
			name:       "valid SSH key Secret",
			secretType: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			data:       map[string][]byte{"public_key": []byte(validPublicKey)},
		},
		{
			name:       "wrong Secret type",
			secretType: privatev1.SecretType_SECRET_TYPE_VALUE,
			data:       map[string][]byte{"public_key": []byte(validPublicKey)},
			wantErr:    true,
		},
		{
			name:       "missing public_key",
			secretType: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			data:       map[string][]byte{"other": []byte("value")},
			wantErr:    true,
		},
		{
			name:       "invalid public key",
			secretType: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			data:       map[string][]byte{"public_key": []byte("not-an-ssh-key")},
			wantErr:    true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSSHKeySecretData(test.secretType, test.data)
			if test.wantErr {
				var validationErr *ValidationError
				if !errors.As(err, &validationErr) {
					t.Fatalf("expected ValidationError, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected valid SSH key Secret, got %v", err)
			}
		})
	}
}
