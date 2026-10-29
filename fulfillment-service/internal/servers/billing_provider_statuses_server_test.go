/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// countingAdapter records HealthCheck calls. Every other method panics through the nil embedded interface, which
// proves the status read uses only the bounded health probe.
type countingAdapter struct {
	billing.Adapter
	calls int
	err   error
}

// HealthCheck records the probe and returns the configured error.
func (a *countingAdapter) HealthCheck(context.Context) error {
	a.calls++
	return a.err
}

var _ = Describe("Billing provider statuses server", func() {
	build := func(p *billing.Provider) *BillingProviderStatusesServer {
		private, err := NewPrivateBillingProviderStatusesServer().SetLogger(logger).SetProvider(p).Build()
		Expect(err).ToNot(HaveOccurred())
		server, err := NewBillingProviderStatusesServer().SetLogger(logger).SetDelegate(private).Build()
		Expect(err).ToNot(HaveOccurred())
		return server
	}
	get := func(s *BillingProviderStatusesServer) *publicv1.BillingProviderStatus {
		response, err := s.Get(ctx, &publicv1.BillingProviderStatusesGetRequest{})
		Expect(err).ToNot(HaveOccurred())
		return response.GetObject()
	}

	It("requires a delegate", func() {
		_, err := NewBillingProviderStatusesServer().SetLogger(logger).Build()
		Expect(err).To(MatchError("delegate is mandatory"))
	})

	It("succeeds with NOT_CONFIGURED and never calls the provider when billing is off", func() {
		adapter := &countingAdapter{}
		// A disabled provider never holds an adapter; passing one proves it is not used.
		server := build(billing.NewProvider("", adapter, nil))
		status := get(server)
		Expect(status.GetState()).To(Equal(publicv1.BillingProviderState_BILLING_PROVIDER_STATE_NOT_CONFIGURED))
		Expect(status.GetProviderKey()).To(BeEmpty())
		Expect(status.GetUsageDeliveryState()).To(Equal(
			publicv1.BillingUsageDeliveryState_BILLING_USAGE_DELIVERY_STATE_UNKNOWN))
		Expect(status.HasLastSuccessfulCheck()).To(BeFalse())
		Expect(adapter.calls).To(BeZero())
	})

	It("is AVAILABLE after a successful one-account probe", func() {
		adapter := &countingAdapter{}
		before := time.Now().Add(-time.Second)
		status := get(build(billing.NewProvider("m360", adapter, nil)))
		Expect(status.GetState()).To(Equal(publicv1.BillingProviderState_BILLING_PROVIDER_STATE_AVAILABLE))
		Expect(status.GetProviderKey()).To(Equal("m360"))
		Expect(status.GetMessage()).To(BeEmpty())
		Expect(status.GetSupportedCapabilities()).To(ConsistOf(
			publicv1.BillingProviderCapability_BILLING_PROVIDER_CAPABILITY_PRICING))
		Expect(status.GetUsageDeliveryState()).To(Equal(
			publicv1.BillingUsageDeliveryState_BILLING_USAGE_DELIVERY_STATE_UNKNOWN))
		Expect(status.GetLastSuccessfulCheck().AsTime()).To(BeTemporally(">", before))
		Expect(adapter.calls).To(Equal(1))
	})

	DescribeTable("is UNAVAILABLE with a stable message on a classified failure",
		func(reason billing.Reason, message string) {
			adapter := &countingAdapter{err: billing.NewError(reason, "HealthCheck", false, 500)}
			status := get(build(billing.NewProvider("m360", adapter, nil)))
			Expect(status.GetState()).To(Equal(publicv1.BillingProviderState_BILLING_PROVIDER_STATE_UNAVAILABLE))
			Expect(status.GetMessage()).To(Equal(message))
			Expect(status.HasLastSuccessfulCheck()).To(BeFalse())
			Expect(adapter.calls).To(Equal(1))
		},
		Entry("provider 401", billing.ReasonUnauthorized, "billing provider rejected the configured credential"),
		Entry("timeout", billing.ReasonTimeout, "billing provider account API timed out"),
		Entry("open breaker or 5xx", billing.ReasonUnavailable, "billing provider account API is unavailable"),
		Entry("malformed", billing.ReasonMalformed, "billing provider account API returned an unexpected response"),
	)

	It("is UNAVAILABLE, not NOT_CONFIGURED, when the provider is configured but its adapter could not be built",
		func() {
			cause := &billing.Error{Reason: billing.ReasonUnavailable, Op: "load config"}
			status := get(build(billing.NewProvider("m360", nil, cause)))
			Expect(status.GetState()).To(Equal(publicv1.BillingProviderState_BILLING_PROVIDER_STATE_UNAVAILABLE))
			Expect(status.GetProviderKey()).To(Equal("m360"))
		})

	It("shares the last successful check between the public and private servers", func() {
		provider := billing.NewProvider("m360", &countingAdapter{}, nil)
		private, err := NewPrivateBillingProviderStatusesServer().SetLogger(logger).SetProvider(provider).Build()
		Expect(err).ToNot(HaveOccurred())
		public, err := NewBillingProviderStatusesServer().SetLogger(logger).SetDelegate(private).Build()
		Expect(err).ToNot(HaveOccurred())
		before := time.Now().Add(-time.Second)
		get(public)
		response, err := private.Get(ctx, &privatev1.BillingProviderStatusesGetRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetLastSuccessfulCheck().AsTime()).To(BeTemporally(">", before))
	})

	It("never echoes an unrecognized provider key", func() {
		status := get(build(billing.NewProvider("https://user:pw@evil.example", nil,
			&billing.Error{Reason: billing.ReasonNotConfigured, Op: "load config"})))
		Expect(status.GetState()).To(Equal(publicv1.BillingProviderState_BILLING_PROVIDER_STATE_UNAVAILABLE))
		Expect(status.GetProviderKey()).To(BeEmpty())
	})

	It("keeps the last successful check after a later failure", func() {
		adapter := &countingAdapter{}
		server := build(billing.NewProvider("m360", adapter, nil))
		first := get(server).GetLastSuccessfulCheck()
		adapter.err = billing.NewError(billing.ReasonTimeout, "HealthCheck", true, 0)
		status := get(server)
		Expect(status.GetState()).To(Equal(publicv1.BillingProviderState_BILLING_PROVIDER_STATE_UNAVAILABLE))
		Expect(status.GetLastSuccessfulCheck().AsTime()).To(Equal(first.AsTime()))
	})
})

