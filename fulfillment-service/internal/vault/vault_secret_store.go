/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package vault

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"regexp"

	vaultapi "github.com/hashicorp/vault/api"
)

// SecretStore defines the interface for storing and retrieving secret data from a Vault-compatible
// backend. Implementations are expected to handle per-tenant namespace isolation.
//
//go:generate mockgen -destination=vault_secret_store_mock.go -package=vault . SecretStore
type SecretStore interface {
	Store(ctx context.Context, tenant, project, name string, data map[string][]byte) error
	Fetch(ctx context.Context, tenant, project, name string) (map[string][]byte, error)
	Delete(ctx context.Context, tenant, project, name string) error
}

type VaultSecretStoreBuilder struct {
	logger          *slog.Logger
	address         string
	token           string
	parentNamespace string
	kvMountPath     string
	caPool          *x509.CertPool
}

type VaultSecretStore struct {
	logger          *slog.Logger
	client          *vaultapi.Client
	parentNamespace string
	kvMountPath     string
}

func NewVaultSecretStore() *VaultSecretStoreBuilder {
	return &VaultSecretStoreBuilder{
		kvMountPath: "secret",
	}
}

func (b *VaultSecretStoreBuilder) SetLogger(value *slog.Logger) *VaultSecretStoreBuilder {
	b.logger = value
	return b
}

func (b *VaultSecretStoreBuilder) SetAddress(value string) *VaultSecretStoreBuilder {
	b.address = value
	return b
}

func (b *VaultSecretStoreBuilder) SetToken(value string) *VaultSecretStoreBuilder {
	b.token = value
	return b
}

func (b *VaultSecretStoreBuilder) SetParentNamespace(value string) *VaultSecretStoreBuilder {
	b.parentNamespace = value
	return b
}

func (b *VaultSecretStoreBuilder) SetKVMountPath(value string) *VaultSecretStoreBuilder {
	b.kvMountPath = value
	return b
}

func (b *VaultSecretStoreBuilder) SetCaPool(value *x509.CertPool) *VaultSecretStoreBuilder {
	b.caPool = value
	return b
}

func (b *VaultSecretStoreBuilder) Build() (result *VaultSecretStore, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.address == "" {
		err = errors.New("address is mandatory")
		return
	}
	if b.token == "" {
		err = errors.New("token is mandatory")
		return
	}
	if b.parentNamespace == "" {
		err = errors.New("parent namespace is mandatory")
		return
	}

	config := vaultapi.DefaultConfig()
	config.Address = b.address

	if b.caPool != nil {
		transport, ok := config.HttpClient.Transport.(*http.Transport)
		if !ok {
			err = errors.New("unexpected transport type from vault default config")
			return
		}
		cloned := transport.Clone()
		cloned.TLSClientConfig.RootCAs = b.caPool
		config.HttpClient.Transport = cloned
	}

	client, err := vaultapi.NewClient(config)
	if err != nil {
		err = fmt.Errorf("failed to create vault client: %w", err)
		return
	}
	client.SetToken(b.token)
	client.SetCloneToken(true)

	result = &VaultSecretStore{
		logger:          b.logger,
		client:          client,
		parentNamespace: b.parentNamespace,
		kvMountPath:     b.kvMountPath,
	}
	return
}

func (s *VaultSecretStore) Store(ctx context.Context, tenant, project, name string,
	data map[string][]byte) error {
	if err := validatePathComponent(tenant, "tenant"); err != nil {
		return err
	}
	if project != "" {
		if err := validatePathComponent(project, "project"); err != nil {
			return err
		}
	}
	if err := validatePathComponent(name, "name"); err != nil {
		return err
	}

	client, err := s.tenantClient(tenant)
	if err != nil {
		return err
	}

	vaultData := make(map[string]any, len(data))
	for k, v := range data {
		vaultData[k] = base64.StdEncoding.EncodeToString(v)
	}

	secretPath := s.secretPath(project, name)
	_, err = client.KVv2(s.kvMountPath).Put(ctx, secretPath, vaultData)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to store secret in vault",
			slog.String("tenant", tenant),
			slog.String("path", secretPath),
			slog.Any("error", err),
		)
		return fmt.Errorf("failed to store secret in vault: %w", err)
	}

	return nil
}

func (s *VaultSecretStore) Fetch(ctx context.Context, tenant, project, name string) (
	map[string][]byte, error) {
	if err := validatePathComponent(tenant, "tenant"); err != nil {
		return nil, err
	}
	if project != "" {
		if err := validatePathComponent(project, "project"); err != nil {
			return nil, err
		}
	}
	if err := validatePathComponent(name, "name"); err != nil {
		return nil, err
	}

	client, err := s.tenantClient(tenant)
	if err != nil {
		return nil, err
	}

	secretPath := s.secretPath(project, name)
	secret, err := client.KVv2(s.kvMountPath).Get(ctx, secretPath)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to fetch secret from vault",
			slog.String("tenant", tenant),
			slog.String("path", secretPath),
			slog.Any("error", err),
		)
		return nil, fmt.Errorf("failed to fetch secret from vault: %w", err)
	}

	result := make(map[string][]byte, len(secret.Data))
	for k, v := range secret.Data {
		str, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected type for secret data key %q: %T", k, v)
		}
		decoded, decodeErr := base64.StdEncoding.DecodeString(str)
		if decodeErr != nil {
			return nil, fmt.Errorf("failed to decode secret data key %q: %w", k, decodeErr)
		}
		result[k] = decoded
	}

	return result, nil
}

func (s *VaultSecretStore) Delete(ctx context.Context, tenant, project, name string) error {
	if err := validatePathComponent(tenant, "tenant"); err != nil {
		return err
	}
	if project != "" {
		if err := validatePathComponent(project, "project"); err != nil {
			return err
		}
	}
	if err := validatePathComponent(name, "name"); err != nil {
		return err
	}

	client, err := s.tenantClient(tenant)
	if err != nil {
		return err
	}

	secretPath := s.secretPath(project, name)
	err = client.KVv2(s.kvMountPath).DeleteMetadata(ctx, secretPath)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to delete secret from vault",
			slog.String("tenant", tenant),
			slog.String("path", secretPath),
			slog.Any("error", err),
		)
		return fmt.Errorf("failed to delete secret from vault: %w", err)
	}

	return nil
}

func (s *VaultSecretStore) tenantClient(tenant string) (*vaultapi.Client, error) {
	client, err := s.client.Clone()
	if err != nil {
		return nil, fmt.Errorf("failed to clone vault client: %w", err)
	}
	client.SetNamespace(path.Join(s.parentNamespace, tenant))
	return client, nil
}

func (s *VaultSecretStore) secretPath(project, name string) string {
	if project != "" {
		return path.Join(project, name)
	}
	return name
}

var pathComponentRegexp = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$`)

func validatePathComponent(value, label string) error {
	if value == "" {
		return fmt.Errorf("%s is mandatory", label)
	}
	if !pathComponentRegexp.MatchString(value) {
		return fmt.Errorf("%s contains invalid characters", label)
	}
	return nil
}
