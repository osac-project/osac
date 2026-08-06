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
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
	"github.com/osac-project/osac-metering/internal/events"
	kafkapub "github.com/osac-project/osac-metering/internal/kafka"
	"github.com/osac-project/osac-metering/internal/projection"
)

var watchReconnects = promauto.NewCounter(prometheus.CounterOpts{
	Name: "osac_metering_watch_stream_reconnects_total",
	Help: "Total Watch stream reconnections",
})

const (
	defaultInitialDelay   = 1 * time.Second
	defaultMaxDelay       = 30 * time.Second
	defaultHandlerRetries = 3
	meteringFilter        = "has(event.compute_instance) || has(event.cluster)"
)

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
	filter := meteringFilter
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
	dims := mapper.BillingDimensionsMap()

	existing, err := c.store.Get(ctx, resourceID)
	if err != nil {
		return fmt.Errorf("reading projection for %s: %w", resourceID, err)
	}

	transitionTime, err := mapper.TransitionTime(event.GetType())
	if err != nil {
		return err
	}

	if c.shouldSkipUpdate(event, existing, currentState, dims, transitionTime, resourceID) {
		return nil
	}

	stateCtx := c.buildStateContext(existing, isBillable, transitionTime, dims)

	ce, err := events.MapWatchEvent(event, mapper, stateCtx)
	if err != nil {
		if errors.Is(err, events.ErrTransientState) {
			return c.handleTransientState(ctx, mapper, existing, version, transitionTime)
		}
		if errors.Is(err, events.ErrSkipNonBillingTransition) {
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

	err = c.store.Upsert(ctx, projState)
	if err != nil {
		if errors.Is(err, projection.ErrStaleVersion) {
			c.logger.Info("stale version, skipping projection update",
				"resource_id", resourceID, "version", version)
			return nil
		}
		return fmt.Errorf("upserting projection for %s: %w", resourceID, err)
	}

	if err := c.publishLifecycleEvents(ctx, ce, mapper, event.GetId()); err != nil {
		return err
	}

	return nil
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

func (c *Consumer) publishLifecycleEvents(ctx context.Context, baseCE *cloudevents.Event, mapper events.ResourceMapper, eventID string) error {
	if mapper.ResourceType() != "cluster_order" ||
		baseCE.Type() == "osac.resource.created.v1" ||
		baseCE.Type() == "osac.resource.deleted.v1" {
		return c.publishWithRetry(ctx, baseCE)
	}

	components := events.DecomposeClusterComponents(mapper.BillingDimensionsMap())
	if len(components) == 0 {
		return c.publishWithRetry(ctx, baseCE)
	}

	for _, comp := range components {
		compCE, ceErr := c.buildComponentEvent(baseCE, eventID, comp)
		if ceErr != nil {
			return ceErr
		}
		if err := c.publishWithRetry(ctx, &compCE); err != nil {
			return err
		}
	}
	return nil
}

func (c *Consumer) handleScalingEvent(ctx context.Context, event *privatev1.Event, mapper events.ResourceMapper, existing *projection.ResourceState, transitionTime time.Time, version int32, currentState string, isBillable bool, dims map[string]any) error {
	resourceID := mapper.ResourceID()
	changed := events.ChangedComponents(existing.BillingDimensions, dims)

	projState := c.buildProjectionState(mapper, existing, transitionTime, version, currentState, isBillable, dims)
	if err := c.store.Upsert(ctx, projState); err != nil {
		if errors.Is(err, projection.ErrStaleVersion) {
			c.logger.Info("stale version during scaling, skipping", "resource_id", resourceID)
			return nil
		}
		return fmt.Errorf("upserting projection for scaling %s: %w", resourceID, err)
	}

	if len(changed) == 0 {
		c.logger.V(1).Info("non-component dimension change, projection updated",
			"resource_id", resourceID)
		return nil
	}

	stateCtx := c.buildStateContext(existing, isBillable, transitionTime, dims)
	for _, comp := range changed {
		ce, ceErr := c.buildScalingEvent(event.GetId(), mapper, comp, stateCtx, transitionTime)
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

func (c *Consumer) buildComponentEvent(baseCE *cloudevents.Event, eventID string, comp events.ComponentRecord) (cloudevents.Event, error) {
	ce := cloudevents.NewEvent()
	ce.SetID(events.ComponentEventID(eventID, comp))
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

	baseData["billing_dimensions"] = comp.FlatBillingDimensions()
	if err := ce.SetData(cloudevents.ApplicationJSON, baseData); err != nil {
		return ce, fmt.Errorf("setting component event data: %w", err)
	}
	return ce, nil
}

func (c *Consumer) buildScalingEvent(eventID string, mapper events.ResourceMapper, comp events.ComponentRecord, stateCtx *events.StateContext, transitionTime time.Time) (cloudevents.Event, error) {
	ce := cloudevents.NewEvent()
	ce.SetID(events.ComponentEventID(eventID, comp))
	ce.SetSource("osac-metering")
	ce.SetType("osac.resource.updated.v1")
	ce.SetTime(transitionTime)

	projectID := ""
	if p := mapper.ProjectID(); p != nil {
		projectID = *p
	}
	events.SetOSACExtensions(&ce, mapper.ResourceID(), mapper.ResourceType(), mapper.TenantID(), projectID)

	var prevStatePtr *string
	if stateCtx.PreviousState != "" {
		prevStatePtr = &stateCtx.PreviousState
	}

	data := map[string]any{
		"resource_id":        mapper.ResourceID(),
		"resource_type":      mapper.ResourceType(),
		"tenant_id":          mapper.TenantID(),
		"project_id":         mapper.ProjectID(),
		"catalog_item_id":    mapper.CatalogItemID(),
		"template_id":        mapper.TemplateID(),
		"previous_state":     prevStatePtr,
		"current_state":      mapper.CurrentState(),
		"transition_time":    transitionTime.Format(time.RFC3339Nano),
		"duration_seconds":   stateCtx.DurationSeconds,
		"billing_dimensions": comp.FlatBillingDimensions(),
		"schema_version":     "v1",
	}
	if err := ce.SetData(cloudevents.ApplicationJSON, data); err != nil {
		return ce, fmt.Errorf("setting scaling event data: %w", err)
	}
	return ce, nil
}
func (c *Consumer) shouldSkipUpdate(event *privatev1.Event, existing *projection.ResourceState, currentState string, dims map[string]any, transitionTime time.Time, resourceID string) bool {
	if event.GetType() != privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED || existing == nil {
		return false
	}
	if existing.CurrentState != currentState || !events.DimensionsEqual(existing.BillingDimensions, dims) {
		return false
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
	projState := projection.ResourceState{
		ResourceID:         mapper.ResourceID(),
		ResourceType:       mapper.ResourceType(),
		TenantID:           mapper.TenantID(),
		CurrentState:       currentState,
		IsBillable:         isBillable,
		TransitionTime:     tt,
		FulfillmentVersion: version,
		BillingDimensions:  dims,
	}
	if p := mapper.ProjectID(); p != nil {
		projState.ProjectID = *p
	}
	if existing != nil {
		projState.PreviousState = existing.CurrentState
		projState.LastHeartbeatAt = existing.LastHeartbeatAt
	}
	if isBillable {
		if existing == nil || !existing.IsBillable || !events.DimensionsEqual(existing.BillingDimensions, dims) {
			projState.BillableSince = &tt
		} else {
			projState.BillableSince = existing.BillableSince
		}
	}
	return projState
}

func (c *Consumer) buildStateContext(existing *projection.ResourceState, nowBillable bool, transitionTime time.Time, newDims map[string]any) *events.StateContext {
	if existing == nil {
		return &events.StateContext{}
	}

	sc := &events.StateContext{
		PreviousState: existing.CurrentState,
		WasBillable:   existing.IsBillable,
		NewDimensions: newDims,
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
	resourceID, _ := ce.Context.GetExtension("osacresourceid")
	tenantID, _ := ce.Context.GetExtension("osactenant")
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
