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
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
)

func TestBuildFulfillmentTokenURL(t *testing.T) {
	tests := []struct {
		name      string
		issuerURL string
		want      string
		wantErr   bool
	}{
		{
			name:      "realm issuer",
			issuerURL: "https://keycloak.example.com/realms/osac",
			want:      "https://keycloak.example.com/realms/osac/protocol/openid-connect/token",
		},
		{
			name:      "issuer with trailing slash",
			issuerURL: "https://keycloak.example.com/realms/osac/",
			want:      "https://keycloak.example.com/realms/osac/protocol/openid-connect/token",
		},
		{name: "http issuer", issuerURL: "http://keycloak.example.com/realms/osac", wantErr: true},
		{name: "relative issuer", issuerURL: "/realms/osac", wantErr: true},
		{name: "userinfo", issuerURL: "https://user:password@keycloak.example.com/realms/osac", wantErr: true},
		{name: "query", issuerURL: "https://keycloak.example.com/realms/osac?x=y", wantErr: true},
		{name: "fragment", issuerURL: "https://keycloak.example.com/realms/osac#fragment", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildFulfillmentTokenURL(tt.issuerURL)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("buildFulfillmentTokenURL(%q) succeeded; want error", tt.issuerURL)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildFulfillmentTokenURL(%q): %v", tt.issuerURL, err)
			}
			if got != tt.want {
				t.Fatalf("buildFulfillmentTokenURL(%q) = %q, want %q", tt.issuerURL, got, tt.want)
			}
		})
	}
}

