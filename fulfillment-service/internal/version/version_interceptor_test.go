/*
Copyright (c) 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package version

import (
	"context"
	"errors"
	"net"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

var _ = Describe("Interceptor", func() {
	Describe("Creation", func() {
		It("Can be created with all the mandatory parameters", func() {
			interceptor, err := NewInterceptor().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(interceptor).ToNot(BeNil())
			Expect(interceptor.product).ToNot(BeEmpty())
		})

		It("Can be created with a custom product", func() {
			interceptor, err := NewInterceptor().
				SetLogger(logger).
				SetProduct("my_product").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(interceptor).ToNot(BeNil())
		})

		It("Can be created with a custom version", func() {
			interceptor, err := NewInterceptor().
				SetLogger(logger).
				SetVersion("2.0.0").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(interceptor).ToNot(BeNil())
		})

		It("Can be created with both custom product and version", func() {
			interceptor, err := NewInterceptor().
				SetLogger(logger).
				SetProduct("my_product").
				SetVersion("2.0.0").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(interceptor).ToNot(BeNil())
		})

		It("Can't be created without a logger", func() {
			interceptor, err := NewInterceptor().
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(interceptor).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var (
			ctx         context.Context
			server      *grpc.Server
			listener    net.Listener
			conn        *grpc.ClientConn
			interceptor *Interceptor
		)

		BeforeEach(func() {
			var err error

			// Create the context:
			ctx = context.Background()

			// Create the interceptor:
			interceptor, err = NewInterceptor().
				SetProduct("my_product").
				SetVersion("2.0.0").
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create a test server:
			listener, err = net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			server = grpc.NewServer()
			go func() {
				defer GinkgoRecover()
				_ = server.Serve(listener)
			}()
			DeferCleanup(server.Stop)

			// Create a client connection with interceptor:
			conn, err = grpc.NewClient(
				listener.Addr().String(),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithUnaryInterceptor(interceptor.UnaryClient),
				grpc.WithStreamInterceptor(interceptor.StreamClient),
			)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(conn.Close)
		})

		Describe("Unary client", func() {
			It("Adds user agent header to unary client requests", func() {
				// Mock invoker that captures metadata:
				var md metadata.MD
				invoker := func(ctx context.Context, _ string, _ any, _ any, _ *grpc.ClientConn,
					_ ...grpc.CallOption) error {
					var ok bool
					md, ok = metadata.FromOutgoingContext(ctx)
					Expect(ok).To(BeTrue())
					return nil
				}

				// Call the interceptor:
				err := interceptor.UnaryClient(ctx, "", nil, nil, conn, invoker)
				Expect(err).ToNot(HaveOccurred())

				// Verify user agent was added:
				header := md.Get("User-Agent")
				Expect(header).To(HaveLen(1))
				Expect(header[0]).To(Equal("my_product/2.0.0"))
			})

			It("Preserves existing metadata", func() {
				// Create context with existing metadata:
				ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("My-Header", "my_value"))

				// Mock invoker that captures metadata:
				var md metadata.MD
				invoker := func(ctx context.Context, _ string, _ any, _ any, _ *grpc.ClientConn,
					_ ...grpc.CallOption) error {
					var ok bool
					md, ok = metadata.FromOutgoingContext(ctx)
					Expect(ok).To(BeTrue())
					return nil
				}

				// Call the interceptor:
				err := interceptor.UnaryClient(ctx, "", nil, nil, conn, invoker)
				Expect(err).ToNot(HaveOccurred())

				// Verify both headers are present:
				header := md.Get("User-Agent")
				Expect(header).To(HaveLen(1))
				Expect(header[0]).To(Equal("my_product/2.0.0"))
				header = md.Get("My-Header")
				Expect(header).To(HaveLen(1))
				Expect(header[0]).To(Equal("my_value"))
			})

			It("Forwards invoker errors", func() {
				// Mock invoker that returns an error:
				invoker := func(context.Context, string, any, any, *grpc.ClientConn,
					...grpc.CallOption) error {
					return errors.New("my error")
				}

				// Call the interceptor:
				err := interceptor.UnaryClient(ctx, "", nil, nil, conn, invoker)
				Expect(err).To(MatchError("my error"))
			})
		})
	})
})
