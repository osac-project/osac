package auth

import (
	"errors"
	"net"
	"net/url"
	"strings"
	"testing"
)

func TestBuildAuthorizeURL_IncludesOrganizationScope(t *testing.T) {
	cfg := &OIDCConfig{
		AuthorizationEndpoint: "https://keycloak.example.com/realms/osac/protocol/openid-connect/auth",
	}

	rawURL := BuildAuthorizeURL(cfg, "osac-ui", "https://app.example.com/api/login/callback", "test-state", "test-challenge")

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("failed to parse authorize URL: %v", err)
	}

	scope := parsed.Query().Get("scope")
	if scope == "" {
		t.Fatal("scope parameter missing from authorize URL")
	}

	wantScopes := []string{"openid", "organization"}
	for _, s := range wantScopes {
		found := false
		for _, got := range splitScopes(scope) {
			if got == s {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("scope %q not found in authorize URL scope=%q", s, scope)
		}
	}
}

func TestBuildAuthorizeURL_SetsAllRequiredParams(t *testing.T) {
	cfg := &OIDCConfig{
		AuthorizationEndpoint: "https://keycloak.example.com/auth",
	}

	rawURL := BuildAuthorizeURL(cfg, "my-client", "https://example.com/callback", "my-state", "my-challenge")

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("failed to parse authorize URL: %v", err)
	}

	q := parsed.Query()
	checks := map[string]string{
		"response_type":         "code",
		"client_id":             "my-client",
		"redirect_uri":          "https://example.com/callback",
		"state":                 "my-state",
		"code_challenge":        "my-challenge",
		"code_challenge_method": "S256",
	}

	for param, want := range checks {
		if got := q.Get(param); got != want {
			t.Errorf("param %q = %q, want %q", param, got, want)
		}
	}
}

func TestSanitizeNetErr_DNSFailureOmitsHostname(t *testing.T) {
	const hostname = "keycloak-keycloak.internal.example.com"
	dnsErr := &net.DNSError{
		Err:  "no such host",
		Name: hostname,
	}
	wrapped := &url.Error{
		Op:  "Get",
		URL: "https://" + hostname + "/.well-known/openid-configuration",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: dnsErr},
	}

	sanitized := sanitizeNetErr(wrapped)

	if strings.Contains(sanitized.Error(), hostname) {
		t.Errorf("sanitized error contains hostname %q: %s", hostname, sanitized.Error())
	}
	// Unwrap must preserve the cause so errors.Is/As still work.
	if !errors.Is(sanitized, dnsErr) {
		t.Error("sanitized error does not preserve DNS cause via Unwrap")
	}
}

func splitScopes(scope string) []string {
	var scopes []string
	current := ""
	for _, c := range scope {
		if c == ' ' {
			if current != "" {
				scopes = append(scopes, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		scopes = append(scopes, current)
	}
	return scopes
}
