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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"
)

var _ = Describe("PanicInterceptor", func() {
	var (
		ctx         context.Context
		interceptor *GrpcPanicInterceptor
	)

	BeforeEach(func() {
		var err error

		// Create a context:
		ctx = context.Background()

		// Create the interceptor:
		interceptor, err = NewGrpcPanicInterceptor().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(interceptor).ToNot(BeNil())
	})

	Describe("Creation", func() {
		It("Should fail if logger is not set", func() {
			interceptor, err := NewGrpcPanicInterceptor().Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger is mandatory"))
			Expect(interceptor).To(BeNil())
		})

		It("Should succeed if logger is set", func() {
			interceptor, err := NewGrpcPanicInterceptor().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(interceptor).ToNot(BeNil())
		})
	})

	Describe("Unary server", func() {
		It("Should pass through normal responses", func() {
			info := &grpc.UnaryServerInfo{
				FullMethod: "/mypackage.MyService/MyMethod",
			}
			handler := func(ctx context.Context, req any) (any, error) {
				return "response", nil
			}
			response, err := interceptor.UnaryServer(ctx, "request", info, handler)
			Expect(err).ToNot(HaveOccurred())
			Expect(response).To(Equal("response"))
		})

		It("Should pass through handler errors", func() {
			info := &grpc.UnaryServerInfo{
				FullMethod: "/mypackage.MyService/MyMethod",
			}
			originalErr := grpcstatus.Error(grpccodes.NotFound, "not found")
			handler := func(ctx context.Context, req any) (any, error) {
				return nil, originalErr
			}
			response, err := interceptor.UnaryServer(ctx, "request", info, handler)
			Expect(err).To(Equal(originalErr))
			Expect(response).To(BeNil())
		})

		It("Should recover from panic and return internal error", func() {
			info := &grpc.UnaryServerInfo{
				FullMethod: "/mypackage.MyService/MyMethod",
			}
			handler := func(ctx context.Context, req any) (any, error) {
				panic("test panic")
			}
			response, err := interceptor.UnaryServer(ctx, "request", info, handler)
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Internal))
			Expect(err.Error()).To(ContainSubstring("Internal error"))
			Expect(response).To(BeNil())
		})
	})

	Describe("Stream server", func() {
		It("Should pass through normal responses", func() {
			info := &grpc.StreamServerInfo{
				FullMethod: "/mypackage.MyService/MyStreamMethod",
			}
			handler := func(srv any, stream grpc.ServerStream) error {
				return nil
			}
			err := interceptor.StreamServer(nil, nil, info, handler)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Should pass through handler errors", func() {
			info := &grpc.StreamServerInfo{
				FullMethod: "/mypackage.MyService/MyStreamMethod",
			}
			originalErr := grpcstatus.Error(grpccodes.NotFound, "not found")
			handler := func(srv any, stream grpc.ServerStream) error {
				return originalErr
			}
			err := interceptor.StreamServer(nil, nil, info, handler)
			Expect(err).To(Equal(originalErr))
		})

		It("Should recover from panic and return internal error", func() {
			info := &grpc.StreamServerInfo{
				FullMethod: "/mypackage.MyService/MyStreamMethod",
			}
			handler := func(srv any, stream grpc.ServerStream) error {
				panic("test panic")
			}
			mockStream := &mockServerStream{
				ctx: ctx,
			}
			err := interceptor.StreamServer(nil, mockStream, info, handler)
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Internal))
			Expect(err.Error()).To(ContainSubstring("Internal error"))
		})
	})
})

// mockServerStream is a minimal implementation of grpc.ServerStream for testing.
type mockServerStream struct {
	ctx context.Context
}

func (m *mockServerStream) Context() context.Context {
	return m.ctx
}

func (m *mockServerStream) SendMsg(msg any) error {
	return nil
}

func (m *mockServerStream) RecvMsg(msg any) error {
	return nil
}

func (m *mockServerStream) SetHeader(metadata.MD) error {
	return nil
}

func (m *mockServerStream) SendHeader(metadata.MD) error {
	return nil
}

func (m *mockServerStream) SetTrailer(metadata.MD) {
}
