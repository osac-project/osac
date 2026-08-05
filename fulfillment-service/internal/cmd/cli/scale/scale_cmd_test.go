/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package scale

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

var _ = Describe("Scale command registration", func() {
	It("should register a cluster subcommand", func() {
		cmd := Cmd()
		sub, _, err := cmd.Find([]string{"cluster"})
		Expect(err).NotTo(HaveOccurred())
		Expect(sub).NotTo(BeNil())
		Expect(sub.Name()).To(Equal("cluster"))
	})

	It("should register --node-set flag on scale cluster", func() {
		cmd := Cmd()
		sub, _, _ := cmd.Find([]string{"cluster"})
		flag := sub.Flags().Lookup("node-set")
		Expect(flag).NotTo(BeNil())
	})

	It("should register --size flag on scale cluster", func() {
		cmd := Cmd()
		sub, _, _ := cmd.Find([]string{"cluster"})
		flag := sub.Flags().Lookup("size")
		Expect(flag).NotTo(BeNil())
	})

	It("should accept the clusters alias", func() {
		cmd := Cmd()
		sub, _, err := cmd.Find([]string{"clusters"})
		Expect(err).NotTo(HaveOccurred())
		Expect(sub).NotTo(BeNil())
	})

	It("should fail when --size is negative", func() {
		cmd := Cmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"cluster", "my-cluster", "--node-set", "workers", "--size", "-1"})
		err := cmd.Execute()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("--size must be >= 0"))
	})
})

