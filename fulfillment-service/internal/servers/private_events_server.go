/*
Copyright (c) 2025 Red Hat Inc.

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
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/osac-project/osac/fulfillment-service/internal/packages"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// PrivateEventsServerBuilder configures and creates a PrivateEventsServer.
type PrivateEventsServerBuilder struct {
	logger           *slog.Logger
	kafkaConfig      *sarama.Config
	kafkaBrokers     []string
	kafkaTopicPrefix string
}

var _ privatev1.EventsServer = (*PrivateEventsServer)(nil)

// PrivateEventsServer implements the private events API, streaming filtered events to independent or grouped
// subscriptions.
type PrivateEventsServer struct {
	privatev1.UnimplementedEventsServer

	logger                  *slog.Logger
	kafkaClient             sarama.Client
	kafkaConfig             *sarama.Config
	kafkaBrokers            []string
	kafkaTopicPrefix        string
	celEnv                  *cel.Env
	kafkaSubscriptionsMutex sync.Mutex
	kafkaSubscriptions      map[*privateEventsServerSubscription]struct{}
	kafkaTopicWatcherOnce   sync.Once
	latestKafkaTopicUpdate  *privateEventsServerTopicUpdate
}

type privateEventsServerSubscription struct {
	server    *PrivateEventsServer
	ctx       context.Context
	cancel    context.CancelFunc
	logger    *slog.Logger
	consumer  sarama.Consumer
	group     sarama.ConsumerGroup
	consumers map[privateEventsServerTopicPartition]sarama.PartitionConsumer
	messages  chan *sarama.ConsumerMessage
	topics    chan privateEventsServerTopicUpdate
	filterSrc string
	filterPrg cel.Program
	stream    grpc.ServerStreamingServer[privatev1.EventsWatchResponse]
}

// NewPrivateEventsServer creates a builder for a private events server.
func NewPrivateEventsServer() *PrivateEventsServerBuilder {
	return &PrivateEventsServerBuilder{
		kafkaTopicPrefix: DefaultEventTopicPrefix,
	}
}

// SetLogger sets the logger used by the server. This is mandatory.
func (b *PrivateEventsServerBuilder) SetLogger(value *slog.Logger) *PrivateEventsServerBuilder {
	b.logger = value
	return b
}

// SetKafkaConfig sets the configuration used to connect to Kafka. This is mandatory.
func (b *PrivateEventsServerBuilder) SetKafkaConfig(value *sarama.Config) *PrivateEventsServerBuilder {
	b.kafkaConfig = value
	return b
}

// SetKafkaBrokers sets the Kafka bootstrap broker addresses. This is mandatory.
func (b *PrivateEventsServerBuilder) SetKafkaBrokers(value ...string) *PrivateEventsServerBuilder {
	b.kafkaBrokers = value
	return b
}

// SetKafkaTopicPrefix sets the prefix of the Kafka topics that contain events. This is optional and defaults to
// DefaultEventTopicPrefix.
func (b *PrivateEventsServerBuilder) SetKafkaTopicPrefix(value string) *PrivateEventsServerBuilder {
	b.kafkaTopicPrefix = value
	return b
}

func (b *PrivateEventsServerBuilder) Build() (result *PrivateEventsServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.kafkaConfig == nil {
		err = errors.New("kafka configuration is mandatory")
		return
	}
	if len(b.kafkaBrokers) == 0 {
		err = errors.New("kafka brokers are mandatory")
		return
	}
	if b.kafkaTopicPrefix == "" {
		err = errors.New("kafka topic prefix is mandatory")
		return
	}

	// Create the CEL environment:
	celEnv, err := b.createCelEnv()
	if err != nil {
		err = fmt.Errorf("failed to create CEL environment: %w", err)
		return
	}

	// Each server owns its client and keeps the original configuration unchanged.
	config := *b.kafkaConfig
	kafkaClient, err := sarama.NewClient(b.kafkaBrokers, &config)
	if err != nil {
		err = fmt.Errorf("failed to create Kafka client: %w", err)
		return
	}

	// Create the object:
	result = &PrivateEventsServer{
		logger:             b.logger,
		kafkaClient:        kafkaClient,
		kafkaConfig:        b.kafkaConfig,
		kafkaBrokers:       slices.Clone(b.kafkaBrokers),
		kafkaTopicPrefix:   b.kafkaTopicPrefix,
		celEnv:             celEnv,
		kafkaSubscriptions: map[*privateEventsServerSubscription]struct{}{},
	}
	return
}

func (b *PrivateEventsServerBuilder) createCelEnv() (result *cel.Env, err error) {
	// Declare constants for the enum types of the package:
	var options []cel.EnvOption
	protoregistry.GlobalTypes.RangeEnums(func(enumType protoreflect.EnumType) bool {
		enumDesc := enumType.Descriptor()
		packageName := string(enumDesc.FullName().Parent())
		if !slices.Contains(packages.Private, packageName) {
			return true
		}
		enumValues := enumDesc.Values()
		for i := range enumValues.Len() {
			valueDesc := enumValues.Get(i)
			valueName := string(valueDesc.Name())
			valueNumber := valueDesc.Number()
			valueConst := cel.Constant(valueName, cel.IntType, types.Int(valueNumber))
			options = append(options, valueConst)
			b.logger.Debug(
				"Added enum constant",
				slog.String("type", string(enumDesc.FullName())),
				slog.String("name", valueName),
				slog.Int64("value", int64(valueNumber)),
			)
		}
		return true
	})

	// Declare the event type:
	var eventModel *privatev1.Event
	options = append(options, cel.Types(eventModel))

	// Declare the event variable:
	eventDesc := eventModel.ProtoReflect().Descriptor()
	eventType := cel.ObjectType(string(eventDesc.FullName()))
	options = append(options, cel.Variable("event", eventType))

	// Create the CEL environment:
	result, err = cel.NewEnv(options...)
	return
}

// Close releases the Kafka client owned by the server. Stop serving watch requests before calling it.
func (s *PrivateEventsServer) Close() error {
	return s.kafkaClient.Close()
}

func (s *PrivateEventsServer) Watch(request *privatev1.EventsWatchRequest,
	stream grpc.ServerStreamingServer[privatev1.EventsWatchResponse]) (err error) {
	subscription, err := s.newSubscription(request, stream)
	if err != nil {
		return err
	}
	defer subscription.close()
	return subscription.run()
}

func (s *PrivateEventsServer) newSubscription(
	request *privatev1.EventsWatchRequest,
	stream grpc.ServerStreamingServer[privatev1.EventsWatchResponse],
) (result *privateEventsServerSubscription, err error) {
	ctx, cancel := context.WithCancel(stream.Context())
	logger := s.logger.With(slog.String("subscription", uuid.New()))

	var filterSrc string
	if request.HasFilter() {
		filterSrc = request.GetFilter()
	}
	var filterPrg cel.Program
	if filterSrc != "" {
		filterPrg, err = s.compileFilter(ctx, filterSrc)
		if err != nil {
			cancel()
			logger.ErrorContext(
				ctx,
				"Failed to compile filter",
				slog.String("filter", filterSrc),
				slog.Any("error", err),
			)
			err = grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"failed to compile filter '%s'",
				filterSrc,
			)
			return
		}
	}

	// Empty groups preserve independent delivery to every watch request.
	var consumer sarama.Consumer
	var group sarama.ConsumerGroup
	if request.GetGroup() == "" {
		consumer, err = sarama.NewConsumerFromClient(s.kafkaClient)
	} else {
		group, err = s.newConsumerGroup(request.GetGroup())
	}
	if err != nil {
		cancel()
		err = fmt.Errorf("failed to create Kafka consumer: %w", err)
		return
	}
	result = &privateEventsServerSubscription{
		server:    s,
		ctx:       ctx,
		cancel:    cancel,
		logger:    logger,
		consumer:  consumer,
		group:     group,
		consumers: map[privateEventsServerTopicPartition]sarama.PartitionConsumer{},
		messages:  make(chan *sarama.ConsumerMessage),
		topics:    make(chan privateEventsServerTopicUpdate, 1),
		filterSrc: filterSrc,
		filterPrg: filterPrg,
		stream:    stream,
	}
	result.logger.DebugContext(result.ctx, "Created subscription")
	return
}

func (s *privateEventsServerSubscription) close() {
	s.cancel()
	for _, consumer := range s.consumers {
		err := consumer.Close()
		if err != nil {
			s.logger.ErrorContext(
				s.ctx,
				"Failed to close Kafka partition consumer",
				slog.String("error", err.Error()),
			)
		}
	}
	var err error
	if s.group != nil {
		err = s.group.Close()
	} else {
		err = s.consumer.Close()
	}
	if err != nil {
		s.logger.ErrorContext(
			s.ctx,
			"Failed to close Kafka consumer",
			slog.String("error", err.Error()),
		)
	}
	s.logger.DebugContext(
		s.ctx,
		"Canceled subscription",
	)
}

func (s *privateEventsServerSubscription) run() (err error) {
	s.server.addSubscription(s)
	defer s.server.removeSubscription(s)
	if s.group != nil {
		return s.runGroup()
	}
	started := false

	for {
		select {
		case message := <-s.messages:
			err = s.processMessage(message)
			if err != nil {
				return err
			}
		case update := <-s.topics:
			if update.err != nil {
				if !started {
					return update.err
				}
				continue
			}
			offset := int64(sarama.OffsetOldest)
			if !started {
				// Messages already in these partitions predate this watch request. Partitions discovered by later
				// updates start at the oldest offset so that their first event isn't missed.
				offset = sarama.OffsetNewest
			}
			err = s.startPartitions(update.partitions, offset)
			if err != nil {
				if !started {
					return err
				}
				s.logger.ErrorContext(
					s.ctx,
					"Failed to refresh Kafka event topics",
					slog.String("error", err.Error()),
				)
				continue
			}
			started = true
		case <-s.ctx.Done():
			s.logger.DebugContext(
				s.ctx,
				"Subscription context canceled",
			)
			return nil
		}
	}
}

type privateEventsServerTopicPartition struct {
	topic     string
	partition int32
}

type privateEventsServerTopicUpdate struct {
	partitions []privateEventsServerTopicPartition
	err        error
}

func (s *privateEventsServerSubscription) startPartitions(partitions []privateEventsServerTopicPartition,
	offset int64) error {
	for _, key := range partitions {
		if s.consumers[key] != nil {
			continue
		}
		consumer, err := s.consumer.ConsumePartition(key.topic, key.partition, offset)
		if err != nil {
			return fmt.Errorf(
				"failed to consume Kafka topic '%s' partition %d: %w",
				key.topic, key.partition, err,
			)
		}
		s.consumers[key] = consumer
		s.logger.DebugContext(
			s.ctx,
			"Started consuming Kafka partition",
			slog.String("topic", key.topic),
			slog.Int("partition", int(key.partition)),
			slog.Int64("offset", offset),
		)
		go s.forwardMessages(consumer)
	}
	return nil
}

func (s *PrivateEventsServer) addSubscription(subscription *privateEventsServerSubscription) {
	s.kafkaSubscriptionsMutex.Lock()
	s.kafkaSubscriptions[subscription] = struct{}{}
	if s.latestKafkaTopicUpdate != nil {
		subscription.notifyTopicUpdate(*s.latestKafkaTopicUpdate)
	}
	s.kafkaSubscriptionsMutex.Unlock()
	s.kafkaTopicWatcherOnce.Do(func() {
		go s.watchTopics()
	})
}

func (s *PrivateEventsServer) removeSubscription(subscription *privateEventsServerSubscription) {
	s.kafkaSubscriptionsMutex.Lock()
	delete(s.kafkaSubscriptions, subscription)
	s.kafkaSubscriptionsMutex.Unlock()
}

func (s *PrivateEventsServer) watchTopics() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		if s.hasSubscriptions() {
			update := s.readTopicUpdate()
			s.publishTopicUpdate(update)
		}
		if s.kafkaClient.Closed() {
			return
		}
		<-ticker.C
	}
}

func (s *PrivateEventsServer) hasSubscriptions() bool {
	s.kafkaSubscriptionsMutex.Lock()
	defer s.kafkaSubscriptionsMutex.Unlock()
	return len(s.kafkaSubscriptions) > 0
}

func (s *PrivateEventsServer) readTopicUpdate() (result privateEventsServerTopicUpdate) {
	err := s.kafkaClient.RefreshMetadata()
	if err != nil {
		result.err = fmt.Errorf("failed to refresh Kafka metadata: %w", err)
		return
	}
	topics, err := s.kafkaClient.Topics()
	if err != nil {
		result.err = fmt.Errorf("failed to list Kafka topics: %w", err)
		return
	}
	for _, topic := range topics {
		if !strings.HasPrefix(topic, s.kafkaTopicPrefix) {
			continue
		}
		partitions, err := s.kafkaClient.Partitions(topic)
		if err != nil {
			result.err = fmt.Errorf("failed to list partitions of Kafka topic '%s': %w", topic, err)
			return
		}
		for _, partition := range partitions {
			result.partitions = append(result.partitions, privateEventsServerTopicPartition{
				topic:     topic,
				partition: partition,
			})
		}
	}
	return
}

func (s *PrivateEventsServer) publishTopicUpdate(update privateEventsServerTopicUpdate) {
	if update.err != nil {
		s.logger.Error(
			"Failed to refresh Kafka event topics",
			slog.String("error", update.err.Error()),
		)
	}
	s.kafkaSubscriptionsMutex.Lock()
	if update.err == nil {
		s.latestKafkaTopicUpdate = &update
	}
	for subscription := range s.kafkaSubscriptions {
		subscription.notifyTopicUpdate(update)
	}
	s.kafkaSubscriptionsMutex.Unlock()
}

func (s *privateEventsServerSubscription) notifyTopicUpdate(update privateEventsServerTopicUpdate) {
	select {
	case s.topics <- update:
	default:
	}
}

func (s *privateEventsServerSubscription) forwardMessages(consumer sarama.PartitionConsumer) {
	for {
		select {
		case message, ok := <-consumer.Messages():
			if !ok {
				return
			}
			select {
			case s.messages <- message:
			case <-s.ctx.Done():
				return
			}
		case consumerErr, ok := <-consumer.Errors():
			if !ok {
				return
			}
			s.logger.ErrorContext(
				s.ctx,
				"Failed to consume Kafka message",
				slog.String("error", consumerErr.Error()),
			)
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *privateEventsServerSubscription) processMessage(message *sarama.ConsumerMessage) error {
	if message == nil {
		return nil
	}
	event := &privatev1.Event{}
	err := proto.Unmarshal(message.Value, event)
	if err != nil {
		return fmt.Errorf(
			"failed to unmarshal event from Kafka topic '%s' partition %d offset %d: %w",
			message.Topic, message.Partition, message.Offset, err,
		)
	}
	accepted := true
	if s.filterPrg != nil {
		accepted, err = s.server.evalFilter(s.ctx, s.filterPrg, event)
		if err != nil {
			s.logger.DebugContext(
				s.ctx,
				"Failed to evaluate filter",
				slog.String("filter", s.filterSrc),
				slog.String("error", err.Error()),
			)
			accepted = false
		}
	}
	if !accepted {
		s.logger.DebugContext(s.ctx, "Event rejected by filter", slog.String("filter", s.filterSrc))
		return nil
	}
	s.logger.DebugContext(s.ctx, "Event accepted by filter", slog.String("filter", s.filterSrc))
	return s.stream.Send(&privatev1.EventsWatchResponse{Event: event})
}

func (s *PrivateEventsServer) compileFilter(ctx context.Context, filterSrc string) (result cel.Program, err error) {
	tree, issues := s.celEnv.Compile(filterSrc)
	err = issues.Err()
	if err != nil {
		return
	}
	result, err = s.celEnv.Program(tree)
	return
}

func (s *PrivateEventsServer) evalFilter(ctx context.Context, filterPrg cel.Program, event *privatev1.Event) (result bool,
	err error) {
	activation, err := cel.NewActivation(map[string]any{
		"event": event,
	})
	if err != nil {
		return
	}
	value, _, err := filterPrg.ContextEval(ctx, activation)
	if err != nil {
		return
	}
	result, ok := value.Value().(bool)
	if !ok {
		err = fmt.Errorf("result of filter should be a boolean, but it is of type '%T'", result)
		return
	}
	return
}

func (s *PrivateEventsServer) newConsumerGroup(name string) (sarama.ConsumerGroup, error) {
	// Each group owns its client, built from the original configuration and bootstrap brokers.
	config := *s.kafkaConfig
	config.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategyRoundRobin()}
	config.Consumer.Offsets.Initial = sarama.OffsetOldest
	config.Consumer.Return.Errors = true
	return sarama.NewConsumerGroup(s.kafkaBrokers, "osac.system."+name, &config)
}

// privateEventsServerGroupHandler forwards claims to the subscription's single stream writer. Waiting for the result
// keeps the claim alive until the message has been sent (or filtered), before marking its offset for commit.
type privateEventsServerGroupHandler struct {
	messages chan privateEventsServerGroupMessage
}

type privateEventsServerGroupMessage struct {
	ctx     context.Context
	message *sarama.ConsumerMessage
	result  chan error
}

func (h *privateEventsServerGroupHandler) Setup(sarama.ConsumerGroupSession) error {
	return nil
}

func (h *privateEventsServerGroupHandler) Cleanup(sarama.ConsumerGroupSession) error {
	return nil
}

func (h *privateEventsServerGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession,
	claim sarama.ConsumerGroupClaim) error {
	ctx := session.Context()
	for {
		select {
		case <-ctx.Done():
			return nil
		case message, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			pending := privateEventsServerGroupMessage{
				ctx:     ctx,
				message: message,
				result:  make(chan error, 1),
			}
			select {
			case h.messages <- pending:
			case <-ctx.Done():
				return nil
			}
			select {
			case err := <-pending.result:
				if err != nil {
					return err
				}
				session.MarkMessage(message, "")
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func (s *privateEventsServerSubscription) runGroup() error {
	handler := &privateEventsServerGroupHandler{messages: make(chan privateEventsServerGroupMessage)}
	var topics []string
	var cancel context.CancelFunc
	var done <-chan error
	stop := func() {
		if cancel != nil {
			cancel()
			<-done
			cancel = nil
			done = nil
		}
	}
	defer stop()
	for {
		select {
		case <-s.ctx.Done():
			return nil
		case err := <-s.group.Errors():
			return fmt.Errorf("failed to consume Kafka group events: %w", err)
		case err := <-done:
			// The result was consumed here, so the deferred stop must not wait for it again.
			cancel()
			cancel = nil
			return err
		case pending := <-handler.messages:
			err := pending.ctx.Err()
			if err != nil {
				pending.result <- err
				continue
			}
			err = s.processMessage(pending.message)
			pending.result <- err
			if err != nil {
				return err
			}
		case update := <-s.topics:
			if update.err != nil {
				if cancel == nil {
					return update.err
				}
				continue
			}
			updated := make([]string, 0, len(update.partitions))
			for _, partition := range update.partitions {
				updated = append(updated, partition.topic)
			}
			slices.Sort(updated)
			updated = slices.Compact(updated)
			if slices.Equal(topics, updated) {
				continue
			}
			// Restart Consume with the new subscription when tenants are added or removed. Unchanged metadata
			// must not interrupt an active session. Kafka itself handles member and partition rebalances.
			stop()
			topics = updated
			if len(topics) == 0 {
				continue
			}
			cancel, done = s.startGroup(topics, handler)
		}
	}
}

func (s *privateEventsServerSubscription) startGroup(topics []string, handler sarama.ConsumerGroupHandler) (
	context.CancelFunc, <-chan error) {
	ctx, cancel := context.WithCancel(s.ctx)
	done := make(chan error, 1)
	go func() {
		for ctx.Err() == nil {
			if err := s.group.Consume(ctx, topics, handler); err != nil {
				done <- err
				return
			}
		}
		done <- ctx.Err()
	}()
	return cancel, done
}
