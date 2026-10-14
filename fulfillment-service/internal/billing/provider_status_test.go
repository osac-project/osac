/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package billing

import (
	"context"
	"errors"
	"testing"
)

type statusProvider struct {
	status ProviderStatus
	err    error
}

func (p statusProvider) GetStatus(context.Context) (ProviderStatus, error) { return p.status, p.err }
func (statusProvider) ListAccounts(context.Context) ([]Account, error)     { return nil, nil }
func (statusProvider) GetAccount(context.Context, string) (Account, error) { return Account{}, nil }
func (statusProvider) UpdateAccountExternalID(context.Context, string, string, string) error {
	return nil
}

func TestResolveProviderStatusReportsBillingDisabled(t *testing.T) {
	status := ResolveProviderStatus(context.Background(), "", nil)
	if status.State != ProviderStateNotConfigured {
		t.Fatalf("got provider state %v, want NOT_CONFIGURED", status.State)
	}
}

func TestResolveProviderStatusRedactsProviderErrors(t *testing.T) {
	status := ResolveProviderStatus(context.Background(), "m360", statusProvider{
		status: ProviderStatus{State: ProviderStateUnavailable},
		err:    errors.New("response contained private account data"),
	})
	if status.State != ProviderStateUnavailable {
		t.Fatalf("got provider state %v, want UNAVAILABLE", status.State)
	}
	if status.ProviderKey != "m360" {
		t.Fatalf("got provider key %q, want m360", status.ProviderKey)
	}
	if status.Message == "" || status.Message == "response contained private account data" {
		t.Fatalf("provider error was not safely summarized: %q", status.Message)
	}
}

func TestResolveProviderStatusReturnsHealthyProviderState(t *testing.T) {
	status := ResolveProviderStatus(context.Background(), "m360", statusProvider{
		status: ProviderStatus{
			State:        ProviderStateAvailable,
			Capabilities: []ProviderCapability{ProviderCapabilityPricing},
		},
	})
	if status.State != ProviderStateAvailable {
		t.Fatalf("got provider state %v, want AVAILABLE", status.State)
	}
	if len(status.Capabilities) != 1 || status.Capabilities[0] != ProviderCapabilityPricing {
		t.Fatalf("got provider capabilities %v, want PRICING", status.Capabilities)
	}
}
