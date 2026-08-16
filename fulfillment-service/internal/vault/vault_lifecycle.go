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
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"

	vaultapi "github.com/hashicorp/vault/api"
)

// LifecycleClient manages tenant namespace lifecycle in a Vault-compatible secret store.
// Implementations handle creating and configuring per-tenant namespaces with KV v2
// secret engines, JWT auth methods, policies, and roles.
//
//go:generate mockgen -destination=vault_lifecycle_mock.go -package=vault . LifecycleClient
type LifecycleClient interface {
	// EnsureTenantNamespace creates a tenant namespace with KV v2, JWT auth, policy, and role.
	// Each step is idempotent — "already exists" errors are tolerated.
	EnsureTenantNamespace(ctx context.Context, tenantName string) error

	// DeleteTenantNamespace deletes a tenant namespace and all resources within it.
	// Deletion is idempotent — "not found" errors are tolerated.
	DeleteTenantNamespace(ctx context.Context, tenantName string) error
}

type VaultLifecycleClientBuilder struct {
	logger            *slog.Logger
	address           string
	tokenSource       VaultTokenSource
	parentNamespace   string
	kvMountPath       string
	keycloakIssuerURL string
	keycloakAudience  string
	caPool            *x509.CertPool
	caPEM             string
}

type VaultLifecycleClient struct {
	logger            *slog.Logger
	client            *vaultapi.Client
	tokenSource       VaultTokenSource
	parentNamespace   string
	kvMountPath       string
	keycloakIssuerURL string
	keycloakAudience  string
	caPEM             string
}

func NewVaultLifecycleClient() *VaultLifecycleClientBuilder {
	return &VaultLifecycleClientBuilder{
		kvMountPath:      "secret",
		keycloakAudience: "osac-api",
	}
}

func (b *VaultLifecycleClientBuilder) SetLogger(value *slog.Logger) *VaultLifecycleClientBuilder {
	b.logger = value
	return b
}

func (b *VaultLifecycleClientBuilder) SetAddress(value string) *VaultLifecycleClientBuilder {
	b.address = value
	return b
}

func (b *VaultLifecycleClientBuilder) SetTokenSource(value VaultTokenSource) *VaultLifecycleClientBuilder {
	b.tokenSource = value
	return b
}

func (b *VaultLifecycleClientBuilder) SetParentNamespace(value string) *VaultLifecycleClientBuilder {
	b.parentNamespace = value
	return b
}

func (b *VaultLifecycleClientBuilder) SetKVMountPath(value string) *VaultLifecycleClientBuilder {
	b.kvMountPath = value
	return b
}

func (b *VaultLifecycleClientBuilder) SetKeycloakIssuerURL(value string) *VaultLifecycleClientBuilder {
	b.keycloakIssuerURL = value
	return b
}

func (b *VaultLifecycleClientBuilder) SetKeycloakAudience(value string) *VaultLifecycleClientBuilder {
	b.keycloakAudience = value
	return b
}

func (b *VaultLifecycleClientBuilder) SetCaPool(value *x509.CertPool) *VaultLifecycleClientBuilder {
	b.caPool = value
	return b
}

func (b *VaultLifecycleClientBuilder) SetCaPEM(value string) *VaultLifecycleClientBuilder {
	b.caPEM = value
	return b
}

func (b *VaultLifecycleClientBuilder) Build() (result *VaultLifecycleClient, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.address == "" {
		err = errors.New("address is mandatory")
		return
	}
	if b.tokenSource == nil {
		err = errors.New("token source is mandatory")
		return
	}
	if b.parentNamespace == "" {
		err = errors.New("parent namespace is mandatory")
		return
	}
	if b.keycloakIssuerURL == "" {
		err = errors.New("keycloak issuer URL is mandatory")
		return
	}
	if err = validatePathComponent(b.kvMountPath, "KV mount path"); err != nil {
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

	client, clientErr := vaultapi.NewClient(config)
	if clientErr != nil {
		err = fmt.Errorf("failed to create vault client: %w", clientErr)
		return
	}

	result = &VaultLifecycleClient{
		logger:            b.logger,
		client:            client,
		tokenSource:       b.tokenSource,
		parentNamespace:   b.parentNamespace,
		kvMountPath:       b.kvMountPath,
		keycloakIssuerURL: b.keycloakIssuerURL,
		keycloakAudience:  b.keycloakAudience,
		caPEM:             b.caPEM,
	}
	return
}

func (c *VaultLifecycleClient) EnsureTenantNamespace(ctx context.Context, tenantName string) error {
	if err := validatePathComponent(tenantName, "tenant name"); err != nil {
		return err
	}

	parentClient, err := c.parentClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create parent namespace client: %w", err)
	}

	tenantClient, err := c.tenantClient(ctx, tenantName)
	if err != nil {
		return fmt.Errorf("failed to create tenant namespace client: %w", err)
	}

	if err := c.createNamespace(ctx, parentClient, tenantName); err != nil {
		return err
	}
	if err := c.mountKV(ctx, tenantClient, tenantName); err != nil {
		return err
	}
	if err := c.enableJWTAuth(ctx, tenantClient, tenantName); err != nil {
		return err
	}
	if err := c.configureJWTAuth(ctx, tenantClient, tenantName); err != nil {
		return err
	}
	if err := c.createPolicy(ctx, tenantClient, tenantName); err != nil {
		return err
	}
	if err := c.createRole(ctx, tenantClient, tenantName); err != nil {
		return err
	}

	c.logger.InfoContext(ctx, "Tenant vault namespace provisioned",
		slog.String("tenant", tenantName),
		slog.String("namespace", path.Join(c.parentNamespace, tenantName)),
	)
	return nil
}

