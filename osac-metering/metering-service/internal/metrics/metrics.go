/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is
distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and limitations under the License.
*/

// Package metrics defines the Prometheus metrics the metering service exports. They register
// on the default registry, which main serves next to the health endpoints, so every binary
// that runs the loops exports them without wiring.
//
// The metrics exist because billing can stop while the pod stays ready: the round loop logs
// its errors and keeps going, and readiness only fails when a loop exits. A tenant whose
// last round ages, whose rounds keep failing, or whose outbox keeps growing is visible here
// even though the pod reports healthy.
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Skip reasons for EventsSkipped.
const (
	// SkipUndecodable marks a record that is not a valid event and can never become one.
	SkipUndecodable = "undecodable"
	// SkipForeignTenant marks an event whose payload names a tenant other than its topic.
	SkipForeignTenant = "foreign_tenant"
	// SkipUnknownPayload marks an event whose payload this build cannot read: a payload
	// kind it does not know, or no payload at all.
	SkipUnknownPayload = "unknown_payload"
	// SkipMissingResourceID marks a tracked payload without a resource id.
	SkipMissingResourceID = "missing_resource_id"
	// SkipMissingCreationTime marks a tracked payload without a creation timestamp.
	SkipMissingCreationTime = "missing_creation_time"
	// SkipMissingDeletionTime marks a deletion event without an envelope timestamp.
	SkipMissingDeletionTime = "missing_deletion_time"
	// SkipRetention marks the range a topic lost below the stored fold offset.
	SkipRetention = "retention"
)

var (
	lastRoundTime = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "osac_metering_round_last_time_seconds",
		Help: "Unix time of the tenant's last round-loop tick that completed without error. " +
			"Billing for the tenant is not progressing while this ages.",
	}, []string{"tenant"})

	roundFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "osac_metering_round_failures_total",
		Help: "Round attempts that returned an error, per tenant.",
	}, []string{"tenant"})

	outboxDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "osac_metering_outbox_depth",
		Help: "Heartbeats the rounds wrote and the relay has not sent to the topic yet, per tenant.",
	}, []string{"tenant"})

	eventsSkipped = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "osac_metering_events_skipped_total",
		Help: "Event-topic records the fold could not or must not use, per tenant and reason.",
	}, []string{"tenant", "reason"})

	rulesReloadFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "osac_metering_rules_reload_failures_total",
		Help: "Failed rule-reload checks, by reason. A disagreement also stops the service.",
	}, []string{"reason"})

	rulesLoaded = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "osac_metering_rules_loaded",
		Help: "Rule revisions currently loaded by the metering service.",
	})
)

// RoundSucceeded records a round-loop tick that completed without error. A nil error
// includes the ticks that deliberately did nothing: the loop is alive either way, which is
// what the gauge reports.
func RoundSucceeded(tenant string, at time.Time) {
	lastRoundTime.WithLabelValues(tenant).Set(float64(at.Unix()))
}

// RoundFailed counts a round attempt that returned an error.
func RoundFailed(tenant string) {
	roundFailures.WithLabelValues(tenant).Inc()
}

// OutboxDepth records how many of the tenant's heartbeats are waiting for the relay.
func OutboxDepth(tenant string, depth int64) {
	outboxDepth.WithLabelValues(tenant).Set(float64(depth))
}

// EventsSkipped counts event-topic records the fold skipped, by reason.
func EventsSkipped(tenant, reason string, count int64) {
	if count <= 0 {
		return
	}
	eventsSkipped.WithLabelValues(tenant, reason).Add(float64(count))
}

// RulesReloadFailed counts a failed rule-reload check, by reason.
func RulesReloadFailed(reason string) {
	rulesReloadFailures.WithLabelValues(reason).Inc()
}

// DeleteTenantHealthMetrics drops a gone tenant's per-tenant series. Without
// this, the stalled-round alert keeps firing for a tenant the service no
// longer serves.
func DeleteTenantHealthMetrics(tenant string) {
	lastRoundTime.DeleteLabelValues(tenant)
	roundFailures.DeleteLabelValues(tenant)
	outboxDepth.DeleteLabelValues(tenant)
}

// RulesLoaded records how many rule revisions the service evaluates.
func RulesLoaded(count int) {
	rulesLoaded.Set(float64(count))
}
