package auth

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestResolveCallbackURIRejectsCrossOriginRedirectBase(t *testing.T) {
	h := &Handler{}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://ui.example.com/api/login", nil)

	if _, err := h.resolveCallbackURI(request, "https://attacker.example"); err == nil {
		t.Fatal("cross-origin redirect_base was accepted")
	}
}

func TestResolveCallbackURIUsesSameOriginOrConfiguredPublicBase(t *testing.T) {
	t.Run("request origin with ingress HTTPS", func(t *testing.T) {
		h := &Handler{}
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://ui.example.com/api/login", nil)
		request.Header.Set("X-Forwarded-Proto", "https")

		got, err := h.resolveCallbackURI(request, "https://ui.example.com")
		if err != nil {
			t.Fatalf("resolve callback URI: %v", err)
		}
		if got != "https://ui.example.com/callback" {
			t.Fatalf("callback URI = %q, want https://ui.example.com/callback", got)
		}
	})

	t.Run("configured public base overrides internal request origin", func(t *testing.T) {
		h := &Handler{BaseUIURL: "https://public.example.com"}
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://ui.internal/api/login", nil)

		got, err := h.resolveCallbackURI(request, "https://attacker.example")
		if err != nil {
			t.Fatalf("resolve callback URI: %v", err)
		}
		if got != "https://public.example.com/callback" {
			t.Fatalf("callback URI = %q, want https://public.example.com/callback", got)
		}
	})
}

func TestHandler_IssuerURLCachesResult(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(capabilitiesResponse{
			Authn: struct {
				TrustedTokenIssuers []string `json:"trusted_token_issuers"`
			}{
				TrustedTokenIssuers: []string{"https://keycloak.example.com/realms/osac"},
			},
		})
	}))
	defer srv.Close()

	h := &Handler{
		FulfillmentAPIURL:     srv.URL,
		FulfillmentHTTPClient: srv.Client(),
	}

	issuer1, err := h.issuerURL()
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	issuer2, err := h.issuerURL()
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if issuer1 != issuer2 {
		t.Fatalf("issuer mismatch: %q vs %q", issuer1, issuer2)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 upstream call (cached), got %d", got)
	}
}

func TestHandler_IssuerURLRefetchesAfterExpiry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(capabilitiesResponse{
			Authn: struct {
				TrustedTokenIssuers []string `json:"trusted_token_issuers"`
			}{
				TrustedTokenIssuers: []string{"https://keycloak.example.com/realms/osac"},
			},
		})
	}))
	defer srv.Close()

	h := &Handler{
		FulfillmentAPIURL:     srv.URL,
		FulfillmentHTTPClient: srv.Client(),
	}

	_, err := h.issuerURL()
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Force cache expiry.
	h.issuerMu.Lock()
	h.issuerExpires = time.Now().Add(-1 * time.Second)
	h.issuerMu.Unlock()

	_, err = h.issuerURL()
	if err != nil {
		t.Fatalf("second call after expiry: %v", err)
	}

	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 upstream calls (cache expired), got %d", got)
	}
}

func TestPostLoginRejectsIssuerTamperingInAuthFlowCookie(t *testing.T) {
	const state = "state-for-cookie-test"
	const trustedIssuer = "https://keycloak.example.com/realms/osac"
	var oidcRequests atomic.Int32

	fulfillment := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/fulfillment/v1/capabilities" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(capabilitiesResponse{
			Authn: struct {
				TrustedTokenIssuers []string `json:"trusted_token_issuers"`
			}{TrustedTokenIssuers: []string{trustedIssuer}},
		})
	}))
	defer fulfillment.Close()

	h := &Handler{
		ClientID:              "test-client",
		FulfillmentAPIURL:     fulfillment.URL,
		FulfillmentHTTPClient: fulfillment.Client(),
		OIDCHTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			oidcRequests.Add(1)
			return nil, errors.New("unexpected OIDC request")
		})},
	}

	cookieWriter := httptest.NewRecorder()
	cookieRequest := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "https://ui.example.com/api/login", nil,
	)
	if err := setAuthFlowCookie(cookieWriter, cookieRequest, state, authFlowCookie{
		Verifier:    "verifier",
		RedirectURI: "https://ui.example.com/callback",
		IssuerURL:   "https://attacker.example/realms/evil",
	}, "https://ui.example.com"); err != nil {
		t.Fatalf("set forged auth-flow cookie: %v", err)
	}
	cookies := cookieWriter.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}

	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/login?state="+state, strings.NewReader(`{"code":"attacker-code"}`))
	request.AddCookie(cookies[0])
	recorder := httptest.NewRecorder()
	h.PostLogin(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if got := oidcRequests.Load(); got != 0 {
		t.Fatalf("made %d OIDC requests for a forged issuer, want none", got)
	}
}

