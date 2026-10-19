/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package billingassignment

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
)

type assignmentSyncMetrics struct {
	failures *prometheus.CounterVec
}

func newAssignmentSyncMetrics(registerer prometheus.Registerer) *assignmentSyncMetrics {
	if registerer == nil {
		return nil
	}
	failures := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "osac_billing_assignment_sync_failures_total",
		Help: "Provider synchronization failures for tenant billing assignments, by stable reason code.",
	}, []string{"reason"})
	if err := registerer.Register(failures); err != nil {
		var alreadyRegistered prometheus.AlreadyRegisteredError
		if errors.As(err, &alreadyRegistered) {
			if existing, ok := alreadyRegistered.ExistingCollector.(*prometheus.CounterVec); ok {
				failures = existing
			} else {
				return nil
			}
		} else {
			return nil
		}
	}
	return &assignmentSyncMetrics{failures: failures}
}

func (m *assignmentSyncMetrics) recordFailure(reason string) {
	if m == nil || m.failures == nil {
		return
	}
	m.failures.WithLabelValues(reason).Inc()
}