func (c *VaultLifecycleClient) DeleteTenantNamespace(ctx context.Context, tenantName string) error {
	if err := validatePathComponent(tenantName, "tenant name"); err != nil {
		return err
	}

	parentClient, err := c.parentClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create parent namespace client: %w", err)
	}

	_, err = parentClient.Logical().DeleteWithContext(ctx,
		fmt.Sprintf("sys/namespaces/%s", tenantName))
	if err != nil && !isNotFoundError(err) {
		return fmt.Errorf("failed to delete namespace %q: %w", tenantName, err)
	}

	c.logger.InfoContext(ctx, "Tenant vault namespace deleted",
		slog.String("tenant", tenantName),
		slog.String("namespace", path.Join(c.parentNamespace, tenantName)),
	)
	return nil
}

func (c *VaultLifecycleClient) createNamespace(ctx context.Context, client *vaultapi.Client,
	tenantName string) error {
	_, err := client.Logical().WriteWithContext(ctx,
		fmt.Sprintf("sys/namespaces/%s", tenantName), nil)
	if err != nil && !isAlreadyExistsError(err) {
		return fmt.Errorf("failed to create namespace %q: %w", tenantName, err)
	}
	return nil
}

func (c *VaultLifecycleClient) mountKV(ctx context.Context, client *vaultapi.Client,
	tenantName string) error {
	err := client.Sys().MountWithContext(ctx, c.kvMountPath, &vaultapi.MountInput{
		Type:    "kv",
		Options: map[string]string{"version": "2"},
	})
	if err != nil && !isAlreadyExistsError(err) {
		return fmt.Errorf("failed to mount KV v2 for tenant %q: %w", tenantName, err)
	}
	return nil
}

func (c *VaultLifecycleClient) enableJWTAuth(ctx context.Context, client *vaultapi.Client,
	tenantName string) error {
	err := client.Sys().EnableAuthWithOptionsWithContext(ctx, "jwt", &vaultapi.EnableAuthOptions{
		Type: "jwt",
	})
	if err != nil && !isAlreadyExistsError(err) {
		return fmt.Errorf("failed to enable JWT auth for tenant %q: %w", tenantName, err)
	}
	return nil
}

func (c *VaultLifecycleClient) configureJWTAuth(ctx context.Context, client *vaultapi.Client,
	tenantName string) error {
	config := map[string]any{
		"oidc_discovery_url": c.keycloakIssuerURL,
		"default_role":       "tenant-access",
	}
	if c.caPEM != "" {
		config["oidc_discovery_ca_pem"] = c.caPEM
	}
	_, err := client.Logical().WriteWithContext(ctx, "auth/jwt/config", config)
	if err != nil {
		return fmt.Errorf("failed to configure JWT auth for tenant %q: %w", tenantName, err)
	}
	return nil
}

func (c *VaultLifecycleClient) createPolicy(ctx context.Context, client *vaultapi.Client,
	tenantName string) error {
	policy := fmt.Sprintf(`path "%s/data/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}
path "%s/metadata/*" {
  capabilities = ["read", "delete", "list"]
}`, c.kvMountPath, c.kvMountPath)

	err := client.Sys().PutPolicyWithContext(ctx, "tenant-kv-access", policy)
	if err != nil {
		return fmt.Errorf("failed to create policy for tenant %q: %w", tenantName, err)
	}
	return nil
}

func (c *VaultLifecycleClient) createRole(ctx context.Context, client *vaultapi.Client,
	tenantName string) error {
	_, err := client.Logical().WriteWithContext(ctx, "auth/jwt/role/tenant-access", map[string]any{
		"role_type":       "jwt",
		"bound_audiences": []string{c.keycloakAudience},
		"bound_claims": map[string]any{
			"organization": []string{tenantName},
		},
		"user_claim": "sub",
		"policies":   []string{"tenant-kv-access"},
	})
	if err != nil {
		return fmt.Errorf("failed to create role for tenant %q: %w", tenantName, err)
	}
	return nil
}

func (c *VaultLifecycleClient) parentClient(ctx context.Context) (*vaultapi.Client, error) {
	token, err := c.tokenSource.VaultToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain vault token: %w", err)
	}
	client, err := c.client.Clone()
	if err != nil {
		return nil, fmt.Errorf("failed to clone vault client: %w", err)
	}
	client.SetToken(token)
	client.SetNamespace(c.parentNamespace)
	return client, nil
}

func (c *VaultLifecycleClient) tenantClient(ctx context.Context, tenantName string) (*vaultapi.Client, error) {
	token, err := c.tokenSource.VaultToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain vault token: %w", err)
	}
	client, err := c.client.Clone()
	if err != nil {
		return nil, fmt.Errorf("failed to clone vault client: %w", err)
	}
	client.SetToken(token)
	client.SetNamespace(path.Join(c.parentNamespace, tenantName))
	return client, nil
}

func isAlreadyExistsError(err error) bool {
	var respErr *vaultapi.ResponseError
	if errors.As(err, &respErr) && respErr.StatusCode == http.StatusBadRequest {
		body := strings.Join(respErr.Errors, " ")
		return strings.Contains(body, "already exists") ||
			strings.Contains(body, "existing mount") ||
			strings.Contains(body, "path is already in use")
	}
	return false
}

func isNotFoundError(err error) bool {
	var respErr *vaultapi.ResponseError
	if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
		return true
	}
	return false
}
