/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package logout

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/oauth"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "logout [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		RunE:                  runner.run,
	}
	return result
}

type runnerContext struct {
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	// Get the context:
	ctx := cmd.Context()

	// Get the configuration, console, and logger from the context:
	cfg := config.SettingsFromContext(ctx)
	console := terminal.ConsoleFromContext(ctx)
	logger := logging.LoggerFromContext(ctx)

	// Terminate the server-side session. If it fails, warn but continue — local credentials
	// are always cleared regardless:
	err := c.terminateSession(ctx, cfg, logger)
	if err != nil {
		console.Errorf(ctx, "Warning: Failed to terminate server session: %v\n", err)
		console.Errorf(ctx, "Local credentials will be cleared, but the server session may remain active.\n")
	}

	// Clear all the details:
	cfg.Reset()

	// Save the configuration:
	err = cfg.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	console.Infof(ctx, "Successfully logged out\n")
	return nil
}

func (c *runnerContext) terminateSession(ctx context.Context, cfg *config.Settings,
	logger *slog.Logger) error {
	// Check that the issuer is configured:
	issuer := cfg.Issuer()
	if issuer == "" {
		return nil
	}

	// Check that a token source is available:
	tokenSource, err := cfg.TokenSource(ctx)
	if err != nil || tokenSource == nil {
		return nil
	}

	// Check that a refresh token is available:
	token, err := tokenSource.Token(ctx)
	if err != nil || token == nil || token.Refresh == "" {
		return nil
	}

	// Get the CA pool:
	caPool, err := cfg.CaPool(ctx)
	if err != nil {
		return fmt.Errorf("failed to get CA pool: %w", err)
	}

	// Discover the OIDC endpoints from the issuer:
	discoveryTool, err := oauth.NewDiscoveryTool().
		SetLogger(logger).
		SetIssuer(issuer).
		SetInsecure(cfg.Insecure()).
		SetCaPool(caPool).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create discovery tool: %w", err)
	}

	metadata, err := discoveryTool.Discover(ctx)
	if err != nil {
		return fmt.Errorf("failed to discover OIDC endpoints: %w", err)
	}

	// Check that the end_session_endpoint is available:
	if metadata.EndSessionEndpoint == "" {
		return nil
	}

	// Create the HTTP client:
	client := c.httpClient(caPool, cfg.Insecure())

	// Obtain a fresh ID token via refresh grant. The ID token is not persisted locally, so we
	// need to fetch one to use as id_token_hint for the end_session_endpoint:
	idToken, err := c.refreshForIdToken(ctx, client, metadata.TokenEndpoint,
		token.Refresh, cfg.ClientId(), cfg.ClientSecret())
	if err != nil {
		return fmt.Errorf("failed to obtain ID token for logout: %w", err)
	}

	// Send a back-channel request to the end_session_endpoint to terminate the server session:
	params := url.Values{}
	params.Set("id_token_hint", idToken)

	logoutURL := fmt.Sprintf("%s?%s", metadata.EndSessionEndpoint, params.Encode())

	resp, err := client.Get(logoutURL)
	if err != nil {
		return fmt.Errorf("failed to call end_session_endpoint: %w", err)
	}
	resp.Body.Close()

	return nil
}

func (c *runnerContext) refreshForIdToken(ctx context.Context, client *http.Client,
	tokenEndpoint, refreshToken, clientId, clientSecret string) (string, error) {
	// Build the refresh grant form:
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)

	// Add client credentials if configured:
	if clientId != "" {
		form.Set("client_id", clientId)
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}

	// Send the request to the token endpoint:
	resp, err := client.PostForm(tokenEndpoint, form)
	if err != nil {
		return "", fmt.Errorf("failed to call token endpoint: %w", err)
	}
	defer resp.Body.Close()

	// Check the response status:
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned error status: %s", resp.Status)
	}

	// Extract the ID token from the response:
	var tokenResponse struct {
		IdToken string `json:"id_token"`
	}
	err = json.NewDecoder(resp.Body).Decode(&tokenResponse)
	if err != nil {
		return "", fmt.Errorf("failed to parse token response: %w", err)
	}

	// Check that the response contains an ID token:
	if tokenResponse.IdToken == "" {
		return "", fmt.Errorf("token endpoint did not return an ID token")
	}

	return tokenResponse.IdToken, nil
}

func (c *runnerContext) httpClient(caPool *x509.CertPool, insecure bool) *http.Client {
	tlsConfig := &tls.Config{
		RootCAs: caPool,
	}
	if insecure {
		tlsConfig.InsecureSkipVerify = true
	}
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}
}

const shortHelp = `Discard connection and authentication details`

const longHelp = `
Discard connection and authentication details.
`
