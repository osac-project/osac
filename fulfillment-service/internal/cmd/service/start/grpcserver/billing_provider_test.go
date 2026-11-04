/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package grpcserver

import (
	"bytes"
	"errors"
	"log/slog"
	"os"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

var _ = Describe("newBillingProvider", func() {
	var env map[string]string
	quiet := slog.New(slog.DiscardHandler)
	getenv := func(k string) string { return env[k] }
	readOK := func(string) ([]byte, error) { return []byte("token\n"), nil }
	readFail := func(string) ([]byte, error) { return nil, os.ErrPermission }
	neverRead := func(string) ([]byte, error) {
		Fail("the credential file must not be read when billing is off")
		return nil, errors.New("unreachable")
	}

	BeforeEach(func() {
		env = map[string]string{
			"OSAC_BILLING_PROVIDER":                               "m360",
			"OSAC_BILLING_BASE_URL":                               "https://revos.example.com/api/v1/external",
			"OSAC_BILLING_CREDENTIAL_FILE":                        "/var/run/billing/credential",
			"OSAC_BILLING_RETRY_COUNT":                            "3",
			"OSAC_BILLING_PRICING_BREAKER_FAILURE_THRESHOLD":      "5",
			"OSAC_BILLING_PRICING_BREAKER_FAILURE_WINDOW_SECONDS": "30",
			"OSAC_BILLING_PRICING_BREAKER_OPEN_SECONDS":           "30",
		}
	})

	It("is disabled, without reading anything, when no provider is set", func() {
		p := newBillingProvider(func(string) string { return "" }, neverRead, quiet)
		Expect(p.IsConfigured()).To(BeFalse())
		adapter, err := p.Adapter()
		Expect(adapter).To(BeNil())
		Expect(err).ToNot(HaveOccurred())
	})

	It("builds the adapter, without calling the provider, when configuration is complete", func() {
		p := newBillingProvider(getenv, readOK, quiet)
		Expect(p.IsConfigured()).To(BeTrue())
		Expect(p.Key()).To(Equal("m360"))
		adapter, err := p.Adapter()
		Expect(err).ToNot(HaveOccurred())
		Expect(adapter).ToNot(BeNil())
	})

	It("logs one info line naming only the provider when the adapter builds", func() {
		var buffer bytes.Buffer
		newBillingProvider(getenv, readOK, slog.New(slog.NewTextHandler(&buffer, nil)))
		Expect(buffer.String()).To(ContainSubstring(`level=INFO msg="Billing provider configured" provider=m360` + "\n"))
	})

	It("stays configured but unusable when the credential file is unreadable", func() {
		p := newBillingProvider(getenv, readFail, quiet)
		Expect(p.IsConfigured()).To(BeTrue())
		_, err := p.Adapter()
		var e *billing.Error
		Expect(errors.As(err, &e)).To(BeTrue())
		Expect(e.Reason).To(Equal(billing.ReasonUnavailable))
	})

	It("never logs an unrecognized provider value", func() {
		var buffer bytes.Buffer
		env["OSAC_BILLING_PROVIDER"] = "https://user:s3cret@evil.example"
		delete(env, "OSAC_BILLING_BASE_URL")
		newBillingProvider(getenv, readOK, slog.New(slog.NewTextHandler(&buffer, nil)))
		Expect(buffer.String()).To(ContainSubstring("unrecognized"))
		Expect(buffer.String()).ToNot(ContainSubstring("s3cret"))
	})

	It("logs the provider key when it is recognized", func() {
		var buffer bytes.Buffer
		delete(env, "OSAC_BILLING_BASE_URL")
		newBillingProvider(getenv, readOK, slog.New(slog.NewTextHandler(&buffer, nil)))
		Expect(buffer.String()).To(ContainSubstring("provider=m360"))
	})

	It("stays configured but unusable when the settings are incomplete", func() {
		delete(env, "OSAC_BILLING_BASE_URL")
		p := newBillingProvider(getenv, readOK, quiet)
		Expect(p.IsConfigured()).To(BeTrue())
		_, err := p.Adapter()
		Expect(err).To(HaveOccurred())
	})

	It("logs the build error and stays configured but unusable when the base URL is insecure", func() {
		var buffer bytes.Buffer
		env["OSAC_BILLING_BASE_URL"] = "http://revos.example.com"
		p := newBillingProvider(getenv, readOK, slog.New(slog.NewTextHandler(&buffer, nil)))
		Expect(p.IsConfigured()).To(BeTrue())
		_, err := p.Adapter()
		Expect(err).To(HaveOccurred())
		Expect(buffer.String()).To(ContainSubstring("Billing provider adapter failed to build"))
		Expect(buffer.String()).ToNot(ContainSubstring("Billing provider configured"))
		Expect(buffer.String()).To(ContainSubstring("provider=m360"))
	})
})
