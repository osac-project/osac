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

	"github.com/IBM/sarama"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/kafka"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
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

var _ = Describe("Events server visibility", Ordered, func() {
	var (
		broker      *kafka.Container
		kafkaClient sarama.Client
		producer    sarama.SyncProducer
	)

	BeforeAll(func() {
		var err error
		broker, err = kafka.NewContainer().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())
		startCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		Expect(broker.Start(startCtx)).To(Succeed())
		DeferCleanup(func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Minute)
			defer stopCancel()
			Expect(broker.Stop(stopCtx)).To(Succeed())
		})
	})

	BeforeEach(func() {
		var err error
		kafkaClient, err = broker.Client()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(kafkaClient.Close)
		producer, err = sarama.NewSyncProducerFromClient(kafkaClient)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(producer.Close)
	})

	// sendEvent writes an event to the tenant topic selected from its payload metadata.
	sendEvent := func(event *privatev1.Event) {
		payloadOneof := event.ProtoReflect().Descriptor().Oneofs().ByName(eventsServerPayloadOneofField)
		payloadField := event.ProtoReflect().WhichOneof(payloadOneof)
		ExpectWithOffset(1, payloadField).ToNot(BeNil())
		payload := event.ProtoReflect().Get(payloadField).Message().Interface()
		metadataGetter, ok := payload.(interface {
			GetMetadata() *privatev1.Metadata
		})
		ExpectWithOffset(1, ok).To(BeTrue())
		data, err := proto.Marshal(event)
		Expect(err).ToNot(HaveOccurred())
		_, _, err = producer.SendMessage(&sarama.ProducerMessage{
			Topic: DefaultEventTopicPrefix + metadataGetter.GetMetadata().GetTenant(),
			Value: sarama.ByteEncoder(data),
		})
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
	}

	// startServer creates a Kafka-backed events server behind a bufconn gRPC server.
	startServer := func(tenancy auth.TenancyLogic) (*EventsServer, publicv1.EventsClient) {
		eventsServer, err := NewEventsServer().
			SetLogger(logger).
			SetKafkaClient(kafkaClient).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

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
	makeTenancy := func(tenants ...string) *auth.MockTenancyLogic {
		builder := auth.NewVisibility()
		builder.AddVisibleTenants(auth.SharedTenant)
		for _, tenant := range tenants {
			builder.AddVisibleTenants(tenant)
		}
		visibility, err := builder.Build()
		Expect(err).ToNot(HaveOccurred())
		mock := auth.NewMockTenancyLogic(ctrl)
		defaultTenant := auth.SharedTenant
		if len(tenants) > 0 {
			defaultTenant = tenants[0]
		}
		mock.EXPECT().DetermineDefaultTenant(gomock.Any()).
			Return(defaultTenant, nil).
			AnyTimes()
		mock.EXPECT().DetermineVisibility(gomock.Any()).
			Return(visibility, nil).
			AnyTimes()
		return mock
	}

	// makeTenancyWithProject creates a mock tenancy logic returning visibility of the given tenant and named project:
	makeTenancyWithProject := func(tenant, project string) *auth.MockTenancyLogic {
		builder := auth.NewVisibility()
		builder.AddVisibleTenants(auth.SharedTenant)
		builder.AddVisibleProject(tenant, project)
		visibility, err := builder.Build()
		Expect(err).ToNot(HaveOccurred())
		mock := auth.NewMockTenancyLogic(ctrl)
		mock.EXPECT().DetermineDefaultTenant(gomock.Any()).
			Return(tenant, nil).
			AnyTimes()
		mock.EXPECT().DetermineVisibility(gomock.Any()).
			Return(visibility, nil).
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
		Eventually(server.Subscriptions, 10*time.Second).Should(BeNumerically(">=", 1))

		// Return the collector and the context cancel function:
		collector = watchCollector
		cancel = watchCancel
		return
	}

	It("Delivers events when tenant is visible", func() {
		// Send an event for a visible tenant:
		server, client := startServer(makeTenancy("tenant-a"))
		collector, cancel := startWatch(server, client)
		defer cancel()
		timestamp := timestamppb.New(time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC))
		eventID := uuid.New()
		sendEvent(
			privatev1.Event_builder{
				Id:        eventID,
				Type:      privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Timestamp: timestamp,
				Cluster: privatev1.Cluster_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Tenant: "tenant-a",
					}.Build(),
				}.Build(),
			}.Build(),
		)

		// Wait till there is one event in the collector:
		Eventually(collector.Events, 10*time.Second).Should(HaveLen(1))

		// Verify that the event is for the visible tenant:
		Expect(collector.Events()[0].GetId()).To(Equal(eventID))
		Expect(collector.Events()[0].GetCluster().GetMetadata().GetTenant()).To(Equal("tenant-a"))
		Expect(collector.Events()[0].GetTimestamp().AsTime()).To(Equal(timestamp.AsTime()))
	})

	It("Delivers events from the shared tenant", func() {
		server, client := startServer(makeTenancy("tenant-a"))
		collector, cancel := startWatch(server, client)
		defer cancel()
		sendEvent(privatev1.Event_builder{
			Id:   uuid.New(),
			Type: privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			Cluster: privatev1.Cluster_builder{
				Id:       uuid.New(),
				Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
			}.Build(),
		}.Build())

		Eventually(collector.Events, 10*time.Second).Should(HaveLen(1))
		Expect(collector.Events()[0].GetCluster().GetMetadata().GetTenant()).To(Equal(auth.SharedTenant))
	})

	It("Consumes only the user and shared tenant topics", func() {
		server, client := startServer(makeTenancy("tenant-a"))
		_, cancel := startWatch(server, client)
		defer cancel()

		server.subscriptionsMutex.Lock()
		defer server.subscriptionsMutex.Unlock()
		Expect(server.subscriptions).To(HaveLen(1))
		for subscription := range server.subscriptions {
			Expect(subscription.topics).To(ConsistOf(
				DefaultEventTopicPrefix+"tenant-a",
				DefaultEventTopicPrefix+auth.SharedTenant,
			))
		}
	})

	It("Filters out events when tenant is not visible", func() {
		server, client := startServer(makeTenancy("tenant-b"))
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
		server, client := startServer(makeTenancy("tenant-a"))
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

	It("Delivers events when project is visible", func() {
		// Send an event for a visible project:
		server, client := startServer(makeTenancyWithProject("tenant-a", "project-x"))
		collector, cancel := startWatch(server, client)
		defer cancel()
		sendEvent(
			privatev1.Event_builder{
				Id:   uuid.New(),
				Type: privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Cluster: privatev1.Cluster_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "project-x",
					}.Build(),
				}.Build(),
			}.Build(),
		)

		// Wait till there is one event in the collector:
		Eventually(collector.Events, time.Second).Should(HaveLen(1))

		// Verify that the event is for the visible project:
		Expect(collector.Events()[0].GetCluster().GetMetadata().GetProject()).To(Equal("project-x"))
	})

	It("Filters out events when project is not visible", func() {
		server, client := startServer(makeTenancyWithProject("tenant-a", "project-y"))
		collector, cancel := startWatch(server, client)
		defer cancel()
		sendEvent(
			privatev1.Event_builder{
				Id:   uuid.New(),
				Type: privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
				Cluster: privatev1.Cluster_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Tenant:  "tenant-a",
						Project: "project-x",
					}.Build(),
				}.Build(),
			}.Build(),
		)
		Consistently(collector.Events, time.Second).Should(BeEmpty())
	})

	It("Delivers event with cluster payload", func() {
		// Send an event with cluster payload:
		server, client := startServer(makeTenancy("tenant-a"))
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
		server, client := startServer(makeTenancy("tenant-a"))
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
		server, client := startServer(makeTenancy("tenant-a"))
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

	// SECURITY: EventsServer.Watch uses its own CEL environment (createCelEnv, scoped to *publicv1.Event), entirely
	// separate from FilterTranslator/GenericServer/GenericDAO's public-filter-oracle fix. This is the only
	// regression coverage confirming that environment stays scoped to the public Event message.
	It("Rejects a filter referencing the private-only hub field", func() {
		_, client := startServer(makeTenancy("tenant-a"))

		// The server-streaming call returns immediately, before the server handler has run (see startWatch above),
		// so the filter-compile failure surfaces on the first Recv, not on Watch itself:
		watchStream, err := client.Watch(context.Background(), publicv1.EventsWatchRequest_builder{
			Filter: new("has(event.hub)"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = watchStream.Recv()
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
	})
})
