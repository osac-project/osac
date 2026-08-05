/*
Copyright (c) 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package references

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	testsv1 "github.com/osac-project/fulfillment-service/internal/api/osac/tests/v1"
)

var _ = Describe("Reference validator", func() {
	var validator *ReferenceValidator

	Describe("Builder", func() {
		It("Succeeds when logger is set", func() {
			result, err := NewReferenceValidator().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(result).ToNot(BeNil())
		})

		It("Fails when logger is not set", func() {
			result, err := NewReferenceValidator().
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(result).To(BeNil())
		})
	})

	Describe("Sealed after serving", func() {
		It("Panics when Register is called after UnaryServer", func() {
			validator, err := NewReferenceValidator().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())

			validator.Register("osac.tests.v1.TestTargetReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				return &ResolvedRef{ID: "id-1", Name: name}, nil
			})

			_, _ = validator.UnaryServer(
				context.Background(),
				"not-a-proto",
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Get"},
				func(ctx context.Context, req any) (any, error) { return "ok", nil },
			)

			Expect(func() {
				validator.Register("osac.tests.v1.TestOtherTargetLocalReference", func(
					ctx context.Context, tenant, project, id, name string,
				) (*ResolvedRef, error) {
					return nil, nil
				})
			}).To(PanicWith(ContainSubstring("Register called after interceptor started serving")))
		})
	})

	Describe("Method filtering", func() {
		BeforeEach(func() {
			var err error
			validator, err = NewReferenceValidator().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Validates Create requests", func() {
			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{}.Build(),
				}.Build(),
			}.Build()

			handlerCalled := false
			mockHandler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "response", nil
			}

			response, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(response).To(Equal("response"))
		})

		It("Validates Update requests", func() {
			request := testsv1.UpdateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{}.Build(),
				}.Build(),
			}.Build()

			handlerCalled := false
			mockHandler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "response", nil
			}

			response, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Update"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(response).To(Equal("response"))
		})

		It("Passes through Get requests without validation", func() {
			handlerCalled := false
			mockHandler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "response", nil
			}

			response, err := validator.UnaryServer(
				context.Background(),
				"not-a-proto-message",
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Get"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(response).To(Equal("response"))
		})

		It("Passes through List requests without validation", func() {
			handlerCalled := false
			mockHandler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "response", nil
			}

			response, err := validator.UnaryServer(
				context.Background(),
				"anything",
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/List"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(response).To(Equal("response"))
		})

		It("Passes through Delete requests without validation", func() {
			handlerCalled := false
			mockHandler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "response", nil
			}

			response, err := validator.UnaryServer(
				context.Background(),
				"anything",
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Delete"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(response).To(Equal("response"))
		})

		It("Passes through Signal requests without validation", func() {
			handlerCalled := false
			mockHandler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "response", nil
			}

			response, err := validator.UnaryServer(
				context.Background(),
				"anything",
				&grpc.UnaryServerInfo{FullMethod: "/osac.private.v1.TestService/Signal"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(response).To(Equal("response"))
		})
	})

	Describe("Field discovery", func() {
		BeforeEach(func() {
			var err error
			validator, err = NewReferenceValidator().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Discovers simple reference fields in spec", func() {
			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						Target: testsv1.TestTargetReference_builder{
							Name: "my-target",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			refs := discoverReferenceFields(request)

			Expect(refs).To(ContainElement(HaveField("FullName",
				protoreflect.FullName("osac.tests.v1.TestTargetReference"))))
		})

		It("Discovers local reference fields", func() {
			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						LocalTarget: testsv1.TestTargetLocalReference_builder{
							Name: "my-local-target",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			refs := discoverReferenceFields(request)

			Expect(refs).To(ContainElement(HaveField("FullName",
				protoreflect.FullName("osac.tests.v1.TestTargetLocalReference"))))
		})

		It("Discovers nested reference fields", func() {
			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						Attachment: testsv1.TestRefAttachment_builder{
							Subnet: testsv1.TestTargetLocalReference_builder{
								Name: "my-subnet",
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			refs := discoverReferenceFields(request)

			Expect(refs).To(ContainElement(HaveField("FullName",
				protoreflect.FullName("osac.tests.v1.TestTargetLocalReference"))))
		})

		It("Discovers repeated reference fields", func() {
			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						OtherTargets: []*testsv1.TestOtherTargetLocalReference{
							testsv1.TestOtherTargetLocalReference_builder{
								Name: "target-1",
							}.Build(),
							testsv1.TestOtherTargetLocalReference_builder{
								Name: "target-2",
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build()

			refs := discoverReferenceFields(request)

			otherTargetCount := 0
			for _, ref := range refs {
				if ref.FullName == "osac.tests.v1.TestOtherTargetLocalReference" {
					otherTargetCount++
				}
			}
			Expect(otherTargetCount).To(Equal(2))
		})

		It("Discovers nested repeated reference fields", func() {
			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						Attachment: testsv1.TestRefAttachment_builder{
							SecurityGroups: []*testsv1.TestOtherTargetLocalReference{
								testsv1.TestOtherTargetLocalReference_builder{
									Name: "sg-1",
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			refs := discoverReferenceFields(request)

			Expect(refs).To(ContainElement(HaveField("FullName",
				protoreflect.FullName("osac.tests.v1.TestOtherTargetLocalReference"))))
		})

		It("Discovers oneof reference fields", func() {
			request := testsv1.CreateTestResourceWithOneofRequest_builder{
				Object: testsv1.TestResourceWithOneof_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefOneofSpec_builder{
						ComputeInstance: testsv1.TestTargetLocalReference_builder{
							Name: "my-instance",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			refs := discoverReferenceFields(request)

			Expect(refs).To(ContainElement(HaveField("FullName",
				protoreflect.FullName("osac.tests.v1.TestTargetLocalReference"))))
		})

		It("Discovers all reference field patterns in a complex message", func() {
			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						Target: testsv1.TestTargetReference_builder{
							Name: "full-ref-target",
						}.Build(),
						LocalTarget: testsv1.TestTargetLocalReference_builder{
							Name: "local-target",
						}.Build(),
						Attachment: testsv1.TestRefAttachment_builder{
							Subnet: testsv1.TestTargetLocalReference_builder{
								Name: "subnet-1",
							}.Build(),
							SecurityGroups: []*testsv1.TestOtherTargetLocalReference{
								testsv1.TestOtherTargetLocalReference_builder{
									Name: "sg-1",
								}.Build(),
							},
						}.Build(),
						OtherTargets: []*testsv1.TestOtherTargetLocalReference{
							testsv1.TestOtherTargetLocalReference_builder{
								Name: "other-1",
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build()

			refs := discoverReferenceFields(request)

			fullRefCount := 0
			localRefCount := 0
			otherRefCount := 0
			for _, ref := range refs {
				switch ref.FullName {
				case "osac.tests.v1.TestTargetReference":
					fullRefCount++
				case "osac.tests.v1.TestTargetLocalReference":
					localRefCount++
				case "osac.tests.v1.TestOtherTargetLocalReference":
					otherRefCount++
				}
			}
			Expect(fullRefCount).To(Equal(1))
			Expect(localRefCount).To(Equal(2))
			Expect(otherRefCount).To(Equal(2))
		})

		It("Returns no references when spec has no reference fields", func() {
			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{}.Build(),
				}.Build(),
			}.Build()

			refs := discoverReferenceFields(request)

			Expect(refs).To(BeEmpty())
		})
	})

	Describe("Stream pass-through", func() {
		BeforeEach(func() {
			var err error
			validator, err = NewReferenceValidator().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Passes streams through without validation", func() {
			handlerCalled := false
			mockHandler := func(srv any, stream grpc.ServerStream) error {
				handlerCalled = true
				return nil
			}

			err := validator.StreamServer(
				nil,
				nil,
				&grpc.StreamServerInfo{FullMethod: "/osac.tests.v1.TestService/Watch"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
		})
	})

	Describe("Reference type detection", func() {
		It("Identifies Reference types", func() {
			Expect(isReferenceType("osac.public.v1.VirtualNetworkReference")).To(BeTrue())
			Expect(isReferenceType("osac.tests.v1.TestTargetReference")).To(BeTrue())
		})

		It("Identifies LocalReference types", func() {
			Expect(isReferenceType("osac.public.v1.SubnetLocalReference")).To(BeTrue())
			Expect(isReferenceType("osac.tests.v1.TestTargetLocalReference")).To(BeTrue())
		})

		It("Rejects non-reference types", func() {
			Expect(isReferenceType("osac.public.v1.Metadata")).To(BeFalse())
			Expect(isReferenceType("osac.public.v1.VirtualNetworkSpec")).To(BeFalse())
			Expect(isReferenceType("osac.public.v1.VirtualNetwork")).To(BeFalse())
		})

		It("Distinguishes local from full references", func() {
			Expect(isLocalReference("osac.tests.v1.TestTargetLocalReference")).To(BeTrue())
			Expect(isLocalReference("osac.tests.v1.TestTargetReference")).To(BeFalse())
		})
	})

	Describe("Create or Update detection", func() {
		It("Matches Create methods", func() {
			Expect(isCreateOrUpdate("/osac.public.v1.VirtualNetworks/Create")).To(BeTrue())
		})

		It("Matches Update methods", func() {
			Expect(isCreateOrUpdate("/osac.public.v1.VirtualNetworks/Update")).To(BeTrue())
		})

		It("Does not match Get methods", func() {
			Expect(isCreateOrUpdate("/osac.public.v1.VirtualNetworks/Get")).To(BeFalse())
		})

		It("Does not match List methods", func() {
			Expect(isCreateOrUpdate("/osac.public.v1.VirtualNetworks/List")).To(BeFalse())
		})

		It("Does not match Delete methods", func() {
			Expect(isCreateOrUpdate("/osac.public.v1.VirtualNetworks/Delete")).To(BeFalse())
		})

		It("Does not match Signal methods", func() {
			Expect(isCreateOrUpdate("/osac.private.v1.VirtualNetworks/Signal")).To(BeFalse())
		})

		It("Does not match CreateSomethingElse (suffix match)", func() {
			Expect(isCreateOrUpdate("/osac.public.v1.Service/CreateNotification")).To(BeFalse())
		})
	})

	Describe("Unregistered reference types", func() {
		BeforeEach(func() {
			var err error
			validator, err = NewReferenceValidator().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Returns Internal error for unregistered reference types (fail closed)", func() {
			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						Target: testsv1.TestTargetReference_builder{
							Name: "my-target",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called when reference type is unregistered")
				return nil, nil
			}

			_, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.Internal))
			Expect(status.Message()).To(ContainSubstring("no lookup registered"))
		})
	})

	Describe("Lookup registration", func() {
		BeforeEach(func() {
			var err error
			validator, err = NewReferenceValidator().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Calls registered lookup function with correct arguments", func() {
			var capturedTenant, capturedProject, capturedID, capturedName string
			validator.Register("osac.tests.v1.TestTargetReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				capturedTenant = tenant
				capturedProject = project
				capturedID = id
				capturedName = name
				return &ResolvedRef{
					ID:      "resolved-id",
					Tenant:  tenant,
					Project: project,
					Name:    name,
				}, nil
			})

			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "project-b",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						Target: testsv1.TestTargetReference_builder{
							Name: "my-target",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			handlerCalled := false
			mockHandler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "response", nil
			}

			_, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(capturedTenant).To(Equal("tenant-a"))
			Expect(capturedProject).To(Equal("project-b"))
			Expect(capturedID).To(BeEmpty())
			Expect(capturedName).To(Equal("my-target"))
		})

		It("Auto-populates id when resolved by name", func() {
			validator.Register("osac.tests.v1.TestTargetLocalReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				return &ResolvedRef{
					ID:      "auto-id-123",
					Tenant:  tenant,
					Project: project,
					Name:    name,
				}, nil
			})

			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						LocalTarget: testsv1.TestTargetLocalReference_builder{
							Name: "my-local-target",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				return "response", nil
			}

			_, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(request.GetObject().GetSpec().GetLocalTarget().GetId()).To(Equal("auto-id-123"))
		})

		It("Auto-populates name when resolved by id", func() {
			validator.Register("osac.tests.v1.TestTargetLocalReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				return &ResolvedRef{
					ID:      id,
					Tenant:  tenant,
					Project: project,
					Name:    "auto-name-from-id",
				}, nil
			})

			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						LocalTarget: testsv1.TestTargetLocalReference_builder{
							Id: "target-id-456",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				return "response", nil
			}

			_, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(request.GetObject().GetSpec().GetLocalTarget().GetName()).To(Equal("auto-name-from-id"))
		})
	})

	Describe("Tenant context extraction", func() {
		BeforeEach(func() {
			var err error
			validator, err = NewReferenceValidator().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Extracts tenant and project from request metadata for local references", func() {
			var capturedTenant, capturedProject string
			validator.Register("osac.tests.v1.TestTargetLocalReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				capturedTenant = tenant
				capturedProject = project
				return &ResolvedRef{ID: "id-1", Tenant: tenant, Project: project, Name: name}, nil
			})

			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "my-tenant",
						Project: "my-project",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						LocalTarget: testsv1.TestTargetLocalReference_builder{
							Name: "target",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				return "response", nil
			}

			_, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(capturedTenant).To(Equal("my-tenant"))
			Expect(capturedProject).To(Equal("my-project"))
		})

		It("Uses explicit project from full reference when present", func() {
			var capturedProject string
			validator.Register("osac.tests.v1.TestTargetReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				capturedProject = project
				return &ResolvedRef{ID: "id-1", Tenant: tenant, Project: project, Name: name}, nil
			})

			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						Target: testsv1.TestTargetReference_builder{
							Name:    "target-in-other-project",
							Project: "other-project",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				return "response", nil
			}

			_, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(capturedProject).To(Equal("other-project"))
		})

		It("Uses shared tenant when shared flag is set on full reference", func() {
			var capturedTenant string
			validator.Register("osac.tests.v1.TestTargetReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				capturedTenant = tenant
				return &ResolvedRef{ID: "id-1", Tenant: tenant, Project: project, Name: name}, nil
			})

			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						Target: testsv1.TestTargetReference_builder{
							Name:   "shared-target",
							Shared: true,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				return "response", nil
			}

			_, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(capturedTenant).To(Equal("shared"))
		})
	})

	Describe("Error reporting", func() {
		BeforeEach(func() {
			var err error
			validator, err = NewReferenceValidator().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Returns InvalidArgument when reference has neither id nor name", func() {
			validator.Register("osac.tests.v1.TestTargetReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				Fail("Lookup should not be called for empty reference")
				return nil, nil
			})

			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						Target: testsv1.TestTargetReference_builder{}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid reference")
				return nil, nil
			}

			_, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))

			details := status.Details()
			Expect(details).To(HaveLen(1))
			badRequest, ok := details[0].(*errdetails.BadRequest)
			Expect(ok).To(BeTrue())
			Expect(badRequest.GetFieldViolations()).To(HaveLen(1))
			fv := badRequest.GetFieldViolations()[0]
			Expect(fv.GetField()).To(Equal("object.spec.target"))
			Expect(fv.GetDescription()).To(ContainSubstring("must specify id or name"))
		})

		It("Returns InvalidArgument with BadRequest FieldViolation for not-found reference", func() {
			validator.Register("osac.tests.v1.TestTargetReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				return nil, &notFoundError{name: name}
			})

			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						Target: testsv1.TestTargetReference_builder{
							Name: "nonexistent",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid reference")
				return nil, nil
			}

			_, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("reference validation failed"))

			details := status.Details()
			Expect(details).To(HaveLen(1))
			badRequest, ok := details[0].(*errdetails.BadRequest)
			Expect(ok).To(BeTrue())
			Expect(badRequest.GetFieldViolations()).To(HaveLen(1))
			fv := badRequest.GetFieldViolations()[0]
			Expect(fv.GetField()).To(Equal("object.spec.target"))
			Expect(fv.GetDescription()).To(ContainSubstring("nonexistent"))
		})

		It("Aggregates multiple reference errors in a single BadRequest", func() {
			validator.Register("osac.tests.v1.TestTargetReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				return nil, &notFoundError{name: name}
			})
			validator.Register("osac.tests.v1.TestTargetLocalReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				return nil, &notFoundError{name: name}
			})

			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						Target: testsv1.TestTargetReference_builder{
							Name: "missing-full",
						}.Build(),
						LocalTarget: testsv1.TestTargetLocalReference_builder{
							Name: "missing-local",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for invalid references")
				return nil, nil
			}

			_, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))

			details := status.Details()
			Expect(details).To(HaveLen(1))
			badRequest, ok := details[0].(*errdetails.BadRequest)
			Expect(ok).To(BeTrue())
			Expect(badRequest.GetFieldViolations()).To(HaveLen(2))
		})

		It("Returns Internal for DAO lookup errors (not InvalidArgument)", func() {
			validator.Register("osac.tests.v1.TestTargetReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				return nil, fmt.Errorf("database connection refused")
			})

			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						Target: testsv1.TestTargetReference_builder{
							Name: "some-target",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called on internal error")
				return nil, nil
			}

			_, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.Internal))
			Expect(status.Message()).To(ContainSubstring("internal error resolving reference"))
		})

		It("Passes through when id and name both match resolved values", func() {
			validator.Register("osac.tests.v1.TestTargetLocalReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				return &ResolvedRef{
					ID:   "my-id",
					Name: "my-name",
				}, nil
			})

			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						LocalTarget: testsv1.TestTargetLocalReference_builder{
							Id:   "my-id",
							Name: "my-name",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			handlerCalled := false
			mockHandler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return nil, nil
			}

			_, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
		})

		It("Returns InvalidArgument for id/name mismatch", func() {
			validator.Register("osac.tests.v1.TestTargetLocalReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				return &ResolvedRef{
					ID:   "different-id",
					Name: "different-name",
				}, nil
			})

			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						LocalTarget: testsv1.TestTargetLocalReference_builder{
							Id:   "my-id",
							Name: "my-name",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				Fail("Handler should not be called for mismatched references")
				return nil, nil
			}

			_, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("do not refer to the same resource"))
		})
	})

	Describe("End-to-end with complex message", func() {
		BeforeEach(func() {
			var err error
			validator, err = NewReferenceValidator().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Resolves and mutates all reference patterns through UnaryServer", func() {
			lookupFunc := func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				return &ResolvedRef{
					ID:   "resolved-" + name,
					Name: name,
				}, nil
			}
			validator.Register("osac.tests.v1.TestTargetReference", lookupFunc)
			validator.Register("osac.tests.v1.TestTargetLocalReference", lookupFunc)
			validator.Register("osac.tests.v1.TestOtherTargetLocalReference", lookupFunc)

			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						Target: testsv1.TestTargetReference_builder{
							Name: "full-ref",
						}.Build(),
						LocalTarget: testsv1.TestTargetLocalReference_builder{
							Name: "local-ref",
						}.Build(),
						Attachment: testsv1.TestRefAttachment_builder{
							Subnet: testsv1.TestTargetLocalReference_builder{
								Name: "nested-subnet",
							}.Build(),
							SecurityGroups: []*testsv1.TestOtherTargetLocalReference{
								testsv1.TestOtherTargetLocalReference_builder{
									Name: "nested-sg",
								}.Build(),
							},
						}.Build(),
						OtherTargets: []*testsv1.TestOtherTargetLocalReference{
							testsv1.TestOtherTargetLocalReference_builder{
								Name: "repeated-other",
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build()

			handlerCalled := false
			mockHandler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "response", nil
			}

			_, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())

			spec := request.GetObject().GetSpec()
			Expect(spec.GetTarget().GetId()).To(Equal("resolved-full-ref"))
			Expect(spec.GetLocalTarget().GetId()).To(Equal("resolved-local-ref"))
			Expect(spec.GetAttachment().GetSubnet().GetId()).To(Equal("resolved-nested-subnet"))
			Expect(spec.GetAttachment().GetSecurityGroups()[0].GetId()).To(Equal("resolved-nested-sg"))
			Expect(spec.GetOtherTargets()[0].GetId()).To(Equal("resolved-repeated-other"))
		})
	})

	Describe("Pass-through for non-proto requests", func() {
		BeforeEach(func() {
			var err error
			validator, err = NewReferenceValidator().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Passes through non-proto request on Create", func() {
			handlerCalled := false
			mockHandler := func(ctx context.Context, req any) (any, error) {
				handlerCalled = true
				return "response", nil
			}

			response, err := validator.UnaryServer(
				context.Background(),
				"not-a-proto",
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(response).To(Equal("response"))
		})
	})

	Describe("Prometheus metrics", func() {
		var registry *prometheus.Registry

		BeforeEach(func() {
			registry = prometheus.NewPedanticRegistry()
			var err error
			validator, err = NewReferenceValidator().
				SetLogger(logger).
				SetMetricsRegisterer(registry).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Increments valid counter on successful validation", func() {
			validator.Register("osac.tests.v1.TestTargetLocalReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				return &ResolvedRef{ID: "id-1", Tenant: tenant, Project: project, Name: name}, nil
			})

			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						LocalTarget: testsv1.TestTargetLocalReference_builder{
							Name: "target",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				return "response", nil
			}

			_, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)
			Expect(err).ToNot(HaveOccurred())

			count := counterValue(registry, "osac_reference_validation_total",
				"TestTargetLocalReference", "valid")
			Expect(count).To(Equal(1.0))
		})

		It("Increments invalid counter on not-found reference", func() {
			validator.Register("osac.tests.v1.TestTargetReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				return nil, &notFoundError{name: name}
			})

			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						Target: testsv1.TestTargetReference_builder{
							Name: "missing",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				return nil, nil
			}

			_, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))

			count := counterValue(registry, "osac_reference_validation_total",
				"TestTargetReference", "invalid")
			Expect(count).To(Equal(1.0))
		})

		It("Increments error counter on DAO error", func() {
			validator.Register("osac.tests.v1.TestTargetReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				return nil, fmt.Errorf("connection refused")
			})

			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						Target: testsv1.TestTargetReference_builder{
							Name: "target",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				return nil, nil
			}

			_, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.Internal))

			count := counterValue(registry, "osac_reference_validation_total",
				"TestTargetReference", "error")
			Expect(count).To(Equal(1.0))
		})

		It("Records duration histogram", func() {
			validator.Register("osac.tests.v1.TestTargetLocalReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				return &ResolvedRef{ID: "id-1", Tenant: tenant, Project: project, Name: name}, nil
			})

			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						LocalTarget: testsv1.TestTargetLocalReference_builder{
							Name: "target",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				return "response", nil
			}

			_, err := validator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)
			Expect(err).ToNot(HaveOccurred())

			families, err := registry.Gather()
			Expect(err).ToNot(HaveOccurred())
			var found bool
			for _, f := range families {
				if f.GetName() == "osac_reference_validation_duration_seconds" {
					found = true
					Expect(f.GetMetric()).ToNot(BeEmpty())
					Expect(f.GetMetric()[0].GetHistogram().GetSampleCount()).To(BeNumerically(">", 0))
				}
			}
			Expect(found).To(BeTrue())
		})

		It("Works without metrics registerer", func() {
			noMetricsValidator, err := NewReferenceValidator().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())

			noMetricsValidator.Register("osac.tests.v1.TestTargetLocalReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				return &ResolvedRef{ID: "id-1", Tenant: tenant, Project: project, Name: name}, nil
			})

			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Id: "resource-1",
					Metadata: testsv1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						LocalTarget: testsv1.TestTargetLocalReference_builder{
							Name: "target",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			mockHandler := func(ctx context.Context, req any) (any, error) {
				return "response", nil
			}

			response, err := noMetricsValidator.UnaryServer(
				context.Background(),
				request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(response).To(Equal("response"))
		})
	})
})

// discoveredRef holds information about a discovered reference field for test assertions.
type discoveredRef struct {
	FullName protoreflect.FullName
	Path     string
}

// discoverReferenceFields is a test helper that walks a request message and returns
// all discovered reference-typed fields.
func discoverReferenceFields(request any) []discoveredRef {
	msg, ok := request.(interface{ ProtoReflect() protoreflect.Message })
	if !ok {
		return nil
	}
	var refs []discoveredRef
	collectRefs(msg.ProtoReflect(), "", &refs)
	return refs
}

func collectRefs(msg protoreflect.Message, path string, refs *[]discoveredRef) {
	msg.Range(func(fd protoreflect.FieldDescriptor, val protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind {
			return true
		}

		fieldPath := fd.TextName()
		if path != "" {
			fieldPath = path + "." + fieldPath
		}

		if fd.IsList() {
			list := val.List()
			for i := 0; i < list.Len(); i++ {
				elemMsg := list.Get(i).Message()
				fullName := elemMsg.Descriptor().FullName()
				if isReferenceType(fullName) {
					*refs = append(*refs, discoveredRef{FullName: fullName, Path: fieldPath})
					continue
				}
				collectRefs(elemMsg, fieldPath, refs)
			}
			return true
		}

		subMsg := val.Message()
		fullName := subMsg.Descriptor().FullName()
		if isReferenceType(fullName) {
			*refs = append(*refs, discoveredRef{FullName: fullName, Path: fieldPath})
			return true
		}
		collectRefs(subMsg, fieldPath, refs)
		return true
	})
}

// notFoundError implements the error interface for testing not-found scenarios.
type notFoundError struct {
	name string
}

func (e *notFoundError) Error() string {
	return "not found: " + e.name
}

func (e *notFoundError) IsNotFound() bool {
	return true
}

// counterValue retrieves the value of a counter metric from a Prometheus registry.
func counterValue(registry *prometheus.Registry, name, resourceType, result string) float64 {
	families, err := registry.Gather()
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := m.GetLabel()
			if matchLabels(labels, resourceType, result) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func matchLabels(labels []*dto.LabelPair, resourceType, result string) bool {
	var gotType, gotResult bool
	for _, l := range labels {
		if l.GetName() == "resource_type" && l.GetValue() == resourceType {
			gotType = true
		}
		if l.GetName() == "result" && l.GetValue() == result {
			gotResult = true
		}
	}
	return gotType && gotResult
}
