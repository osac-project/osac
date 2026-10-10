/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package catalogitem

import (
	"context"
	"net"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type computeServer struct {
	publicv1.UnimplementedComputeInstanceCatalogItemsServer
	items           []*publicv1.ComputeInstanceCatalogItem
	getID           string
	filter          string
	listErr, getErr error
}

func (s *computeServer) List(_ context.Context, r *publicv1.ComputeInstanceCatalogItemsListRequest) (*publicv1.ComputeInstanceCatalogItemsListResponse, error) {
	s.filter = r.GetFilter()
	if s.listErr != nil {
		return nil, s.listErr
	}
	return publicv1.ComputeInstanceCatalogItemsListResponse_builder{Items: s.items}.Build(), nil
}

func (s *computeServer) Get(_ context.Context, r *publicv1.ComputeInstanceCatalogItemsGetRequest) (*publicv1.ComputeInstanceCatalogItemsGetResponse, error) {
	s.getID = r.GetId()
	if s.getErr != nil {
		return nil, s.getErr
	}
	return publicv1.ComputeInstanceCatalogItemsGetResponse_builder{Object: s.items[0]}.Build(), nil
}

type clusterServer struct {
	publicv1.UnimplementedClusterCatalogItemsServer
	item  *publicv1.ClusterCatalogItem
	getID string
}

func (s *clusterServer) List(_ context.Context, _ *publicv1.ClusterCatalogItemsListRequest) (*publicv1.ClusterCatalogItemsListResponse, error) {
	return publicv1.ClusterCatalogItemsListResponse_builder{Items: []*publicv1.ClusterCatalogItem{s.item}}.Build(), nil
}

func (s *clusterServer) Get(_ context.Context, r *publicv1.ClusterCatalogItemsGetRequest) (*publicv1.ClusterCatalogItemsGetResponse, error) {
	s.getID = r.GetId()
	return publicv1.ClusterCatalogItemsGetResponse_builder{Object: s.item}.Build(), nil
}

type bareMetalServer struct {
	publicv1.UnimplementedBareMetalInstanceCatalogItemsServer
	item  *publicv1.BareMetalInstanceCatalogItem
	getID string
}

func (s *bareMetalServer) List(_ context.Context, _ *publicv1.BareMetalInstanceCatalogItemsListRequest) (*publicv1.BareMetalInstanceCatalogItemsListResponse, error) {
	return publicv1.BareMetalInstanceCatalogItemsListResponse_builder{Items: []*publicv1.BareMetalInstanceCatalogItem{s.item}}.Build(), nil
}

func (s *bareMetalServer) Get(_ context.Context, r *publicv1.BareMetalInstanceCatalogItemsGetRequest) (*publicv1.BareMetalInstanceCatalogItemsGetResponse, error) {
	s.getID = r.GetId()
	return publicv1.BareMetalInstanceCatalogItemsGetResponse_builder{Object: s.item}.Build(), nil
}

func testConnection(compute *computeServer, cluster *clusterServer, bare *bareMetalServer) *grpc.ClientConn {
	GinkgoHelper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	publicv1.RegisterComputeInstanceCatalogItemsServer(server, compute)
	publicv1.RegisterClusterCatalogItemsServer(server, cluster)
	publicv1.RegisterBareMetalInstanceCatalogItemsServer(server, bare)
	go func() {
		_ = server.Serve(listener)
	}()
	conn, err := grpc.NewClient("passthrough:///catalog-test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		Expect(conn.Close()).To(Succeed())
		server.Stop()
		Expect(listener.Close()).To(Succeed())
	})
	return conn
}

var _ = Describe("Describe catalog item command", func() {
	Describe("fetching catalog items", func() {
		var (
			compute *computeServer
			cluster *clusterServer
			bare    *bareMetalServer
			conn    *grpc.ClientConn
		)

		BeforeEach(func() {
			compute = &computeServer{items: []*publicv1.ComputeInstanceCatalogItem{{Id: "compute-id"}}}
			cluster = &clusterServer{item: publicv1.ClusterCatalogItem_builder{Id: "cluster-id"}.Build()}
			bare = &bareMetalServer{item: publicv1.BareMetalInstanceCatalogItem_builder{Id: "bare-id"}.Build()}
			conn = testConnection(compute, cluster, bare)
		})

		It("resolves a compute item name to its ID", func() {
			v, err := fetchCompute(context.Background(), conn, "friendly-name")
			Expect(err).NotTo(HaveOccurred())
			Expect(v.id).To(Equal("compute-id"))
			Expect(compute.getID).To(Equal("compute-id"))
			Expect(compute.filter).To(Equal(`this.id == "friendly-name" || this.metadata.name == "friendly-name"`))
		})

		It("resolves a cluster item name to its ID", func() {
			v, err := fetchCluster(context.Background(), conn, "friendly-name")
			Expect(err).NotTo(HaveOccurred())
			Expect(v.id).To(Equal("cluster-id"))
			Expect(cluster.getID).To(Equal("cluster-id"))
		})

		It("resolves a bare metal item name to its ID", func() {
			v, err := fetchBareMetal(context.Background(), conn, "friendly-name")
			Expect(err).NotTo(HaveOccurred())
			Expect(v.id).To(Equal("bare-id"))
			Expect(bare.getID).To(Equal("bare-id"))
		})

		Describe("compute item errors", func() {
			It("reports a missing item", func() {
				compute.items = nil
				_, err := fetchCompute(context.Background(), conn, "missing")
				Expect(err).To(MatchError(ContainSubstring("not found")))
			})

			It("reports an ambiguous name", func() {
				compute.items = []*publicv1.ComputeInstanceCatalogItem{{Id: "a"}, {Id: "b"}}
				_, err := fetchCompute(context.Background(), conn, "duplicate")
				Expect(err).To(MatchError(ContainSubstring("use the ID")))
			})

			It("propagates a List error", func() {
				compute.listErr = status.Error(codes.PermissionDenied, "list blocked")
				_, err := fetchCompute(context.Background(), conn, "compute-id")
				Expect(err).To(MatchError(ContainSubstring("list blocked")))
			})

			It("propagates a Get error", func() {
				compute.getErr = status.Error(codes.NotFound, "removed")
				_, err := fetchCompute(context.Background(), conn, "compute-id")
				Expect(err).To(MatchError(ContainSubstring("removed")))
			})
		})
	})

	It("requires a catalog item name or ID", func() {
		cmd := ComputeCmd()
		Expect(cmd.Args(cmd, nil)).To(HaveOccurred())
	})
})
