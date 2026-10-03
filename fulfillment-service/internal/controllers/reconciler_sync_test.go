/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controllers_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/testing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Reconciler", func() {
	var (
		builder    *controllers.ReconcilerBuilder[*privatev1.Tenant]
		service    *reconcilerTestServer
		reconciled chan string
	)

	BeforeEach(func() {
		service = &reconcilerTestServer{
			events: make(chan *privatev1.Event, 10),
		}
		server := testing.NewServer()
		DeferCleanup(server.Stop)
		privatev1.RegisterTenantsServer(server.Registrar(), service)
		privatev1.RegisterEventsServer(server.Registrar(), service)
		server.Start()
		connection, err := grpc.NewClient(server.Address(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(connection.Close)

		reconciled = make(chan string, 10)
		builder = controllers.NewReconciler[*privatev1.Tenant]().
			SetLogger(slog.New(slog.NewTextHandler(GinkgoWriter, nil))).
			SetName("test").
			SetClient(connection).
			SetSyncInterval(10 * time.Millisecond).
			SetWatchInterval(10 * time.Millisecond).
			SetFunction(func(ctx context.Context, object *privatev1.Tenant) error {
				reconciled <- object.GetId()
				return nil
			})
	})

	start := func() {
		reconciler, err := builder.Build()
		Expect(err).ToNot(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- reconciler.Start(ctx)
		}()
		DeferCleanup(func() {
			cancel()
			Eventually(done).Should(Receive(MatchError(context.Canceled)))
		})
		Eventually(service.watchCalls.Load).Should(BeNumerically(">=", 1))
	}

	DescribeTable("Synchronizes repeatedly when enabled", func(values []bool) {
		for _, value := range values {
			builder.SetSync(value)
		}
		start()
		// Startup and the initial watch kick can cause two calls; a third requires periodic sync.
		Eventually(service.listCalls.Load).Should(BeNumerically(">=", 3))
	},
		Entry("by default", []bool{}),
		Entry("explicitly", []bool{true}),
		Entry("after disabling it on the builder", []bool{false, true}),
	)

	It("Disables startup, periodic and watch-restart syncs while reconciling watched objects", func() {
		builder.SetSync(false)
		start()

		// A nil entry closes the watch so it restarts.
		service.events <- nil
		Eventually(service.watchCalls.Load).Should(BeNumerically(">=", 2))
		service.events <- privatev1.Event_builder{
			Tenant: privatev1.Tenant_builder{Id: "watched-tenant"}.Build(),
		}.Build()
		Eventually(reconciled).Should(Receive(Equal("watched-tenant")))
		Consistently(service.listCalls.Load, 100*time.Millisecond).Should(BeZero())
	})

	DescribeTable("Reconciles dependencies from events when sync is disabled", func(event *privatev1.Event) {
		service.items = []*privatev1.Tenant{privatev1.Tenant_builder{Id: "dependent-tenant"}.Build()}
		builder.SetSync(false)
		start()
		Consistently(service.listCalls.Load, 100*time.Millisecond).Should(BeZero())

		service.events <- event
		Eventually(reconciled).Should(Receive(Equal("dependent-tenant")))
		Expect(service.listCalls.Load()).To(Equal(int32(1)))
		Consistently(service.listCalls.Load, 100*time.Millisecond).Should(Equal(int32(1)))
	},
		Entry("VirtualNetwork readiness", privatev1.Event_builder{
			VirtualNetwork: privatev1.VirtualNetwork_builder{Id: "default-network"}.Build(),
		}.Build()),
		Entry("Hub availability", privatev1.Event_builder{
			Hub: privatev1.Hub_builder{Id: "hub"}.Build(),
		}.Build()),
		Entry("an event without an object payload", &privatev1.Event{}),
	)

	DescribeTable("Rejects nonpositive sync intervals when enabled", func(interval time.Duration) {
		_, err := builder.SetSyncInterval(interval).Build()
		Expect(err).To(MatchError(ContainSubstring("sync interval should be positive")))
	},
		Entry("zero", time.Duration(0)),
		Entry("negative", -time.Second),
	)

	It("Ignores the sync interval when sync is disabled", func() {
		builder.SetSync(false).SetSyncInterval(0)
		start()
		Consistently(service.listCalls.Load, 100*time.Millisecond).Should(BeZero())
	})
})

type reconcilerTestServer struct {
	privatev1.UnimplementedTenantsServer
	privatev1.UnimplementedEventsServer
	events     chan *privatev1.Event
	items      []*privatev1.Tenant
	listCalls  atomic.Int32
	watchCalls atomic.Int32
}

func (s *reconcilerTestServer) List(context.Context, *privatev1.TenantsListRequest) (*privatev1.TenantsListResponse, error) {
	s.listCalls.Add(1)
	return &privatev1.TenantsListResponse{Items: s.items}, nil
}

func (s *reconcilerTestServer) Get(_ context.Context, request *privatev1.TenantsGetRequest) (*privatev1.TenantsGetResponse, error) {
	return &privatev1.TenantsGetResponse{
		Object: privatev1.Tenant_builder{Id: request.GetId()}.Build(),
	}, nil
}

func (s *reconcilerTestServer) Watch(_ *privatev1.EventsWatchRequest, stream grpc.ServerStreamingServer[privatev1.EventsWatchResponse]) error {
	s.watchCalls.Add(1)
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case event := <-s.events:
			if event == nil {
				return status.Error(codes.Unavailable, "restart watch")
			}
			if err := stream.Send(&privatev1.EventsWatchResponse{Event: event}); err != nil {
				return err
			}
		}
	}
}
