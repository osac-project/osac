/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package upgrade

import (
	"bytes"
	"context"
	"log/slog"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/fulfillment-service/internal/testing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type mockClustersClient struct {
	publicv1.ClustersClient
	listFunc   func(ctx context.Context, req *publicv1.ClustersListRequest, opts ...grpc.CallOption) (*publicv1.ClustersListResponse, error)
	updateFunc func(ctx context.Context, req *publicv1.ClustersUpdateRequest, opts ...grpc.CallOption) (*publicv1.ClustersUpdateResponse, error)
}

func (m *mockClustersClient) List(ctx context.Context, req *publicv1.ClustersListRequest, opts ...grpc.CallOption) (*publicv1.ClustersListResponse, error) {
	return m.listFunc(ctx, req, opts...)
}

func (m *mockClustersClient) Update(ctx context.Context, req *publicv1.ClustersUpdateRequest, opts ...grpc.CallOption) (*publicv1.ClustersUpdateResponse, error) {
	return m.updateFunc(ctx, req, opts...)
}

type mockClusterVersionsClient struct {
	publicv1.ClusterVersionsClient
	listFunc func(ctx context.Context, req *publicv1.ClusterVersionsListRequest, opts ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error)
}

func (m *mockClusterVersionsClient) List(ctx context.Context, req *publicv1.ClusterVersionsListRequest, opts ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
	return m.listFunc(ctx, req, opts...)
}

type testClustersServer struct {
	publicv1.UnimplementedClustersServer
}

func (testClustersServer) List(context.Context, *publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error) {
	return publicv1.ClustersListResponse_builder{
		Items: []*publicv1.Cluster{publicv1.Cluster_builder{Id: "cluster-id"}.Build()},
		Size:  1,
		Total: 1,
	}.Build(), nil
}

type testClusterVersionsServer struct {
	publicv1.UnimplementedClusterVersionsServer
}

func (testClusterVersionsServer) List(_ context.Context, req *publicv1.ClusterVersionsListRequest) (*publicv1.ClusterVersionsListResponse, error) {
	if req.GetFilter() != "" {
		return publicv1.ClusterVersionsListResponse_builder{}.Build(), nil
	}
	return publicv1.ClusterVersionsListResponse_builder{
		Items: []*publicv1.ClusterVersion{publicv1.ClusterVersion_builder{
			Metadata: publicv1.Metadata_builder{Name: "4-17-0"}.Build(),
			Spec:     publicv1.ClusterVersionSpec_builder{Version: "4.17.0"}.Build(),
		}.Build()},
		Size:  1,
		Total: 1,
	}.Build(), nil
}

var _ = Describe("Upgrade command", func() {
	It("registers a cluster subcommand", func() {
		cmd := Cmd()
		sub, _, err := cmd.Find([]string{"cluster"})
		Expect(err).NotTo(HaveOccurred())
		Expect(sub).NotTo(BeNil())
		Expect(sub.Name()).To(Equal("cluster"))
	})

	It("requires --version", func() {
		cmd := Cmd()
		sub, _, err := cmd.Find([]string{"cluster"})
		Expect(err).NotTo(HaveOccurred())
		flag := sub.Flags().Lookup("version")
		Expect(flag).NotTo(BeNil())
		Expect(flag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
		Expect(flag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
	})

	It("does not expose control-plane or node-set target flags", func() {
		cmd := Cmd()
		sub, _, err := cmd.Find([]string{"cluster"})
		Expect(err).NotTo(HaveOccurred())
		Expect(sub.Flags().Lookup("control-plane")).To(BeNil())
		Expect(sub.Flags().Lookup("node-set")).To(BeNil())
	})

	It("installs the reflection helper before rendering missing versions", func() {
		logger := slog.New(slog.NewTextHandler(GinkgoWriter, nil))
		output := &bytes.Buffer{}
		console, err := terminal.NewConsole().
			SetLogger(logger).
			SetStdout(output).
			SetStderr(GinkgoWriter).
			Build()
		Expect(err).NotTo(HaveOccurred())

		settings, err := config.NewSettings().
			SetLogger(logger).
			SetDir(GinkgoT().TempDir()).
			Build()
		Expect(err).NotTo(HaveOccurred())

		server := testing.NewServer()
		publicv1.RegisterClustersServer(server.Registrar(), testClustersServer{})
		publicv1.RegisterClusterVersionsServer(server.Registrar(), testClusterVersionsServer{})
		server.Start()
		DeferCleanup(server.Stop)
		settings.SetAddress(server.Address())
		settings.SetPlaintext(true)

		ctx := context.Background()
		ctx = logging.LoggerIntoContext(ctx, logger)
		ctx = terminal.ConsoleIntoContext(ctx, console)
		ctx = config.SettingsIntoContext(ctx, settings)

		cmd := Cmd()
		cmd.SetArgs([]string{"cluster", "cluster-id", "--version", "missing"})
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)

		err = cmd.ExecuteContext(ctx)
		Expect(err).To(HaveOccurred())
		Expect(output.String()).To(ContainSubstring("Version 'missing' doesn't exist."))
		Expect(output.String()).To(ContainSubstring("The following are some of the valid versions:"))
		Expect(output.String()).To(ContainSubstring("4.17.0"))
	})

	It("fails before an API request when --version is missing", func() {
		cmd := Cmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"cluster", "my-cluster"})
		err := cmd.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("required flag(s) \"version\" not set"))
	})
})