func TestFulfillmentTokenSourceAcquiresUsesCachesAndRefreshesToken(t *testing.T) {
	clientIDPath, clientSecretPath := writeFulfillmentCredentials(t, "metering-client", "initial-secret")

	var mu sync.Mutex
	var requests []struct {
		clientID string
		secret   string
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/realms/osac/protocol/openid-connect/token" {
			t.Errorf("unexpected token endpoint path %q", r.URL.Path)
		}
		clientID, secret, ok := r.BasicAuth()
		if !ok {
			t.Error("token request did not use HTTP Basic client authentication")
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse token request form: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "client_credentials" {
			t.Errorf("grant_type = %q, want client_credentials", got)
		}

		mu.Lock()
		requests = append(requests, struct {
			clientID string
			secret   string
		}{clientID: clientID, secret: secret})
		requestNumber := len(requests)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"service-token-%d","token_type":"Bearer","expires_in":3600}`, requestNumber)
	}))
	t.Cleanup(server.Close)
	caFile := filepath.Join(t.TempDir(), "keycloak-ca.pem")
	caBundle := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(caFile, caBundle, 0o600); err != nil {
		t.Fatalf("write test CA bundle: %v", err)
	}

	tokenSource, err := newFulfillmentTokenSource(fulfillmentOAuthConfig{
		issuerURL:        server.URL + "/realms/osac",
		clientIDFile:     clientIDPath,
		clientSecretFile: clientSecretPath,
		caFile:           caFile,
	})
	if err != nil {
		t.Fatalf("newFulfillmentTokenSourceWithHTTPClient: %v", err)
	}

	first, err := tokenSource.Token()
	if err != nil {
		t.Fatalf("acquire initial token: %v", err)
	}
	if first.AccessToken != "service-token-1" {
		t.Fatal("initial access token did not match the token response")
	}
	if first.TokenType != "Bearer" || first.Expiry.IsZero() {
		t.Fatal("initial token is missing its bearer type or expiry")
	}

	metadataContext := credentials.NewContextWithRequestInfo(context.Background(), credentials.RequestInfo{
		Method:   "/osac.private.v1.Events/Watch",
		AuthInfo: credentials.TLSInfo{CommonAuthInfo: credentials.CommonAuthInfo{SecurityLevel: credentials.PrivacyAndIntegrity}},
	})
	metadata, err := (oauth.TokenSource{TokenSource: tokenSource}).GetRequestMetadata(metadataContext, "https://fulfillment.example.com")
	if err != nil {
		t.Fatalf("build gRPC request metadata: %v", err)
	}
	if got := metadata["authorization"]; got != "Bearer service-token-1" {
		t.Fatal("gRPC authorization metadata did not use the acquired bearer token")
	}

	cached, err := tokenSource.Token()
	if err != nil {
		t.Fatalf("reuse token: %v", err)
	}
	if cached.AccessToken != first.AccessToken {
		t.Fatal("token source did not reuse the unexpired access token")
	}
	if got := tokenRequestCount(&mu, &requests); got != 1 {
		t.Fatalf("token endpoint request count after reuse = %d, want 1", got)
	}

	if err := os.WriteFile(clientIDPath, []byte("rotated-client"), 0o600); err != nil {
		t.Fatalf("rotate client ID: %v", err)
	}
	if err := os.WriteFile(clientSecretPath, []byte("rotated-secret"), 0o600); err != nil {
		t.Fatalf("rotate client secret: %v", err)
	}
	rotated, err := tokenSource.Token()
	if err != nil {
		t.Fatalf("acquire token after credential rotation: %v", err)
	}
	if rotated.AccessToken != "service-token-2" {
		t.Fatal("token source did not acquire a new token after credential rotation")
	}
	if got := tokenRequestCount(&mu, &requests); got != 2 {
		t.Fatalf("token endpoint request count after credential rotation = %d, want 2", got)
	}

	rotated.Expiry = time.Now().Add(-time.Minute)
	refreshed, err := tokenSource.Token()
	if err != nil {
		t.Fatalf("refresh expired token: %v", err)
	}
	if refreshed.AccessToken != "service-token-3" {
		t.Fatal("token source did not refresh the expired access token")
	}

	mu.Lock()
	gotRequests := append([]struct {
		clientID string
		secret   string
	}(nil), requests...)
	mu.Unlock()
	if len(gotRequests) != 3 {
		t.Fatalf("token endpoint received %d requests, want 3", len(gotRequests))
	}
	if gotRequests[0].clientID != "metering-client" || gotRequests[0].secret != "initial-secret" {
		t.Fatal("token endpoint did not receive the configured initial credentials")
	}
	if gotRequests[1].clientID != "rotated-client" || gotRequests[1].secret != "rotated-secret" {
		t.Fatal("token endpoint did not receive the rotated credentials")
	}
	if gotRequests[2].clientID != "rotated-client" || gotRequests[2].secret != "rotated-secret" {
		t.Fatal("token refresh did not use the rotated credentials")
	}
}

func TestFulfillmentTokenSourceReloadsRotatedCABundle(t *testing.T) {
	clientIDPath, clientSecretPath := writeFulfillmentCredentials(t, "metering-client", "client-secret")

	var mu sync.Mutex
	requestCount := 0
	issuerServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requestCount++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"rotated-ca-token","token_type":"Bearer","expires_in":3600}`)
	}))
	t.Cleanup(issuerServer.Close)

	caFile := filepath.Join(t.TempDir(), "keycloak-ca.pem")
	oldCABundle := makeTestCA(t)
	if err := os.WriteFile(caFile, oldCABundle, 0o600); err != nil {
		t.Fatalf("write initial test CA bundle: %v", err)
	}

	tokenSource, err := newFulfillmentTokenSource(fulfillmentOAuthConfig{
		issuerURL:        issuerServer.URL + "/realms/osac",
		clientIDFile:     clientIDPath,
		clientSecretFile: clientSecretPath,
		caFile:           caFile,
	})
	if err != nil {
		t.Fatalf("newFulfillmentTokenSource: %v", err)
	}
	if _, err := tokenSource.Token(); err == nil {
		t.Fatal("token acquisition with the old CA bundle succeeded; want TLS verification error")
	}
	mu.Lock()
	initialRequestCount := requestCount
	mu.Unlock()
	if initialRequestCount != 0 {
		t.Fatalf("issuer received %d requests with an untrusted certificate, want 0", initialRequestCount)
	}

	rotatedCABundle := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issuerServer.Certificate().Raw})
	if err := os.WriteFile(caFile, rotatedCABundle, 0o600); err != nil {
		t.Fatalf("write rotated test CA bundle: %v", err)
	}
	token, err := tokenSource.Token()
	if err != nil {
		t.Fatalf("acquire token after CA rotation: %v", err)
	}
	if token.AccessToken != "rotated-ca-token" {
		t.Fatalf("access token after CA rotation = %q, want %q", token.AccessToken, "rotated-ca-token")
	}
	mu.Lock()
	finalRequestCount := requestCount
	mu.Unlock()
	if finalRequestCount != 1 {
		t.Fatalf("issuer received %d requests after CA rotation, want 1", finalRequestCount)
	}
}

