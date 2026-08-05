/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package network

import (
	"fmt"
	"log/slog"
	"net"
	neturl "net/url"
	"strings"
)

// AddressParserBuilder contains the data and logic needed to build an address parser.
type AddressParserBuilder struct {
	logger *slog.Logger
}

// AddressParser parses server addresses and handles both traditional host:port format and URL format (with http://
// or https:// schemes). It returns the parsed address and whether the connection should use plaintext.
type AddressParser struct {
	logger *slog.Logger
}

// NewAddressParser creates a builder that can be used to configure and create an address parser.
func NewAddressParser() *AddressParserBuilder {
	return &AddressParserBuilder{}
}

// SetLogger sets the logger that the parser will use to write messages to the log. This parameter is mandatory.
func (b *AddressParserBuilder) SetLogger(value *slog.Logger) *AddressParserBuilder {
	b.logger = value
	return b
}

// Build uses the data stored in the builder to create and configure a new address parser.
func (b *AddressParserBuilder) Build() (result *AddressParser, err error) {
	// Check parameters:
	if b.logger == nil {
		err = fmt.Errorf("logger is mandatory")
		return
	}

	// Create and populate the object:
	result = &AddressParser{
		logger: b.logger,
	}

	return
}

// Parse parses the server address and handles both traditional host:port format and URL format (with http://
// or https:// schemes). It returns the parsed address (host:port), a boolean indicating if the connection should
// use plaintext (true means plaintext/http, false means TLS/https), and an error if parsing fails. If the address
// doesn't have a scheme and doesn't have a port, the default port 443 is added and plaintext is disabled (TLS is used).
// Only http:// and https:// schemes are supported; other schemes will result in an error.
func (p *AddressParser) Parse(address string) (parsedAddress string, plaintext bool, err error) {
	// If the address looks like a URL, try to parse it as such:
	if strings.Contains(address, "://") {
		var url *neturl.URL
		url, err = neturl.Parse(address)
		if err != nil {
			return
		}
		return p.parseUrl(url)
	}

	// First try to parse assuming it is in 'host:port' format:
	host, port, err := net.SplitHostPort(address)
	if err == nil {
		return p.parseHostPort(host, port)
	}

	// If all the above attempts fail, try to parse assuming it just an IP address or host name, without a port:
	return p.parseHost(address)
}

func (p *AddressParser) parseHostPort(host, port string) (address string, plaintext bool, err error) {
	address = fmt.Sprintf("%s:%s", host, port)
	plaintext = false
	return
}

func (p *AddressParser) parseUrl(url *neturl.URL) (address string, plaintext bool, err error) {
	switch url.Scheme {
	case "http":
		host := url.Hostname()
		port := url.Port()
		if port == "" {
			port = "80"
		}
		address = fmt.Sprintf("%s:%s", host, port)
		plaintext = true
		return
	case "https":
		host := url.Hostname()
		port := url.Port()
		if port == "" {
			port = "443"
		}
		address = fmt.Sprintf("%s:%s", host, port)
		plaintext = false
	default:
		err = fmt.Errorf(
			"unsupported scheme '%s' in address '%s', only 'http' and 'https' are supported",
			url.Scheme, url.String(),
		)
	}
	return
}

func (p *AddressParser) parseHost(host string) (address string, plaintext bool, err error) {
	address = fmt.Sprintf("%s:443", host)
	plaintext = false
	return
}
