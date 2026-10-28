/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRoundMetrics(t *testing.T) {
	tenant := "metrics-round"

	RoundSucceeded(tenant, time.Unix(1000, 0))
	if got := testutil.ToFloat64(lastRoundTime.WithLabelValues(tenant)); got != 1000 {
		t.Fatalf("last round time is %v, want 1000", got)
	}

	RoundFailed(tenant)
	RoundFailed(tenant)
	if got := testutil.ToFloat64(roundFailures.WithLabelValues(tenant)); got != 2 {
		t.Fatalf("round failures is %v, want 2", got)
	}
}

func TestOutboxDepth(t *testing.T) {
	tenant := "metrics-outbox"

	OutboxDepth(tenant, 7)
	if got := testutil.ToFloat64(outboxDepth.WithLabelValues(tenant)); got != 7 {
		t.Fatalf("outbox depth is %v, want 7", got)
	}

	OutboxDepth(tenant, 0)
	if got := testutil.ToFloat64(outboxDepth.WithLabelValues(tenant)); got != 0 {
		t.Fatalf("outbox depth is %v, want 0 after a drain", got)
	}
}

func TestEventsSkipped(t *testing.T) {
	tenant := "metrics-skips"

	EventsSkipped(tenant, SkipForeignTenant, 3)
	if got := testutil.ToFloat64(eventsSkipped.WithLabelValues(tenant, SkipForeignTenant)); got != 3 {
		t.Fatalf("skipped foreign-tenant events is %v, want 3", got)
	}

	// A non-positive count is a no-op, so a retention range that shrank to nothing
	// does not create a series.
	EventsSkipped(tenant, SkipRetention, 0)
	if got := testutil.ToFloat64(eventsSkipped.WithLabelValues(tenant, SkipRetention)); got != 0 {
		t.Fatalf("skipped retention events is %v, want 0", got)
	}
}

func TestRulesMetrics(t *testing.T) {
	RulesLoaded(2)
	if got := testutil.ToFloat64(rulesLoaded); got != 2 {
		t.Fatalf("loaded rules is %v, want 2", got)
	}

	RulesReloadFailed("list")
	RulesReloadFailed("list")
	RulesReloadFailed("check")
	if got := testutil.ToFloat64(rulesReloadFailures.WithLabelValues("list")); got != 2 {
		t.Fatalf("listing reload failures is %v, want 2", got)
	}
	if got := testutil.ToFloat64(rulesReloadFailures.WithLabelValues("check")); got != 1 {
		t.Fatalf("check reload failures is %v, want 1", got)
	}
}

func TestDeleteTenantHealthMetrics(t *testing.T) {
	tenant := "metrics-delete"

	RoundSucceeded(tenant, time.Unix(1000, 0))
	RoundFailed(tenant)
	OutboxDepth(tenant, 7)

	// Other tests in this package add series to the same global vecs, so the
	// check is on the series each deletion removes, not on a total count.
	lastBefore := testutil.CollectAndCount(lastRoundTime)
	failuresBefore := testutil.CollectAndCount(roundFailures)
	outboxBefore := testutil.CollectAndCount(outboxDepth)

	DeleteTenantHealthMetrics(tenant)

	if got := testutil.CollectAndCount(lastRoundTime); got != lastBefore-1 {
		t.Fatalf("last round time series count is %v, want %v", got, lastBefore-1)
	}
	if got := testutil.CollectAndCount(roundFailures); got != failuresBefore-1 {
		t.Fatalf("round failures series count is %v, want %v", got, failuresBefore-1)
	}
	if got := testutil.CollectAndCount(outboxDepth); got != outboxBefore-1 {
		t.Fatalf("outbox depth series count is %v, want %v", got, outboxBefore-1)
	}
}
