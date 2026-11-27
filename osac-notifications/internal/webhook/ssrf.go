/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package webhook

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ValidateURL enforces the design SSRF policy before each webhook POST.
func ValidateURL(raw string) error {
	_, err := publicIPsForURL(raw)
	return err
}

// NewHTTPClient returns a client that does not follow redirects, ignores HTTP
// proxies, and dials only IPs that pass the SSRF policy (re-checked at dial time
// to mitigate DNS rebinding).
func NewHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	dialer := &net.Dialer{Timeout: timeout}
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy:                 func(*http.Request) (*url.URL, error) { return nil, nil },
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialPublic(ctx, dialer, network, addr)
			},
		},
	}
}

func dialPublic(ctx context.Context, dialer *net.Dialer, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("webhook dial address: %w", err)
	}
	ips, err := lookupPublicIPs(host)
	if err != nil {
		return nil, err
	}
	var firstErr error
	for _, ip := range ips {
		// Re-validate immediately before connect (DNS rebinding defense).
		if isBlockedIP(ip) {
			continue
		}
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		if firstErr == nil {
			firstErr = dialErr
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, fmt.Errorf("webhook_url host has no public address to dial")
}

func publicIPsForURL(raw string) ([]net.IP, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("webhook_url is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid webhook_url: %w", err)
	}
	if parsed.Scheme != "https" {
		return nil, fmt.Errorf("webhook_url must use https")
	}
	if parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("webhook_url host is invalid")
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, fmt.Errorf("webhook_url host is required")
	}
	return lookupPublicIPs(host)
}

func lookupPublicIPs(host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return nil, fmt.Errorf("webhook_url host is not a public address")
		}
		return []net.IP{ip}, nil
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("webhook_url host lookup failed: %w", err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("webhook_url host did not resolve")
	}
	out := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		if isBlockedIP(addr) {
			return nil, fmt.Errorf("webhook_url host resolves to a non-public address")
		}
		out = append(out, addr)
	}
	return out, nil
}

func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsPrivate() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return true
		}
		if v4[0] == 169 && v4[1] == 254 && v4[2] == 169 && v4[3] == 254 {
			return true
		}
	}
	return false
}
