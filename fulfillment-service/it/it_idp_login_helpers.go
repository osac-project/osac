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
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/network"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	"github.com/osac-project/osac/fulfillment-service/internal/version"
	"google.golang.org/grpc"
)

// ExtRealmState holds a temporary Keycloak realm that acts as the external OIDC Identity
// Provider in IdP login integration tests. Both the test runner and Keycloak pods inside
// Kind reach this realm at the same cluster-internal address, eliminating the host-to-cluster
// networking issue that affects mock servers running on the test runner's host.
type ExtRealmState struct {
	tool         *Tool
	realmName    string
	clientID     string
	clientSecret string
}

// IssuerURL returns the OIDC issuer URL for this realm.
// This is used as both the OSAC IdP issuer config and as the `iss` claim validator.
func (s *ExtRealmState) IssuerURL() string {
	return fmt.Sprintf("https://%s/realms/%s", keycloakAddr, s.realmName)
}

// AuthorizationURL returns the OIDC authorization endpoint.
func (s *ExtRealmState) AuthorizationURL() string {
	return s.IssuerURL() + "/protocol/openid-connect/auth"
}

// TokenURL returns the OIDC token endpoint.
// Since this is a cluster-internal URL, Keycloak can reach it for server-to-server
// token exchange without any bridge IP or host networking.
func (s *ExtRealmState) TokenURL() string {
	return s.IssuerURL() + "/protocol/openid-connect/token"
}

// JWKSURL returns the OIDC JWKS endpoint.
func (s *ExtRealmState) JWKSURL() string {
	return s.IssuerURL() + "/protocol/openid-connect/certs"
}

// ClientID returns the OIDC client ID registered in this realm.
func (s *ExtRealmState) ClientID() string { return s.clientID }

// ClientSecret returns the OIDC client secret registered in this realm.
func (s *ExtRealmState) ClientSecret() string { return s.clientSecret }

// RealmName returns the Keycloak realm name.
func (s *ExtRealmState) RealmName() string { return s.realmName }