func TestFulfillmentTokenSourceSanitizesOAuthErrors(t *testing.T) {
	const secret = "secret-that-must-not-appear"
	clientIDPath, clientSecretPath := writeFulfillmentCredentials(t, "metering-client", secret)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid_client","error_description":"`+secret+`"}`, http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	tokenSource, err := newFulfillmentTokenSourceWithHTTPClient(fulfillmentOAuthConfig{
		issuerURL:        server.URL + "/realms/osac",
		clientIDFile:     clientIDPath,
		clientSecretFile: clientSecretPath,
	}, server.Client())
	if err != nil {
		t.Fatalf("newFulfillmentTokenSourceWithHTTPClient: %v", err)
	}
	_, err = tokenSource.Token()
	if err == nil {
		t.Fatal("Token() succeeded for rejected credentials; want error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("token acquisition error exposed client secret text")
	}
}

func TestConfigRequiresKeycloakIssuerInsteadOfServiceAccountTokenFile(t *testing.T) {
	t.Setenv("FULFILLMENT_SERVER_ADDRESS", "fulfillment:8001")
	t.Setenv("FULFILLMENT_ISSUER_URL", "")
	t.Setenv("FULFILLMENT_TOKEN_FILE", "/tmp/projected-service-account-token")

	err := configFromEnv().validate()
	if err == nil || !strings.Contains(err.Error(), "FULFILLMENT_ISSUER_URL is required") {
		t.Fatalf("config validation error = %v, want missing FULFILLMENT_ISSUER_URL", err)
	}
}

func writeFulfillmentCredentials(t *testing.T, clientID, clientSecret string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	clientIDPath := filepath.Join(dir, "client-id")
	clientSecretPath := filepath.Join(dir, "client-secret")
	if err := os.WriteFile(clientIDPath, []byte(clientID), 0o600); err != nil {
		t.Fatalf("write client ID file: %v", err)
	}
	if err := os.WriteFile(clientSecretPath, []byte(clientSecret), 0o600); err != nil {
		t.Fatalf("write client secret file: %v", err)
	}
	return clientIDPath, clientSecretPath
}

func tokenRequestCount(mu *sync.Mutex, requests *[]struct {
	clientID string
	secret   string
}) int {
	mu.Lock()
	defer mu.Unlock()
	return len(*requests)
}

func TestNewFulfillmentTokenSourceRequiresCredentialFiles(t *testing.T) {
	client := &http.Client{Transport: http.DefaultTransport}
	for name, cfg := range map[string]fulfillmentOAuthConfig{
		"client ID path":     {issuerURL: "https://keycloak.example.com/realms/osac", clientSecretFile: "/secret"},
		"client secret path": {issuerURL: "https://keycloak.example.com/realms/osac", clientIDFile: "/client-id"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newFulfillmentTokenSourceWithHTTPClient(cfg, client); err == nil {
				t.Fatal("newFulfillmentTokenSourceWithHTTPClient succeeded; want error")
			}
		})
	}
}
