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
	"testing"

	"github.com/stretchr/testify/require"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
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
		{name: "missing pull secret key", secretType: privatev1.SecretType_SECRET_TYPE_PULL_SECRET, wantError: true},
		{name: "empty value", secretType: privatev1.SecretType_SECRET_TYPE_VALUE,
			data: map[string][]byte{"value": {}}, wantError: true},
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
