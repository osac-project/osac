/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package billingproviderstatus

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// failingWriter fails every write with a persistent error, to simulate a broken output stream.
type failingWriter struct {
	err error
}

// Write is the implementation of the io.Writer interface.
func (w *failingWriter) Write(p []byte) (int, error) {
	return 0, w.err
}

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
	// run renders the status and returns the standard output; the console also captures standard error so
	// that no test writes to the real stderr.
	run := func(format string, status *publicv1.BillingProviderStatus) string {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		console, err := terminal.NewConsole().SetLogger(logger).SetStdout(stdout).SetStderr(stderr).Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(render(ctx, console, format, status)).To(Succeed())
		Expect(stderr.String()).To(BeEmpty())
		return stdout.String()
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

	// Golden files live in testdata/. Regenerate with UPDATE_GOLDEN=1 and review the diff.
	DescribeTable("matches the golden output",
		func(format string, status func() *publicv1.BillingProviderStatus, name string) {
			output := run(format, status())
			path := filepath.Join("testdata", name)
			if os.Getenv("UPDATE_GOLDEN") != "" {
				Expect(os.WriteFile(path, []byte(output), 0o644)).To(Succeed())
			}
			expected, err := os.ReadFile(path)
			Expect(err).ToNot(HaveOccurred())
			Expect(output).To(Equal(string(expected)))
		},
		Entry("table, available", "table", available, "available.table.golden"),
		Entry("table, not configured", "table", notConfigured, "not_configured.table.golden"),
		Entry("json, available", "json", available, "available.json.golden"),
		Entry("json, not configured", "json", notConfigured, "not_configured.json.golden"),
	)
})

var _ = Describe("render failures", func() {
	sentinel := errors.New("broken stream")

	available := func() *publicv1.BillingProviderStatus {
		return publicv1.BillingProviderStatus_builder{
			State:              publicv1.BillingProviderState_BILLING_PROVIDER_STATE_AVAILABLE,
			UsageDeliveryState: publicv1.BillingUsageDeliveryState_BILLING_USAGE_DELIVERY_STATE_UNKNOWN,
		}.Build()
	}

	renderErr := func(format string, status *publicv1.BillingProviderStatus, stdout io.Writer) error {
		stderr := &bytes.Buffer{}
		console, err := terminal.NewConsole().SetLogger(logger).SetStdout(stdout).SetStderr(stderr).Build()
		Expect(err).ToNot(HaveOccurred())
		return render(ctx, console, format, status)
	}

	It("reports a failed table flush wrapping the writer error", func() {
		err := renderErr("table", available(), &failingWriter{err: sentinel})
		Expect(err).To(MatchError(ContainSubstring("failed to flush billing provider status table")))
		Expect(errors.Is(err, sentinel)).To(BeTrue())
	})

	It("reports a failed JSON write", func() {
		err := renderErr("json", available(), &failingWriter{err: sentinel})
		Expect(err).To(MatchError(ContainSubstring("failed to render billing provider status")))
		Expect(errors.Is(err, sentinel)).To(BeTrue())
	})

	It("reports a failed YAML write", func() {
		err := renderErr("yaml", available(), &failingWriter{err: sentinel})
		Expect(err).To(MatchError(ContainSubstring("failed to render billing provider status")))
		Expect(errors.Is(err, sentinel)).To(BeTrue())
	})
})
