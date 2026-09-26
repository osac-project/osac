/*
Copyright (c) 2026 Red Hat Inc.

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

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("SelfSubjectAccessReviews Server", func() {
	var (
		mockCtrl      *gomock.Controller
		mockEvaluator *auth.MockAuthorizationEvaluator
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		mockEvaluator = auth.NewMockAuthorizationEvaluator(mockCtrl)
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	Describe("Public Server", func() {
		Describe("Builder", func() {
			It("Can be built with all required parameters", func() {
				server, err := NewSelfSubjectAccessReviewsServer().
					SetLogger(logger).
					SetEvaluator(mockEvaluator).
					Build()
				Expect(err).ToNot(HaveOccurred())
				Expect(server).ToNot(BeNil())
			})

			It("Fails if logger is not set", func() {
				server, err := NewSelfSubjectAccessReviewsServer().
					SetEvaluator(mockEvaluator).
					Build()
				Expect(err).To(MatchError("logger is mandatory"))
				Expect(server).To(BeNil())
			})

			It("Fails if evaluator is not set", func() {
				server, err := NewSelfSubjectAccessReviewsServer().
					SetLogger(logger).
					Build()
				Expect(err).To(MatchError("evaluator is mandatory"))
				Expect(server).To(BeNil())
			})
		})

		Describe("Create", func() {
			var (
				publicServer *SelfSubjectAccessReviewsServer
				testCtx      context.Context
				token        *jwt.Token
			)

			BeforeEach(func() {
				var err error
				publicServer, err = NewSelfSubjectAccessReviewsServer().
					SetLogger(logger).
					SetEvaluator(mockEvaluator).
					Build()
				Expect(err).ToNot(HaveOccurred())

				token = &jwt.Token{
					Valid: true,
					Claims: jwt.MapClaims{
						"preferred_username": "test-user",
						"groups":             []any{"developers", "users"},
					},
				}
				testCtx = auth.ContextWithToken(context.Background(), token)
			})

			It("Returns allowed decision for authorized action", func() {
				mockEvaluator.EXPECT().
					Evaluate(gomock.Any(), gomock.Any(), "/osac.public.v1.Clusters/Create").
					Return(&auth.AuthzDecision{
						Allowed: true,
						Reason:  "user has permission",
					}, nil)

				request := publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: publicv1.SelfSubjectAccessReview_builder{
						Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.Clusters",
							Method:  "Create",
						}.Build(),
					}.Build(),
				}.Build()

				response, err := publicServer.Create(testCtx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				Expect(response.GetObject().GetStatus().GetAllowed()).To(BeTrue())
				Expect(response.GetObject().GetStatus().GetReason()).To(Equal("user has permission"))
			})

			It("Returns denied decision for unauthorized action", func() {
				mockEvaluator.EXPECT().
					Evaluate(gomock.Any(), gomock.Any(), "/osac.public.v1.Clusters/Delete").
					Return(&auth.AuthzDecision{
						Allowed: false,
						Reason:  "user lacks delete permission",
					}, nil)

				request := publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: publicv1.SelfSubjectAccessReview_builder{
						Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.Clusters",
							Method:  "Delete",
						}.Build(),
					}.Build(),
				}.Build()

				response, err := publicServer.Create(testCtx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				Expect(response.GetObject().GetStatus().GetAllowed()).To(BeFalse())
				Expect(response.GetObject().GetStatus().GetReason()).To(Equal("user lacks delete permission"))
			})

			It("Preserves metadata in the response", func() {
				mockEvaluator.EXPECT().
					Evaluate(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&auth.AuthzDecision{Allowed: true}, nil)

				request := publicv1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: publicv1.SelfSubjectAccessReview_builder{
						Metadata: publicv1.Metadata_builder{
							Name: "test-review",
							Annotations: map[string]string{
								"osac.openshift.io/tenant": "my-tenant",
							},
						}.Build(),
						Spec: publicv1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.Clusters",
							Method:  "List",
						}.Build(),
					}.Build(),
				}.Build()

				response, err := publicServer.Create(testCtx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetMetadata().GetName()).To(Equal("test-review"))
				Expect(response.GetObject().GetMetadata().GetAnnotations()).To(HaveKeyWithValue("osac.openshift.io/tenant", "my-tenant"))
			})
		})
	})

	Describe("Private Server", func() {
		Describe("Builder", func() {
			It("Can be built with all required parameters", func() {
				server, err := NewPrivateSelfSubjectAccessReviewsServer().
					SetLogger(logger).
					SetEvaluator(mockEvaluator).
					Build()
				Expect(err).ToNot(HaveOccurred())
				Expect(server).ToNot(BeNil())
			})

			It("Fails if logger is not set", func() {
				server, err := NewPrivateSelfSubjectAccessReviewsServer().
					SetEvaluator(mockEvaluator).
					Build()
				Expect(err).To(MatchError("logger is mandatory"))
				Expect(server).To(BeNil())
			})

			It("Fails if evaluator is not set", func() {
				server, err := NewPrivateSelfSubjectAccessReviewsServer().
					SetLogger(logger).
					Build()
				Expect(err).To(MatchError("evaluator is mandatory"))
				Expect(server).To(BeNil())
			})
		})

		Describe("Create", func() {
			var (
				privateServer *PrivateSelfSubjectAccessReviewsServer
				testCtx       context.Context
				token         *jwt.Token
			)

			BeforeEach(func() {
				var err error
				privateServer, err = NewPrivateSelfSubjectAccessReviewsServer().
					SetLogger(logger).
					SetEvaluator(mockEvaluator).
					Build()
				Expect(err).ToNot(HaveOccurred())

				token = &jwt.Token{
					Valid: true,
					Claims: jwt.MapClaims{
						"preferred_username": "test-user",
						"groups":             []any{"developers", "users"},
					},
				}
				testCtx = auth.ContextWithToken(context.Background(), token)
			})

			It("Returns allowed decision for authorized action", func() {
				mockEvaluator.EXPECT().
					Evaluate(gomock.Any(), gomock.Any(), "/osac.public.v1.Clusters/Create").
					Return(&auth.AuthzDecision{
						Allowed: true,
						Reason:  "user has permission",
					}, nil)

				request := privatev1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: privatev1.SelfSubjectAccessReview_builder{
						Spec: privatev1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.Clusters",
							Method:  "Create",
						}.Build(),
					}.Build(),
				}.Build()

				response, err := privateServer.Create(testCtx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				Expect(response.GetObject().GetStatus().GetAllowed()).To(BeTrue())
				Expect(response.GetObject().GetStatus().GetReason()).To(Equal("user has permission"))
			})

			It("Returns denied decision for unauthorized action", func() {
				mockEvaluator.EXPECT().
					Evaluate(gomock.Any(), gomock.Any(), "/osac.public.v1.Clusters/Delete").
					Return(&auth.AuthzDecision{
						Allowed: false,
						Reason:  "user lacks delete permission",
					}, nil)

				request := privatev1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: privatev1.SelfSubjectAccessReview_builder{
						Spec: privatev1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.Clusters",
							Method:  "Delete",
						}.Build(),
					}.Build(),
				}.Build()

				response, err := privateServer.Create(testCtx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				Expect(response.GetObject().GetStatus().GetAllowed()).To(BeFalse())
				Expect(response.GetObject().GetStatus().GetReason()).To(Equal("user lacks delete permission"))
			})

			It("Returns unauthenticated error when token is missing", func() {
				emptyCtx := context.Background()

				request := privatev1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: privatev1.SelfSubjectAccessReview_builder{
						Spec: privatev1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.Clusters",
							Method:  "Create",
						}.Build(),
					}.Build(),
				}.Build()

				response, err := privateServer.Create(emptyCtx, request)
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.Unauthenticated))
				Expect(status.Message()).To(Equal("authentication required"))
			})

			It("Returns invalid argument error when object is missing", func() {
				request := privatev1.SelfSubjectAccessReviewsCreateRequest_builder{}.Build()

				response, err := privateServer.Create(testCtx, request)
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal("object is required"))
			})

			It("Returns invalid argument error when spec is missing", func() {
				request := privatev1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: privatev1.SelfSubjectAccessReview_builder{}.Build(),
				}.Build()

				response, err := privateServer.Create(testCtx, request)
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal("spec is required"))
			})

			It("Returns invalid argument error when service is missing", func() {
				request := privatev1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: privatev1.SelfSubjectAccessReview_builder{
						Spec: privatev1.SelfSubjectAccessReviewSpec_builder{
							Method: "Create",
						}.Build(),
					}.Build(),
				}.Build()

				response, err := privateServer.Create(testCtx, request)
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal("service is required"))
			})

			It("Returns invalid argument error when method is missing", func() {
				request := privatev1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: privatev1.SelfSubjectAccessReview_builder{
						Spec: privatev1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.Clusters",
						}.Build(),
					}.Build(),
				}.Build()

				response, err := privateServer.Create(testCtx, request)
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal("method is required"))
			})

			It("Returns invalid argument error for unknown service", func() {
				request := privatev1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: privatev1.SelfSubjectAccessReview_builder{
						Spec: privatev1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.NonExistentService",
							Method:  "Create",
						}.Build(),
					}.Build(),
				}.Build()

				response, err := privateServer.Create(testCtx, request)
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("unknown service"))
			})

			It("Returns invalid argument error for unknown method", func() {
				request := privatev1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: privatev1.SelfSubjectAccessReview_builder{
						Spec: privatev1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.Clusters",
							Method:  "NonExistentMethod",
						}.Build(),
					}.Build(),
				}.Build()

				response, err := privateServer.Create(testCtx, request)
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("method NonExistentMethod not supported"))
			})

			It("Prevents recursive check on SelfSubjectAccessReviews service", func() {
				request := privatev1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: privatev1.SelfSubjectAccessReview_builder{
						Spec: privatev1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.SelfSubjectAccessReviews",
							Method:  "Create",
						}.Build(),
					}.Build(),
				}.Build()

				response, err := privateServer.Create(testCtx, request)
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal("cannot check permissions on SelfSubjectAccessReviews service"))
			})

			It("Extracts tenant from metadata annotations", func() {
				mockEvaluator.EXPECT().
					Evaluate(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, authCtx *auth.AuthContext, method string) (*auth.AuthzDecision, error) {
						Expect(authCtx.Tenant).To(Equal("my-tenant"))
						return &auth.AuthzDecision{Allowed: true}, nil
					})

				request := privatev1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: privatev1.SelfSubjectAccessReview_builder{
						Metadata: privatev1.Metadata_builder{
							Annotations: map[string]string{
								"osac.openshift.io/tenant": "my-tenant",
							},
						}.Build(),
						Spec: privatev1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.Clusters",
							Method:  "List",
						}.Build(),
					}.Build(),
				}.Build()

				response, err := privateServer.Create(testCtx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
			})

			It("Extracts auth context from JWT token", func() {
				token := &jwt.Token{
					Valid: true,
					Claims: jwt.MapClaims{
						"preferred_username": "jane-doe",
						"groups":             []any{"admins", "developers"},
						"organization":       []any{"red-hat"},
					},
				}
				testCtx := auth.ContextWithToken(context.Background(), token)

				mockEvaluator.EXPECT().
					Evaluate(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, authCtx *auth.AuthContext, method string) (*auth.AuthzDecision, error) {
						Expect(authCtx.Username).To(Equal("jane-doe"))
						Expect(authCtx.Groups).To(ConsistOf("admins", "developers"))
						Expect(authCtx.Organization).To(ConsistOf("red-hat"))
						return &auth.AuthzDecision{Allowed: true}, nil
					})

				request := privatev1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: privatev1.SelfSubjectAccessReview_builder{
						Spec: privatev1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.Clusters",
							Method:  "Create",
						}.Build(),
					}.Build(),
				}.Build()

				response, err := privateServer.Create(testCtx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
			})

			It("Handles service account authentication", func() {
				token := &jwt.Token{
					Valid: true,
					Claims: jwt.MapClaims{
						"sub":           "system:serviceaccount:osac:test-sa",
						"groups":        []any{"system:serviceaccounts", "system:serviceaccounts:osac"},
						"kubernetes.io": map[string]any{"namespace": "osac"},
					},
				}
				testCtx := auth.ContextWithToken(context.Background(), token)

				mockEvaluator.EXPECT().
					Evaluate(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(ctx context.Context, authCtx *auth.AuthContext, method string) (*auth.AuthzDecision, error) {
						Expect(authCtx.AuthMethod).To(Equal("serviceaccount"))
						Expect(authCtx.Username).To(Equal("system:serviceaccount:osac:test-sa"))
						return &auth.AuthzDecision{Allowed: true}, nil
					})

				request := privatev1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: privatev1.SelfSubjectAccessReview_builder{
						Spec: privatev1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.Clusters",
							Method:  "List",
						}.Build(),
					}.Build(),
				}.Build()

				response, err := privateServer.Create(testCtx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
			})

			It("Preserves spec and metadata in response", func() {
				mockEvaluator.EXPECT().
					Evaluate(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&auth.AuthzDecision{
						Allowed: true,
						Reason:  "test reason",
					}, nil)

				request := privatev1.SelfSubjectAccessReviewsCreateRequest_builder{
					Object: privatev1.SelfSubjectAccessReview_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-review",
							Annotations: map[string]string{
								"custom": "annotation",
							},
						}.Build(),
						Spec: privatev1.SelfSubjectAccessReviewSpec_builder{
							Service: "osac.public.v1.Clusters",
							Method:  "Create",
						}.Build(),
					}.Build(),
				}.Build()

				response, err := privateServer.Create(testCtx, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetMetadata().GetName()).To(Equal("test-review"))
				Expect(response.GetObject().GetMetadata().GetAnnotations()).To(HaveKeyWithValue("custom", "annotation"))
				Expect(response.GetObject().GetSpec().GetService()).To(Equal("osac.public.v1.Clusters"))
				Expect(response.GetObject().GetSpec().GetMethod()).To(Equal("Create"))
			})
		})
	})
})
