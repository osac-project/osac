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
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	testsv1 "github.com/osac-project/osac/proto/gen/osac/tests/v1"
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

		It("rejects non-canonical update mask paths", func() {
			for _, test := range []struct {
				path  string
				valid bool
			}{
				{path: "spec.add_on_operators", valid: true},
				{path: "spec.node_sets.control-plane.size", valid: true},
				{path: "metadata.labels.control-plane", valid: true},
				{path: "spec .add_on_operators", valid: false},
				{path: "spec.add_on_operators.-1", valid: false},
				{path: "spec.add_on_operators.id", valid: false},
				{path: "spec.node_sets.control-plane.invalid-field", valid: false},
				{path: "spec.node_sets.control-plane.size.host_type", valid: false},
				{path: "metadata.labels.control-plane.name", valid: false},
				{path: "spec..add_on_operators", valid: false},
			} {
				request := privatev1.ClustersUpdateRequest_builder{
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{test.path}},
				}.Build()
				err := validateCanonicalUpdateMask(request)
				if test.valid {
					Expect(err).ToNot(HaveOccurred())
				} else {
					Expect(err).To(HaveOccurred())
				}
			}
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

	It("skips only configured reference paths for configured methods", func() {
		updateMethod := "/osac.private.v1.ComputeInstances/Update"
		v, err := NewReferenceValidator().SetLogger(logger).
			SetExcludedReferencePaths([]string{updateMethod}, "object.spec.target").
			Build()
		Expect(err).ToNot(HaveOccurred())
		targetLookups := 0
		v.Register("osac.tests.v1.TestTargetReference", func(ctx context.Context, tenant, project, id, name string) (*ResolvedRef, error) {
			targetLookups++
			Expect(tenant).To(Equal("tenant-a"))
			return nil, &errRefNotFound{identifier: name}
		})
		localLookups := 0
		v.Register("osac.tests.v1.TestTargetLocalReference", func(ctx context.Context, tenant, project, id, name string) (*ResolvedRef, error) {
			localLookups++
			return &ResolvedRef{ID: "local-id", Name: name}, nil
		})
		request := testsv1.UpdateTestResourceWithRefsRequest_builder{Object: testsv1.TestResourceWithRefs_builder{
			Metadata: testsv1.Metadata_builder{Tenant: "tenant-a"}.Build(),
			Spec: testsv1.TestRefSpec_builder{
				Target:      testsv1.TestTargetReference_builder{Name: "excluded"}.Build(),
				LocalTarget: testsv1.TestTargetLocalReference_builder{Name: "validated"}.Build(),
			}.Build(),
		}.Build()}.Build()
		called := false
		handler := func(ctx context.Context, request any) (any, error) { called = true; return nil, nil }
		_, err = v.UnaryServer(context.Background(), request, &grpc.UnaryServerInfo{FullMethod: updateMethod}, handler)
		Expect(err).ToNot(HaveOccurred())
		Expect(called).To(BeTrue())
		Expect(targetLookups).To(BeZero())
		Expect(localLookups).To(Equal(1))

		called = false
		_, err = v.UnaryServer(context.Background(), request,
			&grpc.UnaryServerInfo{FullMethod: "/osac.private.v1.OtherResources/Update"}, handler)
		Expect(err).To(HaveOccurred())
		Expect(called).To(BeFalse())
		Expect(targetLookups).To(Equal(1))
		Expect(localLookups).To(Equal(2))

		_, err = v.UnaryServer(context.Background(), request,
			&grpc.UnaryServerInfo{FullMethod: "/osac.private.v1.ComputeInstances/Create"}, handler)
		Expect(err).To(HaveOccurred())
		Expect(called).To(BeFalse())
		Expect(targetLookups).To(Equal(2))
		Expect(localLookups).To(Equal(3))
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

		It("Does not skip Create reference validation for a deletion timestamp", func() {
			validator.Register("osac.tests.v1.TestTargetReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				return nil, &errRefNotFound{identifier: name}
			})
			request := testsv1.CreateTestResourceWithRefsRequest_builder{
				Object: testsv1.TestResourceWithRefs_builder{
					Metadata: testsv1.Metadata_builder{
						Tenant:            "tenant-a",
						DeletionTimestamp: timestamppb.Now(),
					}.Build(),
					Spec: testsv1.TestRefSpec_builder{
						Target: testsv1.TestTargetReference_builder{Name: "unpublished"}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			handlerCalled := false
			_, err := validator.UnaryServer(
				context.Background(), request,
				&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/Create"},
				func(ctx context.Context, req any) (any, error) {
					handlerCalled = true
					return nil, nil
				},
			)
			Expect(err).To(HaveOccurred())
			Expect(handlerCalled).To(BeFalse())
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

	Describe("Collection reference validation", func() {
		var registry *prometheus.Registry
		var lookups int
		var invoke func(*testsv1.TestRefSpec, string) (any, error)

		BeforeEach(func() {
			registry = prometheus.NewRegistry()
			var err error
			validator, err = NewReferenceValidator().SetLogger(logger).SetMetricsRegisterer(registry).Build()
			Expect(err).ToNot(HaveOccurred())
			lookups = 0
			lookup := func(ctx context.Context, tenant, project, id, name string) (*ResolvedRef, error) {
				lookups++
				if id == "" {
					id = "id-" + name
				}
				if name == "" {
					name = "name-" + id
				}
				return &ResolvedRef{ID: id, Name: name}, nil
			}
			validator.Register("osac.tests.v1.TestTargetReference", lookup)
			validator.Register("osac.tests.v1.TestTargetLocalReference", lookup)
			validator.Register("osac.tests.v1.TestOtherTargetLocalReference", lookup)
			invoke = func(spec *testsv1.TestRefSpec, method string) (any, error) {
				object := testsv1.TestResourceWithRefs_builder{
					Metadata: testsv1.Metadata_builder{Tenant: "tenant-a", Project: "project-a"}.Build(),
					Spec:     spec,
				}.Build()
				var request any = testsv1.CreateTestResourceWithRefsRequest_builder{Object: object}.Build()
				if method == "Update" {
					request = testsv1.UpdateTestResourceWithRefsRequest_builder{Object: object}.Build()
				}
				return validator.UnaryServer(context.Background(), request,
					&grpc.UnaryServerInfo{FullMethod: "/osac.tests.v1.TestService/" + method},
					func(ctx context.Context, actual any) (any, error) {
						Expect(actual).To(BeIdenticalTo(request))
						return spec, nil
					})
			}
		})

		DescribeTable("resolves direct references and mutates their original map values", func(method string) {
			spec := testsv1.TestRefSpec_builder{Targets: map[string]*testsv1.TestTargetReference{
				"by-name": testsv1.TestTargetReference_builder{Name: "target"}.Build(),
				"by-id":   testsv1.TestTargetReference_builder{Id: "target-id"}.Build(),
			}}.Build()
			response, err := invoke(spec, method)
			Expect(err).ToNot(HaveOccurred())
			Expect(response).To(BeIdenticalTo(spec))
			Expect(spec.GetTargets()["by-name"].GetId()).To(Equal("id-target"))
			Expect(spec.GetTargets()["by-id"].GetName()).To(Equal("name-target-id"))
			Expect(lookups).To(Equal(2))
			Expect(counterValue(registry, "osac_reference_validation_total", "TestTargetReference", "valid")).To(Equal(2.0))
			families, err := registry.Gather()
			Expect(err).ToNot(HaveOccurred())
			var samples uint64
			for _, family := range families {
				if family.GetName() == "osac_reference_validation_duration_seconds" {
					for _, metric := range family.GetMetric() {
						samples += metric.GetHistogram().GetSampleCount()
					}
				}
			}
			Expect(samples).To(Equal(uint64(2)))
		}, Entry("Create", "Create"), Entry("Update", "Update"))

		It("resolves nested messages, repeated references, and maps within map and list values", func() {
			attachment := testsv1.TestRefAttachment_builder{
				Subnet: testsv1.TestTargetLocalReference_builder{Name: "subnet"}.Build(),
				SecurityGroups: []*testsv1.TestOtherTargetLocalReference{
					testsv1.TestOtherTargetLocalReference_builder{Name: "group"}.Build(),
				},
				Targets: map[string]*testsv1.TestTargetReference{
					"inner": testsv1.TestTargetReference_builder{Name: "nested"}.Build(),
				},
			}.Build()
			listed := testsv1.TestRefAttachment_builder{Targets: map[string]*testsv1.TestTargetReference{
				"inner": testsv1.TestTargetReference_builder{Name: "listed"}.Build(),
			}}.Build()
			spec := testsv1.TestRefSpec_builder{
				Attachments:      map[string]*testsv1.TestRefAttachment{"outer": attachment},
				OtherAttachments: []*testsv1.TestRefAttachment{listed},
			}.Build()
			_, err := invoke(spec, "Create")
			Expect(err).ToNot(HaveOccurred())
			Expect(attachment.GetSubnet().GetId()).To(Equal("id-subnet"))
			Expect(attachment.GetSecurityGroups()[0].GetId()).To(Equal("id-group"))
			Expect(attachment.GetTargets()["inner"].GetId()).To(Equal("id-nested"))
			Expect(listed.GetTargets()["inner"].GetId()).To(Equal("id-listed"))
			Expect(lookups).To(Equal(4))
		})

		It("resolves integer and boolean keyed maps", func() {
			spec := testsv1.TestRefSpec_builder{
				NumberedTargets: map[int64]*testsv1.TestTargetLocalReference{
					-42: testsv1.TestTargetLocalReference_builder{Name: "numbered"}.Build(),
				},
				EnabledTargets: map[bool]*testsv1.TestTargetReference{
					false: testsv1.TestTargetReference_builder{Name: "disabled"}.Build(),
					true:  testsv1.TestTargetReference_builder{Name: "enabled"}.Build(),
				},
			}.Build()
			_, err := invoke(spec, "Create")
			Expect(err).ToNot(HaveOccurred())
			Expect(spec.GetNumberedTargets()[-42].GetId()).To(Equal("id-numbered"))
			Expect(spec.GetEnabledTargets()[false].GetId()).To(Equal("id-disabled"))
			Expect(spec.GetEnabledTargets()[true].GetId()).To(Equal("id-enabled"))
			Expect(lookups).To(Equal(3))
		})

		It("passes caller and selected scopes to map reference lookups", func() {
			var scopes []string
			lookup := func(ctx context.Context, tenant, project, id, name string) (*ResolvedRef, error) {
				scopes = append(scopes, tenant+"/"+project)
				return &ResolvedRef{ID: "id-" + name, Name: name}, nil
			}
			validator.Register("osac.tests.v1.TestTargetReference", lookup)
			validator.Register("osac.tests.v1.TestTargetLocalReference", lookup)
			spec := testsv1.TestRefSpec_builder{
				Targets: map[string]*testsv1.TestTargetReference{
					"selected": testsv1.TestTargetReference_builder{Name: "shared", Shared: true, Project: "selected"}.Build(),
				},
				NumberedTargets: map[int64]*testsv1.TestTargetLocalReference{
					1: testsv1.TestTargetLocalReference_builder{Name: "local"}.Build(),
				},
			}.Build()
			_, err := invoke(spec, "Create")
			Expect(err).ToNot(HaveOccurred())
			Expect(scopes).To(ConsistOf("shared/selected", "tenant-a/project-a"))
		})

		It("ignores empty maps, scalar values, and messages without references", func() {
			spec := testsv1.TestRefSpec_builder{
				Targets:     map[string]*testsv1.TestTargetReference{},
				Labels:      map[string]string{"target": "not-a-reference"},
				Attachments: map[string]*testsv1.TestRefAttachment{"empty": testsv1.TestRefAttachment_builder{}.Build()},
			}.Build()
			response, err := invoke(spec, "Create")
			Expect(err).ToNot(HaveOccurred())
			Expect(response).To(BeIdenticalTo(spec))
			Expect(lookups).To(BeZero())
		})

		It("aggregates sorted violations with escaped, typed, and nested map paths", func() {
			lookup := func(ctx context.Context, tenant, project, id, name string) (*ResolvedRef, error) {
				return nil, &notFoundError{name: name}
			}
			validator.Register("osac.tests.v1.TestTargetReference", lookup)
			validator.Register("osac.tests.v1.TestTargetLocalReference", lookup)
			validator.Register("osac.tests.v1.TestOtherTargetLocalReference", lookup)
			spec := testsv1.TestRefSpec_builder{
				Targets: map[string]*testsv1.TestTargetReference{
					"z":            testsv1.TestTargetReference_builder{Name: "missing"}.Build(),
					"a":            testsv1.TestTargetReference_builder{}.Build(),
					"quote\"\\key": testsv1.TestTargetReference_builder{Name: "escaped"}.Build(),
				},
				NumberedTargets: map[int64]*testsv1.TestTargetLocalReference{-42: testsv1.TestTargetLocalReference_builder{Name: "numbered"}.Build()},
				EnabledTargets:  map[bool]*testsv1.TestTargetReference{false: testsv1.TestTargetReference_builder{Name: "boolean"}.Build()},
				Attachments: map[string]*testsv1.TestRefAttachment{
					"outer": testsv1.TestRefAttachment_builder{SecurityGroups: []*testsv1.TestOtherTargetLocalReference{
						testsv1.TestOtherTargetLocalReference_builder{Name: "nested"}.Build(),
					}}.Build(),
				},
			}.Build()
			response, err := invoke(spec, "Create")
			Expect(response).To(BeNil())
			st := grpcstatus.Convert(err)
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(st.Details()).To(HaveLen(1))
			violations := st.Details()[0].(*errdetails.BadRequest).GetFieldViolations()
			var paths []string
			for _, violation := range violations {
				paths = append(paths, violation.GetField())
			}
			Expect(paths).To(Equal([]string{
				`object.spec.attachments["outer"].security_groups[0]`,
				"object.spec.enabled_targets[false]",
				"object.spec.numbered_targets[-42]",
				`object.spec.targets["a"]`,
				`object.spec.targets["quote\"\\key"]`,
				`object.spec.targets["z"]`,
			}))
			Expect(violations[3].GetDescription()).To(ContainSubstring("must specify id or name"))
			Expect(counterValue(registry, "osac_reference_validation_total", "TestTargetReference", "invalid")).To(Equal(4.0))
		})

		It("rejects inconsistent id and name in a map reference", func() {
			validator.Register("osac.tests.v1.TestTargetReference", func(ctx context.Context, tenant, project, id, name string) (*ResolvedRef, error) {
				return &ResolvedRef{ID: "other-id", Name: name}, nil
			})
			spec := testsv1.TestRefSpec_builder{Targets: map[string]*testsv1.TestTargetReference{
				"target": testsv1.TestTargetReference_builder{Id: "id", Name: "name"}.Build(),
			}}.Build()
			response, err := invoke(spec, "Create")
			Expect(response).To(BeNil())
			st := grpcstatus.Convert(err)
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(st.Message()).To(ContainSubstring(`object.spec.targets["target"]`))
			Expect(st.Message()).To(ContainSubstring("do not refer to the same resource"))
		})

		It("fails closed for an unregistered map reference type", func() {
			var err error
			validator, err = NewReferenceValidator().SetLogger(logger).Build()
			Expect(err).ToNot(HaveOccurred())
			spec := testsv1.TestRefSpec_builder{Targets: map[string]*testsv1.TestTargetReference{
				"target": testsv1.TestTargetReference_builder{Name: "target"}.Build(),
			}}.Build()
			response, err := invoke(spec, "Create")
			Expect(response).To(BeNil())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Internal))
			Expect(err.Error()).To(ContainSubstring("no lookup registered"))
		})

		It("stops map traversal and blocks the handler when a lookup fails internally", func() {
			validator.Register("osac.tests.v1.TestTargetReference", func(ctx context.Context, tenant, project, id, name string) (*ResolvedRef, error) {
				lookups++
				return nil, fmt.Errorf("database unavailable")
			})
			spec := testsv1.TestRefSpec_builder{Targets: map[string]*testsv1.TestTargetReference{
				"a": testsv1.TestTargetReference_builder{Name: "a"}.Build(),
				"b": testsv1.TestTargetReference_builder{Name: "b"}.Build(),
			}}.Build()
			response, err := invoke(spec, "Create")
			Expect(response).To(BeNil())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Internal))
			Expect(err.Error()).To(ContainSubstring("object.spec.targets["))
			Expect(lookups).To(Equal(1))
			Expect(counterValue(registry, "osac_reference_validation_total", "TestTargetReference", "error")).To(Equal(1.0))
		})

		DescribeTable("honors map and nested field exclusions only for their configured method", func(
			targetPath, attachmentPath string, updateLookups int, keepTargetID string,
		) {
			var err error
			validator, err = NewReferenceValidator().SetLogger(logger).
				SetExcludedReferencePaths([]string{"/osac.tests.v1.TestService/Update"}, targetPath, attachmentPath).Build()
			Expect(err).ToNot(HaveOccurred())
			lookup := func(ctx context.Context, tenant, project, id, name string) (*ResolvedRef, error) {
				lookups++
				return &ResolvedRef{ID: "id-" + name, Name: name}, nil
			}
			validator.Register("osac.tests.v1.TestTargetReference", lookup)
			validator.Register("osac.tests.v1.TestTargetLocalReference", lookup)
			spec := testsv1.TestRefSpec_builder{
				Targets: map[string]*testsv1.TestTargetReference{
					"skip": testsv1.TestTargetReference_builder{Name: "target"}.Build(),
					"keep": testsv1.TestTargetReference_builder{Name: "kept-target"}.Build(),
				},
				Attachments: map[string]*testsv1.TestRefAttachment{
					"skip": testsv1.TestRefAttachment_builder{Subnet: testsv1.TestTargetLocalReference_builder{Name: "excluded"}.Build()}.Build(),
					"keep": testsv1.TestRefAttachment_builder{Subnet: testsv1.TestTargetLocalReference_builder{Name: "included"}.Build()}.Build(),
				},
			}.Build()
			_, err = invoke(spec, "Update")
			Expect(err).ToNot(HaveOccurred())
			Expect(lookups).To(Equal(updateLookups))
			Expect(spec.GetTargets()["skip"].GetId()).To(BeEmpty())
			Expect(spec.GetTargets()["keep"].GetId()).To(Equal(keepTargetID))
			Expect(spec.GetAttachments()["skip"].GetSubnet().GetId()).To(BeEmpty())
			Expect(spec.GetAttachments()["keep"].GetSubnet().GetId()).To(Equal("id-included"))
			_, err = invoke(spec, "Create")
			Expect(err).ToNot(HaveOccurred())
			Expect(lookups).To(Equal(updateLookups + 4))
			Expect(spec.GetTargets()["skip"].GetId()).To(Equal("id-target"))
			Expect(spec.GetTargets()["keep"].GetId()).To(Equal("id-kept-target"))
			Expect(spec.GetAttachments()["skip"].GetSubnet().GetId()).To(Equal("id-excluded"))
		},
			Entry("whole map and nested field", "object.spec.targets", `object.spec.attachments["skip"].subnet`, 1, ""),
			Entry("individual reference entry and nested field", `object.spec.targets["skip"]`, `object.spec.attachments["skip"].subnet`, 2, "id-kept-target"),
			Entry("whole map and individual message entry", "object.spec.targets", `object.spec.attachments["skip"]`, 1, ""),
			Entry("individual reference and message entries", `object.spec.targets["skip"]`, `object.spec.attachments["skip"]`, 2, "id-kept-target"),
		)

		DescribeTable("honors list exclusions only for their configured method", func(
			referencePath, attachmentPath string, expectedIDs []string, updateLookups int,
		) {
			var err error
			validator, err = NewReferenceValidator().SetLogger(logger).
				SetExcludedReferencePaths([]string{"/osac.tests.v1.TestService/Update"}, referencePath, attachmentPath).Build()
			Expect(err).ToNot(HaveOccurred())
			lookup := func(ctx context.Context, tenant, project, id, name string) (*ResolvedRef, error) {
				lookups++
				return &ResolvedRef{ID: "id-" + name, Name: name}, nil
			}
			validator.Register("osac.tests.v1.TestOtherTargetLocalReference", lookup)
			validator.Register("osac.tests.v1.TestTargetLocalReference", lookup)
			spec := testsv1.TestRefSpec_builder{
				OtherTargets: []*testsv1.TestOtherTargetLocalReference{
					testsv1.TestOtherTargetLocalReference_builder{Name: "target-0"}.Build(),
					testsv1.TestOtherTargetLocalReference_builder{Name: "target-1"}.Build(),
				},
				OtherAttachments: []*testsv1.TestRefAttachment{
					testsv1.TestRefAttachment_builder{Subnet: testsv1.TestTargetLocalReference_builder{Name: "subnet-0"}.Build()}.Build(),
					testsv1.TestRefAttachment_builder{Subnet: testsv1.TestTargetLocalReference_builder{Name: "subnet-1"}.Build()}.Build(),
				},
			}.Build()
			_, err = invoke(spec, "Update")
			Expect(err).ToNot(HaveOccurred())
			Expect(lookups).To(Equal(updateLookups))
			Expect([]string{
				spec.GetOtherTargets()[0].GetId(), spec.GetOtherTargets()[1].GetId(),
				spec.GetOtherAttachments()[0].GetSubnet().GetId(), spec.GetOtherAttachments()[1].GetSubnet().GetId(),
			}).To(Equal(expectedIDs))
			_, err = invoke(spec, "Create")
			Expect(err).ToNot(HaveOccurred())
			Expect(lookups).To(Equal(updateLookups + 4))
			Expect([]string{
				spec.GetOtherTargets()[0].GetId(), spec.GetOtherTargets()[1].GetId(),
				spec.GetOtherAttachments()[0].GetSubnet().GetId(), spec.GetOtherAttachments()[1].GetSubnet().GetId(),
			}).To(Equal([]string{"id-target-0", "id-target-1", "id-subnet-0", "id-subnet-1"}))
		},
			Entry("whole lists", "object.spec.other_targets", "object.spec.other_attachments", []string{"", "", "", ""}, 0),
			Entry("individual reference and whole message list", "object.spec.other_targets[0]", "object.spec.other_attachments", []string{"", "id-target-1", "", ""}, 1),
			Entry("whole reference list and individual message", "object.spec.other_targets", "object.spec.other_attachments[0]", []string{"", "", "", "id-subnet-1"}, 1),
			Entry("individual references and messages", "object.spec.other_targets[0]", "object.spec.other_attachments[0]", []string{"", "id-target-1", "", "id-subnet-1"}, 2),
		)
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

		It("Reports whether a lookup is registered", func() {
			Expect(validator.HasLookup("osac.tests.v1.TestTargetReference")).To(BeFalse())
			validator.Register("osac.tests.v1.TestTargetReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				return &ResolvedRef{ID: id, Tenant: tenant, Project: project, Name: name}, nil
			})
			Expect(validator.HasLookup("osac.tests.v1.TestTargetReference")).To(BeTrue())
			Expect(validator.HasLookup("osac.tests.v1.TestTargetLocalReference")).To(BeFalse())
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

		// Regression guard for the DiskImage deletion-protection invariant (OSAC-3715): the
		// compute_instance_templates clause of check_disk_image_not_in_use() matches
		// spec_defaults.disk_image->>'id'. A template may be created referencing a disk image by
		// name only, so id ends up stored solely because this interceptor backfills it from the
		// resolved reference before the handler persists the object. The template server handler
		// does not backfill id itself, so this must hold at the interceptor layer. Uses the real
		// ComputeInstanceTemplate message and the real registered type name.
		It("Backfills id on a template's spec_defaults.disk_image referenced by name only", func() {
			validator.Register("osac.private.v1.DiskImageReference", func(
				ctx context.Context, tenant, project, id, name string,
			) (*ResolvedRef, error) {
				return &ResolvedRef{
					ID:   "resolved-" + name,
					Name: name,
				}, nil
			})

			request := privatev1.ComputeInstanceTemplatesCreateRequest_builder{
				Object: privatev1.ComputeInstanceTemplate_builder{
					Id: "template-1",
					Metadata: privatev1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "default",
					}.Build(),
					SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
						DiskImage: privatev1.DiskImageReference_builder{
							Name: "tmpl-di",
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
				&grpc.UnaryServerInfo{FullMethod: "/osac.private.v1.ComputeInstanceTemplates/Create"},
				mockHandler,
			)

			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())

			diskImage := request.GetObject().GetSpecDefaults().GetDiskImage()
			Expect(diskImage.GetId()).To(Equal("resolved-tmpl-di"))
			Expect(diskImage.GetName()).To(Equal("tmpl-di"))
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
