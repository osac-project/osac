/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package projection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/osac-project/osac-metering/internal/events"
	"github.com/osac-project/osac-metering/schema"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

const resourceStateSelect = `
	SELECT r.resource_id, r.resource_type, r.tenant_id, r.project_id,
	       r.current_state, r.previous_state, r.is_billable, r.ever_billable, r.billable_since,
	       r.last_heartbeat_at, r.transition_time, r.fulfillment_version,
	       r.billing_dimensions, r.component_billable_since, r.deleted_at,
	       allocation.active_since, allocation.first_started_at,
	       consumption.active_since, consumption.first_started_at
	FROM metering_resource_state AS r
	LEFT JOIN metering_resource_meter_state AS allocation
	       ON allocation.resource_id = r.resource_id AND allocation.meter_type = 'allocation'
	LEFT JOIN metering_resource_meter_state AS consumption
	       ON consumption.resource_id = r.resource_id AND consumption.meter_type = 'consumption'
`

func (s *PostgresStore) Get(ctx context.Context, resourceID string) (*ResourceState, error) {
	row := s.pool.QueryRow(ctx, resourceStateSelect+`WHERE r.resource_id = $1`,
		resourceID)

	state, err := scanResourceState(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("querying resource state %s: %w", resourceID, err)
	}
	return state, nil
}

