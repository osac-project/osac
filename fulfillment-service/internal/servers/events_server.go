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

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/collections"
	"github.com/osac-project/fulfillment-service/internal/events"
	"github.com/osac-project/fulfillment-service/internal/packages"
	"github.com/osac-project/fulfillment-service/internal/util"
	"github.com/osac-project/fulfillment-service/internal/uuid"
)

// EventsServerBuilder contains the data and logic needed to create an EventsServer.
type EventsServerBuilder struct {
	logger       *slog.Logger
	listener     events.Listener
	tenancyLogic auth.TenancyLogic
}

var _ publicv1.EventsServer = (*EventsServer)(nil)

type EventsServer struct {
	publicv1.UnimplementedEventsServer

	logger       *slog.Logger
	listener     events.Listener
	subs         map[string]eventsServerSubInfo
	subsLock     *sync.RWMutex
	celEnv       *cel.Env
	mapper       *GenericMapper[*privatev1.Event, *publicv1.Event]
	tenancyLogic auth.TenancyLogic
	payloadOneof protoreflect.OneofDescriptor
}

type eventsServerSubInfo struct {
	stream     grpc.ServerStreamingServer[publicv1.EventsWatchResponse]
	tenants    collections.Set[string]
	filterSrc  string
	filterPrg  cel.Program
	eventsChan chan *publicv1.Event
}

func NewEventsServer() *EventsServerBuilder {
	return &EventsServerBuilder{}
}

func (b *EventsServerBuilder) SetLogger(value *slog.Logger) *EventsServerBuilder {
	b.logger = value
	return b
}

// SetListener sets the listener that will be used to receive event notifications. This is mandatory.
func (b *EventsServerBuilder) SetListener(value events.Listener) *EventsServerBuilder {
	b.listener = util.NormalizeNil(value)
	return b
}