var _ = Describe("upgradeCluster", func() {
	var (
		ctx     context.Context
		console *terminal.Console
		output  *bytes.Buffer
	)

	BeforeEach(func() {
		ctx = context.Background()
		logger := slog.New(slog.NewTextHandler(GinkgoWriter, nil))
		output = &bytes.Buffer{}
		var err error
		console, err = terminal.NewConsole().
			SetLogger(logger).
			SetStdout(output).
			SetStderr(GinkgoWriter).
			Build()
		Expect(err).NotTo(HaveOccurred())
	})

	cluster := func(id string) *publicv1.Cluster {
		return publicv1.Cluster_builder{Id: id}.Build()
	}

	version := func(name, semver string) *publicv1.ClusterVersion {
		return publicv1.ClusterVersion_builder{
			Metadata: publicv1.Metadata_builder{Name: name}.Build(),
			Spec:     publicv1.ClusterVersionSpec_builder{Version: semver}.Build(),
		}.Build()
	}

	clusterList := func(items ...*publicv1.Cluster) *publicv1.ClustersListResponse {
		return publicv1.ClustersListResponse_builder{
			Items: items,
			Size:  int32(len(items)),
			Total: int32(len(items)),
		}.Build()
	}

	versionList := func(items ...*publicv1.ClusterVersion) *publicv1.ClusterVersionsListResponse {
		return publicv1.ClusterVersionsListResponse_builder{
			Items: items,
			Size:  int32(len(items)),
			Total: int32(len(items)),
		}.Build()
	}

	It("resolves the target name-first and sends only spec.version", func() {
		clusters := &mockClustersClient{
			listFunc: func(_ context.Context, req *publicv1.ClustersListRequest, _ ...grpc.CallOption) (*publicv1.ClustersListResponse, error) {
				Expect(req.GetFilter()).To(ContainSubstring("my-cluster"))
				return clusterList(cluster("cluster-id")), nil
			},
			updateFunc: func(_ context.Context, req *publicv1.ClustersUpdateRequest, _ ...grpc.CallOption) (*publicv1.ClustersUpdateResponse, error) {
				Expect(req.GetUpdateMask().GetPaths()).To(ConsistOf("spec.version"))
				Expect(req.GetObject().GetId()).To(Equal("cluster-id"))
				Expect(req.GetObject().GetSpec().GetVersion().GetName()).To(Equal("4-17-0"))
				return publicv1.ClustersUpdateResponse_builder{Object: req.GetObject()}.Build(), nil
			},
		}
		versions := &mockClusterVersionsClient{
			listFunc: func(_ context.Context, req *publicv1.ClusterVersionsListRequest, _ ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
				Expect(req.GetFilter()).To(ContainSubstring("this.metadata.name == \"4-17-0\""))
				return versionList(version("other-name", "4-17-0"), version("4-17-0", "4.17.0")), nil
			},
		}

		Expect(upgradeCluster(ctx, clusters, versions, console, "my-cluster", "4-17-0")).To(Succeed())
		Expect(output.String()).To(ContainSubstring("Requested upgrade of cluster 'cluster-id' to version '4.17.0'"))
	})

	It("resolves a semver input to the catalog version name", func() {
		clusters := &mockClustersClient{
			listFunc: func(_ context.Context, _ *publicv1.ClustersListRequest, _ ...grpc.CallOption) (*publicv1.ClustersListResponse, error) {
				return clusterList(cluster("cluster-id")), nil
			},
			updateFunc: func(_ context.Context, req *publicv1.ClustersUpdateRequest, _ ...grpc.CallOption) (*publicv1.ClustersUpdateResponse, error) {
				Expect(req.GetObject().GetSpec().GetVersion().GetName()).To(Equal("4-17-0-a1b2"))
				return publicv1.ClustersUpdateResponse_builder{Object: req.GetObject()}.Build(), nil
			},
		}
		versions := &mockClusterVersionsClient{
			listFunc: func(_ context.Context, _ *publicv1.ClusterVersionsListRequest, _ ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
				return versionList(version("4-17-0-a1b2", "4.17.0")), nil
			},
		}

		Expect(upgradeCluster(ctx, clusters, versions, console, "cluster-id", "4.17.0")).To(Succeed())
	})

	It("propagates server rejection reasons", func() {
		clusters := &mockClustersClient{
			listFunc: func(_ context.Context, _ *publicv1.ClustersListRequest, _ ...grpc.CallOption) (*publicv1.ClustersListResponse, error) {
				return clusterList(cluster("cluster-id")), nil
			},
			updateFunc: func(_ context.Context, _ *publicv1.ClustersUpdateRequest, _ ...grpc.CallOption) (*publicv1.ClustersUpdateResponse, error) {
				return nil, status.Error(codes.PermissionDenied, "target version is not allowed")
			},
		}
		versions := &mockClusterVersionsClient{
			listFunc: func(_ context.Context, _ *publicv1.ClusterVersionsListRequest, _ ...grpc.CallOption) (*publicv1.ClusterVersionsListResponse, error) {
				return versionList(version("4-17-0", "4.17.0")), nil
			},
		}

		err := upgradeCluster(ctx, clusters, versions, console, "cluster-id", "4-17-0")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to upgrade cluster"))
		Expect(err.Error()).To(ContainSubstring("target version is not allowed"))
	})
})
