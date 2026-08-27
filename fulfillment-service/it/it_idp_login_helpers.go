/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/oauth2-proxy/mockoidc"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/network"
	"github.com/osac-project/osac/fulfillment-service/internal/version"
	"google.golang.org/grpc"
)

// MockOIDCState holds a running mock OIDC server and the addresses from which it can be
// reached by the test runner and by Keycloak pods inside the Kind cluster.
type MockOIDCState struct {
	server      *mockoidc.MockOIDC
	localAddr   string // 127.0.0.1:<port> — reachable by the test runner
	clusterAddr string // <bridge-ip>:<port> — reachable by Keycloak inside Kind
}

// ClientID returns the OAuth2 client ID that mockoidc expects.
func (s *MockOIDCState) ClientID() string { return s.server.ClientID }

// ClientSecret returns the OAuth2 client secret that mockoidc expects.
func (s *MockOIDCState) ClientSecret() string { return s.server.ClientSecret }

// LocalIssuer returns the OIDC issuer URL as seen by the test runner.
// This is also the `iss` claim that mockoidc puts into every token it issues.
func (s *MockOIDCState) LocalIssuer() string {
	return fmt.Sprintf("http://%s%s", s.localAddr, mockoidc.IssuerBase)
}

// ClusterTokenURL returns the token endpoint URL reachable by Keycloak inside Kind.
func (s *MockOIDCState) ClusterTokenURL() string {
	return fmt.Sprintf("http://%s%s", s.clusterAddr, mockoidc.TokenEndpoint)
}

// ClusterJWKSURL returns the JWKS endpoint URL reachable by Keycloak inside Kind.
func (s *MockOIDCState) ClusterJWKSURL() string {
	return fmt.Sprintf("http://%s%s", s.clusterAddr, mockoidc.JWKSEndpoint)
}

// LocalAuthURL returns the authorization endpoint URL reachable by the test runner.
func (s *MockOIDCState) LocalAuthURL() string {
	return fmt.Sprintf("http://%s%s", s.localAddr, mockoidc.AuthorizationEndpoint)
}

// QueueUser adds a mock user to the authorization queue. The next call to the
// authorization endpoint pops this user and issues a token on their behalf.
// If the queue is empty, mockoidc uses DefaultUser() automatically.
func (s *MockOIDCState) QueueUser(subject, email, preferredUsername string) {
	s.server.QueueUser(&mockoidc.MockUser{
		Subject:           subject,
		Email:             email,
		EmailVerified:     true,
		PreferredUsername: preferredUsername,
	})
}

// StartMockOIDC starts an embedded mock OIDC server bound to all interfaces so that
// Keycloak pods inside the Kind cluster can reach the token and JWKS endpoints.
// Call StopMockOIDC(state) in DeferCleanup to shut it down.
func (t *Tool) StartMockOIDC() (*MockOIDCState, error) {
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return nil, fmt.Errorf("failed to start mock OIDC listener: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	srv, err := mockoidc.NewServer(nil)
	if err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("failed to create mock OIDC server: %w", err)
	}
	if err := srv.Start(ln, nil); err != nil {
		return nil, fmt.Errorf("failed to start mock OIDC server: %w", err)
	}

	bridgeIP, err := t.kindBridgeIP()
	if err != nil {
		_ = srv.Shutdown()
		return nil, fmt.Errorf("failed to detect Kind bridge IP: %w", err)
	}

	return &MockOIDCState{
		server:      srv,
		localAddr:   fmt.Sprintf("127.0.0.1:%d", port),
		clusterAddr: fmt.Sprintf("%s:%d", bridgeIP, port),
	}, nil
}

// StopMockOIDC shuts down the mock OIDC server.
func StopMockOIDC(state *MockOIDCState) error {
	if state == nil || state.server == nil {
		return nil
	}
	return state.server.Shutdown()
}

// kindBridgeIP returns the gateway IP of the Kind network: the host-accessible IP that
// pods inside the Kind cluster can use to reach services on the test runner's host.
// Tries podman first (this environment uses Podman), then falls back to docker.
func (t *Tool) kindBridgeIP() (string, error) {
	// Podman's network inspect returns JSON with a different schema than Docker's.
	// Try it first since this environment uses Podman.
	if ip, err := t.kindBridgeIPViaPodman(); err == nil {
		return ip, nil
	}

	// Fall back to Docker's template-based inspect.
	out, err := t.runCommand(context.Background(), "docker", "network", "inspect", "kind",
		"--format", "{{range .IPAM.Config}}{{if .Gateway}}{{.Gateway}}\n{{end}}{{end}}")
	if err != nil {
		return "", fmt.Errorf("could not detect Kind bridge IP via podman or docker: %w", err)
	}
	return parseFirstIPv4Line(string(out))
}

