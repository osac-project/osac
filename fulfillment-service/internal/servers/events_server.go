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

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/packages"
	"github.com/osac-project/osac/fulfillment-service/internal/util"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// EventsServerBuilder contains the data and logic needed to create an EventsServer.
type EventsServerBuilder struct {
	logger           *slog.Logger
	kafkaClient      sarama.Client
	kafkaTopicPrefix string
	tenancyLogic     auth.TenancyLogic
}

var _ publicv1.EventsServer = (*EventsServer)(nil)

type EventsServer struct {
	publicv1.UnimplementedEventsServer

	logger           *slog.Logger
	kafkaClient      sarama.Client
	kafkaTopicPrefix string
	celEnv           *cel.Env
	mapper           *GenericMapper[*privatev1.Event, *publicv1.Event]
	tenancyLogic     auth.TenancyLogic
	payloadOneof     protoreflect.OneofDescriptor

	subscriptionsMutex sync.Mutex
	subscriptions      map[*eventsSubscription]struct{}
}

type eventsSubscription struct {
	server        *EventsServer
	ctx           context.Context
	cancel        context.CancelFunc
	logger        *slog.Logger
	consumer      sarama.Consumer
	consumers     map[kafkaTopicPartition]sarama.PartitionConsumer
	messages      chan *sarama.ConsumerMessage
	topics        []string
	allowedTopics map[string]struct{}
	visibility    *auth.Visibility
	filterSrc     string
	filterPrg     cel.Program
	stream        grpc.ServerStreamingServer[publicv1.EventsWatchResponse]
}

func NewEventsServer() *EventsServerBuilder {
	return &EventsServerBuilder{
		kafkaTopicPrefix: DefaultEventTopicPrefix,
	}
}

func (b *EventsServerBuilder) SetLogger(value *slog.Logger) *EventsServerBuilder {
	b.logger = value
	return b
}

// SetKafkaClient sets the client used to consume events from Kafka. This is mandatory.
func (b *EventsServerBuilder) SetKafkaClient(value sarama.Client) *EventsServerBuilder {
	b.kafkaClient = value
	return b
}

// SetKafkaTopicPrefix sets the prefix of the Kafka topics that contain events. This is optional and defaults to
// DefaultEventTopicPrefix.
func (b *EventsServerBuilder) SetKafkaTopicPrefix(value string) *EventsServerBuilder {
	b.kafkaTopicPrefix = value
	return b
}

