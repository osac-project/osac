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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"

	"github.com/osac-project/osac-metering/internal/events"
	"github.com/osac-project/osac-metering/internal/heartbeat"
	kafkapub "github.com/osac-project/osac-metering/internal/kafka"
	"github.com/osac-project/osac-metering/internal/projection"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
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

	bmaasReconciliationHolds = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "osac_metering_bmaas_reconciliation_holds_total",
		Help: "BMaaS reconciliation passes held by reason",
	}, []string{"reason"})

	reconLastCompleted = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "osac_metering_reconciliation_last_completed_at",
		Help: "Unix timestamp of last completed reconciliation",
	})
)

type ComputeInstancesClient interface {
	List(ctx context.Context, in *privatev1.ComputeInstancesListRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesListResponse, error)
}

type ClustersClient interface {
	List(ctx context.Context, in *privatev1.ClustersListRequest, opts ...grpc.CallOption) (*privatev1.ClustersListResponse, error)
}

type BareMetalInstancesClient interface {
	List(ctx context.Context, in *privatev1.BareMetalInstancesListRequest, opts ...grpc.CallOption) (*privatev1.BareMetalInstancesListResponse, error)
}

var ErrBMaaSReplayUnavailable = errors.New("BMaaS replay unavailable")

// BMaaSReplayRecord is the mapped form of one durable fulfillment event.
// Records are returned in source order and preserve the event identity and
// authoritative transition timestamp needed for deterministic replay.
type BMaaSReplayRecord struct {
	ResourceID     string
	TenantID       string
	ProjectID      string
	State          string
	Version        int32
	EventType      privatev1.EventType
	EventID        string
	BillingDims    map[string]any
	TransitionTime time.Time
}

type BMaaSReplaySource interface {
	// Replay returns records with versions in (fromVersion, toVersion], in
	// oldest-first order. OBJECT_DELETED may reuse the prior version and is
	// therefore also returned when its version equals fromVersion.
	Replay(ctx context.Context, resourceID string, fromVersion, toVersion int32) ([]BMaaSReplayRecord, error)
}

type unavailableBMaaSReplaySource struct{}

func NewUnavailableBMaaSReplaySource() BMaaSReplaySource {
	return unavailableBMaaSReplaySource{}
}

func (unavailableBMaaSReplaySource) Replay(context.Context, string, int32, int32) ([]BMaaSReplayRecord, error) {
	return nil, ErrBMaaSReplayUnavailable
}

type Reconciler struct {
	computeClient     ComputeInstancesClient
	clusterClient     ClustersClient
	bareMetalClient   BareMetalInstancesClient
	replaySource      BMaaSReplaySource
	store             projection.Store
	publisher         kafkapub.EventPublisher
	logger            logr.Logger
	heartbeatInterval time.Duration
	bmaasHolds        map[string]struct{}
	bmaasHoldMetrics  map[string]struct{}
	bmaasSkipped      map[string]struct{}
	bmaasPresence     *heartbeat.BMaaSPresence
}

func (r *Reconciler) SetBMaaSPresence(presence *heartbeat.BMaaSPresence) {
	r.bmaasPresence = presence
}

