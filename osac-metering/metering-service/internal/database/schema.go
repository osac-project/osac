/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// InitializeSchema creates the metering database schema. Every table is rebuildable from Kafka
// or from the rule revision ConfigMaps; only the outbox is not, because a ledger rebuilt from
// the heartbeat topic does not include heartbeats that were never sent.
func InitializeSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("creating metering database schema: %w", err)
	}
	return nil
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS history (
    tenant        TEXT        NOT NULL,
    resource_type TEXT        NOT NULL,
    resource_id   TEXT        NOT NULL,
    field         TEXT        NOT NULL,
    since         TIMESTAMPTZ NOT NULL,
    value         TEXT        NOT NULL,
    event_offset  BIGINT      NOT NULL,
    PRIMARY KEY (tenant, resource_type, resource_id, field, since)
);

-- Finds the resources whose facts changed since the previous round.
CREATE INDEX IF NOT EXISTS history_by_offset
    ON history (tenant, event_offset);

-- Finds the resources with facts dated after the previous round, because a writer's clock can
-- run slightly ahead of the round clock.
CREATE INDEX IF NOT EXISTS history_by_since
    ON history (tenant, since);

CREATE TABLE IF NOT EXISTS ledger (
    tenant         TEXT        NOT NULL,
    resource_type  TEXT        NOT NULL,
    resource_id    TEXT        NOT NULL,
    meter          TEXT        NOT NULL,
    component      TEXT        NOT NULL,
    dimensions     TEXT        NOT NULL,
    start_time     TIMESTAMPTZ NOT NULL,
    end_time       TIMESTAMPTZ NOT NULL,
    quantity_micro BIGINT      NOT NULL,
    PRIMARY KEY (tenant, resource_type, resource_id, meter, component, dimensions, start_time)
);

-- Finds the meters that were billable at the previous round: their rows end at the previous
-- round's time and only need their latest interval extended.
CREATE INDEX IF NOT EXISTS ledger_by_end_time
    ON ledger (tenant, end_time);

CREATE TABLE IF NOT EXISTS outbox (
    seq        BIGSERIAL PRIMARY KEY,
    tenant     TEXT        NOT NULL,
    id         TEXT        NOT NULL UNIQUE,
    payload    JSONB       NOT NULL,
    round_time TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS outbox_by_tenant
    ON outbox (tenant, seq);

CREATE TABLE IF NOT EXISTS progress (
    tenant                  TEXT PRIMARY KEY,
    event_offset            BIGINT      NOT NULL DEFAULT 0,
    heartbeat_offset        BIGINT      NOT NULL DEFAULT 0,
    last_round_time         TIMESTAMPTZ,
    last_round_event_offset BIGINT      NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS rule_revisions (
    revision     TEXT        PRIMARY KEY,
    effective_at TIMESTAMPTZ NOT NULL,
    meters_yaml  TEXT        NOT NULL
);
`
