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
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	applicationJSON           = "application/json"
	applicationFormURLencoded = "application/x-www-form-urlencoded"
	oauth2ClientCredentials   = "client_credentials"
	vaultNamespaceHeader      = "X-Vault-Namespace"
	contentTypeHeader         = "Content-Type"
)

type AuthenticatorBuilder struct {
	logger                *slog.Logger
	vaultAddress          string
	vaultNamespace        string
	vaultAuthMountPath    string
	vaultRole             string
	keycloakTokenEndpoint string
	keycloakClientID      string
	keycloakClientSecret  string
	caPool                *x509.CertPool
}

type Authenticator struct {
	logger                *slog.Logger
	vaultAddress          string
	vaultNamespace        string
	vaultAuthMountPath    string
	vaultRole             string
	keycloakTokenEndpoint string
	keycloakClientID      string
	keycloakClientSecret  string
	httpClient            *http.Client

	mu          sync.Mutex
	cachedToken string
	tokenExpiry time.Time
	sfGroup     singleflight.Group
}

func NewAuthenticator() *AuthenticatorBuilder {
	return &AuthenticatorBuilder{
		vaultAuthMountPath: "jwt",
	}
}

func (b *AuthenticatorBuilder) SetLogger(value *slog.Logger) *AuthenticatorBuilder {
	b.logger = value
	return b
}

func (b *AuthenticatorBuilder) SetVaultAddress(value string) *AuthenticatorBuilder {
	b.vaultAddress = value
	return b
}

func (b *AuthenticatorBuilder) SetVaultNamespace(value string) *AuthenticatorBuilder {
	b.vaultNamespace = value
	return b
}

func (b *AuthenticatorBuilder) SetVaultAuthMountPath(value string) *AuthenticatorBuilder {
	b.vaultAuthMountPath = value
	return b
}

func (b *AuthenticatorBuilder) SetVaultRole(value string) *AuthenticatorBuilder {
	b.vaultRole = value
	return b
}

func (b *AuthenticatorBuilder) SetKeycloakTokenEndpoint(value string) *AuthenticatorBuilder {
	b.keycloakTokenEndpoint = value
	return b
}

func (b *AuthenticatorBuilder) SetKeycloakClientID(value string) *AuthenticatorBuilder {
	b.keycloakClientID = value
	return b
}

func (b *AuthenticatorBuilder) SetKeycloakClientSecret(value string) *AuthenticatorBuilder {
	b.keycloakClientSecret = value
	return b
}

func (b *AuthenticatorBuilder) SetCaPool(value *x509.CertPool) *AuthenticatorBuilder {
	b.caPool = value
	return b
}

func (b *AuthenticatorBuilder) Build() (result *Authenticator, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.vaultAddress == "" {
		err = errors.New("vault address is mandatory")
		return
	}
	if b.vaultNamespace == "" {
		err = errors.New("vault namespace is mandatory")
		return
	}
	if b.vaultRole == "" {
		err = errors.New("vault role is mandatory")
		return
	}
	if b.keycloakTokenEndpoint == "" {
		err = errors.New("keycloak token endpoint is mandatory")
		return
	}
	if b.keycloakClientID == "" {
		err = errors.New("keycloak client ID is mandatory")
		return
	}
	if b.keycloakClientSecret == "" {
		err = errors.New("keycloak client secret is mandatory")
		return
	}
	if err = validatePathComponent(b.vaultAuthMountPath, "vault auth mount path"); err != nil {
		return
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}
	if b.caPool != nil {
		transport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			err = errors.New("unexpected default transport type")
			return
		}
		cloned := transport.Clone()
		cloned.TLSClientConfig.RootCAs = b.caPool
		httpClient.Transport = cloned
	}

	result = &Authenticator{
		logger:                b.logger,
		vaultAddress:          b.vaultAddress,
		vaultNamespace:        b.vaultNamespace,
		vaultAuthMountPath:    b.vaultAuthMountPath,
		vaultRole:             b.vaultRole,
		keycloakTokenEndpoint: b.keycloakTokenEndpoint,
		keycloakClientID:      b.keycloakClientID,
		keycloakClientSecret:  b.keycloakClientSecret,
		httpClient:            httpClient,
	}
	return
}

func (a *Authenticator) VaultToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	if a.cachedToken != "" && time.Until(a.tokenExpiry) > 30*time.Second {
		token := a.cachedToken
		a.mu.Unlock()
		return token, nil
	}
	a.mu.Unlock()

	result, err, _ := a.sfGroup.Do("vault-lifecycle-token", func() (any, error) {
		keycloakJWT, err := a.fetchKeycloakToken(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to obtain keycloak token: %w", err)
		}

		vaultToken, leaseDuration, err := a.loginToVault(ctx, keycloakJWT)
		if err != nil {
			return "", fmt.Errorf("failed to login to vault with JWT: %w", err)
		}

		a.mu.Lock()
		a.cachedToken = vaultToken
		a.tokenExpiry = time.Now().Add(time.Duration(leaseDuration) * time.Second)
		a.mu.Unlock()

		a.logger.InfoContext(ctx, "Authenticated to vault via keycloak")

		return vaultToken, nil
	})
	if err != nil {
		return "", err
	}
	return result.(string), nil
}

type keycloakTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

func (a *Authenticator) fetchKeycloakToken(ctx context.Context) (string, error) {
	form := url.Values{
		"grant_type":    {oauth2ClientCredentials},
		"client_id":     {a.keycloakClientID},
		"client_secret": {a.keycloakClientSecret},
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, a.keycloakTokenEndpoint,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create keycloak request: %w", err)
	}
	req.Header.Set(contentTypeHeader, applicationFormURLencoded)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("keycloak token request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read keycloak response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("keycloak returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var tokenResp keycloakTokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode keycloak response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", errors.New("keycloak returned empty access token")
	}

	return tokenResp.AccessToken, nil
}

type vaultLoginResponse struct {
	Auth *vaultAuthData `json:"auth"`
}

type vaultAuthData struct {
	ClientToken   string `json:"client_token"`
	LeaseDuration int    `json:"lease_duration"`
}

func (a *Authenticator) loginToVault(ctx context.Context, jwt string) (string, int, error) {
	loginURL := fmt.Sprintf("%s/v1/auth/%s/login", a.vaultAddress, a.vaultAuthMountPath)

	body, err := json.Marshal(map[string]string{
		"jwt":  jwt,
		"role": a.vaultRole,
	})
	if err != nil {
		return "", 0, fmt.Errorf("failed to marshal vault login request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, bytes.NewReader(body))
	if err != nil {
		return "", 0, fmt.Errorf("failed to create vault login request: %w", err)
	}
	req.Header.Set(contentTypeHeader, applicationJSON)
	req.Header.Set(vaultNamespaceHeader, a.vaultNamespace)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("vault login request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("failed to read vault response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("vault login returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var loginResp vaultLoginResponse
	if err := json.Unmarshal(respBody, &loginResp); err != nil {
		return "", 0, fmt.Errorf("failed to decode vault login response: %w", err)
	}

	if loginResp.Auth == nil || loginResp.Auth.ClientToken == "" {
		return "", 0, errors.New("vault login response missing auth token")
	}

	return loginResp.Auth.ClientToken, loginResp.Auth.LeaseDuration, nil
}