// CreateExtRealm creates a temporary Keycloak realm with a pre-configured OIDC client.
// The realm serves as the external OIDC Identity Provider for the test. Call
// DeleteExtRealm in DeferCleanup to remove it.
//
// The realm and its client are accessible at the same cluster-internal Keycloak address
// used by all other OSAC integration tests, so no special networking is required.
func (t *Tool) CreateExtRealm(ctx context.Context) (*ExtRealmState, error) {
	realmName := fmt.Sprintf("ext-%s", uuid.New())
	clientID := "ext-client-" + uuid.New()
	clientSecret := uuid.New()

	// Create the realm.
	code, body, err := t.KeycloakAdminRequestForRealm(ctx, "", http.MethodPost, "/realms", map[string]any{
		"realm":   realmName,
		"enabled": true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create ext realm %q: %w", realmName, err)
	}
	if code != http.StatusCreated {
		return nil, fmt.Errorf("unexpected HTTP %d creating ext realm %q: %s", code, realmName, body)
	}

	// Disable VERIFY_PROFILE as a realm-level default required action so that new users
	// are not prompted to verify their profile during their first login. Setting
	// requiredActions:[] on the user object only clears user-specific actions; realm
	// defaults are injected into the login session at runtime, which is why we must
	// disable the default here. A 404 response is acceptable — it just means the
	// action is not registered in this realm.
	code, body, err = t.KeycloakAdminRequestForRealm(ctx, realmName, http.MethodPut,
		"/authentication/required-actions/VERIFY_PROFILE",
		map[string]any{
			"alias":         "VERIFY_PROFILE",
			"name":          "Verify Profile",
			"providerId":    "VERIFY_PROFILE",
			"enabled":       true,
			"defaultAction": false,
			"priority":      90,
			"config":        map[string]any{},
		})
	if err != nil {
		return nil, fmt.Errorf("failed to disable VERIFY_PROFILE default in ext realm %q: %w", realmName, err)
	}
	if code != http.StatusNoContent && code != http.StatusNotFound {
		return nil, fmt.Errorf("unexpected HTTP %d disabling VERIFY_PROFILE in ext realm %q: %s", code, realmName, body)
	}

	// Create an OIDC client in the new realm. redirectUris is set to "*" so KC osac
	// realm can use any broker callback URL without us needing to know the IdP alias
	// in advance.
	code, body, err = t.KeycloakAdminRequestForRealm(ctx, realmName, http.MethodPost, "/clients", map[string]any{
		"clientId":            clientID,
		"secret":              clientSecret,
		"protocol":            "openid-connect",
		"publicClient":        false,
		"redirectUris":        []string{"*"},
		"enabled":             true,
		"standardFlowEnabled": true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create OIDC client in ext realm %q: %w", realmName, err)
	}
	if code != http.StatusCreated {
		return nil, fmt.Errorf("unexpected HTTP %d creating client in ext realm %q: %s", code, realmName, body)
	}

	return &ExtRealmState{
		tool:         t,
		realmName:    realmName,
		clientID:     clientID,
		clientSecret: clientSecret,
	}, nil
}

// DeleteExtRealm removes the temporary Keycloak realm. Safe to call with a nil state.
func (t *Tool) DeleteExtRealm(ctx context.Context, state *ExtRealmState) error {
	if state == nil {
		return nil
	}
	code, body, err := t.KeycloakAdminRequestForRealm(ctx, "", http.MethodDelete,
		"/realms/"+state.realmName, nil)
	if err != nil {
		return fmt.Errorf("failed to delete ext realm %q: %w", state.realmName, err)
	}
	if code != http.StatusNoContent && code != http.StatusNotFound {
		return fmt.Errorf("unexpected HTTP %d deleting ext realm %q: %s", code, state.realmName, body)
	}
	return nil
}

// PatchKCIdPDisableTrustManager patches the osac-realm Keycloak IdP identified by
// idpAlias to configure it for a test environment where Keycloak uses a self-signed
// TLS certificate. It sets the following flags:
//
//   - disableTrustManager=true — KC skips TLS certificate verification when making the
//     back-channel token exchange call to the ext realm. Without this, KC returns HTTP 502
//     because the cluster's self-signed CA is not in its JVM default truststore.
//   - validateSignature=false — prevents KC from making a separate back-channel JWKS
//     fetch to validate the ID token signature, which would also fail TLS verification.
//   - useJwksUrl=false — disables the JWKS URL lookup so KC does not attempt any
//     additional back-channel HTTPS call to retrieve public keys.
//
// This must be called after the OSAC controller has reconciled the IdP into Keycloak
// (i.e. after the IdP reaches READY phase), because the reconciler will create the IdP
// without these flags and a subsequent reconciliation will not overwrite them (the
// reconciler does not manage these fields).
func (t *Tool) PatchKCIdPDisableTrustManager(ctx context.Context, idpAlias string) error {
	// GET the current KC IdP to obtain the full config.
	code, body, err := t.KeycloakAdminRequest(ctx, http.MethodGet,
		fmt.Sprintf("/identity-provider/instances/%s", url.PathEscape(idpAlias)), nil)
	if err != nil {
		return fmt.Errorf("get KC IdP %q for trust-manager patch: %w", idpAlias, err)
	}
	if code != http.StatusOK {
		return fmt.Errorf("get KC IdP %q for trust-manager patch: HTTP %d: %s", idpAlias, code, body)
	}

	// Unmarshal the response, patch the config, and PUT it back.
	var kcIdp map[string]any
	if err := json.Unmarshal(body, &kcIdp); err != nil {
		return fmt.Errorf("unmarshal KC IdP %q: %w", idpAlias, err)
	}

	config, _ := kcIdp["config"].(map[string]any)
	if config == nil {
		config = map[string]any{}
	}
	// Disable TLS verification for back-channel token exchange (cluster self-signed cert).
	config["disableTrustManager"] = "true"
	// Disable signature validation to avoid a separate JWKS back-channel HTTPS call.
	config["validateSignature"] = "false"
	// Disable the JWKS URL lookup so KC does not attempt an additional HTTPS fetch.
	config["useJwksUrl"] = "false"
	kcIdp["config"] = config

	code, body, err = t.KeycloakAdminRequest(ctx, http.MethodPut,
		fmt.Sprintf("/identity-provider/instances/%s", url.PathEscape(idpAlias)), kcIdp)
	if err != nil {
		return fmt.Errorf("patch KC IdP %q disableTrustManager: %w", idpAlias, err)
	}
	if code != http.StatusNoContent {
		return fmt.Errorf("patch KC IdP %q disableTrustManager: unexpected HTTP %d: %s", idpAlias, code, body)
	}

	return nil
}

// AddUser creates a user with the given credentials in the ext realm. Returns the
// Keycloak-assigned user ID, which is the `sub` claim value in tokens issued by this realm.
// Use the returned ID as the external subject when linking the user in the osac realm via
// ProvisionOIDCUser.
func (s *ExtRealmState) AddUser(ctx context.Context, username, password, email string) (userID string, err error) {
	// Create the user. requiredActions is explicitly cleared so KC does not trigger
	// VERIFY_PROFILE or any other required-action interstitial during the OIDC flow.
	code, body, err := s.tool.KeycloakAdminRequestForRealm(ctx, s.realmName, http.MethodPost, "/users",
		map[string]any{
			"username":        username,
			"email":           email,
			"emailVerified":   true,
			"enabled":         true,
			"firstName":       username,
			"lastName":        "ExtIdPTest",
			"requiredActions": []string{},
		})
	if err != nil {
		return "", fmt.Errorf("failed to create user %q in ext realm %q: %w", username, s.realmName, err)
	}
	if code != http.StatusCreated {
		return "", fmt.Errorf("unexpected HTTP %d creating user %q in ext realm %q: %s",
			code, username, s.realmName, body)
	}

	// Look up the user to get their ID.
	code, body, err = s.tool.KeycloakAdminRequestForRealm(ctx, s.realmName, http.MethodGet,
		fmt.Sprintf("/users?username=%s&exact=true", url.QueryEscape(username)), nil)
	if err != nil {
		return "", fmt.Errorf("failed to look up user %q in ext realm %q: %w", username, s.realmName, err)
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("unexpected HTTP %d looking up user %q in ext realm %q: %s",
			code, username, s.realmName, body)
	}
	var users []struct {
		ID string `json:"id"`
	}
	if jsonErr := json.Unmarshal(body, &users); jsonErr != nil || len(users) == 0 {
		return "", fmt.Errorf("failed to parse user list for %q in ext realm %q (body=%s): %w",
			username, s.realmName, body, jsonErr)
	}
	userID = users[0].ID

	// Set the password.
	code, body, err = s.tool.KeycloakAdminRequestForRealm(ctx, s.realmName, http.MethodPut,
		fmt.Sprintf("/users/%s/reset-password", userID),
		map[string]any{"type": "password", "value": password, "temporary": false})
	if err != nil {
		return "", fmt.Errorf("failed to set password for user %q in ext realm %q: %w",
			username, s.realmName, err)
	}
	if code != http.StatusNoContent {
		return "", fmt.Errorf("unexpected HTTP %d setting password for user %q in ext realm %q: %s",
			code, username, s.realmName, body)
	}
	return userID, nil
}

// ProvisionOIDCUser creates a user in the KC **osac** realm and configures everything
// needed for that user to authenticate via an external IdP:
//
//  1. Creates the user in the osac realm (username, email, enabled).
//  2. Sets their password (same as username, for direct-grant fallback in other tests).
//  3. Links the user to the external IdP via a KC federated identity record.
//     externalSubject must be the `sub` claim value in tokens issued by the ext realm
//     (i.e. the user's UUID in the ext realm — returned by ExtRealmState.AddUser).
//  4. Adds the user to the KC organization that corresponds to tenantName, and puts them
//     in the /members group so the `organization` claim appears in their JWT.
//
// Returns the KC user ID in the osac realm.
func (t *Tool) ProvisionOIDCUser(
	ctx context.Context,
	username, email, tenantName, idpAlias, externalSubject string,
) (userID string, err error) {
	// 1. Create user. requiredActions is explicitly cleared so KC does not trigger
	// VERIFY_PROFILE or any other required-action interstitial during the OIDC flow.
	code, body, err := t.KeycloakAdminRequest(ctx, http.MethodPost, "/users", map[string]any{
		"username":        username,
		"email":           email,
		"emailVerified":   true,
		"enabled":         true,
		"firstName":       username,
		"lastName":        "OsacIdPTest",
		"requiredActions": []string{},
	})
	if err != nil {
		return "", fmt.Errorf("create KC osac user %q: %w", username, err)
	}
	if code != http.StatusCreated {
		return "", fmt.Errorf("create KC osac user %q: HTTP %d: %s", username, code, body)
	}

	// Look up user ID.
	code, body, err = t.KeycloakAdminRequest(ctx, http.MethodGet,
		fmt.Sprintf("/users?username=%s&exact=true", url.QueryEscape(username)), nil)
	if err != nil {
		return "", fmt.Errorf("look up KC osac user %q: %w", username, err)
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("look up KC osac user %q: HTTP %d: %s", username, code, body)
	}
	var users []struct {
		ID string `json:"id"`
	}
	if jsonErr := json.Unmarshal(body, &users); jsonErr != nil || len(users) == 0 {
		return "", fmt.Errorf("user %q not found in osac realm after creation (body=%s): %w",
			username, body, jsonErr)
	}
	userID = users[0].ID

	// 2. Set password.
	code, body, err = t.KeycloakAdminRequest(ctx, http.MethodPut,
		fmt.Sprintf("/users/%s/reset-password", userID),
		map[string]any{"type": "password", "value": username, "temporary": false})
	if err != nil {
		return "", fmt.Errorf("set password for KC osac user %q: %w", username, err)
	}
	if code != http.StatusNoContent {
		return "", fmt.Errorf("set password for KC osac user %q: HTTP %d: %s", username, code, body)
	}

	// 3. Link federated identity to the ext realm IdP.
	//    The identityProvider field must match the KC IdP alias exactly.
	code, body, err = t.KeycloakAdminRequest(ctx, http.MethodPost,
		fmt.Sprintf("/users/%s/federated-identity/%s", userID, idpAlias),
		map[string]any{
			"identityProvider": idpAlias,
			"userId":           externalSubject,
			"userName":         username,
		})
	if err != nil {
		return "", fmt.Errorf("link federated identity for %q → %q: %w", username, idpAlias, err)
	}
	if code != http.StatusCreated && code != http.StatusNoContent && code != http.StatusConflict {
		return "", fmt.Errorf("link federated identity for %q → %q: HTTP %d: %s",
			username, idpAlias, code, body)
	}

	// 4. Add user to the KC organization for this tenant and put them in /members.
	if addErr := t.addOIDCUserToOrg(ctx, userID, username, tenantName); addErr != nil {
		return "", addErr
	}

	return userID, nil
}

// addOIDCUserToOrg adds a KC osac realm user to the organization named tenantName and
// puts them in the /members group so the `organization` claim appears in their JWT.
func (t *Tool) addOIDCUserToOrg(ctx context.Context, userID, username, tenantName string) error {
	// Find (or create) the KC organization for this tenant.
	orgPayload := map[string]any{"name": tenantName, "enabled": true}
	code, body, err := t.KeycloakAdminRequest(ctx, http.MethodPost, "/organizations", orgPayload)
	if err != nil {
		return fmt.Errorf("ensure KC org %q: %w", tenantName, err)
	}
	if code != http.StatusCreated && code != http.StatusConflict {
		return fmt.Errorf("ensure KC org %q: HTTP %d: %s", tenantName, code, body)
	}

	code, body, err = t.KeycloakAdminRequest(ctx, http.MethodGet,
		fmt.Sprintf("/organizations?exact=true&search=%s", url.QueryEscape(tenantName)), nil)
	if err != nil {
		return fmt.Errorf("look up KC org %q: %w", tenantName, err)
	}
	if code != http.StatusOK {
		return fmt.Errorf("look up KC org %q: HTTP %d: %s", tenantName, code, body)
	}
	var orgs []struct {
		ID string `json:"id"`
	}
	if jsonErr := json.Unmarshal(body, &orgs); jsonErr != nil || len(orgs) == 0 {
		return fmt.Errorf("KC org %q not found after creation (body=%s)", tenantName, body)
	}
	orgID := orgs[0].ID

	// Add user to org.
	code, _, err = t.KeycloakAdminRequest(ctx, http.MethodPost,
		fmt.Sprintf("/organizations/%s/members", orgID), userID)
	if err != nil {
		return fmt.Errorf("add user %q to KC org %q: %w", username, tenantName, err)
	}
	if code != http.StatusCreated && code != http.StatusNoContent && code != http.StatusConflict {
		return fmt.Errorf("add user %q to KC org %q: HTTP %d", username, tenantName, code)
	}

	// Ensure the /members group exists in the org and add the user to it.
	groupPayload := map[string]any{"name": "/members"}
	code, body, err = t.KeycloakAdminRequest(ctx, http.MethodPost,
		fmt.Sprintf("/organizations/%s/groups", orgID), groupPayload)
	if err != nil {
		return fmt.Errorf("ensure /members group in KC org %q: %w", tenantName, err)
	}

	var groupID string
	if code == http.StatusCreated {
		var g map[string]any
		if jsonErr := json.Unmarshal(body, &g); jsonErr == nil {
			groupID, _ = g["id"].(string)
		}
	}
	if groupID == "" {
		_, body, err = t.KeycloakAdminRequest(ctx, http.MethodGet,
			fmt.Sprintf("/organizations/%s/groups", orgID), nil)
		if err != nil {
			return fmt.Errorf("list groups in KC org %q: %w", tenantName, err)
		}
		var groups []map[string]any
		if jsonErr := json.Unmarshal(body, &groups); jsonErr == nil {
			for _, g := range groups {
				if name, ok := g["name"].(string); ok && name == "/members" {
					groupID, _ = g["id"].(string)
					break
				}
			}
		}
	}
	if groupID == "" {
		return fmt.Errorf("could not find /members group in KC org %q", tenantName)
	}

	code, _, err = t.KeycloakAdminRequest(ctx, http.MethodPut,
		fmt.Sprintf("/organizations/%s/groups/%s/members/%s", orgID, groupID, userID), nil)
	if err != nil {
		return fmt.Errorf("add user %q to /members group in KC org %q: %w", username, tenantName, err)
	}
	if code != http.StatusOK && code != http.StatusCreated &&
		code != http.StatusNoContent && code != http.StatusConflict {
		return fmt.Errorf("add user %q to /members group in KC org %q: HTTP %d", username, tenantName, code)
	}
	return nil
}

// SimulateOIDCLogin drives the full OIDC authorization code redirect chain to obtain a
// Keycloak JWT for an external IdP user. This mirrors the flow that `osac login` executes.
//
// The redirect chain is:
//  1. Test runner  → GET KC /auth?kc_idp_hint=<alias>&code_challenge=... (PKCE S256)
//  2. ← 302 → ext realm login page
//  3. Test runner  → GET ext realm login page (KC login form HTML)
//  4. Test runner  → POST credentials to login form action URL
//  5. ← 302 → KC /broker/<alias>/endpoint?code=<ext-code>
//  6. Test runner  → GET KC broker endpoint
//  7. KC (in-Kind) → POST ext realm /token (cluster-internal — same KC instance ✓)
//  8. KC (in-Kind) → GET  ext realm /certs (JWKS — cluster-internal ✓)
//  9. ← 302 → http://localhost?code=<kc-code>
//  10. Test runner  → POST KC /token (authorization_code grant + PKCE verifier)
//  11. ← KC JWT access_token
//
// Steps 7–8 are server-to-server calls from KC to the ext realm. Because both realms
// live in the same Keycloak instance, these calls use the cluster-internal address and
// never need to leave the cluster — eliminating the host-to-cluster networking issue.
//
// idpAlias is the Keycloak IdP alias (typically "<tenantName>-<idpName>").
// username and password are credentials for a user in the ext realm.
// Pass empty strings if login is expected to fail before reaching the login form.
func (t *Tool) SimulateOIDCLogin(ctx context.Context, idpAlias, username, password string) (string, error) {
	const callbackBase = "http://localhost"

	codeVerifier, codeChallenge, err := generatePKCE()
	if err != nil {
		return "", fmt.Errorf("failed to generate PKCE challenge: %w", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create cookie jar: %w", err)
	}

	// Do NOT auto-follow redirects — we drive each hop manually so we can detect
	// KC login forms and submit credentials.
	httpClient := &http.Client{
		Jar: jar,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    t.caPool,
				MinVersion: tls.VersionTLS12,
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	type hop struct {
		method string
		rawURL string
		form   url.Values
	}

	authURL := fmt.Sprintf(
		"https://%s/realms/osac/protocol/openid-connect/auth?"+
			"client_id=osac-cli&response_type=code"+
			"&redirect_uri=%s&state=%s"+
			"&kc_idp_hint=%s&scope=%s"+
			"&code_challenge=%s&code_challenge_method=S256",
		keycloakAddr,
		url.QueryEscape(callbackBase),
		url.QueryEscape("osac-it-"+idpAlias),
		url.QueryEscape(idpAlias),
		url.QueryEscape("openid organization"),
		url.QueryEscape(codeChallenge),
	)

	next := hop{method: http.MethodGet, rawURL: authURL}
	var redirectLog []string

	for i := 0; i < 20; i++ {
		var bodyReader io.Reader
		if next.form != nil {
			bodyReader = strings.NewReader(next.form.Encode())
		}
		req, err := http.NewRequestWithContext(ctx, next.method, next.rawURL, bodyReader)
		if err != nil {
			return "", fmt.Errorf("hop %d: failed to build request to %s: %w", i, next.rawURL, err)
		}
		if next.form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("hop %d: request to %s failed: %w\nchain:\n%s",
				i, next.rawURL, err, strings.Join(redirectLog, "\n"))
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		resp.Body.Close()

		redirectLog = append(redirectLog, fmt.Sprintf("hop %d: %s %s → HTTP %d",
			i, next.method, next.rawURL, resp.StatusCode))

		switch resp.StatusCode {
		case http.StatusFound, http.StatusSeeOther, http.StatusMovedPermanently,
			http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
			location := resp.Header.Get("Location")
			if location == "" {
				return "", fmt.Errorf("hop %d: %d response has no Location header\nchain:\n%s",
					i, resp.StatusCode, strings.Join(redirectLog, "\n"))
			}
			// Resolve relative Location URLs against the current request URL.
			locURL, resolveErr := req.URL.Parse(location)
			if resolveErr != nil {
				return "", fmt.Errorf("hop %d: invalid Location %q: %w", i, location, resolveErr)
			}
			location = locURL.String()

			if strings.HasPrefix(location, callbackBase) {
				// KC redirected to the callback — extract the authorization code.
				parsed, parseErr := url.Parse(location)
				if parseErr != nil {
					return "", fmt.Errorf("hop %d: invalid callback URL %q: %w", i, location, parseErr)
				}
				if kcErr := parsed.Query().Get("error"); kcErr != "" {
					return "", fmt.Errorf("KC error at callback: %s — %s",
						kcErr, parsed.Query().Get("error_description"))
				}
				kcCode := parsed.Query().Get("code")
				if kcCode == "" {
					return "", fmt.Errorf("hop %d: callback URL has no code: %s", i, location)
				}
				return t.exchangeKCCode(ctx, httpClient, kcCode, callbackBase, codeVerifier)
			}
			next = hop{method: http.MethodGet, rawURL: location}

		case http.StatusOK:
			// Check for a KC login form (e.g. the ext realm presenting its login page).
			formAction := extractKCLoginFormAction(respBody)
			if formAction == "" {
				return "", fmt.Errorf("hop %d: got HTTP 200 but response is not a KC login form\nchain:\n%s\nbody (first 500 chars): %.500s",
					i, strings.Join(redirectLog, "\n"), string(respBody))
			}
			if username == "" || password == "" {
				return "", fmt.Errorf("hop %d: KC login form presented but no credentials provided (idpAlias=%s)",
					i, idpAlias)
			}
			// Resolve the form action URL relative to the current request URL.
			actionURL, resolveErr := req.URL.Parse(formAction)
			if resolveErr != nil {
				return "", fmt.Errorf("hop %d: invalid form action %q: %w", i, formAction, resolveErr)
			}
			next = hop{
				method: http.MethodPost,
				rawURL: actionURL.String(),
				form: url.Values{
					"username":     {username},
					"password":     {password},
					"credentialId": {""},
				},
			}

		default:
			return "", fmt.Errorf("hop %d: unexpected HTTP %d at %s\nchain:\n%s\nbody (first 500 chars): %.500s",
				i, resp.StatusCode, next.rawURL, strings.Join(redirectLog, "\n"), string(respBody))
		}
	}

	return "", fmt.Errorf("OIDC redirect chain exceeded maximum hops\nchain:\n%s", strings.Join(redirectLog, "\n"))
}

// exchangeKCCode exchanges a KC authorization code for a JWT access token.
func (t *Tool) exchangeKCCode(ctx context.Context, httpClient *http.Client, code, redirectURI, codeVerifier string) (string, error) {
	tokenURL := fmt.Sprintf("https://%s/realms/osac/protocol/openid-connect/token", keycloakAddr)
	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL,
		strings.NewReader(url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {redirectURI},
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
		return "", fmt.Errorf("token exchange returned HTTP %d: %s", tokenResp.StatusCode, body)
	}

	var payload struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}
	if payload.Error != "" {
		return "", fmt.Errorf("token exchange error %q: %s", payload.Error, payload.Description)
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("token exchange returned OK but access_token is empty")
	}
	return payload.AccessToken, nil
}

// MakeOIDCGRPCConn creates a gRPC connection to the OSAC external API authenticated with
// the given raw JWT bearer token. Intended for use with tokens from SimulateOIDCLogin.
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

// WaitForKeycloakIdP polls until the Keycloak admin API reports the IdP alias as present
// in the osac realm.
func (t *Tool) WaitForKeycloakIdP(ctx context.Context, alias string) error {
	for {
		code, _, err := t.KeycloakAdminRequest(ctx, http.MethodGet,
			fmt.Sprintf("/identity-provider/instances/%s", alias), nil)
		if err == nil && code == http.StatusOK {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for KC IdP alias %q", alias)
		default:
		}
	}
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

// kcLoginFormActionRe matches the action attribute of KC's login form.
// KC login forms look like:
//
//	<form id="kc-form-login" ... action="https://keycloak/realms/xxx/login-actions/authenticate?...">
var kcLoginFormActionRe = regexp.MustCompile(`action="(https?://[^"]+/login-actions/authenticate[^"]*)"`)

// extractKCLoginFormAction parses a KC login page HTML body and returns the form action
// URL, or an empty string if the body is not a KC login form.
func extractKCLoginFormAction(body []byte) string {
	matches := kcLoginFormActionRe.FindSubmatch(body)
	if len(matches) < 2 {
		return ""
	}
	// KC HTML-encodes & as &amp; in attribute values.
	return strings.ReplaceAll(string(matches[1]), "&amp;", "&")
}
