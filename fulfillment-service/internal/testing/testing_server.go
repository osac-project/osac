/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package testing

import (
	"context"
	"crypto/tls"
	"net"
	"strings"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/grpc"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

// Server is a gRPC server used only for tests.
type Server struct {
	listener net.Listener
	server   *grpc.Server
}

// NewServer creates a new gRPC server that listens in a randomly selected port in the local host.
func NewServer() *Server {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	server := grpc.NewServer()
	return &Server{
		listener: listener,
		server:   server,
	}
}

// NewTLSServer creates a new gRPC server with TLS using a self-signed localhost certificate. It listens on a randomly
// selected port on the local host.
func NewTLSServer() *Server {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	tlsListener := tls.NewListener(listener, &tls.Config{
		Certificates: []tls.Certificate{
			LocalhostCertificate(),
		},
	})
	server := grpc.NewServer()
	return &Server{
		listener: tlsListener,
		server:   server,
	}
}

// Address returns the address where the server is listening.
func (s *Server) Address() string {
	return s.listener.Addr().String()
}

// Registrar returns the registrar that can be used to register server implementations.
func (s *Server) Registrar() grpc.ServiceRegistrar {
	return s.server
}

// Start starts the server. This needs to be done after registering all server implementations, and before trying to
// call any of them.
func (s *Server) Start() {
	go func() {
		defer GinkgoRecover()
		err := s.server.Serve(s.listener)
		Expect(err).ToNot(HaveOccurred())
	}()
}

// Stop stops the server, closing all connections and releasing all the resources it was using.
func (s *Server) Stop() {
	s.server.Stop()
}

// Make sure that we implement the interface.
var _ publicv1.ClustersServer = (*ClustersServerFuncs)(nil)

// ClustersServerFuncs is an implementation of the clusters server that uses configurable functions to implement the
// methods.
type ClustersServerFuncs struct {
	publicv1.UnimplementedClustersServer

	CreateFunc               func(context.Context, *publicv1.ClustersCreateRequest) (*publicv1.ClustersCreateResponse, error)
	DeleteFunc               func(context.Context, *publicv1.ClustersDeleteRequest) (*publicv1.ClustersDeleteResponse, error)
	GetFunc                  func(context.Context, *publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error)
	ListFunc                 func(context.Context, *publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error)
	GetKubeconfigFunc        func(context.Context, *publicv1.ClustersGetKubeconfigRequest) (*publicv1.ClustersGetKubeconfigResponse, error)
	GetKubeconfigViaHttpFunc func(context.Context, *publicv1.ClustersGetKubeconfigViaHttpRequest) (*httpbody.HttpBody, error)
	UpdateFunc               func(context.Context, *publicv1.ClustersUpdateRequest) (*publicv1.ClustersUpdateResponse, error)
}

func (s *ClustersServerFuncs) Create(ctx context.Context,
	request *publicv1.ClustersCreateRequest) (response *publicv1.ClustersCreateResponse, err error) {
	response, err = s.CreateFunc(ctx, request)
	return
}

func (s *ClustersServerFuncs) Delete(ctx context.Context,
	request *publicv1.ClustersDeleteRequest) (response *publicv1.ClustersDeleteResponse, err error) {
	response, err = s.DeleteFunc(ctx, request)
	return
}

func (s *ClustersServerFuncs) Get(ctx context.Context,
	request *publicv1.ClustersGetRequest) (response *publicv1.ClustersGetResponse, err error) {
	response, err = s.GetFunc(ctx, request)
	return
}

func (s *ClustersServerFuncs) GetKubeconfig(ctx context.Context,
	request *publicv1.ClustersGetKubeconfigRequest) (response *publicv1.ClustersGetKubeconfigResponse, err error) {
	response, err = s.GetKubeconfigFunc(ctx, request)
	return
}

func (s *ClustersServerFuncs) GetKubeconfigViaHttp(ctx context.Context,
	request *publicv1.ClustersGetKubeconfigViaHttpRequest) (response *httpbody.HttpBody, err error) {
	response, err = s.GetKubeconfigViaHttpFunc(ctx, request)
	return
}

func (s *ClustersServerFuncs) List(ctx context.Context,
	request *publicv1.ClustersListRequest) (response *publicv1.ClustersListResponse, err error) {
	response, err = s.ListFunc(ctx, request)
	return
}

func (s *ClustersServerFuncs) Update(ctx context.Context,
	request *publicv1.ClustersUpdateRequest) (response *publicv1.ClustersUpdateResponse, err error) {
	response, err = s.UpdateFunc(ctx, request)
	return
}

// Make sure that we implement the interface.
var _ publicv1.ComputeInstancesServer = (*ComputeInstancesServerFuncs)(nil)

// ComputeInstancesServerFuncs is an implementation of the compute instances server that uses configurable functions to implement the
// methods.
type ComputeInstancesServerFuncs struct {
	publicv1.UnimplementedComputeInstancesServer

	CreateFunc func(context.Context, *publicv1.ComputeInstancesCreateRequest) (*publicv1.ComputeInstancesCreateResponse, error)
	DeleteFunc func(context.Context, *publicv1.ComputeInstancesDeleteRequest) (*publicv1.ComputeInstancesDeleteResponse, error)
	GetFunc    func(context.Context, *publicv1.ComputeInstancesGetRequest) (*publicv1.ComputeInstancesGetResponse, error)
	ListFunc   func(context.Context, *publicv1.ComputeInstancesListRequest) (*publicv1.ComputeInstancesListResponse, error)
	UpdateFunc func(context.Context, *publicv1.ComputeInstancesUpdateRequest) (*publicv1.ComputeInstancesUpdateResponse, error)
}

func (s *ComputeInstancesServerFuncs) Create(ctx context.Context,
	request *publicv1.ComputeInstancesCreateRequest) (response *publicv1.ComputeInstancesCreateResponse, err error) {
	response, err = s.CreateFunc(ctx, request)
	return
}

func (s *ComputeInstancesServerFuncs) Delete(ctx context.Context,
	request *publicv1.ComputeInstancesDeleteRequest) (response *publicv1.ComputeInstancesDeleteResponse, err error) {
	response, err = s.DeleteFunc(ctx, request)
	return
}

func (s *ComputeInstancesServerFuncs) Get(ctx context.Context,
	request *publicv1.ComputeInstancesGetRequest) (response *publicv1.ComputeInstancesGetResponse, err error) {
	response, err = s.GetFunc(ctx, request)
	return
}

func (s *ComputeInstancesServerFuncs) List(ctx context.Context,
	request *publicv1.ComputeInstancesListRequest) (response *publicv1.ComputeInstancesListResponse, err error) {
	response, err = s.ListFunc(ctx, request)
	return
}

func (s *ComputeInstancesServerFuncs) Update(ctx context.Context,
	request *publicv1.ComputeInstancesUpdateRequest) (response *publicv1.ComputeInstancesUpdateResponse, err error) {
	response, err = s.UpdateFunc(ctx, request)
	return
}

// Make sure that we implement the interface.
var _ publicv1.ComputeInstanceTemplatesServer = (*ComputeInstanceTemplatesServerFuncs)(nil)

// ComputeInstanceTemplatesServerFuncs is an implementation of the compute instance templates server that uses configurable functions to implement the
// methods.
type ComputeInstanceTemplatesServerFuncs struct {
	publicv1.UnimplementedComputeInstanceTemplatesServer

	CreateFunc func(context.Context, *publicv1.ComputeInstanceTemplatesCreateRequest) (*publicv1.ComputeInstanceTemplatesCreateResponse, error)
	DeleteFunc func(context.Context, *publicv1.ComputeInstanceTemplatesDeleteRequest) (*publicv1.ComputeInstanceTemplatesDeleteResponse, error)
	GetFunc    func(context.Context, *publicv1.ComputeInstanceTemplatesGetRequest) (*publicv1.ComputeInstanceTemplatesGetResponse, error)
	ListFunc   func(context.Context, *publicv1.ComputeInstanceTemplatesListRequest) (*publicv1.ComputeInstanceTemplatesListResponse, error)
	UpdateFunc func(context.Context, *publicv1.ComputeInstanceTemplatesUpdateRequest) (*publicv1.ComputeInstanceTemplatesUpdateResponse, error)
}

func (s *ComputeInstanceTemplatesServerFuncs) Create(ctx context.Context,
	request *publicv1.ComputeInstanceTemplatesCreateRequest) (response *publicv1.ComputeInstanceTemplatesCreateResponse, err error) {
	response, err = s.CreateFunc(ctx, request)
	return
}

func (s *ComputeInstanceTemplatesServerFuncs) Delete(ctx context.Context,
	request *publicv1.ComputeInstanceTemplatesDeleteRequest) (response *publicv1.ComputeInstanceTemplatesDeleteResponse, err error) {
	response, err = s.DeleteFunc(ctx, request)
	return
}

func (s *ComputeInstanceTemplatesServerFuncs) Get(ctx context.Context,
	request *publicv1.ComputeInstanceTemplatesGetRequest) (response *publicv1.ComputeInstanceTemplatesGetResponse, err error) {
	response, err = s.GetFunc(ctx, request)
	return
}

func (s *ComputeInstanceTemplatesServerFuncs) List(ctx context.Context,
	request *publicv1.ComputeInstanceTemplatesListRequest) (response *publicv1.ComputeInstanceTemplatesListResponse, err error) {
	response, err = s.ListFunc(ctx, request)
	return
}

func (s *ComputeInstanceTemplatesServerFuncs) Update(ctx context.Context,
	request *publicv1.ComputeInstanceTemplatesUpdateRequest) (response *publicv1.ComputeInstanceTemplatesUpdateResponse, err error) {
	response, err = s.UpdateFunc(ctx, request)
	return
}

// Make sure that we implement the interface.
var _ publicv1.EventsServer = (*EventsServerFuncs)(nil)

// EventsServerFuncs is an implementation of the events server that uses configurable functions to implement the
// methods.
type EventsServerFuncs struct {
	publicv1.UnimplementedEventsServer

	WatchFunc func(*publicv1.EventsWatchRequest, publicv1.Events_WatchServer) error
}

func (s *EventsServerFuncs) Watch(request *publicv1.EventsWatchRequest, stream publicv1.Events_WatchServer) error {
	return s.WatchFunc(request, stream)
}

// Helper function to extract object ID from event
func GetEventObjectID(event *publicv1.Event) string {
	switch payload := event.Payload.(type) {
	case *publicv1.Event_Cluster:
		if payload.Cluster != nil {
			return payload.Cluster.Id
		}
	case *publicv1.Event_ClusterTemplate:
		if payload.ClusterTemplate != nil {
			return payload.ClusterTemplate.Id
		}
	}
	return ""
}

// Helper function to check if an event matches the filter
func MatchesFilter(event *publicv1.Event, filter string) bool {
	// Empty filter - send all events
	if filter == "" {
		return true
	}

	// Determine what type the filter is for
	// Check for cluster_template first (more specific) to avoid substring issues
	isClusterTemplateFilter := strings.Contains(filter, "event.cluster_template")
	isClusterFilter := strings.Contains(filter, "event.cluster") && !isClusterTemplateFilter

	// Check if the event type matches the filter type
	switch event.Payload.(type) {
	case *publicv1.Event_Cluster:
		if !isClusterFilter {
			return false
		}
		// If filter is just a type check, send all cluster events
		if filter == "has(event.cluster)" {
			return true
		}
	case *publicv1.Event_ClusterTemplate:
		if !isClusterTemplateFilter {
			return false
		}
		// If filter is just a type check, send all cluster template events
		if filter == "has(event.cluster_template)" {
			return true
		}
	default:
		return false
	}

	// Extract ID and name from the event
	var id, name string
	switch payload := event.Payload.(type) {
	case *publicv1.Event_Cluster:
		if payload.Cluster != nil {
			id = payload.Cluster.Id
			if payload.Cluster.Metadata != nil {
				name = payload.Cluster.Metadata.Name
			}
		}
	case *publicv1.Event_ClusterTemplate:
		if payload.ClusterTemplate != nil {
			id = payload.ClusterTemplate.Id
			if payload.ClusterTemplate.Metadata != nil {
				name = payload.ClusterTemplate.Metadata.Name
			}
		}
	}

	// Check if filter contains the specific ID or name
	return strings.Contains(filter, id) || strings.Contains(filter, name)
}

// Helper function to send an event if it matches the filter
func SendEventIfMatches(event *publicv1.Event, filter string, stream publicv1.Events_WatchServer) error {
	if MatchesFilter(event, filter) {
		return stream.Send(&publicv1.EventsWatchResponse{Event: event})
	}
	return nil
}
