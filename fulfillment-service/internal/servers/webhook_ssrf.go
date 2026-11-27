/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// validateWebhookURL enforces the design SSRF policy for subscription webhook URLs:
// HTTPS only, no userinfo, and the hostname must not resolve only to blocked ranges
// at create time. Delivery-time re-validation belongs in the send worker.
func validateWebhookURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("webhook_url is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid webhook_url: %w", err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("webhook_url must use https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("webhook_url host is required")
	}
	if parsed.User != nil {
		return fmt.Errorf("webhook_url must not contain userinfo")
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("webhook_url host is required")
	}
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedWebhookIP(ip) {
			return fmt.Errorf("webhook_url host is not a public address")
		}
		return nil
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("webhook_url host lookup failed: %w", err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("webhook_url host did not resolve")
	}
	for _, addr := range addrs {
		if isBlockedWebhookIP(addr) {
			return fmt.Errorf("webhook_url host resolves to a non-public address")
		}
	}
	return nil
}

func isBlockedWebhookIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsPrivate() {
		return true
	}
	// CGNAT 100.64.0.0/10
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return true
		}
		// Cloud metadata IPv4
		if v4[0] == 169 && v4[1] == 254 && v4[2] == 169 && v4[3] == 254 {
			return true
		}
	}
	return false
}
