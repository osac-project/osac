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
		Help: "Watch events skipped before publication",
	}, []string{"reason"})
)

const (
	defaultInitialDelay   = 1 * time.Second
	defaultMaxDelay       = 30 * time.Second
	defaultHandlerRetries = 3
)

func BuildFilter() string {
	arr := []string{
		"has(event.compute_instance)",
		"has(event.cluster)",
		"has(event.external_ip)",
		"has(event.nat_gateway)",
		"has(event.volume)",
		"has(event.bare_metal_instance)",
	}
	return strings.Join(arr, " || ")
}

// Consumer connects to the fulfillment-service gRPC Watch stream, maps
// incoming events to CloudEvents, and publishes them to Kafka. It
// automatically reconnects with exponential backoff when the stream breaks.
type Consumer struct {
	client        privatev1.EventsClient
	mapperFactory *MapperFactory
	publisher     kafkapub.EventPublisher
	store         projection.Store
	logger        logr.Logger

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
	mapperFactory *MapperFactory,
) (*Consumer, error) {
	if mapperFactory == nil {
		return nil, fmt.Errorf("mapper factory is required")
	}
	return &Consumer{
		client:         client,
		mapperFactory:  mapperFactory,
		publisher:      publisher,
		store:          store,
		logger:         logger,
		InitialDelay:   defaultInitialDelay,
		MaxDelay:       defaultMaxDelay,
		HandlerRetries: defaultHandlerRetries,
		Filter:         BuildFilter(),
	}, nil
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
	prepared, skipped, err := c.prepareEvent(ctx, event)
	if err != nil {
		return err
	}
	if skipped {
		return nil
	}
	if c.skipStaleEvent(prepared) {
		return nil
	}
	if transitionTimeIsStale(prepared.existing, prepared.event.GetType(), prepared.currentState, prepared.version, prepared.transitionTime) {
		c.logger.Info("skipping Watch event with stale transition time",
			"resource_id", prepared.resourceID,
			"event_version", prepared.version,
			"projection_version", prepared.existing.FulfillmentVersion,
			"event_transition_time", prepared.transitionTime,
			"projection_transition_time", prepared.existing.TransitionTime)
		return nil
	}
	skip, err := c.shouldSkipUpdate(
		ctx,
		event,
		prepared.mapper,
		prepared.existing,
		prepared.currentState,
		prepared.isBillable,
		prepared.dimensions,
		prepared.version,
		prepared.transitionTime,
	)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	if prepared.mapper.ResourceType() == events.ResourceTypeBareMetalInstance &&
		prepared.event.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED &&
		prepared.existing != nil && !events.DimensionsEqual(prepared.existing.BillingDimensions, prepared.dimensions) {
		eventsSkipped.WithLabelValues("bmaas_dimension_drift").Inc()
		c.logger.Info("skipping BMaaS event with immutable billing dimension drift",
			"resource_id", prepared.resourceID,
			"event_version", prepared.version)
		return nil
	}

	if prepared.mapper.ResourceType() == events.ResourceTypeBareMetalInstance {
		return c.handleBareMetalEvent(ctx, prepared.event, prepared.mapper, prepared.existing, prepared.version, prepared.transitionTime, prepared.dimensions)
	}

	if handled, err := c.handlePreMappingScaling(
		ctx,
		prepared.event,
		prepared.mapper,
		prepared.existing,
		prepared.transitionTime,
		prepared.version,
		prepared.currentState,
		prepared.isBillable,
		prepared.dimensions,
	); handled || err != nil {
		return err
	}

	cloudEvent, handled, err := c.mapPreparedEvent(ctx, prepared)
	if err != nil {
		return err
	}
	if handled {
		return nil
	}
	return c.commitMappedEvent(ctx, prepared, cloudEvent)
}

