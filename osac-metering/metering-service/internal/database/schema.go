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
-- What we know, and since when. One row per fact: the value one field of one resource
-- held from a moment on. The fold writes it from the tenant's events, idempotently, in
-- any order; the whole table is rebuildable by replaying the event topic.
--
-- Example: tenant acme's VM vm-7 entered RUNNING at 10:00, event 42 carried it:
--   ('acme', 'ComputeInstance', 'vm-7', 'status.state', '2026-10-05 10:00:00Z', 'RUNNING', 42)
CREATE TABLE IF NOT EXISTS history (
    tenant        TEXT        NOT NULL,
    resource_type TEXT        NOT NULL,
    resource_id   TEXT        NOT NULL,
    field         TEXT        NOT NULL,
    since         TIMESTAMPTZ NOT NULL,   -- the moment the value took effect
    value         TEXT        NOT NULL,   -- the value from then on: a state name, a
                                          -- number, or a reference's id, as text
    event_offset  BIGINT      NOT NULL,   -- position of the carrying event in the
                                          -- topic; the round uses it to find changed
                                          -- resources
    PRIMARY KEY (tenant, resource_type, resource_id, field, since)
);
-- Finds the resources whose facts changed since the previous round.
CREATE INDEX IF NOT EXISTS history_by_offset
    ON history (tenant, event_offset);
-- Finds the resources with facts dated after the previous round, because a writer's clock can
-- run slightly ahead of the round clock.
CREATE INDEX IF NOT EXISTS history_by_since
    ON history (tenant, since);
-- Finds the resources a changed root owns; without it every root change scans the tenant's
-- whole history.
CREATE INDEX IF NOT EXISTS history_by_owner
    ON history (tenant, field, value);

-- What metering has already billed. One row per committed usage piece: the round compares
-- the usage it expects against this table and sends only the difference. Rebuildable from
-- the heartbeat topic, except pieces that were never sent.
--
-- Example: 60 seconds of compute billed for vm-7 under the small type:
--   ('acme', 'ComputeInstance', 'vm-7', 'compute', 'compute',
--    '{"deployment":"us-east","instance_type":"small","tenant":"acme"}',
--    '2026-10-05 10:00:00Z', '2026-10-05 10:01:00Z', 60000000)
CREATE TABLE IF NOT EXISTS ledger (
    tenant         TEXT        NOT NULL,
    resource_type  TEXT        NOT NULL,
    resource_id    TEXT        NOT NULL,
    meter          TEXT        NOT NULL,  -- the measured quantity, for example compute
    component      TEXT        NOT NULL,  -- the provider's billable line the meter feeds;
                                          -- defaults to the meter name
    dimensions     TEXT        NOT NULL,  -- the piece's dimension values, as JSON text;
                                          -- part of the billing identity
    start_time     TIMESTAMPTZ NOT NULL,
    end_time       TIMESTAMPTZ NOT NULL,
    quantity_micro BIGINT      NOT NULL,  -- the billed quantity in micro units of the
                                          -- meter's unit: 60 seconds is 60000000
    PRIMARY KEY (tenant, resource_type, resource_id, meter, component, dimensions, start_time)
);
-- Finds the meters that were billable at the previous round: their rows end at the previous
-- round's time and only need their latest interval extended.
CREATE INDEX IF NOT EXISTS ledger_by_end_time
    ON ledger (tenant, end_time);

-- Heartbeats waiting for the relay, in round order. The relay sends them to the tenant's
-- heartbeat topic and deletes each row after its send succeeds, so a crash resend carries
-- the same heartbeat id and the provider drops it as a duplicate.
--
-- Example: seq 7 carries the heartbeat with id 'osac-heartbeat-abc123', written by the
-- round at 10:04.
CREATE TABLE IF NOT EXISTS outbox (
    seq        BIGSERIAL PRIMARY KEY,
    tenant     TEXT        NOT NULL,
    id         TEXT        NOT NULL UNIQUE,  -- the heartbeat's CloudEvent id; unique, so
                                             -- a resend bills once
    payload    JSONB       NOT NULL,
    round_time TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS outbox_by_tenant
    ON outbox (tenant, seq);

-- Where each tenant stopped. One row per tenant, updated by the loops as they go.
--
-- Example: tenant acme's fold consumed events up to offset 412, catch-up read heartbeats
-- up to offset 380, and the last round ran at 10:04.
CREATE TABLE IF NOT EXISTS progress (
    tenant                  TEXT PRIMARY KEY,
    event_offset            BIGINT      NOT NULL DEFAULT 0,  -- the fold's position in the
                                                             -- event topic
    heartbeat_offset        BIGINT      NOT NULL DEFAULT 0,  -- catch-up's position in the
                                                             -- heartbeat topic
    last_round_time         TIMESTAMPTZ,                     -- when the last round ran
    last_round_event_offset BIGINT      NOT NULL DEFAULT 0   -- the event offset the last
                                                             -- round had folded
);

-- Which rule revisions metering has billed under, so a revision that would reprice
-- spans this database already billed refuses to start rather than change them.
--
-- Example: revision 0001, effective since the epoch, with its meters as YAML.
CREATE TABLE IF NOT EXISTS rule_revisions (
    revision     TEXT        PRIMARY KEY,
    effective_at TIMESTAMPTZ NOT NULL,
    meters_yaml  TEXT        NOT NULL
);

-- The heartbeat ids catch-up applied during the recovery in progress, so a copy the
-- relay resent after a crash mid-replay counts once. The comparison clears the table
-- when its round commits: from then on the last round time alone separates copies,
-- because every heartbeat on the topic is from a round at or before it.
--
-- Example: during acme's rebuild, the heartbeat 'osac-heartbeat-abc123' was applied;
-- a resent copy of it is skipped wherever it appears.
CREATE TABLE IF NOT EXISTS replayed_heartbeats (
    tenant TEXT NOT NULL,
    id     TEXT NOT NULL,
    PRIMARY KEY (tenant, id)
);
`
