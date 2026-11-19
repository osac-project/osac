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
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

// Stable reasons carried in the ErrorInfo detail of the provider-dependent billing failures.
const (
	// billingNotConfiguredReason reports that the deployment has no billing provider.
	billingNotConfiguredReason = "BILLING_NOT_CONFIGURED"

	// billingProviderUnavailableReason reports that a provider is configured but unusable, for example because its
	// credential Secret is unreadable.
	billingProviderUnavailableReason = "BILLING_PROVIDER_UNAVAILABLE"
)

// requireAdapter is the guard every provider-dependent billing handler calls first. It returns the adapter to use, so
// a handler never holds a nil adapter, and fails with FAILED_PRECONDITION/BILLING_NOT_CONFIGURED (HTTP 400 over REST)
// when billing is off, or UNAVAILABLE/BILLING_PROVIDER_UNAVAILABLE (HTTP 503 over REST) when the adapter could not be
// built. Neither failure names the provider URL, a Secret, or the cause. Only the provider status server reads the
// provider directly, because it reports the two states separately instead of failing.
func requireAdapter(provider *billing.Provider) (billing.Adapter, error) {
	if !provider.IsConfigured() {
		return nil, billingFailure(codes.FailedPrecondition, "billing is not configured",
			billingNotConfiguredReason)
	}
	adapter, err := provider.Adapter()
	if err != nil || adapter == nil {
		return nil, billingFailure(codes.Unavailable, "billing provider is unavailable",
			billingProviderUnavailableReason)
	}
	return adapter, nil
}

// billingFailure builds a status error carrying the stable reason as an ErrorInfo detail, falling back to the plain
// status error when the detail cannot be attached.
func billingFailure(code codes.Code, message, reason string) error {
	result := status.New(code, message)
	if detailed, err := result.WithDetails(&errdetails.ErrorInfo{Reason: reason}); err == nil {
		result = detailed
	}
	return result.Err()
}