// kindBridgeIPViaPodman uses `podman network inspect kind` (JSON output) to find the
// gateway of the Kind network. Podman's schema is:
//
//	[{"subnets": [{"subnet": "10.89.0.0/24", "gateway": "10.89.0.1"}]}]
func (t *Tool) kindBridgeIPViaPodman() (string, error) {
	out, err := t.runCommand(context.Background(), "podman", "network", "inspect", "kind")
	if err != nil {
		return "", err
	}
	// Parse the JSON array that podman returns.
	var networks []struct {
		Subnets []struct {
			Gateway string `json:"gateway"`
		} `json:"subnets"`
	}
	if jsonErr := json.Unmarshal(out, &networks); jsonErr != nil {
		return "", fmt.Errorf("failed to parse podman network inspect output: %w", jsonErr)
	}
	for _, net := range networks {
		for _, subnet := range net.Subnets {
			if gw := strings.TrimSpace(subnet.Gateway); gw != "" && !strings.Contains(gw, ":") {
				return gw, nil
			}
		}
	}
	return "", fmt.Errorf("no IPv4 gateway found in podman network inspect output")
}

// parseFirstIPv4Line returns the first non-empty, non-IPv6 line from s.
func parseFirstIPv4Line(s string) (string, error) {
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.Contains(line, ":") {
			return line, nil
		}
	}
	return "", fmt.Errorf("no IPv4 address found in output: %q", s)
}

// RegisterMockOIDCIdP creates an OIDC identity provider entry in Keycloak directly via the
// admin API, wired to the running MockOIDCState. It does NOT go through the OSAC API so
// that it can be used to test the login flow independently of the OSAC controller.
//
// Returns the Keycloak IdP alias ("<tenantName>-<idpName>").
func (t *Tool) RegisterMockOIDCIdP(
	ctx context.Context,
	state *MockOIDCState,
	tenantName, idpName string,
) (alias string, err error) {
	alias = fmt.Sprintf("%s-%s", tenantName, idpName)
	payload := map[string]any{
		"providerId":  "oidc",
		"alias":       alias,
		"displayName": fmt.Sprintf("Mock OIDC (%s)", idpName),
		"enabled":     true,
		"config": map[string]any{
			"clientId":          state.ClientID(),
			"clientSecret":      state.ClientSecret(),
			"authorizationUrl":  state.LocalAuthURL(),
			"tokenUrl":          state.ClusterTokenURL(),
			"jwksUrl":           state.ClusterJWKSURL(),
			"issuer":            state.LocalIssuer(),
			"useJwksUrl":        "true",
			"validateSignature": "true",
			"allowedClockSkew":  "5",
			// Required to allow HTTP token/JWKS endpoints from within Kind.
			"allowHttpScheme": "true",
		},
	}
	code, body, createErr := t.KeycloakAdminRequest(ctx, http.MethodPost, "/identity-provider/instances", payload)
	if createErr != nil {
		err = fmt.Errorf("failed to create IdP in Keycloak: %w", createErr)
		return
	}
	if code != http.StatusCreated && code != http.StatusConflict {
		err = fmt.Errorf("unexpected HTTP status creating IdP: %d body=%s", code, string(body))
		return
	}
	return alias, nil
}

