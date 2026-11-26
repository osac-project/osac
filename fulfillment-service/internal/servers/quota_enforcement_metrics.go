/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"errors"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// quotaMetrics holds the quota enforcement metrics: refused requests per dimension, the time
// the check takes, the time a request waits for the tenant lock, and the number of warnings
// written to the outbox. A nil quotaMetrics records nothing, for servers built without a
// registerer.
type quotaMetrics struct {
	refused         *prometheus.CounterVec
	checkTime       prometheus.Histogram
	lockWait        prometheus.Histogram
	warningsWritten prometheus.Counter
}

// newQuotaMetrics registers the metrics. Servers that share a registerer share the collectors:
// a metric already registered is reused rather than registered again.
func newQuotaMetrics(registerer prometheus.Registerer) (result *quotaMetrics, err error) {
	if registerer == nil {
		return nil, nil
	}
	refused, err := registerOrReuse(registerer, prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: "quota",
			Name:      "refused_requests_total",
			Help:      "Number of requests refused by the quota check, per dimension.",
		},
		[]string{"dimension"},
	))
	if err != nil {
		return nil, fmt.Errorf("failed to register the quota refused requests metric: %w", err)
	}
	checkTime, err := registerOrReuse(registerer, prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Subsystem: "quota",
			Name:      "check_duration_seconds",
			Help:      "Duration of the quota check in seconds.",
			Buckets:   prometheus.ExponentialBuckets(0.001, 2.5, 10),
		},
	))
	if err != nil {
		return nil, fmt.Errorf("failed to register the quota check duration metric: %w", err)
	}
	lockWait, err := registerOrReuse(registerer, prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Subsystem: "quota",
			Name:      "tenant_lock_wait_seconds",
			Help:      "Duration a request waits for its tenant's row lock in seconds.",
			Buckets:   prometheus.ExponentialBuckets(0.001, 2.5, 10),
		},
	))
	if err != nil {
		return nil, fmt.Errorf("failed to register the quota tenant lock wait metric: %w", err)
	}
	// A counter of the warnings written, not a gauge of the warnings waiting: the row a
	// request writes is invisible until its transaction commits, and nothing drains the
	// outbox until the publisher (OSAC-75) exists, so this counts writes and claims nothing
	// about the table's depth.
	warningsWritten, err := registerOrReuse(registerer, prometheus.NewCounter(
		prometheus.CounterOpts{
			Subsystem: "quota",
			Name:      "warnings_written_total",
			Help:      "Number of quota warnings written to the outbox.",
		},
	))
	if err != nil {
		return nil, fmt.Errorf("failed to register the quota warnings metric: %w", err)
	}
	return &quotaMetrics{
		refused:         refused,
		checkTime:       checkTime,
		lockWait:        lockWait,
		warningsWritten: warningsWritten,
	}, nil
}

// recordRefusal counts one refused request of the dimension.
func (m *quotaMetrics) recordRefusal(dimension string) {
	if m == nil {
		return
	}
	m.refused.WithLabelValues(dimension).Inc()
}

// observeCheck records the duration of one quota check.
func (m *quotaMetrics) observeCheck(duration time.Duration) {
	if m == nil {
		return
	}
	m.checkTime.Observe(duration.Seconds())
}

// observeLockWait records the duration of one tenant lock wait.
func (m *quotaMetrics) observeLockWait(duration time.Duration) {
	if m == nil {
		return
	}
	m.lockWait.Observe(duration.Seconds())
}

// recordOutboxInsert counts one warning written to the outbox.
func (m *quotaMetrics) recordOutboxInsert() {
	if m == nil {
		return
	}
	m.warningsWritten.Inc()
}

// registerOrReuse registers the collector, or reuses the one already
// registered. The enforcer is built once per server, so the second build of
// the same server finds its collectors already present.
func registerOrReuse[T prometheus.Collector](registerer prometheus.Registerer, collector T) (T, error) {
	if err := registerer.Register(collector); err != nil {
		var already prometheus.AlreadyRegisteredError
		if ok := errors.As(err, &already); ok {
			existing, ok := already.ExistingCollector.(T)
			if !ok {
				var zero T
				return zero, fmt.Errorf(
					"existing collector has unexpected type %T", already.ExistingCollector,
				)
			}
			return existing, nil
		}
		return collector, err
	}
	return collector, nil
}
