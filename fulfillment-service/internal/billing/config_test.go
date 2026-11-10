/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package billing_test

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

var _ = Describe("LoadConfig", func() {
	const (
		testCredential = "test-credential"
		path           = "/var/run/billing/credential-xyz"
	)

	var env map[string]string
	var files map[string]string

	getenv := func(k string) string { return env[k] }
	readFile := func(p string) ([]byte, error) {
		c, ok := files[p]
		if !ok {
			return nil, fmt.Errorf("open %s: permission denied", p)
		}
		return []byte(c), nil
	}
	reasonOf := func(err error) billing.Reason {
		var e *billing.Error
		ExpectWithOffset(1, errors.As(err, &e)).To(BeTrue())
		return e.Reason
	}

	BeforeEach(func() {
		env = map[string]string{
			"OSAC_BILLING_PROVIDER":                               "m360",
			"OSAC_BILLING_BASE_URL":                               "https://revos.example.com/api/v1/external",
			"OSAC_BILLING_CREDENTIAL_FILE":                        path,
			"OSAC_BILLING_RETRY_COUNT":                            "3",
			"OSAC_BILLING_PRICING_BREAKER_FAILURE_THRESHOLD":      "5",
			"OSAC_BILLING_PRICING_BREAKER_FAILURE_WINDOW_SECONDS": "30",
			"OSAC_BILLING_PRICING_BREAKER_OPEN_SECONDS":           "20",
		}
		files = map[string]string{path: testCredential + "\n"}
	})

	It("loads a complete configuration and trims the trailing newline", func() {
		cfg, err := billing.LoadConfig(getenv, readFile)
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.BaseURL).To(Equal(env["OSAC_BILLING_BASE_URL"]))
		Expect(cfg.Credential).To(Equal(testCredential))
		Expect(cfg.RetryCount).To(Equal(3))
		Expect(cfg.BreakerThreshold).To(Equal(5))
		Expect(cfg.BreakerWindow).To(Equal(30 * time.Second))
		Expect(cfg.BreakerOpen).To(Equal(20 * time.Second))
		Expect(cfg.RootCAs).To(BeNil(), "no CA file means the system roots")
		Expect(cfg.Validate()).To(Succeed())
	})

	It("accepts zero retries", func() {
		env["OSAC_BILLING_RETRY_COUNT"] = "0"
		cfg, err := billing.LoadConfig(getenv, readFile)
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.RetryCount).To(BeZero())
	})

	It("fills in the tuning defaults when the optional settings are unset", func() {
		delete(env, "OSAC_BILLING_RETRY_COUNT")
		delete(env, "OSAC_BILLING_PRICING_BREAKER_FAILURE_THRESHOLD")
		delete(env, "OSAC_BILLING_PRICING_BREAKER_FAILURE_WINDOW_SECONDS")
		delete(env, "OSAC_BILLING_PRICING_BREAKER_OPEN_SECONDS")
		cfg, err := billing.LoadConfig(getenv, readFile)
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.RetryCount).To(Equal(3))
		Expect(cfg.BreakerThreshold).To(Equal(5))
		Expect(cfg.BreakerWindow).To(Equal(30 * time.Second))
		Expect(cfg.BreakerOpen).To(Equal(30 * time.Second))
		Expect(cfg.Validate()).To(Succeed())
	})

	It("means billing is off when the provider is unset", func() {
		delete(env, "OSAC_BILLING_PROVIDER")
		cfg, err := billing.LoadConfig(getenv, readFile)
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg).To(Equal(billing.Config{}))
	})

	DescribeTable("fails with NOT_CONFIGURED when the setup is unusable",
		func(key, value string) {
			if value == "" {
				delete(env, key)
			} else {
				env[key] = value
			}
			_, err := billing.LoadConfig(getenv, readFile)
			Expect(err).To(HaveOccurred())
			Expect(reasonOf(err)).To(Equal(billing.ReasonNotConfigured))
		},
		Entry("unknown provider", "OSAC_BILLING_PROVIDER", "stripe"),
		Entry("missing base URL", "OSAC_BILLING_BASE_URL", ""),
		Entry("missing credential file path", "OSAC_BILLING_CREDENTIAL_FILE", ""),
		Entry("negative retry count", "OSAC_BILLING_RETRY_COUNT", "-1"),
		Entry("non-integer retry count", "OSAC_BILLING_RETRY_COUNT", "1.5"),
		Entry("zero breaker threshold", "OSAC_BILLING_PRICING_BREAKER_FAILURE_THRESHOLD", "0"),
		Entry("negative breaker threshold", "OSAC_BILLING_PRICING_BREAKER_FAILURE_THRESHOLD", "-5"),
		Entry("non-integer breaker threshold", "OSAC_BILLING_PRICING_BREAKER_FAILURE_THRESHOLD", "five"),
		Entry("zero breaker window", "OSAC_BILLING_PRICING_BREAKER_FAILURE_WINDOW_SECONDS", "0"),
		Entry("negative breaker window", "OSAC_BILLING_PRICING_BREAKER_FAILURE_WINDOW_SECONDS", "-30"),
		Entry("non-integer breaker window", "OSAC_BILLING_PRICING_BREAKER_FAILURE_WINDOW_SECONDS", "30s"),
		Entry("zero breaker open time", "OSAC_BILLING_PRICING_BREAKER_OPEN_SECONDS", "0"),
		Entry("negative breaker open time", "OSAC_BILLING_PRICING_BREAKER_OPEN_SECONDS", "-30"),
		Entry("non-integer breaker open time", "OSAC_BILLING_PRICING_BREAKER_OPEN_SECONDS", "0.5"),
		Entry("breaker window overflowing a Duration", "OSAC_BILLING_PRICING_BREAKER_FAILURE_WINDOW_SECONDS",
			fmt.Sprint(maxDurationSeconds+1)),
		Entry("breaker open time overflowing a Duration", "OSAC_BILLING_PRICING_BREAKER_OPEN_SECONDS",
			fmt.Sprint(maxDurationSeconds+1)),
	)

	It("accepts the largest breaker seconds a Duration can hold", func() {
		env["OSAC_BILLING_PRICING_BREAKER_FAILURE_WINDOW_SECONDS"] = fmt.Sprint(maxDurationSeconds)
		env["OSAC_BILLING_PRICING_BREAKER_OPEN_SECONDS"] = fmt.Sprint(maxDurationSeconds)
		cfg, err := billing.LoadConfig(getenv, readFile)
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.BreakerWindow).To(Equal(time.Duration(maxDurationSeconds) * time.Second))
		Expect(cfg.BreakerOpen).To(Equal(time.Duration(maxDurationSeconds) * time.Second))
	})

	DescribeTable("fails with UNAVAILABLE when the credential file cannot be used",
		func(content *string) {
			delete(files, path)
			if content != nil {
				files[path] = *content
			}
			_, err := billing.LoadConfig(getenv, readFile)
			Expect(err).To(HaveOccurred())
			Expect(reasonOf(err)).To(Equal(billing.ReasonUnavailable))
			// The credential is a mounted Secret read once at startup: retrying cannot fix it.
			var be *billing.Error
			Expect(errors.As(err, &be)).To(BeTrue())
			Expect(be.Retryable).To(BeFalse())
		},
		Entry("unreadable", nil),
		Entry("empty", ptr("")),
		Entry("only a newline", ptr("\n")),
	)

	DescribeTable("fails with UNAVAILABLE when the CA file cannot be used",
		func(content *string) {
			const caPath = "/etc/billing/ca.pem"
			env["OSAC_BILLING_CA_FILE"] = caPath
			if content != nil {
				files[caPath] = *content
			}
			_, err := billing.LoadConfig(getenv, readFile)
			Expect(err).To(HaveOccurred())
			Expect(reasonOf(err)).To(Equal(billing.ReasonUnavailable))
		},
		Entry("unreadable", nil),
		Entry("empty", ptr("")),
		Entry("not PEM", ptr("not a certificate")),
		Entry("PEM without a certificate", ptr("-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n")),
	)

	It("never leaks the credential or its path", func() {
		cfg, err := billing.LoadConfig(getenv, readFile)
		Expect(err).ToNot(HaveOccurred())
		var buf strings.Builder
		slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "cfg", cfg)
		out := []string{
			fmt.Sprint(cfg), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg), buf.String(),
		}

		delete(files, path)
		_, err = billing.LoadConfig(getenv, readFile)
		Expect(err).To(HaveOccurred())
		out = append(out, err.Error())
		env["OSAC_BILLING_PROVIDER"] = "stripe"
		_, err = billing.LoadConfig(getenv, readFile)
		out = append(out, err.Error())

		for _, s := range out {
			Expect(s).ToNot(ContainSubstring(testCredential))
			Expect(s).ToNot(ContainSubstring(path))
		}
	})
})

