// Package authgate authenticates the admin query Routes. Every request must
// carry a bearer token: invalid is 401, valid without the required role or
// group is 403, otherwise the request is forwarded to the allowlist sidecar.
// There is no browser sign-in on these Routes.
package authgate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/osac-project/osac/osac-observability/internal/queryscope"
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

// Config carries the gate's upstream, authenticator and metric scope.
type Config struct {
	AllowlistURL  string
	Authenticator Authenticator
	// Scope restricts what the request may read. The zero value disables
	// scoping, which is what the persona that may see every metric uses.
	Scope queryscope.Rewriter
}

// Gate routes one authenticated request to the allowlist sidecar.
type Gate struct {
	allowlist *httputil.ReverseProxy
	auth      Authenticator
	scope     queryscope.Rewriter
}

// New builds a Gate. The upstream must be an absolute URL and an
// authenticator is mandatory: a gate that cannot decide is not a gate.
func New(cfg Config) (*Gate, error) {
	if cfg.Authenticator == nil {
		return nil, errors.New("an authenticator is required")
	}
	allowlist, err := newProxy(cfg.AllowlistURL)
	if err != nil {
		return nil, fmt.Errorf("ALLOWLIST_URL: %w", err)
	}
	return &Gate{
		allowlist: allowlist,
		auth:      cfg.Authenticator,
		scope:     cfg.Scope,
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
//   - a bearer token is validated here: 401 invalid, 403 valid without the
//     role or group, otherwise the request goes to the allowlist;
//   - anything else is 401.
func (g *Gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/livez" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		w.WriteHeader(http.StatusOK)
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
			if err := g.applyScope(r); err != nil {
				http.Error(w, "request cannot be scoped: "+err.Error(), http.StatusBadRequest)
				return
			}
			g.allowlist.ServeHTTP(w, r)
		}
		return
	}
	w.Header().Set("WWW-Authenticate", `Bearer realm="osac"`)
	http.Error(w, "authentication required", http.StatusUnauthorized)
}

// applyScope injects the persona's metric scope into the request parameters. A
// request the scoper cannot parse is refused: forwarding it unscoped would leak
// the other visibility class, which is the one thing this gate exists to
// prevent.
func (g *Gate) applyScope(r *http.Request) error {
	if !g.scope.Enabled() {
		return nil
	}
	params := r.URL.Query()
	if err := g.scope.Request(r.URL.Path, params); err != nil {
		return err
	}
	r.URL.RawQuery = params.Encode()
	return nil
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}
