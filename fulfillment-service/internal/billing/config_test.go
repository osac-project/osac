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
	"strings"

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
			"OSAC_BILLING_PROVIDER":        "m360",
			"OSAC_BILLING_BASE_URL":        "https://revos.example.com",
			"OSAC_BILLING_API_VERSION":     "v1",
			"OSAC_BILLING_CREDENTIAL_FILE": path,
		}
		files = map[string]string{path: testCredential + "\n"}
	})

	It("loads a complete configuration and trims the trailing newline", func() {
		cfg, err := billing.LoadConfig(getenv, readFile)
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.BaseURL).To(Equal(env["OSAC_BILLING_BASE_URL"]))
		Expect(cfg.APIVersion).To(Equal("v1"))
		Expect(cfg.Credential).To(Equal(testCredential))
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
		Entry("missing API version", "OSAC_BILLING_API_VERSION", ""),
		Entry("missing credential file path", "OSAC_BILLING_CREDENTIAL_FILE", ""),
	)

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

var _ = DescribeTable("Config.Validate checks the base URL",
	func(baseURL string, valid bool) {
		cfg := billing.Config{BaseURL: baseURL, APIVersion: "v1", Credential: "tok"}
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
	Entry("http loopback name", "http://localhost:8080", true),
	Entry("http loopback IPv4", "http://127.0.0.1:8080", true),
	Entry("http loopback IPv6", "http://[::1]:8080", true),
	Entry("http remote host leaks the token", "http://revos.example.com", false),
	Entry("no scheme", "revos.example.com", false),
	Entry("other scheme", "ftp://revos.example.com", false),
	Entry("empty host", "https://", false),
	Entry("unparsable", "https://exa mple.com", false),
)

// ptr boxes s so DescribeTable entries can pass string pointers.
func ptr(s string) *string { return &s }
