package bridge

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
)

const consoleConnectPath = "/api/fulfillment/v1/console_sessions/connect"

// NewConsoleWebSocketProxy forwards browser WebSocket upgrade requests to the
// fulfillment ingress console-proxy route. The Connect JSON transcoder cannot
// handle WebSocket upgrades, so this handler must be registered on the exact
// connect path before the /api/fulfillment/* catch-all.
func NewConsoleWebSocketProxy(upstreamBaseURL string, tlsConfig *tls.Config) (http.Handler, error) {
	target, err := url.Parse(upstreamBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream URL: %w", err)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, fmt.Errorf("unsupported upstream scheme %q", target.Scheme)
	}
	if target.Host == "" {
		return nil, fmt.Errorf("upstream URL must include a host")
	}

	// Rewrite, not Director: Director is deprecated as of Go 1.26. The reverse
	// proxy sanitizes the outbound request before calling Rewrite -- hop-by-hop
	// headers are removed and the upgrade headers restored for WebSocket
	// handshakes, and inbound Forwarded and X-Forwarded-* headers are deleted
	// -- whereas a Director runs before that cleanup, so a client-supplied
	// Connection header can strip headers the Director sets, and inbound
	// forwarding headers reach the upstream verbatim. Rewrite receives the
	// sanitized request, which keeps the console upgrade path intact and sends
	// no client-controlled forwarding headers upstream. SetURL clears Out.Host
	// so the transport uses the target URL's host, matching the previous
	// explicit req.Host assignment.
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.URL.Path = consoleConnectPath
			r.Out.URL.RawPath = ""
		},
	}
	if target.Scheme == "https" {
		proxy.Transport = &http.Transport{
			TLSClientConfig: tlsConfig,
		}
	}

	return proxy, nil
}
