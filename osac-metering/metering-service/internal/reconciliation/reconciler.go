/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
	"github.com/osac-project/osac-metering/internal/events"
	kafkapub "github.com/osac-project/osac-metering/internal/kafka"
	"github.com/osac-project/osac-metering/internal/projection"
)

const defaultPageSize = 500

var (
	reconDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "osac_metering_reconciliation_duration_seconds",
		Help:    "Duration of reconciliation passes",
		Buckets: prometheus.DefBuckets,
	})

	reconCorrections = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "osac_metering_reconciliation_corrections_total",
		Help: "Corrections emitted by reconciliation",
	}, []string{"reason", "resource_type"})

	reconLastCompleted = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "osac_metering_reconciliation_last_completed_at",
		Help: "Unix timestamp of last completed reconciliation",
	})
)

type ComputeInstanceLister interface {
	List(ctx context.Context, in *privatev1.ComputeInstancesListRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesListResponse, error)
}

type Reconciler struct {
	computeClient     ComputeInstanceLister
	store             projection.Store
	publisher         kafkapub.EventPublisher
	logger            logr.Logger
	heartbeatInterval time.Duration
}

func NewReconciler(
	computeClient ComputeInstanceLister,
	store projection.Store,
	publisher kafkapub.EventPublisher,
	logger logr.Logger,
	heartbeatInterval time.Duration,
) *Reconciler {
	return &Reconciler{
		computeClient:     computeClient,
		store:             store,
		publisher:         publisher,
		logger:            logger,
		heartbeatInterval: heartbeatInterval,
	}
}

func (r *Reconciler) Reconcile(ctx context.Context) error {
	start := time.Now()
	now := start.UTC()
	r.logger.Info("starting reconciliation")

	fulfillmentState, err := r.loadFulfillmentState(ctx)
	if err != nil {
		return fmt.Errorf("loading fulfillment state: %w", err)
	}

	projectionState, err := r.store.ListAll(ctx)
	if err != nil {
		return fmt.Errorf("loading projection state: %w", err)
	}
	projMap := make(map[string]projection.ResourceState, len(projectionState))
	for _, ps := range projectionState {
		projMap[ps.ResourceID] = ps
	}

	corrections := 0

	n, err := r.reconcileFulfillmentResources(ctx, fulfillmentState, projMap, now)
	corrections += n
	if err != nil {
		return err
	}

	n, err = r.reconcileMissedDeletions(ctx, fulfillmentState, projMap, now)
	corrections += n
	if err != nil {
		return err
	}

	n, err = r.reconcileStaleHeartbeats(ctx, now)
	corrections += n
	if err != nil {
		return err
	}

	duration := time.Since(start)
	reconDuration.Observe(duration.Seconds())
	reconLastCompleted.SetToCurrentTime()
	r.logger.Info("reconciliation completed",
		"duration", duration,
		"corrections", corrections,
		"fulfillment_resources", len(fulfillmentState),
		"projection_resources", len(projMap),
	)

	return nil
}

