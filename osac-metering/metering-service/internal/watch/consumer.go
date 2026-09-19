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
	parts = append(parts, "has(event.external_ip)", "has(event.nat_gateway)")
	return strings.Join(parts, " || ")
}

// Consumer connects to the fulfillment-service gRPC Watch stream, maps
// incoming events to CloudEvents, and publishes them to Kafka. It
// automatically reconnects with exponential backoff when the stream breaks.
type Consumer struct {
	client               privatev1.EventsClient
	ExternalIPPoolClient privatev1.ExternalIPPoolsClient
	publisher            kafkapub.EventPublisher
	store                projection.Store
	logger               logr.Logger
	DeploymentID         string
	ExternalIPPools      map[string]string

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
	mapperContext, err := c.mapperContext(ctx, event)
	if err != nil {
		return err
	}
	mapper, err := events.MapperForEventWithContext(event, mapperContext)
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
		return fmt.Errorf("building billing dimensions for %s: %w", resourceID, err)
	}

	existing, err := c.store.Get(ctx, resourceID)
	if err != nil {
		return fmt.Errorf("reading projection for %s: %w", resourceID, err)
	}

	previousState := ""
	if existing != nil {
		previousState = existing.CurrentState
	}
	transitionTime, err := mapper.TransitionTime(event, previousState)
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
	if transitionTimeIsStale(existing, event.GetType(), currentState, version, transitionTime) {
		c.logger.Info("skipping Watch event with stale transition time",
			"resource_id", resourceID,
			"event_version", version,
			"projection_version", existing.FulfillmentVersion,
			"event_transition_time", transitionTime,
			"projection_transition_time", existing.TransitionTime)
		return nil
	}
	if mapper.ResourceType() == events.ResourceTypeBareMetalInstance &&
		event.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED &&
		existing != nil && !events.DimensionsEqual(existing.BillingDimensions, dims) {
		eventsSkipped.WithLabelValues("bmaas_dimension_drift").Inc()
		c.logger.Info("skipping BMaaS event with immutable billing dimension drift",
			"resource_id", resourceID,
			"event_version", version)
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
		if err := c.publishLifecycleEvents(ctx, ce, mapper, event.GetId(), dims); err != nil {
			return err
		}
		if existing != nil {
			deleted, err := c.store.DeleteIfVersion(ctx, resourceID, version)
			if err != nil {
				return fmt.Errorf("deleting projection for %s: %w", resourceID, err)
			}
			if !deleted {
				c.logger.Info("skipping stale delete after projection changed", "resource_id", resourceID, "event_version", version)
			}
		}
		return nil
	}

	return c.publishAndUpsert(ctx, func() error {
		return c.publishLifecycleEvents(ctx, ce, mapper, event.GetId(), dims)
	}, projState, resourceID)
}

func (c *Consumer) mapperContext(ctx context.Context, event *privatev1.Event) (events.MapperContext, error) {
	context := events.MapperContext{
		DeploymentID:    c.DeploymentID,
		ExternalIPPools: c.ExternalIPPools,
	}
	ip := event.GetExternalIp()
	if ip == nil {
		gateway := event.GetNatGateway()
		if gateway == nil {
			return context, nil
		}
		return context, nil
	}
	poolID := ip.GetSpec().GetPool().GetId()
	if _, ok := context.ExternalIPPools[poolID]; ok {
		return context, nil
	}
	if c.ExternalIPPoolClient == nil {
		return context, fmt.Errorf("external IP pool client is required for pool %s", poolID)
	}
	response, err := c.ExternalIPPoolClient.Get(ctx, &privatev1.ExternalIPPoolsGetRequest{Id: poolID})
	if err != nil {
		return context, fmt.Errorf("getting external IP pool %s: %w", poolID, err)
	}
	if response.GetObject().GetId() != poolID {
		return context, fmt.Errorf("external IP pool lookup returned %s for requested pool %s", response.GetObject().GetId(), poolID)
	}
	family, err := events.ExternalIPPoolFamily(response.GetObject())
	if err != nil {
		return context, err
	}
	if c.ExternalIPPools == nil {
		c.ExternalIPPools = make(map[string]string)
	}
	c.ExternalIPPools[poolID] = family
	context.ExternalIPPools = c.ExternalIPPools
	return context, nil
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
	if existing.Deleted {
		return true
	}
	if existing.FulfillmentVersion > version {
		return true
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

// DimComponents is the billing dimensions key for the nested components array.
const DimComponents = "components"

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
		}, projectionState, resourceID)
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
		// VMaaS and networking use a single updated.v1. An ExternalIP
		// dimension change closes the prior slice with its prior dimensions.
		scalingDims := dims
		if mapper.ResourceType() == events.ResourceTypeExternalIP {
			scalingDims = existing.BillingDimensions
		}
		ce, ceErr := c.buildScalingEvent(event.GetId(), mapper, scalingDims, stateCtx, transitionTime)
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
