/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package billing_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
)

const testCredentialValue = "t"

var _ = Describe("Error", func() {
	It("renders reason and operation without any provider detail", func() {
		err := &billing.Error{Reason: billing.ReasonUnavailable, Op: "GetAccount", Retryable: true}
		Expect(err.Error()).To(Equal("billing GetAccount: UNAVAILABLE"))
	})

	It("is matchable with errors.As through wrapping", func() {
		wrapped := fmt.Errorf("outer: %w", &billing.Error{Reason: billing.ReasonConflict, Op: "x"})
		var target *billing.Error
		Expect(errors.As(wrapped, &target)).To(BeTrue())
		Expect(target.Reason).To(Equal(billing.ReasonConflict))
	})

	It("keeps the producing HTTP status out of the rendered message", func() {
		err := billing.NewError(billing.ReasonMalformed, "GetAccount", false, 418)
		Expect(err.Error()).To(Equal("billing GetAccount: MALFORMED"))
		Expect(err.HTTPStatus()).To(Equal(418))
	})
})

var _ = Describe("Opaque refs", func() {
	const native = "native-id-12345"

	DescribeTable("never leak the provider id through formatting, logging or JSON",
		func(ref any) {
			var buf strings.Builder
			l := slog.New(slog.NewTextHandler(&buf, nil))
			l.Info("x", "ref", ref)
			marshalled, err := json.Marshal(ref)
			Expect(err).ToNot(HaveOccurred())
			for _, s := range []string{
				fmt.Sprint(ref), fmt.Sprintf("%v", ref), fmt.Sprintf("%+v", ref),
				fmt.Sprintf("%#v", ref), fmt.Sprintf("%s", ref), buf.String(), string(marshalled),
			} {
				Expect(s).ToNot(ContainSubstring(native))
			}
		},
		Entry("account ref", billing.NewAccountRef(native)),
		Entry("service ref", billing.NewServiceRef(native)),
		Entry("config credential", billing.Config{Credential: native}),
		Entry("account correlation value", billing.Account{ExternalID: native}),
	)

	It("round-trips the id for the adapter that issued it", func() {
		Expect(billing.NewAccountRef(native).ID()).To(Equal(native))
		Expect(billing.NewServiceRef(native).ID()).To(Equal(native))
	})

	It("stays redacted through the production logger and the logr bridge", func() {
		var buf strings.Builder
		logger, err := logging.NewLogger().SetWriter(&buf).SetRedact(true).Build()
		Expect(err).ToNot(HaveOccurred())
		logger.Info("x", "cfg", billing.Config{Credential: native})
		// logr never renders values itself: FromSlogHandler forwards them to the
		// slog handler, whose LogValuer resolution does the redaction.
		logr.FromSlogHandler(logger.Handler()).Info("x", "ref", billing.NewAccountRef(native))
		Expect(buf.String()).To(ContainSubstring("[redacted]"))
		Expect(buf.String()).ToNot(ContainSubstring(native))
	})
})

var _ = Describe("Config", func() {
	DescribeTable("validation",
		func(cfg billing.Config, ok bool) {
			err := cfg.ValidateConnectionConfig()
			if ok {
				Expect(err).ToNot(HaveOccurred())
				return
			}
			var e *billing.Error
			Expect(errors.As(err, &e)).To(BeTrue())
			Expect(e.Reason).To(Equal(billing.ReasonNotConfigured))
		},
		Entry("complete", billing.Config{BaseURL: "https://x", Credential: testCredentialValue}, true),
		Entry("missing url", billing.Config{Credential: testCredentialValue}, false),
		Entry("missing credential", billing.Config{BaseURL: "https://x"}, false),
	)
})
