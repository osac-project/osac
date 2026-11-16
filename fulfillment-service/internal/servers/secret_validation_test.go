/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestValidateSecretData(t *testing.T) {
	tests := []struct {
		name       string
		secretType privatev1.SecretType
		data       map[string][]byte
		wantError  bool
	}{
		{name: "opaque permits empty data", secretType: privatev1.SecretType_SECRET_TYPE_OPAQUE},
		{name: "unspecified permits empty data", secretType: privatev1.SecretType_SECRET_TYPE_UNSPECIFIED},
		{name: "pull secret", secretType: privatev1.SecretType_SECRET_TYPE_PULL_SECRET,
			data: map[string][]byte{".dockerconfigjson": []byte("not parsed")}},
		{name: "kubeconfig", secretType: privatev1.SecretType_SECRET_TYPE_KUBECONFIG,
			data: map[string][]byte{"kubeconfig": []byte("not parsed")}},
		{name: "user data", secretType: privatev1.SecretType_SECRET_TYPE_USER_DATA,
			data: map[string][]byte{"userdata": []byte("#cloud-config")}},
		{name: "value", secretType: privatev1.SecretType_SECRET_TYPE_VALUE,
			data: map[string][]byte{"value": []byte("secret")}},
		{name: "ssh public key", secretType: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			data: map[string][]byte{"public_key": []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG8K1ZuSC7tmzxD5LJJXwkCfStVEjzXWYCFhJaLBxWAn test@example.com")}},
		{name: "missing pull secret key", secretType: privatev1.SecretType_SECRET_TYPE_PULL_SECRET, wantError: true},
		{name: "empty value", secretType: privatev1.SecretType_SECRET_TYPE_VALUE,
			data: map[string][]byte{"value": {}}, wantError: true},
		{name: "missing ssh public key", secretType: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY, wantError: true},
		{name: "invalid ssh public key", secretType: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			data: map[string][]byte{"public_key": []byte("not-an-ssh-key")}, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSecretData(tt.secretType, tt.data)
			if tt.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateSecretDataWindowsUserData(t *testing.T) {
	tests := []struct {
		name        string
		data        map[string][]byte
		wantMessage string
	}{
		{
			name: "valid XML is accepted",
			data: map[string][]byte{"userdata": []byte(`<?xml version="1.0"?><root/>`)},
		},
		{
			name:        "missing userdata key is rejected",
			data:        map[string][]byte{},
			wantMessage: `requires a non-empty data["userdata"]`,
		},
		{
			name:        "empty userdata is rejected",
			data:        map[string][]byte{"userdata": {}},
			wantMessage: `requires a non-empty data["userdata"]`,
		},
		{
			name:        "oversized malformed XML rejects for size before parsing",
			data:        map[string][]byte{"userdata": bytes.Repeat([]byte("<"), 65_537)},
			wantMessage: "exceeds the maximum of 65536 bytes",
		},
		{
			name:        "UTF-16 declaration is rejected as unsupported encoding",
			data:        map[string][]byte{"userdata": []byte(`<?xml version="1.0" encoding="UTF-16"?><root/>`)},
			wantMessage: "unsupported encoding",
		},
		{
			name:        "raw invalid UTF-8 is rejected as unsupported encoding",
			data:        map[string][]byte{"userdata": {0xff, 0xfe, '<', 'r', 'o', 'o', 't', '/', '>'}},
			wantMessage: "unsupported encoding",
		},
		{
			name:        "UTF-16 bytes are rejected as unsupported encoding",
			data:        map[string][]byte{"userdata": {0xff, 0xfe, 0x3c, 0x00, 0x72, 0x00, 0x6f, 0x00, 0x6f, 0x00, 0x74, 0x00, 0x2f, 0x00, 0x3e, 0x00}},
			wantMessage: "unsupported encoding",
		},
		{
			name:        "truncated XML is rejected as malformed",
			data:        map[string][]byte{"userdata": []byte(`<root><child></root>`)},
			wantMessage: "not well-formed XML",
		},
		{
			name:        "DOCTYPE is rejected as a prohibited directive",
			data:        map[string][]byte{"userdata": []byte(`<!DOCTYPE root><root/>`)},
			wantMessage: "prohibited XML directive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSecretData(privatev1.SecretType_SECRET_TYPE_WINDOWS_USER_DATA, tt.data)
			if tt.wantMessage == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantMessage)
		})
	}
}

func TestValidateWindowsUserDataSecretOrder(t *testing.T) {
	const maxBytes = 65_536
	exactLimitXML := append([]byte("<root>"),
		bytes.Repeat([]byte("a"), maxBytes-len("<root>")-len("</root>"))...)
	exactLimitXML = append(exactLimitXML, "</root>"...)

	t.Run("oversized malformed value rejects for size without parsing", func(t *testing.T) {
		calls := 0
		err := validateWindowsUserDataSecret(bytes.Repeat([]byte("<"), maxBytes+1), func([]byte) error {
			calls++
			return nil
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "exceeds the maximum of 65536 bytes")
		require.Equal(t, 0, calls)
	})

	t.Run("exact-limit valid value parses once", func(t *testing.T) {
		calls := 0
		var observed []byte
		err := validateWindowsUserDataSecret(bytes.Clone(exactLimitXML), func(value []byte) error {
			calls++
			observed = bytes.Clone(value)
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, 1, calls)
		require.Equal(t, exactLimitXML, observed)
	})
}

func TestSecretNamesAreNotReservedForInternalUse(t *testing.T) {
	name := "service-account"
	secret := privatev1.Secret_builder{
		Metadata: privatev1.Metadata_builder{Name: name, Tenant: "tenant-a"}.Build(),
		Type:     privatev1.SecretType_SECRET_TYPE_OPAQUE,
		Data:     map[string][]byte{"client_secret": []byte("credential")},
	}.Build()
	require.NoError(t, (&PrivateSecretsServer{}).validateSecretCreate(secret))

	existing := privatev1.Secret_builder{
		Metadata: privatev1.Metadata_builder{Name: name, Tenant: "tenant-a"}.Build(),
		Backend:  privatev1.SecretBackend_SECRET_BACKEND_VAULT,
		Type:     privatev1.SecretType_SECRET_TYPE_OPAQUE,
	}.Build()
	updated := privatev1.Secret_builder{
		Data: map[string][]byte{"client_secret": []byte("rotated")},
	}.Build()
	require.NoError(t, (&PrivateSecretsServer{}).validateSecretUpdate(context.TODO(), updated,
		&fieldmaskpb.FieldMask{Paths: []string{"data"}}, existing))
}
