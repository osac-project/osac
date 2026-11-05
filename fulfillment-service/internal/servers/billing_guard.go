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
	"fmt"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

// billingNotConfiguredReason is the stable reason carried in the ErrorInfo detail of the disabled-mode error.
const billingNotConfiguredReason = "BILLING_NOT_CONFIGURED"

// requireBilling is the guard every provider-dependent billing handler calls first, before any validation that
// could reveal provider state. It fails with FAILED_PRECONDITION/BILLING_NOT_CONFIGURED, HTTP 400 over REST, when
// no provider is configured. The message names neither the provider URL nor a Secret.
func requireBilling(provider *billing.Provider) error {
	if provider.IsConfigured() {
		return nil
	}
	return statusWithReason(codes.FailedPrecondition, billingNotConfiguredReason, "billing is not configured")
}

// statusWithReason builds a gRPC status carrying reason in its ErrorInfo detail - the shape every billing
// failure uses.
func statusWithReason(code codes.Code, reason, format string, args ...any) error {
	result := status.New(code, fmt.Sprintf(format, args...))
	if detailed, err := result.WithDetails(&errdetails.ErrorInfo{Reason: reason}); err == nil {
		result = detailed
	}
	return result.Err()
}

// mapClassifiedBillingError classifies a *billing.Error from provider I/O for an admin-facing billing surface:
// provider unavailability or a provider timeout becomes UNAVAILABLE carrying unavailableReason, and any other
// classified provider failure - a provider that never reports a last page is MALFORMED, not merely down -
// surfaces the provider's own reason as FAILED_PRECONDITION detail. The caller picks unavailableReason: the
// discovery surface reports the stable BILLING_PROVIDER_UNAVAILABLE token so pollers can tell a provider that
// is down from a provider that misbehaves, while the assignment surface reports the provider's own reason
// (e.g. TIMEOUT) so an admin can see which bound the assign hit.
func mapClassifiedBillingError(err *billing.Error, unavailableReason string) error {
	if err.Reason == billing.ReasonUnavailable || err.Reason == billing.ReasonTimeout {
		return statusWithReason(codes.Unavailable, unavailableReason,
			"billing provider is unavailable: %s", err.Reason)
	}
	return failedPreconditionWithReason(string(err.Reason), "billing provider error: %s", err.Reason)
}
