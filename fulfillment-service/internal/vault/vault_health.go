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

	vaultapi "github.com/hashicorp/vault/api"
)

type HealthCheckerBuilder struct {
	logger  *slog.Logger
	address string
	caPool  *x509.CertPool
}

type HealthChecker struct {
	logger *slog.Logger
	client *vaultapi.Client
}

func NewHealthChecker() *HealthCheckerBuilder {
	return &HealthCheckerBuilder{}
}

func (b *HealthCheckerBuilder) SetLogger(value *slog.Logger) *HealthCheckerBuilder {
	b.logger = value
	return b
}

func (b *HealthCheckerBuilder) SetAddress(value string) *HealthCheckerBuilder {
	b.address = value
	return b
}

func (b *HealthCheckerBuilder) SetCaPool(value *x509.CertPool) *HealthCheckerBuilder {
	b.caPool = value
	return b
}

func (b *HealthCheckerBuilder) Build() (result *HealthChecker, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.address == "" {
		err = errors.New("address is mandatory")
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
	result = &HealthChecker{
		logger: b.logger,
		client: client,
	}
	return
}

func (c *HealthChecker) Check(ctx context.Context) error {
	health, err := c.client.Sys().HealthWithContext(ctx)
	if err != nil {
		return fmt.Errorf("vault health request failed: %w", err)
	}
	if !health.Initialized {
		return fmt.Errorf("vault is not initialized")
	}
	if health.Sealed {
		return fmt.Errorf("vault is sealed")
	}
	c.logger.InfoContext(ctx, "Vault health check passed",
		slog.String("version", health.Version),
	)
	return nil
}
