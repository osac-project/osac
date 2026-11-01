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

// billingNotConfiguredReason is the stable reason carried in the ErrorInfo detail of the disabled-mode error.
const billingNotConfiguredReason = "BILLING_NOT_CONFIGURED"

// requireBilling is the guard every provider-dependent billing handler calls first, before any validation that
// could reveal provider state. It fails with FAILED_PRECONDITION/BILLING_NOT_CONFIGURED, HTTP 400 over REST, when
// no provider is configured. The message names neither the provider URL nor a Secret.
func requireBilling(provider *billing.Provider) error {
	if provider.IsConfigured() {
		return nil
	}
	result := status.New(codes.FailedPrecondition, "billing is not configured")
	if detailed, err := result.WithDetails(&errdetails.ErrorInfo{Reason: billingNotConfiguredReason}); err == nil {
		result = detailed
	}
	return result.Err()
}
