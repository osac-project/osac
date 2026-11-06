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

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

var _ = Describe("Provider", func() {
	It("is not configured without a key", func() {
		p := billing.NewProvider("", nil, nil)
		Expect(p.IsConfigured()).To(BeFalse())
		Expect(p.Key()).To(BeEmpty())
	})

	It("is configured when it has a key and an adapter", func() {
		adapter := &fakeAdapter{}
		p := billing.NewProvider("m360", adapter, nil)
		Expect(p.IsConfigured()).To(BeTrue())
		Expect(p.Key()).To(Equal("m360"))
		got, err := p.Adapter()
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(BeIdenticalTo(adapter))
	})

	It("stays configured, with the error, when the adapter could not be built", func() {
		cause := &billing.Error{Reason: billing.ReasonUnavailable, Op: "load config"}
		p := billing.NewProvider("m360", nil, cause)
		Expect(p.IsConfigured()).To(BeTrue())
		adapter, err := p.Adapter()
		Expect(adapter).To(BeNil())
		var e *billing.Error
		Expect(errors.As(err, &e)).To(BeTrue())
		Expect(e).To(BeIdenticalTo(cause))
		Expect(e.Reason).To(Equal(billing.ReasonUnavailable))
	})

	It("fails with NOT_CONFIGURED, and no adapter, when billing is off", func() {
		p := billing.NewProvider("", nil, nil)
		Expect(p.IsConfigured()).To(BeFalse())
		adapter, err := p.Adapter()
		Expect(adapter).To(BeNil())
		Expect(err).To(HaveOccurred())
		var e *billing.Error
		Expect(errors.As(err, &e)).To(BeTrue())
		Expect(e.Reason).To(Equal(billing.ReasonNotConfigured))
		Expect(e.Retryable).To(BeFalse())
		Expect(e.HTTPStatus()).To(BeZero())
	})

	It("fails with UNAVAILABLE when configured without an adapter or a stored error", func() {
		p := billing.NewProvider("m360", nil, nil)
		Expect(p.IsConfigured()).To(BeTrue())
		adapter, err := p.Adapter()
		Expect(adapter).To(BeNil())
		var e *billing.Error
		Expect(errors.As(err, &e)).To(BeTrue())
		Expect(e.Reason).To(Equal(billing.ReasonUnavailable))
		Expect(e.Retryable).To(BeFalse())
		Expect(e.HTTPStatus()).To(BeZero())
	})
})