func (b *EventsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *EventsServerBuilder {
	b.tenancyLogic = util.NormalizeNil(value)
	return b
}

func (b *EventsServerBuilder) Build() (result *EventsServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.listener == nil {
		err = errors.New("listener is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create  the CEL environment:
	celEnv, err := b.createCelEnv()
	if err != nil {
		err = fmt.Errorf("failed to create CEL environment: %w", err)
		return
	}

	// Create the mappers:
	mapper, err := NewGenericMapper[*privatev1.Event, *publicv1.Event]().
		SetLogger(b.logger).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create mapper: %w", err)
		return
	}

	// Look up the payload oneof and metadata field descriptors:
	payloadOneof, err := b.findPayloadOneof()
	if err != nil {
		return
	}

	// Create the object early so that we can use its methods as callback functions:
	result = &EventsServer{
		logger:       b.logger,
		listener:     b.listener,
		subs:         map[string]eventsServerSubInfo{},
		subsLock:     &sync.RWMutex{},
		celEnv:       celEnv,
		mapper:       mapper,
		tenancyLogic: b.tenancyLogic,
		payloadOneof: payloadOneof,
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
	// Declare constants for the enum types of the package:
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

	// Declare the event type:
	var eventModel *publicv1.Event
	options = append(options, cel.Types(eventModel))

	// Declare the event variable:
	eventDesc := eventModel.ProtoReflect().Descriptor()
	eventType := cel.ObjectType(string(eventDesc.FullName()))
	options = append(options, cel.Variable("event", eventType))

	// Create the CEL environment:
	result, err = cel.NewEnv(options...)
	return
}

// Starts starts the background components of the server, in particular the notification listener. This is a blocking
// operation, and will return only when the context is canceled.
func (s *EventsServer) Start(ctx context.Context) error {
	return s.listener.Listen(ctx, s.processPayload)
}

// Subscriptions returns the number of active subscriptions. This is intended for use in tests, where it is important
// to wait for a subscription to be registered before sending events.
func (s *EventsServer) Subscriptions() int {
	s.subsLock.RLock()
	defer s.subsLock.RUnlock()
	return len(s.subs)
}

func (s *EventsServer) Watch(request *publicv1.EventsWatchRequest,
	stream grpc.ServerStreamingServer[publicv1.EventsWatchResponse]) (err error) {
	// Get the context:
	ctx := stream.Context()

	// Determine the visible tenants:
	tenants, err := s.tenancyLogic.DetermineVisibleTenants(ctx)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to determine visible tenants",
			slog.Any("error", err),
		)
		return grpcstatus.Errorf(grpccodes.Internal, "failed to determine visible tenants")
	}

	// Compile the filter expression:
	var (
		filterSrc string
		filterPrg cel.Program
	)
	if request.Filter != nil {
		filterSrc = *request.Filter
		if filterSrc != "" {
			filterPrg, err = s.compileFilter(ctx, filterSrc)
			if err != nil {
				s.logger.ErrorContext(
					ctx,
					"Failed to compile filter",
					slog.String("filter", filterSrc),
					slog.Any("error", err),
				)
				return grpcstatus.Errorf(
					grpccodes.InvalidArgument,
					"failed to compile filter '%s'",
					filterSrc,
				)
			}
		}
	}

	// Create a subscription and remember to remove it when done:
	subId := uuid.New()
	logger := s.logger.With(
		slog.String("subscription", subId),
	)
	subInfo := eventsServerSubInfo{
		stream:     stream,
		tenants:    tenants,
		filterSrc:  filterSrc,
		filterPrg:  filterPrg,
		eventsChan: make(chan *publicv1.Event),
	}
	s.subsLock.Lock()
	s.subs[subId] = subInfo
	s.subsLock.Unlock()
	logger.DebugContext(ctx, "Created subscription")
	defer func() {
		s.subsLock.Lock()
		defer s.subsLock.Unlock()
		delete(s.subs, subId)
		close(subInfo.eventsChan)
		logger.DebugContext(ctx, "Canceled subcription")
	}()

	// Wait to receive events on the channel of the subscription and forward them to the client:
	for {
		select {
		case event, ok := <-subInfo.eventsChan:
			if !ok {
				logger.DebugContext(ctx, "Subscription channel closed")
				return nil
			}
			err = stream.Send(publicv1.EventsWatchResponse_builder{
				Event: event,
			}.Build())
			if err != nil {
				return err
			}
		case <-stream.Context().Done():
			s.logger.DebugContext(ctx, "Subscription context canceled")
			return nil
		}
	}
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

func (s *EventsServer) processPayload(ctx context.Context, payload proto.Message) error {
	// Get the object:
	private, ok := payload.(*privatev1.Event)
	if !ok {
		s.logger.ErrorContext(
			ctx,
			"Unexpected payload type",
			slog.String("expected", fmt.Sprintf("%T", private)),
			slog.String("actual", fmt.Sprintf("%T", payload)),
		)
		return nil
	}

	// Skip signal events:
	if private.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_SIGNALED {
		return nil
	}

	// Skip objects that don't have a public representation:
	if private.HasHub() {
		return nil
	}

	// Translate the private event to a public event and process it:
	public := &publicv1.Event{}
	err := s.mapper.Copy(ctx, private, public)
	if err != nil {
		return fmt.Errorf("failed to translate event: %w", err)
	}
	return s.processEvent(ctx, public, private)
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

// extractPayload extracts the payload from the event message. For example, if the event is about a cluster, it will
// get the value of the 'cluster' field of the payload oneof. Returns nil if there is no payload.
func (s *EventsServer) extractPayload(ctx context.Context, event *privatev1.Event) (result proto.Message, err error) {
	eventReflect := event.ProtoReflect()
	payloadDesc := eventReflect.WhichOneof(s.payloadOneof)
	if payloadDesc == nil {
		s.logger.ErrorContext(
			ctx,
			"Event has no payload field",
		)
		return
	}
	payloadValue := eventReflect.Get(payloadDesc)
	payloadReflect := payloadValue.Message()
	result = payloadReflect.Interface()
	return
}

func (s *EventsServer) processEvent(ctx context.Context, public *publicv1.Event, private *privatev1.Event) error {
	s.subsLock.RLock()
	defer s.subsLock.RUnlock()
	for subId, sub := range s.subs {
		logger := s.logger.With(
			slog.String("filter", sub.filterSrc),
			slog.String("sub", subId),
			slog.Any("public", public),
			slog.Any("private", private),
		)
		accepted := true

		// Check if the user has permission to see the event:
		metadata := s.extractMetadata(ctx, private)
		if metadata == nil {
			continue
		}
		tenant := metadata.GetTenant()
		visible := sub.tenants.Contains(tenant)
		if !visible {
			continue
		}

		// Apply user-defined filter:
		if sub.filterPrg != nil {
			var err error
			accepted, err = s.evalFilter(ctx, sub.filterPrg, public)
			if err != nil {
				logger.DebugContext(
					ctx,
					"Failed to evaluate filter",
					slog.Any("error", err),
				)
				accepted = false
			}
		}

		// Forward the event to the subscription:
		if accepted {
			logger.DebugContext(ctx, "Event accepted by filter")
			sub.eventsChan <- public
		} else {
			logger.DebugContext(ctx, "Event rejected by filter")
		}
	}
	return nil
}

// Names of fields:
const (
	eventsServerPayloadOneofField = "payload"
)
