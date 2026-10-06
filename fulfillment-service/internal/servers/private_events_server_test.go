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
	"fmt"
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
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	"github.com/osac-project/osac/fulfillment-service/internal/validation"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// privateEventsWatchStream controls delivery through the server's exported Watch interface.
type privateEventsWatchStream struct {
	grpc.ServerStream
	ctx  context.Context
	send func(*privatev1.EventsWatchResponse) error
}

// Context returns the context controlling the test's watch subscription.
func (s *privateEventsWatchStream) Context() context.Context { return s.ctx }

// Send delegates delivery to the callback configured by the test.
func (s *privateEventsWatchStream) Send(response *privatev1.EventsWatchResponse) error {
	return s.send(response)
}

var _ = Describe("Private events server", Ordered, func() {
	var (
		broker      *kafka.Container
		kafkaConfig *sarama.Config
	)

	BeforeAll(func(ctx context.Context) {
		var err error
		broker, err = kafka.NewContainer().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(broker.Start(ctx)).To(Succeed())
		kafkaConfig, err = broker.Config()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx context.Context) {
			Expect(broker.Stop(ctx)).To(Succeed())
		})
	})

	// newServer starts a private events server over an in-memory gRPC connection and returns its client.
	// The optional prefix restricts event topics. The server and connection are closed after the test.
	newServer := func(prefixes ...string) privatev1.EventsClient {
		builder := NewPrivateEventsServer().
			SetLogger(logger).
			SetKafkaConfig(kafkaConfig).
			SetKafkaBrokers(broker.Brokers())
		if len(prefixes) > 0 {
			builder.SetKafkaTopicPrefix(prefixes[0])
		}
		server, err := builder.Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(server.Close)

		listener := bufconn.Listen(1024 * 1024)
		interceptor, err := validation.NewProtovalidateInterceptor().SetLogger(logger).Build()
		Expect(err).ToNot(HaveOccurred())
		grpcServer := grpc.NewServer(grpc.StreamInterceptor(interceptor.StreamServer))
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

	// newClient creates a client for the test broker and registers its cleanup after the test.
	newClient := func() sarama.Client {
		config, err := broker.Config()
		Expect(err).ToNot(HaveOccurred())
		client, err := sarama.NewClient([]string{broker.Brokers()}, config)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(client.Close)
		return client
	}

	// sendEvent serializes an event and publishes it synchronously to the specified topic.
	sendEvent := func(producer sarama.SyncProducer, topic string, event *privatev1.Event) {
		data, err := proto.Marshal(event)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		_, _, err = producer.SendMessage(&sarama.ProducerMessage{
			Topic: topic,
			Value: sarama.ByteEncoder(data),
		})
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
	}

	It("Exposes the group only in the private API", func() {
		private := (&privatev1.EventsWatchRequest{}).ProtoReflect().Descriptor()
		public := (&publicv1.EventsWatchRequest{}).ProtoReflect().Descriptor()
		Expect(private.Fields().ByName("group")).ToNot(BeNil())
		Expect(public.Fields().ByName("group")).To(BeNil())
	})

	It("Requires a Kafka configuration", func() {
		server, err := NewPrivateEventsServer().
			SetLogger(logger).
			Build()
		Expect(err).To(MatchError("kafka configuration is mandatory"))
		Expect(server).To(BeNil())
	})

	It("Requires Kafka bootstrap brokers", func() {
		server, err := NewPrivateEventsServer().
			SetLogger(logger).
			SetKafkaConfig(kafkaConfig).
			Build()
		Expect(err).To(MatchError("kafka brokers are mandatory"))
		Expect(server).To(BeNil())
	})

	It("Keeps the original configuration unchanged when creating independent group clients", func() {
		config := *kafkaConfig
		config.ClientID = "private-events-test"
		config.Consumer.Offsets.Initial = sarama.OffsetNewest
		config.Consumer.Return.Errors = false
		server, err := NewPrivateEventsServer().
			SetLogger(logger).
			SetKafkaConfig(&config).
			SetKafkaBrokers(broker.Brokers()).
			Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			if !server.kafkaClient.Closed() {
				Expect(server.Close()).To(Succeed())
			}
		})

		// Group construction must not depend on the metadata client's discovered brokers or configuration.
		Expect(server.Close()).To(Succeed())
		group, err := server.newConsumerGroup("config-test-" + uuid.New())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(group.Close)
		Expect(config.ClientID).To(Equal("private-events-test"))
		Expect(config.Consumer.Offsets.Initial).To(Equal(sarama.OffsetNewest))
		Expect(config.Consumer.Return.Errors).To(BeFalse())
		Expect(config.Consumer.Group.Rebalance.GroupStrategies[0].Name()).To(Equal("range"))
	})

	It("Consumes, filters and streams Kafka events without changing the Watch request or event ID", func() {
		client := newClient()
		eventsClient := newServer()
		producer, err := sarama.NewSyncProducerFromClient(client)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(producer.Close)
		topic := DefaultEventTopicPrefix + "private-watch-" + uuid.New()

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

	It("Discovers an events topic created after Watch starts", func() {
		client := newClient()
		eventsClient := newServer()
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

		topic := DefaultEventTopicPrefix + "private-watch-new-" + uuid.New()
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

	It("Streams the same event to concurrent subscriptions", func() {
		client := newClient()
		eventsClient := newServer()
		producer, err := sarama.NewSyncProducerFromClient(client)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(producer.Close)
		topic := DefaultEventTopicPrefix + "private-watch-shared-" + uuid.New()

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

	It("Rejects an invalid filter", func() {
		eventsClient := newServer()
		filter := "event.cluster =="
		stream, err := eventsClient.Watch(context.Background(), &privatev1.EventsWatchRequest{Filter: &filter})
		Expect(err).ToNot(HaveOccurred())
		_, err = stream.Recv()
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	Describe("Consumer groups", func() {
		var (
			client   sarama.Client
			admin    sarama.ClusterAdmin
			producer sarama.SyncProducer
			prefix   string
		)

		BeforeEach(func() {
			client = newClient()
			var err error
			admin, err = sarama.NewClusterAdminFromClient(newClient())
			Expect(err).ToNot(HaveOccurred())
			producer, err = sarama.NewSyncProducerFromClient(client)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(producer.Close)
			prefix = DefaultEventTopicPrefix + uuid.New() + "-"
		})

		// createTopic creates a tenant topic with one partition and one replica on the test broker.
		createTopic := func(topic string) {
			Expect(admin.CreateTopic(topic, &sarama.TopicDetail{
				NumPartitions: 1, ReplicationFactor: 1,
			}, false)).To(Succeed())
		}

		// watch starts a subscription with the given group and filter, returning its events and cancellation function.
		// Each watch uses a separate server so group tests exercise coordination through Kafka. The subscription is
		// canceled after the test, and its event channel is closed when receiving stops.
		watch := func(group, filter string) (<-chan *privatev1.Event, context.CancelFunc) {
			eventsClient := newServer(prefix)
			watchCtx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			stream, err := eventsClient.Watch(watchCtx, privatev1.EventsWatchRequest_builder{
				Group: &group, Filter: &filter,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			responses := make(chan *privatev1.Event, 100)
			go func() {
				defer GinkgoRecover()
				defer close(responses)
				for {
					response, err := stream.Recv()
					if err != nil {
						Expect(watchCtx.Err()).To(HaveOccurred(), "watch failed: %v", err)
						return
					}
					select {
					case responses <- response.GetEvent():
					case <-watchCtx.Done():
						return
					}
				}
			}()
			return responses, cancel
		}

		// waitForAssignments waits for the subscription's prefixed Kafka group to have the given partition counts
		// per member, in any order.
		// If no counts are supplied, it waits for the group to have no members.
		waitForAssignments := func(group string, counts ...int) {
			Eventually(func(g Gomega) {
				groups, err := admin.DescribeConsumerGroups([]string{"osac.system." + group})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(groups).To(HaveLen(1))
				g.Expect(groups[0].Err).To(Equal(sarama.ErrNoError))
				if len(counts) == 0 {
					g.Expect(groups[0].State).To(Equal("Empty"))
				} else {
					g.Expect(groups[0].State).To(Equal("Stable"))
				}
				g.Expect(groups[0].Members).To(HaveLen(len(counts)))
				actual := make([]int, 0, len(counts))
				for _, member := range groups[0].Members {
					assignment, err := member.GetMemberAssignment()
					g.Expect(err).ToNot(HaveOccurred())
					count := 0
					for _, partitions := range assignment.Topics {
						count += len(partitions)
					}
					actual = append(actual, count)
				}
				g.Expect(actual).To(ConsistOf(counts))
			}, 30*time.Second, 100*time.Millisecond).Should(Succeed())
		}

		// publish sends a cluster event with the supplied ID to the specified topic.
		publish := func(topic, id string) {
			sendEvent(producer, topic, privatev1.Event_builder{
				Id: id, Cluster: privatev1.Cluster_builder{Id: id}.Build(),
			}.Build())
		}

		// committedOffset reads the prefixed Kafka group's committed offset for the topic's only partition.
		committedOffset := func(group, topic string) int64 {
			offsets, err := admin.ListConsumerGroupOffsets("osac.system."+group, map[string][]int32{topic: {0}})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			block := offsets.GetBlock(topic, 0)
			ExpectWithOffset(1, block).ToNot(BeNil())
			ExpectWithOffset(1, block.Err).To(Equal(sarama.ErrNoError))
			return block.Offset
		}

		It("Prefixes Kafka consumer group names with the OSAC system namespace", func() {
			createTopic(prefix + "tenant")
			_, _ = watch("my-controller", "")
			waitForAssignments("my-controller", 1)

			groups, err := admin.ListConsumerGroups()
			Expect(err).ToNot(HaveOccurred())
			Expect(groups).To(HaveKey("osac.system.my-controller"))
			Expect(groups).ToNot(HaveKey("my-controller"))
		})

		DescribeTable("Commits events only after successful delivery", func(outcome string) {
			group := "private-watch-" + uuid.New()
			topic := prefix + "tenant"
			createTopic(topic)
			server, err := NewPrivateEventsServer().
				SetLogger(logger).
				SetKafkaConfig(kafkaConfig).
				SetKafkaBrokers(broker.Brokers()).
				SetKafkaTopicPrefix(prefix).
				Build()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(server.Close)
			watchCtx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			responses := make(chan *privatev1.EventsWatchResponse, 1)
			delivery := make(chan error, 1)
			stream := &privateEventsWatchStream{
				ctx: watchCtx,
				send: func(response *privatev1.EventsWatchResponse) error {
					responses <- response
					select {
					case err := <-delivery:
						return err
					case <-watchCtx.Done():
						return watchCtx.Err()
					}
				},
			}
			done := make(chan error, 1)
			go func() {
				done <- server.Watch(privatev1.EventsWatchRequest_builder{Group: &group}.Build(), stream)
			}()
			publish(topic, "pending-delivery")
			var response *privatev1.EventsWatchResponse
			Eventually(responses, 30*time.Second).Should(Receive(&response))
			Expect(response.GetEvent().GetId()).To(Equal("pending-delivery"))
			// Let the automatic commit interval elapse while Send is blocked.
			Consistently(func() int64 {
				return committedOffset(group, topic)
			}, 2*time.Second, 100*time.Millisecond).Should(BeNumerically("<", 1))
			switch outcome {
			case "sent":
				delivery <- nil
				Eventually(func() int64 {
					return committedOffset(group, topic)
				}, 10*time.Second).Should(Equal(int64(1)))
				cancel()
				Eventually(done, 10*time.Second).Should(Receive(BeNil()))
			case "failed":
				failure := errors.New("stream send failed")
				delivery <- failure
				Eventually(done, 10*time.Second).Should(Receive(MatchError(failure)))
			case "canceled":
				cancel()
				Eventually(done, 10*time.Second).Should(Receive(MatchError(context.Canceled)))
			}
			waitForAssignments(group)
			if outcome != "sent" {
				Expect(committedOffset(group, topic)).To(BeNumerically("<", 1))
				reconnected, _ := watch(group, "")
				var event *privatev1.Event
				Eventually(reconnected, 30*time.Second).Should(Receive(&event))
				Expect(event.GetId()).To(Equal("pending-delivery"))
			}
		},
			Entry("Successful send", "sent"),
			Entry("Failed send", "failed"),
			Entry("Canceled watch", "canceled"),
		)

		It("Leaves the group when canceled while waiting for events", func() {
			group := "private-watch-" + uuid.New()
			createTopic(prefix + "tenant")
			responses, cancel := watch(group, "")
			waitForAssignments(group, 1)
			cancel()
			Eventually(responses, 10*time.Second).Should(BeClosed())
			waitForAssignments(group)
		})

		It("Distributes tenant topics and reassigns them when a member leaves", func() {
			group := "private-watch-" + uuid.New()
			for i := range 4 {
				createTopic(fmt.Sprintf("%s%d", prefix, i))
			}
			first, cancelFirst := watch(group, "")
			second, _ := watch(group, "")
			waitForAssignments(group, 2, 2)
			for i := range 4 {
				publish(fmt.Sprintf("%s%d", prefix, i), fmt.Sprintf("event-%d", i))
			}
			ids := []string{}
			for _, responses := range []<-chan *privatev1.Event{first, second} {
				for range 2 {
					var event *privatev1.Event
					Eventually(responses, 10*time.Second).Should(Receive(&event))
					ids = append(ids, event.GetId())
				}
				Consistently(responses, 200*time.Millisecond).ShouldNot(Receive())
			}
			Expect(ids).To(ConsistOf("event-0", "event-1", "event-2", "event-3"))
			cancelFirst()
			waitForAssignments(group, 4)
			for i := range 4 {
				publish(fmt.Sprintf("%s%d", prefix, i), fmt.Sprintf("after-leave-%d", i))
			}
			ids = nil
			for range 4 {
				var event *privatev1.Event
				Eventually(second, 10*time.Second).Should(Receive(&event))
				ids = append(ids, event.GetId())
			}
			Expect(ids).To(ConsistOf("after-leave-0", "after-leave-1", "after-leave-2", "after-leave-3"))
		})

		It("Delivers independently to different groups and an empty group, applying filters", func() {
			topic := prefix + "tenant"
			createTopic(topic)
			group := "private-watch-" + uuid.New()
			first, _ := watch(group, "has(event.cluster)")
			second, _ := watch(group+"-other", "has(event.cluster)")
			independent, _ := watch("", "has(event.cluster)")
			waitForAssignments(group, 1)
			waitForAssignments(group+"-other", 1)
			// Establish the ungrouped watcher before publishing the single event under test.
			Eventually(func(g Gomega) {
				sendEvent(producer, topic, privatev1.Event_builder{
					Id: "warmup", Cluster: privatev1.Cluster_builder{}.Build(),
				}.Build())
				g.Eventually(independent, time.Second).Should(Receive())
			}, 10*time.Second).Should(Succeed())
			sendEvent(producer, topic, privatev1.Event_builder{
				Id: "rejected", Project: privatev1.Project_builder{}.Build(),
			}.Build())
			publish(topic, "accepted")
			for _, responses := range []<-chan *privatev1.Event{first, second, independent} {
				Eventually(func() string {
					select {
					case event := <-responses:
						Expect(event.GetId()).ToNot(Equal("rejected"))
						return event.GetId()
					default:
						return ""
					}
				}, 10*time.Second).Should(Equal("accepted"))
				Consistently(responses, 200*time.Millisecond).ShouldNot(Receive())
			}
		})

		It("Waits for the first tenant topic and discovers new topics without losing their first event", func() {
			group := "private-watch-" + uuid.New()
			responses, _ := watch(group, "")
			for i := range 2 {
				topic := fmt.Sprintf("%s%d", prefix, i)
				createTopic(topic)
				publish(topic, topic)
				var event *privatev1.Event
				Eventually(responses, 30*time.Second).Should(Receive(&event))
				Expect(event.GetId()).To(Equal(topic))
			}
		})

		It("Resumes committed offsets after reconnecting", func() {
			group := "private-watch-" + uuid.New()
			topic := prefix + "tenant"
			createTopic(topic)
			first, cancel := watch(group, "")
			publish(topic, "before-disconnect")
			Eventually(first, 30*time.Second).Should(Receive())
			Eventually(func() int64 {
				return committedOffset(group, topic)
			}, 10*time.Second).Should(Equal(int64(1)))
			cancel()
			waitForAssignments(group)
			publish(topic, "while-disconnected")
			second, _ := watch(group, "")
			var event *privatev1.Event
			Eventually(second, 30*time.Second).Should(Receive(&event))
			Expect(event.GetId()).To(Equal("while-disconnected"))
			Consistently(second, 200*time.Millisecond).ShouldNot(Receive())
		})
	})
})
