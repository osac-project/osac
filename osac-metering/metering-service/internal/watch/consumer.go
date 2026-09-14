/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package watch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/osac-project/osac-metering/internal/events"
	kafkapub "github.com/osac-project/osac-metering/internal/kafka"
	"github.com/osac-project/osac-metering/internal/projection"
	"github.com/osac-project/osac-metering/schema"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var (
	watchReconnects = promauto.NewCounter(prometheus.CounterOpts{
		Name: "osac_metering_watch_stream_reconnects_total",
		Help: "Total Watch stream reconnections",
	})
	eventsSkipped = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "osac_metering_events_skipped_total",
		Help: "Watch events skipped due to unsupported type or data quality issues",
	}, []string{"reason"})
)

const (
	defaultInitialDelay   = 1 * time.Second
	defaultMaxDelay       = 30 * time.Second
	defaultHandlerRetries = 3
)

func BuildFilter(vmaas, caas, bmaas bool) string {
	var parts []string
	if vmaas {
		parts = append(parts, "has(event.compute_instance)")
	}
	if caas {
		parts = append(parts, "has(event.cluster)")
	}
	if bmaas {
		parts = append(parts, "has(event.bare_metal_instance)")
	}
	return strings.Join(parts, " || ")
}

// Consumer connects to the fulfillment-service gRPC Watch stream, maps
// incoming events to CloudEvents, and publishes them to Kafka. It
// automatically reconnects with exponential backoff when the stream breaks.
type Consumer struct {
	client    privatev1.EventsClient
	publisher kafkapub.EventPublisher
	store     projection.Store
	logger    logr.Logger

	InitialDelay   time.Duration
	MaxDelay       time.Duration
	HandlerRetries int
	Filter         string
}

func NewConsumer(
	client privatev1.EventsClient,
	publisher kafkapub.EventPublisher,
	store projection.Store,
	logger logr.Logger,
) *Consumer {
	return &Consumer{
		client:         client,
		publisher:      publisher,
		store:          store,
		logger:         logger,
		InitialDelay:   defaultInitialDelay,
		MaxDelay:       defaultMaxDelay,
		HandlerRetries: defaultHandlerRetries,
		Filter:         BuildFilter(true, true, true),
	}
}

