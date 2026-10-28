/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package database

import (
	"strings"
	"testing"
)

func TestSchemaSQLIncludesNormalizedBMaaSMeterStateTable(t *testing.T) {
	for _, statement := range []string{
		"CREATE TABLE IF NOT EXISTS metering_resource_meter_state",
		"PRIMARY KEY (resource_id, meter_type)",
		"REFERENCES metering_resource_state(resource_id) ON DELETE CASCADE",
		"deleted_at        TIMESTAMPTZ",
		"ALTER TABLE metering_resource_state ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ",
	} {
		if !strings.Contains(schemaSQL, statement) {
			t.Errorf("schemaSQL does not contain %q", statement)
		}
	}
	if strings.Contains(schemaSQL, "component_ever_started") {
		t.Error("schemaSQL should not contain the branch-local legacy component_ever_started column")
	}
}
