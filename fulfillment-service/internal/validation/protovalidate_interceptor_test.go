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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("Protovalidate interceptor", func() {
	var interceptor *ProtovalidateInterceptor

	Describe("Creation", func() {
		It("Can be built if logger is set", func() {
			result, err := NewProtovalidateInterceptor().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(result).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			result, err := NewProtovalidateInterceptor().
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(result).To(BeNil())
		})
	})

	Describe("Unary server validation", func() {
		BeforeEach(func() {
			var err error
			interceptor, err = NewProtovalidateInterceptor().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Passes valid requests to the handler", func() {
			// Create a valid Metadata message:
			validMetadata := &publicv1.Metadata{
				Name: "valid-name",
			}

			// Mock handler that tracks if it was called:
			handlerCalled := false
			mockHandler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "response", nil
			}

			// Call the interceptor:
			response, err := interceptor.UnaryServer(
				context.Background(),
				validMetadata,
				&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
				mockHandler,
			)

			// Verify handler was called and no error occurred:
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(response).To(Equal("response"))
		})

		It("Rejects requests with invalid name (too long)", func() {
			// Create Metadata with name > 63 chars:
			invalidMetadata := &publicv1.Metadata{
				Name: "this-name-is-way-too-long-and-exceeds-the-sixty-three-character-limit",
			}

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid request")
				return nil, nil
			}

			// Call the interceptor:
			response, err := interceptor.UnaryServer(
				context.Background(),
				invalidMetadata,
				&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
				mockHandler,
			)

			// Verify validation error:
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("validation failed"))
		})

		It("Rejects requests with invalid name pattern (uppercase)", func() {
			// Create Metadata with uppercase letters (invalid):
			invalidMetadata := &publicv1.Metadata{
				Name: "MyCluster",
			}

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid request")
				return nil, nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				invalidMetadata,
				&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("validation failed"))
		})

		It("Rejects requests with invalid name pattern (underscore)", func() {
			// Create Metadata with underscore (invalid):
			invalidMetadata := &publicv1.Metadata{
				Name: "my_cluster",
			}

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid request")
				return nil, nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				invalidMetadata,
				&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("validation failed"))
		})

		It("Rejects requests with name starting with hyphen", func() {
			// Create Metadata with leading hyphen (invalid):
			invalidMetadata := &publicv1.Metadata{
				Name: "-mycluster",
			}

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid request")
				return nil, nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				invalidMetadata,
				&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("validation failed"))
		})

		It("Rejects requests with name ending with hyphen", func() {
			// Create Metadata with trailing hyphen (invalid):
			invalidMetadata := &publicv1.Metadata{
				Name: "mycluster-",
			}

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid request")
				return nil, nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				invalidMetadata,
				&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("validation failed"))
		})

		It("Rejects requests with label keys that are too long", func() {
			// Create Metadata with label key > 316 chars:
			longKey := ""
			for i := 0; i < 320; i++ {
				longKey = longKey + "a"
			}
			invalidMetadata := &publicv1.Metadata{
				Name: "valid-name",
				Labels: map[string]string{
					longKey: "value",
				},
			}

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid request")
				return nil, nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				invalidMetadata,
				&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		It("Rejects empty name (mandatory field)", func() {
			invalidMetadata := &publicv1.Metadata{
				Name: "",
			}

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid request")
				return nil, nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				invalidMetadata,
				&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		It("Accepts single-character name", func() {
			validMetadata := &publicv1.Metadata{
				Name: "a",
			}

			handlerCalled := false
			mockHandler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "response", nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				validMetadata,
				&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(response).To(Equal("response"))
		})

		It("Accepts 63-character name (max length)", func() {
			validMetadata := &publicv1.Metadata{
				Name: "a23456789012345678901234567890123456789012345678901234567890123",
			}

			handlerCalled := false
			mockHandler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "response", nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				validMetadata,
				&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(response).To(Equal("response"))
		})

		It("Rejects 64-character name (exceeds max length)", func() {
			invalidMetadata := &publicv1.Metadata{
				Name: "a234567890123456789012345678901234567890123456789012345678901234",
			}

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid request")
				return nil, nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				invalidMetadata,
				&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		It("Accepts valid labels", func() {
			validMetadata := &publicv1.Metadata{
				Name: "valid-name",
				Labels: map[string]string{
					"key1":                    "value1",
					"example.com/key2":        "value2",
					"subdomain.example.com/k": "v",
				},
			}

			handlerCalled := false
			mockHandler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "response", nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				validMetadata,
				&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(response).To(Equal("response"))
		})

		DescribeTable("Accepts display_name and description within length limits",
			func(msg proto.Message) {
				handlerCalled := false
				mockHandler := func(ctx context.Context, req any) (any, error) {
					handlerCalled = true
					return "response", nil
				}

				response, err := interceptor.UnaryServer(
					context.Background(),
					msg,
					&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
					mockHandler,
				)

				Expect(err).ToNot(HaveOccurred())
				Expect(handlerCalled).To(BeTrue())
				Expect(response).To(Equal("response"))
			},
			Entry("public max lengths", &publicv1.Metadata{
				Name:        "valid-name",
				DisplayName: strings.Repeat("a", 63),
				Description: strings.Repeat("b", 256),
			}),
			Entry("public empty values", &publicv1.Metadata{
				Name:        "valid-name",
				DisplayName: "",
				Description: "",
			}),
			Entry("private max lengths", &privatev1.Metadata{
				Name:        "valid-name",
				DisplayName: strings.Repeat("a", 63),
				Description: strings.Repeat("b", 256),
			}),
			Entry("private empty values", &privatev1.Metadata{
				Name:        "valid-name",
				DisplayName: "",
				Description: "",
			}),
		)

		DescribeTable("Rejects over-long display_name and description",
			func(msg proto.Message) {
				mockHandler := func(ctx context.Context, req any) (any, error) {
					Fail("Handler should not be called for invalid request")
					return nil, nil
				}

				response, err := interceptor.UnaryServer(
					context.Background(),
					msg,
					&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
					mockHandler,
				)

				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("validation failed"))
			},
			Entry("public display_name too long", &publicv1.Metadata{
				Name:        "valid-name",
				DisplayName: strings.Repeat("a", 64),
			}),
			Entry("public description too long", &publicv1.Metadata{
				Name:        "valid-name",
				Description: strings.Repeat("b", 257),
			}),
			Entry("private display_name too long", &privatev1.Metadata{
				Name:        "valid-name",
				DisplayName: strings.Repeat("a", 64),
			}),
			Entry("private description too long", &privatev1.Metadata{
				Name:        "valid-name",
				Description: strings.Repeat("b", 257),
			}),
		)
	})

	Describe("Project name validation", func() {
		BeforeEach(func() {
			var err error
			interceptor, err = NewProtovalidateInterceptor().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects project with empty name", func() {
			project := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "",
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid request")
				return nil, nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				project,
				&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		It("Accepts project with single DNS label name", func() {
			project := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "myproject",
				}.Build(),
			}.Build()

			handlerCalled := false
			mockHandler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "response", nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				project,
				&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(response).To(Equal("response"))
		})

		It("Accepts project with dot-separated name", func() {
			project := privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "team.dev",
				}.Build(),
			}.Build()

			handlerCalled := false
			mockHandler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "response", nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				project,
				&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(response).To(Equal("response"))
		})
	})

	Describe("StorageBackend spec validation", func() {
		BeforeEach(func() {
			var err error
			interceptor, err = NewProtovalidateInterceptor().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects create request with missing spec", func() {
			request := privatev1.StorageBackendsCreateRequest_builder{
				Object: privatev1.StorageBackend_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-backend",
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid request")
				return nil, nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.private.v1.StorageBackendsService/Create"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("validation failed"))
		})

		It("Rejects create request with empty provider", func() {
			request := privatev1.StorageBackendsCreateRequest_builder{
				Object: privatev1.StorageBackend_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-backend",
					}.Build(),
					Spec: privatev1.StorageBackendSpec_builder{
						Provider: "",
						Endpoint: "https://storage.example.com",
						Credentials: privatev1.StorageBackendCredentials_builder{
							Username: "admin",
							Password: "secret",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid request")
				return nil, nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.private.v1.StorageBackendsService/Create"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("validation failed"))
		})

		It("Rejects create request with empty endpoint", func() {
			request := privatev1.StorageBackendsCreateRequest_builder{
				Object: privatev1.StorageBackend_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-backend",
					}.Build(),
					Spec: privatev1.StorageBackendSpec_builder{
						Provider: "ceph",
						Endpoint: "",
						Credentials: privatev1.StorageBackendCredentials_builder{
							Username: "admin",
							Password: "secret",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid request")
				return nil, nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.private.v1.StorageBackendsService/Create"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("validation failed"))
		})

		It("Rejects create request with missing credentials", func() {
			request := privatev1.StorageBackendsCreateRequest_builder{
				Object: privatev1.StorageBackend_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-backend",
					}.Build(),
					Spec: privatev1.StorageBackendSpec_builder{
						Provider: "ceph",
						Endpoint: "https://storage.example.com",
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid request")
				return nil, nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.private.v1.StorageBackendsService/Create"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("validation failed"))
		})

		It("Rejects create request with empty credentials username", func() {
			request := privatev1.StorageBackendsCreateRequest_builder{
				Object: privatev1.StorageBackend_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-backend",
					}.Build(),
					Spec: privatev1.StorageBackendSpec_builder{
						Provider: "ceph",
						Endpoint: "https://storage.example.com",
						Credentials: privatev1.StorageBackendCredentials_builder{
							Username: "",
							Password: "secret",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid request")
				return nil, nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.private.v1.StorageBackendsService/Create"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("validation failed"))
		})

		It("Rejects create request with empty credentials password", func() {
			request := privatev1.StorageBackendsCreateRequest_builder{
				Object: privatev1.StorageBackend_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-backend",
					}.Build(),
					Spec: privatev1.StorageBackendSpec_builder{
						Provider: "ceph",
						Endpoint: "https://storage.example.com",
						Credentials: privatev1.StorageBackendCredentials_builder{
							Username: "admin",
							Password: "",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid request")
				return nil, nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.private.v1.StorageBackendsService/Create"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("validation failed"))
		})

		It("Accepts valid create request", func() {
			request := privatev1.StorageBackendsCreateRequest_builder{
				Object: privatev1.StorageBackend_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-backend",
					}.Build(),
					Spec: privatev1.StorageBackendSpec_builder{
						Provider: "ceph",
						Endpoint: "https://storage.example.com",
						Credentials: privatev1.StorageBackendCredentials_builder{
							Username: "admin",
							Password: "secret",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			handlerCalled := false
			validHandler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "response", nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.private.v1.StorageBackendsService/Create"},
				validHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(response).To(Equal("response"))
		})
	})

	Describe("InstanceType spec validation", func() {
		BeforeEach(func() {
			var err error
			interceptor, err = NewProtovalidateInterceptor().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects create request with zero cores", func() {
			request := privatev1.InstanceTypesCreateRequest_builder{
				Object: privatev1.InstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-type",
					}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:     0,
						MemoryGib: 16,
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid request")
				return nil, nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.private.v1.InstanceTypesService/Create"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("validation failed"))
		})

		It("Rejects create request with zero memory_gib", func() {
			request := privatev1.InstanceTypesCreateRequest_builder{
				Object: privatev1.InstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-type",
					}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:     4,
						MemoryGib: 0,
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid request")
				return nil, nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.private.v1.InstanceTypesService/Create"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("validation failed"))
		})

		It("Accepts valid create request", func() {
			request := privatev1.InstanceTypesCreateRequest_builder{
				Object: privatev1.InstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-type",
					}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Cores:     4,
						MemoryGib: 16,
					}.Build(),
				}.Build(),
			}.Build()

			handlerCalled := false
			validHandler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "response", nil
			}

			response, err := interceptor.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.private.v1.InstanceTypesService/Create"},
				validHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(response).To(Equal("response"))
		})
	})

	Describe("Stream server validation", func() {
		BeforeEach(func() {
			var err error
			interceptor, err = NewProtovalidateInterceptor().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Validates messages received from stream", func() {
			// Create a mock stream:
			mockStream := &mockServerStream{
				recvFunc: func(m any) error {
					// Simulate receiving an invalid message:
					metadata := m.(*publicv1.Metadata)
					metadata.Name = "this-name-is-way-too-long-and-exceeds-the-sixty-three-character-limit"
					return nil
				},
			}

			handlerCalled := false
			mockHandler := func(srv any, stream grpc.ServerStream) error {
				handlerCalled = true
				// Try to receive a message (which should fail validation):
				var msg publicv1.Metadata
				err := stream.RecvMsg(&msg)
				return err
			}

			err := interceptor.StreamServer(
				nil,
				mockStream,
				&grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"},
				mockHandler,
			)

			// Verify the handler was called and validation failed:
			Expect(handlerCalled).To(BeTrue())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		})
	})
})

// mockServerStream implements grpc.ServerStream for testing.
type mockServerStream struct {
	grpc.ServerStream
	recvFunc func(m any) error
}

func (m *mockServerStream) Context() context.Context {
	return context.Background()
}

func (m *mockServerStream) RecvMsg(msg any) error {
	if m.recvFunc != nil {
		return m.recvFunc(msg)
	}
	return nil
}

func (m *mockServerStream) SendMsg(msg any) error {
	return nil
}

func (m *mockServerStream) SendHeader(md metadata.MD) error {
	return nil
}

func (m *mockServerStream) SetHeader(md metadata.MD) error {
	return nil
}

func (m *mockServerStream) SetTrailer(md metadata.MD) {
}