func (r *Reconciler) reconcileFulfillmentResources(ctx context.Context, fulfillmentState map[string]fulfillmentResource, projMap map[string]projection.ResourceState, now time.Time) (int, error) {
	corrections := 0

	for id, fs := range fulfillmentState {
		ps, exists := projMap[id]
		if !exists {
			ce, ceErr := buildCorrectionEvent(id, "compute_instance", fs.tenantID, fs.projectID,
				MissedCreation, "", fs.state, fs.billingDimensions, nil, now)
			if ceErr != nil {
				return corrections, fmt.Errorf("building missed_creation event for %s: %w", id, ceErr)
			}
			if err := r.publisher.Publish(ctx, ce); err != nil {
				return corrections, fmt.Errorf("publishing missed_creation for %s: %w", id, err)
			}
			reconCorrections.WithLabelValues(string(MissedCreation), "compute_instance").Inc()
			corrections++

			isBillable := events.IsBillableState(fs.state)
			newState := projection.ResourceState{
				ResourceID:         id,
				ResourceType:       "compute_instance",
				TenantID:           fs.tenantID,
				ProjectID:          fs.projectID,
				CurrentState:       fs.state,
				IsBillable:         isBillable,
				TransitionTime:     now,
				FulfillmentVersion: fs.version,
				BillingDimensions:  fs.billingDimensions,
			}
			if isBillable {
				newState.BillableSince = &now
			}
			if err := r.store.Upsert(ctx, newState); err != nil {
				if errors.Is(err, projection.ErrStaleVersion) {
					r.logger.Info("stale version during reconciliation, skipping", "resource_id", id)
				} else {
					return corrections, fmt.Errorf("upserting missed creation for %s: %w", id, err)
				}
			}
			continue
		}

		if fs.version < ps.FulfillmentVersion {
			r.logger.V(1).Info("projection ahead of fulfillment, skipping",
				"resource_id", id,
				"fulfillment_version", fs.version,
				"projection_version", ps.FulfillmentVersion)
			continue
		}

		if fs.version > ps.FulfillmentVersion &&
			ps.CurrentState == fs.state &&
			events.DimensionsEqual(ps.BillingDimensions, fs.billingDimensions) {
			ps.FulfillmentVersion = fs.version
			if err := r.store.Upsert(ctx, ps); err != nil && !errors.Is(err, projection.ErrStaleVersion) {
				return corrections, fmt.Errorf("advancing fulfillment version for %s: %w", id, err)
			}
			continue
		}

		if ps.CurrentState != fs.state {
			ce, ceErr := buildCorrectionEvent(id, "compute_instance", fs.tenantID, fs.projectID,
				StateDrift, ps.CurrentState, fs.state, fs.billingDimensions, nil, now)
			if ceErr != nil {
				return corrections, fmt.Errorf("building state_drift event for %s: %w", id, ceErr)
			}
			if err := r.publisher.Publish(ctx, ce); err != nil {
				return corrections, fmt.Errorf("publishing state_drift for %s: %w", id, err)
			}
			reconCorrections.WithLabelValues(string(StateDrift), "compute_instance").Inc()
			corrections++

			isBillable := events.IsBillableState(fs.state)
			ps.PreviousState = ps.CurrentState
			ps.CurrentState = fs.state
			wasBillable := ps.IsBillable
			ps.IsBillable = isBillable
			ps.FulfillmentVersion = fs.version
			ps.TransitionTime = now
			if isBillable && !wasBillable {
				ps.BillableSince = &now
			}
			if !isBillable {
				ps.BillableSince = nil
			}
			if err := r.store.Upsert(ctx, ps); err != nil {
				if errors.Is(err, projection.ErrStaleVersion) {
					r.logger.Info("stale version during reconciliation, skipping", "resource_id", id)
				} else {
					return corrections, fmt.Errorf("upserting state drift for %s: %w", id, err)
				}
			}
		} else if !events.DimensionsEqual(ps.BillingDimensions, fs.billingDimensions) {
			ce, ceErr := buildCorrectionEvent(id, "compute_instance", fs.tenantID, fs.projectID,
				BillingDimensionsDrift, ps.CurrentState, fs.state, fs.billingDimensions, nil, now)
			if ceErr != nil {
				return corrections, fmt.Errorf("building billing_dimensions_drift event for %s: %w", id, ceErr)
			}
			if err := r.publisher.Publish(ctx, ce); err != nil {
				return corrections, fmt.Errorf("publishing billing_dimensions_drift for %s: %w", id, err)
			}
			reconCorrections.WithLabelValues(string(BillingDimensionsDrift), "compute_instance").Inc()
			corrections++

			ps.BillingDimensions = fs.billingDimensions
			ps.FulfillmentVersion = fs.version
			ps.TransitionTime = now
			if err := r.store.Upsert(ctx, ps); err != nil {
				if errors.Is(err, projection.ErrStaleVersion) {
					r.logger.Info("stale version during reconciliation, skipping", "resource_id", id)
				} else {
					return corrections, fmt.Errorf("upserting billing dimensions drift for %s: %w", id, err)
				}
			}
		}
	}

	return corrections, nil
}

func (r *Reconciler) reconcileMissedDeletions(ctx context.Context, fulfillmentState map[string]fulfillmentResource, projMap map[string]projection.ResourceState, now time.Time) (int, error) {
	corrections := 0

	for id, ps := range projMap {
		if _, exists := fulfillmentState[id]; !exists {
			ce, ceErr := buildCorrectionEvent(id, ps.ResourceType, ps.TenantID, ps.ProjectID,
				MissedDeletion, ps.CurrentState, "", ps.BillingDimensions, nil, now)
			if ceErr != nil {
				return corrections, fmt.Errorf("building missed_deletion event for %s: %w", id, ceErr)
			}
			if err := r.publisher.Publish(ctx, ce); err != nil {
				return corrections, fmt.Errorf("publishing missed_deletion for %s: %w", id, err)
			}
			reconCorrections.WithLabelValues(string(MissedDeletion), ps.ResourceType).Inc()
			corrections++

			if err := r.store.Delete(ctx, id); err != nil {
				return corrections, fmt.Errorf("deleting missed deletion for %s: %w", id, err)
			}
		}
	}

	return corrections, nil
}

