//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package osaccli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Token contains an OAuth access token and its refresh token and expiry.
type Token struct {
	Access  string
	Refresh string
	Expiry  time.Time
}

// isFresh returns true if the token can still be used and isn't about to expire, using the same thirty second margin
// as the OSAC CLI.
func (t *Token) isFresh() bool {
	if t == nil || t.Access == "" {
		return false
	}
	return t.Expiry.IsZero() || time.Until(t.Expiry) > 30*time.Second
}

// isExpired returns true if the token has expired.
func (t *Token) isExpired() bool {
	return !t.Expiry.IsZero() && !time.Now().Before(t.Expiry)
}

// serverMetadata contains the authorization server metadata fields needed to obtain tokens. See RFC 8414 and the
// OpenID Connect Discovery 1.0 specification.
type serverMetadata struct {
	TokenEndpoint   string   `json:"token_endpoint,omitempty"`
	ScopesSupported []string `json:"scopes_supported,omitempty"`
}

// tokenEndpointResponse contains the fields of the response of the OAuth token endpoint.
type tokenEndpointResponse struct {
	AccessToken  string `json:"access_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
}

// endpointError is the error response of the OAuth token endpoint.
type endpointError struct {
	ErrorCode        string `json:"error,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
}

func (e *endpointError) Error() string {
	return fmt.Sprintf("%s: %s", e.ErrorCode, e.ErrorDescription)
}

// TokenSource obtains and renews the access tokens of an `osac login` session. It supports the same non-interactive
// mechanisms the CLI uses after login: a stored access token, a stored refresh token, the client credentials and
// password OAuth flows, and the token generation script. Interactive flows (authorization code and device) can't run
// inside a Terraform provider, so an expired session that needs them must be renewed with a new `osac login`.
type TokenSource struct {
	logger   *slog.Logger
	settings *Settings
	caPool   *x509.CertPool

	mutex         sync.Mutex
	token         *Token
	tokenEndpoint string
	httpClient    *http.Client
}

// NewTokenSource creates a token source for the given settings.
func NewTokenSource(logger *slog.Logger, settings *Settings, caPool *x509.CertPool) (*TokenSource, error) {
	if settings == nil {
		return nil, errors.New("settings are mandatory")
	}
	caPoolCopy := caPool
	if caPoolCopy == nil {
		var err error
		caPoolCopy, err = settings.CaPool()
		if err != nil {
			return nil, fmt.Errorf("failed to create CA pool: %w", err)
		}
	}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    caPoolCopy,
	}
	if settings.General.Insecure {
		tlsConfig.InsecureSkipVerify = true
	}
	return &TokenSource{
		logger:   logger,
		settings: settings,
		caPool:   caPoolCopy,
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsConfig},
		},
		token: &Token{
			Access:  settings.Secrets.AccessToken,
			Refresh: settings.Secrets.RefreshToken,
			Expiry:  settings.Secrets.TokenExpiry,
		},
	}, nil
}

// Token returns a usable access token, renewing it with the stored refresh token or the configured non-interactive
// flow when necessary. Renewed tokens are written back to the CLI configuration so that they are shared with the CLI.
func (s *TokenSource) Token(ctx context.Context) (*Token, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// If the in-memory token is fresh there is no need to do anything:
	if s.token.isFresh() {
		return s.token, nil
	}

	// Try to renew using the refresh token:
	if s.token != nil && s.token.Refresh != "" {
		token, err := s.refresh(ctx, s.token.Refresh)
		if err == nil {
			err = s.store(ctx, token)
			if err != nil {
				s.logger.WarnContext(ctx, "Failed to save renewed token", slog.Any("error", err))
			}
			return token, nil
		}
		s.logger.DebugContext(
			ctx,
			"Failed to refresh the token, will try to request a new one",
			slog.Any("error", err),
		)
	}

	// Try the configured flow, if it is one of the non-interactive ones:
	switch s.settings.General.Flow {
	case "credentials":
		token, err := s.clientCredentials(ctx)
		if err == nil {
			err = s.store(ctx, token)
			if err != nil {
				s.logger.WarnContext(ctx, "Failed to save new token", slog.Any("error", err))
			}
			return token, nil
		}
		return nil, fmt.Errorf("failed to obtain token using the client credentials flow: %w", err)
	case "password":
		token, err := s.password(ctx)
		if err == nil {
			err = s.store(ctx, token)
			if err != nil {
				s.logger.WarnContext(ctx, "Failed to save new token", slog.Any("error", err))
			}
			return token, nil
		}
		return nil, fmt.Errorf("failed to obtain token using the password flow: %w", err)
	}

	// Try the token generation script, if there is one:
	if s.settings.General.TokenScript != "" {
		token, err := s.runTokenScript(ctx)
		if err == nil {
			return token, nil
		}
		return nil, fmt.Errorf("failed to obtain token using the token script: %w", err)
	}

	// The token may still be valid even if it expires soon:
	if s.token != nil && !s.token.isExpired() {
		s.logger.WarnContext(ctx, "Using token that expires soon")
		return s.token, nil
	}
	return nil, errors.New(
		"no token available: the 'osac login' session has expired, run 'osac login' again",
	)
}

// Invalidate discards the in-memory token so that the next call to Token requests a fresh one. It doesn't clear the
// stored credentials, so the CLI session isn't destroyed.
func (s *TokenSource) Invalidate() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.token = nil
}

