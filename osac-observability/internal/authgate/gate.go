// Package authgate authenticates the admin query Routes before either OAuth
// proxy sees the request, so a bearer client gets a plain 401 or 403 instead of
// a login redirect from the stock proxies. Browser sessions and the OAuth
// callback path are passed through to the proxy unchanged.
package authgate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
)

// ErrUnauthenticated means the credential is absent, malformed or expired.
// ErrForbidden means it is valid but the caller lacks the required role or
// group.
var (
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrForbidden       = errors.New("forbidden")
)

// Authenticator validates one bearer token.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) error
}

// Config carries the gate's upstreams, cookie name and exempt paths.
type Config struct {
	ProxyURL          string
	AllowlistURL      string
	SessionCookieName string
	Authenticator     Authenticator
	ExemptPaths       []string
}

// Gate routes one request to the OAuth proxy or the allowlist sidecar.
type Gate struct {
	proxy     *httputil.ReverseProxy
	allowlist *httputil.ReverseProxy
	auth      Authenticator
	cookie    string
	exempt    []string
}

// New builds a Gate. Both upstreams must be absolute URLs and an
// authenticator is mandatory: a gate that cannot decide is not a gate.
func New(cfg Config) (*Gate, error) {
	if cfg.Authenticator == nil {
		return nil, errors.New("an authenticator is required")
	}
	proxy, err := newProxy(cfg.ProxyURL)
	if err != nil {
		return nil, fmt.Errorf("PROXY_URL: %w", err)
	}
	allowlist, err := newProxy(cfg.AllowlistURL)
	if err != nil {
		return nil, fmt.Errorf("ALLOWLIST_URL: %w", err)
	}
	if cfg.SessionCookieName == "" {
		return nil, errors.New("a session cookie name is required")
	}
	return &Gate{
		proxy:     proxy,
		allowlist: allowlist,
		auth:      cfg.Authenticator,
		cookie:    cfg.SessionCookieName,
		exempt:    cfg.ExemptPaths,
	}, nil
}

func newProxy(raw string) (*httputil.ReverseProxy, error) {
	target, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("%q is not an absolute URL", raw)
	}
	reverse := httputil.NewSingleHostReverseProxy(target)
	reverse.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, "upstream unavailable: "+err.Error(), http.StatusBadGateway)
	}
	return reverse, nil
}

// ServeHTTP applies the contract:
//
//   - the gate answers its own liveness probe;
//   - an exempt path (OAuth callback, health) goes to the proxy;
//   - a bearer token is validated here: 401 invalid, 403 valid without the
//     role or group, otherwise the request goes straight to the allowlist;
//   - a session cookie goes to the proxy, which owns the browser login state;
//   - anything else is 401.
func (g *Gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/livez" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		w.WriteHeader(http.StatusOK)
		return
	}
	if g.isExempt(path.Clean(r.URL.Path)) {
		g.proxy.ServeHTTP(w, r)
		return
	}
	if token := bearerToken(r); token != "" {
		switch err := g.auth.Authenticate(r.Context(), token); {
		case errors.Is(err, ErrForbidden):
			w.Header().Set("WWW-Authenticate", `Bearer realm="osac", error="insufficient_scope"`)
			http.Error(w, "forbidden: the token lacks the required role or group", http.StatusForbidden)
		case err != nil:
			w.Header().Set("WWW-Authenticate", `Bearer realm="osac", error="invalid_token"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		default:
			g.allowlist.ServeHTTP(w, r)
		}
		return
	}
	if _, err := r.Cookie(g.cookie); err == nil {
		g.proxy.ServeHTTP(w, r)
		return
	}
	w.Header().Set("WWW-Authenticate", `Bearer realm="osac"`)
	http.Error(w, "authentication required", http.StatusUnauthorized)
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

func (g *Gate) isExempt(path string) bool {
	for _, prefix := range g.exempt {
		if prefix == "" {
			continue
		}
		if strings.HasSuffix(prefix, "/") {
			if strings.HasPrefix(path, prefix) {
				return true
			}
			continue
		}
		if path == prefix {
			return true
		}
	}
	return false
}
