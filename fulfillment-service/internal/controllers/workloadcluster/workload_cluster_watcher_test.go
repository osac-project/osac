/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package workloadcluster

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type watcherWorkloadClustersServer struct {
	privatev1.UnimplementedWorkloadClustersServer
	signals chan *privatev1.WorkloadClustersSignalRequest
}

func (s *watcherWorkloadClustersServer) List(context.Context, *privatev1.WorkloadClustersListRequest) (*privatev1.WorkloadClustersListResponse, error) {
	return privatev1.WorkloadClustersListResponse_builder{
		Items: []*privatev1.WorkloadCluster{privatev1.WorkloadCluster_builder{
			Id:   "wc-1",
			Spec: privatev1.WorkloadClusterSpec_builder{SourceHubId: "hub-1", ManagedClusterName: "worker-1"}.Build(),
		}.Build()},
	}.Build(), nil
}

func (s *watcherWorkloadClustersServer) Signal(_ context.Context, request *privatev1.WorkloadClustersSignalRequest) (*privatev1.WorkloadClustersSignalResponse, error) {
	s.signals <- request
	return &privatev1.WorkloadClustersSignalResponse{}, nil
}

func TestSignalForHubRecordsReconnectMetadata(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	server := &watcherWorkloadClustersServer{signals: make(chan *privatev1.WorkloadClustersSignalRequest, 1)}
	privatev1.RegisterWorkloadClustersServer(grpcServer, server)
	serveErr := make(chan error, 1)
	go func() { serveErr <- grpcServer.Serve(listener) }()
	defer grpcServer.Stop()

	connection, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer connection.Close()

	reconciler := &function{workloadClusters: privatev1.NewWorkloadClustersClient(connection)}
	reconciler.signalForHub(context.Background(), "hub-1", "HubReconnected", "Normal")

	var request *privatev1.WorkloadClustersSignalRequest
	select {
	case request = <-server.signals:
	case err := <-serveErr:
		require.NoError(t, err)
		t.Fatal("gRPC server stopped before receiving the signal")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Hub signal")
	}
	require.Equal(t, "wc-1", request.GetId())
	require.Equal(t, "HubReconnected", request.GetReason())
	require.Equal(t, "Normal", request.GetSeverity())
}