func (r *Reconciler) reconcileStaleHeartbeats(ctx context.Context, now time.Time) (int, error) {
	// Stale heartbeat detection: reload projection from DB so we see the
	// corrected state after drift/creation/deletion upserts above. Using
	// projMap here would read stale IsBillable values for corrected resources.
	freshProjection, err := r.store.ListBillable(ctx)
	if err != nil {
		return 0, fmt.Errorf("loading billable resources for heartbeat check: %w", err)
	}
	corrections := 0
	var heartbeatIDs []string
	for i := range freshProjection {
		ps := &freshProjection[i]
		if ps.LastHeartbeatAt == nil || now.Sub(*ps.LastHeartbeatAt) > 2*r.heartbeatInterval {
			hbEvent, hbErr := buildSyntheticHeartbeat(*ps, now)
			if hbErr != nil {
				r.logger.Error(hbErr, "building synthetic heartbeat", "resource_id", ps.ResourceID)
				continue
			}
			if err := r.publisher.Publish(ctx, hbEvent); err != nil {
				r.logger.Error(err, "publishing synthetic heartbeat", "resource_id", ps.ResourceID)
				continue
			}
			heartbeatIDs = append(heartbeatIDs, ps.ResourceID)
			reconCorrections.WithLabelValues("stale_heartbeat", ps.ResourceType).Inc()
			corrections++
		}
	}
	if len(heartbeatIDs) > 0 {
		if err := r.store.UpdateLastHeartbeat(ctx, heartbeatIDs, now); err != nil {
			return corrections, fmt.Errorf("updating last heartbeat after synthetic heartbeats: %w", err)
		}
	}

	return corrections, nil
}

func (r *Reconciler) RunPeriodic(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	r.logger.Info("periodic reconciliation started", "interval", interval)

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("periodic reconciliation stopping")
			return
		case <-ticker.C:
			if err := r.Reconcile(ctx); err != nil {
				r.logger.Error(err, "periodic reconciliation failed")
			}
		}
	}
}

type fulfillmentResource struct {
	state             string
	version           int32
	tenantID          string
	projectID         string
	billingDimensions map[string]any
}

func (r *Reconciler) loadFulfillmentState(ctx context.Context) (map[string]fulfillmentResource, error) {
	result := make(map[string]fulfillmentResource)
	var offset int32

	for {
		limit := int32(defaultPageSize)
		resp, err := r.computeClient.List(ctx, &privatev1.ComputeInstancesListRequest{
			Offset: &offset,
			Limit:  &limit,
		})
		if err != nil {
			return nil, fmt.Errorf("listing compute instances (offset=%d): %w", offset, err)
		}

		items := resp.GetItems()
		for _, ci := range items {
			state := "UNSPECIFIED"
			if s := ci.GetStatus(); s != nil {
				state = strings.TrimPrefix(s.GetState().String(), events.ComputeInstanceStatePrefix)
			}
			tenantID := ""
			projectID := ""
			var version int32
			if md := ci.GetMetadata(); md != nil {
				tenantID = md.GetTenant()
				projectID = md.GetProject()
				version = md.GetVersion()
			}
			result[ci.GetId()] = fulfillmentResource{
				state:             state,
				version:           version,
				tenantID:          tenantID,
				projectID:         projectID,
				billingDimensions: events.ComputeInstanceBillingDimensions(ci),
			}
		}

		if len(items) < defaultPageSize {
			break
		}
		offset += int32(len(items))
	}

	return result, nil
}

func buildSyntheticHeartbeat(ps projection.ResourceState, now time.Time) (cloudevents.Event, error) {
	ce := cloudevents.NewEvent()
	ce.SetID(uuid.NewString())
	ce.SetSource("osac-metering/reconciler")
	ce.SetType("osac.resource.heartbeat.v1")
	ce.SetTime(now)
	events.SetOSACExtensions(&ce, ps.ResourceID, ps.ResourceType, ps.TenantID, ps.ProjectID)

	var durationSeconds float64
	if ps.BillableSince != nil {
		durationSeconds = now.Sub(*ps.BillableSince).Seconds()
	}

	data := map[string]any{
		"resource_id":        ps.ResourceID,
		"resource_type":      ps.ResourceType,
		"tenant_id":          ps.TenantID,
		"project_id":         events.NilIfEmpty(ps.ProjectID),
		"current_state":      ps.CurrentState,
		"duration_seconds":   durationSeconds,
		"billing_dimensions": ps.BillingDimensions,
		"schema_version":     "v1",
	}
	if err := ce.SetData(cloudevents.ApplicationJSON, data); err != nil {
		return ce, fmt.Errorf("setting synthetic heartbeat CloudEvent data: %w", err)
	}
	return ce, nil
}
