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
	"errors"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// PrivateBillingProviderStatusesServerBuilder contains the data and logic needed to create a new private billing
// provider statuses server.
type PrivateBillingProviderStatusesServerBuilder struct {
	logger   *slog.Logger
	provider *billing.Provider
}

var _ privatev1.BillingProviderStatusesServer = (*PrivateBillingProviderStatusesServer)(nil)

// PrivateBillingProviderStatusesServer reports whether billing is configured and whether the provider account API
// answers.
type PrivateBillingProviderStatusesServer struct {
	privatev1.UnimplementedBillingProviderStatusesServer
	logger   *slog.Logger
	provider *billing.Provider

	// lastCheck is the time of the last successful probe, kept per process; zero until one succeeds.
	mu        sync.Mutex
	lastCheck time.Time
}

// NewPrivateBillingProviderStatusesServer starts building the private statuses server.
func NewPrivateBillingProviderStatusesServer() *PrivateBillingProviderStatusesServerBuilder {
	return &PrivateBillingProviderStatusesServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *PrivateBillingProviderStatusesServerBuilder) SetLogger(
	value *slog.Logger) *PrivateBillingProviderStatusesServerBuilder {
	b.logger = value
	return b
}

// SetProvider sets the billing provider. This is mandatory.
func (b *PrivateBillingProviderStatusesServerBuilder) SetProvider(
	value *billing.Provider) *PrivateBillingProviderStatusesServerBuilder {
	b.provider = value
	return b
}

// Build fails when the logger or provider is missing.
func (b *PrivateBillingProviderStatusesServerBuilder) Build() (result *PrivateBillingProviderStatusesServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.provider == nil {
		err = errors.New("provider is mandatory")
		return
	}
	result = &PrivateBillingProviderStatusesServer{logger: b.logger, provider: b.provider}
	return
}

// Get never calls the provider when billing is off. Otherwise it makes one HealthCheck, a single smallest-page
// account-list request with a short timeout. Account and health reads bypass the pricing breaker by design (6878
// design section 4.6), so an open pricing breaker does not turn the status UNAVAILABLE.
func (s *PrivateBillingProviderStatusesServer) Get(ctx context.Context,
	request *privatev1.BillingProviderStatusesGetRequest) (*privatev1.BillingProviderStatusesGetResponse, error) {
	status := privatev1.BillingProviderStatus_builder{
		UsageDeliveryState: privatev1.BillingUsageDeliveryState_BILLING_USAGE_DELIVERY_STATE_UNKNOWN,
	}.Build()
	if !s.provider.IsConfigured() {
		status.SetState(privatev1.BillingProviderState_BILLING_PROVIDER_STATE_NOT_CONFIGURED)
		return privatev1.BillingProviderStatusesGetResponse_builder{Object: status}.Build(), nil
	}
	// The key is raw deployment config; echo it only when it is the known adapter key.
	if s.provider.Key() == billing.KeyM360 {
		status.SetProviderKey(billing.KeyM360)
	}
	status.SetSupportedCapabilities([]privatev1.BillingProviderCapability{
		privatev1.BillingProviderCapability_BILLING_PROVIDER_CAPABILITY_PRICING,
	})
	adapter, err := s.provider.Adapter()
	if err == nil {
		err = adapter.HealthCheck(ctx)
	}
	s.mu.Lock()
	if err == nil {
		s.lastCheck = time.Now()
	}
	lastCheck := s.lastCheck
	s.mu.Unlock()
	if !lastCheck.IsZero() {
		status.SetLastSuccessfulCheck(timestamppb.New(lastCheck))
	}
	if err != nil {
		reason := probeReason(err)
		s.logger.WarnContext(ctx, "Billing provider probe failed", slog.String("reason", string(reason)))
		status.SetState(privatev1.BillingProviderState_BILLING_PROVIDER_STATE_UNAVAILABLE)
		status.SetMessage(probeMessage(reason))
	} else {
		status.SetState(privatev1.BillingProviderState_BILLING_PROVIDER_STATE_AVAILABLE)
	}
	return privatev1.BillingProviderStatusesGetResponse_builder{Object: status}.Build(), nil
}

// probeReason extracts the Reason of a *billing.Error, defaulting to UNAVAILABLE.
func probeReason(err error) billing.Reason {
	var e *billing.Error
	if errors.As(err, &e) {
		return e.Reason
	}
	return billing.ReasonUnavailable
}

// probeMessage maps a failure reason to stable administrator-facing text; it never repeats provider output.
func probeMessage(reason billing.Reason) string {
	switch reason {
	case billing.ReasonNotConfigured:
		return "billing provider configuration is incomplete"
	case billing.ReasonTimeout:
		return "billing provider account API timed out"
	case billing.ReasonUnauthorized:
		return "billing provider rejected the configured credential"
	case billing.ReasonMalformed:
		return "billing provider account API returned an unexpected response"
	default:
		return "billing provider account API is unavailable"
	}
}