// ProvisionOIDCUser creates a Keycloak user with a password, links them to the given IdP
// alias using the provided external subject, and adds them to the tenant's Keycloak
// organization so the organization claim appears in their JWT.
//
// The password is set to the user's username for simplicity — these are ephemeral test
// users. Use LoginOIDCUser to authenticate via the password grant.
func (t *Tool) ProvisionOIDCUser(
	ctx context.Context,
	username, email, tenantName, idpAlias, externalSubject string,
) (userID string, err error) {
	code, body, err := t.KeycloakAdminRequest(ctx, http.MethodPost, "/users", map[string]any{
		"username":      username,
		"email":         email,
		"emailVerified": true,
		"enabled":       true,
		"firstName":     username,
		"lastName":      "IdPTest",
	})
	if err != nil {
		return "", fmt.Errorf("failed to create Keycloak user %q: %w", username, err)
	}
	if code != http.StatusCreated && code != http.StatusConflict {
		return "", fmt.Errorf("unexpected HTTP status creating user %q: %d body=%s", username, code, string(body))
	}

	userID, err = t.keycloakEnsureUserReady(ctx, username)
	if err != nil {
		return
	}

	// Set the password via a separate API call (KC ignores credentials in the create payload).
	code, body, err = t.KeycloakAdminRequest(ctx, http.MethodPut,
		fmt.Sprintf("/users/%s/reset-password", userID),
		map[string]any{
			"type":      "password",
			"value":     username,
			"temporary": false,
		},
	)
	if err != nil {
		return "", fmt.Errorf("failed to set password for user %q: %w", username, err)
	}
	if code != http.StatusNoContent {
		return "", fmt.Errorf("unexpected HTTP status setting password for %q: %d body=%s", username, code, string(body))
	}

	// Link user to the external IdP (federated identity).
	code, body, err = t.KeycloakAdminRequest(ctx, http.MethodPost,
		fmt.Sprintf("/users/%s/federated-identity/%s", userID, idpAlias),
		map[string]any{
			"identityProvider": idpAlias,
			"userId":           externalSubject,
			"userName":         username,
		},
	)
	if err != nil {
		return "", fmt.Errorf("failed to link user %q to IdP %q: %w", username, idpAlias, err)
	}
	if code != http.StatusCreated && code != http.StatusNoContent && code != http.StatusConflict {
		return "", fmt.Errorf("unexpected HTTP status linking user %q to IdP: %d body=%s", username, code, string(body))
	}

	if err = t.ensureUserInOrg(ctx, username, tenantName); err != nil {
		return "", fmt.Errorf("failed to add user %q to org %q: %w", username, tenantName, err)
	}
	return userID, nil
}

// LoginOIDCUser authenticates a provisioned IdP user via Keycloak's password grant and
// returns a JWT access token. This avoids the full OIDC redirect chain (which requires
// the Kind cluster to reach mockoidc on the host) while still validating that the user
// is properly linked to the IdP and has the correct tenant/organization claims.
func (t *Tool) LoginOIDCUser(ctx context.Context, username string) (string, error) {
	tokenSource, err := t.makeKeycloakTokenSource(ctx, username, username)
	if err != nil {
		return "", fmt.Errorf("failed to create token source for user %q: %w", username, err)
	}
	token, err := tokenSource.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to obtain token for user %q: %w", username, err)
	}
	return token.Access, nil
}

// WaitForKeycloakIdP polls until the Keycloak admin API reports the IdP alias as present.
func (t *Tool) WaitForKeycloakIdP(ctx context.Context, alias string) error {
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 2 * time.Second
	bo.MaxInterval = 10 * time.Second
	bo.MaxElapsedTime = 2 * time.Minute
	return backoff.Retry(func() error {
		code, _, err := t.KeycloakAdminRequest(ctx, http.MethodGet,
			fmt.Sprintf("/identity-provider/instances/%s", alias), nil)
		if err != nil {
			return err
		}
		if code == http.StatusOK {
			return nil
		}
		return fmt.Errorf("IdP %q not yet in Keycloak (status %d)", alias, code)
	}, backoff.WithContext(bo, ctx))
}

