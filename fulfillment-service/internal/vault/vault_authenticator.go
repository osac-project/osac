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
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/oauth"
	"github.com/osac-project/osac/fulfillment-service/internal/trust"
)

type AuthenticatorBuilder struct {
	logger               *slog.Logger
	vaultAddress         string
	vaultNamespace       string
	vaultAuthMountPath   string
	vaultRole            string
	keycloakIssuerURL    string
	keycloakClientID     string
	keycloakClientSecret string
	keycloakAudience     string
	caPool               *trust.CertPool
}

type Authenticator struct {
	logger             *slog.Logger
	vaultAddress       string
	vaultNamespace     string
	vaultAuthMountPath string
	vaultRole          string
	oauthTokenSource   *oauth.TokenSource
	httpClient         *http.Client

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

func (b *AuthenticatorBuilder) SetKeycloakIssuerURL(value string) *AuthenticatorBuilder {
	b.keycloakIssuerURL = value
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

func (b *AuthenticatorBuilder) SetKeycloakAudience(value string) *AuthenticatorBuilder {
	b.keycloakAudience = value
	return b
}

func (b *AuthenticatorBuilder) SetCaPool(value *trust.CertPool) *AuthenticatorBuilder {
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
	if b.keycloakIssuerURL == "" {
		err = errors.New("keycloak issuer URL is mandatory")
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

	tokenStore, err := auth.NewMemoryTokenStore().
		SetLogger(b.logger).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create token store: %w", err)
		return
	}

	tokenSourceBuilder := oauth.NewTokenSource().
		SetLogger(b.logger).
		SetFlow(oauth.CredentialsFlow).
		SetIssuer(b.keycloakIssuerURL).
		SetClientId(b.keycloakClientID).
		SetClientSecret(b.keycloakClientSecret).
		SetCaPool(b.caPool).
		SetStore(tokenStore)

	if b.keycloakAudience != "" {
		tokenSourceBuilder.SetAudience(b.keycloakAudience)
	}

	oauthTokenSource, oauthErr := tokenSourceBuilder.Build()
	if oauthErr != nil {
		err = fmt.Errorf("failed to create keycloak token source: %w", oauthErr)
		return
	}

	httpClient, httpErr := newHTTPClient(b.caPool)
	if httpErr != nil {
		err = httpErr
		return
	}

	result = &Authenticator{
		logger:             b.logger,
		vaultAddress:       b.vaultAddress,
		vaultNamespace:     b.vaultNamespace,
		vaultAuthMountPath: b.vaultAuthMountPath,
		vaultRole:          b.vaultRole,
		oauthTokenSource:   oauthTokenSource,
		httpClient:         httpClient,
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
		keycloakToken, kcErr := a.oauthTokenSource.Token(ctx)
		if kcErr != nil {
			return "", fmt.Errorf("failed to obtain keycloak token: %w", kcErr)
		}

		vaultToken, leaseDuration, err := a.loginToVault(ctx, keycloakToken.Access)
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

func (a *Authenticator) loginToVault(ctx context.Context, jwt string) (string, int, error) {
	return loginToVault(ctx, a.httpClient, a.vaultAddress, a.vaultNamespace, a.vaultAuthMountPath, a.vaultRole, jwt)
}
