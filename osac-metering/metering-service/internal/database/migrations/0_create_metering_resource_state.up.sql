-- Design doc (enhancement-proposals#131) schema includes catalog_item_id, template_id,
-- last_metered_at, and version columns; fulfillment_version is BIGINT there.
-- Deferred: catalog_item_id/template_id (not yet used by heartbeat/correction consumers),
-- last_metered_at (superseded by last_heartbeat_at + transition_time),
-- version (fulfillment_version + SELECT FOR UPDATE suffice for current concurrency model).
-- INT for fulfillment_version matches proto int32; BIGINT deferred until version space grows.

CREATE TABLE metering_resource_state (
    resource_id       TEXT        NOT NULL PRIMARY KEY,
    resource_type     TEXT        NOT NULL,
    tenant_id         TEXT        NOT NULL,
    project_id        TEXT,
    current_state     TEXT        NOT NULL,
    previous_state    TEXT,
    is_billable       BOOLEAN     NOT NULL DEFAULT FALSE,
    billable_since    TIMESTAMPTZ,
    last_heartbeat_at TIMESTAMPTZ,
    transition_time   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    fulfillment_version INT       NOT NULL,
    billing_dimensions JSONB      NOT NULL DEFAULT '{}'::JSONB,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_metering_resource_state_billable
    ON metering_resource_state (is_billable, last_heartbeat_at)
    WHERE is_billable = TRUE;

CREATE INDEX idx_metering_resource_state_tenant
    ON metering_resource_state (tenant_id);

CREATE INDEX idx_metering_resource_state_resource_type
    ON metering_resource_state (resource_type);
