/*
Copyright (c) 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package metrics

import (
	"context"
	"io"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"

	. "github.com/osac-project/fulfillment-service/internal/testing"
)

var _ = Describe("Create", func() {
	It("Can't be created without a subsystem", func() {
		interceptor, err := NewGrpcInterceptor().
			SetRegisterer(prometheus.NewRegistry()).
			Build()
		Expect(err).To(HaveOccurred())
		Expect(interceptor).To(BeNil())
		message := err.Error()
		Expect(message).To(ContainSubstring("subsystem"))
		Expect(message).To(ContainSubstring("mandatory"))
	})

	It("Can't be created without a registerer", func() {
		interceptor, err := NewGrpcInterceptor().
			SetSubsystem("my_subsystem").
			Build()
		Expect(err).To(HaveOccurred())
		Expect(interceptor).To(BeNil())
		message := err.Error()
		Expect(message).To(ContainSubstring("registerer"))
		Expect(message).To(ContainSubstring("mandatory"))
	})

	It("Can be created with a subsystem", func() {
		interceptor, err := NewGrpcInterceptor().
			SetSubsystem("my_subsystem").
			SetRegisterer(prometheus.NewRegistry()).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(interceptor).ToNot(BeNil())
	})

	It("Handles already registered metrics gracefully", func() {
		registry := prometheus.NewRegistry()

		// Create first interceptor:
		interceptor1, err := NewGrpcInterceptor().
			SetSubsystem("duplicate_test").
			SetRegisterer(registry).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(interceptor1).ToNot(BeNil())

		// Create second interceptor with same subsystem - should reuse existing metrics:
		interceptor2, err := NewGrpcInterceptor().
			SetSubsystem("duplicate_test").
			SetRegisterer(registry).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(interceptor2).ToNot(BeNil())
	})
})

var _ = Describe("Metrics", func() {
	var (
		ctx         context.Context
		server      *MetricsServer
		interceptor *GrpcInterceptor
	)

	BeforeEach(func() {
		var err error

		// Create a context:
		ctx = context.Background()

		// Create a metrics server:
		server = NewMetricsServer()

		// Create the interceptor:
		interceptor, err = NewGrpcInterceptor().
			SetSubsystem("my").
			SetRegisterer(server.Registry()).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(interceptor).ToNot(BeNil())
	})

	AfterEach(func() {
		// Stop the metrics server:
		server.Close()
	})

	// callUnary sends a unary request with the given method and returns an error if the handler returns one.
	callUnary := func(method string, handlerErr error) error {
		info := &grpc.UnaryServerInfo{FullMethod: method}
		handler := func(ctx context.Context, req any) (any, error) {
			if handlerErr != nil {
				return nil, handlerErr
			}
			return "response", nil
		}
		_, err := interceptor.UnaryServer(ctx, "request", info, handler)
		return err
	}

	// callStream sends a stream request with the given method and returns an error if the handler returns one.
	callStream := func(method string, handlerErr error) error {
		info := &grpc.StreamServerInfo{FullMethod: method}
		handler := func(srv any, stream grpc.ServerStream) error {
			return handlerErr
		}
		mockStream := &mockServerStream{ctx: ctx}
		return interceptor.StreamServer(nil, mockStream, info, handler)
	}

	Describe("Unary request count", func() {
		It("Honours subsystem", func() {
			// Send a request:
			err := callUnary("/package.Service/Method", nil)
			Expect(err).ToNot(HaveOccurred())

			// Verify the metrics:
			metrics := server.Metrics()
			Expect(metrics).To(MatchLine(`^my_unary_request_count\{.*\} .*$`))
		})

		DescribeTable(
			"Counts correctly",
			func(count int) {
				// Send multiple requests:
				for i := 0; i < count; i++ {
					err := callUnary("/package.Service/Method", nil)
					Expect(err).ToNot(HaveOccurred())
				}

				// Verify the metrics:
				metrics := server.Metrics()
				Expect(metrics).To(MatchLine(`^\w+_unary_request_count\{.*\} %d$`, count))
			},
			Entry(
				"One",
				1,
			),
			Entry(
				"Two",
				2,
			),
			Entry(
				"Three",
				3,
			),
		)

		DescribeTable(
			"Includes service label",
			func(fullMethod, expectedService string) {
				// Send a request:
				err := callUnary(fullMethod, nil)
				Expect(err).ToNot(HaveOccurred())

				// Verify the metrics:
				metrics := server.Metrics()
				Expect(metrics).To(MatchLine(`^\w+_unary_request_count\{.*service="%s".*\} .*$`, expectedService))
			},
			Entry(
				"Simple service",
				"/mypackage.MyService/MyMethod",
				"mypackage.MyService",
			),
			Entry(
				"Versioned service",
				"/osac.public.v1.Clusters/Create",
				"osac.public.v1.Clusters",
			),
			Entry(
				"Complex package",
				"/com.example.api.v1.MyService/DoSomething",
				"com.example.api.v1.MyService",
			),
			Entry(
				"Without leading slash",
				"mypackage.MyService/MyMethod",
				"mypackage.MyService",
			),
		)

		DescribeTable(
			"Includes method label",
			func(fullMethod, expectedMethod string) {
				// Send a request:
				err := callUnary(fullMethod, nil)
				Expect(err).ToNot(HaveOccurred())

				// Verify the metrics:
				metrics := server.Metrics()
				Expect(metrics).To(MatchLine(`^\w+_unary_request_count\{.*method="%s".*\} .*$`, expectedMethod))
			},
			Entry(
				"Create",
				"/osac.public.v1.Clusters/Create",
				"Create",
			),
			Entry(
				"Get",
				"/osac.public.v1.Clusters/Get",
				"Get",
			),
			Entry(
				"List",
				"/osac.public.v1.Clusters/List",
				"List",
			),
			Entry(
				"Update",
				"/osac.public.v1.Clusters/Update",
				"Update",
			),
			Entry(
				"Delete",
				"/osac.public.v1.Clusters/Delete",
				"Delete",
			),
		)

		DescribeTable(
			"Includes code label",
			func(code grpccodes.Code) {
				// Send a request with the given error code:
				var handlerErr error
				if code != grpccodes.OK {
					handlerErr = grpcstatus.Error(code, "error")
				}
				_ = callUnary("/package.Service/Method", handlerErr)

				// Verify the metrics:
				metrics := server.Metrics()
				Expect(metrics).To(MatchLine(`^\w+_unary_request_count\{.*code="%s".*\} .*$`, code.String()))
			},
			Entry(
				"OK",
				grpccodes.OK,
			),
			Entry(
				"Canceled",
				grpccodes.Canceled,
			),
			Entry(
				"Unknown",
				grpccodes.Unknown,
			),
			Entry(
				"InvalidArgument",
				grpccodes.InvalidArgument,
			),
			Entry(
				"DeadlineExceeded",
				grpccodes.DeadlineExceeded,
			),
			Entry(
				"NotFound",
				grpccodes.NotFound,
			),
			Entry(
				"AlreadyExists",
				grpccodes.AlreadyExists,
			),
			Entry(
				"PermissionDenied",
				grpccodes.PermissionDenied,
			),
			Entry(
				"ResourceExhausted",
				grpccodes.ResourceExhausted,
			),
			Entry(
				"FailedPrecondition",
				grpccodes.FailedPrecondition,
			),
			Entry(
				"Aborted",
				grpccodes.Aborted,
			),
			Entry(
				"OutOfRange",
				grpccodes.OutOfRange,
			),
			Entry(
				"Unimplemented",
				grpccodes.Unimplemented,
			),
			Entry(
				"Internal",
				grpccodes.Internal,
			),
			Entry(
				"Unavailable",
				grpccodes.Unavailable,
			),
			Entry(
				"DataLoss",
				grpccodes.DataLoss,
			),
			Entry(
				"Unauthenticated",
				grpccodes.Unauthenticated,
			),
		)

	})

	Describe("Stream count", func() {
		It("Works for stream requests", func() {
			// Send a stream request:
			err := callStream("/osac.public.v1.Events/Watch", nil)
			Expect(err).ToNot(HaveOccurred())

			// Verify the metrics:
			metrics := server.Metrics()
			Expect(metrics).To(MatchLine(`^\w+_stream_count\{.*service="osac.public.v1.Events".*\} 1$`))
			Expect(metrics).To(MatchLine(`^\w+_stream_count\{.*method="Watch".*\} 1$`))
			Expect(metrics).To(MatchLine(`^\w+_stream_count\{.*code="OK".*\} 1$`))
		})
	})

	Describe("Unary request duration", func() {
		It("Honours subsystem", func() {
			// Send a request:
			err := callUnary("/package.Service/Method", nil)
			Expect(err).ToNot(HaveOccurred())

			// Verify the metrics:
			metrics := server.Metrics()
			Expect(metrics).To(MatchLine(`^my_unary_request_duration_bucket\{.*\} .*$`))
			Expect(metrics).To(MatchLine(`^my_unary_request_duration_sum\{.*\} .*$`))
			Expect(metrics).To(MatchLine(`^my_unary_request_duration_count\{.*\} .*$`))
		})

		DescribeTable(
			"Counts correctly",
			func(count int) {
				// Send multiple requests:
				for range count {
					err := callUnary("/package.Service/Method", nil)
					Expect(err).ToNot(HaveOccurred())
				}

				// Verify the metrics:
				metrics := server.Metrics()
				Expect(metrics).To(MatchLine(`^\w+_unary_request_duration_count\{.*\} %d$`, count))
			},
			Entry(
				"One",
				1,
			),
			Entry(
				"Two",
				2,
			),
			Entry(
				"Three",
				3,
			),
		)

		DescribeTable(
			"Includes service label",
			func(fullMethod, expectedService string) {
				// Send a request:
				err := callUnary(fullMethod, nil)
				Expect(err).ToNot(HaveOccurred())

				// Verify the metrics:
				metrics := server.Metrics()
				Expect(metrics).To(MatchLine(`^\w+_unary_request_duration_bucket\{.*service="%s".*\} .*$`, expectedService))
				Expect(metrics).To(MatchLine(`^\w+_unary_request_duration_sum\{.*service="%s".*\} .*$`, expectedService))
				Expect(metrics).To(MatchLine(`^\w+_unary_request_duration_count\{.*service="%s".*\} .*$`, expectedService))
			},
			Entry(
				"Simple service",
				"/mypackage.MyService/MyMethod",
				"mypackage.MyService",
			),
			Entry(
				"Versioned service",
				"/osac.public.v1.Clusters/Create",
				"osac.public.v1.Clusters",
			),
			Entry(
				"Complex package",
				"/com.example.api.v1.MyService/DoSomething",
				"com.example.api.v1.MyService",
			),
		)

		DescribeTable(
			"Includes method label",
			func(fullMethod, expectedMethod string) {
				// Send a request:
				err := callUnary(fullMethod, nil)
				Expect(err).ToNot(HaveOccurred())

				// Verify the metrics:
				metrics := server.Metrics()
				Expect(metrics).To(MatchLine(`^\w+_unary_request_duration_bucket\{.*method="%s".*\} .*$`, expectedMethod))
				Expect(metrics).To(MatchLine(`^\w+_unary_request_duration_sum\{.*method="%s".*\} .*$`, expectedMethod))
				Expect(metrics).To(MatchLine(`^\w+_unary_request_duration_count\{.*method="%s".*\} .*$`, expectedMethod))
			},
			Entry(
				"Create",
				"/osac.public.v1.Clusters/Create",
				"Create",
			),
			Entry(
				"Get",
				"/osac.public.v1.Clusters/Get",
				"Get",
			),
			Entry(
				"List",
				"/osac.public.v1.Clusters/List",
				"List",
			),
			Entry(
				"Update",
				"/osac.public.v1.Clusters/Update",
				"Update",
			),
			Entry(
				"Delete",
				"/osac.public.v1.Clusters/Delete",
				"Delete",
			),
		)

		DescribeTable(
			"Includes code label",
			func(code grpccodes.Code) {
				// Send a request with the given error code:
				var handlerErr error
				if code != grpccodes.OK {
					handlerErr = grpcstatus.Error(code, "error")
				}
				_ = callUnary("/package.Service/Method", handlerErr)

				// Verify the metrics:
				metrics := server.Metrics()
				Expect(metrics).To(MatchLine(`^\w+_unary_request_duration_bucket\{.*code="%s".*\} .*$`, code.String()))
				Expect(metrics).To(MatchLine(`^\w+_unary_request_duration_sum\{.*code="%s".*\} .*$`, code.String()))
				Expect(metrics).To(MatchLine(`^\w+_unary_request_duration_count\{.*code="%s".*\} .*$`, code.String()))
			},
			Entry(
				"OK",
				grpccodes.OK,
			),
			Entry(
				"NotFound",
				grpccodes.NotFound,
			),
			Entry(
				"Internal",
				grpccodes.Internal,
			),
			Entry(
				"Unauthenticated",
				grpccodes.Unauthenticated,
			),
		)
	})

	Describe("Stream duration", func() {
		It("Works for stream requests", func() {
			// Send a stream request:
			err := callStream("/osac.public.v1.Events/Watch", nil)
			Expect(err).ToNot(HaveOccurred())

			// Verify the metrics:
			metrics := server.Metrics()
			Expect(metrics).To(MatchLine(`^\w+_stream_duration_count\{.*service="osac.public.v1.Events".*\} 1$`))
			Expect(metrics).To(MatchLine(`^\w+_stream_duration_count\{.*method="Watch".*\} 1$`))
			Expect(metrics).To(MatchLine(`^\w+_stream_duration_count\{.*code="OK".*\} 1$`))
		})
	})

	// callUnaryClient simulates a unary client call with the given method and returns an error if the invoker
	// returns one.
	callUnaryClient := func(method string, invokerErr error) error {
		invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn,
			opts ...grpc.CallOption) error {
			return invokerErr
		}
		return interceptor.UnaryClient(ctx, method, "request", "reply", nil, invoker)
	}

	// callStreamClient simulates a stream client call with the given method and returns the wrapped stream.
	callStreamClient := func(method string, mockStream *mockClientStream) (grpc.ClientStream, error) {
		desc := &grpc.StreamDesc{StreamName: "TestStream"}
		streamer := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string,
			opts ...grpc.CallOption) (grpc.ClientStream, error) {
			if mockStream == nil {
				return nil, grpcstatus.Error(grpccodes.Unavailable, "connection failed")
			}
			return mockStream, nil
		}
		return interceptor.StreamClient(ctx, desc, nil, method, streamer)
	}

	Describe("Unary client request count", func() {
		It("Honours subsystem", func() {
			// Send a request:
			err := callUnaryClient("/package.Service/Method", nil)
			Expect(err).ToNot(HaveOccurred())

			// Verify the metrics:
			metrics := server.Metrics()
			Expect(metrics).To(MatchLine(`^my_unary_request_count\{.*\} .*$`))
		})

		DescribeTable(
			"Counts correctly",
			func(count int) {
				// Send multiple requests:
				for i := 0; i < count; i++ {
					err := callUnaryClient("/package.Service/Method", nil)
					Expect(err).ToNot(HaveOccurred())
				}

				// Verify the metrics:
				metrics := server.Metrics()
				Expect(metrics).To(MatchLine(`^\w+_unary_request_count\{.*\} %d$`, count))
			},
			Entry(
				"One",
				1,
			),
			Entry(
				"Two",
				2,
			),
			Entry(
				"Three",
				3,
			),
		)

		DescribeTable(
			"Includes service label",
			func(fullMethod, expectedService string) {
				// Send a request:
				err := callUnaryClient(fullMethod, nil)
				Expect(err).ToNot(HaveOccurred())

				// Verify the metrics:
				metrics := server.Metrics()
				Expect(metrics).To(MatchLine(`^\w+_unary_request_count\{.*service="%s".*\} .*$`, expectedService))
			},
			Entry(
				"Simple service",
				"/mypackage.MyService/MyMethod",
				"mypackage.MyService",
			),
			Entry(
				"Versioned service",
				"/osac.public.v1.Clusters/Create",
				"osac.public.v1.Clusters",
			),
		)

		DescribeTable(
			"Includes method label",
			func(fullMethod, expectedMethod string) {
				// Send a request:
				err := callUnaryClient(fullMethod, nil)
				Expect(err).ToNot(HaveOccurred())

				// Verify the metrics:
				metrics := server.Metrics()
				Expect(metrics).To(MatchLine(`^\w+_unary_request_count\{.*method="%s".*\} .*$`, expectedMethod))
			},
			Entry(
				"Create",
				"/osac.public.v1.Clusters/Create",
				"Create",
			),
			Entry(
				"Get",
				"/osac.public.v1.Clusters/Get",
				"Get",
			),
		)

		DescribeTable(
			"Includes code label",
			func(code grpccodes.Code) {
				// Send a request with the given error code:
				var invokerErr error
				if code != grpccodes.OK {
					invokerErr = grpcstatus.Error(code, "error")
				}
				_ = callUnaryClient("/package.Service/Method", invokerErr)

				// Verify the metrics:
				metrics := server.Metrics()
				Expect(metrics).To(MatchLine(`^\w+_unary_request_count\{.*code="%s".*\} .*$`, code.String()))
			},
			Entry(
				"OK",
				grpccodes.OK,
			),
			Entry(
				"NotFound",
				grpccodes.NotFound,
			),
			Entry(
				"Internal",
				grpccodes.Internal,
			),
		)
	})

	Describe("Stream client count", func() {
		It("Records metrics when stream creation fails", func() {
			// Try to create a stream that fails:
			stream, err := callStreamClient("/osac.public.v1.Events/Watch", nil)
			Expect(err).To(HaveOccurred())
			Expect(stream).To(BeNil())

			// Verify the metrics:
			metrics := server.Metrics()
			Expect(metrics).To(MatchLine(`^\w+_stream_count\{.*service="osac.public.v1.Events".*\} 1$`))
			Expect(metrics).To(MatchLine(`^\w+_stream_count\{.*method="Watch".*\} 1$`))
			Expect(metrics).To(MatchLine(`^\w+_stream_count\{.*code="Unavailable".*\} 1$`))
		})

		It("Records metrics when stream completes with EOF", func() {
			// Create a mock stream that returns EOF after some messages:
			mockStream := &mockClientStream{ctx: ctx, maxRecv: 2, recvErr: io.EOF}

			// Create the stream:
			stream, err := callStreamClient("/osac.public.v1.Events/Watch", mockStream)
			Expect(err).ToNot(HaveOccurred())
			Expect(stream).ToNot(BeNil())

			// Receive messages until EOF:
			for {
				err = stream.RecvMsg(nil)
				if err != nil {
					break
				}
			}
			Expect(err).To(Equal(io.EOF))

			// Verify the metrics - EOF should be recorded as OK:
			metrics := server.Metrics()
			Expect(metrics).To(MatchLine(`^\w+_stream_count\{.*service="osac.public.v1.Events".*\} 1$`))
			Expect(metrics).To(MatchLine(`^\w+_stream_count\{.*method="Watch".*\} 1$`))
			Expect(metrics).To(MatchLine(`^\w+_stream_count\{.*code="OK".*\} 1$`))
		})

		It("Records metrics when stream completes with error", func() {
			// Create a mock stream that returns an error after some messages:
			mockStream := &mockClientStream{ctx: ctx, maxRecv: 1, recvErr: grpcstatus.Error(grpccodes.Internal, "error")}

			// Create the stream:
			stream, err := callStreamClient("/osac.public.v1.Events/Watch", mockStream)
			Expect(err).ToNot(HaveOccurred())
			Expect(stream).ToNot(BeNil())

			// Receive messages until error:
			for {
				err = stream.RecvMsg(nil)
				if err != nil {
					break
				}
			}
			Expect(err).To(HaveOccurred())

			// Verify the metrics:
			metrics := server.Metrics()
			Expect(metrics).To(MatchLine(`^\w+_stream_count\{.*service="osac.public.v1.Events".*\} 1$`))
			Expect(metrics).To(MatchLine(`^\w+_stream_count\{.*code="Internal".*\} 1$`))
		})

		It("Counts sent and received messages", func() {
			// Create a mock stream:
			mockStream := &mockClientStream{ctx: ctx, maxRecv: 3, recvErr: io.EOF}

			// Create the stream:
			stream, err := callStreamClient("/osac.public.v1.Events/Watch", mockStream)
			Expect(err).ToNot(HaveOccurred())

			// Send some messages:
			for i := 0; i < 2; i++ {
				err = stream.SendMsg(nil)
				Expect(err).ToNot(HaveOccurred())
			}

			// Receive messages until EOF:
			for {
				err = stream.RecvMsg(nil)
				if err != nil {
					break
				}
			}

			// Verify the metrics:
			metrics := server.Metrics()
			Expect(metrics).To(MatchLine(`^\w+_stream_messages_sent\{.*\} 2$`))
			Expect(metrics).To(MatchLine(`^\w+_stream_messages_received\{.*\} 3$`))
		})

		It("Records metrics only once on multiple errors", func() {
			// Create a mock stream that returns error:
			mockStream := &mockClientStream{ctx: ctx, maxRecv: 0, recvErr: io.EOF}

			// Create the stream:
			stream, err := callStreamClient("/osac.public.v1.Events/Watch", mockStream)
			Expect(err).ToNot(HaveOccurred())

			// Call RecvMsg multiple times after EOF:
			_ = stream.RecvMsg(nil)
			_ = stream.RecvMsg(nil)
			_ = stream.RecvMsg(nil)

			// Verify the metrics - should be counted only once:
			metrics := server.Metrics()
			Expect(metrics).To(MatchLine(`^\w+_stream_count\{.*code="OK".*\} 1$`))
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

// mockClientStream is a minimal implementation of grpc.ClientStream for testing.
type mockClientStream struct {
	ctx           context.Context
	recvCount     int
	maxRecv       int
	recvErr       error
	sentCount     int
	receivedCount int
}

func (m *mockClientStream) Context() context.Context {
	return m.ctx
}

func (m *mockClientStream) Header() (metadata.MD, error) {
	return nil, nil
}

func (m *mockClientStream) Trailer() metadata.MD {
	return nil
}

func (m *mockClientStream) CloseSend() error {
	return nil
}

func (m *mockClientStream) SendMsg(msg any) error {
	m.sentCount++
	return nil
}

func (m *mockClientStream) RecvMsg(msg any) error {
	if m.recvErr != nil && m.recvCount >= m.maxRecv {
		return m.recvErr
	}
	m.recvCount++
	m.receivedCount++
	return nil
}