// SimulateOIDCLogin drives the OIDC authorization code flow programmatically to obtain a
// Keycloak JWT for an external IdP user. The redirect chain is:
//
//  1. Test runner  → GET KC auth endpoint (kc_idp_hint=<alias>)
//  2.              ← 302 to mockoidc /oidc/authorize
//  3. Test runner  → GET mockoidc /oidc/authorize  (pops queued user; auto-approves)
//  4.              ← 302 to KC broker callback with mock code
//  5. Test runner  → GET KC /broker/<alias>/endpoint?code=<mock-code>
//  6. KC (in-Kind) → POST mockoidc /oidc/token (bridge IP) — exchanges code
//  7. KC (in-Kind) → GET  mockoidc /oidc/.well-known/jwks.json — validates signature
//  8. KC           ← 302 to original redirect_uri?code=<kc-code>
//  9. Test runner intercepts redirect, extracts kc-code
//  10. Test runner → POST KC /token grant_type=authorization_code&code=<kc-code>
//  11.             ← KC JWT access_token
//
// Call QueueUser on the MockOIDCState before calling this function to control which user
// is returned. If the queue is empty, mockoidc uses DefaultUser().
//
// The returned access token can be used directly as a Bearer token against the OSAC API.
func (t *Tool) SimulateOIDCLogin(ctx context.Context, idpAlias string) (string, error) {
	// The osac-cli Keycloak client only allows "http://localhost" as a redirect URI.
	// We use that exact value and intercept the redirect via CheckRedirect before
	// the HTTP client actually tries to connect to it.
	const callbackBase = "http://localhost"
	callbackURL := callbackBase

	// PKCE (RFC 7636) — required by the osac-cli Keycloak client.
	codeVerifier, codeChallenge, err := generatePKCE()
	if err != nil {
		return "", fmt.Errorf("failed to generate PKCE challenge: %w", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create cookie jar: %w", err)
	}

	var kcCode string
	var redirectLog []string
	httpClient := &http.Client{
		Jar: jar,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    t.caPool,
				MinVersion: tls.VersionTLS12,
			},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			target := req.URL.String()
			redirectLog = append(redirectLog, fmt.Sprintf("redirect #%d → %s", len(via), target))
			if strings.HasPrefix(target, callbackBase) {
				kcCode = req.URL.Query().Get("code")
				if kcErr := req.URL.Query().Get("error"); kcErr != "" {
					redirectLog = append(redirectLog, fmt.Sprintf("  KC error: %s — %s",
						kcErr, req.URL.Query().Get("error_description")))
				}
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	// Steps 1–9: drive the full redirect chain until we hit the callback URL.
	authURL := fmt.Sprintf(
		"https://%s/realms/osac/protocol/openid-connect/auth?"+
			"client_id=osac-cli&response_type=code"+
			"&redirect_uri=%s&state=%s"+
			"&kc_idp_hint=%s&scope=%s"+
			"&code_challenge=%s&code_challenge_method=S256",
		keycloakAddr,
		url.QueryEscape(callbackURL),
		url.QueryEscape("osac-it-"+idpAlias),
		url.QueryEscape(idpAlias),
		url.QueryEscape("openid organization"),
		url.QueryEscape(codeChallenge),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to build KC auth request: %w", err)
	}
	resp, doErr := httpClient.Do(req)
	var respDebug string
	if resp != nil {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		respDebug = fmt.Sprintf("status=%d url=%s body=%s", resp.StatusCode, resp.Request.URL, string(bodyBytes))
	}
	// ErrUseLastResponse is how CheckRedirect signals "stop here, I captured the code".
	if doErr != nil && kcCode == "" {
		return "", fmt.Errorf("OIDC redirect chain failed before capturing KC code: %w (response: %s)", doErr, respDebug)
	}
	if kcCode == "" {
		return "", fmt.Errorf("redirect chain stopped without KC code — redirects:\n%s\nfinal response: %s",
			strings.Join(redirectLog, "\n"), respDebug)
	}

	// Step 10–11: exchange the KC authorization code for a KC JWT.
	tokenURL := fmt.Sprintf("https://%s/realms/osac/protocol/openid-connect/token", keycloakAddr)
	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL,
		strings.NewReader(url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {kcCode},
			"redirect_uri":  {callbackURL},
			"client_id":     {"osac-cli"},
			"code_verifier": {codeVerifier},
		}.Encode()),
	)
	if err != nil {
		return "", fmt.Errorf("failed to build token exchange request: %w", err)
	}
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	tokenResp, err := httpClient.Do(tokenReq)
	if err != nil {
		return "", fmt.Errorf("token exchange request failed: %w", err)
	}
	defer tokenResp.Body.Close()

	if tokenResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(tokenResp.Body)
		return "", fmt.Errorf("token exchange returned HTTP %d: %s", tokenResp.StatusCode, string(body))
	}

	var tokenPayload struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenPayload); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}
	if tokenPayload.Error != "" {
		return "", fmt.Errorf("token exchange error %q: %s", tokenPayload.Error, tokenPayload.Description)
	}
	if tokenPayload.AccessToken == "" {
		return "", fmt.Errorf("token exchange returned OK but access_token is empty")
	}
	return tokenPayload.AccessToken, nil
}

// MakeOIDCGRPCConn creates a gRPC connection to the external API authenticated with the
// given raw JWT bearer token. Intended for use with tokens obtained via SimulateOIDCLogin.
func (t *Tool) MakeOIDCGRPCConn(_ context.Context, jwtToken string) (*grpc.ClientConn, error) {
	tokenSource, err := auth.NewStaticTokenSource().
		SetLogger(t.logger).
		SetToken(&auth.Token{Access: jwtToken}).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create static token source: %w", err)
	}
	return network.NewGrpcClient().
		SetLogger(t.logger).
		SetCaPool(t.caPool).
		SetAddress(externalServiceAddr).
		SetTokenSource(tokenSource).
		SetUserAgent(fmt.Sprintf("%s/%s", userAgent, version.Get())).
		Build()
}

// generatePKCE creates a PKCE code_verifier and its S256 code_challenge (RFC 7636).
func generatePKCE() (verifier, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return verifier, challenge, nil
}
