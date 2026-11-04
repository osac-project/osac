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
	"crypto/x509"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config is the provider connection settings, built by LoadConfig or supplied directly by the caller. Embedding
// opaque keeps the credential out of logs and formatted output.
type Config struct {
	opaque
	BaseURL    string
	Credential string
	// RetryCount is how many times a retryable failure is retried after the first attempt.
	RetryCount int
	// The pricing breaker opens for BreakerOpen once BreakerThreshold calls fail within BreakerWindow.
	BreakerThreshold int
	BreakerWindow    time.Duration
	BreakerOpen      time.Duration
	// RootCAs verifies the provider certificate; nil means the system roots.
	RootCAs *x509.CertPool
}

// Validate fails with a NOT_CONFIGURED *Error when the configuration is incomplete or the base URL would send the
// credential in cleartext: it needs a host and the https scheme, except that http is accepted for loopback hosts.
// Like the installer's values schema, the base URL may carry the path to the provider API, but no query, fragment,
// credentials or percent escapes in the host. Well-formed percent escapes are allowed in the path.
func (c Config) Validate() error {
	if c.Credential == "" || !validBaseURL(c.BaseURL) {
		return &Error{Reason: ReasonNotConfigured, Op: "configure"}
	}
	return nil
}

// The two patterns mirror the baseUrl branches of the installer's values schemas; keep them in sync. Neither pattern
// allows percent escapes, brackets, backslashes or pipes in an ordinary host. The schema also validates path escapes;
// net/url does that here and verifies bracketed IPv6 hosts and control characters.
var httpsOrigin = regexp.MustCompile(`^https://(\[[0-9a-fA-F:.]+\]|[^/?#\s@:%\[\]\\|\x00-\x1F\x7F]+)(:[0-9]+)?(/[^?#\s\x00-\x1F\x7F]*)?$`)

var httpLoopbackOrigin = regexp.MustCompile(
	`^http://(localhost|127(\.(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])){3}|\[::1\])(:[0-9]+)?(/[^?#\s\x00-\x1F\x7F]*)?$`)

// validBaseURL reports whether raw is an https URL with a host, or an http one on a loopback host, optionally
// carrying the path to the provider API, and never a query, fragment or credentials. net/url vets it first: the
// patterns cannot see what it rejects — malformed percent escapes, control characters, unclosed brackets — and
// every request built from the base URL goes through the same parser anyway.
func validBaseURL(raw string) bool {
	if _, err := url.Parse(raw); err != nil {
		return false
	}
	return httpsOrigin.MatchString(raw) || httpLoopbackOrigin.MatchString(raw)
}

// LoadConfig builds a Config from the deployment: OSAC_BILLING_PROVIDER, OSAC_BILLING_BASE_URL,
// OSAC_BILLING_CREDENTIAL_FILE (a mounted Secret file), OSAC_BILLING_RETRY_COUNT, the
// OSAC_BILLING_PRICING_BREAKER_* settings and the optional OSAC_BILLING_CA_FILE (PEM roots trusted on top of the
// system ones). An unset provider means billing is off and yields a zero Config with no error. Incomplete or invalid
// settings fail with NOT_CONFIGURED; an unreadable or empty credential file, or a CA file without a certificate,
// fails with UNAVAILABLE. Errors never carry the credential, path or URL.
func LoadConfig(getenv func(string) string, readFile func(string) ([]byte, error)) (Config, error) {
	const op = "load config"
	provider := getenv("OSAC_BILLING_PROVIDER")
	if provider == "" {
		return Config{}, nil
	}
	file := getenv("OSAC_BILLING_CREDENTIAL_FILE")
	cfg := Config{BaseURL: getenv("OSAC_BILLING_BASE_URL")}
	retries, retriesOK := intAtLeast(getenv("OSAC_BILLING_RETRY_COUNT"), 0)
	threshold, thresholdOK := intAtLeast(getenv("OSAC_BILLING_PRICING_BREAKER_FAILURE_THRESHOLD"), 1)
	window, windowOK := intAtLeast(getenv("OSAC_BILLING_PRICING_BREAKER_FAILURE_WINDOW_SECONDS"), 1)
	open, openOK := intAtLeast(getenv("OSAC_BILLING_PRICING_BREAKER_OPEN_SECONDS"), 1)
	// Not a policy limit: more seconds than a Duration holds would wrap negative and silently break the breaker.
	const maxSeconds = math.MaxInt64 / int64(time.Second)
	if provider != KeyM360 || cfg.BaseURL == "" || file == "" ||
		!retriesOK || !thresholdOK || !windowOK || !openOK || int64(window) > maxSeconds || int64(open) > maxSeconds {
		return Config{}, &Error{Reason: ReasonNotConfigured, Op: op}
	}
	cfg.RetryCount, cfg.BreakerThreshold = retries, threshold
	cfg.BreakerWindow, cfg.BreakerOpen = time.Duration(window)*time.Second, time.Duration(open)*time.Second
	raw, err := readFile(file)
	cred := strings.TrimRight(string(raw), "\r\n")
	if err != nil || cred == "" {
		return Config{}, &Error{Reason: ReasonUnavailable, Op: op}
	}
	cfg.Credential = cred
	if caFile := getenv("OSAC_BILLING_CA_FILE"); caFile != "" {
		pem, err := readFile(caFile)
		roots, rootsErr := x509.SystemCertPool()
		if err != nil || rootsErr != nil || !roots.AppendCertsFromPEM(pem) {
			return Config{}, &Error{Reason: ReasonUnavailable, Op: op}
		}
		cfg.RootCAs = roots
	}
	return cfg, nil
}

// intAtLeast parses s as an integer no smaller than floor, reporting false when s is empty or invalid.
func intAtLeast(s string, floor int) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= floor
}