// store remembers the token and writes it back to the CLI configuration so that it is shared with the CLI.
func (s *TokenSource) store(ctx context.Context, token *Token) error {
	s.token = token
	s.settings.Secrets.AccessToken = token.Access
	s.settings.Secrets.RefreshToken = token.Refresh
	s.settings.Secrets.TokenExpiry = token.Expiry
	return s.settings.SaveSecrets(ctx, s.logger)
}

// discover performs endpoint discovery using the issuer URL saved by `osac login`, trying the OAuth and then the
// OpenID Connect well-known endpoints, exactly like the CLI.
func (s *TokenSource) discover(ctx context.Context) (string, error) {
	if s.tokenEndpoint != "" {
		return s.tokenEndpoint, nil
	}
	issuer := strings.TrimSuffix(s.settings.General.Issuer, "/")
	if issuer == "" {
		return "", errors.New("no token issuer in the OSAC configuration")
	}
	var metadata *serverMetadata
	for _, endpoint := range []string{"oauth-authorization-server", "openid-configuration"} {
		metadataURL := fmt.Sprintf("%s/.well-known/%s", issuer, endpoint)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
		if err != nil {
			return "", fmt.Errorf("failed to create metadata request: %w", err)
		}
		response, err := s.httpClient.Do(request)
		if err != nil {
			continue
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || readErr != nil {
			continue
		}
		var candidate serverMetadata
		if json.Unmarshal(body, &candidate) != nil || candidate.TokenEndpoint == "" {
			continue
		}
		metadata = &candidate
		break
	}
	if metadata == nil {
		return "", fmt.Errorf("failed to discover token endpoint for issuer '%s'", issuer)
	}
	s.tokenEndpoint = metadata.TokenEndpoint
	return s.tokenEndpoint, nil
}

// clientCredentials runs the OAuth client credentials flow.
func (s *TokenSource) clientCredentials(ctx context.Context) (*Token, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", s.settings.Secrets.ClientId)
	form.Set("client_secret", s.settings.Secrets.ClientSecret)
	if len(s.settings.General.Scopes) > 0 {
		form.Set("scope", strings.Join(s.settings.General.Scopes, " "))
	}
	return s.tokenRequest(ctx, form)
}

// password runs the OAuth resource owner password credentials flow.
func (s *TokenSource) password(ctx context.Context) (*Token, error) {
	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", s.settings.Secrets.ClientId)
	form.Set("client_secret", s.settings.Secrets.ClientSecret)
	form.Set("username", s.settings.Secrets.User)
	form.Set("password", s.settings.Secrets.Password)
	if len(s.settings.General.Scopes) > 0 {
		form.Set("scope", strings.Join(s.settings.General.Scopes, " "))
	}
	return s.tokenRequest(ctx, form)
}

// refresh renews the given refresh token.
func (s *TokenSource) refresh(ctx context.Context, refreshToken string) (*Token, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", s.settings.Secrets.ClientId)
	form.Set("refresh_token", refreshToken)
	token, err := s.tokenRequest(ctx, form)
	if err != nil {
		return nil, err
	}

	// Some authorization servers don't return a new refresh token, in those cases keep the old one:
	if token.Refresh == "" {
		token.Refresh = refreshToken
	}
	return token, nil
}

// tokenRequest sends a form to the token endpoint and returns the resulting token.
func (s *TokenSource) tokenRequest(ctx context.Context, form url.Values) (*Token, error) {
	endpoint, err := s.discover(ctx)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = response.Body.Close()
	}()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		var endpointErr endpointError
		if json.Unmarshal(body, &endpointErr) == nil && endpointErr.ErrorCode != "" {
			return nil, &endpointErr
		}
		return nil, fmt.Errorf("unexpected response code %d from token endpoint", response.StatusCode)
	}
	contentType := response.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		return nil, fmt.Errorf("expected 'application/json' content type from token endpoint, but got '%s'", contentType)
	}
	var tokenResponse tokenEndpointResponse
	err = json.Unmarshal(body, &tokenResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}
	if tokenResponse.AccessToken == "" {
		return nil, errors.New("token response doesn't contain an access token")
	}
	return &Token{
		Access:  tokenResponse.AccessToken,
		Refresh: tokenResponse.RefreshToken,
		Expiry:  time.Now().Add(time.Duration(tokenResponse.ExpiresIn) * time.Second),
	}, nil
}

// runTokenScript runs the token generation script configured in the CLI, using the same convention as the CLI: the
// script is executed with the user's shell and the trimmed standard output is the access token.
func (s *TokenSource) runTokenScript(ctx context.Context) (*Token, error) {
	shell, ok := os.LookupEnv("SHELL")
	if !ok {
		shell = "/usr/bin/sh"
	}
	command := exec.CommandContext(ctx, shell, "-c", s.settings.General.TokenScript)
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to execute token script: %w", err)
	}
	token := &Token{
		Access: strings.TrimSpace(string(output)),
	}
	if token.Access == "" {
		return nil, errors.New("token script didn't produce a token")
	}

	// Try to parse the token as a JWT to get the expiry. If it isn't a JWT then the zero expiry means it is used
	// as-is, exactly like the CLI does.
	token.Expiry = jwtExpiry(token.Access)
	return token, nil
}

// jwtExpiry returns the expiry extracted from the 'exp' claim of the given JWT, or the zero time if the token isn't
// a parseable JWT. This doesn't verify the signature, it only reads the claim, like the CLI does.
func jwtExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Expiry int64 `json:"exp,omitempty"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return time.Time{}
	}
	if claims.Expiry == 0 {
		return time.Time{}
	}
	return time.Unix(claims.Expiry, 0)
}