func NewReconciler(
	computeClient ComputeInstancesClient,
	clusterClient ClustersClient,
	bareMetalClient BareMetalInstancesClient,
	replaySource BMaaSReplaySource,
	store projection.Store,
	publisher kafkapub.EventPublisher,
	logger logr.Logger,
	heartbeatInterval time.Duration,
) *Reconciler {
	if replaySource == nil {
		replaySource = NewUnavailableBMaaSReplaySource()
	}
	return &Reconciler{
		computeClient:     computeClient,
		clusterClient:     clusterClient,
		bareMetalClient:   bareMetalClient,
		replaySource:      replaySource,
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
	r.bmaasHolds = make(map[string]struct{})
	r.bmaasHoldMetrics = make(map[string]struct{})
	r.bmaasSkipped = make(map[string]struct{})

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

	n, err = r.reconcileStaleHeartbeats(ctx, fulfillmentState, now)
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

func (r *Reconciler) publishCorrections(ctx context.Context, id, resourceType, tenantID, projectID string, reason CorrectionReason, projState, sourceState string, dims map[string]any, now time.Time) error {
	ces, err := buildCorrectionEvents(id, resourceType, tenantID, projectID,
		reason, projState, sourceState, dims, nil, now)
	if err != nil {
		return fmt.Errorf("building %s event for %s: %w", reason, id, err)
	}
	for _, ce := range ces {
		if err := r.publisher.Publish(ctx, ce); err != nil {
			return fmt.Errorf("publishing %s for %s: %w", reason, id, err)
		}
	}
	reconCorrections.WithLabelValues(string(reason), resourceType).Inc()
	return nil
}

func (r *Reconciler) publishBMaaSCorrections(
	ctx context.Context,
	id, tenantID, projectID string,
	reason CorrectionReason,
	projectionState, sourceState string,
	dims map[string]any,
	intervals events.BMaaSMeterIntervals,
	allocationEffect, consumptionEffect string,
	allocationEverStarted, consumptionEverStarted bool,
	transitionTime time.Time,
) (bool, error) {
	ces, err := buildBMaaSCorrectionEvents(
		id, tenantID, projectID, reason, projectionState, sourceState, dims, intervals,
		allocationEffect, consumptionEffect, allocationEverStarted, consumptionEverStarted, transitionTime,
	)
	if err != nil {
		return false, fmt.Errorf("building %s events for %s: %w", reason, id, err)
	}
	for _, ce := range ces {
		if err := r.publisher.Publish(ctx, ce); err != nil {
			return false, fmt.Errorf("publishing %s for %s: %w", reason, id, err)
		}
	}
	if len(ces) == 0 {
		return false, nil
	}
	reconCorrections.WithLabelValues(string(reason), events.ResourceTypeBareMetalInstance).Inc()
	return true, nil
}

func (r *Reconciler) reconcileFulfillmentResources(ctx context.Context, fulfillmentState map[string]fulfillmentResource, projMap map[string]projection.ResourceState, now time.Time) (int, error) {
	corrections := 0

	for id, fs := range fulfillmentState {
		if fs.resourceType == events.ResourceTypeBareMetalInstance {
			ps, exists := projMap[id]
			n, err := r.reconcileBareMetalFulfillmentResource(ctx, id, fs, ps, exists)
			corrections += n
			if err != nil {
				return corrections, err
			}
			continue
		}

		ps, exists := projMap[id]
		if !exists {
			if isTransientForType(fs.resourceType, fs.state) {
				r.logger.V(1).Info("fulfillment reports transient state for unknown resource, skipping",
					"resource_id", id,
					"fulfillment_state", fs.state)
				continue
			}
			if err := r.publishCorrections(ctx, id, fs.resourceType, fs.tenantID, fs.projectID,
				MissedCreation, "", fs.state, fs.billingDimensions, now); err != nil {
				return corrections, err
			}
			corrections++

			isBillable, billErr := isBillableForType(fs.resourceType, fs.state)
			if billErr != nil {
				return corrections, fmt.Errorf("checking billability for %s: %w", id, billErr)
			}
			newState := projection.ResourceState{
				ResourceID:         id,
				ResourceType:       fs.resourceType,
				TenantID:           fs.tenantID,
				ProjectID:          fs.projectID,
				CurrentState:       fs.state,
				IsBillable:         isBillable,
				EverBillable:       isBillable,
				TransitionTime:     now,
				FulfillmentVersion: fs.version,
				BillingDimensions:  fs.billingDimensions,
			}
			if isBillable {
				newState.BillableSince = &now
				newState.ComponentBillableSince = events.NextComponentBillableSince(nil, nil, fs.billingDimensions, now)
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

		if isTransientForType(fs.resourceType, fs.state) {
			if fs.version > ps.FulfillmentVersion {
				r.logger.V(1).Info("fulfillment reports transient state, advancing version only",
					"resource_id", id,
					"projection_state", ps.CurrentState,
					"fulfillment_state", fs.state)
				ps.FulfillmentVersion = fs.version
				if err := r.store.Upsert(ctx, ps); err != nil && !errors.Is(err, projection.ErrStaleVersion) {
					return corrections, fmt.Errorf("advancing version for transient state %s: %w", id, err)
				}
			}
			continue
		}

		if ps.CurrentState != fs.state {
			if err := r.publishCorrections(ctx, id, fs.resourceType, fs.tenantID, fs.projectID,
				StateDrift, ps.CurrentState, fs.state, fs.billingDimensions, now); err != nil {
				return corrections, err
			}
			corrections++

			isBillable, billErr := isBillableForType(fs.resourceType, fs.state)
			if billErr != nil {
				return corrections, fmt.Errorf("checking billability for %s: %w", id, billErr)
			}
			ps.PreviousState = ps.CurrentState
			ps.CurrentState = fs.state
			wasBillable := ps.IsBillable
			ps.IsBillable = isBillable
			ps.EverBillable = ps.EverBillable || isBillable
			ps.FulfillmentVersion = fs.version
			ps.TransitionTime = now
			if isBillable && !wasBillable {
				ps.BillableSince = &now
				ps.ComponentBillableSince = events.NextComponentBillableSince(nil, nil, fs.billingDimensions, now)
			}
			if !isBillable {
				ps.BillableSince = nil
				ps.ComponentBillableSince = nil
			}
			if err := r.store.Upsert(ctx, ps); err != nil {
				if errors.Is(err, projection.ErrStaleVersion) {
					r.logger.Info("stale version during reconciliation, skipping", "resource_id", id)
				} else {
					return corrections, fmt.Errorf("upserting state drift for %s: %w", id, err)
				}
			}
		} else if !events.DimensionsEqual(ps.BillingDimensions, fs.billingDimensions) {
			if err := r.publishCorrections(ctx, id, fs.resourceType, fs.tenantID, fs.projectID,
				BillingDimensionsDrift, ps.CurrentState, fs.state, fs.billingDimensions, now); err != nil {
				return corrections, err
			}
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

func (r *Reconciler) reconcileBareMetalFulfillmentResource(ctx context.Context, id string, fs fulfillmentResource, ps projection.ResourceState, exists bool) (int, error) {
	if fs.transitionTime == nil {
		r.holdBMaaS(id, "missing_transition_time")
		r.logger.Info("holding bare metal instance reconciliation without source transition timestamp", "resource_id", id)
		return 0, nil
	}
	transitionTime := fs.transitionTime.UTC()
	if !exists {
		records, err := r.replay(ctx, id, 0, fs.version)
		if err != nil {
			if !errors.Is(err, ErrBMaaSReplayUnavailable) {
				return 0, err
			}
			r.holdBMaaS(id, "missing_replay")
			r.logger.Info("holding bare metal instance reconciliation while history is unavailable", "resource_id", id, "error", err)
			return 0, nil
		}
		expectedVersion := int32(1)
		for _, record := range records {
			if record.State == "" || record.TransitionTime.IsZero() || record.Version != expectedVersion || record.EventType == privatev1.EventType_EVENT_TYPE_OBJECT_DELETED {
				r.holdBMaaS(id, "incomplete_history")
				r.logger.Info("holding bare metal instance reconciliation with incomplete creation history", "resource_id", id)
				return 0, nil
			}
			expectedVersion++
		}
		if len(records) == 0 {
			r.holdBMaaS(id, "incomplete_history")
			r.logger.Info("holding bare metal instance reconciliation with incomplete history", "resource_id", id)
			return 0, nil
		}
		lastRecord := records[len(records)-1]
		if lastRecord.Version != fs.version || lastRecord.State != fs.state || lastRecord.BillingDims == nil || !events.DimensionsEqual(lastRecord.BillingDims, fs.billingDimensions) {
			r.holdBMaaS(id, "replay_endpoint_drift")
			r.logger.Info("holding bare metal instance reconciliation with replay endpoint drift", "resource_id", id)
			return 0, nil
		}
		corrections := 0
		state := projection.ResourceState{}
		for _, record := range records {
			recordFS := fulfillmentResource{
				resourceType:      events.ResourceTypeBareMetalInstance,
				state:             record.State,
				version:           record.Version,
				tenantID:          record.TenantID,
				projectID:         record.ProjectID,
				billingDimensions: record.BillingDims,
				transitionTime:    &record.TransitionTime,
			}
			if recordFS.tenantID == "" {
				recordFS.tenantID = fs.tenantID
			}
			if recordFS.projectID == "" {
				recordFS.projectID = fs.projectID
			}
			if recordFS.billingDimensions == nil {
				recordFS.billingDimensions = fs.billingDimensions
			}
			n, next, applyErr := r.applyBMaaSTransition(ctx, id, state, recordFS, MissedCreation, false)
			if applyErr != nil {
				return corrections, applyErr
			}
			if n == 0 && next.FulfillmentVersion == state.FulfillmentVersion && next.CurrentState == state.CurrentState {
				r.holdBMaaS(id, "incomplete_history")
				return corrections, nil
			}
			corrections += n
			state = next
		}
		return corrections, nil
	}

	if fs.version < ps.FulfillmentVersion {
		r.logger.V(1).Info("projection ahead of fulfillment, skipping", "resource_id", id,
			"fulfillment_version", fs.version, "projection_version", ps.FulfillmentVersion)
		return 0, nil
	}
	if fs.version > ps.FulfillmentVersion+1 ||
		(fs.version > ps.FulfillmentVersion && ps.CurrentState == fs.state && events.DimensionsEqual(ps.BillingDimensions, fs.billingDimensions)) ||
		(ps.CurrentState == "RUNNING" && fs.state == "STOPPED") {
		return r.replayBMaaSGap(ctx, id, ps, fs)
	}
	reason := StateDrift
	dimensionDrift := false
	allocationEffect, err := events.ResolveAllocationTransition(ps.CurrentState, fs.state)
	if err != nil {
		r.holdBMaaS(id, "unobserved_transition")
		r.logger.Info("holding bare metal instance reconciliation with an unobserved state transition", "resource_id", id, "error", err)
		return 0, nil
	}
	consumptionEffect, err := events.ResolveConsumptionTransition(ps.CurrentState, fs.state)
	if err != nil {
		r.holdBMaaS(id, "unobserved_transition")
		r.logger.Info("holding bare metal instance reconciliation with an unobserved state transition", "resource_id", id, "error", err)
		return 0, nil
	}
	if ps.CurrentState == fs.state {
		if events.DimensionsEqual(ps.BillingDimensions, fs.billingDimensions) {
			return 0, nil
		}
		if bmaasInstanceTypeDrift(ps.BillingDimensions, fs.billingDimensions) {
			r.holdBMaaS(id, "immutable_instance_type")
			r.logger.Info("holding bare metal instance reconciliation with immutable instance type drift", "resource_id", id)
			return 0, nil
		}
		reason = BillingDimensionsDrift
		dimensionDrift = true
		_, consumptionActive := ps.ComponentBillableSince[events.BMaaSMeterConsumption]
		consumptionEffect = bmaasActiveCorrectionEffect(consumptionActive)
	}

	intervals := bmaasIntervals(ps)
	if events.IsAllocationBillableState(fs.state) && intervals.AllocationSince == nil {
		r.holdBMaaS(id, "incomplete_history")
		r.logger.Info("holding bare metal instance reconciliation without an allocation boundary", "resource_id", id)
		return 0, nil
	}
	if dimensionDrift {
		// Catalog metadata may change while the immutable instance type and
		// lifecycle state remain stable. Update the projection without inventing
		// a lifecycle event or reopening an already active meter interval.
		allocationEffect = events.BMaaSEffectSkip
		consumptionEffect = events.BMaaSEffectSkip
	}
	if bmaasHasClosure(allocationEffect, consumptionEffect) {
		directSnapshotClosure := ps.CurrentState == "RUNNING" && fs.version == ps.FulfillmentVersion+1 &&
			(fs.state == "STARTING" || fs.state == "STOPPING" || fs.state == "FAILED" || fs.state == "DELETING")
		if directSnapshotClosure {
			if !bmaasClosureIntervalsComplete(intervals, allocationEffect, consumptionEffect) {
				r.holdBMaaS(id, "incomplete_closure_intervals")
				return 0, nil
			}
		} else {
			resolvedIntervals, boundaryTime, ok, resolveErr := r.resolveBMaaSClosure(ctx, id, fs.version, fs.state, intervals, allocationEffect, consumptionEffect)
			if resolveErr != nil {
				return 0, resolveErr
			}
			if !ok {
				return 0, nil
			}
			intervals = resolvedIntervals
			transitionTime = boundaryTime
		}
	}
	published, err := r.publishBMaaSCorrections(ctx, id, fs.tenantID, fs.projectID, reason, ps.CurrentState, fs.state,
		fs.billingDimensions, intervals, allocationEffect, consumptionEffect,
		ps.ComponentEverStarted[events.BMaaSMeterAllocation], ps.ComponentEverStarted[events.BMaaSMeterConsumption], transitionTime)
	if err != nil {
		return 0, err
	}
	state := reconciledBMaaSState(id, ps, fs, intervals, allocationEffect, consumptionEffect, transitionTime)
	if err := r.store.Upsert(ctx, state); err != nil {
		if errors.Is(err, projection.ErrStaleVersion) {
			r.logger.Info("stale version during reconciliation, skipping", "resource_id", id)
			return 0, nil
		}
		return 0, fmt.Errorf("upserting %s for %s: %w", reason, id, err)
	}
	return boolToInt(published), nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func bmaasIntervals(state projection.ResourceState) events.BMaaSMeterIntervals {
	intervals := events.BMaaSMeterIntervals{AllocationSince: state.BillableSince}
	if since, ok := state.ComponentBillableSince[events.BMaaSMeterConsumption]; ok {
		intervals.ConsumptionSince = &since
	}
	return intervals
}

func bmaasHasClosure(allocationEffect, consumptionEffect string) bool {
	return allocationEffect == events.BMaaSEffectSuspend || consumptionEffect == events.BMaaSEffectSuspend
}

func bmaasClosureIntervalsComplete(intervals events.BMaaSMeterIntervals, allocationEffect, consumptionEffect string) bool {
	if allocationEffect == events.BMaaSEffectSuspend && intervals.AllocationSince == nil {
		return false
	}
	return consumptionEffect != events.BMaaSEffectSuspend || intervals.ConsumptionSince != nil
}

func (r *Reconciler) resolveBMaaSClosure(ctx context.Context, id string, sourceVersion int32, boundaryState string, intervals events.BMaaSMeterIntervals, allocationEffect, consumptionEffect string) (events.BMaaSMeterIntervals, time.Time, bool, error) {
	records, err := r.replay(ctx, id, sourceVersion-1, sourceVersion)
	if err != nil {
		if !errors.Is(err, ErrBMaaSReplayUnavailable) {
			return events.BMaaSMeterIntervals{}, time.Time{}, false, err
		}
		r.holdBMaaS(id, "missing_replay")
		r.logger.Info("holding bare metal instance closure while history is unavailable", "resource_id", id, "error", err)
		return events.BMaaSMeterIntervals{}, time.Time{}, false, nil
	}
	boundaryTime, ok := replayStateBoundary(records, boundaryState)
	if !ok {
		r.holdBMaaS(id, "missing_replay_boundary")
		r.logger.Info("holding bare metal instance closure without authoritative boundary", "resource_id", id)
		return events.BMaaSMeterIntervals{}, time.Time{}, false, nil
	}
	if !bmaasClosureIntervalsComplete(intervals, allocationEffect, consumptionEffect) {
		r.holdBMaaS(id, "incomplete_closure_intervals")
		r.logger.Info("holding bare metal instance closure with incomplete meter intervals", "resource_id", id)
		return events.BMaaSMeterIntervals{}, time.Time{}, false, nil
	}
	return intervals, boundaryTime, true, nil
}

func (r *Reconciler) resolveBMaaSDeletionBoundary(ctx context.Context, id string, sourceVersion int32) (time.Time, bool, error) {
	records, err := r.replay(ctx, id, sourceVersion, sourceVersion)
	if err != nil {
		if !errors.Is(err, ErrBMaaSReplayUnavailable) {
			return time.Time{}, false, err
		}
		r.holdBMaaS(id, "missing_replay")
		r.logger.Info("holding bare metal instance deletion while history is unavailable", "resource_id", id, "error", err)
		return time.Time{}, false, nil
	}
	for _, record := range records {
		if record.EventType == privatev1.EventType_EVENT_TYPE_OBJECT_DELETED && !record.TransitionTime.IsZero() {
			return record.TransitionTime.UTC(), true, nil
		}
	}
	r.holdBMaaS(id, "missing_replay_boundary")
	r.logger.Info("holding bare metal instance deletion without an authoritative deletion event", "resource_id", id)
	return time.Time{}, false, nil
}

func bmaasActiveCorrectionEffect(active bool) string {
	if active {
		return events.BMaaSEffectStart
	}
	return events.BMaaSEffectSkip
}

func bmaasInstanceTypeDrift(existing, current map[string]any) bool {
	currentType, ok := current["bm_instance_type"].(string)
	if !ok || currentType == "" {
		return false
	}
	existingType, ok := existing["bm_instance_type"].(string)
	return !ok || existingType != currentType
}

func (r *Reconciler) holdBMaaS(id string, reasons ...string) {
	reason := "unspecified"
	if len(reasons) > 0 && reasons[0] != "" {
		reason = reasons[0]
	}
	if r.bmaasHolds == nil {
		r.bmaasHolds = make(map[string]struct{})
	}
	r.bmaasHolds[id] = struct{}{}
	r.logger.Info("holding bare metal instance reconciliation", "resource_id", id, "reason", reason)
	if r.bmaasHoldMetrics == nil {
		r.bmaasHoldMetrics = make(map[string]struct{})
	}
	key := id + "\x00" + reason
	if _, exists := r.bmaasHoldMetrics[key]; !exists {
		r.bmaasHoldMetrics[key] = struct{}{}
		bmaasReconciliationHolds.WithLabelValues(reason).Inc()
	}
}

func (r *Reconciler) replay(ctx context.Context, resourceID string, fromVersion, toVersion int32) ([]BMaaSReplayRecord, error) {
	if r.replaySource == nil {
		return nil, ErrBMaaSReplayUnavailable
	}
	return r.replaySource.Replay(ctx, resourceID, fromVersion, toVersion)
}

func (r *Reconciler) replayBMaaSGap(ctx context.Context, id string, existing projection.ResourceState, target fulfillmentResource) (int, error) {
	records, err := r.replay(ctx, id, existing.FulfillmentVersion, target.version)
	if err != nil {
		if !errors.Is(err, ErrBMaaSReplayUnavailable) {
			return 0, err
		}
		r.holdBMaaS(id, "missing_replay")
		r.logger.Info("holding bare metal instance reconciliation while replay is unavailable", "resource_id", id, "error", err)
		return 0, nil
	}
	if len(records) == 0 || records[len(records)-1].Version != target.version {
		r.holdBMaaS(id, "non_contiguous_replay")
		r.logger.Info("holding bare metal instance reconciliation with incomplete replay", "resource_id", id)
		return 0, nil
	}
	expectedVersion := existing.FulfillmentVersion + 1
	sawStopping := existing.CurrentState != "RUNNING" || target.state != "STOPPED"
	for _, record := range records {
		if record.State == "" || record.TransitionTime.IsZero() || record.Version != expectedVersion || record.EventType == privatev1.EventType_EVENT_TYPE_OBJECT_DELETED {
			r.holdBMaaS(id, "non_contiguous_replay")
			r.logger.Info("holding bare metal instance reconciliation with non-contiguous replay", "resource_id", id)
			return 0, nil
		}
		if existing.CurrentState == "RUNNING" && target.state == "STOPPED" {
			if record.State == "STOPPING" {
				sawStopping = true
			}
			if record.State == "STOPPED" && !sawStopping {
				r.holdBMaaS(id, "incomplete_history")
				r.logger.Info("holding bare metal instance reconciliation without STOPPING boundary", "resource_id", id)
				return 0, nil
			}
		}
		expectedVersion++
	}
	if records[len(records)-1].State != target.state {
		r.holdBMaaS(id, "replay_endpoint_drift")
		r.logger.Info("holding bare metal instance reconciliation with replay endpoint drift", "resource_id", id)
		return 0, nil
	}

	corrections := 0
	state := existing
	for _, record := range records {
		fs := fulfillmentResource{
			resourceType:      events.ResourceTypeBareMetalInstance,
			state:             record.State,
			version:           record.Version,
			tenantID:          record.TenantID,
			projectID:         record.ProjectID,
			billingDimensions: record.BillingDims,
			transitionTime:    &record.TransitionTime,
		}
		if fs.tenantID == "" {
			fs.tenantID = target.tenantID
		}
		if fs.projectID == "" {
			fs.projectID = target.projectID
		}
		if fs.billingDimensions == nil {
			fs.billingDimensions = target.billingDimensions
		}
		n, next, applyErr := r.applyBMaaSTransition(ctx, id, state, fs, StateDrift, false)
		if applyErr != nil {
			return corrections, applyErr
		}
		if n == 0 && next.FulfillmentVersion == state.FulfillmentVersion && next.CurrentState == state.CurrentState {
			return corrections, nil
		}
		corrections += n
		state = next
	}
	return corrections, nil
}

func (r *Reconciler) applyBMaaSTransition(ctx context.Context, id string, ps projection.ResourceState, fs fulfillmentResource, reason CorrectionReason, resolveClosure bool) (int, projection.ResourceState, error) {
	allocationEffect, err := events.ResolveAllocationTransition(ps.CurrentState, fs.state)
	if err != nil {
		r.holdBMaaS(id, "unobserved_transition")
		return 0, ps, nil
	}
	consumptionEffect, err := events.ResolveConsumptionTransition(ps.CurrentState, fs.state)
	if err != nil {
		r.holdBMaaS(id, "unobserved_transition")
		return 0, ps, nil
	}
	intervals := bmaasIntervals(ps)
	transitionTime := fs.transitionTime.UTC()
	if allocationEffect == events.BMaaSEffectStart || allocationEffect == events.BMaaSEffectResume {
		if intervals.AllocationSince == nil && events.IsAllocationBillableState(fs.state) {
			intervals.AllocationSince = &transitionTime
		}
	}
	if consumptionEffect == events.BMaaSEffectStart || consumptionEffect == events.BMaaSEffectResume {
		if intervals.ConsumptionSince == nil && events.IsConsumptionBillableState(fs.state) {
			intervals.ConsumptionSince = &transitionTime
		}
	}
	if bmaasHasClosure(allocationEffect, consumptionEffect) && !bmaasClosureIntervalsComplete(intervals, allocationEffect, consumptionEffect) {
		r.holdBMaaS(id, "incomplete_closure_intervals")
		return 0, ps, nil
	}
	if resolveClosure && bmaasHasClosure(allocationEffect, consumptionEffect) {
		resolvedIntervals, boundaryTime, ok, resolveErr := r.resolveBMaaSClosure(ctx, id, fs.version, fs.state, intervals, allocationEffect, consumptionEffect)
		if resolveErr != nil {
			return 0, ps, resolveErr
		}
		if !ok {
			return 0, ps, nil
		}
		intervals = resolvedIntervals
		transitionTime = boundaryTime
	}
	published, err := r.publishBMaaSCorrections(ctx, id, fs.tenantID, fs.projectID, reason, ps.CurrentState, fs.state,
		fs.billingDimensions, intervals, allocationEffect, consumptionEffect,
		ps.ComponentEverStarted[events.BMaaSMeterAllocation], ps.ComponentEverStarted[events.BMaaSMeterConsumption], transitionTime)
	if err != nil {
		return 0, ps, err
	}
	state := reconciledBMaaSState(id, ps, fs, intervals, allocationEffect, consumptionEffect, transitionTime)
	if err := r.store.Upsert(ctx, state); err != nil {
		if errors.Is(err, projection.ErrStaleVersion) {
			return 0, ps, nil
		}
		return 0, ps, fmt.Errorf("upserting replay transition for %s: %w", id, err)
	}
	return boolToInt(published), state, nil
}

func replayStateBoundary(records []BMaaSReplayRecord, state string) (time.Time, bool) {
	for _, record := range records {
		if record.EventType == privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED && record.State == state && !record.TransitionTime.IsZero() {
			return record.TransitionTime.UTC(), true
		}
	}
	return time.Time{}, false
}

func reconciledBMaaSState(resourceID string, existing projection.ResourceState, fs fulfillmentResource, intervals events.BMaaSMeterIntervals, allocationEffect, consumptionEffect string, transitionTime time.Time) projection.ResourceState {
	state := projection.ResourceState{
		ResourceID:             resourceID,
		ResourceType:           events.ResourceTypeBareMetalInstance,
		TenantID:               fs.tenantID,
		ProjectID:              fs.projectID,
		CurrentState:           fs.state,
		PreviousState:          existing.CurrentState,
		EverBillable:           existing.EverBillable,
		LastHeartbeatAt:        existing.LastHeartbeatAt,
		TransitionTime:         transitionTime,
		FulfillmentVersion:     fs.version,
		BillingDimensions:      fs.billingDimensions,
		ComponentBillableSince: map[string]time.Time{},
		ComponentEverStarted:   map[string]bool{},
	}
	for meter, since := range existing.ComponentBillableSince {
		state.ComponentBillableSince[meter] = since
	}
	for meter, started := range existing.ComponentEverStarted {
		state.ComponentEverStarted[meter] = started
	}

	if events.IsAllocationBillableState(fs.state) {
		state.BillableSince = intervals.AllocationSince
	} else {
		state.BillableSince = nil
	}
	if allocationEffect == events.BMaaSEffectStart || allocationEffect == events.BMaaSEffectResume {
		state.ComponentEverStarted[events.BMaaSMeterAllocation] = true
	}

	if events.IsConsumptionBillableState(fs.state) {
		if intervals.ConsumptionSince != nil {
			state.ComponentBillableSince[events.BMaaSMeterConsumption] = *intervals.ConsumptionSince
		} else {
			state.ComponentBillableSince[events.BMaaSMeterConsumption] = transitionTime
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

func (r *Reconciler) reconcileMissedDeletions(ctx context.Context, fulfillmentState map[string]fulfillmentResource, projMap map[string]projection.ResourceState, now time.Time) (int, error) {
	corrections := 0
	computeSkipLogged := false
	clusterSkipLogged := false
	bmaasSkipLogged := false

	for id, ps := range projMap {
		if _, exists := fulfillmentState[id]; !exists {
			if ps.ResourceType == events.ResourceTypeComputeInstance && r.computeClient == nil {
				if !computeSkipLogged {
					r.logger.Info("skipping compute_instance missed deletion checks, no compute client configured")
					computeSkipLogged = true
				}
				continue
			}
			if ps.ResourceType == events.ResourceTypeClusterOrder && r.clusterClient == nil {
				if !clusterSkipLogged {
					r.logger.Info("skipping cluster_order missed deletion checks, no cluster client configured")
					clusterSkipLogged = true
				}
				continue
			}
			if ps.ResourceType == events.ResourceTypeBareMetalInstance {
				if _, skipped := r.bmaasSkipped[id]; skipped {
					r.logger.Info("holding bare metal instance missed deletion after source row was skipped", "resource_id", id)
					continue
				}
				if r.bareMetalClient == nil {
					if !bmaasSkipLogged {
						r.logger.Info("skipping bare_metal_instance missed deletion checks, no BMI client configured")
						bmaasSkipLogged = true
					}
					continue
				}
				allocationEffect := events.BMaaSEffectSkip
				if ps.BillableSince != nil {
					allocationEffect = events.BMaaSEffectSuspend
				}
				consumptionEffect := events.BMaaSEffectSkip
				if _, active := ps.ComponentBillableSince[events.BMaaSMeterConsumption]; active {
					consumptionEffect = events.BMaaSEffectSuspend
				}
				boundaryTime, ok, resolveErr := r.resolveBMaaSDeletionBoundary(ctx, id, ps.FulfillmentVersion)
				if resolveErr != nil {
					return corrections, resolveErr
				}
				if !ok {
					continue
				}
				intervals := bmaasIntervals(ps)
				published, err := r.publishBMaaSCorrections(ctx, id, ps.TenantID, ps.ProjectID, MissedDeletion,
					ps.CurrentState, "", ps.BillingDimensions, intervals,
					allocationEffect, consumptionEffect,
					ps.ComponentEverStarted[events.BMaaSMeterAllocation], ps.ComponentEverStarted[events.BMaaSMeterConsumption], boundaryTime)
				if err != nil {
					return corrections, err
				}
				corrections += boolToInt(published)
				if err := r.store.Delete(ctx, id); err != nil {
					return corrections, fmt.Errorf("deleting missed deletion for %s: %w", id, err)
				}
				continue
			}
			if err := r.publishCorrections(ctx, id, ps.ResourceType, ps.TenantID, ps.ProjectID,
				MissedDeletion, ps.CurrentState, "", ps.BillingDimensions, now); err != nil {
				return corrections, err
			}
			corrections++

			if err := r.store.Delete(ctx, id); err != nil {
				return corrections, fmt.Errorf("deleting missed deletion for %s: %w", id, err)
			}
		}
	}

	return corrections, nil
}

func (r *Reconciler) reconcileStaleHeartbeats(ctx context.Context, fulfillmentState map[string]fulfillmentResource, now time.Time) (int, error) {
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
		if ps.ResourceType == events.ResourceTypeBareMetalInstance {
			if r.bareMetalClient == nil {
				r.logger.V(1).Info("skipping stale bare metal instance heartbeat, no bare metal client configured", "resource_id", ps.ResourceID)
				continue
			}
			if _, present := fulfillmentState[ps.ResourceID]; !present {
				r.logger.Info("holding stale bare metal instance heartbeat until meter-specific reconciliation is available", "resource_id", ps.ResourceID)
				continue
			}
			if _, held := r.bmaasHolds[ps.ResourceID]; held {
				r.logger.Info("holding stale bare metal instance heartbeat while BMaaS reconciliation is held", "resource_id", ps.ResourceID)
				continue
			}
		}
		if ps.LastHeartbeatAt == nil || now.Sub(*ps.LastHeartbeatAt) > 2*r.heartbeatInterval {
			hbEvents, hbErr := buildSyntheticHeartbeats(*ps, now)
			if hbErr != nil {
				r.logger.Error(hbErr, "building synthetic heartbeat", "resource_id", ps.ResourceID)
				continue
			}
			if ps.ResourceType == events.ResourceTypeBareMetalInstance && len(hbEvents) == 0 {
				r.logger.V(1).Info("skipping stale bare metal instance heartbeat without a meter boundary", "resource_id", ps.ResourceID)
				continue
			}
			published := true
			for _, hb := range hbEvents {
				if err := r.publisher.Publish(ctx, hb); err != nil {
					r.logger.Error(err, "publishing synthetic heartbeat", "resource_id", ps.ResourceID)
					published = false
					break
				}
			}
			if !published {
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
	resourceType      string
	state             string
	version           int32
	tenantID          string
	projectID         string
	billingDimensions map[string]any
	transitionTime    *time.Time
}

var billabilityCheckers = map[string]func(string) bool{
	events.ResourceTypeComputeInstance:   events.IsBillableState,
	events.ResourceTypeClusterOrder:      events.IsClusterBillableState,
	events.ResourceTypeBareMetalInstance: events.IsAllocationBillableState,
}

var transientCheckers = map[string]func(string) bool{
	events.ResourceTypeComputeInstance: events.IsTransientState,
	events.ResourceTypeClusterOrder:    events.IsClusterTransientState,
	events.ResourceTypeBareMetalInstance: func(string) bool {
		return false
	},
}

// isTransientForType reports whether the given state is transient for the
// resource type. Returns bool (not (bool, error) like isBillableForType)
// because unknown resource types have no transient states — returning false
// is correct, not an error.
func isTransientForType(resourceType, state string) bool {
	checker, ok := transientCheckers[resourceType]
	if !ok {
		return false
	}
	return checker(state)
}

func isBillableForType(resourceType, state string) (bool, error) {
	checker, ok := billabilityCheckers[resourceType]
	if !ok {
		return false, fmt.Errorf("unknown resource type: %s", resourceType)
	}
	return checker(state), nil
}

func (r *Reconciler) loadFulfillmentState(ctx context.Context) (map[string]fulfillmentResource, error) {
	result := make(map[string]fulfillmentResource)

	if r.computeClient != nil {
		if err := r.loadComputeInstances(ctx, result); err != nil {
			return nil, err
		}
	}
	if r.clusterClient != nil {
		if err := r.loadClusters(ctx, result); err != nil {
			return nil, err
		}
	}
	if r.bareMetalClient != nil {
		if err := r.loadBareMetalInstances(ctx, result); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (r *Reconciler) loadComputeInstances(ctx context.Context, result map[string]fulfillmentResource) error {
	var offset int32
	for {
		limit := int32(defaultPageSize)
		resp, err := r.computeClient.List(ctx, &privatev1.ComputeInstancesListRequest{
			Offset: &offset,
			Limit:  &limit,
		})
		if err != nil {
			return fmt.Errorf("listing compute instances (offset=%d): %w", offset, err)
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
				resourceType:      events.ResourceTypeComputeInstance,
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
	return nil
}

func (r *Reconciler) loadClusters(ctx context.Context, result map[string]fulfillmentResource) error {
	var offset int32
	for {
		limit := int32(defaultPageSize)
		resp, err := r.clusterClient.List(ctx, &privatev1.ClustersListRequest{
			Offset: &offset,
			Limit:  &limit,
		})
		if err != nil {
			return fmt.Errorf("listing clusters (offset=%d): %w", offset, err)
		}

		items := resp.GetItems()
		for _, cl := range items {
			state := "UNSPECIFIED"
			if s := cl.GetStatus(); s != nil {
				state = strings.TrimPrefix(s.GetState().String(), events.ClusterStatePrefix)
			}
			tenantID := ""
			projectID := ""
			var version int32
			if md := cl.GetMetadata(); md != nil {
				tenantID = md.GetTenant()
				projectID = md.GetProject()
				version = md.GetVersion()
			}
			result[cl.GetId()] = fulfillmentResource{
				resourceType:      events.ResourceTypeClusterOrder,
				state:             state,
				version:           version,
				tenantID:          tenantID,
				projectID:         projectID,
				billingDimensions: events.ClusterBillingDimensions(cl),
			}
		}

		if len(items) < defaultPageSize {
			break
		}
		offset += int32(len(items))
	}
	return nil
}

func (r *Reconciler) loadBareMetalInstances(ctx context.Context, result map[string]fulfillmentResource) error {
	var offset int32
	var listedIDs []string
	for {
		limit := int32(defaultPageSize)
		resp, err := r.bareMetalClient.List(ctx, &privatev1.BareMetalInstancesListRequest{
			Offset: &offset,
			Limit:  &limit,
		})
		if err != nil {
			return fmt.Errorf("listing bare metal instances (offset=%d): %w", offset, err)
		}

		items := resp.GetItems()
		for _, bmi := range items {
			state := "UNSPECIFIED"
			var transitionTime *time.Time
			if status := bmi.GetStatus(); status != nil {
				state = strings.TrimPrefix(status.GetState().String(), events.BareMetalInstanceStatePrefix)
				if timestamp := status.GetStateTransitionTime(); timestamp != nil {
					value := timestamp.AsTime().UTC()
					transitionTime = &value
				}
			}
			tenantID := ""
			projectID := ""
			var version int32
			if md := bmi.GetMetadata(); md != nil {
				tenantID = md.GetTenant()
				projectID = md.GetProject()
				version = md.GetVersion()
			}
			dimensions, err := events.BareMetalInstanceBillingDimensions(bmi)
			if err != nil {
				if r.bmaasSkipped == nil {
					r.bmaasSkipped = make(map[string]struct{})
				}
				r.bmaasSkipped[bmi.GetId()] = struct{}{}
				r.holdBMaaS(bmi.GetId(), "missing_instance_type")
				r.logger.Error(err, "skipping bare metal instance with invalid billing dimensions", "resource_id", bmi.GetId())
				continue
			}
			result[bmi.GetId()] = fulfillmentResource{
				resourceType:      events.ResourceTypeBareMetalInstance,
				state:             state,
				version:           version,
				tenantID:          tenantID,
				projectID:         projectID,
				billingDimensions: dimensions,
				transitionTime:    transitionTime,
			}
			listedIDs = append(listedIDs, bmi.GetId())
		}

		if len(items) < defaultPageSize {
			break
		}
		offset += int32(len(items))
	}
	if r.bmaasPresence != nil {
		r.bmaasPresence.Replace(listedIDs)
	}
	return nil
}

func buildSyntheticHeartbeats(ps projection.ResourceState, now time.Time) ([]cloudevents.Event, error) {
	baseID := fmt.Sprintf("synthetic-hb/%s/%d", ps.ResourceID, staleReferencePoint(ps, now).Unix())
	return heartbeat.BuildHeartbeatEvents(&ps, baseID, now, "osac-metering/reconciler")
}

// staleReferencePoint returns the timestamp identifying the billing gap a
// synthetic heartbeat is catching up on, rather than the moment reconciliation
// happened to run. Keying the CloudEvent base ID off this value means retries
// across reconciliation cycles for the SAME unresolved gap reproduce the SAME
// ID (so adapter-side ID dedup recognizes them as the same logical catch-up),
// while a genuinely new gap (LastHeartbeatAt has since advanced) gets a new one.
func staleReferencePoint(ps projection.ResourceState, now time.Time) time.Time {
	if ps.LastHeartbeatAt != nil {
		return *ps.LastHeartbeatAt
	}
	if ps.BillableSince != nil {
		return *ps.BillableSince
	}
	if ps.ResourceType == events.ResourceTypeBareMetalInstance {
		if consumptionSince, ok := ps.ComponentBillableSince[events.BMaaSMeterConsumption]; ok {
			return consumptionSince
		}
	}
	return now
}
