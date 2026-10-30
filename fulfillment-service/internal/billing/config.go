/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package billing

import (
	"net"
	"net/url"
	"strings"
)

// Config is the provider connection settings, built by LoadConfig or supplied directly by the caller. Embedding
// opaque keeps the credential out of logs and formatted output.
type Config struct {
	opaque
	BaseURL    string
	APIVersion string
	Credential string
}

// Validate fails with a NOT_CONFIGURED *Error when the configuration is incomplete or the base URL would send the
// credential in cleartext: it needs a host and the https scheme, except that http is accepted for loopback hosts.
func (c Config) Validate() error {
	if c.APIVersion == "" || c.Credential == "" || !validBaseURL(c.BaseURL) {
		return &Error{Reason: ReasonNotConfigured, Op: "configure"}
	}
	return nil
}

// validBaseURL reports whether raw is an https URL with a host, or an http one
// on a loopback host.
func validBaseURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback()
	}
	return false
}

// LoadConfig builds a Config from the deployment: OSAC_BILLING_PROVIDER, OSAC_BILLING_BASE_URL,
// OSAC_BILLING_API_VERSION and OSAC_BILLING_CREDENTIAL_FILE (a mounted Secret file). An unset provider means
// billing is off and yields a zero Config with no error. Incomplete settings fail with NOT_CONFIGURED and an
// unreadable or empty credential file with UNAVAILABLE; errors never carry the credential, path or URL.
func LoadConfig(getenv func(string) string, readFile func(string) ([]byte, error)) (Config, error) {
	const op = "load config"
	provider := getenv("OSAC_BILLING_PROVIDER")
	if provider == "" {
		return Config{}, nil
	}
	file := getenv("OSAC_BILLING_CREDENTIAL_FILE")
	cfg := Config{BaseURL: getenv("OSAC_BILLING_BASE_URL"), APIVersion: getenv("OSAC_BILLING_API_VERSION")}
	if provider != "m360" || cfg.BaseURL == "" || cfg.APIVersion == "" || file == "" {
		return Config{}, &Error{Reason: ReasonNotConfigured, Op: op}
	}
	raw, err := readFile(file)
	cred := strings.TrimRight(string(raw), "\r\n")
	if err != nil || cred == "" {
		return Config{}, &Error{Reason: ReasonUnavailable, Op: op}
	}
	cfg.Credential = cred
	return cfg, nil
}
