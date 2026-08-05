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
	"errors"
	"log/slog"

	"google.golang.org/grpc/credentials"
)

// TokenCredentialsBuilder contains the logic needed to create credentials that implement the gRPC PerRPCCredentials
// interface by delegating to our internal TokenSource interface.
type TokenCredentialsBuilder struct {
	logger *slog.Logger
	source TokenSource
}

// tokenCredentials implements the gRPC PerRPCCredentials interface by delegating to our internal TokenSource interface.
type tokenCredentials struct {
	logger *slog.Logger
	source TokenSource
}

// NewTokenCredentials creates a builder that can then be used to configure and create token credentials that
// implement the gRPC PerRPCCredentials interface.
func NewTokenCredentials() *TokenCredentialsBuilder {
	return &TokenCredentialsBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *TokenCredentialsBuilder) SetLogger(value *slog.Logger) *TokenCredentialsBuilder {
	b.logger = value
	return b
}

// SetSource sets the internal token source to delegate to. This is mandatory.
func (b *TokenCredentialsBuilder) SetSource(value TokenSource) *TokenCredentialsBuilder {
	b.source = value
	return b
}

// Build uses the data stored in the builder to build new token credentials.
func (b *TokenCredentialsBuilder) Build() (result credentials.PerRPCCredentials, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.source == nil {
		err = errors.New("source is mandatory")
		return
	}

	// Create and populate the object:
	result = &tokenCredentials{
		logger: b.logger,
		source: b.source,
	}
	return
}

// GetRequestMetadata is the implementation of the gRPC PerRPCCredentials interface. It retrieves the current request
// metadata, refreshing tokens if necessary. It delegates to our internal TokenSource interface.
func (c *tokenCredentials) GetRequestMetadata(ctx context.Context, uri ...string) (result map[string]string,
	err error) {
	token, err := c.source.Token(ctx)
	if err != nil {
		return
	}
	result = map[string]string{
		"Authorization": "Bearer " + token.Access,
	}
	return
}

// RequireTransportSecurity indicates whether the credentials require transport security. This returns true to ensure
// that tokens are only sent over secure connections.
func (c *tokenCredentials) RequireTransportSecurity() bool {
	return true
}