var _ = Describe("scaleCluster", func() {
	var (
		ctx     context.Context
		ctrl    *gomock.Controller
		client  *MockClustersClient
		console *terminal.Console
		out     *bytes.Buffer
	)

	newConsole := func(w *bytes.Buffer) *terminal.Console {
		logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError}))
		con, err := terminal.NewConsole().SetLogger(logger).SetStdout(w).SetStderr(&bytes.Buffer{}).Build()
		Expect(err).NotTo(HaveOccurred())
		Expect(con.AddTemplates(templatesFS, "templates")).To(Succeed())
		return con
	}

	clusterWith := func(id string, nodeSets map[string]int32) *publicv1.Cluster {
		sets := make(map[string]*publicv1.ClusterNodeSet, len(nodeSets))
		for name, size := range nodeSets {
			sets[name] = publicv1.ClusterNodeSet_builder{Size: size}.Build()
		}
		return publicv1.Cluster_builder{
			Id:   id,
			Spec: publicv1.ClusterSpec_builder{NodeSets: sets}.Build(),
		}.Build()
	}

	listResp := func(clusters ...*publicv1.Cluster) *publicv1.ClustersListResponse {
		return publicv1.ClustersListResponse_builder{
			Items: clusters,
			Size:  int32(len(clusters)),
			Total: int32(len(clusters)),
		}.Build()
	}

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		client = NewMockClustersClient(ctrl)
		out = &bytes.Buffer{}
		console = newConsole(out)
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	Describe("happy path", func() {
		It("calls Update with spec.node_sets field mask and the new size", func() {
			cluster := clusterWith("abc-123", map[string]int32{"workers": 2})

			client.EXPECT().
				List(ctx, gomock.Any()).
				Return(listResp(cluster), nil)

			client.EXPECT().
				Update(ctx, gomock.Any()).
				DoAndReturn(func(_ context.Context, req *publicv1.ClustersUpdateRequest, _ ...grpc.CallOption) (*publicv1.ClustersUpdateResponse, error) {
					Expect(req.GetUpdateMask().GetPaths()).To(ConsistOf("spec.node_sets"))
					Expect(req.GetObject().GetSpec().GetNodeSets()["workers"].GetSize()).To(Equal(int32(5)))
					return publicv1.ClustersUpdateResponse_builder{Object: req.GetObject()}.Build(), nil
				})

			Expect(scaleCluster(ctx, client, console, "abc-123", "workers", 5)).To(Succeed())
		})

		It("prints previous and new size in the success message", func() {
			cluster := clusterWith("abc-123", map[string]int32{"workers": 2})

			client.EXPECT().List(ctx, gomock.Any()).Return(listResp(cluster), nil)
			client.EXPECT().Update(ctx, gomock.Any()).Return(
				publicv1.ClustersUpdateResponse_builder{Object: cluster}.Build(), nil,
			)

			Expect(scaleCluster(ctx, client, console, "abc-123", "workers", 5)).To(Succeed())
			Expect(out.String()).To(ContainSubstring("2 → 5"))
		})

		It("succeeds when scaling a node set to zero", func() {
			cluster := clusterWith("abc-123", map[string]int32{"workers": 2})

			client.EXPECT().List(ctx, gomock.Any()).Return(listResp(cluster), nil)
			client.EXPECT().Update(ctx, gomock.Any()).
				DoAndReturn(func(_ context.Context, req *publicv1.ClustersUpdateRequest, _ ...grpc.CallOption) (*publicv1.ClustersUpdateResponse, error) {
					Expect(req.GetObject().GetSpec().GetNodeSets()["workers"].GetSize()).To(Equal(int32(0)))
					return publicv1.ClustersUpdateResponse_builder{Object: req.GetObject()}.Build(), nil
				})

			Expect(scaleCluster(ctx, client, console, "abc-123", "workers", 0)).To(Succeed())
		})

		It("uses a CEL filter to resolve the cluster by name", func() {
			cluster := clusterWith("abc-123", map[string]int32{"workers": 2})

			client.EXPECT().
				List(ctx, gomock.Any()).
				DoAndReturn(func(_ context.Context, req *publicv1.ClustersListRequest, _ ...grpc.CallOption) (*publicv1.ClustersListResponse, error) {
					Expect(req.GetFilter()).To(ContainSubstring("my-cluster"))
					return listResp(cluster), nil
				})
			client.EXPECT().Update(ctx, gomock.Any()).Return(
				publicv1.ClustersUpdateResponse_builder{Object: cluster}.Build(), nil,
			)

			Expect(scaleCluster(ctx, client, console, "my-cluster", "workers", 3)).To(Succeed())
		})
	})

	Describe("validation errors", func() {
		It("returns an error when the cluster is not found", func() {
			client.EXPECT().List(ctx, gomock.Any()).Return(listResp(), nil)

			err := scaleCluster(ctx, client, console, "missing", "workers", 3)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("missing"))
		})

		It("returns an error and renders the no_node_sets template when the cluster has no node sets", func() {
			cluster := publicv1.Cluster_builder{Id: "abc-123"}.Build()
			client.EXPECT().List(ctx, gomock.Any()).Return(listResp(cluster), nil)

			err := scaleCluster(ctx, client, console, "abc-123", "workers", 3)
			Expect(err).To(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("no node sets"))
		})

		It("returns an error and renders the node_set_not_found template listing available sets", func() {
			cluster := clusterWith("abc-123", map[string]int32{"gpu": 1})
			client.EXPECT().List(ctx, gomock.Any()).Return(listResp(cluster), nil)

			err := scaleCluster(ctx, client, console, "abc-123", "workers", 3)
			Expect(err).To(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("workers"))
			Expect(out.String()).To(ContainSubstring("gpu"))
		})

		It("returns an error when multiple clusters match the ref", func() {
			c1 := clusterWith("abc-123", map[string]int32{"workers": 2})
			c2 := clusterWith("def-456", map[string]int32{"workers": 1})
			client.EXPECT().List(ctx, gomock.Any()).Return(listResp(c1, c2), nil)

			err := scaleCluster(ctx, client, console, "workers", "workers", 3)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("use the ID instead"))
		})

		It("wraps server Update errors with context", func() {
			cluster := clusterWith("abc-123", map[string]int32{"workers": 2})
			client.EXPECT().List(ctx, gomock.Any()).Return(listResp(cluster), nil)
			client.EXPECT().Update(ctx, gomock.Any()).Return(
				nil, status.Error(codes.PermissionDenied, "permission denied"),
			)

			err := scaleCluster(ctx, client, console, "abc-123", "workers", 3)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to scale cluster"))
		})

		It("wraps server List errors with context", func() {
			client.EXPECT().List(ctx, gomock.Any()).Return(nil, fmt.Errorf("connection refused"))

			err := scaleCluster(ctx, client, console, "abc-123", "workers", 3)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to list clusters"))
		})
	})
})
