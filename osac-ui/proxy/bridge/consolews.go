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

	proxy := httputil.NewSingleHostReverseProxy(target)
	if target.Scheme == "https" {
		proxy.Transport = &http.Transport{
			TLSClientConfig: tlsConfig,
		}
	}

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.URL.Path = consoleConnectPath
		req.URL.RawPath = ""
		req.Host = target.Host
	}

	return proxy, nil
}
