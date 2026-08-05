/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"context"
	"log/slog"

	"github.com/osac-project/fulfillment-service/internal/collections"
)

// GuestTenancyLogicBuilder contains the data and logic needed to create guest tenancy logic.
type GuestTenancyLogicBuilder struct {
	logger *slog.Logger
}

// GuestTenancyLogic is a tenancy logic implementation that assigns the guest tenant to all objects and makes both
// the guest and shared tenants visible. This is useful for scenarios where all users should be treated as guests
// with access to a limited set of shared resources.
type GuestTenancyLogic struct {
	logger *slog.Logger
}

// NewGuestTenancyLogic creates a new builder for guest tenancy logic.
func NewGuestTenancyLogic() *GuestTenancyLogicBuilder {
	return &GuestTenancyLogicBuilder{}
}

// SetLogger sets the logger that will be used by the tenancy logic.
func (b *GuestTenancyLogicBuilder) SetLogger(value *slog.Logger) *GuestTenancyLogicBuilder {
	b.logger = value
	return b
}

// Build creates the guest tenancy logic.
func (b *GuestTenancyLogicBuilder) Build() (result *GuestTenancyLogic, err error) {
	// Create the tenancy logic:
	result = &GuestTenancyLogic{
		logger: b.logger,
	}
	return
}

// DetermineAssignableTenants returns a set containing only the guest tenant, regardless of the user's identity.
func (p *GuestTenancyLogic) DetermineAssignableTenants(_ context.Context) (result collections.Set[string], err error) {
	result = GuestTenants
	return
}

// DetermineDefaultTenant returns the guest tenant, regardless of the user's identity.
func (p *GuestTenancyLogic) DetermineDefaultTenant(_ context.Context) (result string, err error) {
	result = "guest"
	return
}

// DetermineVisibleTenants returns a set containing both the guest and shared tenants, allowing guest users to see
// objects from both tenants.
func (p *GuestTenancyLogic) DetermineVisibleTenants(_ context.Context) (result collections.Set[string], err error) {
	result = GuestTenants.Union(SharedTenants)
	return
}

// GuestTenants is the set of tenants that are assigned to guest users.
var GuestTenants = collections.NewSet("guest")
