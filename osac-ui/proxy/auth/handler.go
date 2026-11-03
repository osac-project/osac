package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Handler implements the BFF auth endpoints for the OIDC PKCE flow.
type Handler struct {
	// ClientID is the OIDC client_id registered in the IdP for this UI application.
	ClientID string
	// BaseUIURL is the public base URL of the UI (e.g. "https://ui.example.com").
	// Used to compute the /callback redirect URI. If empty, the request origin is used.
	BaseUIURL string
	// FulfillmentHTTPClient is used to call the fulfillment capabilities endpoint.
	FulfillmentHTTPClient *http.Client
	// FulfillmentAPIURL is the internal URL of the fulfillment API.
	FulfillmentAPIURL string
	// OIDCHTTPClient is used for all OIDC requests (discovery, token exchange, refresh).
	// If nil, the proxy's safe OIDC client is used. Tests and development callers may
	// provide a client with custom TLS roots for private IdP certificates.
	OIDCHTTPClient *http.Client

	issuerMu      sync.RWMutex
	cachedIssuer  string
	issuerExpires time.Time
}

type loginStartResponse struct {
	URL string `json:"url"`
}

type loginCallbackRequest struct {
	Code string `json:"code"`
}

type loginCallbackResponse struct {
	ExpiresIn int `json:"expiresIn"`
}

type loginInfoResponse struct {
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
	Groups   []string `json:"groups"`
	TenantID string   `json:"tenantId"`
}

