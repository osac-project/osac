/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package netbox reads BMC credentials from OSAC Secrets associated with NetBox devices.
package netbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/credentials"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/secretapi"
)

const (
	deviceSecretLabelPrefix = "osac.openshift.io/netbox-device-"
	secretListLimit         = 1
)

var (
	errMissingNetBoxSecret   = errors.New("no system-scoped OSAC Secret is associated with the NetBox device")
	errAmbiguousNetBoxSecret = errors.New("multiple system-scoped OSAC Secrets are associated with the NetBox device")
)

// SecretAPIClient defines the Secret API operations used by this reader.
type SecretAPIClient interface {
	List(ctx context.Context, filter secretapi.Filter, limit int32) (secretapi.ListResult, error)
	Get(ctx context.Context, id string) (secretapi.Secret, error)
	Close() error
}

// Reader finds the system Secret associated with a NetBox device.
type Reader struct {
	secrets SecretAPIClient
}

var _ credentials.Reader = (*Reader)(nil)

// NewReader creates a NetBox-specific BMC credentials reader.
func NewReader(secrets SecretAPIClient) *Reader {
	return &Reader{secrets: secrets}
}

// GetCredentials resolves the unique system Secret associated with a NetBox device.
func (r *Reader) GetCredentials(ctx context.Context, hostID string) (*credentials.BMC, error) {
	if r == nil || r.secrets == nil {
		return nil, errors.New("OSAC Secret API client is not configured")
	}

	secret, err := r.getDeviceSecret(ctx, deviceSecretLabelPrefix+hostID)
	if err != nil {
		return nil, err
	}
	username, password, err := bmcCredentials(secret)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", credentials.ErrResolutionFailed, err)
	}
	return &credentials.BMC{Username: username, Password: password}, nil
}

// Close releases resources owned by the Secret API client.
func (r *Reader) Close() error {
	if r == nil || r.secrets == nil {
		return nil
	}
	return r.secrets.Close()
}

func (r *Reader) getDeviceSecret(ctx context.Context, labelKey string) (secretapi.Secret, error) {
	filter := secretapi.FilterByTenantAndLabel("system", labelKey)
	result, err := r.secrets.List(ctx, filter, secretListLimit)
	if err != nil {
		return secretapi.Secret{}, fmt.Errorf("failed to list OSAC Secrets for NetBox device: %w", err)
	}
	secretID, err := uniqueDeviceSecretID(result)
	if err != nil {
		if errors.Is(err, credentials.ErrCredentialsNotFound) || errors.Is(err, credentials.ErrAmbiguousCredentials) {
			return secretapi.Secret{}, fmt.Errorf("%w: %w", credentials.ErrResolutionFailed, err)
		}
		return secretapi.Secret{}, err
	}

	secret, err := r.secrets.Get(ctx, secretID)
	if err != nil {
		return secretapi.Secret{}, fmt.Errorf("failed to get OSAC Secret for NetBox device: %w", err)
	}
	if !isDeviceSecret(secret, labelKey) {
		return secretapi.Secret{}, errors.New("OSAC Secret is not a system-scoped Secret associated with the NetBox device")
	}
	return secret, nil
}

func uniqueDeviceSecretID(result secretapi.ListResult) (string, error) {
	if result.Total == 0 {
		return "", fmt.Errorf("%w: %w", credentials.ErrCredentialsNotFound, errMissingNetBoxSecret)
	}
	if result.Total > 1 {
		return "", fmt.Errorf("%w: %w", credentials.ErrAmbiguousCredentials, errAmbiguousNetBoxSecret)
	}
	if result.Total != 1 || len(result.Items) != 1 {
		return "", errors.New("inconsistent OSAC Secret list response for NetBox device")
	}
	return result.Items[0].ID, nil
}

func isDeviceSecret(secret secretapi.Secret, labelKey string) bool {
	if secret.Tenant != "system" {
		return false
	}
	_, found := secret.Labels[labelKey]
	return found
}

func bmcCredentials(secret secretapi.Secret) ([]byte, []byte, error) {
	username := secret.Data["username"]
	password := secret.Data["password"]
	if len(username) == 0 || len(password) == 0 {
		return nil, nil, errors.New("OSAC Secret must contain non-empty username and password values")
	}
	return append([]byte(nil), username...), append([]byte(nil), password...), nil
}
