/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package billingproviderstatus

import (
	"bytes"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/fulfillment-service/internal/testing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("render", func() {
	available := func() *publicv1.BillingProviderStatus {
		return publicv1.BillingProviderStatus_builder{
			State:       publicv1.BillingProviderState_BILLING_PROVIDER_STATE_AVAILABLE,
			ProviderKey: "m360",
			SupportedCapabilities: []publicv1.BillingProviderCapability{
				publicv1.BillingProviderCapability_BILLING_PROVIDER_CAPABILITY_PRICING,
			},
			LastSuccessfulCheck: timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
			UsageDeliveryState:  publicv1.BillingUsageDeliveryState_BILLING_USAGE_DELIVERY_STATE_UNKNOWN,
		}.Build()
	}
	notConfigured := func() *publicv1.BillingProviderStatus {
		return publicv1.BillingProviderStatus_builder{
			State:              publicv1.BillingProviderState_BILLING_PROVIDER_STATE_NOT_CONFIGURED,
			UsageDeliveryState: publicv1.BillingUsageDeliveryState_BILLING_USAGE_DELIVERY_STATE_UNKNOWN,
		}.Build()
	}
	run := func(format string, status *publicv1.BillingProviderStatus) string {
		buffer := &bytes.Buffer{}
		console, err := terminal.NewConsole().SetLogger(logger).SetStdout(buffer).Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(render(ctx, console, format, status)).To(Succeed())
		return buffer.String()
	}

	It("renders a readable table without enum prefixes", func() {
		output := run("table", available())
		Expect(output).To(MatchRegexp(`State:\s+AVAILABLE`))
		Expect(output).To(MatchRegexp(`Provider:\s+m360`))
		Expect(output).To(MatchRegexp(`Capabilities:\s+PRICING`))
		Expect(output).To(MatchRegexp(`Usage delivery:\s+UNKNOWN`))
		Expect(output).ToNot(ContainSubstring("BILLING_"))
	})

	It("shows dashes for what a disabled deployment does not have", func() {
		output := run("table", notConfigured())
		Expect(output).To(MatchRegexp(`State:\s+NOT_CONFIGURED`))
		Expect(output).To(MatchRegexp(`Provider:\s+-`))
		Expect(output).To(MatchRegexp(`Last successful check:\s+-`))
	})

	It("renders JSON with the API field names and omits an absent last check", func() {
		output := run("json", notConfigured())
		Expect(output).To(MatchJSON(`{
			"state": "BILLING_PROVIDER_STATE_NOT_CONFIGURED",
			"usage_delivery_state": "BILLING_USAGE_DELIVERY_STATE_UNKNOWN"
		}`))
	})

	It("renders YAML with the API field names", func() {
		output := run("yaml", available())
		Expect(output).To(ContainSubstring("provider_key: m360"))
		Expect(output).To(ContainSubstring("last_successful_check:"))
	})

	DescribeTable("renders the expected output",
		func(format string, status func() *publicv1.BillingProviderStatus, assert func(string)) {
			assert(run(format, status()))
		},
		Entry("table, available", "table", available, func(output string) {
			Expect(testing.TableRows(output)).To(Equal([][]string{
				{"State:", "AVAILABLE"},
				{"Provider:", "m360"},
				{"Message:", "-"},
				{"Capabilities:", "PRICING"},
				{"Last successful check:", "2026-01-02T03:04:05Z"},
				{"Usage delivery:", "UNKNOWN"},
			}))
		}),
		Entry("table, not configured", "table", notConfigured, func(output string) {
			Expect(testing.TableRows(output)).To(Equal([][]string{
				{"State:", "NOT_CONFIGURED"},
				{"Provider:", "-"},
				{"Message:", "-"},
				{"Capabilities:", "-"},
				{"Last successful check:", "-"},
				{"Usage delivery:", "UNKNOWN"},
			}))
		}),
		Entry("json, available", "json", available, func(output string) {
			Expect(output).To(MatchJSON(`{
				"last_successful_check": "2026-01-02T03:04:05Z",
				"provider_key": "m360",
				"state": "BILLING_PROVIDER_STATE_AVAILABLE",
				"supported_capabilities": ["BILLING_PROVIDER_CAPABILITY_PRICING"],
				"usage_delivery_state": "BILLING_USAGE_DELIVERY_STATE_UNKNOWN"
			}`))
		}),
		Entry("json, not configured", "json", notConfigured, func(output string) {
			Expect(output).To(MatchJSON(`{
				"state": "BILLING_PROVIDER_STATE_NOT_CONFIGURED",
				"usage_delivery_state": "BILLING_USAGE_DELIVERY_STATE_UNKNOWN"
			}`))
		}),
	)

	It("returns an error when writing the table fails immediately", func() {
		err := errors.New("write failed")
		console, buildErr := terminal.NewConsole().SetLogger(logger).SetStdout(failingWriter{err: err}).Build()
		Expect(buildErr).ToNot(HaveOccurred())
		Expect(render(ctx, console, "table", available())).To(MatchError(err))
	})

	It("returns an error when writing the table fails after some output is buffered", func() {
		err := errors.New("write failed")
		writer := &delayedFailingWriter{err: err, limit: 16}
		console, buildErr := terminal.NewConsole().SetLogger(logger).SetStdout(writer).Build()
		Expect(buildErr).ToNot(HaveOccurred())
		Expect(render(ctx, console, "table", available())).To(MatchError(err))
	})
})

// failingWriter fails every write.
type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

// delayedFailingWriter accepts a limited number of bytes before failing every write, so part of the output is
// buffered before the failure surfaces while flushing the table.
type delayedFailingWriter struct {
	err   error
	limit int
}

func (w *delayedFailingWriter) Write(p []byte) (int, error) {
	if len(p) > w.limit {
		return 0, w.err
	}
	w.limit -= len(p)
	return len(p), nil
}
