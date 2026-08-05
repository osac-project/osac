/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package cluster

import (
	"context"
	"errors"
	"log/slog"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/exit"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

// mockClusterVersionsClient is a minimal mock that intercepts List calls.
type mockClusterVersionsClient struct {
	publicv1.ClusterVersionsClient
	listFunc func(ctx context.Context, req *publicv1.ClusterVersionsListRequest, opts ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error)
}

func (m *mockClusterVersionsClient) List(ctx context.Context, req *publicv1.ClusterVersionsListRequest, opts ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
	return m.listFunc(ctx, req, opts...)
}

var _ = Describe("Create cluster flag registration", func() {
	It("should register --catalog-item flag", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("catalog-item")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Usage).To(ContainSubstring("Catalog item"))
	})

	It("should still register --template flag", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("template")
		Expect(flag).NotTo(BeNil())
	})

	It("should register --catalog-item without a short flag", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("catalog-item")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Shorthand).To(BeEmpty())
	})

	It("should keep -t as shorthand for --template", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		flag := cmd.Flags().Lookup("template")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Shorthand).To(Equal("t"))
	})
})

var _ = Describe("Create cluster flag validation", func() {
	It("should return error when both --catalog-item and --template are set", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetArgs([]string{"--catalog-item", "cat-001", "--template", "tpl-001", "--name", "test"})
		err := cmd.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("if any flags in the group"))
		Expect(err.Error()).To(ContainSubstring("catalog-item"))
		Expect(err.Error()).To(ContainSubstring("template"))
	})

	It("should return error when neither --catalog-item nor --template is set", func() {
		cmd := Cmd()
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetArgs([]string{"--name", "test"})
		err := cmd.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("at least one of the flags"))
		Expect(err.Error()).To(ContainSubstring("catalog-item"))
		Expect(err.Error()).To(ContainSubstring("template"))
	})
})

var _ = Describe("findVersion", func() {
	var (
		ctx     context.Context
		console *terminal.Console
		logger  *slog.Logger
	)

	BeforeEach(func() {
		ctx = context.Background()
		logger = slog.New(slog.NewTextHandler(GinkgoWriter, nil))
		var err error
		console, err = terminal.NewConsole().
			SetLogger(logger).
			SetStdout(GinkgoWriter).
			SetStderr(GinkgoWriter).
			Build()
		Expect(err).ToNot(HaveOccurred())
		err = console.AddTemplates(templatesFS, "templates")
		Expect(err).ToNot(HaveOccurred())
	})

	makeVersion := func(name, version string) *publicv1.ClusterVersion {
		return publicv1.ClusterVersion_builder{
			Metadata: publicv1.Metadata_builder{
				Name: name,
			}.Build(),
			Spec: publicv1.ClusterVersionSpec_builder{
				Version: version,
			}.Build(),
		}.Build()
	}

	It("returns the version when exactly one matches by metadata.name", func() {
		mock := &mockClusterVersionsClient{
			listFunc: func(_ context.Context, _ *publicv1.ClusterVersionsListRequest, _ ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
				return publicv1.ClusterVersionsListResponse_builder{
					Items: []*publicv1.ClusterVersion{makeVersion("4-17-0", "4.17.0")},
					Size:  1,
					Total: 1,
				}.Build(), nil
			},
		}
		runner := &runnerContext{
			console:               console,
			logger:                logger,
			clusterVersionsClient: mock,
		}
		runner.args.version = "4-17-0"
		result, err := runner.findVersion(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.GetMetadata().GetName()).To(Equal("4-17-0"))
	})

	It("returns the version when exactly one matches by spec.version", func() {
		mock := &mockClusterVersionsClient{
			listFunc: func(_ context.Context, _ *publicv1.ClusterVersionsListRequest, _ ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
				return publicv1.ClusterVersionsListResponse_builder{
					Items: []*publicv1.ClusterVersion{makeVersion("4-17-0-a1b2", "4.17.0")},
					Size:  1,
					Total: 1,
				}.Build(), nil
			},
		}
		runner := &runnerContext{
			console:               console,
			logger:                logger,
			clusterVersionsClient: mock,
		}
		runner.args.version = "4.17.0"
		result, err := runner.findVersion(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.GetMetadata().GetName()).To(Equal("4-17-0-a1b2"))
	})

	It("prefers metadata.name match when two versions match", func() {
		nameMatch := makeVersion("4-17-0", "4.17.0")
		versionMatch := makeVersion("other-name", "4-17-0")
		mock := &mockClusterVersionsClient{
			listFunc: func(_ context.Context, _ *publicv1.ClusterVersionsListRequest, _ ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
				return publicv1.ClusterVersionsListResponse_builder{
					Items: []*publicv1.ClusterVersion{versionMatch, nameMatch},
					Size:  2,
					Total: 2,
				}.Build(), nil
			},
		}
		runner := &runnerContext{
			console:               console,
			logger:                logger,
			clusterVersionsClient: mock,
		}
		runner.args.version = "4-17-0"
		result, err := runner.findVersion(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.GetMetadata().GetName()).To(Equal("4-17-0"))
	})

	It("returns exit error when no versions match", func() {
		callCount := 0
		mock := &mockClusterVersionsClient{
			listFunc: func(_ context.Context, _ *publicv1.ClusterVersionsListRequest, _ ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
				callCount++
				if callCount == 1 {
					// Filtered query: no matches
					return publicv1.ClusterVersionsListResponse_builder{}.Build(), nil
				}
				// Examples query:
				return publicv1.ClusterVersionsListResponse_builder{
					Items: []*publicv1.ClusterVersion{makeVersion("4-17-0", "4.17.0")},
					Size:  1,
					Total: 1,
				}.Build(), nil
			},
		}
		runner := &runnerContext{
			console:               console,
			logger:                logger,
			clusterVersionsClient: mock,
		}
		runner.args.version = "nonexistent"
		result, err := runner.findVersion(ctx)
		Expect(result).To(BeNil())
		Expect(err).To(HaveOccurred())
		var exitErr exit.Error
		Expect(errors.As(err, &exitErr)).To(BeTrue())
	})

	It("propagates gRPC errors from the list call", func() {
		mock := &mockClusterVersionsClient{
			listFunc: func(_ context.Context, _ *publicv1.ClusterVersionsListRequest, _ ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
				return nil, errors.New("connection refused")
			},
		}
		runner := &runnerContext{
			console:               console,
			logger:                logger,
			clusterVersionsClient: mock,
		}
		runner.args.version = "4.17.0"
		result, err := runner.findVersion(ctx)
		Expect(result).To(BeNil())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("connection refused"))
		var exitErr exit.Error
		Expect(errors.As(err, &exitErr)).To(BeFalse())
	})
})
