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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

type ExternalIPsClient interface {
	List(ctx context.Context, in *privatev1.ExternalIPsListRequest, opts ...grpc.CallOption) (*privatev1.ExternalIPsListResponse, error)
	Get(ctx context.Context, in *privatev1.ExternalIPsGetRequest, opts ...grpc.CallOption) (*privatev1.ExternalIPsGetResponse, error)
}

type NATGatewaysClient interface {
	List(ctx context.Context, in *privatev1.NATGatewaysListRequest, opts ...grpc.CallOption) (*privatev1.NATGatewaysListResponse, error)
	Get(ctx context.Context, in *privatev1.NATGatewaysGetRequest, opts ...grpc.CallOption) (*privatev1.NATGatewaysGetResponse, error)
}

type ExternalIPPoolsClient interface {
	List(ctx context.Context, in *privatev1.ExternalIPPoolsListRequest, opts ...grpc.CallOption) (*privatev1.ExternalIPPoolsListResponse, error)
}

type VolumesClient interface {
	List(ctx context.Context, in *privatev1.VolumesListRequest, opts ...grpc.CallOption) (*privatev1.VolumesListResponse, error)
	Get(ctx context.Context, in *privatev1.VolumesGetRequest, opts ...grpc.CallOption) (*privatev1.VolumesGetResponse, error)
}

type BareMetalInstancesClient interface {
	List(ctx context.Context, in *privatev1.BareMetalInstancesListRequest, opts ...grpc.CallOption) (*privatev1.BareMetalInstancesListResponse, error)
}

type Reconciler struct {
	computeClient        ComputeInstancesClient
	clusterClient        ClustersClient
	externalIPClient     ExternalIPsClient
	natGatewayClient     NATGatewaysClient
	externalIPPoolClient ExternalIPPoolsClient
	volumeClient         VolumesClient
	deploymentID         string
	bareMetalClient      BareMetalInstancesClient
	store                projection.Store
	publisher            kafkapub.EventPublisher
	logger               logr.Logger
	heartbeatInterval    time.Duration
	unavailableTypes     map[string]struct{}
	bmaasHolds           map[string]struct{}
	bmaasHoldMetrics     map[string]struct{}
	bmaasSkipped         map[string]struct{}
	bmaasPresence        *heartbeat.BMaaSPresence
}

var correctionResourceTypes = map[string]struct{}{
	events.ResourceTypeComputeInstance:   {},
	events.ResourceTypeClusterOrder:      {},
	events.ResourceTypeExternalIP:        {},
	events.ResourceTypeNATGateway:        {},
	events.ResourceTypeVolume:            {},
	events.ResourceTypeBareMetalInstance: {},
}

func isUnavailable(err error) bool {
	return status.Code(err) == codes.Unavailable
}

func NewReconciler(
	computeClient ComputeInstancesClient,
	clusterClient ClustersClient,
	externalIPClient ExternalIPsClient,
	natGatewayClient NATGatewaysClient,
	externalIPPoolClient ExternalIPPoolsClient,
	volumeClient VolumesClient,
	bareMetalClient BareMetalInstancesClient,
	store projection.Store,
	publisher kafkapub.EventPublisher,
	logger logr.Logger,
	heartbeatInterval time.Duration,
	deploymentID string,
	bmaasPresence *heartbeat.BMaaSPresence,
) *Reconciler {
	return &Reconciler{
		computeClient:        computeClient,
		clusterClient:        clusterClient,
		externalIPClient:     externalIPClient,
		natGatewayClient:     natGatewayClient,
		externalIPPoolClient: externalIPPoolClient,
		volumeClient:         volumeClient,
		bareMetalClient:      bareMetalClient,
		store:                store,
		publisher:            publisher,
		logger:               logger,
		heartbeatInterval:    heartbeatInterval,
		deploymentID:         deploymentID,
		unavailableTypes:     make(map[string]struct{}),
		bmaasHolds:           make(map[string]struct{}),
		bmaasHoldMetrics:     make(map[string]struct{}),
		bmaasPresence:        bmaasPresence,
	}
}

