/*
Copyright (c) 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package logging

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	testsv1 "github.com/osac-project/osac/proto/gen/osac/tests/v1"
)

var _ = Describe("Interceptor", func() {
	var (
		ctx         context.Context
		server      *grpc.Server
		listener    net.Listener
		conn        *grpc.ClientConn
		interceptor *Interceptor
		buffer      *bytes.Buffer
		logger      *slog.Logger
	)

	BeforeEach(func() {
		var err error

		// Create the context:
		ctx = context.Background()

		// Prepare the writer so that it will write to a buffer in memory and also to the Ginkgo writer, so that
		// the log messages are also visible in the test output.
		buffer = &bytes.Buffer{}
		writer := io.MultiWriter(buffer, GinkgoWriter)

		// Create a logger that writes to the buffer:
		logger, err = NewLogger().
			SetLevel(slog.LevelDebug.String()).
			SetOut(writer).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create the interceptor:
		interceptor, err = NewInterceptor().
			SetLogger(logger).
			SetHeaders(true).
			SetBodies(true).
			SetRedact(false).
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
		It("Logs unary client requests and responses", func() {
			method := "/test.Service/TestMethod"

			// Mock request and response:
			request := testsv1.Object_builder{
				Id:       "my_id",
				MyString: "my_value",
			}.Build()
			response := testsv1.Object_builder{}.Build()

			// Mock invoker that copies request to response:
			invoker := func(ctx context.Context, method string, request, response any, cc *grpc.ClientConn,
				options ...grpc.CallOption) error {
				requestObject := response.(*testsv1.Object)
				responseObject := request.(*testsv1.Object)
				requestObject.Id = responseObject.Id
				requestObject.MyString = responseObject.MyString
				return nil
			}

			// Add metadata to context:
			ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer my_token"))

			// Call the interceptor:
			err := interceptor.UnaryClient(ctx, method, request, response, conn, invoker)
			Expect(err).ToNot(HaveOccurred())

			// Parse the log messages:
			messages := Parse(buffer)
			Expect(messages).To(HaveLen(2))

			// Verify request log:
			requestMessage := messages[0]
			Expect(requestMessage["msg"]).To(Equal("Sending unary request"))
			Expect(requestMessage["method"]).To(Equal(method))
			Expect(requestMessage["target"]).To(Equal(listener.Addr().String()))
			Expect(requestMessage["metadata"]).To(HaveKey("authorization"))
			Expect(requestMessage["request"]).To(HaveKey("myString"))

			// Verify response log:
			responseMessage := messages[1]
			Expect(responseMessage).To(HaveKeyWithValue("msg", "Received unary response"))
			Expect(responseMessage).To(HaveKeyWithValue("method", method))
			Expect(responseMessage).To(HaveKeyWithValue("target", listener.Addr().String()))
			Expect(responseMessage).To(HaveKeyWithValue("code", "OK"))
			Expect(responseMessage).To(HaveKeyWithValue("response", HaveKey("myString")))
		})

		It("Logs gRPC errors in unary client calls", func() {
			method := "/test.Service/TestMethod"

			// Mock request and response:
			request := &testsv1.Object{}
			response := &testsv1.Object{}

			// Mock invoker that returns an error:
			invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
				return grpcstatus.Error(grpccodes.NotFound, "not found")
			}

			// Call the interceptor:
			err := interceptor.UnaryClient(ctx, method, request, response, conn, invoker)
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
			Expect(status.Message()).To(Equal("not found"))

			// Parse the log messages:
			messages := Parse(buffer)
			Expect(messages).To(HaveLen(2))

			// Verify response log contains error code but no error detail:
			responseMessage := messages[1]
			Expect(responseMessage).To(HaveKeyWithValue("msg", "Received unary response"))
			Expect(responseMessage).To(HaveKeyWithValue("code", "NotFound"))
			Expect(responseMessage).To(HaveKeyWithValue("message", "not found"))
			Expect(responseMessage).ToNot(HaveKey("error"))
		})

		It("Logs other errors in unary client calls", func() {
			method := "/test.Service/TestMethod"

			// Mock request and response:
			request := &testsv1.Object{}
			response := &testsv1.Object{}

			// Mock invoker that returns an error:
			invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
				return errors.New("my error")
			}

			// Call the interceptor:
			err := interceptor.UnaryClient(ctx, method, request, response, conn, invoker)
			_, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeFalse())

			// Parse the log messages:
			messages := Parse(buffer)
			Expect(messages).To(HaveLen(2))

			// Verify response log contains error code but no error detail:
			responseMessage := messages[1]
			Expect(responseMessage).To(HaveKeyWithValue("msg", "Received unary response"))
			Expect(responseMessage).ToNot(HaveKey("code"))
			Expect(responseMessage).ToNot(HaveKey("message"))
			Expect(responseMessage).To(HaveKeyWithValue("error", HaveKeyWithValue("message", "my error")))
		})

		It("Skips logging for reflection methods", func() {
			method := "/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo"

			// Mock invoker:
			invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
				return nil
			}

			// Call the interceptor:
			err := interceptor.UnaryClient(ctx, method, nil, nil, conn, invoker)
			Expect(err).ToNot(HaveOccurred())

			// Parse the log messages - should be empty:
			messages := Parse(buffer)
			Expect(messages).To(BeEmpty())
		})

		It("Skips logging for health check methods", func() {
			method := "/grpc.health.v1.Health/Check"

			// Mock invoker:
			invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
				return nil
			}

			// Call the interceptor:
			err := interceptor.UnaryClient(ctx, method, nil, nil, conn, invoker)
			Expect(err).ToNot(HaveOccurred())

			// Parse the log messages - should be empty:
			messages := Parse(buffer)
			Expect(messages).To(BeEmpty())
		})

		It("Skips logging when debug is disabled", func() {
			// Create a logger with info level:
			infoLogger, err := NewLogger().
				SetLevel(slog.LevelInfo.String()).
				SetOut(buffer).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create interceptor with info logger:
			infoInterceptor, err := NewInterceptor().
				SetLogger(infoLogger).
				Build()
			Expect(err).ToNot(HaveOccurred())

			method := "/test.Service/TestMethod"

			// Mock invoker:
			invoker := func(ctx context.Context, method string, req, resp any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
				return nil
			}

			// Call the interceptor:
			err = infoInterceptor.UnaryClient(ctx, method, nil, nil, conn, invoker)
			Expect(err).ToNot(HaveOccurred())

			// Parse the log messages - should be empty:
			messages := Parse(buffer)
			Expect(messages).To(BeEmpty())
		})
	})

	Describe("Stream client", func() {
		It("Logs stream client start and operations", func() {
			method := "/test.Service/TestStream"
			desc := &grpc.StreamDesc{
				StreamName:    "TestStream",
				ClientStreams: true,
				ServerStreams: true,
			}

			// Mock streamer:
			streamer := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string,
				opts ...grpc.CallOption) (stream grpc.ClientStream, err error) {
				stream = &mockClientStream{
					ctx: ctx,
				}
				return
			}

			// Add metadata to context:
			ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer my_token"))

			// Call the interceptor:
			stream, err := interceptor.StreamClient(ctx, desc, conn, method, streamer)
			Expect(err).ToNot(HaveOccurred())
			Expect(stream).ToNot(BeNil())

			// Use the stream to trigger object logging:
			object := &testsv1.Object{MyString: "my_value", Id: "my_id"}
			err = stream.SendMsg(object)
			Expect(err).ToNot(HaveOccurred())
			err = stream.RecvMsg(object)
			Expect(err).To(Equal(io.EOF))

			// Parse the log messages:
			messages := Parse(buffer)
			Expect(messages).To(HaveLen(1))

			// Verify send log:
			sendMessage := messages[0]
			Expect(sendMessage["msg"]).To(Equal("Sent stream message"))
			Expect(sendMessage["bytes"]).To(BeNumerically(">", 0))
		})

		It("Skips logging for reflection methods", func() {
			method := "/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo"
			desc := &grpc.StreamDesc{}

			// Mock stream:
			mockStream := &mockClientStream{ctx: ctx}

			// Mock streamer:
			streamer := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string,
				opts ...grpc.CallOption) (grpc.ClientStream, error) {
				return mockStream, nil
			}

			// Call the interceptor:
			stream, err := interceptor.StreamClient(ctx, desc, conn, method, streamer)
			Expect(err).ToNot(HaveOccurred())
			Expect(stream).To(Equal(mockStream)) // Should return original stream

			// Parse the log messages - should be empty:
			messages := Parse(buffer)
			Expect(messages).To(BeEmpty())
		})

		It("Skips logging for health check methods", func() {
			method := "/grpc.health.v1.Health/Watch"
			desc := &grpc.StreamDesc{}

			// Mock stream:
			mockStream := &mockClientStream{ctx: ctx}

			// Mock streamer:
			streamer := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string,
				opts ...grpc.CallOption) (grpc.ClientStream, error) {
				return mockStream, nil
			}

			// Call the interceptor:
			stream, err := interceptor.StreamClient(ctx, desc, conn, method, streamer)
			Expect(err).ToNot(HaveOccurred())
			Expect(stream).To(Equal(mockStream)) // Should return original stream

			// Parse the log messages - should be empty:
			messages := Parse(buffer)
			Expect(messages).To(BeEmpty())
		})
	})

	It("Redacts Secret data from body logs", func() {
		redactedInterceptor, err := NewInterceptor().
			SetLogger(logger).
			SetBodies(true).
			SetRedact(true).
			Build()
		Expect(err).ToNot(HaveOccurred())

		request := privatev1.SecretsCreateRequest_builder{
			Object: privatev1.Secret_builder{
				Type: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
				Data: map[string][]byte{"public_key": []byte("ssh-ed25519 AAAAsecret")},
			}.Build(),
		}.Build()

		field, ok := redactedInterceptor.dumpMessage(ctx, "request", request)
		Expect(ok).To(BeTrue())
		logger.LogAttrs(ctx, slog.LevelDebug, "body", field.(slog.Attr))

		messages := Parse(buffer)
		Expect(messages).To(HaveLen(1))
		body := messages[0]["request"].(map[string]any)
		object := body["object"].(map[string]any)
		data := object["data"].(map[string]any)
		Expect(data).To(HaveKeyWithValue("public_key", "Kioq"))
		Expect(buffer.String()).ToNot(ContainSubstring("ssh-ed25519 AAAAsecret"))
		Expect(string(request.GetObject().GetData()["public_key"])).To(Equal("ssh-ed25519 AAAAsecret"))
	})

	It("Redacts public and private permission rule values without modifying messages", func() {
		permissionRules := []proto.Message{
			publicv1.PermissionRule_builder{
				Service: "osac.public.compute_instances",
				Methods: []string{"get", "update"},
			}.Build(),
			privatev1.PermissionRule_builder{
				Service: "osac.private.compute_instances",
				Methods: []string{"get", "update"},
			}.Build(),
		}

		for _, rule := range permissionRules {
			original := proto.Clone(rule)
			redacted := redactProtoMessage(rule).ProtoReflect()
			fields := redacted.Descriptor().Fields()

			Expect(redacted.Get(fields.ByName("service")).String()).To(Equal(redactMark))
			methods := redacted.Get(fields.ByName("methods")).List()
			Expect(methods.Len()).To(Equal(2))
			for index := 0; index < methods.Len(); index++ {
				Expect(methods.Get(index).String()).To(Equal(redactMark))
			}
			Expect(proto.Equal(rule, original)).To(BeTrue())
		}
	})

	It("Redacts ComputeInstance customer data from unary client body logs", func() {
		redactedInterceptor, err := NewInterceptor().
			SetLogger(logger).
			SetBodies(true).
			SetRedact(true).
			Build()
		Expect(err).ToNot(HaveOccurred())

		request, response, err := computeInstanceLogFixtures()
		Expect(err).ToNot(HaveOccurred())
		originalRequest := proto.Clone(request)
		originalResponse := proto.Clone(response)
		responseTemplate := proto.Clone(response)

		invoker := func(_ context.Context, _ string, _, response any, _ *grpc.ClientConn,
			_ ...grpc.CallOption) error {
			proto.Reset(response.(proto.Message))
			proto.Merge(response.(proto.Message), responseTemplate)
			return nil
		}
		err = redactedInterceptor.UnaryClient(ctx, "/osac.public.v1.ComputeInstances/Create",
			request, response, conn, invoker)
		Expect(err).ToNot(HaveOccurred())

		Expect(buffer.String()).ToNot(ContainSubstring("template-parameter-fixture-value"))
		Expect(buffer.String()).ToNot(ContainSubstring("user-data-fixture-value"))
		Expect(buffer.String()).ToNot(ContainSubstring("10.1.2.3"))
		Expect(buffer.String()).ToNot(ContainSubstring("192.0.2.4"))
		Expect(buffer.String()).ToNot(ContainSubstring("10.1.2.5"))
		Expect(buffer.String()).ToNot(ContainSubstring("subnet-private-id"))
		Expect(buffer.String()).ToNot(ContainSubstring("customer.example.test/private-annotation"))
		Expect(buffer.String()).ToNot(ContainSubstring("compute-instance-annotation-fixture-value"))
		Expect(buffer.String()).ToNot(ContainSubstring("customer.example.test/private-label"))
		Expect(buffer.String()).ToNot(ContainSubstring("compute-instance-label-fixture-value"))
		Expect(buffer.String()).To(ContainSubstring(redactMark))
		expectComputeInstanceBodyLogs(buffer, "Sending unary request", "Received unary response")
		Expect(proto.Equal(request, originalRequest)).To(BeTrue())
		Expect(proto.Equal(response, originalResponse)).To(BeTrue())
	})

	It("Redacts ComputeInstance customer data from unary server body logs", func() {
		redactedInterceptor, err := NewInterceptor().
			SetLogger(logger).
			SetBodies(true).
			SetRedact(true).
			Build()
		Expect(err).ToNot(HaveOccurred())

		request, response, err := computeInstanceLogFixtures()
		Expect(err).ToNot(HaveOccurred())
		originalRequest := proto.Clone(request)
		originalResponse := proto.Clone(response)

		_, err = redactedInterceptor.UnaryServer(ctx, request,
			&grpc.UnaryServerInfo{FullMethod: "/osac.public.v1.ComputeInstances/Create"},
			func(context.Context, any) (any, error) { return response, nil })
		Expect(err).ToNot(HaveOccurred())

		Expect(buffer.String()).ToNot(ContainSubstring("template-parameter-fixture-value"))
		Expect(buffer.String()).ToNot(ContainSubstring("user-data-fixture-value"))
		Expect(buffer.String()).ToNot(ContainSubstring("10.1.2.3"))
		Expect(buffer.String()).ToNot(ContainSubstring("192.0.2.4"))
		Expect(buffer.String()).ToNot(ContainSubstring("10.1.2.5"))
		Expect(buffer.String()).ToNot(ContainSubstring("subnet-private-id"))
		Expect(buffer.String()).ToNot(ContainSubstring("customer.example.test/private-annotation"))
		Expect(buffer.String()).ToNot(ContainSubstring("compute-instance-annotation-fixture-value"))
		Expect(buffer.String()).ToNot(ContainSubstring("customer.example.test/private-label"))
		Expect(buffer.String()).ToNot(ContainSubstring("compute-instance-label-fixture-value"))
		Expect(buffer.String()).To(ContainSubstring(redactMark))
		expectComputeInstanceBodyLogs(buffer, "Received unary request", "Sent unary response")
		Expect(proto.Equal(request, originalRequest)).To(BeTrue())
		Expect(proto.Equal(response, originalResponse)).To(BeTrue())
	})

	expectLifecycleUpdateCreatorRedacted := func(updatePath string) {
		redactedInterceptor, err := NewInterceptor().
			SetLogger(logger).
			SetBodies(true).
			SetRedact(true).
			Build()
		Expect(err).ToNot(HaveOccurred())

		request, response := computeInstanceUpdateLogFixtures(updatePath)
		originalRequest := proto.Clone(request)
		originalResponse := proto.Clone(response)
		responseTemplate := proto.Clone(response)

		invoker := func(_ context.Context, _ string, _, response any, _ *grpc.ClientConn,
			_ ...grpc.CallOption) error {
			proto.Reset(response.(proto.Message))
			proto.Merge(response.(proto.Message), responseTemplate)
			return nil
		}
		err = redactedInterceptor.UnaryClient(ctx, "/osac.public.v1.ComputeInstances/Update",
			request, response, conn, invoker)
		Expect(err).ToNot(HaveOccurred())

		Expect(buffer.String()).ToNot(ContainSubstring("mcp-owner@example.test"))
		Expect(buffer.String()).To(ContainSubstring(redactMark))
		messages := Parse(buffer)
		responseLogs := Find(messages, "Received unary response")
		Expect(responseLogs).To(HaveLen(1))
		responseBody, ok := responseLogs[0]["response"].(map[string]any)
		Expect(ok).To(BeTrue())
		object, ok := responseBody["object"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(object).To(HaveKeyWithValue("id", "compute-instance-fixture"))
		metadata, ok := object["metadata"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(metadata).To(HaveKeyWithValue("creator", redactMark))
		Expect(proto.Equal(request, originalRequest)).To(BeTrue())
		Expect(proto.Equal(response, originalResponse)).To(BeTrue())
	}

	It("Redacts creator addresses from power update response body logs", func() {
		expectLifecycleUpdateCreatorRedacted("spec.run_strategy")
	})

	It("Redacts creator addresses from restart update response body logs", func() {
		expectLifecycleUpdateCreatorRedacted("spec.restart_requested_at")
	})

	It("Redacts ComputeInstance catalog policies from unary client body logs", func() {
		redactedInterceptor, err := NewInterceptor().
			SetLogger(logger).
			SetBodies(true).
			SetRedact(true).
			Build()
		Expect(err).ToNot(HaveOccurred())

		request, response, err := computeInstanceCatalogItemLogFixtures()
		Expect(err).ToNot(HaveOccurred())
		originalRequest := proto.Clone(request)
		originalResponse := proto.Clone(response)
		responseTemplate := proto.Clone(response)

		invoker := func(_ context.Context, _ string, _, response any, _ *grpc.ClientConn,
			_ ...grpc.CallOption) error {
			proto.Reset(response.(proto.Message))
			proto.Merge(response.(proto.Message), responseTemplate)
			return nil
		}
		err = redactedInterceptor.UnaryClient(ctx, "/osac.public.v1.ComputeInstanceCatalogItems/Get",
			request, response, conn, invoker)
		Expect(err).ToNot(HaveOccurred())

		Expect(buffer.String()).ToNot(ContainSubstring("locked-template-parameter-fixture"))
		Expect(buffer.String()).ToNot(ContainSubstring("default-template-parameter-fixture"))
		Expect(buffer.String()).ToNot(ContainSubstring("inline-user-data-fixture"))
		Expect(buffer.String()).ToNot(ContainSubstring("customer.example.test/private-annotation"))
		Expect(buffer.String()).ToNot(ContainSubstring("catalog-item-annotation-fixture-value"))
		Expect(buffer.String()).ToNot(ContainSubstring("customer.example.test/catalog-private-label"))
		Expect(buffer.String()).ToNot(ContainSubstring("catalog-item-label-fixture-value"))

		messages := Parse(buffer)
		requestLogs := Find(messages, "Sending unary request")
		Expect(requestLogs).To(HaveLen(1))
		Expect(requestLogs[0]).To(HaveKey("request"))
		responseLogs := Find(messages, "Received unary response")
		Expect(responseLogs).To(HaveLen(1))
		responseBody, ok := responseLogs[0]["response"].(map[string]any)
		Expect(ok).To(BeTrue())
		object, ok := responseBody["object"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(object).To(HaveKeyWithValue("id", "catalog-item-fixture"))
		Expect(proto.Equal(request, originalRequest)).To(BeTrue())
		Expect(proto.Equal(response, originalResponse)).To(BeTrue())
	})

	It("Redacts ComputeInstance catalog policies from unary server body logs", func() {
		redactedInterceptor, err := NewInterceptor().
			SetLogger(logger).
			SetBodies(true).
			SetRedact(true).
			Build()
		Expect(err).ToNot(HaveOccurred())

		request, response, err := computeInstanceCatalogItemLogFixtures()
		Expect(err).ToNot(HaveOccurred())
		originalRequest := proto.Clone(request)
		originalResponse := proto.Clone(response)

		_, err = redactedInterceptor.UnaryServer(ctx, request,
			&grpc.UnaryServerInfo{FullMethod: "/osac.public.v1.ComputeInstanceCatalogItems/Get"},
			func(context.Context, any) (any, error) { return response, nil })
		Expect(err).ToNot(HaveOccurred())

		Expect(buffer.String()).ToNot(ContainSubstring("locked-template-parameter-fixture"))
		Expect(buffer.String()).ToNot(ContainSubstring("default-template-parameter-fixture"))
		Expect(buffer.String()).ToNot(ContainSubstring("inline-user-data-fixture"))
		Expect(buffer.String()).ToNot(ContainSubstring("customer.example.test/private-annotation"))
		Expect(buffer.String()).ToNot(ContainSubstring("catalog-item-annotation-fixture-value"))
		Expect(buffer.String()).ToNot(ContainSubstring("customer.example.test/catalog-private-label"))
		Expect(buffer.String()).ToNot(ContainSubstring("catalog-item-label-fixture-value"))

		messages := Parse(buffer)
		requestLogs := Find(messages, "Received unary request")
		Expect(requestLogs).To(HaveLen(1))
		Expect(requestLogs[0]).To(HaveKey("request"))
		responseLogs := Find(messages, "Sent unary response")
		Expect(responseLogs).To(HaveLen(1))
		responseBody, ok := responseLogs[0]["response"].(map[string]any)
		Expect(ok).To(BeTrue())
		object, ok := responseBody["object"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(object).To(HaveKeyWithValue("id", "catalog-item-fixture"))
		Expect(proto.Equal(request, originalRequest)).To(BeTrue())
		Expect(proto.Equal(response, originalResponse)).To(BeTrue())
	})
})

// computeInstanceLogFixtures returns public request and response bodies with private customer data.
func computeInstanceLogFixtures() (
	*publicv1.ComputeInstancesCreateRequest,
	*publicv1.ComputeInstancesCreateResponse,
	error,
) {
	fixtureTemplateParameter, err := anypb.New(wrapperspb.String("template-parameter-fixture-value"))
	if err != nil {
		return nil, nil, err
	}
	fixtureUserData := "user-data-fixture-value"
	spec := publicv1.ComputeInstanceSpec_builder{
		TemplateParameters: map[string]*anypb.Any{"fixture-param": fixtureTemplateParameter},
		UserDataSource:     publicv1.UserDataSource_builder{Inline: &fixtureUserData}.Build(),
	}.Build()
	metadata := publicv1.Metadata_builder{
		Labels: map[string]string{
			"customer.example.test/private-label": "compute-instance-label-fixture-value",
		},
		Annotations: map[string]string{
			"customer.example.test/private-annotation": "compute-instance-annotation-fixture-value",
		},
	}.Build()
	request := publicv1.ComputeInstancesCreateRequest_builder{
		Object: publicv1.ComputeInstance_builder{
			Id: "compute-instance-fixture", Metadata: metadata, Spec: spec,
		}.Build(),
	}.Build()
	response := publicv1.ComputeInstancesCreateResponse_builder{
		Object: publicv1.ComputeInstance_builder{
			Id:       "compute-instance-fixture",
			Metadata: metadata,
			Spec:     spec,
			Status: publicv1.ComputeInstanceStatus_builder{
				InternalIpAddress: "10.1.2.3",
				ExternalIpAddress: "192.0.2.4",
				ComputeNetworkAttachmentStatuses: []*publicv1.ComputeNetworkAttachmentStatus{
					publicv1.ComputeNetworkAttachmentStatus_builder{
						SubnetRef: "subnet-private-id",
						IpAddress: "10.1.2.5",
					}.Build(),
				},
			}.Build(),
		}.Build(),
	}.Build()
	return request, response, nil
}

func computeInstanceUpdateLogFixtures(updatePath string) (
	*publicv1.ComputeInstancesUpdateRequest,
	*publicv1.ComputeInstancesUpdateResponse,
) {
	request := publicv1.ComputeInstancesUpdateRequest_builder{
		Object: publicv1.ComputeInstance_builder{
			Id: "compute-instance-fixture",
			Metadata: publicv1.Metadata_builder{
				Version: 1,
			}.Build(),
		}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{updatePath}},
		Lock:       true,
	}.Build()
	response := publicv1.ComputeInstancesUpdateResponse_builder{
		Object: publicv1.ComputeInstance_builder{
			Id: "compute-instance-fixture",
			Metadata: publicv1.Metadata_builder{
				Creator: "mcp-owner@example.test",
				Version: 2,
			}.Build(),
		}.Build(),
	}.Build()
	return request, response
}

func expectComputeInstanceBodyLogs(buffer *bytes.Buffer, requestEvent, responseEvent string) {
	messages := Parse(buffer)
	requestLogs := Find(messages, requestEvent)
	Expect(requestLogs).To(HaveLen(1))
	requestBody, ok := requestLogs[0]["request"].(map[string]any)
	Expect(ok).To(BeTrue())
	requestObject, ok := requestBody["object"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(requestObject).To(HaveKeyWithValue("id", "compute-instance-fixture"))

	responseLogs := Find(messages, responseEvent)
	Expect(responseLogs).To(HaveLen(1))
	responseBody, ok := responseLogs[0]["response"].(map[string]any)
	Expect(ok).To(BeTrue())
	responseObject, ok := responseBody["object"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(responseObject).To(HaveKeyWithValue("id", "compute-instance-fixture"))
}

// computeInstanceCatalogItemLogFixtures returns catalog policies with fake sensitive values.
func computeInstanceCatalogItemLogFixtures() (
	*publicv1.ComputeInstanceCatalogItemsGetRequest,
	*publicv1.ComputeInstanceCatalogItemsGetResponse,
	error,
) {
	lockedTemplateParameter, err := anypb.New(wrapperspb.String("locked-template-parameter-fixture"))
	if err != nil {
		return nil, nil, err
	}
	defaultTemplateParameter, err := anypb.New(wrapperspb.String("default-template-parameter-fixture"))
	if err != nil {
		return nil, nil, err
	}
	inlineUserData := "inline-user-data-fixture"
	metadata := publicv1.Metadata_builder{
		Labels: map[string]string{
			"customer.example.test/catalog-private-label": "catalog-item-label-fixture-value",
		},
		Annotations: map[string]string{
			"customer.example.test/private-annotation": "catalog-item-annotation-fixture-value",
		},
	}.Build()
	item := publicv1.ComputeInstanceCatalogItem_builder{
		Id:       "catalog-item-fixture",
		Metadata: metadata,
		TemplateParameters: map[string]*publicv1.TemplateParameterPolicy{
			"locked-param": publicv1.TemplateParameterPolicy_builder{
				Locked: lockedTemplateParameter,
			}.Build(),
			"default-param": publicv1.TemplateParameterPolicy_builder{
				Editable: publicv1.EditableTemplateParameter_builder{
					DefaultValue: defaultTemplateParameter,
				}.Build(),
			}.Build(),
		},
		Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
			UserDataSource: publicv1.UserDataSourceFieldPolicy_builder{
				Editable: publicv1.EditableUserDataSourceField_builder{
					DefaultValue: publicv1.UserDataSource_builder{Inline: &inlineUserData}.Build(),
				}.Build(),
			}.Build(),
		}.Build(),
	}.Build()
	return publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: item.GetId()}.Build(),
		publicv1.ComputeInstanceCatalogItemsGetResponse_builder{Object: item}.Build(), nil
}

type mockClientStream struct {
	ctx context.Context
}

func (m *mockClientStream) Context() context.Context {
	return m.ctx
}

func (m *mockClientStream) Header() (result metadata.MD, err error) {
	result = metadata.New(map[string]string{
		"my_header": "my_value",
	})
	return
}

func (m *mockClientStream) Trailer() metadata.MD {
	return metadata.New(map[string]string{
		"my_trailer": "my_value",
	})
}

func (m *mockClientStream) CloseSend() error {
	return nil
}

func (m *mockClientStream) SendMsg(message any) error {
	return nil
}

func (m *mockClientStream) RecvMsg(message any) error {
	return io.EOF
}
