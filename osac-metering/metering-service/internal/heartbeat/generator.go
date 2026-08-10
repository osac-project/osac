/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package heartbeat

import (
	"context"
	"fmt"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/osac-project/osac-metering/internal/events"
	kafkapub "github.com/osac-project/osac-metering/internal/kafka"
	"github.com/osac-project/osac-metering/internal/projection"
)

var (
	heartbeatLag = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "osac_metering_heartbeat_lag_seconds",
		Help: "Max staleness of heartbeats per resource type",
	}, []string{"resource_type"})

	projectionResources = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "osac_metering_state_projection_resources",
		Help: "Number of resources in State Projection",
	}, []string{"resource_type", "is_billable"})
)

type Generator struct {
	store     projection.Store
	publisher kafkapub.EventPublisher
	logger    logr.Logger
	interval  time.Duration
}

func NewGenerator(
	store projection.Store,
	publisher kafkapub.EventPublisher,
	logger logr.Logger,
	interval time.Duration,
) *Generator {
	return &Generator{
		store:     store,
		publisher: publisher,
		logger:    logger,
		interval:  interval,
	}
}

func (g *Generator) Run(ctx context.Context) error {
	ticker := time.NewTicker(g.interval)
	defer ticker.Stop()

	g.logger.Info("heartbeat generator started", "interval", g.interval)

	for {
		select {
		case <-ctx.Done():
			g.logger.Info("heartbeat generator stopping")
			return nil
		case <-ticker.C:
			if err := g.tick(ctx); err != nil {
				g.logger.Error(err, "heartbeat tick failed")
			}
		}
	}
}

func (g *Generator) tick(ctx context.Context) error {
	billable, err := g.store.ListBillable(ctx)
	if err != nil {
		return fmt.Errorf("querying billable resources: %w", err)
	}

	g.updateGauges(billable)

	if len(billable) == 0 {
		return nil
	}

	now := time.Now().UTC()
	var publishedIDs []string

	// One resource's publish failure is isolated to that resource: it is
	// skipped for this tick (so its checkpoint does not advance and the
	// reconciler's stale-heartbeat detection picks it back up), but it does
	// not block heartbeats for the other resources in the same tick.
	for i := range billable {
		hbEvents, ceErr := g.buildHeartbeatEvents(&billable[i], now)
		if ceErr != nil {
			g.logger.Error(ceErr, "building heartbeat event, skipping resource",
				"resource_id", billable[i].ResourceID)
			continue
		}
		if !g.publishResourceHeartbeats(ctx, hbEvents, billable[i].ResourceID) {
			continue
		}
		publishedIDs = append(publishedIDs, billable[i].ResourceID)
	}

	if len(publishedIDs) > 0 {
		if err := g.store.UpdateLastHeartbeat(ctx, publishedIDs, now); err != nil {
			return fmt.Errorf("updating last heartbeat: %w", err)
		}
	}

	g.logger.Info("heartbeat tick completed", "published", len(publishedIDs), "total", len(billable))
	return nil
}

// publishResourceHeartbeats publishes every event in one resource's N+1
// fan-out. A failure partway through means the resource is not checkpointed
// this tick, but it does not prevent other resources from heartbeating.
func (g *Generator) publishResourceHeartbeats(ctx context.Context, hbEvents []cloudevents.Event, resourceID string) bool {
	for j := range hbEvents {
		if err := g.publisher.Publish(ctx, hbEvents[j]); err != nil {
			g.logger.Error(err, "publishing heartbeat, resource will not be checkpointed this tick",
				"resource_id", resourceID)
			return false
		}
	}
	return true
}

func (g *Generator) buildHeartbeatEvents(state *projection.ResourceState, now time.Time) ([]cloudevents.Event, error) {
	buildFn := func(dims map[string]any, eventID string) (cloudevents.Event, error) {
		return g.buildHeartbeatEvent(state, eventID, dims, now)
	}

	// Base ID should be reproducible for a given resource and heartbeat
	// window, so that building this same tick's events more than once —
	// e.g. a future in-tick retry — reproduces the same per-component
	// CloudEvent IDs.
	baseID := fmt.Sprintf("hb/%s/%d", state.ResourceID, now.Truncate(g.interval).Unix())
	return events.BuildResourceEvents(state.ResourceType, state.BillingDimensions, baseID, buildFn)
}

func (g *Generator) buildHeartbeatEvent(state *projection.ResourceState, eventID string, dims map[string]any, now time.Time) (cloudevents.Event, error) {
	ce := cloudevents.NewEvent()
	ce.SetID(eventID)
	ce.SetSource("osac-metering")
	ce.SetType(events.EventHeartbeat)
	ce.SetTime(now)

	events.SetOSACExtensions(&ce, state.ResourceID, state.ResourceType, state.TenantID, state.ProjectID)

	var durationSeconds float64
	if state.BillableSince != nil {
		durationSeconds = now.Sub(*state.BillableSince).Seconds()
	}

	data := heartbeatData{
		ResourceID:        state.ResourceID,
		ResourceType:      state.ResourceType,
		TenantID:          state.TenantID,
		ProjectID:         events.NilIfEmpty(state.ProjectID),
		CurrentState:      state.CurrentState,
		DurationSeconds:   durationSeconds,
		BillingDimensions: dims,
		SchemaVersion:     "v1",
	}
	if err := ce.SetData(cloudevents.ApplicationJSON, data); err != nil {
		return ce, fmt.Errorf("setting heartbeat CloudEvent data: %w", err)
	}

	return ce, nil
}

func (g *Generator) updateGauges(billable []projection.ResourceState) {
	projectionResources.Reset()
	heartbeatLag.Reset()

	counts := map[string]int{}
	lags := map[string]float64{}
	now := time.Now()
	for i := range billable {
		rt := billable[i].ResourceType
		counts[rt]++
		if billable[i].LastHeartbeatAt != nil {
			lag := now.Sub(*billable[i].LastHeartbeatAt).Seconds()
			if lag > lags[rt] {
				lags[rt] = lag
			}
		}
	}
	for rt, count := range counts {
		projectionResources.WithLabelValues(rt, "true").Set(float64(count))
		heartbeatLag.WithLabelValues(rt).Set(lags[rt])
	}
}

type heartbeatData struct {
	ResourceID        string         `json:"resource_id"`
	ResourceType      string         `json:"resource_type"`
	TenantID          string         `json:"tenant_id"`
	ProjectID         *string        `json:"project_id"`
	CurrentState      string         `json:"current_state"`
	DurationSeconds   float64        `json:"duration_seconds"`
	BillingDimensions map[string]any `json:"billing_dimensions"`
	SchemaVersion     string         `json:"schema_version"`
}