// GetLogin handles GET /api/login — starts the OIDC Authorization Code + PKCE flow.
// Query params:
//   - redirect_base (required): window.location.origin from the SPA.
func (h *Handler) GetLogin(w http.ResponseWriter, r *http.Request) {
	redirectBase := r.URL.Query().Get("redirect_base")
	if redirectBase == "" {
		log.Warn("login request missing required redirect_base query param")
		http.Error(w, "redirect_base is required", http.StatusBadRequest)
		return
	}

	redirectURI, err := h.resolveCallbackURI(r, redirectBase)
	if err != nil {
		log.WithError(err).Error("failed to resolve callback URI")
		http.Error(w, "invalid redirect_base", http.StatusBadRequest)
		return
	}

	issuerURL, err := h.issuerURL()
	if err != nil {
		log.WithError(err).Error("failed to get OIDC issuer URL")
		http.Error(w, "could not determine OIDC issuer", http.StatusBadGateway)
		return
	}

	oidcCfg, err := FetchOIDCConfig(issuerURL, h.OIDCHTTPClient)
	if err != nil {
		log.WithError(err).Error("OIDC discovery failed")
		http.Error(w, "OIDC discovery failed", http.StatusBadGateway)
		return
	}

	verifier, err := generateCodeVerifier()
	if err != nil {
		log.WithError(err).Error("failed to generate PKCE verifier")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	state, err := generateState()
	if err != nil {
		log.WithError(err).Error("failed to generate state")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := setAuthFlowCookie(w, r, state, authFlowCookie{
		Verifier:    verifier,
		RedirectURI: redirectURI,
		IssuerURL:   issuerURL,
	}, h.BaseUIURL); err != nil {
		log.WithError(err).Error("failed to set auth flow cookie")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	authorizeURL := BuildAuthorizeURL(oidcCfg, h.ClientID, redirectURI, state, codeChallenge(verifier))

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(loginStartResponse{URL: authorizeURL}); err != nil {
		log.WithError(err).Warn("failed to encode login start response")
	}
}

// PostLogin handles POST /api/login?state=<state> — exchanges the authorization code for tokens.
// Request body: { "code": "<authorization_code>" }
func (h *Handler) PostLogin(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state == "" {
		log.Warn("login callback missing required state query param")
		http.Error(w, "state is required", http.StatusBadRequest)
		return
	}

	var body loginCallbackRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Code == "" {
		log.WithError(err).Warn("login callback body missing or invalid (expected {\"code\": \"...\"})")
		http.Error(w, "request body must be {\"code\": \"...\"}", http.StatusBadRequest)
		return
	}

	flow, err := getAndClearAuthFlowCookie(w, r, state)
	if err != nil {
		log.WithError(err).Warn("auth flow cookie missing or invalid")
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}

	issuerURL, err := h.issuerURL()
	if err != nil {
		log.WithError(err).Error("failed to get OIDC issuer URL during token exchange")
		http.Error(w, "could not determine OIDC issuer", http.StatusBadGateway)
		return
	}
	if !sameOIDCIssuer(flow.IssuerURL, issuerURL) {
		log.Warn("auth flow cookie issuer does not match the configured issuer")
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}

	// The flow cookie is client-controlled. Use the issuer advertised by the
	// Fulfillment service as the request destination after binding the state to it.
	oidcCfg, err := FetchOIDCConfig(issuerURL, h.OIDCHTTPClient)
	if err != nil {
		log.WithError(err).Error("OIDC discovery failed during token exchange")
		http.Error(w, "OIDC discovery failed", http.StatusBadGateway)
		return
	}

	tr, err := ExchangeCode(oidcCfg, h.ClientID, body.Code, flow.Verifier, flow.RedirectURI, h.OIDCHTTPClient)
	if err != nil {
		log.WithError(err).Error("token exchange failed")
		http.Error(w, "token exchange failed", http.StatusBadGateway)
		return
	}

	tokenData := TokenData{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		IDToken:      tr.IDToken,
	}
	SetSessionCookies(w, r, tokenData, tr.ExpiresIn, h.BaseUIURL)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(loginCallbackResponse{ExpiresIn: tr.ExpiresIn}); err != nil {
		log.WithError(err).Warn("failed to encode login callback response")
	}
}

// GetLoginInfo handles GET /api/login/info — returns the username if there is an active session.
func (h *Handler) GetLoginInfo(w http.ResponseWriter, r *http.Request) {
	tokenData := LookupSessionCookies(r)
	if tokenData == nil {
		log.Warn("login info requested with no active session (missing or expired access cookie)")
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	// Use the ID token for user identity (richer OIDC claims); fall back to access token.
	idToken := tokenData.IDToken
	if idToken == "" {
		idToken = tokenData.AccessToken
	}
	// Use the access token for roles: Keycloak only includes realm_access.roles
	// in the access token (not the ID token) by default.
	roleToken := tokenData.AccessToken
	if roleToken == "" {
		roleToken = idToken
	}

	username := UsernameFromToken(idToken)
	roles := RolesFromToken(roleToken)
	groups := GroupsFromToken(roleToken)
	tenantID := TenantIdFromToken(idToken)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(loginInfoResponse{Username: username, Roles: roles, Groups: groups, TenantID: tenantID}); err != nil {
		log.WithError(err).Warn("failed to encode login info response")
	}
}

// GetLoginRefresh handles GET /api/login/refresh — refreshes the session using the refresh token.
func (h *Handler) GetLoginRefresh(w http.ResponseWriter, r *http.Request) {
	tokenData := LookupRefreshCookies(r)
	if tokenData == nil {
		log.Warn("login refresh requested with no active session (missing refresh cookie)")
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	if tokenData.RefreshToken == "" {
		log.Warn("login refresh requested but refresh cookie was empty")
		http.Error(w, "no refresh token available", http.StatusBadRequest)
		return
	}

	issuerURL, err := h.issuerURL()
	if err != nil {
		log.WithError(err).Error("failed to get OIDC issuer URL for refresh")
		http.Error(w, "could not determine OIDC issuer", http.StatusBadGateway)
		return
	}

	oidcCfg, err := FetchOIDCConfig(issuerURL, h.OIDCHTTPClient)
	if err != nil {
		log.WithError(err).Error("OIDC discovery failed during refresh")
		http.Error(w, "OIDC discovery failed", http.StatusBadGateway)
		return
	}

	tr, err := RefreshTokens(oidcCfg, h.ClientID, tokenData.RefreshToken, h.OIDCHTTPClient)
	if err != nil {
		log.WithError(err).Warn("token refresh failed, clearing session")
		ClearSessionCookies(w, r, h.BaseUIURL)
		http.Error(w, "token refresh failed", http.StatusUnauthorized)
		return
	}

	newData := TokenData{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		IDToken:      tr.IDToken,
	}
	if newData.RefreshToken == "" {
		newData.RefreshToken = tokenData.RefreshToken
	}
	SetSessionCookies(w, r, newData, tr.ExpiresIn, h.BaseUIURL)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(loginCallbackResponse{ExpiresIn: tr.ExpiresIn}); err != nil {
		log.WithError(err).Warn("failed to encode login refresh response")
	}
}

// PostLogout handles POST /api/logout — logs out from Keycloak and expires all session cookies.
func (h *Handler) PostLogout(w http.ResponseWriter, r *http.Request) {
	tokenData := LookupSessionCookies(r)

	if tokenData != nil {
		issuerURL, err := h.issuerURL()
		if err != nil {
			log.WithError(err).Error("failed to get OIDC issuer URL for logout")
			http.Error(w, "could not determine OIDC issuer", http.StatusBadGateway)
			return
		}

		oidcCfg, err := FetchOIDCConfig(issuerURL, h.OIDCHTTPClient)
		if err != nil {
			log.WithError(err).Error("OIDC discovery failed during logout")
			http.Error(w, "OIDC discovery failed", http.StatusBadGateway)
			return
		}

		if err := EndSession(oidcCfg, h.ClientID, tokenData.RefreshToken, tokenData.IDToken, h.OIDCHTTPClient); err != nil {
			log.WithError(err).Error("Keycloak end_session failed")
			ClearSessionCookies(w, r, h.BaseUIURL)
			http.Error(w, "logout from identity provider failed", http.StatusBadGateway)
			return
		}
	}

	ClearSessionCookies(w, r, h.BaseUIURL)
	w.WriteHeader(http.StatusNoContent)
}

const issuerCacheTTL = 5 * time.Minute

// issuerURL returns the OIDC issuer URL, using a 5-minute cache to avoid
// hitting the capabilities endpoint on every auth request.
func (h *Handler) issuerURL() (string, error) {
	h.issuerMu.RLock()
	if h.cachedIssuer != "" && time.Now().Before(h.issuerExpires) {
		issuer := h.cachedIssuer
		h.issuerMu.RUnlock()
		return issuer, nil
	}
	h.issuerMu.RUnlock()

	issuer, err := FetchIssuerURL(h.FulfillmentAPIURL, h.FulfillmentHTTPClient)
	if err != nil {
		return "", err
	}

	h.issuerMu.Lock()
	h.cachedIssuer = issuer
	h.issuerExpires = time.Now().Add(issuerCacheTTL)
	h.issuerMu.Unlock()

	return issuer, nil
}

// resolveCallbackURI computes the /callback redirect URI from the SPA's redirect_base.
// If BaseUIURL is configured, it is used instead (useful behind ingress where the
// request origin may differ from the public URL). Otherwise the browser-provided
// origin must match the request origin.
func (h *Handler) resolveCallbackURI(r *http.Request, redirectBase string) (string, error) {
	base := h.BaseUIURL
	if base == "" {
		redirectURL, err := parseCallbackBase(redirectBase)
		if err != nil {
			return "", err
		}
		requestURL, err := requestOrigin(r)
		if err != nil {
			return "", err
		}
		if !sameOrigin(requestURL, redirectURL) {
			return "", fmt.Errorf("redirect_base must match the request origin")
		}
		base = redirectBase
	}
	u, err := parseCallbackBase(base)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/callback"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func parseCallbackBase(raw string) (*url.URL, error) {
	base, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (!strings.EqualFold(base.Scheme, "http") && !strings.EqualFold(base.Scheme, "https")) ||
		base.Host == "" || base.Hostname() == "" || base.User != nil || base.Opaque != "" ||
		base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || base.RawFragment != "" {
		return nil, fmt.Errorf("redirect_base must be an HTTP(S) URL without credentials, query, or fragment")
	}
	base.Scheme = strings.ToLower(base.Scheme)
	return base, nil
}

func requestOrigin(r *http.Request) (*url.URL, error) {
	if r == nil || r.Host == "" {
		return nil, fmt.Errorf("request origin is unavailable")
	}
	scheme := "http"
	if IsSecure(r, "") {
		scheme = "https"
	}
	return &url.URL{Scheme: scheme, Host: r.Host}, nil
}
