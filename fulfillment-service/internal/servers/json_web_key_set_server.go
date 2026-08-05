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
	"encoding/json"
	"errors"
	"log/slog"

	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth/jwe"
)

// JsonWebKeySetServerBuilder contains the data and logic needed to create a JSON Web Key Set server. Don't create
// instances of this type directly, use the NewJsonWebKeySetServer function instead.
type JsonWebKeySetServerBuilder struct {
	logger *slog.Logger
	sealer *jwe.Sealer
}

// jsonWebKeySetServer implements the JsonWebKeySet gRPC service.
type jsonWebKeySetServer struct {
	publicv1.UnimplementedJsonWebKeySetServer
	logger *slog.Logger
	sealer *jwe.Sealer
}

// NewJsonWebKeySetServer creates a builder that can then be used to configure and create a new JSON Web Key Set server.
func NewJsonWebKeySetServer() *JsonWebKeySetServerBuilder {
	return &JsonWebKeySetServerBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *JsonWebKeySetServerBuilder) SetLogger(value *slog.Logger) *JsonWebKeySetServerBuilder {
	b.logger = value
	return b
}

// SetSealer sets the token sealer used to provide the JWKS. This is mandatory.
func (b *JsonWebKeySetServerBuilder) SetSealer(value *jwe.Sealer) *JsonWebKeySetServerBuilder {
	b.sealer = value
	return b
}

// Build uses the data stored in the builder to create and configure a new JSON Web Key Set server.
func (b *JsonWebKeySetServerBuilder) Build() (publicv1.JsonWebKeySetServer, error) {
	// Check parameters:
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.sealer == nil {
		return nil, errors.New("sealer is mandatory")
	}

	// Create and populate the object:
	return &jsonWebKeySetServer{
		logger: b.logger,
		sealer: b.sealer,
	}, nil
}

// Get returns the JSON Web Key Set containing the public signing keys
// used to verify tokens issued by this server.
func (s *jsonWebKeySetServer) Get(
	ctx context.Context,
	req *publicv1.JsonWebKeySetGetRequest,
) (*httpbody.HttpBody, error) {
	set, err := s.sealer.JWKSet(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to build JWKS", slog.String("error", err.Error()))
		return nil, status.Errorf(codes.Internal, "failed to build JWKS")
	}
	data, err := json.Marshal(set)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to marshal JWKS", slog.String("error", err.Error()))
		return nil, status.Errorf(codes.Internal, "failed to encode JWKS")
	}
	return &httpbody.HttpBody{
		ContentType: "application/json",
		Data:        data,
	}, nil
}
