/*
Copyright (c) 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package recovery

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GrpcPanicInterceptorBuilder contains the data and logic needed to build an interceptor that recovers from panics.
// Don't create instances of this type directly, use the NewGrpcPanicInterceptor function instead.
type GrpcPanicInterceptorBuilder struct {
	logger *slog.Logger
}

// PanicInterceptor contains the data needed by the interceptor.
type GrpcPanicInterceptor struct {
	logger *slog.Logger
}

// NewGrpcPanicInterceptor creates a builder that can then be used to configure and create a panic recovery interceptor.
func NewGrpcPanicInterceptor() *GrpcPanicInterceptorBuilder {
	return &GrpcPanicInterceptorBuilder{}
}

// SetLogger sets the logger that will be used to write to the log. This is mandatory.
func (b *GrpcPanicInterceptorBuilder) SetLogger(value *slog.Logger) *GrpcPanicInterceptorBuilder {
	b.logger = value
	return b
}

// Build uses the data stored in the builder to create and configure a new interceptor.
func (b *GrpcPanicInterceptorBuilder) Build() (result *GrpcPanicInterceptor, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	// Create and populate the object:
	result = &GrpcPanicInterceptor{
		logger: b.logger,
	}
	return
}

// UnaryServer is the unary server interceptor function that recovers from panics.
func (i *GrpcPanicInterceptor) UnaryServer(ctx context.Context, request any, info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler) (response any, err error) {
	defer func() {
		fault := recover()
		if fault != nil {
			i.logger.ErrorContext(
				ctx,
				"Panic occurred in gRPC unary handler",
				slog.String("method", info.FullMethod),
				slog.Any("fault", fault),
			)
			err = status.Errorf(codes.Internal, "Internal error")
			response = nil
		}
	}()
	response, err = handler(ctx, request)
	return
}

// StreamServer is the stream server interceptor function that recovers from panics.
func (i *GrpcPanicInterceptor) StreamServer(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo,
	handler grpc.StreamHandler) (err error) {
	defer func() {
		fault := recover()
		if fault != nil {
			i.logger.ErrorContext(
				stream.Context(),
				"Panic occurred in gRPC stream handler",
				slog.String("method", info.FullMethod),
				slog.Any("fault", fault),
			)
			err = status.Errorf(codes.Internal, "Internal error")
		}
	}()
	err = handler(server, stream)
	return
}