func (r *Reconciler) Reconcile(ctx context.Context) error {
	start := time.Now()
	now := start.UTC()
	r.unavailableTypes = make(map[string]struct{})
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
	if _, ok := correctionResourceTypes[resourceType]; !ok {
		r.logger.Info("skipping unsupported correction resource type", "resource_id", id, "resource_type", resourceType)
		return nil
	}
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
		if (events.IsNetworkingResourceType(fs.resourceType) || events.IsVolumeResourceType(fs.resourceType)) && fs.transitionTime.IsZero() {
			sourceBillable, billErr := fulfillmentResourceBillable(fs)
			if billErr != nil {
				return corrections, fmt.Errorf("checking billability for %s: %w", id, billErr)
			}
			if sourceBillable || (exists && ps.IsBillable) {
				return corrections, fmt.Errorf("resource %s (%s) has no authoritative transition time", id, fs.resourceType)
			}
			r.logger.Info("skipping non-billable resource without authoritative transition time",
				"resource_id", id,
				"resource_type", fs.resourceType,
				"state", fs.state)
			continue
		}
		transitionTime, err := reconciliationTransitionTime(fs, now)
		if err != nil {
			return corrections, err
		}
		if !exists {
			if isTransientForType(fs.resourceType, fs.state) {
				r.logger.V(1).Info("fulfillment reports transient state for unknown resource, skipping",
					"resource_id", id,
					"fulfillment_state", fs.state)
				continue
			}
			entryTime, entryErr := reconciliationStateEntryTime(fs, transitionTime)
			if entryErr != nil {
				return corrections, entryErr
			}
			if err := r.publishCorrections(ctx, id, fs.resourceType, fs.tenantID, fs.projectID,
				MissedCreation, "", fs.state, fs.billingDimensions, now); err != nil {
				return corrections, err
			}
			corrections++

			isBillable, billErr := fulfillmentResourceBillable(fs)
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
				TransitionTime:     entryTime,
				FulfillmentVersion: fs.version,
				BillingDimensions:  fs.billingDimensions,
			}
			if isBillable {
				newState.BillableSince = &entryTime
				newState.ComponentBillableSince = events.NextComponentBillableSince(nil, nil, fs.billingDimensions, entryTime)
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
			if events.IsVolumeResourceType(fs.resourceType) && ps.IsBillable != fs.isBillable {
				return corrections, fmt.Errorf("volume %s changed billability without an authoritative transition", id)
			}
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

			isBillable, billErr := fulfillmentResourceBillable(fs)
			if billErr != nil {
				return corrections, fmt.Errorf("checking billability for %s: %w", id, billErr)
			}
			entryTime, entryErr := reconciliationStateEntryTime(fs, transitionTime)
			if entryErr != nil {
				return corrections, entryErr
			}
			ps.PreviousState = ps.CurrentState
			ps.CurrentState = fs.state
			wasBillable := ps.IsBillable
			ps.IsBillable = isBillable
			ps.EverBillable = ps.EverBillable || isBillable
			ps.FulfillmentVersion = fs.version
			ps.TransitionTime = entryTime
			if isBillable && !wasBillable {
				ps.BillableSince = &entryTime
				ps.ComponentBillableSince = events.NextComponentBillableSince(nil, nil, fs.billingDimensions, entryTime)
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
			if events.IsVolumeResourceType(fs.resourceType) {
				return corrections, fmt.Errorf("volume %s has an unobserved billing-dimension change; refusing to emit a correction without an authoritative capacity boundary", id)
			}
			if err := r.publishCorrections(ctx, id, fs.resourceType, fs.tenantID, fs.projectID,
				BillingDimensionsDrift, ps.CurrentState, fs.state, fs.billingDimensions, now); err != nil {
				return corrections, err
			}
			corrections++

			ps.BillingDimensions = fs.billingDimensions
			ps.FulfillmentVersion = fs.version
			ps.TransitionTime = transitionTime
			if fs.resourceType == events.ResourceTypeExternalIP && ps.IsBillable {
				ps.BillableSince = &transitionTime
			}
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
	if fs.transitionTime.IsZero() {
		r.holdBMaaS(id, "missing_transition_time")
		r.logger.Info("holding bare metal instance reconciliation without source transition timestamp", "resource_id", id)
		return 0, nil
	}
	transitionTime := fs.transitionTime.UTC()
	if !exists {
		r.holdBMaaS(id, "history_unavailable")
		r.logger.Info("holding bare metal instance reconciliation until reliable history is available", "resource_id", id)
		return 0, nil
	}

	if fs.version < ps.FulfillmentVersion {
		r.logger.V(1).Info("projection ahead of fulfillment, skipping", "resource_id", id,
			"fulfillment_version", fs.version, "projection_version", ps.FulfillmentVersion)
		return 0, nil
	}
	if fs.version > ps.FulfillmentVersion+1 ||
		(fs.version > ps.FulfillmentVersion && ps.CurrentState == fs.state && events.DimensionsEqual(ps.BillingDimensions, fs.billingDimensions)) ||
		(ps.CurrentState == "RUNNING" && fs.state == "STOPPED") {
		r.holdBMaaS(id, "history_unavailable")
		r.logger.Info("holding bare metal instance reconciliation until reliable history is available", "resource_id", id)
		return 0, nil
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
		consumptionActive := ps.BMaaSMeterState.Consumption.ActiveSince != nil
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
			r.holdBMaaS(id, "history_unavailable")
			r.logger.Info("holding bare metal instance closure until reliable history is available", "resource_id", id)
			return 0, nil
		}
	}
	published, err := r.publishBMaaSCorrections(ctx, id, fs.tenantID, fs.projectID, reason, ps.CurrentState, fs.state,
		fs.billingDimensions, intervals, allocationEffect, consumptionEffect,
		ps.BMaaSMeterState.Allocation.FirstStartedAt != nil,
		ps.BMaaSMeterState.Consumption.FirstStartedAt != nil, transitionTime)
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
	return events.BMaaSMeterIntervals{
		AllocationSince:  state.BMaaSMeterState.Allocation.ActiveSince,
		ConsumptionSince: state.BMaaSMeterState.Consumption.ActiveSince,
	}
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

func reconciledBMaaSState(resourceID string, existing projection.ResourceState, fs fulfillmentResource, intervals events.BMaaSMeterIntervals, allocationEffect, consumptionEffect string, transitionTime time.Time) projection.ResourceState {
	state := projection.ResourceState{
		ResourceID:         resourceID,
		ResourceType:       events.ResourceTypeBareMetalInstance,
		TenantID:           fs.tenantID,
		ProjectID:          fs.projectID,
		CurrentState:       fs.state,
		PreviousState:      existing.CurrentState,
		EverBillable:       existing.EverBillable,
		LastHeartbeatAt:    existing.LastHeartbeatAt,
		TransitionTime:     transitionTime,
		FulfillmentVersion: fs.version,
		BillingDimensions:  fs.billingDimensions,
		BMaaSMeterState:    existing.BMaaSMeterState,
	}
	if events.IsAllocationBillableState(fs.state) {
		if state.BMaaSMeterState.Allocation.ActiveSince == nil {
			state.BMaaSMeterState.Allocation.ActiveSince = intervals.AllocationSince
		}
		if state.BMaaSMeterState.Allocation.ActiveSince == nil {
			since := transitionTime.UTC()
			state.BMaaSMeterState.Allocation.ActiveSince = &since
		}
		state.BillableSince = state.BMaaSMeterState.Allocation.ActiveSince
	} else {
		state.BMaaSMeterState.Allocation.ActiveSince = nil
		state.BillableSince = nil
	}
	switch allocationEffect {
	case events.BMaaSEffectStart, events.BMaaSEffectResume:
		if state.BMaaSMeterState.Allocation.ActiveSince == nil {
			since := transitionTime.UTC()
			state.BMaaSMeterState.Allocation.ActiveSince = &since
		}
		if state.BMaaSMeterState.Allocation.FirstStartedAt == nil {
			firstStartedAt := transitionTime.UTC()
			state.BMaaSMeterState.Allocation.FirstStartedAt = &firstStartedAt
		}
	case events.BMaaSEffectSuspend:
		state.BMaaSMeterState.Allocation.ActiveSince = nil
	}

	if events.IsConsumptionBillableState(fs.state) {
		if state.BMaaSMeterState.Consumption.ActiveSince == nil {
			state.BMaaSMeterState.Consumption.ActiveSince = intervals.ConsumptionSince
		}
		if state.BMaaSMeterState.Consumption.ActiveSince == nil {
			since := transitionTime.UTC()
			state.BMaaSMeterState.Consumption.ActiveSince = &since
		}
	} else {
		state.BMaaSMeterState.Consumption.ActiveSince = nil
	}
	switch consumptionEffect {
	case events.BMaaSEffectStart, events.BMaaSEffectResume:
		if state.BMaaSMeterState.Consumption.ActiveSince == nil {
			since := transitionTime.UTC()
			state.BMaaSMeterState.Consumption.ActiveSince = &since
		}
		if state.BMaaSMeterState.Consumption.FirstStartedAt == nil {
			firstStartedAt := transitionTime.UTC()
			state.BMaaSMeterState.Consumption.FirstStartedAt = &firstStartedAt
		}
	case events.BMaaSEffectSuspend:
		state.BMaaSMeterState.Consumption.ActiveSince = nil
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
			if _, unavailable := r.unavailableTypes[ps.ResourceType]; unavailable {
				continue
			}
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
				r.holdBMaaS(id, "history_unavailable")
				r.logger.Info("holding bare metal instance deletion until reliable history is available", "resource_id", id)
				continue
			}
			if ps.ResourceType == events.ResourceTypeExternalIP && r.externalIPClient == nil {
				r.logger.Info("skipping external_ip missed deletion checks, no external IP client configured")
				continue
			}
			if ps.ResourceType == events.ResourceTypeNATGateway && r.natGatewayClient == nil {
				r.logger.Info("skipping nat_gateway missed deletion checks, no NAT gateway client configured")
				continue
			}
			if ps.ResourceType == events.ResourceTypeVolume && r.volumeClient == nil {
				r.logger.Info("skipping volume missed deletion checks, no volume client configured")
				continue
			}
			if ps.ResourceType == events.ResourceTypeExternalIP {
				response, err := r.externalIPClient.Get(ctx, &privatev1.ExternalIPsGetRequest{Id: id})
				if err == nil && response.GetObject() != nil {
					continue
				}
				if isUnavailable(err) {
					continue
				}
				if status.Code(err) != codes.NotFound {
					return corrections, fmt.Errorf("confirming ExternalIP %s absence: %w", id, err)
				}
			}
			if ps.ResourceType == events.ResourceTypeNATGateway {
				response, err := r.natGatewayClient.Get(ctx, &privatev1.NATGatewaysGetRequest{Id: id})
				if err == nil && response.GetObject() != nil {
					continue
				}
				if isUnavailable(err) {
					continue
				}
				if status.Code(err) != codes.NotFound {
					return corrections, fmt.Errorf("confirming NATGateway %s absence: %w", id, err)
				}
			}
			if ps.ResourceType == events.ResourceTypeVolume {
				response, err := r.volumeClient.Get(ctx, &privatev1.VolumesGetRequest{Id: id})
				if err == nil && response.GetObject() != nil {
					continue
				}
				if isUnavailable(err) {
					continue
				}
				if status.Code(err) != codes.NotFound {
					return corrections, fmt.Errorf("confirming Volume %s absence: %w", id, err)
				}
			}
			if err := r.publishCorrections(ctx, id, ps.ResourceType, ps.TenantID, ps.ProjectID,
				MissedDeletion, ps.CurrentState, "", ps.BillingDimensions, now); err != nil {
				return corrections, err
			}
			corrections++

			deleted, err := r.store.DeleteIfVersion(ctx, id, ps.FulfillmentVersion)
			if err != nil {
				return corrections, fmt.Errorf("deleting missed deletion for %s: %w", id, err)
			}
			if !deleted {
				r.logger.Info("skipping stale missed deletion after projection changed",
					"resource_id", id, "projection_version", ps.FulfillmentVersion)
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
				sourcePresent := r.bmaasPresence != nil && r.bmaasPresence.Contains(ps.ResourceID)
				if !sourcePresent {
					r.logger.Info("holding stale bare metal instance heartbeat until source presence is confirmed", "resource_id", ps.ResourceID)
					continue
				}
			}
		}
		if ps.LastHeartbeatAt == nil || now.Sub(*ps.LastHeartbeatAt) > 2*r.heartbeatInterval {
			mute := heartbeat.BMaaSMeterMute{}
			if ps.ResourceType == events.ResourceTypeBareMetalInstance && r.bmaasPresence != nil {
				mute = r.bmaasPresence.MeterMute(ps.ResourceID)
			}
			hbEvents, hbErr := buildSyntheticHeartbeatsWithMutes(*ps, now, mute)
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
	resourceType        string
	state               string
	isBillable          bool
	version             int32
	tenantID            string
	projectID           string
	transitionTime      time.Time
	stateTransitionTime time.Time
	billingDimensions   map[string]any
}

var billabilityCheckers = map[string]func(string) bool{
	events.ResourceTypeComputeInstance:   events.IsBillableState,
	events.ResourceTypeClusterOrder:      events.IsClusterBillableState,
	events.ResourceTypeExternalIP:        events.IsExternalIPBillableState,
	events.ResourceTypeNATGateway:        events.IsNATGatewayBillableState,
	events.ResourceTypeVolume:            events.IsVolumeBillableState,
	events.ResourceTypeBareMetalInstance: events.IsAllocationBillableState,
}

var transientCheckers = map[string]func(string) bool{
	events.ResourceTypeComputeInstance:   events.IsTransientState,
	events.ResourceTypeClusterOrder:      events.IsClusterTransientState,
	events.ResourceTypeExternalIP:        events.IsExternalIPTransientState,
	events.ResourceTypeNATGateway:        events.IsNATGatewayTransientState,
	events.ResourceTypeVolume:            events.IsVolumeTransientState,
	events.ResourceTypeBareMetalInstance: func(string) bool { return false }}

// isTransientForType reports whether the given state is transient for the
// resource type. The map lookup is intentionally strict for resource types
// registered with the reconciler.
func isTransientForType(resourceType, state string) bool {
	return transientCheckers[resourceType](state)
}

func isBillableForType(resourceType, state string) (bool, error) {
	checker, ok := billabilityCheckers[resourceType]
	if !ok {
		return false, fmt.Errorf("unknown resource type: %s", resourceType)
	}
	return checker(state), nil
}

func fulfillmentResourceBillable(resource fulfillmentResource) (bool, error) {
	if events.IsVolumeResourceType(resource.resourceType) {
		return resource.isBillable, nil
	}
	return isBillableForType(resource.resourceType, resource.state)
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
	if r.externalIPClient != nil {
		if r.externalIPPoolClient == nil {
			return nil, fmt.Errorf("external IP client requires external IP pool client")
		}
		pools, err := LoadExternalIPPools(ctx, r.externalIPPoolClient)
		if err != nil {
			return nil, err
		}
		if err := r.loadExternalIPs(ctx, result, pools); err != nil {
			return nil, err
		}
	}
	if r.natGatewayClient != nil {
		if err := r.loadNATGateways(ctx, result); err != nil {
			return nil, err
		}
	}
	if r.volumeClient != nil {
		if err := r.loadVolumes(ctx, result); err != nil {
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

func LoadExternalIPPools(ctx context.Context, client ExternalIPPoolsClient) (map[string]string, error) {
	result := make(map[string]string)
	var offset int32
	for {
		limit := int32(defaultPageSize)
		resp, err := client.List(ctx, &privatev1.ExternalIPPoolsListRequest{
			Offset: &offset,
			Limit:  &limit,
		})
		if err != nil {
			if isUnavailable(err) {
				return map[string]string{}, nil
			}
			return nil, fmt.Errorf("listing external IP pools (offset=%d): %w", offset, err)
		}
		total := resp.GetTotal()
		items := resp.GetItems()
		for _, pool := range items {
			family, err := events.ExternalIPPoolFamily(pool)
			if err != nil {
				return nil, err
			}
			result[pool.GetId()] = family
		}
		offset += int32(len(items))
		if offset >= total {
			break
		}
		if len(items) == 0 {
			return nil, fmt.Errorf("listing external IP pools made no progress at offset %d", offset)
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
			if isUnavailable(err) {
				r.unavailableTypes[events.ResourceTypeComputeInstance] = struct{}{}
				return nil
			}
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
			if isUnavailable(err) {
				r.unavailableTypes[events.ResourceTypeClusterOrder] = struct{}{}
				return nil
			}
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

func (r *Reconciler) loadExternalIPs(ctx context.Context, result map[string]fulfillmentResource, pools map[string]string) error {
	var offset int32
	for {
		limit := int32(defaultPageSize)
		resp, err := r.externalIPClient.List(ctx, &privatev1.ExternalIPsListRequest{
			Offset: &offset,
			Limit:  &limit,
		})
		if err != nil {
			if isUnavailable(err) {
				r.unavailableTypes[events.ResourceTypeExternalIP] = struct{}{}
				return nil
			}
			return fmt.Errorf("listing external IPs (offset=%d): %w", offset, err)
		}
		items := resp.GetItems()
		for _, ip := range items {
			state := events.ExternalIPCurrentState(ip)
			var transitionTime time.Time
			var stateTransitionTime time.Time
			if ip.GetMetadata().GetDeletionTimestamp() != nil || ip.GetStatus().GetStateTransitionTime() != nil {
				transitionTime, err = events.ExternalIPTransitionTime(ip)
				if err != nil {
					return err
				}
				if timestamp := ip.GetStatus().GetStateTransitionTime(); timestamp != nil {
					stateTransitionTime = timestamp.AsTime()
				}
			}
			dimensions, dimErr := events.ExternalIPBillingDimensions(ip, r.deploymentID, pools)
			if dimErr != nil {
				return fmt.Errorf("mapping external IP %s: %w", ip.GetId(), dimErr)
			}
			result[ip.GetId()] = fulfillmentResource{
				resourceType:        events.ResourceTypeExternalIP,
				state:               state,
				version:             ip.GetMetadata().GetVersion(),
				tenantID:            ip.GetMetadata().GetTenant(),
				projectID:           ip.GetMetadata().GetProject(),
				transitionTime:      transitionTime,
				stateTransitionTime: stateTransitionTime,
				billingDimensions:   dimensions,
			}
		}
		if offset >= resp.GetTotal() {
			break
		}
		if len(items) == 0 {
			return fmt.Errorf("listing external IPs made no progress at offset %d", offset)
		}
		offset += int32(len(items))
	}
	return nil
}

func (r *Reconciler) loadNATGateways(ctx context.Context, result map[string]fulfillmentResource) error {
	var offset int32
	for {
		limit := int32(defaultPageSize)
		resp, err := r.natGatewayClient.List(ctx, &privatev1.NATGatewaysListRequest{
			Offset: &offset,
			Limit:  &limit,
		})
		if err != nil {
			if isUnavailable(err) {
				r.unavailableTypes[events.ResourceTypeNATGateway] = struct{}{}
				return nil
			}
			return fmt.Errorf("listing NAT gateways (offset=%d): %w", offset, err)
		}
		items := resp.GetItems()
		for _, gateway := range items {
			state := events.NATGatewayCurrentState(gateway)
			var transitionTime time.Time
			var stateTransitionTime time.Time
			if gateway.GetMetadata().GetDeletionTimestamp() != nil || gateway.GetStatus().GetStateTransitionTime() != nil {
				transitionTime, err = events.NetworkingTransitionTime(gateway.GetMetadata().GetDeletionTimestamp(), gateway.GetStatus().GetStateTransitionTime(), gateway.GetId())
				if err != nil {
					return err
				}
				if timestamp := gateway.GetStatus().GetStateTransitionTime(); timestamp != nil {
					stateTransitionTime = timestamp.AsTime()
				}
			}
			dimensions := events.NATGatewayBillingDimensions(gateway, r.deploymentID)
			if err := events.ValidateBillingDimensions(events.ResourceTypeNATGateway, dimensions); err != nil {
				return fmt.Errorf("mapping NAT gateway %s: %w", gateway.GetId(), err)
			}
			result[gateway.GetId()] = fulfillmentResource{
				resourceType:        events.ResourceTypeNATGateway,
				state:               state,
				version:             gateway.GetMetadata().GetVersion(),
				tenantID:            gateway.GetMetadata().GetTenant(),
				projectID:           gateway.GetMetadata().GetProject(),
				transitionTime:      transitionTime,
				stateTransitionTime: stateTransitionTime,
				billingDimensions:   dimensions,
			}
		}
		if offset >= resp.GetTotal() {
			break
		}
		if len(items) == 0 {
			return fmt.Errorf("listing NAT gateways made no progress at offset %d", offset)
		}
		offset += int32(len(items))
	}
	return nil
}

func (r *Reconciler) loadVolumes(ctx context.Context, result map[string]fulfillmentResource) error {
	var offset int32
	for {
		limit := int32(defaultPageSize)
		resp, err := r.volumeClient.List(ctx, &privatev1.VolumesListRequest{Offset: &offset, Limit: &limit})
		if err != nil {
			if isUnavailable(err) {
				r.unavailableTypes[events.ResourceTypeVolume] = struct{}{}
				return nil
			}
			return fmt.Errorf("listing volumes (offset=%d): %w", offset, err)
		}
		items := resp.GetItems()
		for _, volume := range items {
			dimensions, dimErr := events.VolumeBillingDimensions(volume)
			if dimErr != nil {
				return fmt.Errorf("mapping volume %s: %w", volume.GetId(), dimErr)
			}
			transitionTime := time.Time{}
			stateTransitionTime := time.Time{}
			if volume.GetMetadata().GetDeletionTimestamp() != nil {
				transitionTime = volume.GetMetadata().GetDeletionTimestamp().AsTime()
			} else if volume.GetStatus().GetStateTransitionTime() != nil {
				transitionTime = volume.GetStatus().GetStateTransitionTime().AsTime()
			}
			if volume.GetStatus().GetStateTransitionTime() != nil {
				stateTransitionTime = volume.GetStatus().GetStateTransitionTime().AsTime()
			}
			result[volume.GetId()] = fulfillmentResource{
				resourceType:        events.ResourceTypeVolume,
				state:               events.VolumeCurrentState(volume),
				isBillable:          events.IsVolumeBillable(volume),
				version:             volume.GetMetadata().GetVersion(),
				tenantID:            volume.GetMetadata().GetTenant(),
				projectID:           volume.GetMetadata().GetProject(),
				transitionTime:      transitionTime,
				stateTransitionTime: stateTransitionTime,
				billingDimensions:   dimensions,
			}
		}
		if offset >= resp.GetTotal() {
			break
		}
		if len(items) == 0 {
			return fmt.Errorf("listing volumes made no progress at offset %d", offset)
		}
		offset += int32(len(items))
	}
	return nil
}

func (r *Reconciler) loadBareMetalInstances(ctx context.Context, result map[string]fulfillmentResource) error {
	var offset int32
	var listedIDs []string
	var mutes map[string]heartbeat.BMaaSMeterMute
	for {
		limit := int32(defaultPageSize)
		resp, err := r.bareMetalClient.List(ctx, &privatev1.BareMetalInstancesListRequest{
			Offset: &offset,
			Limit:  &limit,
		})
		if err != nil {
			if isUnavailable(err) {
				r.unavailableTypes[events.ResourceTypeBareMetalInstance] = struct{}{}
				return nil
			}
			return fmt.Errorf("listing bare metal instances (offset=%d): %w", offset, err)
		}

		items := resp.GetItems()
		for _, bmi := range items {
			state := "UNSPECIFIED"
			var transitionTime time.Time
			if status := bmi.GetStatus(); status != nil {
				state = strings.TrimPrefix(status.GetState().String(), events.BareMetalInstanceStatePrefix)
				if timestamp := status.GetStateTransitionTime(); timestamp != nil {
					transitionTime = timestamp.AsTime().UTC()
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
			// Keep source presence and meter state independent of dimension conversion.
			// Existing billable projections can still heartbeat using their last valid
			// dimensions while the source row is repaired.
			listedIDs = append(listedIDs, bmi.GetId())
			if r.bmaasPresence != nil {
				if mutes == nil {
					mutes = make(map[string]heartbeat.BMaaSMeterMute)
				}
				mutes[bmi.GetId()] = heartbeat.BMaaSMeterMute{
					Allocation:  !events.IsAllocationBillableState(state),
					Consumption: !events.IsConsumptionBillableState(state),
				}
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
		}

		if len(items) < defaultPageSize {
			break
		}
		offset += int32(len(items))
	}
	if r.bmaasPresence != nil {
		r.bmaasPresence.Replace(listedIDs)
		r.bmaasPresence.SetMeterMutes(mutes)
	}
	return nil
}

func buildSyntheticHeartbeats(ps projection.ResourceState, now time.Time) ([]cloudevents.Event, error) {
	return buildSyntheticHeartbeatsWithMutes(ps, now, heartbeat.BMaaSMeterMute{})
}

func buildSyntheticHeartbeatsWithMutes(ps projection.ResourceState, now time.Time, mute heartbeat.BMaaSMeterMute) ([]cloudevents.Event, error) {
	if err := events.ValidateBillingDimensions(ps.ResourceType, ps.BillingDimensions); err != nil {
		return nil, err
	}
	baseID := fmt.Sprintf("synthetic-hb/%s/%d", ps.ResourceID, staleReferencePoint(ps, now).Unix())
	if events.IsNetworkingResourceType(ps.ResourceType) || events.IsVolumeResourceType(ps.ResourceType) {
		identity, err := events.HeartbeatIdentity(ps.ResourceType, ps.BillingDimensions, ps.BillableSince)
		if err != nil {
			return nil, err
		}
		baseID = fmt.Sprintf("%s/%s", baseID, identity)
	}
	return heartbeat.BuildHeartbeatEventsWithMutes(&ps, baseID, now, "osac-metering/reconciler", mute)
}

func reconciliationTransitionTime(resource fulfillmentResource, now time.Time) (time.Time, error) {
	if events.IsNetworkingResourceType(resource.resourceType) || events.IsVolumeResourceType(resource.resourceType) {
		if resource.transitionTime.IsZero() {
			return time.Time{}, fmt.Errorf("networking resource %s has no authoritative transition time", resource.resourceType)
		}
		return resource.transitionTime, nil
	}
	return now, nil
}

func reconciliationStateEntryTime(resource fulfillmentResource, current time.Time) (time.Time, error) {
	billable, err := fulfillmentResourceBillable(resource)
	if err != nil {
		return time.Time{}, fmt.Errorf("checking billability for %s: %w", resource.resourceType, err)
	}
	if billable && !resource.stateTransitionTime.IsZero() {
		return resource.stateTransitionTime, nil
	}
	return current, nil
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
	if ps.ResourceType == events.ResourceTypeBareMetalInstance {
		if ps.BMaaSMeterState.Allocation.ActiveSince != nil {
			return *ps.BMaaSMeterState.Allocation.ActiveSince
		}
		if ps.BMaaSMeterState.Consumption.ActiveSince != nil {
			return *ps.BMaaSMeterState.Consumption.ActiveSince
		}
		return now
	}
	if ps.BillableSince != nil {
		return *ps.BillableSince
	}
	return now
}
