/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package idpclient

import (
	"context"
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

// Config contains the credentials and TLS settings for a Keycloak management client.
type Config struct {
	Logger           *slog.Logger
	BaseURL          string
	IssuerURL        string
	ClientID         string
	ClientIDFile     string
	ClientSecret     string
	ClientSecretFile string
	CAPool           *trust.CertPool
}

// New builds an IDP client using the supplied service-account credentials.
func New(ctx context.Context, config Config) (*idp.Client, error) {
	if config.Logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if config.BaseURL == "" {
		return nil, errors.New("flag '--idp-url' is required")
	}
	if config.IssuerURL == "" {
		return nil, errors.New("flag '--auth-issuer-url' is required")
	}
	clientID, err := credential("idp-client-id", config.ClientID, config.ClientIDFile)
	if err != nil {
		return nil, err
	}
	clientSecret, err := credential("idp-client-secret", config.ClientSecret, config.ClientSecretFile)
	if err != nil {
		return nil, err
	}
	store, err := auth.NewMemoryTokenStore().SetLogger(config.Logger).Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create IDP token store: %w", err)
	}
	source, err := oauth.NewTokenSource().SetLogger(config.Logger).SetStore(store).SetCaPool(config.CAPool).
		SetIssuer(config.IssuerURL).SetFlow(oauth.CredentialsFlow).SetClientId(clientID).SetClientSecret(clientSecret).Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create IDP token source: %w", err)
	}

	config.Logger.InfoContext(ctx, "Creating Keycloak IDP client")
	client, err := idp.NewClient().SetLogger(config.Logger).SetBaseURL(config.BaseURL).
		SetTokenSource(source).SetCaPool(config.CAPool).Build()
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