func TestPostLogoutClearsLocalSessionWhenIdPEndpointIsUntrusted(t *testing.T) {
	const trustedIssuer = "https://keycloak.example.com/realms/osac"
	var oidcRequests atomic.Int32

	fulfillment := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/fulfillment/v1/capabilities" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(capabilitiesResponse{
			Authn: struct {
				TrustedTokenIssuers []string `json:"trusted_token_issuers"`
			}{TrustedTokenIssuers: []string{trustedIssuer}},
		})
	}))
	defer fulfillment.Close()

	h := &Handler{
		ClientID:              "test-client",
		FulfillmentAPIURL:     fulfillment.URL,
		FulfillmentHTTPClient: fulfillment.Client(),
		OIDCHTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			oidcRequests.Add(1)
			if request.URL.Path != "/realms/osac/.well-known/openid-configuration" {
				t.Errorf("OIDC request path = %q, want discovery", request.URL.Path)
			}
			metadata, err := json.Marshal(OIDCConfig{
				Issuer:                trustedIssuer,
				AuthorizationEndpoint: trustedIssuer + "/authorize",
				TokenEndpoint:         trustedIssuer + "/token",
				EndSessionEndpoint:    "https://logout.attacker.example/logout",
			})
			if err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(string(metadata))),
				Request:    request,
			}, nil
		})},
	}

	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/logout", nil)
	request.AddCookie(&http.Cookie{Name: accessTokenCookieName, Value: "access-token"})
	request.AddCookie(&http.Cookie{Name: refreshTokenCookieName, Value: "refresh-token"})
	request.AddCookie(&http.Cookie{Name: idTokenCookieName, Value: "id-token"})
	recorder := httptest.NewRecorder()
	h.PostLogout(recorder, request)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body: %s", recorder.Code, http.StatusBadGateway, recorder.Body.String())
	}
	if got := oidcRequests.Load(); got != 1 {
		t.Fatalf("made %d OIDC requests, want discovery only", got)
	}
	assertSessionCookiesCleared(t, recorder)
}

func TestPostLogoutClearsLocalSessionWhenIssuerLookupOrDiscoveryFails(t *testing.T) {
	const trustedIssuer = "https://keycloak.example.test/realms/osac"

	for _, test := range []struct {
		name             string
		failIssuerLookup bool
		wantOIDCRequests int32
	}{
		{name: "issuer lookup failure", failIssuerLookup: true},
		{name: "discovery failure", wantOIDCRequests: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var oidcRequests atomic.Int32
			fulfillment := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/fulfillment/v1/capabilities" {
					http.NotFound(w, r)
					return
				}
				if test.failIssuerLookup {
					http.Error(w, "capabilities unavailable", http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(capabilitiesResponse{
					Authn: struct {
						TrustedTokenIssuers []string `json:"trusted_token_issuers"`
					}{TrustedTokenIssuers: []string{trustedIssuer}},
				})
			}))
			defer fulfillment.Close()

			h := &Handler{
				FulfillmentAPIURL:     fulfillment.URL,
				FulfillmentHTTPClient: fulfillment.Client(),
				OIDCHTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					oidcRequests.Add(1)
					return nil, errors.New("OIDC discovery unavailable")
				})},
			}

			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/logout", nil)
			request.AddCookie(&http.Cookie{Name: accessTokenCookieName, Value: "access-token"})
			request.AddCookie(&http.Cookie{Name: refreshTokenCookieName, Value: "refresh-token"})
			request.AddCookie(&http.Cookie{Name: idTokenCookieName, Value: "id-token"})
			recorder := httptest.NewRecorder()
			h.PostLogout(recorder, request)

			if recorder.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want %d; body: %s", recorder.Code, http.StatusBadGateway, recorder.Body.String())
			}
			if got := oidcRequests.Load(); got != test.wantOIDCRequests {
				t.Fatalf("made %d OIDC requests, want %d", got, test.wantOIDCRequests)
			}
			assertSessionCookiesCleared(t, recorder)
		})
	}
}

