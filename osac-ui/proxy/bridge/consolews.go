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

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = consoleConnectPath
			pr.Out.URL.RawPath = ""
			pr.Out.Host = target.Host
			pr.Out.Header["X-Forwarded-For"] = pr.In.Header["X-Forwarded-For"]
			pr.SetXForwarded()
		},
	}
	if target.Scheme == "https" {
		proxy.Transport = &http.Transport{
			TLSClientConfig: tlsConfig,
		}
	}

	return proxy, nil
}
