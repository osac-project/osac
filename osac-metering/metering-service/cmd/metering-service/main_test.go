/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package main

import (
	"strings"
	"testing"

	kafkapkg "github.com/osac-project/osac-metering/internal/kafka"
)

// TestValidateRejectsDestructiveRetention checks the retention guard: zero deletes the
// heartbeat topics as they are written, and any negative other than -1 is rejected per
// record, forever. Both fail at startup instead.
func TestValidateRejectsDestructiveRetention(t *testing.T) {
	base := &config{
		kafka:        kafkapkg.ConnectionConfig{Brokers: "b:9093", SASLUser: "u", SASLPassFile: "f"},
		dbURLFile:    "/db",
		deploymentID: "osac/osac",
	}
	for _, retention := range []string{"0", "-2"} {
		cfg := *base
		cfg.topicRetentionMS = retention
		err := cfg.validate()
		if err == nil || !strings.Contains(err.Error(), "must be -1 (indefinite) or positive") {
			t.Fatalf("retention %q must be rejected, got %v", retention, err)
		}
	}
	for _, retention := range []string{"-1", "604800000"} {
		cfg := *base
		cfg.topicRetentionMS = retention
		if err := cfg.validate(); err != nil {
			t.Fatalf("retention %q must be accepted, got %v", retention, err)
		}
	}
}
