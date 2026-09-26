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
	"path"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/oauth"
	"github.com/osac-project/osac/fulfillment-service/internal/trust"
)

type ServiceTenantTokenSourceBuilder struct {
	logger               *slog.Logger
	vaultAddress         string
	parentNamespace      string
	keycloakIssuerURL    string
	keycloakClientID     string
	keycloakClientSecret string
	keycloakAudience     string
	caPool               *trust.CertPool
}

type ServiceTenantTokenSource struct {
	logger           *slog.Logger
	vaultAddress     string
	parentNamespace  string
	oauthTokenSource *oauth.TokenSource
	httpClient       *http.Client

	mu           sync.Mutex
	tenantTokens map[string]cachedToken

	sfGroup singleflight.Group
}

type cachedToken struct {
	token  string
	expiry time.Time
}

func NewServiceTenantTokenSource() *ServiceTenantTokenSourceBuilder {
	return &ServiceTenantTokenSourceBuilder{}
}

func (b *ServiceTenantTokenSourceBuilder) SetLogger(value *slog.Logger) *ServiceTenantTokenSourceBuilder {
	b.logger = value
	return b
}

func (b *ServiceTenantTokenSourceBuilder) SetVaultAddress(value string) *ServiceTenantTokenSourceBuilder {
	b.vaultAddress = value
	return b
}

func (b *ServiceTenantTokenSourceBuilder) SetParentNamespace(value string) *ServiceTenantTokenSourceBuilder {
	b.parentNamespace = value
	return b
}

func (b *ServiceTenantTokenSourceBuilder) SetKeycloakIssuerURL(value string) *ServiceTenantTokenSourceBuilder {
	b.keycloakIssuerURL = value
	return b
}

func (b *ServiceTenantTokenSourceBuilder) SetKeycloakClientID(value string) *ServiceTenantTokenSourceBuilder {
	b.keycloakClientID = value
	return b
}

func (b *ServiceTenantTokenSourceBuilder) SetKeycloakClientSecret(value string) *ServiceTenantTokenSourceBuilder {
	b.keycloakClientSecret = value
	return b
}

func (b *ServiceTenantTokenSourceBuilder) SetKeycloakAudience(value string) *ServiceTenantTokenSourceBuilder {
	b.keycloakAudience = value
	return b
}

func (b *ServiceTenantTokenSourceBuilder) SetCaPool(value *trust.CertPool) *ServiceTenantTokenSourceBuilder {
	b.caPool = value
	return b
}

func (b *ServiceTenantTokenSourceBuilder) Build() (result *ServiceTenantTokenSource, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.vaultAddress == "" {
		err = errors.New("vault address is mandatory")
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
	if b.keycloakClientID == "" {
		err = errors.New("keycloak client ID is mandatory")
		return
	}
	if b.keycloakClientSecret == "" {
		err = errors.New("keycloak client secret is mandatory")
		return
	}

	tokenStore, err := auth.NewMemoryTokenStore().
		SetLogger(b.logger).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create token store: %w", err)
		return
	}

	oauthTokenSource, oauthErr := oauth.NewTokenSource().
		SetLogger(b.logger).
		SetFlow(oauth.CredentialsFlow).
		SetIssuer(b.keycloakIssuerURL).
		SetClientId(b.keycloakClientID).
		SetClientSecret(b.keycloakClientSecret).
		SetAudience(b.keycloakAudience).
		SetCaPool(b.caPool).
		SetStore(tokenStore).
		Build()
	if oauthErr != nil {
		err = fmt.Errorf("failed to create keycloak token source: %w", oauthErr)
		return
	}

	httpClient, httpErr := newHTTPClient(b.caPool)
	if httpErr != nil {
		err = httpErr
		return
	}

	result = &ServiceTenantTokenSource{
		logger:           b.logger,
		vaultAddress:     b.vaultAddress,
		parentNamespace:  b.parentNamespace,
		oauthTokenSource: oauthTokenSource,
		httpClient:       httpClient,
		tenantTokens:     make(map[string]cachedToken),
	}
	return
}

func (s *ServiceTenantTokenSource) VaultToken(ctx context.Context, tenant string) (string, error) {
	if tenant == "" {
		return "", errors.New("tenant is required for service vault authentication")
	}

	s.mu.Lock()
	if cached, ok := s.tenantTokens[tenant]; ok && time.Until(cached.expiry) > 30*time.Second {
		token := cached.token
		s.mu.Unlock()
		return token, nil
	}
	s.mu.Unlock()

	result, err, _ := s.sfGroup.Do(tenant, func() (any, error) {
		keycloakToken, kcErr := s.oauthTokenSource.Token(ctx)
		if kcErr != nil {
			return "", fmt.Errorf("failed to obtain keycloak token: %w", kcErr)
		}

		namespace := path.Join(s.parentNamespace, tenant)
		vaultToken, leaseDuration, loginErr := loginToVault(
			ctx,
			s.httpClient,
			s.vaultAddress,
			namespace,
			TenantAuthMountPath,
			ServiceAuthRole,
			keycloakToken.Access,
		)
		if loginErr != nil {
			return "", fmt.Errorf("failed to login to vault for tenant %q: %w", tenant, loginErr)
		}

		ttl := time.Duration(leaseDuration) * time.Second
		if ttl <= 0 {
			ttl = 5 * time.Minute
		}

		s.mu.Lock()
		s.tenantTokens[tenant] = cachedToken{
			token:  vaultToken,
			expiry: time.Now().Add(ttl),
		}
		s.mu.Unlock()

		s.logger.DebugContext(ctx, "Service authenticated to vault for tenant",
			slog.String("tenant", tenant),
		)

		return vaultToken, nil
	})
	if err != nil {
		return "", err
	}
	return result.(string), nil
}

func (s *ServiceTenantTokenSource) InvalidateTenantToken(tenant string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tenantTokens, tenant)
}
