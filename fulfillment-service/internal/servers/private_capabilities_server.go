/*
Copyright (c) 2025 Red Hat Inc.

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
	"slices"

	"github.com/osac-project/osac/fulfillment-service/internal/services"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// PrivateCapabilitiesServerBuilder contains the data and logic needed to create a new private capabilities server.
type PrivateCapabilitiesServerBuilder struct {
	logger                   *slog.Logger
	authnTrustedTokenIssuers []string
	serviceFlags             *services.Flags
}

var _ privatev1.CapabilitiesServer = (*PrivateCapabilitiesServer)(nil)

// PrivateCapabilitiesServer is the private server for capabilities, like the list of trusted access token issuers.
type PrivateCapabilitiesServer struct {
	privatev1.UnimplementedCapabilitiesServer

	logger                   *slog.Logger
	authnTrustedTokenIssuers []string
	serviceFlags             *services.Flags
}

// NewPrivateCapabilitiesServer creates a builder that can then be used to configure and create a new private
// capabilities server.
func NewPrivateCapabilitiesServer() *PrivateCapabilitiesServerBuilder {
	return &PrivateCapabilitiesServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *PrivateCapabilitiesServerBuilder) SetLogger(value *slog.Logger) *PrivateCapabilitiesServerBuilder {
	b.logger = value
	return b
}

// SetServiceFlags sets the services advertised by the server.
func (b *PrivateCapabilitiesServerBuilder) SetServiceFlags(value *services.Flags) *PrivateCapabilitiesServerBuilder {
	b.serviceFlags = value
	return b
}

// AddAuthnTrustedTokenIssuers adds a list of token issuers whose tokens are accepted by the server for
// authentication.
func (b *PrivateCapabilitiesServerBuilder) AddAuthnTrustedTokenIssuers(
	value ...string) *PrivateCapabilitiesServerBuilder {
	b.authnTrustedTokenIssuers = append(b.authnTrustedTokenIssuers, value...)
	return b
}

// Build uses the data stored in the builder to create a new private capabilities server.
func (b *PrivateCapabilitiesServerBuilder) Build() (result *PrivateCapabilitiesServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	authnTrustedTokenIssuers := slices.Clone(b.authnTrustedTokenIssuers)
	slices.Sort(authnTrustedTokenIssuers)
	authnTrustedTokenIssuers = slices.Compact(authnTrustedTokenIssuers)

	result = &PrivateCapabilitiesServer{
		logger:                   b.logger,
		authnTrustedTokenIssuers: authnTrustedTokenIssuers,
		serviceFlags:             b.serviceFlags,
	}
	return
}

// Get is the implementation of the method that returns the capabilities of the server.
func (s *PrivateCapabilitiesServer) Get(ctx context.Context,
	request *privatev1.CapabilitiesGetRequest) (response *privatev1.CapabilitiesGetResponse, err error) {
	var enabledServices []string
	if s.serviceFlags != nil {
		enabledServices = s.serviceFlags.EnabledServices()
	}
	response = privatev1.CapabilitiesGetResponse_builder{
		Authn: &privatev1.AuthnCapabilities{
			TrustedTokenIssuers: s.authnTrustedTokenIssuers,
		},
		EnabledServices: enabledServices,
	}.Build()
	return response, nil
}
