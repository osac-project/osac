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
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// CapabilitiesServerBuilder contains the data and logic needed to create a new capabilities server.
type CapabilitiesServerBuilder struct {
	logger                   *slog.Logger
	authnTrustedTokenIssuers []string
	serviceFlags             *services.Flags
}

// Make sure that we implement the interface:
var _ publicv1.CapabilitiesServer = (*CapabilitiesServer)(nil)

// CapabilitiesServer is the server for capabilities, like the list of trusted access token issuers.
type CapabilitiesServer struct {
	publicv1.UnimplementedCapabilitiesServer

	logger                   *slog.Logger
	authnTrustedTokenIssuers []string
	serviceFlags             *services.Flags
}

// NewCapabilitiesServer creates a builder that can the be used to configure and create a new capabilities server.
func NewCapabilitiesServer() *CapabilitiesServerBuilder {
	return &CapabilitiesServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *CapabilitiesServerBuilder) SetLogger(value *slog.Logger) *CapabilitiesServerBuilder {
	b.logger = value
	return b
}

// SetServiceFlags sets the services advertised by the server.
func (b *CapabilitiesServerBuilder) SetServiceFlags(value *services.Flags) *CapabilitiesServerBuilder {
	b.serviceFlags = value
	return b
}

// AddAutnTrustedTokenIssuers adds a list of token issuers whose tokens are accepted by the server for authentication.
func (b *CapabilitiesServerBuilder) AddAutnTrustedTokenIssuers(value ...string) *CapabilitiesServerBuilder {
	b.authnTrustedTokenIssuers = append(b.authnTrustedTokenIssuers, value...)
	return b
}

// Build uses the data stored in the builder to create a new metadata server.
func (b *CapabilitiesServerBuilder) Build() (result *CapabilitiesServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	// Make sure that the list of issuers doens't have duplicates, and that it is sorted in a predictable way:
	authnTrustedTokenIssuers := slices.Clone(b.authnTrustedTokenIssuers)
	slices.Sort(authnTrustedTokenIssuers)
	authnTrustedTokenIssuers = slices.Compact(authnTrustedTokenIssuers)

	// Create and populate the object:
	result = &CapabilitiesServer{
		logger:                   b.logger,
		authnTrustedTokenIssuers: authnTrustedTokenIssuers,
		serviceFlags:             b.serviceFlags,
	}
	return
}

// Get is the implementation of the method that returns the capabilities of the server.
func (s *CapabilitiesServer) Get(ctx context.Context,
	request *publicv1.CapabilitiesGetRequest) (response *publicv1.CapabilitiesGetResponse, err error) {
	var enabledServices []string
	if s.serviceFlags != nil {
		enabledServices = s.serviceFlags.EnabledServices()
	}
	response = publicv1.CapabilitiesGetResponse_builder{
		Authn: &publicv1.AuthnCapabilities{
			TrustedTokenIssuers: s.authnTrustedTokenIssuers,
		},
		EnabledServices: enabledServices,
	}.Build()
	return response, nil
}
