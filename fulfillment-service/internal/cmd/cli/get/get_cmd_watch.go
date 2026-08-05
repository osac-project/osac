/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package get

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

type eventStream struct {
	stream  grpc.ClientStream
	newResp func() proto.Message
}

func (s *eventStream) Recv() (proto.Message, error) {
	resp := s.newResp()
	if err := s.stream.RecvMsg(resp); err != nil {
		return nil, err
	}
	msg := resp.ProtoReflect()
	eventField := msg.Descriptor().Fields().ByName("event")
	if eventField == nil || !msg.Has(eventField) {
		return nil, nil
	}
	return msg.Get(eventField).Message().Interface(), nil
}

// watch watches for events and displays updated objects.
func (c *runnerContext) watch(ctx context.Context, keys []string) error {
	// Build filter for events
	filter, err := c.buildEventFilter(keys)
	if err != nil {
		return fmt.Errorf("failed to build event filter: %w", err)
	}

	// Start watching
	c.console.Infof(ctx, "Watching for changes (Ctrl+C to stop)...\n\n")

	// Create the appropriate event stream based on the object's API package
	stream, err := c.createEventStream(ctx, filter)
	if err != nil {
		return fmt.Errorf("failed to start watching events: %w", err)
	}

	// Process events
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to receive event: %w", err)
		}

		if event == nil {
			continue
		}

		// Extract the object from the event payload
		object := c.extractObjectFromEvent(event)
		if object == nil {
			continue
		}

		// Display the event
		c.displayEvent(ctx, event, object)
	}
}

func (c *runnerContext) createEventStream(ctx context.Context, filter string) (*eventStream, error) {
	if c.isPrivateObject() {
		client := privatev1.NewEventsClient(c.conn)
		stream, err := client.Watch(ctx, &privatev1.EventsWatchRequest{Filter: &filter})
		if err != nil {
			return nil, err
		}
		return &eventStream{stream: stream, newResp: func() proto.Message { return &privatev1.EventsWatchResponse{} }}, nil
	}
	client := publicv1.NewEventsClient(c.conn)
	stream, err := client.Watch(ctx, &publicv1.EventsWatchRequest{Filter: &filter})
	if err != nil {
		return nil, err
	}
	return &eventStream{stream: stream, newResp: func() proto.Message { return &publicv1.EventsWatchResponse{} }}, nil
}

// buildEventFilter builds a CEL filter expression for watching events.
func (c *runnerContext) buildEventFilter(keys []string) (string, error) {
	// Get the field name for this object type in the Event message
	fieldName := c.getEventPayloadFieldName()
	if fieldName == "" {
		return "", fmt.Errorf("object type '%s' is not supported for watching", c.objectHelper)
	}

	// Build filter parts
	var parts []string

	// Filter by object type (check if the payload field is set)
	parts = append(parts, fmt.Sprintf("has(event.%s)", fieldName))

	// If specific IDs/names are provided, filter by them
	if len(keys) > 0 {
		var idFilters []string
		for _, key := range keys {
			// Match by ID or name
			idFilters = append(idFilters, fmt.Sprintf(
				"event.%s.id == %q || event.%s.metadata.name == %q",
				fieldName, key, fieldName, key,
			))
		}
		parts = append(parts, "("+strings.Join(idFilters, " || ")+")")
	}

	return strings.Join(parts, " && "), nil
}

// getEventPayloadFieldName returns the field name in the Event message for the current object type
// by introspecting the Event proto descriptor's payload oneof.
func (c *runnerContext) getEventPayloadFieldName() string {
	eventDesc := c.getEventDescriptor()
	payloadOneof := eventDesc.Oneofs().ByName("payload")
	if payloadOneof == nil {
		return ""
	}

	objectFullName := c.objectHelper.Descriptor().FullName()
	for i := 0; i < payloadOneof.Fields().Len(); i++ {
		field := payloadOneof.Fields().Get(i)
		if field.Message() != nil && field.Message().FullName() == objectFullName {
			return string(field.Name())
		}
	}
	return ""
}

// getEventDescriptor returns the Event message descriptor matching the object's API package.
func (c *runnerContext) getEventDescriptor() protoreflect.MessageDescriptor {
	if c.isPrivateObject() {
		return (&privatev1.Event{}).ProtoReflect().Descriptor()
	}
	return (&publicv1.Event{}).ProtoReflect().Descriptor()
}

// isPrivateObject returns true if the current object type belongs to the private API package.
func (c *runnerContext) isPrivateObject() bool {
	return strings.HasPrefix(string(c.objectHelper.FullName()), "osac.private.")
}

// extractObjectFromEvent extracts the proto message from an event's payload oneof using reflection.
func (c *runnerContext) extractObjectFromEvent(event proto.Message) proto.Message {
	msg := event.ProtoReflect()
	payloadOneof := msg.Descriptor().Oneofs().ByName("payload")
	if payloadOneof == nil {
		return nil
	}
	whichField := msg.WhichOneof(payloadOneof)
	if whichField == nil {
		return nil
	}
	return msg.Get(whichField).Message().Interface()
}

// getEventTypeName extracts the event type name from an Event proto message using reflection.
func getEventTypeName(event proto.Message) string {
	eventMsg := event.ProtoReflect()
	typeField := eventMsg.Descriptor().Fields().ByName("type")
	typeValue := eventMsg.Get(typeField)
	if enumDesc := typeField.Enum().Values().ByNumber(typeValue.Enum()); enumDesc != nil {
		return strings.TrimPrefix(string(enumDesc.Name()), "EVENT_TYPE_")
	}
	return fmt.Sprintf("UNKNOWN(%d)", typeValue.Enum())
}

// displayEvent displays an event and the updated object.
func (c *runnerContext) displayEvent(ctx context.Context, event proto.Message, object proto.Message) {
	timestamp := time.Now().Format(time.TimeOnly)

	eventType := getEventTypeName(event)

	objectId := c.getObjectId(object)

	c.console.Infof(ctx, "[%s] %s %s '%s'\n", timestamp, eventType, c.objectHelper.Singular(), objectId)

	var render func(context.Context, []proto.Message) error
	switch c.args.format {
	case outputFormatJson:
		render = c.renderJson
	case outputFormatYaml:
		render = c.renderYaml
	default:
		render = c.renderTable
	}

	err := render(ctx, []proto.Message{object})
	if err != nil {
		c.logger.WarnContext(
			ctx,
			"Failed to render object",
			"object_id", objectId,
			"error", err,
		)
	}

	c.console.Infof(ctx, "\n")
}

// getObjectId extracts the ID from an object.
func (c *runnerContext) getObjectId(object proto.Message) string {
	// Use reflection to get the ID field
	msg := object.ProtoReflect()
	desc := msg.Descriptor()

	// Try to find the 'id' field
	idField := desc.Fields().ByName("id")
	if idField != nil {
		return msg.Get(idField).String()
	}

	return "<unknown>"
}
