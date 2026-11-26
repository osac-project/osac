/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package clusterversions

import (
	"bytes"
	"context"
	"errors"
	"log/slog"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"

	"github.com/osac-project/osac/fulfillment-service/internal/exit"
	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type mockClusterVersionsClient struct {
	publicv1.ClusterVersionsClient
	listFunc func(ctx context.Context, req *publicv1.ClusterVersionsListRequest, opts ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error)
}

func (m *mockClusterVersionsClient) List(ctx context.Context, req *publicv1.ClusterVersionsListRequest, opts ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
	return m.listFunc(ctx, req, opts...)
}

var _ = Describe("Find", func() {
	var (
		ctx     context.Context
		console *terminal.Console
		output  *bytes.Buffer
	)

	BeforeEach(func() {
		ctx = context.Background()
		logger := slog.New(slog.NewTextHandler(GinkgoWriter, nil))
		var err error
		output = &bytes.Buffer{}
		console, err = terminal.NewConsole().
			SetLogger(logger).
			SetStdout(output).
			SetStderr(GinkgoWriter).
			Build()
		Expect(err).NotTo(HaveOccurred())

		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		descriptor := (&publicv1.ClusterVersion{}).ProtoReflect().Descriptor()
		objectHelper := reflection.NewMockObjectHelper(ctrl)
		objectHelper.EXPECT().Descriptor().Return(descriptor).AnyTimes()
		objectHelper.EXPECT().FullName().Return(descriptor.FullName()).AnyTimes()
		objectHelper.EXPECT().IsTenantScoped().Return(false).AnyTimes()
		helper := reflection.NewMockHelper(ctrl)
		helper.EXPECT().Lookup(string(descriptor.FullName())).Return(objectHelper).AnyTimes()
		console.SetHelper(helper)
	})

	version := func(name, semver string) *publicv1.ClusterVersion {
		return publicv1.ClusterVersion_builder{
			Metadata: publicv1.Metadata_builder{Name: name}.Build(),
			Spec:     publicv1.ClusterVersionSpec_builder{Version: semver}.Build(),
		}.Build()
	}

	response := func(items ...*publicv1.ClusterVersion) *publicv1.ClusterVersionsListResponse {
		return publicv1.ClusterVersionsListResponse_builder{
			Items: items,
			Size:  int32(len(items)),
			Total: int32(len(items)),
		}.Build()
	}

	It("returns a unique metadata-name match", func() {
		client := &mockClusterVersionsClient{
			listFunc: func(_ context.Context, req *publicv1.ClusterVersionsListRequest, _ ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
				Expect(req.GetFilter()).To(ContainSubstring("this.metadata.name == \"4-17-0\""))
				Expect(req.GetFilter()).To(ContainSubstring("this.spec.version == \"4-17-0\""))
				Expect(req.GetLimit()).To(Equal(int32(2)))
				return response(version("4-17-0", "4.17.0")), nil
			},
		}

		result, err := Find(ctx, client, console, "4-17-0")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.GetMetadata().GetName()).To(Equal("4-17-0"))
	})

	It("returns a unique spec-version match", func() {
		client := &mockClusterVersionsClient{
			listFunc: func(_ context.Context, _ *publicv1.ClusterVersionsListRequest, _ ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
				return response(version("4-17-0-a1b2", "4.17.0")), nil
			},
		}

		result, err := Find(ctx, client, console, "4.17.0")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.GetMetadata().GetName()).To(Equal("4-17-0-a1b2"))
	})

	It("prefers the metadata-name match when name and semver match different versions", func() {
		client := &mockClusterVersionsClient{
			listFunc: func(_ context.Context, _ *publicv1.ClusterVersionsListRequest, _ ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
				return response(version("other-name", "4-17-0"), version("4-17-0", "4.17.0")), nil
			},
		}

		result, err := Find(ctx, client, console, "4-17-0")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.GetMetadata().GetName()).To(Equal("4-17-0"))
	})

	It("renders examples and returns an exit error when no version matches", func() {
		calls := 0
		client := &mockClusterVersionsClient{
			listFunc: func(_ context.Context, req *publicv1.ClusterVersionsListRequest, _ ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
				calls++
				if calls == 1 {
					Expect(req.GetFilter()).NotTo(BeEmpty())
					return response(), nil
				}
				Expect(req.GetFilter()).To(BeEmpty())
				Expect(req.GetLimit()).To(Equal(int32(10)))
				return response(version("4-17-0", "4.17.0")), nil
			},
		}

		result, err := Find(ctx, client, console, "missing")
		Expect(result).To(BeNil())
		Expect(err).To(HaveOccurred())
		var exitErr exit.Error
		Expect(errors.As(err, &exitErr)).To(BeTrue())
		Expect(output.String()).To(ContainSubstring("Version 'missing' doesn't exist."))
		Expect(output.String()).To(ContainSubstring("4-17-0"))
	})

	It("wraps client list errors", func() {
		client := &mockClusterVersionsClient{
			listFunc: func(_ context.Context, _ *publicv1.ClusterVersionsListRequest, _ ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
				return nil, errors.New("connection refused")
			},
		}

		result, err := Find(ctx, client, console, "4.17.0")
		Expect(result).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("failed to list cluster versions")))
		Expect(err).To(MatchError(ContainSubstring("connection refused")))
	})
})