// Run starts consuming the Watch stream. It blocks until ctx is cancelled,
// at which point it returns nil. Stream errors trigger automatic reconnection
// with exponential backoff.
func (c *Consumer) Run(ctx context.Context) error {
	delay := c.InitialDelay
	for {
		received, err := c.consumeStream(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if received > 0 {
			delay = c.InitialDelay
		}
		watchReconnects.Inc()
		c.logger.Error(err, "Watch stream error, reconnecting", "delay", delay, "receivedEvents", received)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
		delay = min(delay*2, c.MaxDelay)
	}
}

func (c *Consumer) consumeStream(ctx context.Context) (int, error) {
	filter := c.Filter
	stream, err := c.client.Watch(ctx, &privatev1.EventsWatchRequest{
		Filter: &filter,
	})
	if err != nil {
		return 0, fmt.Errorf("establishing watch stream: %w", err)
	}

	received := 0
	for {
		resp, err := stream.Recv()
		if err != nil {
			return received, fmt.Errorf("receiving event: %w", err)
		}
		if resp.GetEvent() == nil {
			c.logger.V(1).Info("Received response with nil event, skipping")
			continue
		}
		received++

		if err := c.handleEvent(ctx, resp.GetEvent()); err != nil {
			return received, fmt.Errorf("handling event %s: %w", resp.GetEvent().GetId(), err)
		}
	}
}

func (c *Consumer) handleEvent(ctx context.Context, event *privatev1.Event) error {
	mapper, err := events.MapperForEvent(event)
	if err != nil {
		return fmt.Errorf("unexpected event payload for %s: %w", event.GetId(), err)
	}

	resourceID := mapper.ResourceID()
	currentState := mapper.CurrentState()
	isBillable := mapper.IsBillable()
	version := mapper.FulfillmentVersion()
	dims, err := mapper.BillingDimensionsMap()
	if err != nil {
		if errors.Is(err, events.ErrDataQuality) {
			eventsSkipped.WithLabelValues("data_quality").Inc()
			c.logger.Info("skipping event with invalid billing dimensions",
				"event_id", event.GetId(), "resource_id", resourceID, "error", err)
			return nil
		}
		return fmt.Errorf("extracting billing dimensions for %s: %w", resourceID, err)
	}

	existing, err := c.store.Get(ctx, resourceID)
	if err != nil {
		return fmt.Errorf("reading projection for %s: %w", resourceID, err)
	}

	transitionTime, err := mapper.TransitionTime(event)
	if err != nil {
		if errors.Is(err, events.ErrUnsupportedEvent) {
			eventsSkipped.WithLabelValues("unsupported_event_type").Inc()
			c.logger.V(1).Info("skipping unsupported event type",
				"event_id", event.GetId(), "resource_id", resourceID)
			return nil
		}
		if errors.Is(err, events.ErrDataQuality) && event.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED && existing != nil && existing.CurrentState == currentState {
			c.logger.V(1).Info("skipping metadata-only update with no state change",
				"event_id", event.GetId(), "resource_id", resourceID, "state", currentState)
			return nil
		}
		if errors.Is(err, events.ErrDataQuality) && event.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_DELETED {
			eventsSkipped.WithLabelValues("missing_event_timestamp").Inc()
			c.logger.Info("skipping deleted event with missing timestamp",
				"event_id", event.GetId(), "resource_id", resourceID)
			return nil
		}
		return err
	}
	if projectionIsAhead(existing, version, currentState, dims) {
		c.logger.Info("skipping stale Watch event before publication",
			"resource_id", resourceID,
			"event_version", version,
			"projection_version", existing.FulfillmentVersion)
		return nil
	}

	if c.shouldSkipUpdate(ctx, event, existing, currentState, dims, version, transitionTime, resourceID) {
		return nil
	}

	if mapper.ResourceType() == events.ResourceTypeBareMetalInstance {
		return c.handleBareMetalEvent(ctx, event, mapper, existing, version, transitionTime, dims)
	}

	stateCtx := c.buildStateContext(existing, isBillable, transitionTime, dims)

	eventDims := dims
	if mapper.ResourceType() == events.ResourceTypeClusterOrder &&
		(event.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_CREATED ||
			event.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_DELETED) {
		eventDims = topLevelDims(dims)
	}

	ce, err := events.MapWatchEvent(event, mapper, stateCtx, eventDims)
	if err != nil {
		if errors.Is(err, events.ErrTransientState) {
			return c.handleTransientState(ctx, mapper, existing, version, transitionTime)
		}
		if errors.Is(err, events.ErrSkipTransition) {
			if existing != nil && !events.DimensionsEqual(existing.BillingDimensions, dims) {
				return c.handleScalingEvent(ctx, event, mapper, existing, transitionTime, version, currentState, isBillable, dims)
			}
			c.logger.V(1).Info("non-billing state transition, updating projection only",
				"resource_id", resourceID, "state", currentState)
			projState := c.buildProjectionState(mapper, existing, transitionTime, version, currentState, isBillable, dims)
			if upsertErr := c.store.Upsert(ctx, projState); upsertErr != nil && !errors.Is(upsertErr, projection.ErrStaleVersion) {
				return fmt.Errorf("upserting projection for %s: %w", resourceID, upsertErr)
			}
			return nil
		}
		return err
	}

	projState := c.buildProjectionState(mapper, existing, transitionTime, version, currentState, isBillable, dims)

	if event.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_DELETED {
		latest, err := c.store.Get(ctx, resourceID)
		if err != nil {
			return fmt.Errorf("rechecking projection for %s: %w", resourceID, err)
		}
		if projectionIsAhead(latest, version, currentState, dims) {
			c.logger.Info("skipping stale delete event before publication",
				"resource_id", resourceID,
				"event_version", version,
				"projection_version", latest.FulfillmentVersion)
			return nil
		}
		if err := c.publishLifecycleEvents(ctx, ce, mapper, event.GetId()); err != nil {
			return err
		}
		if existing != nil {
			if err := c.store.Delete(ctx, resourceID); err != nil {
				return fmt.Errorf("deleting projection for %s: %w", resourceID, err)
			}
		}
		return nil
	}

	return c.publishAndUpsert(ctx, func() error {
		return c.publishLifecycleEvents(ctx, ce, mapper, event.GetId())
	}, projState, resourceID)
}

// publishAndUpsert publishes events first, then commits projection state.
// Publish-first ensures no data loss: if publish fails, projection is not
// committed, and replay retries the full publish. If upsert fails after
// successful publish, replay produces duplicate events (handled by adapter
// dedup via deterministic CloudEvent IDs).
func (c *Consumer) publishAndUpsert(ctx context.Context, publish func() error, state projection.ResourceState, resourceID string) error {
	latest, err := c.store.Get(ctx, resourceID)
	if err != nil {
		return fmt.Errorf("rechecking projection for %s: %w", resourceID, err)
	}
	if projectionIsAhead(latest, state.FulfillmentVersion, state.CurrentState, state.BillingDimensions) {
		c.logger.Info("skipping stale Watch event before publication",
			"resource_id", resourceID,
			"event_version", state.FulfillmentVersion,
			"projection_version", latest.FulfillmentVersion)
		return nil
	}

	if err := publish(); err != nil {
		return err
	}

	if err := c.store.Upsert(ctx, state); err != nil {
		if errors.Is(err, projection.ErrStaleVersion) {
			c.logger.Info("stale version, skipping projection update",
				"resource_id", resourceID)
			return nil
		}
		return fmt.Errorf("upserting projection for %s: %w", resourceID, err)
	}
	return nil
}

// projectionIsAhead rejects an older snapshot and a conflicting snapshot with
// the same fulfillment version. A missing projection is always accepted because
// no ordering information exists until the resource is first observed.
func projectionIsAhead(existing *projection.ResourceState, version int32, currentState string, dims map[string]any) bool {
	if existing == nil {
		return false
	}
	if existing.FulfillmentVersion > version {
		return true
	}
	return existing.FulfillmentVersion == version &&
		(existing.CurrentState != currentState || !events.DimensionsEqual(existing.BillingDimensions, dims))
}

// handleTransientState updates only FulfillmentVersion and TransitionTime
// for transient states (STOPPING, STARTING) without changing CurrentState,
// billing fields, or emitting a CloudEvent. The projection keeps
// CurrentState=RUNNING so the subsequent final state (e.g., STOPPED)
// sees previous_state=RUNNING and computes duration_seconds correctly.
func (c *Consumer) handleTransientState(
	ctx context.Context,
	mapper events.ResourceMapper,
	existing *projection.ResourceState,
	version int32,
	transitionTime time.Time,
) error {
	if existing == nil {
		return nil
	}

	existing.FulfillmentVersion = version
	existing.TransitionTime = transitionTime.UTC()

	err := c.store.Upsert(ctx, *existing)
	if err != nil {
		if errors.Is(err, projection.ErrStaleVersion) {
			c.logger.Info("stale version during transient state update, skipping",
				"resource_id", mapper.ResourceID())
			return nil
		}
		return fmt.Errorf("upserting transient state for %s: %w", mapper.ResourceID(), err)
	}

	c.logger.V(1).Info("transient state updated (no CloudEvent)",
		"resource_id", mapper.ResourceID())
	return nil
}

// DimComponents is the billing dimensions key for the nested components array.
const DimComponents = "components"

func (c *Consumer) publishLifecycleEvents(ctx context.Context, baseCE *cloudevents.Event, mapper events.ResourceMapper, eventID string) error {
	if baseCE.Type() == events.EventCreated || baseCE.Type() == events.EventDeleted {
		return c.publishWithRetry(ctx, baseCE)
	}

	dims, err := mapper.BillingDimensionsMap()
	if err != nil {
		return err
	}
	decomposed, err := events.BuildResourceEvents(mapper.ResourceType(), dims, eventID, func(dims map[string]any, compEventID string) (cloudevents.Event, error) {
		return c.buildComponentEvent(baseCE, compEventID, dims)
	})
	if err != nil {
		return err
	}
	for i := range decomposed {
		if err := c.publishWithRetry(ctx, &decomposed[i]); err != nil {
			return err
		}
	}
	return nil
}

func (c *Consumer) handleBareMetalEvent(
	ctx context.Context,
	event *privatev1.Event,
	mapper events.ResourceMapper,
	existing *projection.ResourceState,
	version int32,
	transitionTime time.Time,
	dims map[string]any,
) error {
	resourceID := mapper.ResourceID()
	previousState := ""
	if existing != nil {
		previousState = existing.CurrentState
	}

	if event.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_DELETED {
		return c.handleBareMetalDeletion(ctx, event, mapper, existing, dims, transitionTime)
	}

	allocationEffect, err := events.ResolveAllocationTransition(previousState, mapper.CurrentState())
	if err != nil {
		if errors.Is(err, events.ErrInvalidBMaaSTransition) {
			eventsSkipped.WithLabelValues("invalid_bmaas_transition").Inc()
			c.logger.Info("skipping invalid BMaaS state transition",
				"event_id", event.GetId(), "resource_id", resourceID,
				"previous_state", previousState, "current_state", mapper.CurrentState())
			return nil
		}
		return err
	}
	consumptionEffect, err := events.ResolveConsumptionTransition(previousState, mapper.CurrentState())
	if err != nil {
		if errors.Is(err, events.ErrInvalidBMaaSTransition) {
			eventsSkipped.WithLabelValues("invalid_bmaas_transition").Inc()
			c.logger.Info("skipping invalid BMaaS state transition",
				"event_id", event.GetId(), "resource_id", resourceID,
				"previous_state", previousState, "current_state", mapper.CurrentState())
			return nil
		}
		return err
	}

	projectionState := c.buildBareMetalProjectionState(
		mapper,
		existing,
		transitionTime,
		version,
		dims,
		allocationEffect,
		consumptionEffect,
	)

	if event.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_CREATED {
		created, err := events.MapWatchEvent(event, mapper, &events.StateContext{}, dims)
		if err != nil {
			return err
		}
		return c.publishAndUpsert(ctx, func() error {
			return c.publishWithRetry(ctx, created)
		}, projectionState, resourceID)
	}

	lifecycleEvents, err := c.buildBareMetalLifecycleEvents(
		mapper,
		existing,
		event.GetId(),
		transitionTime,
		dims,
		allocationEffect,
		consumptionEffect,
	)
	if err != nil {
		return err
	}

	return c.publishAndUpsert(ctx, func() error {
		for i := range lifecycleEvents {
			if err := c.publishWithRetry(ctx, &lifecycleEvents[i]); err != nil {
				return err
			}
		}
		return nil
	}, projectionState, resourceID)
}

func (c *Consumer) handleBareMetalDeletion(
	ctx context.Context,
	event *privatev1.Event,
	mapper events.ResourceMapper,
	existing *projection.ResourceState,
	dims map[string]any,
	transitionTime time.Time,
) error {
	previousState := ""
	if existing != nil {
		previousState = existing.CurrentState
	}
	closureEvents, err := c.buildBareMetalLifecycleEvents(
		mapper,
		existing,
		event.GetId(),
		transitionTime,
		dims,
		events.BMaaSEffectSuspend,
		events.BMaaSEffectSuspend,
	)
	if err != nil {
		return err
	}

	audit, err := events.MapWatchEvent(
		event,
		mapper,
		&events.StateContext{PreviousState: previousState},
		dims,
	)
	if err != nil {
		return err
	}
	for i := range closureEvents {
		if err := c.publishWithRetry(ctx, &closureEvents[i]); err != nil {
			return err
		}
	}
	if err := c.publishWithRetry(ctx, audit); err != nil {
		return err
	}
	if existing != nil {
		if err := c.store.Delete(ctx, mapper.ResourceID()); err != nil {
			return fmt.Errorf("deleting projection for %s: %w", mapper.ResourceID(), err)
		}
	}
	return nil
}

func (c *Consumer) buildBareMetalLifecycleEvents(
	mapper events.ResourceMapper,
	existing *projection.ResourceState,
	eventID string,
	transitionTime time.Time,
	dims map[string]any,
	allocationEffect string,
	consumptionEffect string,
) ([]cloudevents.Event, error) {
	previousState := ""
	intervals := events.BMaaSMeterIntervals{}
	allocationEverStarted := false
	consumptionEverStarted := false
	if existing != nil {
		previousState = existing.CurrentState
		intervals.AllocationSince = existing.BillableSince
		if since, ok := existing.ComponentBillableSince[events.BMaaSMeterConsumption]; ok {
			intervals.ConsumptionSince = &since
		}
		allocationEverStarted = existing.ComponentEverStarted[events.BMaaSMeterAllocation]
		consumptionEverStarted = existing.ComponentEverStarted[events.BMaaSMeterConsumption]
	}

	return events.DecomposeBMIEvents(
		dims,
		eventID,
		transitionTime,
		intervals,
		func(request events.BMaaSEventBuildRequest) (cloudevents.Event, error) {
			return buildBareMetalEvent(mapper, previousState, transitionTime, request)
		},
		mapBMaaSEffectToEvent(allocationEffect, allocationEverStarted),
		mapBMaaSEffectToEvent(consumptionEffect, consumptionEverStarted),
	)
}

func buildBareMetalEvent(
	mapper events.ResourceMapper,
	previousState string,
	transitionTime time.Time,
	request events.BMaaSEventBuildRequest,
) (cloudevents.Event, error) {
	return events.BuildLifecycleEvent(
		request.EventID,
		request.EventType,
		mapper,
		request.BillingDims,
		previousState,
		request.DurationSeconds,
		transitionTime,
	)
}

func mapBMaaSEffectToEvent(effect string, everStarted bool) string {
	switch effect {
	case events.BMaaSEffectStart:
		return events.ResolveLifecycleStartEvent(everStarted)
	case events.BMaaSEffectResume:
		return events.ResolveLifecycleStartEvent(everStarted)
	case events.BMaaSEffectSuspend:
		return events.EventSuspended
	default:
		return ""
	}
}

func (c *Consumer) buildBareMetalProjectionState(
	mapper events.ResourceMapper,
	existing *projection.ResourceState,
	transitionTime time.Time,
	version int32,
	dims map[string]any,
	allocationEffect string,
	consumptionEffect string,
) projection.ResourceState {
	state := c.newProjectionState(mapper, existing, transitionTime, version, mapper.CurrentState(), dims)
	state.ComponentBillableSince = map[string]time.Time{}
	state.ComponentEverStarted = map[string]bool{}
	if existing != nil {
		state.BillableSince = cloneTimePointer(existing.BillableSince)
		for key, since := range existing.ComponentBillableSince {
			state.ComponentBillableSince[key] = since
		}
		for key, started := range existing.ComponentEverStarted {
			state.ComponentEverStarted[key] = started
		}
		state.EverBillable = existing.EverBillable
	}

	if events.IsAllocationBillableState(state.CurrentState) {
		if state.BillableSince == nil {
			now := transitionTime.UTC()
			state.BillableSince = &now
		}
	} else {
		state.BillableSince = nil
	}
	if allocationEffect == events.BMaaSEffectStart || allocationEffect == events.BMaaSEffectResume {
		state.ComponentEverStarted[events.BMaaSMeterAllocation] = true
	}

	if events.IsConsumptionBillableState(state.CurrentState) {
		if _, ok := state.ComponentBillableSince[events.BMaaSMeterConsumption]; !ok {
			now := transitionTime.UTC()
			state.ComponentBillableSince[events.BMaaSMeterConsumption] = now
		}
	} else {
		delete(state.ComponentBillableSince, events.BMaaSMeterConsumption)
	}
	if consumptionEffect == events.BMaaSEffectStart || consumptionEffect == events.BMaaSEffectResume {
		state.ComponentEverStarted[events.BMaaSMeterConsumption] = true
	}

	state.IsBillable = state.BillableSince != nil
	state.EverBillable = state.EverBillable || state.IsBillable
	return state
}

func (c *Consumer) newProjectionState(
	mapper events.ResourceMapper,
	existing *projection.ResourceState,
	transitionTime time.Time,
	version int32,
	currentState string,
	dims map[string]any,
) projection.ResourceState {
	state := projection.ResourceState{
		ResourceID:         mapper.ResourceID(),
		ResourceType:       mapper.ResourceType(),
		TenantID:           mapper.TenantID(),
		CurrentState:       currentState,
		TransitionTime:     transitionTime.UTC(),
		FulfillmentVersion: version,
		BillingDimensions:  dims,
	}
	if project := mapper.ProjectID(); project != nil {
		state.ProjectID = *project
	}
	if existing != nil {
		state.PreviousState = existing.CurrentState
		state.LastHeartbeatAt = existing.LastHeartbeatAt
		state.EverBillable = existing.EverBillable
	}
	return state
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (c *Consumer) handleScalingEvent(ctx context.Context, event *privatev1.Event, mapper events.ResourceMapper, existing *projection.ResourceState, transitionTime time.Time, version int32, currentState string, isBillable bool, dims map[string]any) error {
	resourceID := mapper.ResourceID()
	projState := c.buildProjectionState(mapper, existing, transitionTime, version, currentState, isBillable, dims)
	stateCtx := c.buildStateContext(existing, isBillable, transitionTime, dims)

	return c.publishAndUpsert(ctx, func() error {
		if mapper.ResourceType() == events.ResourceTypeClusterOrder {
			changed, err := events.ChangedComponents(existing.BillingDimensions, dims)
			if err != nil {
				return err
			}
			if len(changed) == 0 {
				c.logger.V(1).Info("non-component dimension change, projection updated",
					"resource_id", resourceID)
				return nil
			}
			for _, comp := range changed {
				scalingCtx := &events.StateContext{
					PreviousState: stateCtx.PreviousState,
				}
				if !comp.IsNew {
					scalingCtx.DurationSeconds = c.componentDurationSeconds(existing, comp.NodeSet, transitionTime)
				}
				ce, ceErr := c.buildScalingEvent(
					events.ComponentEventID(event.GetId(), comp),
					mapper, comp.FlatBillingDimensions(), scalingCtx, transitionTime)
				if ceErr != nil {
					return ceErr
				}
				if err := c.publishWithRetry(ctx, &ce); err != nil {
					return err
				}
			}
			c.logger.Info("published scaling events",
				"resource_id", resourceID, "changed_components", len(changed))
			return nil
		}
		// VMaaS: single updated.v1
		ce, ceErr := c.buildScalingEvent(event.GetId(), mapper, dims, stateCtx, transitionTime)
		if ceErr != nil {
			return ceErr
		}
		return c.publishWithRetry(ctx, &ce)
	}, projState, resourceID)
}

func topLevelDims(dims map[string]any) map[string]any {
	flat := make(map[string]any, len(dims))
	for k, v := range dims {
		if k != DimComponents {
			flat[k] = v
		}
	}
	return flat
}

func (c *Consumer) buildComponentEvent(baseCE *cloudevents.Event, eventID string, dims map[string]any) (cloudevents.Event, error) {
	ce := cloudevents.NewEvent()
	ce.SetID(eventID)
	ce.SetSource(baseCE.Source())
	ce.SetType(baseCE.Type())
	ce.SetTime(baseCE.Time())

	for k, v := range baseCE.Extensions() {
		ce.SetExtension(k, v)
	}

	var baseData map[string]any
	if err := baseCE.DataAs(&baseData); err != nil {
		return ce, fmt.Errorf("reading base event data: %w", err)
	}
	if baseData == nil {
		baseData = map[string]any{}
	}

	baseData["billing_dimensions"] = dims
	if err := ce.SetData(cloudevents.ApplicationJSON, baseData); err != nil {
		return ce, fmt.Errorf("setting component event data: %w", err)
	}
	return ce, nil
}

func (c *Consumer) buildScalingEvent(eventID string, mapper events.ResourceMapper, dims map[string]any, stateCtx *events.StateContext, transitionTime time.Time) (cloudevents.Event, error) {
	return events.BuildLifecycleEvent(
		eventID,
		events.EventUpdated,
		mapper,
		dims,
		stateCtx.PreviousState,
		stateCtx.DurationSeconds,
		transitionTime,
	)
}
func (c *Consumer) shouldSkipUpdate(ctx context.Context, event *privatev1.Event, existing *projection.ResourceState, currentState string, dims map[string]any, version int32, transitionTime time.Time, resourceID string) bool {
	if event.GetType() != privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED || existing == nil {
		return false
	}
	if existing.CurrentState != currentState || !events.DimensionsEqual(existing.BillingDimensions, dims) {
		return false
	}
	if version > existing.FulfillmentVersion {
		existing.FulfillmentVersion = version
		existing.TransitionTime = transitionTime.UTC()
		if err := c.store.Upsert(ctx, *existing); err != nil && !errors.Is(err, projection.ErrStaleVersion) {
			c.logger.Error(err, "failed to advance projection version", "resource_id", resourceID)
		}
	}
	if !existing.TransitionTime.Truncate(time.Microsecond).Equal(transitionTime.UTC().Truncate(time.Microsecond)) {
		c.logger.Info("skipping replayed event (upserted but likely unpublished)",
			"resource_id", resourceID, "state", currentState)
	} else {
		c.logger.V(1).Info("same state and dimensions, skipping",
			"resource_id", resourceID, "state", currentState)
	}
	return true
}

func (c *Consumer) buildProjectionState(mapper events.ResourceMapper, existing *projection.ResourceState, transitionTime time.Time, version int32, currentState string, isBillable bool, dims map[string]any) projection.ResourceState {
	tt := transitionTime.UTC()
	projState := c.newProjectionState(mapper, existing, transitionTime, version, currentState, dims)
	projState.IsBillable = isBillable
	projState.EverBillable = isBillable || projState.EverBillable
	if isBillable {
		if existing == nil || !existing.IsBillable || !events.DimensionsEqual(existing.BillingDimensions, dims) {
			projState.BillableSince = &tt
		} else {
			projState.BillableSince = existing.BillableSince
		}
		var oldDims map[string]any
		var oldSince map[string]time.Time
		if existing != nil {
			oldDims = existing.BillingDimensions
			oldSince = existing.ComponentBillableSince
		}
		projState.ComponentBillableSince = events.NextComponentBillableSince(oldDims, oldSince, dims, tt)
	}
	return projState
}

// componentDurationSeconds returns how long a component's prior billing
// dimensions were in effect. Returns nil if no per-component timestamp is
// recorded for nodeSet — an honest "unknown" (the same signal already used
// for a genuinely new component) rather than guessing via the resource-wide
// BillableSince, which would silently reintroduce a narrower version of the
// cross-component bug this exists to fix. The only path that can leave an
// entry missing is a Reconciler correction that hasn't been updated to
// maintain ComponentBillableSince (see events.NextComponentBillableSince
// callers in the reconciliation package) — logged so an unexpected rate of
// occurrence is debuggable rather than silently absorbed.
func (c *Consumer) componentDurationSeconds(existing *projection.ResourceState, nodeSet string, transitionTime time.Time) *float64 {
	since, ok := existing.ComponentBillableSince[nodeSet]
	if !ok {
		c.logger.V(1).Info("no per-component billable-since recorded, reporting nil duration_seconds",
			"resource_id", existing.ResourceID, "node_set", nodeSet)
		return nil
	}
	duration := transitionTime.Sub(since).Seconds()
	return &duration
}

func (c *Consumer) buildStateContext(existing *projection.ResourceState, nowBillable bool, transitionTime time.Time, newDims map[string]any) *events.StateContext {
	if existing == nil {
		return &events.StateContext{}
	}

	sc := &events.StateContext{
		PreviousState: existing.CurrentState,
		EverBillable:  existing.EverBillable,
	}

	if existing.IsBillable && existing.BillableSince != nil {
		if !nowBillable || !events.DimensionsEqual(existing.BillingDimensions, newDims) {
			duration := transitionTime.Sub(*existing.BillableSince).Seconds()
			sc.DurationSeconds = &duration
			sc.BillableSince = existing.BillableSince
		}
	}

	return sc
}

func (c *Consumer) logPublished(ce *cloudevents.Event) {
	resourceID, _ := ce.Context.GetExtension(schema.ExtResourceID)
	tenantID, _ := ce.Context.GetExtension(schema.ExtTenant)
	c.logger.Info("published metering event",
		"event_id", ce.ID(),
		"type", ce.Type(),
		"resource_id", resourceID,
		"tenant_id", tenantID,
	)
}

func (c *Consumer) publishWithRetry(ctx context.Context, ce *cloudevents.Event) error {
	delay := c.InitialDelay
	for attempt := range c.HandlerRetries {
		err := c.publisher.Publish(ctx, *ce)
		if err == nil {
			c.logPublished(ce)
			return nil
		}
		c.logger.Error(err, "publish error, retrying",
			"event_id", ce.ID(),
			"attempt", attempt+1,
			"maxAttempts", c.HandlerRetries,
		)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
		delay = min(delay*2, c.MaxDelay)
	}
	return fmt.Errorf("publish failed after %d retries for event %s", c.HandlerRetries, ce.ID())
}