func (b *EventsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *EventsServerBuilder {
	b.tenancyLogic = util.NormalizeNil(value)
	return b
}

func (b *EventsServerBuilder) Build() (result *EventsServer, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.kafkaClient == nil {
		err = errors.New("kafka client is mandatory")
		return
	}
	if b.kafkaTopicPrefix == "" {
		err = errors.New("kafka topic prefix is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	celEnv, err := b.createCelEnv()
	if err != nil {
		err = fmt.Errorf("failed to create CEL environment: %w", err)
		return
	}
	mapper, err := NewGenericMapper[*privatev1.Event, *publicv1.Event]().
		SetLogger(b.logger).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create mapper: %w", err)
		return
	}
	payloadOneof, err := b.findPayloadOneof()
	if err != nil {
		return
	}

	result = &EventsServer{
		logger:           b.logger,
		kafkaClient:      b.kafkaClient,
		kafkaTopicPrefix: b.kafkaTopicPrefix,
		celEnv:           celEnv,
		mapper:           mapper,
		tenancyLogic:     b.tenancyLogic,
		payloadOneof:     payloadOneof,
		subscriptions:    map[*eventsSubscription]struct{}{},
	}
	return
}

// findPayloadOneof returns the descriptor of the payload oneof field in the event message. Returns an error if the
// oneof is not found.
func (b *EventsServerBuilder) findPayloadOneof() (result protoreflect.OneofDescriptor, err error) {
	var eventTempl *privatev1.Event
	eventDesc := eventTempl.ProtoReflect().Descriptor()
	payloadDesc := eventDesc.Oneofs().ByName(eventsServerPayloadOneofField)
	if payloadDesc == nil {
		err = fmt.Errorf(
			"event message '%s' has no '%s' oneof",
			eventDesc.FullName(), eventsServerPayloadOneofField,
		)
		return
	}
	result = payloadDesc
	return
}

func (b *EventsServerBuilder) createCelEnv() (result *cel.Env, err error) {
	var options []cel.EnvOption
	protoregistry.GlobalTypes.RangeEnums(func(enumType protoreflect.EnumType) bool {
		enumDesc := enumType.Descriptor()
		packageName := string(enumDesc.FullName().Parent())
		if !slices.Contains(packages.Public, packageName) {
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

	var eventModel *publicv1.Event
	options = append(options, cel.Types(eventModel))
	eventDesc := eventModel.ProtoReflect().Descriptor()
	eventType := cel.ObjectType(string(eventDesc.FullName()))
	options = append(options, cel.Variable("event", eventType))
	result, err = cel.NewEnv(options...)
	return
}

// Subscriptions returns the number of active subscriptions. This is intended for use in tests, where it is important
// to wait for a subscription to be ready before sending events.
func (s *EventsServer) Subscriptions() int {
	s.subscriptionsMutex.Lock()
	defer s.subscriptionsMutex.Unlock()
	return len(s.subscriptions)
}

func (s *EventsServer) Watch(request *publicv1.EventsWatchRequest,
	stream grpc.ServerStreamingServer[publicv1.EventsWatchResponse]) error {
	subscription, err := s.newSubscription(request, stream)
	if err != nil {
		return err
	}
	defer subscription.close()
	return subscription.run()
}

func (s *EventsServer) newSubscription(
	request *publicv1.EventsWatchRequest,
	stream grpc.ServerStreamingServer[publicv1.EventsWatchResponse],
) (result *eventsSubscription, err error) {
	ctx, cancel := context.WithCancel(stream.Context())
	logger := s.logger.With(slog.String("subscription", uuid.New()))

	tenant, err := s.tenancyLogic.DetermineDefaultTenant(ctx)
	if err != nil || tenant == "" {
		cancel()
		logger.ErrorContext(ctx, "Failed to determine tenant", slog.Any("error", err))
		err = grpcstatus.Error(grpccodes.Internal, "failed to determine tenant")
		return
	}
	visibility, err := s.tenancyLogic.DetermineVisibility(ctx)
	if err != nil {
		cancel()
		logger.ErrorContext(ctx, "Failed to determine visibility", slog.Any("error", err))
		err = grpcstatus.Error(grpccodes.Internal, "failed to determine visibility")
		return
	}

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
			err = grpcstatus.Errorf(grpccodes.InvalidArgument, "failed to compile filter '%s'", filterSrc)
			return
		}
	}

	consumer, err := sarama.NewConsumerFromClient(s.kafkaClient)
	if err != nil {
		cancel()
		err = fmt.Errorf("failed to create Kafka consumer: %w", err)
		return
	}
	topics := []string{s.kafkaTopicPrefix + tenant}
	if tenant != auth.SharedTenant {
		topics = append(topics, s.kafkaTopicPrefix+auth.SharedTenant)
	}
	allowedTopics := make(map[string]struct{}, len(topics))
	for _, topic := range topics {
		allowedTopics[topic] = struct{}{}
	}
	result = &eventsSubscription{
		server:        s,
		ctx:           ctx,
		cancel:        cancel,
		logger:        logger,
		consumer:      consumer,
		consumers:     map[kafkaTopicPartition]sarama.PartitionConsumer{},
		messages:      make(chan *sarama.ConsumerMessage),
		topics:        topics,
		allowedTopics: allowedTopics,
		visibility:    visibility,
		filterSrc:     filterSrc,
		filterPrg:     filterPrg,
		stream:        stream,
	}
	logger.DebugContext(ctx, "Created subscription", slog.Any("topics", topics))
	return
}

func (s *eventsSubscription) run() error {
	// Existing partitions start at the newest offset, because events written before the Watch request must not be
	// replayed. A topic or partition discovered later starts at the oldest offset so its first event isn't missed.
	err := s.refreshPartitions(sarama.OffsetNewest)
	if err != nil {
		return err
	}
	s.server.addSubscription(s)
	defer s.server.removeSubscription(s)

	ticker := time.NewTicker(eventsTopicRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case message := <-s.messages:
			err = s.processMessage(message)
			if err != nil {
				return err
			}
		case <-ticker.C:
			err = s.refreshPartitions(sarama.OffsetOldest)
			if err != nil {
				s.logger.ErrorContext(
					s.ctx,
					"Failed to refresh Kafka event topics",
					slog.String("error", err.Error()),
				)
			}
		case <-s.ctx.Done():
			s.logger.DebugContext(s.ctx, "Subscription context canceled")
			return nil
		}
	}
}

func (s *EventsServer) addSubscription(subscription *eventsSubscription) {
	s.subscriptionsMutex.Lock()
	s.subscriptions[subscription] = struct{}{}
	s.subscriptionsMutex.Unlock()
}

func (s *EventsServer) removeSubscription(subscription *eventsSubscription) {
	s.subscriptionsMutex.Lock()
	delete(s.subscriptions, subscription)
	s.subscriptionsMutex.Unlock()
}

func (s *eventsSubscription) close() {
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
	err := s.consumer.Close()
	if err != nil {
		s.logger.ErrorContext(s.ctx, "Failed to close Kafka consumer", slog.String("error", err.Error()))
	}
	s.logger.DebugContext(s.ctx, "Canceled subscription")
}

func (s *eventsSubscription) refreshPartitions(offset int64) error {
	for _, topic := range s.topics {
		err := s.server.kafkaClient.RefreshMetadata(topic)
		if isKafkaTopicUnavailable(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to refresh Kafka topic '%s': %w", topic, err)
		}
		partitions, err := s.server.kafkaClient.Partitions(topic)
		if isKafkaTopicUnavailable(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to list partitions of Kafka topic '%s': %w", topic, err)
		}
		for _, partition := range partitions {
			key := kafkaTopicPartition{topic: topic, partition: partition}
			if s.consumers[key] != nil {
				continue
			}
			consumer, err := s.consumer.ConsumePartition(topic, partition, offset)
			if err != nil {
				return fmt.Errorf("failed to consume Kafka topic '%s' partition %d: %w", topic, partition, err)
			}
			s.consumers[key] = consumer
			s.logger.DebugContext(
				s.ctx,
				"Started consuming Kafka partition",
				slog.String("topic", topic),
				slog.Int("partition", int(partition)),
				slog.Int64("offset", offset),
			)
			go s.forwardMessages(consumer)
		}
	}
	return nil
}

func isKafkaTopicUnavailable(err error) bool {
	return errors.Is(err, sarama.ErrUnknownTopicOrPartition) || errors.Is(err, sarama.ErrLeaderNotAvailable)
}

func (s *eventsSubscription) forwardMessages(consumer sarama.PartitionConsumer) {
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

func (s *eventsSubscription) processMessage(message *sarama.ConsumerMessage) error {
	if message == nil {
		return nil
	}
	if _, ok := s.allowedTopics[message.Topic]; !ok {
		return fmt.Errorf("received event from unexpected Kafka topic '%s'", message.Topic)
	}
	private := &privatev1.Event{}
	err := proto.Unmarshal(message.Value, private)
	if err != nil {
		return fmt.Errorf(
			"failed to unmarshal event from Kafka topic '%s' partition %d offset %d: %w",
			message.Topic, message.Partition, message.Offset, err,
		)
	}

	// Signal events and objects without a public representation are private-only.
	if private.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_SIGNALED || private.HasHub() {
		return nil
	}
	metadata := s.server.extractMetadata(s.ctx, private)
	if metadata == nil || !s.visibility.IsProjectVisible(metadata.GetTenant(), metadata.GetProject()) {
		return nil
	}
	public := &publicv1.Event{}
	err = s.server.mapper.Copy(s.ctx, private, public)
	if err != nil {
		return fmt.Errorf("failed to translate event: %w", err)
	}

	accepted := true
	if s.filterPrg != nil {
		accepted, err = s.server.evalFilter(s.ctx, s.filterPrg, public)
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
	return s.stream.Send(publicv1.EventsWatchResponse_builder{Event: public}.Build())
}

func (s *EventsServer) compileFilter(ctx context.Context, filterSrc string) (result cel.Program, err error) {
	tree, issues := s.celEnv.Compile(filterSrc)
	err = issues.Err()
	if err != nil {
		return
	}
	result, err = s.celEnv.Program(tree)
	return
}

func (s *EventsServer) evalFilter(ctx context.Context, filterPrg cel.Program, event *publicv1.Event) (result bool,
	err error) {
	activation, err := cel.NewActivation(map[string]any{"event": event})
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

// extractMetadata extracts the metadata from the event payload. Returns nil if the metadata is not found.
func (s *EventsServer) extractMetadata(ctx context.Context, event *privatev1.Event) *privatev1.Metadata {
	payloadMessage, err := s.extractPayload(ctx, event)
	if err != nil || payloadMessage == nil {
		return nil
	}
	type payloadIface interface {
		GetMetadata() *privatev1.Metadata
	}
	payload, ok := payloadMessage.(payloadIface)
	if !ok {
		s.logger.ErrorContext(
			ctx,
			"Event payload does not have a method to get the metadata",
			slog.Any("payload", fmt.Sprintf("%T", payloadMessage)),
		)
		return nil
	}
	return payload.GetMetadata()
}

// extractPayload extracts the payload from the event message. For example, if the event is about a cluster, it gets
// the value of the 'cluster' field of the payload oneof. Returns nil if there is no payload.
func (s *EventsServer) extractPayload(ctx context.Context, event *privatev1.Event) (result proto.Message, err error) {
	eventReflect := event.ProtoReflect()
	payloadDesc := eventReflect.WhichOneof(s.payloadOneof)
	if payloadDesc == nil {
		s.logger.ErrorContext(ctx, "Event has no payload field")
		return
	}
	payloadValue := eventReflect.Get(payloadDesc)
	result = payloadValue.Message().Interface()
	return
}

const (
	eventsServerPayloadOneofField = "payload"
	eventsTopicRefreshInterval    = time.Second
)
