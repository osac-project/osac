/*
Copyright (c) 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package validation

import (
	"context"
	"errors"
	"log/slog"

	"buf.build/go/protovalidate"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// ProtovalidateInterceptorBuilder contains the data and logic needed to build a gRPC interceptor that validates
// incoming requests using protovalidate. Don't create instances of this type directly, use the
// NewProtovalidateInterceptor function instead.
type ProtovalidateInterceptorBuilder struct {
	logger *slog.Logger
}

// ProtovalidateInterceptor is a gRPC interceptor that validates request messages using protovalidate and returns
// InvalidArgument errors with structured field violations.
type ProtovalidateInterceptor struct {
	logger    *slog.Logger
	validator protovalidate.Validator
}

// NewProtovalidateInterceptor creates a builder that can then be used to configure and create a new validation
// interceptor.
func NewProtovalidateInterceptor() *ProtovalidateInterceptorBuilder {
	return &ProtovalidateInterceptorBuilder{}
}

// SetLogger sets the logger that will be used to write to the log. This is mandatory.
func (b *ProtovalidateInterceptorBuilder) SetLogger(value *slog.Logger) *ProtovalidateInterceptorBuilder {
	b.logger = value
	return b
}

// Build uses the data stored in the builder to create and configure a new interceptor.
func (b *ProtovalidateInterceptorBuilder) Build() (result *ProtovalidateInterceptor, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	// Create the protovalidate validator:
	validator, err := protovalidate.New()
	if err != nil {
		return
	}

	// Create the interceptor:
	result = &ProtovalidateInterceptor{
		logger:    b.logger,
		validator: validator,
	}
	return
}

// UnaryServer is the unary server interceptor function.
func (i *ProtovalidateInterceptor) UnaryServer(ctx context.Context, request any, info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler) (response any, err error) {
	// Validate the request message:
	err = i.validate(request)
	if err != nil {
		return
	}
	return handler(ctx, request)
}

// StreamServer is the stream server interceptor function.
func (i *ProtovalidateInterceptor) StreamServer(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo,
	handler grpc.StreamHandler) error {
	// Wrap the stream to validate messages as they're received:
	wrappedStream := &protovalidateStream{
		interceptor: i,
		stream:      stream,
	}
	return handler(server, wrappedStream)
}

// protovalidateStream wraps a gRPC server stream to validate messages on RecvMsg.
type protovalidateStream struct {
	interceptor *ProtovalidateInterceptor
	stream      grpc.ServerStream
}

func (s *protovalidateStream) Context() context.Context {
	return s.stream.Context()
}

func (s *protovalidateStream) RecvMsg(message any) error {
	err := s.stream.RecvMsg(message)
	if err != nil {
		return err
	}
	// Validate the received message:
	return s.interceptor.validate(message)
}

func (s *protovalidateStream) SendHeader(md metadata.MD) error {
	return s.stream.SendHeader(md)
}

func (s *protovalidateStream) SendMsg(message any) error {
	return s.stream.SendMsg(message)
}

func (s *protovalidateStream) SetHeader(md metadata.MD) error {
	return s.stream.SetHeader(md)
}

func (s *protovalidateStream) SetTrailer(md metadata.MD) {
	s.stream.SetTrailer(md)
}

// validate checks if the message satisfies protovalidate constraints.
func (i *ProtovalidateInterceptor) validate(message any) error {
	// Type-assert to proto.Message:
	protoMessage, ok := message.(proto.Message)
	if !ok {
		// Not a protobuf message, skip validation
		return nil
	}

	// Skip validation for Update requests - they are validated in the server after mask merging.
	// This avoids false validation errors when clients send partial objects with update_mask.
	type updateRequest interface {
		GetUpdateMask() any
	}
	if _, isUpdate := message.(updateRequest); isUpdate {
		return nil
	}

	// Validate using protovalidate:
	err := i.validator.Validate(protoMessage)
	if err != nil {
		// Map protovalidate error to gRPC InvalidArgument:
		i.logger.Debug("Request validation failed", "error", err.Error())
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "validation failed: %s", err.Error())
	}

	return nil
}
