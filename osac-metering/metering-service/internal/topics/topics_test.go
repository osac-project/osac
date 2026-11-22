/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package topics

import "testing"

// TestHeartbeatTopicPrefixResolution checks the default-prefix convenience against the
// prefix-aware form: an empty prefix must name exactly the topics the shared constants
// name, and a configured prefix must be kept.
func TestHeartbeatTopicPrefixResolution(t *testing.T) {
	if got := HeartbeatTopic("", "acme"); got != Heartbeat("acme") || got != "osac.metering.heartbeat.acme" {
		t.Fatalf("empty prefix must use the default, got %q", got)
	}
	if got := DeadLetterTopic("", "acme"); got != DeadLetter("acme") || got != "osac.metering.dlq.acme" {
		t.Fatalf("empty prefix must use the default, got %q", got)
	}
	if got := HeartbeatTopic("pr2100v1.metering.heartbeat.", "acme"); got != "pr2100v1.metering.heartbeat.acme" {
		t.Fatalf("configured prefix must be kept, got %q", got)
	}
	if got := DeadLetterTopic("pr2100v1.metering.dlq.", "acme"); got != "pr2100v1.metering.dlq.acme" {
		t.Fatalf("configured prefix must be kept, got %q", got)
	}
}
