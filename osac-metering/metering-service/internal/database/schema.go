/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

func InitializeSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("creating metering database schema: %w", err)
	}
	return nil
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS metering_resource_state (
    resource_id       TEXT        NOT NULL PRIMARY KEY,
    resource_type     TEXT        NOT NULL,
    tenant_id         TEXT        NOT NULL,
    project_id        TEXT,
    current_state     TEXT        NOT NULL,
    previous_state    TEXT,
    billable_since    TIMESTAMPTZ,
    is_billable       BOOLEAN GENERATED ALWAYS AS (billable_since IS NOT NULL) STORED,
    ever_billable     BOOLEAN     NOT NULL DEFAULT FALSE,
    last_heartbeat_at TIMESTAMPTZ,
    transition_time   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    fulfillment_version INT       NOT NULL,
    billing_dimensions JSONB      NOT NULL DEFAULT '{}'::JSONB,
    component_billable_since JSONB NOT NULL DEFAULT '{}'::JSONB,
    deleted_at        TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS metering_resource_meter_state (
    resource_id      TEXT        NOT NULL REFERENCES metering_resource_state(resource_id) ON DELETE CASCADE,
    meter_type       TEXT        NOT NULL CHECK (meter_type IN ('allocation', 'consumption')),
    active_since     TIMESTAMPTZ,
    first_started_at TIMESTAMPTZ,
    PRIMARY KEY (resource_id, meter_type)
);
-- CREATE TABLE IF NOT EXISTS does not alter an existing table created before
-- deleted_at was introduced, so keep the upgrade idempotent too.
ALTER TABLE metering_resource_state ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_metering_resource_state_billable
    ON metering_resource_state (is_billable, last_heartbeat_at)
    WHERE is_billable = TRUE;

CREATE INDEX IF NOT EXISTS idx_metering_resource_state_tenant
    ON metering_resource_state (tenant_id);

CREATE INDEX IF NOT EXISTS idx_metering_resource_state_resource_type
    ON metering_resource_state (resource_type);
`