func assertSessionCookiesCleared(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	cleared := make(map[string]bool)
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.MaxAge < 0 {
			cleared[cookie.Name] = true
		}
	}
	for _, name := range []string{accessTokenCookieName, refreshTokenCookieName, idTokenCookieName} {
		if !cleared[name] {
			t.Errorf("session cookie %q was not cleared", name)
		}
	}
}

// TestGetLoginRefreshRefreshOnlyCookie covers the path exercised after the short-lived
// access cookie has expired but the refresh cookie is still valid: LookupRefreshCookies
// must find the session from the refresh cookie alone, and the handler must reissue
// session cookies from the refreshed tokens.
func TestGetLoginRefreshRefreshOnlyCookie(t *testing.T) {
	t.Parallel()

	const oldRefreshToken = "old-refresh-token"
	const newAccessToken = "new-access-token"
	const newRefreshToken = "new-refresh-token"

	var issuer string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/fulfillment/v1/capabilities", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"authn": map[string]any{"trusted_token_issuers": []string{issuer}},
		}); err != nil {
			t.Errorf("encode capabilities response: %v", err)
		}
	})
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(OIDCConfig{Issuer: issuer, TokenEndpoint: issuer + "/token"}); err != nil {
			t.Errorf("encode OIDC discovery response: %v", err)
		}
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse token request form: %v", err)
		}
		if got := r.FormValue("grant_type"); got != "refresh_token" {
			t.Fatalf("grant_type = %q, want refresh_token", got)
		}
		if got := r.FormValue("refresh_token"); got != oldRefreshToken {
			t.Fatalf("refresh_token = %q, want %q", got, oldRefreshToken)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(TokenResponse{
			AccessToken:  newAccessToken,
			RefreshToken: newRefreshToken,
			ExpiresIn:    300,
		}); err != nil {
			t.Errorf("encode token response: %v", err)
		}
	})

	server := httptest.NewTLSServer(mux)
	defer server.Close()
	issuer = server.URL

	h := &Handler{
		ClientID:              "test-client",
		FulfillmentAPIURL:     server.URL,
		FulfillmentHTTPClient: server.Client(),
		OIDCHTTPClient:        server.Client(),
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/login/refresh", nil)
	req.AddCookie(&http.Cookie{Name: refreshTokenCookieName, Value: oldRefreshToken})

	rec := httptest.NewRecorder()
	h.GetLoginRefresh(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var gotAccess, gotRefresh bool
	for _, c := range rec.Result().Cookies() {
		switch {
		case c.Name == accessTokenCookieName && c.Value == newAccessToken:
			gotAccess = true
		case c.Name == refreshTokenCookieName && c.Value == newRefreshToken:
			gotRefresh = true
		}
	}
	if !gotAccess {
		t.Fatalf("expected reissued access cookie %q, got %+v", newAccessToken, rec.Result().Cookies())
	}
	if !gotRefresh {
		t.Fatalf("expected reissued refresh cookie %q, got %+v", newRefreshToken, rec.Result().Cookies())
	}
}
