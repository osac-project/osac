/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

const fulfillmentOAuthRequestTimeout = 10 * time.Second

type fulfillmentOAuthConfig struct {
	issuerURL        string
	clientIDFile     string
	clientSecretFile string
	caFile           string
}

// rotatingFulfillmentTokenSource watches the projected credentials and CA
// bundle, rebuilding its client-credentials source when Kubernetes rotates
// either one.
type rotatingFulfillmentTokenSource struct {
	cfg        fulfillmentOAuthConfig
	tokenURL   string
	httpClient *http.Client
	caHash     string

	mu           sync.Mutex
	clientID     string
	clientSecret string
	source       oauth2.TokenSource
}

func newFulfillmentTokenSource(cfg fulfillmentOAuthConfig) (oauth2.TokenSource, error) {
	httpClient, caHash, err := fulfillmentOAuthHTTPClient(cfg.caFile)
	if err != nil {
		return nil, err
	}
	return newFulfillmentTokenSourceWithHTTPClientAndCAHash(cfg, httpClient, caHash)
}

func newFulfillmentTokenSourceWithHTTPClient(cfg fulfillmentOAuthConfig, httpClient *http.Client) (oauth2.TokenSource, error) {
	return newFulfillmentTokenSourceWithHTTPClientAndCAHash(cfg, httpClient, "")
}

func newFulfillmentTokenSourceWithHTTPClientAndCAHash(cfg fulfillmentOAuthConfig, httpClient *http.Client, caHash string) (oauth2.TokenSource, error) {
	tokenURL, err := buildFulfillmentTokenURL(cfg.issuerURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.clientIDFile) == "" {
		return nil, fmt.Errorf("fulfillment client ID file is required")
	}
	if strings.TrimSpace(cfg.clientSecretFile) == "" {
		return nil, fmt.Errorf("fulfillment client secret file is required")
	}
	if httpClient == nil {
		return nil, fmt.Errorf("fulfillment OAuth HTTP client is required")
	}
	return &rotatingFulfillmentTokenSource{
		cfg:        cfg,
		tokenURL:   tokenURL,
		httpClient: httpClient,
		caHash:     caHash,
	}, nil
}

func buildFulfillmentTokenURL(rawIssuerURL string) (string, error) {
	issuer, err := url.Parse(strings.TrimSpace(rawIssuerURL))
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil ||
		issuer.RawQuery != "" || issuer.Fragment != "" {
		return "", fmt.Errorf("fulfillment issuer URL must be an absolute HTTPS URL without credentials, query, or fragment")
	}
	tokenURL, err := url.JoinPath(issuer.String(), "protocol/openid-connect/token")
	if err != nil {
		return "", fmt.Errorf("building fulfillment token endpoint URL")
	}
	return tokenURL, nil
}

func fulfillmentOAuthHTTPClient(caFile string) (*http.Client, string, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	caHash := ""
	if caFile != "" {
		pemData, err := os.ReadFile(caFile)
		if err != nil {
			return nil, "", fmt.Errorf("reading fulfillment OAuth CA bundle: %w", err)
		}
		rootCAs, err := verifiedCAPool(pemData)
		if err != nil {
			return nil, "", fmt.Errorf("loading fulfillment OAuth CA bundle: %w", err)
		}
		tlsConfig.RootCAs = rootCAs
		sum := sha256.Sum256(pemData)
		caHash = hex.EncodeToString(sum[:])
	} else {
		rootCAs, err := x509.SystemCertPool()
		if err != nil {
			return nil, "", fmt.Errorf("loading system CA certificates: %w", err)
		}
		tlsConfig.RootCAs = rootCAs
	}
	transport.TLSClientConfig = tlsConfig
	return &http.Client{
		Transport: transport,
		Timeout:   fulfillmentOAuthRequestTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, caHash, nil
}

func (s *rotatingFulfillmentTokenSource) Token() (*oauth2.Token, error) {
	clientID, err := readFulfillmentCredential(s.cfg.clientIDFile, "client ID")
	if err != nil {
		return nil, err
	}
	clientSecret, err := readFulfillmentCredential(s.cfg.clientSecretFile, "client secret")
	if err != nil {
		return nil, err
	}
	caHash, err := fulfillmentOAuthCABundleHash(s.cfg.caFile)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.source == nil || clientID != s.clientID || clientSecret != s.clientSecret || caHash != s.caHash {
		httpClient := s.httpClient
		loadedCAHash := s.caHash
		if caHash != s.caHash {
			var err error
			httpClient, loadedCAHash, err = fulfillmentOAuthHTTPClient(s.cfg.caFile)
			if err != nil {
				s.mu.Unlock()
				return nil, err
			}
		}
		ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
		s.source = (&clientcredentials.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			TokenURL:     s.tokenURL,
			AuthStyle:    oauth2.AuthStyleInHeader,
		}).TokenSource(ctx)
		s.clientID = clientID
		s.clientSecret = clientSecret
		if httpClient != s.httpClient {
			s.httpClient.CloseIdleConnections()
			s.httpClient = httpClient
		}
		s.caHash = loadedCAHash
	}
	source := s.source
	s.mu.Unlock()

	token, err := source.Token()
	if err != nil {
		// OAuth errors can contain response bodies. Keep credentials and tokens
		// out of application logs by returning a sanitized error.
		return nil, fmt.Errorf("requesting fulfillment access token failed")
	}
	if token == nil || token.AccessToken == "" || token.Expiry.IsZero() {
		return nil, fmt.Errorf("fulfillment issuer returned a token without a valid expiry")
	}
	return token, nil
}

func fulfillmentOAuthCABundleHash(caFile string) (string, error) {
	if caFile == "" {
		return "", nil
	}
	pemData, err := os.ReadFile(caFile)
	if err != nil {
		return "", fmt.Errorf("reading fulfillment OAuth CA bundle: %w", err)
	}
	sum := sha256.Sum256(pemData)
	return hex.EncodeToString(sum[:]), nil
}

func readFulfillmentCredential(path, label string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading fulfillment %s file %s: %w", label, path, err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("fulfillment %s file %s is empty", label, path)
	}
	return value, nil
}
