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
	"fmt"
	"log/slog"

	"github.com/osac-project/osac/fulfillment-service/internal/trust"
)

// NewServiceTenantTokenSourceFromConfig creates a TenantTokenSource that
// authenticates to Vault using Keycloak client credentials. It reads the
// client secret from disk and builds a ServiceTenantTokenSource with
// per-tenant token caching.
func NewServiceTenantTokenSourceFromConfig(
	logger *slog.Logger,
	base BaseConfig,
	caPool *trust.CertPool,
) (TenantTokenSource, error) {
	if err := ValidateBaseKeycloakConfig(base); err != nil {
		return nil, err
	}

	keycloakClientSecret, err := readTrimmedFile(base.KeycloakClientSecretFile)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to read vault keycloak client secret from file '%s': %w",
			base.KeycloakClientSecretFile, err,
		)
	}

	logger.Info("Creating service tenant token source",
		slog.String("endpoint", base.Endpoint),
		slog.String("namespace", base.Namespace),
	)

	source, err := NewServiceTenantTokenSource().
		SetLogger(logger).
		SetVaultAddress(base.Endpoint).
		SetParentNamespace(base.Namespace).
		SetKeycloakIssuerURL(base.KeycloakIssuerURL).
		SetKeycloakClientID(base.KeycloakClientID).
		SetKeycloakClientSecret(keycloakClientSecret).
		SetKeycloakAudience(base.KeycloakAudience).
		SetCaPool(caPool).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create service tenant token source: %w", err)
	}

	return source, nil
}
