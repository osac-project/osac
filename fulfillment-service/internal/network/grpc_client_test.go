/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package network

import (
	"context"
	"crypto/tls"
	"net"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/testing"
)

var _ = Describe("gRPC client", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		ctrl   *gomock.Controller
	)

	BeforeEach(func() {
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		DeferCleanup(cancel)
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("Can't be created without a logger", func() {
		client, err := NewGrpcClient().
			SetAddress("127.0.0.1:0").
			Build()
		Expect(err).To(HaveOccurred())
		Expect(client).To(BeNil())
		msg := err.Error()
		Expect(msg).To(ContainSubstring("logger"))
		Expect(msg).To(ContainSubstring("mandatory"))
	})

	It("Can't be created without an address", func() {
		client, err := NewGrpcClient().
			SetLogger(logger).
			Build()
		Expect(err).To(HaveOccurred())
		Expect(client).To(BeNil())
		msg := err.Error()
		Expect(msg).To(ContainSubstring("address"))
		Expect(msg).To(ContainSubstring("mandatory"))
	})

	It("Can connect to a real gRPC server using plaintext", func() {
		// Create a test gRPC server with a random port:
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())

		// Create and start the gRPC server with health service:
		server := grpc.NewServer()
		healthServer := health.NewServer()
		healthpb.RegisterHealthServer(server, healthServer)
		healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
		go func() {
			defer GinkgoRecover()
			_ = server.Serve(listener)
		}()
		defer server.Stop()

		// Create the client:
		client, err := NewGrpcClient().
			SetLogger(logger).
			SetAddress(listener.Addr().String()).
			SetPlaintext(true).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())
		defer func() {
			err := client.Close()
			Expect(err).ToNot(HaveOccurred())
		}()

		// Verify the connection by calling the health service:
		healthClient := healthpb.NewHealthClient(client)
		response, err := healthClient.Check(ctx, &healthpb.HealthCheckRequest{
			Service: "",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.Status).To(Equal(healthpb.HealthCheckResponse_SERVING))
	})

	It("Can connect to a real gRPC server using TLS", func() {
		// Create a test gRPC server with TLS and a random port
		tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		tlsListener := tls.NewListener(tcpListener, &tls.Config{
			Certificates: []tls.Certificate{
				testing.LocalhostCertificate(),
			},
		})

		// Create and start the gRPC server with health service:
		server := grpc.NewServer()
		healthServer := health.NewServer()
		healthpb.RegisterHealthServer(server, healthServer)
		healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
		go func() {
			defer GinkgoRecover()
			_ = server.Serve(tlsListener)
		}()
		defer server.Stop()

		// Create the client:
		client, err := NewGrpcClient().
			SetLogger(logger).
			SetAddress(tcpListener.Addr().String()).
			SetInsecure(true).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())
		defer func() {
			err := client.Close()
			Expect(err).ToNot(HaveOccurred())
		}()

		// Verify the connection by calling the health service:
		healthClient := healthpb.NewHealthClient(client)
		response, err := healthClient.Check(ctx, &healthpb.HealthCheckRequest{
			Service: "",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.Status).To(Equal(healthpb.HealthCheckResponse_SERVING))
	})

	It("Fails to connect to non-existent server", func() {
		// Create a client that tries to connect to a non existent server:
		client, err := NewGrpcClient().
			SetLogger(logger).
			SetAddress("127.0.0.1:0").
			SetPlaintext(true).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())
		defer func() {
			err := client.Close()
			Expect(err).ToNot(HaveOccurred())
		}()

		// Try to call the health service, and verify that it fails:
		healthClient := healthpb.NewHealthClient(client)
		_, err = healthClient.Check(ctx, &healthpb.HealthCheckRequest{
			Service: "",
		})
		Expect(err).To(HaveOccurred())
	})

	It("Can use custom interceptors", func() {
		// Create a test gRPC server with a random port:
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())

		// Create and start the gRPC server with health service
		server := grpc.NewServer()
		healthServer := health.NewServer()
		healthpb.RegisterHealthServer(server, healthServer)
		healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
		go func() {
			defer GinkgoRecover()
			_ = server.Serve(listener)
		}()
		defer server.Stop()

		// Create a custom interceptor that adds a header
		called := false
		interceptor := func(ctx context.Context, method string, request, reply any, cc *grpc.ClientConn,
			invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			called = true
			return invoker(ctx, method, request, reply, cc, opts...)
		}

		// Create the client:
		client, err := NewGrpcClient().
			SetLogger(logger).
			SetAddress(listener.Addr().String()).
			SetPlaintext(true).
			AddUnaryInterceptor(interceptor).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())
		defer func() {
			err := client.Close()
			Expect(err).ToNot(HaveOccurred())
		}()

		// Verify the connection and that the interceptor was called:
		healthClient := healthpb.NewHealthClient(client)
		response, err := healthClient.Check(ctx, &healthpb.HealthCheckRequest{
			Service: "",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.Status).To(Equal(healthpb.HealthCheckResponse_SERVING))
		Expect(called).To(BeTrue())
	})

	It("Sends the configured user agent", func() {
		// Create a test gRPC server with a random port:
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())

		// Create a server interceptor to capture the user agent:
		var agent string
		serverInterceptor := func(ctx context.Context, request any, info *grpc.UnaryServerInfo,
			handler grpc.UnaryHandler) (any, error) {
			md, ok := metadata.FromIncomingContext(ctx)
			if ok {
				agents := md.Get("user-agent")
				if len(agents) > 0 {
					agent = agents[0]
				}
			}
			return handler(ctx, request)
		}

		// Create and start the gRPC server with health service:
		server := grpc.NewServer(grpc.UnaryInterceptor(serverInterceptor))
		healthServer := health.NewServer()
		healthpb.RegisterHealthServer(server, healthServer)
		healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
		go func() {
			defer GinkgoRecover()
			_ = server.Serve(listener)
		}()
		defer server.Stop()

		// Create the client with a custom user agent:
		client, err := NewGrpcClient().
			SetLogger(logger).
			SetAddress(listener.Addr().String()).
			SetPlaintext(true).
			SetUserAgent("my-agent/1.0").
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())
		defer func() {
			err := client.Close()
			Expect(err).ToNot(HaveOccurred())
		}()

		// Make a request to trigger the interceptor:
		healthClient := healthpb.NewHealthClient(client)
		response, err := healthClient.Check(ctx, &healthpb.HealthCheckRequest{
			Service: "",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.Status).To(Equal(healthpb.HealthCheckResponse_SERVING))

		// Verify the user agent was captured and contains our custom value:
		Expect(agent).To(ContainSubstring("my-agent/1.0"))
	})

	It("Sends the configured host in the authority header", func() {
		// Create a test gRPC server with a random port:
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())

		// Create a server interceptor to capture the authority:
		var authority string
		serverInterceptor := func(ctx context.Context, request any, info *grpc.UnaryServerInfo,
			handler grpc.UnaryHandler) (any, error) {
			md, ok := metadata.FromIncomingContext(ctx)
			if ok {
				authorities := md.Get(":authority")
				if len(authorities) > 0 {
					authority = authorities[0]
				}
			}
			return handler(ctx, request)
		}

		// Create and start the gRPC server with health service:
		server := grpc.NewServer(grpc.UnaryInterceptor(serverInterceptor))
		healthServer := health.NewServer()
		healthpb.RegisterHealthServer(server, healthServer)
		healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
		go func() {
			defer GinkgoRecover()
			_ = server.Serve(listener)
		}()
		defer server.Stop()

		// Create the client with a custom host:
		client, err := NewGrpcClient().
			SetLogger(logger).
			SetAddress(listener.Addr().String()).
			SetHost("my.example.com").
			SetPlaintext(true).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())
		defer func() {
			err := client.Close()
			Expect(err).ToNot(HaveOccurred())
		}()

		// Make a request to trigger the interceptor:
		healthClient := healthpb.NewHealthClient(client)
		response, err := healthClient.Check(ctx, &healthpb.HealthCheckRequest{
			Service: "",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.Status).To(Equal(healthpb.HealthCheckResponse_SERVING))

		// Verify the authority was captured and equals our custom host:
		Expect(authority).To(Equal("my.example.com"))
	})

	It("Retries once when the server returns the unauthenticated status code", func() {
		// Create a TLS listener:
		tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		tlsListener := tls.NewListener(tcpListener, &tls.Config{
			Certificates: []tls.Certificate{
				testing.LocalhostCertificate(),
			},
		})

		// Track call count and return the unauthenticated status code only on the first call:
		var calls atomic.Int32
		interceptor := func(ctx context.Context, request any, info *grpc.UnaryServerInfo,
			handler grpc.UnaryHandler) (response any, err error) {
			if calls.Add(1) == 1 {
				err = status.Error(codes.Unauthenticated, "token expired")
				return
			}
			response, err = handler(ctx, request)
			return
		}

		// Start the server:
		server := grpc.NewServer(grpc.UnaryInterceptor(interceptor))
		healthServer := health.NewServer()
		healthpb.RegisterHealthServer(server, healthServer)
		healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
		go func() {
			defer GinkgoRecover()
			_ = server.Serve(tlsListener)
		}()
		defer server.Stop()

		// There should be two calls to get the token, and one call to invalidate it:
		token := &auth.Token{
			Access: "test-token",
			Expiry: time.Now().Add(time.Hour),
		}
		tokenSource := auth.NewMockTokenSource(ctrl)
		tokenSource.EXPECT().Token(gomock.Any()).Return(token, nil).Times(2)
		tokenSource.EXPECT().Invalidate(gomock.Any()).Return(nil).Times(1)

		// Create the client with the token source so the retry interceptor is active:
		client, err := NewGrpcClient().
			SetLogger(logger).
			SetAddress(tcpListener.Addr().String()).
			SetInsecure(true).
			SetTokenSource(tokenSource).
			Build()
		Expect(err).ToNot(HaveOccurred())
		defer func() {
			err := client.Close()
			Expect(err).ToNot(HaveOccurred())
		}()

		// The first attempt gets the unauthenticated status code, but the retry should succeed:
		healthClient := healthpb.NewHealthClient(client)
		response, err := healthClient.Check(ctx, &healthpb.HealthCheckRequest{
			Service: "",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(response.Status).To(Equal(healthpb.HealthCheckResponse_SERVING))
		Expect(calls.Load()).To(BeNumerically("==", 2))
	})

	It("Returns the unauthenticated status code when the retry also fails", func() {
		// Create a TLS listener:
		tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		tlsListener := tls.NewListener(tcpListener, &tls.Config{
			Certificates: []tls.Certificate{
				testing.LocalhostCertificate(),
			},
		})

		// Always return the unauthenticated status code:
		var calls atomic.Int32
		interceptor := func(ctx context.Context, request any, info *grpc.UnaryServerInfo,
			handler grpc.UnaryHandler) (response any, err error) {
			calls.Add(1)
			err = status.Error(codes.Unauthenticated, "invalid credentials")
			return
		}

		// Start the server:
		server := grpc.NewServer(grpc.UnaryInterceptor(interceptor))
		healthServer := health.NewServer()
		healthpb.RegisterHealthServer(server, healthServer)
		healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
		go func() {
			defer GinkgoRecover()
			_ = server.Serve(tlsListener)
		}()
		defer server.Stop()

		// There should be two calls to get the token, and one call to invalidate it:
		token := &auth.Token{
			Access: "test-token",
			Expiry: time.Now().Add(time.Hour),
		}
		tokenSource := auth.NewMockTokenSource(ctrl)
		tokenSource.EXPECT().Token(gomock.Any()).Return(token, nil).Times(2)
		tokenSource.EXPECT().Invalidate(gomock.Any()).Return(nil).Times(1)

		// Create the client:
		client, err := NewGrpcClient().
			SetLogger(logger).
			SetAddress(tcpListener.Addr().String()).
			SetInsecure(true).
			SetTokenSource(tokenSource).
			Build()
		Expect(err).ToNot(HaveOccurred())
		defer func() {
			err := client.Close()
			Expect(err).ToNot(HaveOccurred())
		}()

		// Both attempts fail, so the error is returned to the caller:
		healthClient := healthpb.NewHealthClient(client)
		_, err = healthClient.Check(ctx, &healthpb.HealthCheckRequest{
			Service: "",
		})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.Unauthenticated))
		Expect(calls.Load()).To(BeNumerically("==", 2))
	})

	It("Does not retry when the server returns a status code other than unauthenticated", func() {
		// Create a TLS listener:
		tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		tlsListener := tls.NewListener(tcpListener, &tls.Config{
			Certificates: []tls.Certificate{
				testing.LocalhostCertificate(),
			},
		})

		// Always return the permission denied status code:
		var calls atomic.Int32
		interceptor := func(ctx context.Context, request any, info *grpc.UnaryServerInfo,
			handler grpc.UnaryHandler) (response any, err error) {
			calls.Add(1)
			err = status.Error(codes.PermissionDenied, "forbidden")
			return
		}

		// Start the server:
		server := grpc.NewServer(grpc.UnaryInterceptor(interceptor))
		healthServer := health.NewServer()
		healthpb.RegisterHealthServer(server, healthServer)
		healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
		go func() {
			defer GinkgoRecover()
			_ = server.Serve(tlsListener)
		}()
		defer server.Stop()

		// There should be one call to get the token, and no call to invalidate it:
		token := &auth.Token{
			Access: "test-token",
			Expiry: time.Now().Add(time.Hour),
		}
		tokenSource := auth.NewMockTokenSource(ctrl)
		tokenSource.EXPECT().Token(gomock.Any()).Return(token, nil).Times(1)

		// Create the client:
		client, err := NewGrpcClient().
			SetLogger(logger).
			SetAddress(tcpListener.Addr().String()).
			SetInsecure(true).
			SetTokenSource(tokenSource).
			Build()
		Expect(err).ToNot(HaveOccurred())
		defer func() {
			err := client.Close()
			Expect(err).ToNot(HaveOccurred())
		}()

		// The error is returned directly without retrying:
		healthClient := healthpb.NewHealthClient(client)
		_, err = healthClient.Check(ctx, &healthpb.HealthCheckRequest{
			Service: "",
		})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied))
		Expect(calls.Load()).To(Equal(int32(1)))
	})
})
