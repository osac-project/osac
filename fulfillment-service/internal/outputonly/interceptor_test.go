/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package outputonly

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var (
	publicUpdateMethod  = "/osac.public.v1.ComputeInstances/Update"
	privateUpdateMethod = "/osac.private.v1.ComputeInstances/Update"
	publicCreateMethod  = "/osac.public.v1.ComputeInstances/Create"
)

// computeUpdateRequest builds a public compute instance update request with the given mask
// paths.
func computeUpdateRequest(paths ...string) *publicv1.ComputeInstancesUpdateRequest {
	return publicv1.ComputeInstancesUpdateRequest_builder{
		Object: publicv1.ComputeInstance_builder{
			Id: "test-instance",
		}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
	}.Build()
}

// runInterceptor runs the interceptor with a handler that records that it was called.
func runInterceptor(interceptor *Interceptor, method string, request any) (handlerCalled bool, err error) {
	handler := func(ctx context.Context, request any) (any, error) {
		handlerCalled = true
		return nil, nil
	}
	_, err = interceptor.UnaryServer(
		context.Background(),
		request,
		&grpc.UnaryServerInfo{FullMethod: method},
		handler,
	)
	return handlerCalled, err
}

var _ = Describe("Output-only interceptor", func() {
	var interceptor *Interceptor

	BeforeEach(func() {
		var err error
		interceptor, err = NewInterceptor().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Creation", func() {
		It("Fails if logger is not set", func() {
			result, err := NewInterceptor().
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(result).To(BeNil())
		})
	})

	Describe("Unary server interception", func() {
		It("Rejects a public update whose mask touches an output-only field", func() {
			handlerCalled, err := runInterceptor(
				interceptor,
				publicUpdateMethod,
				computeUpdateRequest("status.instance_type"),
			)
			Expect(err).To(HaveOccurred())
			grpcErr, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(grpcErr.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(grpcErr.Message()).To(ContainSubstring("status.instance_type"))
			Expect(grpcErr.Message()).To(ContainSubstring("output-only"))
			Expect(handlerCalled).To(BeFalse())
		})

		It("Rejects a public update whose mask names the status field itself", func() {
			_, err := runInterceptor(
				interceptor,
				publicUpdateMethod,
				computeUpdateRequest("status"),
			)
			Expect(err).To(HaveOccurred())
			grpcErr, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(grpcErr.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(grpcErr.Message()).To(ContainSubstring("output-only"))
		})

		It("Rejects a public update whose mask touches a subpath of an output-only field", func() {
			_, err := runInterceptor(
				interceptor,
				publicUpdateMethod,
				computeUpdateRequest("spec.instance_type", "status.instance_type.name"),
			)
			Expect(err).To(HaveOccurred())
			grpcErr, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(grpcErr.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(grpcErr.Message()).To(ContainSubstring("status.instance_type.name"))
		})

		It("Rejects a public update whose mask uses JSON field names", func() {
			_, err := runInterceptor(
				interceptor,
				publicUpdateMethod,
				computeUpdateRequest("status.instanceType"),
			)
			Expect(err).To(HaveOccurred())
			grpcErr, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(grpcErr.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		It("Rejects masks with whitespace the server's path compiler trims", func() {
			// The generic server normalizes mask paths (masks.SplitPath) before
			// resolving them, so " status" reaches the status field there. The
			// interceptor must resolve the same paths it does.
			for _, path := range []string{" status", "status ", " status.state", "status.  state"} {
				_, err := runInterceptor(
					interceptor,
					publicUpdateMethod,
					computeUpdateRequest(path),
				)
				Expect(err).To(HaveOccurred(), "path %q must be rejected", path)
				grpcErr, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(grpcErr.Code()).To(Equal(grpccodes.InvalidArgument))
			}
		})

		It("Rejects masks with empty segments the server's path compiler drops", func() {
			_, err := runInterceptor(
				interceptor,
				publicUpdateMethod,
				computeUpdateRequest("spec..instance_type", "status..instance_type"),
			)
			Expect(err).To(HaveOccurred())
			grpcErr, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(grpcErr.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		It("Rejects masks that reach annotated fields nested in maps", func() {
			// ClusterNodeSet.observed_version is annotated OUTPUT_ONLY and lives
			// inside the spec.node_sets map; the segment after a map field is its
			// key, so the walk must descend into the map's value message.
			_, err := runInterceptor(
				interceptor,
				"/osac.public.v1.Clusters/Update",
				publicv1.ClustersUpdateRequest_builder{
					Object: publicv1.Cluster_builder{Id: "test-cluster"}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.node_sets.workers.observed_version"},
					},
				}.Build(),
			)
			Expect(err).To(HaveOccurred())
			grpcErr, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(grpcErr.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(grpcErr.Message()).To(ContainSubstring("observed_version"))
		})

		It("Allows masks that set a whole map entry, without touching annotated fields", func() {
			handlerCalled, err := runInterceptor(
				interceptor,
				"/osac.public.v1.Clusters/Update",
				publicv1.ClustersUpdateRequest_builder{
					Object: publicv1.Cluster_builder{Id: "test-cluster"}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.node_sets.workers"},
					},
				}.Build(),
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
		})

		It("Allows masks with list indexes and passes invalid ones to the server", func() {
			handlerCalled, err := runInterceptor(
				interceptor,
				"/osac.public.v1.Clusters/Update",
				publicv1.ClustersUpdateRequest_builder{
					Object: publicv1.Cluster_builder{Id: "test-cluster"}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.node_sets.workers.size"},
					},
				}.Build(),
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
		})

		It("Allows a public update whose mask only touches writable fields", func() {
			handlerCalled, err := runInterceptor(
				interceptor,
				publicUpdateMethod,
				computeUpdateRequest("spec.instance_type", "metadata"),
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
		})

		It("Allows a private update even when the mask touches an output-only field", func() {
			handlerCalled, err := runInterceptor(
				interceptor,
				privateUpdateMethod,
				computeUpdateRequest("status.instance_type"),
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
		})

		It("Allows public methods that are not updates", func() {
			handlerCalled, err := runInterceptor(
				interceptor,
				publicCreateMethod,
				computeUpdateRequest("status.instance_type"),
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
		})

		It("Allows updates without a mask", func() {
			handlerCalled, err := runInterceptor(
				interceptor,
				publicUpdateMethod,
				computeUpdateRequest(),
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
		})

		It("Allows masks with unknown paths, leaving them to the server", func() {
			handlerCalled, err := runInterceptor(
				interceptor,
				publicUpdateMethod,
				computeUpdateRequest("spec.does_not_exist"),
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
		})

		It("Allows requests that are not protobuf messages", func() {
			handlerCalled, err := runInterceptor(
				interceptor,
				publicUpdateMethod,
				"not a protobuf message",
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
		})
	})
})
