/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers_test

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/IBM/sarama"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/kafka"
	"github.com/osac-project/osac/fulfillment-service/internal/servers"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Private events server", Ordered, func() {
	var (
		broker     *kafka.Container
		testLogger *slog.Logger
	)

	BeforeAll(func() {
		var err error
		testLogger = slog.New(slog.NewTextHandler(GinkgoWriter, nil))
		startCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		broker, err = kafka.NewContainer().
			SetLogger(testLogger).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(broker.Start(startCtx)).To(Succeed())
		DeferCleanup(func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Minute)
			defer stopCancel()
			Expect(broker.Stop(stopCtx)).To(Succeed())
		})
	})

	startServer := func(client sarama.Client) privatev1.EventsClient {
		server, err := servers.NewPrivateEventsServer().
			SetLogger(testLogger).
			SetKafkaClient(client).
			Build()
		Expect(err).ToNot(HaveOccurred())

		listener := bufconn.Listen(1024 * 1024)
		grpcServer := grpc.NewServer()
		privatev1.RegisterEventsServer(grpcServer, server)
		go func() {
			defer GinkgoRecover()
			Expect(grpcServer.Serve(listener)).To(Or(Succeed(), MatchError(grpc.ErrServerStopped)))
		}()
		DeferCleanup(func() {
			grpcServer.Stop()
			Expect(listener.Close()).To(Succeed())
		})

		connection, err := grpc.NewClient(
			"passthrough:///bufconn",
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
				return listener.Dial()
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(connection.Close)
		return privatev1.NewEventsClient(connection)
	}

	newClient := func() sarama.Client {
		client, err := broker.Client()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(client.Close)
		return client
	}

	sendEvent := func(producer sarama.SyncProducer, topic string, event *privatev1.Event) {
		data, err := proto.Marshal(event)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		_, _, err = producer.SendMessage(&sarama.ProducerMessage{
			Topic: topic,
			Value: sarama.ByteEncoder(data),
		})
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
	}

	It("requires a Kafka client", func() {
		server, err := servers.NewPrivateEventsServer().
			SetLogger(testLogger).
			Build()
		Expect(err).To(MatchError("kafka client is mandatory"))
		Expect(server).To(BeNil())
	})

	It("consumes, filters and streams Kafka events without changing the Watch request or event ID", func() {
		client := newClient()
		eventsClient := startServer(client)
		producer, err := sarama.NewSyncProducerFromClient(client)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(producer.Close)
		topic := servers.DefaultEventTopicPrefix + "private-watch-" + uuid.New()

		// Create the topic before Watch starts. Existing topics begin at the newest offset, so this seed event must not
		// be replayed to the new subscription.
		sendEvent(producer, topic, privatev1.Event_builder{
			Project: privatev1.Project_builder{Id: "seed"}.Build(),
		}.Build())

		watchCtx, cancel := context.WithCancel(context.Background())
		filter := "has(event.cluster)"
		stream, err := eventsClient.Watch(watchCtx, &privatev1.EventsWatchRequest{Filter: &filter})
		Expect(err).ToNot(HaveOccurred())
		responses := make(chan *privatev1.EventsWatchResponse, 2)
		receiveErrors := make(chan error, 1)
		go func() {
			for {
				response, receiveErr := stream.Recv()
				if receiveErr != nil {
					receiveErrors <- receiveErr
					return
				}
				responses <- response
			}
		}()

		// Establish that the subscription is active using only its streamed response. Retrying the publish avoids
		// depending on consumer implementation details while Kafka metadata and the partition consumer initialize.
		warmup := privatev1.Event_builder{
			Cluster: privatev1.Cluster_builder{Id: "warmup"}.Build(),
		}.Build()
		var response *privatev1.EventsWatchResponse
		Eventually(func() bool {
			sendEvent(producer, topic, warmup)
			select {
			case response = <-responses:
				return true
			case <-time.After(100 * time.Millisecond):
				return false
			}
		}, 10*time.Second, 200*time.Millisecond).Should(BeTrue())
		Expect(response.GetEvent().GetCluster().GetId()).To(Equal("warmup"))

		rejected := privatev1.Event_builder{
			Project: privatev1.Project_builder{Id: "rejected"}.Build(),
		}.Build()
		accepted := privatev1.Event_builder{
			Id:      "accepted-event-id",
			Cluster: privatev1.Cluster_builder{Id: "accepted"}.Build(),
		}.Build()
		sendEvent(producer, topic, rejected)
		sendEvent(producer, topic, accepted)

		Eventually(func() bool {
			select {
			case response = <-responses:
				return response.GetEvent().GetCluster().GetId() == "accepted"
			default:
				return false
			}
		}, 10*time.Second).Should(BeTrue())
		Expect(response.GetEvent().GetId()).To(Equal("accepted-event-id"))
		Consistently(responses).ShouldNot(Receive())

		cancel()
		Eventually(receiveErrors).Should(Receive())
	})

	It("discovers an events topic created after Watch starts", func() {
		client := newClient()
		eventsClient := startServer(client)
		producer, err := sarama.NewSyncProducerFromClient(client)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(producer.Close)

		watchCtx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		stream, err := eventsClient.Watch(watchCtx, &privatev1.EventsWatchRequest{})
		Expect(err).ToNot(HaveOccurred())
		responses := make(chan *privatev1.EventsWatchResponse, 1)
		receiveErrors := make(chan error, 1)
		go func() {
			response, receiveErr := stream.Recv()
			if receiveErr != nil {
				receiveErrors <- receiveErr
				return
			}
			responses <- response
		}()

		topic := servers.DefaultEventTopicPrefix + "private-watch-new-" + uuid.New()
		event := privatev1.Event_builder{
			Cluster: privatev1.Cluster_builder{Id: "new-topic"}.Build(),
		}.Build()
		var response *privatev1.EventsWatchResponse
		Eventually(func() bool {
			sendEvent(producer, topic, event)
			select {
			case response = <-responses:
				return true
			case err = <-receiveErrors:
				Expect(err).ToNot(HaveOccurred())
				return false
			case <-time.After(100 * time.Millisecond):
				return false
			}
		}, 10*time.Second, 200*time.Millisecond).Should(BeTrue())
		Expect(response.GetEvent().GetCluster().GetId()).To(Equal("new-topic"))
	})

	It("streams the same event to concurrent subscriptions", func() {
		client := newClient()
		eventsClient := startServer(client)
		producer, err := sarama.NewSyncProducerFromClient(client)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(producer.Close)
		topic := servers.DefaultEventTopicPrefix + "private-watch-shared-" + uuid.New()

		// Ensure that the topic exists before the subscriptions start.
		sendEvent(producer, topic, privatev1.Event_builder{
			Cluster: privatev1.Cluster_builder{Id: "seed"}.Build(),
		}.Build())

		responses := []chan *privatev1.EventsWatchResponse{
			make(chan *privatev1.EventsWatchResponse, 1),
			make(chan *privatev1.EventsWatchResponse, 1),
		}
		receiveErrors := make(chan error, len(responses))
		for i := range responses {
			watchCtx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			stream, err := eventsClient.Watch(watchCtx, &privatev1.EventsWatchRequest{})
			Expect(err).ToNot(HaveOccurred())
			go func() {
				response, receiveErr := stream.Recv()
				if receiveErr != nil {
					receiveErrors <- receiveErr
					return
				}
				responses[i] <- response
			}()
		}

		event := privatev1.Event_builder{
			Cluster: privatev1.Cluster_builder{Id: "shared"}.Build(),
		}.Build()
		received := make([]bool, len(responses))
		Eventually(func() bool {
			sendEvent(producer, topic, event)
			for i := range responses {
				select {
				case response := <-responses[i]:
					Expect(response.GetEvent().GetCluster().GetId()).To(Equal("shared"))
					received[i] = true
				case receiveErr := <-receiveErrors:
					Expect(receiveErr).ToNot(HaveOccurred())
				default:
				}
			}
			return received[0] && received[1]
		}, 10*time.Second, 200*time.Millisecond).Should(BeTrue())
	})

	It("rejects an invalid filter", func() {
		eventsClient := startServer(newClient())
		filter := "event.cluster =="
		stream, err := eventsClient.Watch(context.Background(), &privatev1.EventsWatchRequest{Filter: &filter})
		Expect(err).ToNot(HaveOccurred())
		_, err = stream.Recv()
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})
})
