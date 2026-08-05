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
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) Get(ctx context.Context, resourceID string) (*ResourceState, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT resource_id, resource_type, tenant_id, project_id,
		       current_state, previous_state, is_billable, billable_since,
		       last_heartbeat_at, transition_time, fulfillment_version,
		       billing_dimensions
		FROM metering_resource_state
		WHERE resource_id = $1`,
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

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var storedVersion *int32
	err = tx.QueryRow(ctx, `
		SELECT fulfillment_version
		FROM metering_resource_state
		WHERE resource_id = $1
		FOR UPDATE`,
		state.ResourceID).Scan(&storedVersion)

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("locking resource state %s: %w", state.ResourceID, err)
	}

	// Reject strictly older versions. Same-version upserts are allowed so that
	// a replayed event (e.g. after publish failure) can re-process without being
	// silently dropped — the projection write is idempotent for the same version.
	if storedVersion != nil && *storedVersion > state.FulfillmentVersion {
		return ErrStaleVersion
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO metering_resource_state (
			resource_id, resource_type, tenant_id, project_id,
			current_state, previous_state, is_billable, billable_since,
			last_heartbeat_at, transition_time, fulfillment_version,
			billing_dimensions, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW())
		ON CONFLICT (resource_id) DO UPDATE SET
			resource_type = EXCLUDED.resource_type,
			tenant_id = EXCLUDED.tenant_id,
			project_id = EXCLUDED.project_id,
			current_state = EXCLUDED.current_state,
			previous_state = EXCLUDED.previous_state,
			is_billable = EXCLUDED.is_billable,
			billable_since = EXCLUDED.billable_since,
			last_heartbeat_at = EXCLUDED.last_heartbeat_at,
			transition_time = EXCLUDED.transition_time,
			fulfillment_version = EXCLUDED.fulfillment_version,
			billing_dimensions = EXCLUDED.billing_dimensions,
			updated_at = NOW()
		WHERE metering_resource_state.fulfillment_version <= EXCLUDED.fulfillment_version`,
		state.ResourceID,
		state.ResourceType,
		state.TenantID,
		nullIfEmpty(state.ProjectID),
		state.CurrentState,
		nullIfEmpty(state.PreviousState),
		state.IsBillable,
		state.BillableSince,
		state.LastHeartbeatAt,
		state.TransitionTime,
		state.FulfillmentVersion,
		dimensions,
	)
	if err != nil {
		return fmt.Errorf("upserting resource state %s: %w", state.ResourceID, err)
	}

	return tx.Commit(ctx)
}

func (s *PostgresStore) Delete(ctx context.Context, resourceID string) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM metering_resource_state WHERE resource_id = $1`,
		resourceID)
	if err != nil {
		return fmt.Errorf("deleting resource state %s: %w", resourceID, err)
	}
	return nil
}

func (s *PostgresStore) ListBillable(ctx context.Context) ([]ResourceState, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT resource_id, resource_type, tenant_id, project_id,
		       current_state, previous_state, is_billable, billable_since,
		       last_heartbeat_at, transition_time, fulfillment_version,
		       billing_dimensions
		FROM metering_resource_state
		WHERE is_billable = TRUE`)
	if err != nil {
		return nil, fmt.Errorf("querying billable resources: %w", err)
	}
	defer rows.Close()
	return collectResourceStates(rows)
}

func (s *PostgresStore) ListAll(ctx context.Context) ([]ResourceState, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT resource_id, resource_type, tenant_id, project_id,
		       current_state, previous_state, is_billable, billable_since,
		       last_heartbeat_at, transition_time, fulfillment_version,
		       billing_dimensions
		FROM metering_resource_state`)
	if err != nil {
		return nil, fmt.Errorf("querying all resources: %w", err)
	}
	defer rows.Close()
	return collectResourceStates(rows)
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

func scanResourceState(row pgx.Row) (*ResourceState, error) {
	var (
		state          ResourceState
		previousState  *string
		projectID      *string
		billableSince  *time.Time
		lastHeartbeat  *time.Time
		dimensionsJSON []byte
	)

	err := row.Scan(
		&state.ResourceID,
		&state.ResourceType,
		&state.TenantID,
		&projectID,
		&state.CurrentState,
		&previousState,
		&state.IsBillable,
		&billableSince,
		&lastHeartbeat,
		&state.TransitionTime,
		&state.FulfillmentVersion,
		&dimensionsJSON,
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

	if len(dimensionsJSON) > 0 {
		if err := json.Unmarshal(dimensionsJSON, &state.BillingDimensions); err != nil {
			return nil, fmt.Errorf("unmarshaling billing dimensions: %w", err)
		}
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
