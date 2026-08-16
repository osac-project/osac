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
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// NewLifecycleClientFromConfig creates a fully wired LifecycleClient from the
// base and lifecycle configuration. It validates the config, reads the Keycloak
// client secret from disk, creates the Vault authenticator, and builds the
// lifecycle client. The caller provides the CA pool (which should already
// include any vault-specific CA certificates).
func NewLifecycleClientFromConfig(
	logger *slog.Logger,
	base BaseConfig,
	lifecycle LifecycleConfig,
	caPool *x509.CertPool,
) (LifecycleClient, error) {
	if err := ValidateLifecycleConfig(lifecycle); err != nil {
		return nil, err
	}

	keycloakClientSecret, err := readTrimmedFile(lifecycle.KeycloakClientSecretFile)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to read vault keycloak client secret from file '%s': %w",
			lifecycle.KeycloakClientSecretFile, err,
		)
	}

	logger.Info("Creating vault authenticator",
		slog.String("endpoint", base.Endpoint),
		slog.String("namespace", base.Namespace),
		slog.String("role", lifecycle.Role),
		slog.String("mount_path", lifecycle.MountPath),
	)

	authenticator, err := NewAuthenticator().
		SetLogger(logger).
		SetVaultAddress(base.Endpoint).
		SetVaultNamespace(base.Namespace).
		SetVaultAuthMountPath(lifecycle.MountPath).
		SetVaultRole(lifecycle.Role).
		SetKeycloakTokenEndpoint(lifecycle.KeycloakTokenEndpoint).
		SetKeycloakClientID(lifecycle.KeycloakClientID).
		SetKeycloakClientSecret(keycloakClientSecret).
		SetCaPool(caPool).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create vault authenticator: %w", err)
	}

	logger.Info("Creating vault lifecycle client",
		slog.String("endpoint", base.Endpoint),
		slog.String("namespace", base.Namespace),
	)

	builder := NewVaultLifecycleClient().
		SetLogger(logger).
		SetAddress(base.Endpoint).
		SetTokenSource(authenticator).
		SetParentNamespace(base.Namespace).
		SetKVMountPath(base.KVMountPath).
		SetKeycloakIssuerURL(lifecycle.KeycloakIssuerURL).
		SetKeycloakAudience(lifecycle.KeycloakAudience).
		SetCaPool(caPool)
	if base.CaCertFile != "" {
		caPEM, readErr := readTrimmedFile(base.CaCertFile)
		if readErr != nil {
			return nil, fmt.Errorf(
				"failed to read vault CA cert from file '%s': %w",
				base.CaCertFile, readErr,
			)
		}
		builder.SetCaPEM(caPEM)
	}
	client, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create vault lifecycle client: %w", err)
	}

	return client, nil
}

func readTrimmedFile(file string) (string, error) {
	data, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
