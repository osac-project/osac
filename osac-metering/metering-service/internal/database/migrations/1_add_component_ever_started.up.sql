ALTER TABLE metering_resource_state
    ADD COLUMN component_ever_started JSONB NOT NULL DEFAULT '{}'::JSONB;