// publishAndUpsert publishes events first, then commits projection state.
// Publish-first ensures no data loss: if publish fails, projection is not
// committed, and replay retries the full publish. If upsert fails after
// successful publish, replay produces duplicate events (handled by adapter
// dedup via deterministic CloudEvent IDs).
func (c *Consumer) publishAndUpsert(ctx context.Context, publish func() error, state projection.ResourceState, resourceID string, allowSameVersionBoundary bool) error {
	latest, err := c.store.Get(ctx, resourceID)
	if err != nil {
		return fmt.Errorf("rechecking projection for %s: %w", resourceID, err)
	}
	if projectionIsAhead(latest, state.FulfillmentVersion, state.CurrentState, state.BillingDimensions, allowSameVersionBoundary) {
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
func projectionIsAhead(existing *projection.ResourceState, version int32, currentState string, dims map[string]any, allowSameVersionBoundary bool) bool {
	if existing == nil {
		return false
	}
	if existing.Deleted {
		return true
	}
	if existing.FulfillmentVersion > version {
		return true
	}
	if allowSameVersionBoundary && existing.FulfillmentVersion == version {
		return false
	}
	return existing.FulfillmentVersion == version &&
		(existing.CurrentState != currentState || !events.DimensionsEqual(existing.BillingDimensions, dims))
}

// transitionTimeIsStale prevents a newer fulfillment snapshot from moving the
// authoritative transition time backwards. State changes and deletes require a
// strictly newer timestamp because their durations are calculated from it.
// Metadata-only updates may reuse the same timestamp, but not an earlier one.
func transitionTimeIsStale(
	existing *projection.ResourceState,
	eventType privatev1.EventType,
	currentState string,
	version int32,
	transitionTime time.Time,
) bool {
	if existing == nil || version <= existing.FulfillmentVersion {
		return false
	}

	stateChanging := existing.CurrentState != currentState
	if eventType == privatev1.EventType_EVENT_TYPE_OBJECT_DELETED || stateChanging {
		return !transitionTime.After(existing.TransitionTime)
	}
	return transitionTime.Before(existing.TransitionTime)
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

func (c *Consumer) publishLifecycleEvents(ctx context.Context, baseCE *cloudevents.Event, mapper events.ResourceMapper, eventID string, billingDims map[string]any) error {
	if baseCE.Type() == events.EventCreated || baseCE.Type() == events.EventDeleted {
		return c.publishWithRetry(ctx, baseCE)
	}

	decomposed, err := events.BuildResourceEvents(mapper.ResourceType(), billingDims, eventID, func(dims map[string]any, compEventID string) (cloudevents.Event, error) {
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

	if event.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_CREATED {
		created, err := events.MapWatchEvent(event, mapper, &events.StateContext{}, dims)
		if err != nil {
			return err
		}
		return c.publishAndUpsert(ctx, func() error {
			if err := c.publishWithRetry(ctx, created); err != nil {
				return err
			}
			for i := range lifecycleEvents {
				if err := c.publishWithRetry(ctx, &lifecycleEvents[i]); err != nil {
					return err
				}
			}
			return nil
		}, projectionState, resourceID, false)
	}

	return c.publishAndUpsert(ctx, func() error {
		for i := range lifecycleEvents {
			if err := c.publishWithRetry(ctx, &lifecycleEvents[i]); err != nil {
				return err
			}
		}
		return nil
	}, projectionState, resourceID, false)
}

func (c *Consumer) handleBareMetalDeletion(
	ctx context.Context,
	event *privatev1.Event,
	mapper events.ResourceMapper,
	existing *projection.ResourceState,
	dims map[string]any,
	transitionTime time.Time,
) error {
	if existing != nil && existing.Deleted {
		return nil
	}
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
		deleted, err := c.store.DeleteIfVersion(ctx, mapper.ResourceID(), mapper.FulfillmentVersion())
		if err != nil {
			return fmt.Errorf("deleting projection for %s: %w", mapper.ResourceID(), err)
		}
		if !deleted {
			c.logger.Info("skipping stale BMaaS delete after projection changed",
				"resource_id", mapper.ResourceID(), "event_version", mapper.FulfillmentVersion())
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
	allocationState := projection.MeterState{}
	consumptionState := projection.MeterState{}
	if existing != nil {
		previousState = existing.CurrentState
		allocationState = existing.BMaaSMeterState.Allocation
		consumptionState = existing.BMaaSMeterState.Consumption
		intervals.AllocationSince = allocationState.ActiveSince
		intervals.ConsumptionSince = consumptionState.ActiveSince
	}

	return events.DecomposeBMIEvents(
		dims,
		eventID,
		transitionTime,
		intervals,
		func(request events.BMaaSEventBuildRequest) (cloudevents.Event, error) {
			return buildBareMetalEvent(mapper, previousState, transitionTime, request)
		},
		mapBMaaSEffectToEvent(allocationEffect, allocationState),
		mapBMaaSEffectToEvent(consumptionEffect, consumptionState),
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
		nil,
		transitionTime,
	)
}

func mapBMaaSEffectToEvent(effect string, meterState projection.MeterState) string {
	switch effect {
	case events.BMaaSEffectStart, events.BMaaSEffectResume:
		if meterState.ActiveSince != nil {
			return ""
		}
		if meterState.FirstStartedAt == nil {
			return events.EventStarted
		}
		return events.EventResumed
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
	state.BMaaSMeterState = projection.BMaaSMeterState{}
	if existing != nil {
		state.BMaaSMeterState = existing.BMaaSMeterState
		state.EverBillable = existing.EverBillable
	}

	switch allocationEffect {
	case events.BMaaSEffectStart, events.BMaaSEffectResume:
		if state.BMaaSMeterState.Allocation.ActiveSince == nil {
			now := transitionTime.UTC()
			state.BMaaSMeterState.Allocation.ActiveSince = &now
		}
		if state.BMaaSMeterState.Allocation.FirstStartedAt == nil {
			now := transitionTime.UTC()
			state.BMaaSMeterState.Allocation.FirstStartedAt = &now
		}
	case events.BMaaSEffectSuspend:
		state.BMaaSMeterState.Allocation.ActiveSince = nil
	}
	state.BillableSince = cloneTimePointer(state.BMaaSMeterState.Allocation.ActiveSince)

	switch consumptionEffect {
	case events.BMaaSEffectStart, events.BMaaSEffectResume:
		if state.BMaaSMeterState.Consumption.ActiveSince == nil {
			now := transitionTime.UTC()
			state.BMaaSMeterState.Consumption.ActiveSince = &now
		}
		if state.BMaaSMeterState.Consumption.FirstStartedAt == nil {
			now := transitionTime.UTC()
			state.BMaaSMeterState.Consumption.FirstStartedAt = &now
		}
	case events.BMaaSEffectSuspend:
		state.BMaaSMeterState.Consumption.ActiveSince = nil
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

func (c *Consumer) shouldSkipUpdate(ctx context.Context, event *privatev1.Event, mapper events.ResourceMapper, existing *projection.ResourceState, currentState string, isBillable bool, dims map[string]any, version int32, transitionTime time.Time) (bool, error) {
	if event.GetType() != privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED ||
		!sameMeteringState(existing, mapper, currentState, isBillable, dims) {
		return false, nil
	}
	if version > existing.FulfillmentVersion {
		existing.FulfillmentVersion = version
		existing.TransitionTime = transitionTime.UTC()
		if err := c.store.Upsert(ctx, *existing); err != nil && !errors.Is(err, projection.ErrStaleVersion) {
			return false, fmt.Errorf("advancing projection version for %s: %w", mapper.ResourceID(), err)
		}
	}
	if !existing.TransitionTime.Truncate(time.Microsecond).Equal(transitionTime.UTC().Truncate(time.Microsecond)) {
		c.logger.Info("skipping replayed event (upserted but likely unpublished)",
			"resource_id", mapper.ResourceID(), "state", currentState)
	} else {
		c.logger.V(1).Info("same state and dimensions, skipping",
			"resource_id", mapper.ResourceID(), "state", currentState)
	}
	return true, nil
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