// baseURLWithCredentials is built at runtime so the rejection case exercises
// URL userinfo without storing a credential-bearing literal in the repository.
// The non-secret userinfo values are held in test-named constants.
const (
	testUser     = "test-user"
	testPassword = "test-password"
	testToken    = "tok"
)

var baseURLWithCredentials = url.URL{
	Scheme: "https",
	Host:   "revos.example.com",
	User:   url.UserPassword(testUser, testPassword),
}

var _ = DescribeTable("Config.Validate checks the base URL",
	func(baseURL string, valid bool) {
		cfg := billing.Config{BaseURL: baseURL, Credential: testToken}
		err := cfg.Validate()
		if valid {
			Expect(err).ToNot(HaveOccurred())
			return
		}
		var be *billing.Error
		Expect(errors.As(err, &be)).To(BeTrue())
		Expect(be.Reason).To(Equal(billing.ReasonNotConfigured))
	},
	Entry("https host", "https://revos.example.com", true),
	Entry("https host with port", "https://revos.example.com:8443", true),
	Entry("https IPv6 host", "https://[2001:db8::1]:8443", true),
	Entry("http loopback name", "http://localhost:8080", true),
	Entry("http loopback IPv4", "http://127.0.0.1:8080", true),
	Entry("http loopback IPv6", "http://[::1]:8080", true),
	Entry("http remote host leaks the token", "http://revos.example.com", false),
	Entry("path to the provider API", "https://revos.example.com/api", true),
	Entry("trailing slash", "https://revos.example.com/", true),
	Entry("percent-encoded path", "https://revos.example.com/api%20v1", true),
	Entry("query", "https://revos.example.com?x=1", false),
	Entry("fragment", "https://revos.example.com#f", false),
	Entry("url credentials", baseURLWithCredentials.String(), false),
	Entry("empty port", "https://revos.example.com:", false),
	Entry("uppercase scheme", "HTTPS://revos.example.com", false),
	Entry("no scheme", "revos.example.com", false),
	Entry("other scheme", "ftp://revos.example.com", false),
	Entry("empty host", "https://", false),
	Entry("unparsable", "https://exa mple.com", false),
	Entry("malformed percent escape in the path", "https://revos.example.com/api%zz", false),
	Entry("dangling percent escape", "https://revos.example.com/api%", false),
	Entry("truncated percent escape", "https://revos.example.com/api%2", false),
	Entry("percent-escaped host", "https://%62illing.example.com", false),
	Entry("escaped percent in host", "https://%25billing.example.com", false),
	Entry("malformed percent escape in the host", "https://ex%zzample.com", false),
	Entry("backslash in the host", `https://billing\example.com`, false),
	Entry("pipe in the host", "https://billing|example.com", false),
	Entry("invalid IPv6 host", "https://[::::]", false),
	Entry("control character in the path", "https://revos.example.com/api\x01", false),
	Entry("unclosed bracket in the host", "https://exa[mple.com", false),
)

// maxDurationSeconds is the largest whole number of seconds a time.Duration holds.
const maxDurationSeconds = math.MaxInt64 / int64(time.Second)

// ptr boxes s so DescribeTable entries can pass string pointers.
func ptr(s string) *string { return &s }