var _ = Describe("Billing guard", func() {
	It("lets a configured deployment through", func() {
		Expect(requireBilling(billing.NewProvider("m360", nil, nil))).To(Succeed())
	})

	It("lets a configured deployment through even when its Secret is unreadable", func() {
		cause := &billing.Error{Reason: billing.ReasonUnavailable, Op: "load config"}
		Expect(requireBilling(billing.NewProvider("m360", nil, cause))).To(Succeed())
	})

	It("fails with FAILED_PRECONDITION and the stable reason when billing is off", func() {
		err := requireBilling(billing.NewProvider("", nil, nil))
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.FailedPrecondition))
		Expect(st.Details()).To(HaveLen(1))
		Expect(st.Details()[0].(*errdetails.ErrorInfo).GetReason()).To(Equal("BILLING_NOT_CONFIGURED"))
	})

	It("is HTTP 400 over REST with the reason in the details", func() {
		err := requireBilling(billing.NewProvider("", nil, nil))
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/fulfillment/v1/billing/accounts", nil)
		runtime.DefaultHTTPErrorHandler(ctx, runtime.NewServeMux(), &runtime.JSONPb{}, recorder, request, err)
		Expect(recorder.Code).To(Equal(http.StatusBadRequest))
		body := recorder.Body.String()
		Expect(body).To(ContainSubstring(`"reason":"BILLING_NOT_CONFIGURED"`))
		Expect(strings.ToLower(body)).ToNot(ContainSubstring("secret"))
		Expect(strings.ToLower(body)).ToNot(ContainSubstring("http"))
	})
})
