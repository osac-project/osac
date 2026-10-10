/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package netboxclient

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/url"
	"os"
	"strings"
)

var (
	errInvalidEndpoint        = errors.New("NetBox endpoint must be an HTTP or HTTPS origin")
	errInsecureHTTPNotAllowed = errors.New("NetBox HTTP endpoints require AllowInsecureHTTP")
	errInvalidToken           = errors.New("NetBox API token file is missing or invalid")
	errInvalidCA              = errors.New("NetBox CA file is missing or invalid")
)

// Config contains the filesystem and endpoint settings for the NetBox API.
type Config struct {
	Endpoint  string
	TokenFile string
	CAFile    string
	// AllowInsecureHTTP allows NetBox connections over unencrypted HTTP.
	AllowInsecureHTTP bool
}

func normalizeEndpoint(endpoint string, allowInsecureHTTP bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil ||
		parsed == nil ||
		parsed.Host == "" ||
		parsed.Hostname() == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.ForceQuery ||
		parsed.Fragment != "" ||
		parsed.RawFragment != "" {
		return "", errInvalidEndpoint
	}

	if err := validateEndpointTransport(parsed, allowInsecureHTTP); err != nil {
		return "", err
	}

	if path := strings.TrimSuffix(parsed.EscapedPath(), "/"); path != "" {
		return "", errInvalidEndpoint
	}

	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Path = ""
	parsed.RawPath = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func validateEndpointTransport(endpoint *url.URL, allowInsecureHTTP bool) error {
	if endpoint == nil {
		return errInvalidEndpoint
	}

	switch strings.ToLower(endpoint.Scheme) {
	case "https":
		return nil
	case "http":
		if allowInsecureHTTP {
			return nil
		}
		return errInsecureHTTPNotAllowed
	default:
		return errInvalidEndpoint
	}
}

func readToken(tokenFile string) (string, error) {
	if tokenFile == "" {
		return "", errInvalidToken
	}

	data, err := os.ReadFile(tokenFile)
	if err != nil {
		return "", errInvalidToken
	}

	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", errInvalidToken
	}
	// This only checks that the file contains one printable ASCII value suitable
	// for an Authorization header; NetBox validates whether the token is authentic.
	for _, character := range token {
		if character < 0x21 || character > 0x7e {
			return "", errInvalidToken
		}
	}
	return token, nil
}

func newTLSConfig(caFile string) (*tls.Config, error) {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		return nil, errors.New("failed to load system TLS roots for NetBox")
	}

	if caFile != "" {
		caPEM, err := os.ReadFile(caFile)
		if err != nil || !roots.AppendCertsFromPEM(caPEM) {
			return nil, errInvalidCA
		}
	}

	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
	}, nil
}