func (s *PostgresStore) Upsert(ctx context.Context, state ResourceState) error {
	dimensions, err := json.Marshal(state.BillingDimensions)
	if err != nil {
		return fmt.Errorf("marshaling billing dimensions: %w", err)
	}
	componentSince, err := json.Marshal(state.ComponentBillableSince)
	if err != nil {
		return fmt.Errorf("marshaling component billable since: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var storedVersion *int32
	var storedDeletedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT fulfillment_version, deleted_at
		FROM metering_resource_state
		WHERE resource_id = $1
		FOR UPDATE`,
		state.ResourceID).Scan(&storedVersion, &storedDeletedAt)

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("locking resource state %s: %w", state.ResourceID, err)
	}

	// Tombstones are terminal for this resource ID. Same-version upserts on a
	// live row remain allowed so a replay after publish failure can re-process
	// idempotently, but no delayed event may revive a deleted row.
	if storedDeletedAt != nil {
		return ErrStaleVersion
	}
	if storedVersion != nil && *storedVersion > state.FulfillmentVersion {
		return ErrStaleVersion
	}

	if state.ResourceType == schema.ResourceTypeBareMetalInstance {
		state.BillableSince = state.BMaaSMeterState.Allocation.ActiveSince
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO metering_resource_state (
			resource_id, resource_type, tenant_id, project_id,
			current_state, previous_state, ever_billable, billable_since,
			last_heartbeat_at, transition_time, fulfillment_version,
			billing_dimensions, component_billable_since, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, ($7::timestamptz IS NOT NULL), $7, $8, $9, $10, $11, $12, NOW())
		ON CONFLICT (resource_id) DO UPDATE SET
			resource_type = EXCLUDED.resource_type,
			tenant_id = EXCLUDED.tenant_id,
			project_id = EXCLUDED.project_id,
			current_state = EXCLUDED.current_state,
			previous_state = EXCLUDED.previous_state,
			ever_billable = metering_resource_state.ever_billable OR (EXCLUDED.billable_since IS NOT NULL),
			billable_since = EXCLUDED.billable_since,
			last_heartbeat_at = EXCLUDED.last_heartbeat_at,
			transition_time = EXCLUDED.transition_time,
			fulfillment_version = EXCLUDED.fulfillment_version,
			billing_dimensions = EXCLUDED.billing_dimensions,
			component_billable_since = EXCLUDED.component_billable_since,
			deleted_at = NULL,
			updated_at = NOW()
		WHERE metering_resource_state.deleted_at IS NULL
		  AND metering_resource_state.fulfillment_version <= EXCLUDED.fulfillment_version`,
		state.ResourceID,
		state.ResourceType,
		state.TenantID,
		nullIfEmpty(state.ProjectID),
		state.CurrentState,
		nullIfEmpty(state.PreviousState),
		state.BillableSince,
		state.LastHeartbeatAt,
		state.TransitionTime,
		state.FulfillmentVersion,
		dimensions,
		componentSince,
	)
	if err != nil {
		return fmt.Errorf("upserting resource state %s: %w", state.ResourceID, err)
	}

	if state.ResourceType == schema.ResourceTypeBareMetalInstance {
		_, err = tx.Exec(ctx, `
			INSERT INTO metering_resource_meter_state (
				resource_id, meter_type, active_since, first_started_at
			) VALUES
				($1, 'allocation', $2, $3),
				($1, 'consumption', $4, $5)
			ON CONFLICT (resource_id, meter_type) DO UPDATE SET
				active_since = EXCLUDED.active_since,
				first_started_at = COALESCE(
					metering_resource_meter_state.first_started_at,
					EXCLUDED.first_started_at
				)`,
			state.ResourceID,
			state.BMaaSMeterState.Allocation.ActiveSince,
			state.BMaaSMeterState.Allocation.FirstStartedAt,
			state.BMaaSMeterState.Consumption.ActiveSince,
			state.BMaaSMeterState.Consumption.FirstStartedAt,
		)
		if err != nil {
			return fmt.Errorf("upserting BMaaS meter state %s: %w", state.ResourceID, err)
		}
	}

	return tx.Commit(ctx)
}

func (s *PostgresStore) DeleteIfVersion(ctx context.Context, resourceID string, version int32) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("beginning tombstone transaction for %s: %w", resourceID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result, err := tx.Exec(ctx, `
		UPDATE metering_resource_state
		SET deleted_at = NOW(), fulfillment_version = GREATEST(fulfillment_version, $2),
		    billable_since = NULL, component_billable_since = '{}'::JSONB, updated_at = NOW()
		WHERE resource_id = $1 AND fulfillment_version <= $2 AND deleted_at IS NULL`,
		resourceID, version)
	if err != nil {
		return false, fmt.Errorf("tombstoning resource state %s: %w", resourceID, err)
	}
	if result.RowsAffected() != 1 {
		return false, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE metering_resource_meter_state
		SET active_since = NULL
		WHERE resource_id = $1`, resourceID); err != nil {
		return false, fmt.Errorf("clearing BMaaS meter state %s: %w", resourceID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("committing tombstone for %s: %w", resourceID, err)
	}
	return true, nil
}

func (s *PostgresStore) ListBillable(ctx context.Context) ([]ResourceState, error) {
	rows, err := s.pool.Query(ctx, resourceStateSelect+`WHERE
		r.deleted_at IS NULL AND ((r.resource_type <> $1 AND r.is_billable = TRUE)
		OR (r.resource_type = $1 AND allocation.active_since IS NOT NULL))`,
		schema.ResourceTypeBareMetalInstance)
	if err != nil {
		return nil, fmt.Errorf("querying billable resources: %w", err)
	}
	defer rows.Close()
	states, err := collectResourceStates(rows)
	if err != nil {
		return nil, err
	}
	billableStates := states[:0]
	for _, state := range states {
		if state.ResourceType == schema.ResourceTypeBareMetalInstance &&
			!events.IsAllocationBillableState(state.CurrentState) {
			continue
		}
		billableStates = append(billableStates, state)
	}
	return billableStates, nil
}

func (s *PostgresStore) ListAll(ctx context.Context) ([]ResourceState, error) {
	rows, err := s.pool.Query(ctx, resourceStateSelect+`WHERE r.deleted_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("querying all resources: %w", err)
	}
	defer rows.Close()
	states, err := collectResourceStates(rows)
	if err != nil {
		return nil, err
	}
	return states, nil
}

func (s *PostgresStore) UpdateLastHeartbeat(ctx context.Context, resourceIDs []string, at time.Time) error {
	if len(resourceIDs) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE metering_resource_state
		SET last_heartbeat_at = $1, updated_at = NOW()
		WHERE resource_id = ANY($2)`,
		at, resourceIDs)
	if err != nil {
		return fmt.Errorf("updating last heartbeat: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanResourceState(row rowScanner) (*ResourceState, error) {
	var (
		state              ResourceState
		previousState      *string
		projectID          *string
		billableSince      *time.Time
		lastHeartbeat      *time.Time
		deletedAt          *time.Time
		dimensionsJSON     []byte
		componentSinceJSON []byte
		allocationActive   *time.Time
		allocationFirst    *time.Time
		consumptionActive  *time.Time
		consumptionFirst   *time.Time
	)

	err := row.Scan(
		&state.ResourceID,
		&state.ResourceType,
		&state.TenantID,
		&projectID,
		&state.CurrentState,
		&previousState,
		&state.IsBillable,
		&state.EverBillable,
		&billableSince,
		&lastHeartbeat,
		&state.TransitionTime,
		&state.FulfillmentVersion,
		&dimensionsJSON,
		&componentSinceJSON,
		&deletedAt,
		&allocationActive,
		&allocationFirst,
		&consumptionActive,
		&consumptionFirst,
	)
	if err != nil {
		return nil, err
	}

	if previousState != nil {
		state.PreviousState = *previousState
	}
	if projectID != nil {
		state.ProjectID = *projectID
	}
	state.BillableSince = billableSince
	state.LastHeartbeatAt = lastHeartbeat
	state.Deleted = deletedAt != nil

	if len(dimensionsJSON) > 0 {
		if err := json.Unmarshal(dimensionsJSON, &state.BillingDimensions); err != nil {
			return nil, fmt.Errorf("unmarshaling billing dimensions: %w", err)
		}
	}

	if len(componentSinceJSON) > 0 {
		if err := json.Unmarshal(componentSinceJSON, &state.ComponentBillableSince); err != nil {
			return nil, fmt.Errorf("unmarshaling component billable since: %w", err)
		}
	}
	state.BMaaSMeterState = BMaaSMeterState{
		Allocation: MeterState{
			ActiveSince:    allocationActive,
			FirstStartedAt: allocationFirst,
		},
		Consumption: MeterState{
			ActiveSince:    consumptionActive,
			FirstStartedAt: consumptionFirst,
		},
	}
	if state.ResourceType == schema.ResourceTypeBareMetalInstance {
		state.BillableSince = allocationActive
		state.IsBillable = allocationActive != nil
	}
	if state.Deleted {
		state.IsBillable = false
		state.BillableSince = nil
		state.BMaaSMeterState.Allocation.ActiveSince = nil
		state.BMaaSMeterState.Consumption.ActiveSince = nil
	}
	return &state, nil
}

func collectResourceStates(rows pgx.Rows) ([]ResourceState, error) {
	var states []ResourceState
	for rows.Next() {
		state, err := scanResourceState(rows)
		if err != nil {
			return nil, err
		}
		states = append(states, *state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating resource states: %w", err)
	}
	return states, nil
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
