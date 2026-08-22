/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package events

import (
	"errors"
	"fmt"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
	"github.com/osac-project/osac-metering/schema"
)

var (
	ErrTransientState   = errors.New("transient state: update projection only, no CloudEvent")
	ErrSkipTransition   = errors.New("no billing boundary: skip event, check for scaling")
	ErrUnsupportedEvent = errors.New("unsupported event type")
)

// eventBillableStart is an internal marker, never emitted as a real CloudEvent
// type. Transition tables use it for every "crossed into billable" row instead
// of picking EventStarted or EventResumed themselves -- that choice depends on
// whether the resource has EVER been billable before, which is historical
// information a (previousState, currentState) table lookup cannot see (by the
// time a real transition into a billable state is observed, previousState is
// never the empty/never-seen sentinel; CREATE always seeds a concrete state
// first). MapWatchEvent resolves this marker via StateContext.EverBillable.
const eventBillableStart = "internal.billable-boundary-crossed"

// Resource type constants — re-exported from the shared schema module for
// convenience within this package.
const (
	ResourceTypeComputeInstance = schema.ResourceTypeComputeInstance
	ResourceTypeClusterOrder    = schema.ResourceTypeClusterOrder
)

// StateEmpty is the empty previous state for initial transitions.
const StateEmpty = ""

// TransitionKey identifies a state transition by (previous, current) state.
type TransitionKey struct {
	From string
	To   string
}

// TransitionResult defines what happens on a state transition.
type TransitionResult struct {
	EventType string // CloudEvent type to emit (empty when Transient or Skip)
	Transient bool   // projection-only update, no CloudEvent
	Skip      bool   // no projection update, no CloudEvent (non-billing transition)
}

// TransitionTable maps (previous, current) state pairs to their billing effect.
// Missing entries are invalid transitions — resolveTransition returns an error.
type TransitionTable map[TransitionKey]TransitionResult

// resolveTransition looks up the event type for a state transition.
// Exact (from, to) match only — missing entry = error (fail fast on unknown transitions).
func resolveTransition(table TransitionTable, from, to string) (string, error) {
	if result, ok := table[TransitionKey{from, to}]; ok {
		return applyResult(result)
	}
	return "", fmt.Errorf("unexpected state transition: %s -> %s", from, to)
}

func applyResult(r TransitionResult) (string, error) {
	if r.Skip {
		return "", ErrSkipTransition
	}
	if r.Transient {
		return "", ErrTransientState
	}
	return r.EventType, nil
}

// fixedEventTypes maps event types that always produce the same CloudEvent type
// regardless of state transition.
var fixedEventTypes = map[privatev1.EventType]string{
	privatev1.EventType_EVENT_TYPE_OBJECT_CREATED: EventCreated,
	privatev1.EventType_EVENT_TYPE_OBJECT_DELETED: EventDeleted,
}

// ResolveCloudEventType returns the CloudEvent type for a given proto event type
// and state transition. CREATED and DELETED are fixed; UPDATED delegates to the
// transition table.
func ResolveCloudEventType(table TransitionTable, eventType privatev1.EventType, previousState, currentState string) (string, error) {
	if ceType, ok := fixedEventTypes[eventType]; ok {
		return ceType, nil
	}
	if eventType == privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED {
		return resolveTransition(table, previousState, currentState)
	}
	return "", fmt.Errorf("%w: %v", ErrUnsupportedEvent, eventType)
}

// ResolveTransitionTime selects the appropriate timestamp for a given event type.
func ResolveTransitionTime(eventType privatev1.EventType, creation, deletion, stateTransition *timestamppb.Timestamp, resourceID string) (time.Time, error) {
	timestamps := map[privatev1.EventType]*timestamppb.Timestamp{
		privatev1.EventType_EVENT_TYPE_OBJECT_CREATED: creation,
		privatev1.EventType_EVENT_TYPE_OBJECT_DELETED: deletion,
		privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED: stateTransition,
	}

	ts, ok := timestamps[eventType]
	if !ok {
		return time.Time{}, fmt.Errorf("%w for timestamp: %v", ErrUnsupportedEvent, eventType)
	}
	if ts == nil {
		return time.Time{}, fmt.Errorf("%w: resource %s has no timestamp for event type %v", ErrDataQuality, resourceID, eventType)
	}
	return ts.AsTime(), nil
}

// EventBuilder creates a CloudEvent from billing dimensions and an event ID.
type EventBuilder func(dims map[string]any, eventID string) (cloudevents.Event, error)

// EventDecomposer produces one or more CloudEvents from billing dimensions.
type EventDecomposer func(dims map[string]any, baseID string, buildFn EventBuilder) ([]cloudevents.Event, error)

func singleEvent(dims map[string]any, baseID string, buildFn EventBuilder) ([]cloudevents.Event, error) {
	ce, err := buildFn(dims, baseID)
	if err != nil {
		return nil, err
	}
	return []cloudevents.Event{ce}, nil
}

var resourceDecomposers = map[string]EventDecomposer{
	ResourceTypeComputeInstance: singleEvent,
	ResourceTypeClusterOrder:    DecomposeClusterEvents,
}

// BuildResourceEvents dispatches event building to the correct decomposer
// for the given resource type.
func BuildResourceEvents(resourceType string, dims map[string]any, baseID string, buildFn EventBuilder) ([]cloudevents.Event, error) {
	decomposer, ok := resourceDecomposers[resourceType]
	if !ok {
		return nil, fmt.Errorf("unknown resource type for event decomposition: %s", resourceType)
	}
	return decomposer(dims, baseID, buildFn)
}
