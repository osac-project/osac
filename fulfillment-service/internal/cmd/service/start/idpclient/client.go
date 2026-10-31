//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package idpclient

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/idp"
	"github.com/osac-project/osac/fulfillment-service/internal/oauth"
	"github.com/osac-project/osac/fulfillment-service/internal/trust"
)

// ClientBuilder configures a Keycloak management client.
type ClientBuilder struct {
	logger           *slog.Logger
	baseURL          string
	issuerURL        string
	clientID         string
	clientIDFile     string
	clientSecret     string
	clientSecretFile string
	caPool           *trust.CertPool
}

// NewClient creates a builder for a Keycloak management client.
func NewClient() *ClientBuilder {
	return &ClientBuilder{}
}

func (b *ClientBuilder) SetLogger(value *slog.Logger) *ClientBuilder {
	b.logger = value
	return b
}

func (b *ClientBuilder) SetBaseURL(value string) *ClientBuilder {
	b.baseURL = value
	return b
}

func (b *ClientBuilder) SetIssuerURL(value string) *ClientBuilder {
	b.issuerURL = value
	return b
}

func (b *ClientBuilder) SetClientID(value string) *ClientBuilder {
	b.clientID = value
	return b
}

func (b *ClientBuilder) SetClientIDFile(value string) *ClientBuilder {
	b.clientIDFile = value
	return b
}

func (b *ClientBuilder) SetClientSecret(value string) *ClientBuilder {
	b.clientSecret = value
	return b
}

func (b *ClientBuilder) SetClientSecretFile(value string) *ClientBuilder {
	b.clientSecretFile = value
	return b
}

func (b *ClientBuilder) SetCaPool(value *trust.CertPool) *ClientBuilder {
	b.caPool = value
	return b
}

// Build creates an IDP client using the configured service-account credentials.
func (b *ClientBuilder) Build() (*idp.Client, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.baseURL == "" {
		return nil, errors.New("flag '--idp-url' is required")
	}
	if b.issuerURL == "" {
		return nil, errors.New("flag '--auth-issuer-url' is required")
	}
	clientID, err := credential("idp-client-id", b.clientID, b.clientIDFile)
	if err != nil {
		return nil, err
	}
	clientSecret, err := credential("idp-client-secret", b.clientSecret, b.clientSecretFile)
	if err != nil {
		return nil, err
	}
	store, err := auth.NewMemoryTokenStore().SetLogger(b.logger).Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create IDP token store: %w", err)
	}
	source, err := oauth.NewTokenSource().SetLogger(b.logger).SetStore(store).SetCaPool(b.caPool).
		SetIssuer(b.issuerURL).SetFlow(oauth.CredentialsFlow).SetClientId(clientID).SetClientSecret(clientSecret).Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create IDP token source: %w", err)
	}

	b.logger.Info("Creating Keycloak IDP client")
	client, err := idp.NewClient().SetLogger(b.logger).SetBaseURL(b.baseURL).
		SetTokenSource(source).SetCaPool(b.caPool).Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create Keycloak client: %w", err)
	}
	return client, nil
}

func credential(flag, value, path string) (string, error) {
	if value != "" && path != "" {
		return "", fmt.Errorf("flags '--%s' and '--%s-file' are mutually exclusive", flag, flag)
	}
	if path != "" {
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return "", fmt.Errorf("failed to read '--%s-file': %w", flag, err)
		}
		value = strings.TrimSpace(string(data))
	}
	if value == "" {
		return "", fmt.Errorf("flag '--%s' or a nonempty '--%s-file' is required", flag, flag)
	}
	return value, nil
}
