/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/collections"
	"github.com/osac-project/fulfillment-service/internal/events"
	"github.com/osac-project/fulfillment-service/internal/uuid"
)

// eventsCollector reads events from a Watch stream in the background and collects them for later assertions.
type eventsCollector struct {
	lock   sync.Mutex
	events []*publicv1.Event
}

// Collect reads events from the stream in a goroutine until the stream ends or the context is canceled.
func (c *eventsCollector) Collect(stream publicv1.Events_WatchClient) {
	go func() {
		defer GinkgoRecover()
		for {
			response, err := stream.Recv()
			if errors.Is(err, io.EOF) || err != nil {
				return
			}
			c.lock.Lock()
			c.events = append(c.events, response.GetEvent())
			c.lock.Unlock()
		}
	}()
}

// Events returns a snapshot of collected events, safe for use with Eventually/Consistently.
func (c *eventsCollector) Events() []*publicv1.Event {
	c.lock.Lock()
	defer c.lock.Unlock()
	result := make([]*publicv1.Event, len(c.events))
	copy(result, c.events)
	return result
}

var _ = Describe("Events server visibility", func() {
	var (
		listener *events.MockListener
		callback events.Callback
	)

	BeforeEach(func() {
		// Create a mock listener that captures the callback and blocks until the context is canceled:
		listener = events.NewMockListener(ctrl)
		listener.EXPECT().
			Listen(gomock.Any(), gomock.Any()).
			DoAndReturn(
				func(ctx context.Context, cb events.Callback) error {
					callback = cb
					<-ctx.Done()
					return ctx.Err()
				},
			).
			AnyTimes()
	})

	// sendEvent delivers an event directly through the captured listener callback.
	sendEvent := func(event *privatev1.Event) {
		err := callback(context.Background(), event)
		Expect(err).ToNot(HaveOccurred())
	}

	// startServer creates an events server with the mock listener and given tenancy, starts it behind a bufconn
	// gRPC server, and returns the events server and a connected client.
	startServer := func(tenancy auth.TenancyLogic) (*EventsServer, publicv1.EventsClient) {
		// Create the events server:
		eventsServer, err := NewEventsServer().
			SetLogger(logger).
			SetListener(listener).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Start the events server in the background:
		eventsCtx, eventsCancel := context.WithCancel(context.Background())
		go func() {
			defer GinkgoRecover()
			_ = eventsServer.Start(eventsCtx)
		}()
		DeferCleanup(eventsCancel)

		// Create the gRPC server using bufconn:
		grpcListener := bufconn.Listen(1024 * 1024)
		grpcServer := grpc.NewServer()
		publicv1.RegisterEventsServer(grpcServer, eventsServer)
		go func() {
			defer GinkgoRecover()
			_ = grpcServer.Serve(grpcListener)
		}()
		DeferCleanup(grpcServer.Stop)

		// Create the gRPC connection using bufconn:
		grpcDialer := func(context.Context, string) (result net.Conn, err error) {
			result, err = grpcListener.Dial()
			return
		}
		grpcCredentials := insecure.NewCredentials()
		grpcConnection, err := grpc.NewClient(
			"passthrough://bufnet",
			grpc.WithContextDialer(grpcDialer),
			grpc.WithTransportCredentials(grpcCredentials),
		)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(grpcConnection.Close)

		return eventsServer, publicv1.NewEventsClient(grpcConnection)
	}

	// makeTenancy creates a mock tenancy logic returning the given visible tenants:
	makeTenancy := func(tenants collections.Set[string]) *auth.MockTenancyLogic {
		mock := auth.NewMockTenancyLogic(ctrl)
		mock.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(tenants, nil).
			AnyTimes()
		return mock
	}

	// startWatch opens a Watch stream, waits for the subscription to be registered server-side, and returns a
	// collector that accumulates received events.
	startWatch := func(server *EventsServer, client publicv1.EventsClient) (collector *eventsCollector,
		cancel context.CancelFunc) {
		// Start the watch stream. Note that due to the way gRPC works, this will return immediately once the
		// transport is established, even though the subscription is not yet registered. That is why we need to
		// explicitly wait for the subscription to be registered.
		watchCtx, watchCancel := context.WithCancel(context.Background())
		watchStream, err := client.Watch(watchCtx, publicv1.EventsWatchRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Start collecting events from the stream:
		watchCollector := &eventsCollector{}
		watchCollector.Collect(watchStream)

		// Wait for the subscription to be registered:
		Eventually(server.Subscriptions, time.Second).Should(BeNumerically(">=", 1))

		// Return the collector and the context cancel function:
		collector = watchCollector
		cancel = watchCancel
		return
	}

	It("Delivers events when tenant is visible", func() {
		// Send an event for a visible tenant:
		server, client := startServer(makeTenancy(
			collections.NewSet("tenant-a"),
		))
		collector, cancel := startWatch(server, client)
		defer cancel()
		sendEvent(
			privatev1.Event_builder{
				Id:   uuid.New(),
				Type: privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Cluster: privatev1.Cluster_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Tenant: "tenant-a",
					}.Build(),
				}.Build(),
			}.Build(),
		)

		// Wait till there is one event in the collector:
		Eventually(collector.Events, time.Second).Should(HaveLen(1))

		// Verify that the event is for the visible tenant:
		Expect(collector.Events()[0].GetCluster().GetMetadata().GetTenant()).To(Equal("tenant-a"))
	})

	It("Filters out events when tenant is not visible", func() {
		server, client := startServer(makeTenancy(
			collections.NewSet("tenant-b"),
		))
		collector, cancel := startWatch(server, client)
		defer cancel()
		sendEvent(
			privatev1.Event_builder{
				Id:   uuid.New(),
				Type: privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Cluster: privatev1.Cluster_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Tenant: "tenant-a",
					}.Build(),
				}.Build(),
			}.Build(),
		)
		Consistently(collector.Events, time.Second).Should(BeEmpty())
	})

	It("Delivers only visible events when multiple are sent", func() {
		// Send two events, one for a visible tenant and one for a non-visible tenant:
		server, client := startServer(makeTenancy(
			collections.NewSet("tenant-a"),
		))
		collector, cancel := startWatch(server, client)
		defer cancel()
		sendEvent(
			privatev1.Event_builder{
				Id:   uuid.New(),
				Type: privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Cluster: privatev1.Cluster_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Tenant: "tenant-a",
					}.Build(),
				}.Build(),
			}.Build(),
		)
		sendEvent(
			privatev1.Event_builder{
				Id:   uuid.New(),
				Type: privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Cluster: privatev1.Cluster_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Tenant: "tenant-b",
					}.Build(),
				}.Build(),
			}.Build(),
		)

		// Wait till there is one event in the collector:
		Eventually(collector.Events, time.Second).Should(HaveLen(1))
		Expect(collector.Events()[0].GetCluster().GetMetadata().GetTenant()).To(Equal("tenant-a"))

		// Verify that no more events are delivered:
		Consistently(collector.Events, time.Second).Should(HaveLen(1))
	})

	It("Delivers event with cluster payload", func() {
		// Send an event with cluster payload:
		server, client := startServer(makeTenancy(
			collections.NewSet("tenant-a"),
		))
		collector, cancel := startWatch(server, client)
		defer cancel()
		sendEvent(
			privatev1.Event_builder{
				Id:   uuid.New(),
				Type: privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Cluster: privatev1.Cluster_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Tenant: "tenant-a",
					}.Build(),
				}.Build(),
			}.Build(),
		)

		// Wait till the event is delivered:
		Eventually(collector.Events, time.Second).Should(HaveLen(1))

		// Verify that the payload of the event is a cluster:
		Expect(collector.Events()[0].HasCluster()).To(BeTrue())
	})

	It("Delivers event with compute instance payload", func() {
		// Send an event with compute instance payload:
		server, client := startServer(makeTenancy(
			collections.NewSet("tenant-a"),
		))
		collector, cancel := startWatch(server, client)
		defer cancel()
		sendEvent(
			privatev1.Event_builder{
				Id:   uuid.New(),
				Type: privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				ComputeInstance: privatev1.ComputeInstance_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Tenant: "tenant-a",
					}.Build(),
				}.Build(),
			}.Build(),
		)

		// Wait till the event is delivered:
		Eventually(collector.Events, time.Second).Should(HaveLen(1))

		// Verify that the payload of the event is a compute instance:
		Expect(collector.Events()[0].HasComputeInstance()).To(BeTrue())
	})

	It("Delivers event with bare metal instance payload", func() {
		// Send an event with bare metal instance payload:
		server, client := startServer(makeTenancy(
			collections.NewSet("tenant-a"),
		))
		collector, cancel := startWatch(server, client)
		defer cancel()
		sendEvent(
			privatev1.Event_builder{
				Id:   uuid.New(),
				Type: privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				BareMetalInstance: privatev1.BareMetalInstance_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Tenant: "tenant-a",
					}.Build(),
				}.Build(),
			}.Build(),
		)

		// Wait till the event is delivered:
		Eventually(collector.Events, time.Second).Should(HaveLen(1))

		// Verify that the payload of the event is a bare metal instance:
		Expect(collector.Events()[0].HasBareMetalInstance()).To(BeTrue())
	})
})
