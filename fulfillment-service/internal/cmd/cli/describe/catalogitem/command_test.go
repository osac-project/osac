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
	"strings"
	"testing"

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

func testConnection(t *testing.T, compute *computeServer, cluster *clusterServer, bare *bareMetalServer) *grpc.ClientConn {
	t.Helper()
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
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	})
	return conn
}

func TestFetchCatalogItemViews(t *testing.T) {
	compute := &computeServer{items: []*publicv1.ComputeInstanceCatalogItem{{Id: "compute-id"}}}
	cluster := &clusterServer{item: publicv1.ClusterCatalogItem_builder{Id: "cluster-id"}.Build()}
	bare := &bareMetalServer{item: publicv1.BareMetalInstanceCatalogItem_builder{Id: "bare-id"}.Build()}
	conn := testConnection(t, compute, cluster, bare)
	for _, tt := range []struct {
		name  string
		fetch fetchFunc
		id    string
		getID *string
	}{
		{"compute", fetchCompute, "compute-id", &compute.getID},
		{"cluster", fetchCluster, "cluster-id", &cluster.getID},
		{"bare metal", fetchBareMetal, "bare-id", &bare.getID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v, err := tt.fetch(context.Background(), conn, "friendly-name")
			if err != nil {
				t.Fatal(err)
			}
			if v.id != tt.id || *tt.getID != tt.id {
				t.Fatalf("got view ID %q, Get ID %q, want %q", v.id, *tt.getID, tt.id)
			}
		})
	}
	if want := `this.id == "friendly-name" || this.metadata.name == "friendly-name"`; compute.filter != want {
		t.Fatalf("name-or-ID filter = %q, want %q", compute.filter, want)
	}
}

func TestFetchErrors(t *testing.T) {
	compute := &computeServer{}
	conn := testConnection(t, compute, &clusterServer{}, &bareMetalServer{})
	if _, err := fetchCompute(context.Background(), conn, "missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing item error = %v", err)
	}
	compute.items = []*publicv1.ComputeInstanceCatalogItem{{Id: "a"}, {Id: "b"}}
	if _, err := fetchCompute(context.Background(), conn, "duplicate"); err == nil || !strings.Contains(err.Error(), "use the ID") {
		t.Fatalf("ambiguous item error = %v", err)
	}
	compute.listErr = status.Error(codes.PermissionDenied, "list blocked")
	if _, err := fetchCompute(context.Background(), conn, "a"); err == nil || !strings.Contains(err.Error(), "list blocked") {
		t.Fatalf("List error = %v", err)
	}
	compute.listErr = nil
	compute.items = compute.items[:1]
	compute.getErr = status.Error(codes.NotFound, "removed")
	if _, err := fetchCompute(context.Background(), conn, "a"); err == nil || !strings.Contains(err.Error(), "removed") {
		t.Fatalf("Get error = %v", err)
	}
	cmd := ComputeCmd()
	if err := cmd.Args(cmd, nil); err == nil {
		t.Fatal("describe command must require an item name or ID")
	}
}
