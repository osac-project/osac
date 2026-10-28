/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package database

import (
	"strings"
	"testing"
)

func TestSchemaCreatesEventLogTables(t *testing.T) {
	for _, table := range []string{
		"history", "ledger", "outbox", "progress", "rule_revisions",
	} {
		if !strings.Contains(schemaSQL, "CREATE TABLE IF NOT EXISTS "+table+" (") {
			t.Errorf("schemaSQL does not create table %s", table)
		}
	}
}

func TestSchemaHistoryKeyCoversFieldAndSince(t *testing.T) {
	if !strings.Contains(
		schemaSQL,
		"PRIMARY KEY (tenant, resource_type, resource_id, field, since)",
	) {
		t.Error("history rows must be keyed by tenant, resource, field, and since")
	}
}
