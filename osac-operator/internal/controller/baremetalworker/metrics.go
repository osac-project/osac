/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalworker

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	metricTenantLabel       = "tenant"
	metricWorkerTypeLabel   = "worker_type"
	metricInstanceTypeLabel = "instance_type"

	// workerTypeBareMetal is the worker_type label value for workers this controller manages.
	// The metric names (osac_caas_worker_*) are shared with future compute (VM) worker flows;
	// worker_type is what disambiguates the two, so it is emitted even though every series in
	// this controller is bare-metal.
	workerTypeBareMetal = "bare_metal"

	// tenantAnnotationKey associates a ClusterOrder with its tenant. The canonical
	// definition is osacTenantKey ("osac.openshift.io/tenant") in the parent controller
	// package (tenant_names.go); it is duplicated here because that constant is unexported
	// and this is a subpackage.
	tenantAnnotationKey = "osac.openshift.io/tenant"
)

var metricLabels = []string{metricTenantLabel, metricWorkerTypeLabel, metricInstanceTypeLabel}

// Worker metrics for fleet-health monitoring and alerting (design §Observability and Monitoring).
// All are aggregated by tenant, worker_type, and instance_type — never per-ClusterOrder, which
// would be unbounded. Per-cluster detail lives in Kubernetes events and status fields instead.
//
//   - desired/ready are gauges (levels that move both directions): dashboards, the fulfillment
//     ratio (ready/desired), and the silent-stall alert (desired-ready > 0 sustained). They are
//     recomputed from the full ClusterOrder set on each reconcile via updateWorkerGauges
//     (Reset + Set), mirroring bare-metal-fulfillment-operator's bcmHostsAvailable.
//   - provisioningFailures is a counter (a failure is a cumulative event, not a level): the
//     rate-alertable failure signal, robust to workers being retried or deleted afterward.
//   - the duration histograms anchor on WorkerStatus.CreationTimestamp, which survives retries.
var (
	workerDesired = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "osac_caas_worker_desired",
			Help: "Desired number of CaaS workers, by tenant, worker type, and instance type.",
		},
		metricLabels,
	)

	workerReady = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "osac_caas_worker_ready",
			Help: "Ready CaaS workers, by tenant, worker type, and instance type.",
		},
		metricLabels,
	)

	workerProvisioningFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "osac_caas_worker_provisioning_failures_total",
			Help: "Total CaaS worker provisioning failures, by tenant, worker type, and instance type.",
		},
		metricLabels,
	)

	workerProvisioningDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "osac_caas_worker_provisioning_duration_seconds",
			Help:    "Time from CaaS worker creation to Ready, in seconds (includes retries).",
			Buckets: prometheus.ExponentialBuckets(30, 2, 8), // 30s .. ~64m
		},
		metricLabels,
	)

	workerCorrelationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "osac_caas_worker_correlation_duration_seconds",
			Help:    "Time from CaaS worker creation to agent correlation (Binding), in seconds.",
			Buckets: prometheus.ExponentialBuckets(30, 2, 8), // 30s .. ~64m
		},
		metricLabels,
	)
)

func init() {
	metrics.Registry.MustRegister(
		workerDesired,
		workerReady,
		workerProvisioningFailures,
		workerProvisioningDuration,
		workerCorrelationDuration,
	)
}

// tenantOf returns the tenant a ClusterOrder belongs to, read from its tenant annotation.
func tenantOf(co *v1alpha1.ClusterOrder) string {
	return co.GetAnnotations()[tenantAnnotationKey]
}

// updateWorkerGauges recomputes the desired/ready gauges from the full set of ClusterOrders.
// Reset clears stale (tenant, worker_type, instance_type) series — e.g. for deleted clusters —
// before the current counts are set, mirroring bcmHostsAvailable. Failures are tracked
// separately as a counter (see observeProvisioningFailure), not recomputed here.
func updateWorkerGauges(orders []v1alpha1.ClusterOrder) {
	workerDesired.Reset()
	workerReady.Reset()

	type key struct{ tenant, instanceType string }
	type counts struct{ desired, ready float64 }
	agg := map[key]*counts{}
	for i := range orders {
		tenant := tenantOf(&orders[i])
		for _, w := range orders[i].Status.Workers {
			k := key{tenant: tenant, instanceType: w.InstanceType}
			c := agg[k]
			if c == nil {
				c = &counts{}
				agg[k] = c
			}
			c.desired++
			if w.Phase == workerPhaseReady {
				c.ready++
			}
		}
	}

	for k, c := range agg {
		workerDesired.WithLabelValues(k.tenant, workerTypeBareMetal, k.instanceType).Set(c.desired)
		workerReady.WithLabelValues(k.tenant, workerTypeBareMetal, k.instanceType).Set(c.ready)
	}
}

// observeProvisioningFailure records one worker provisioning failure. Call it on each transition
// into the Failed phase (each failed attempt counts once; retries that fail again count again).
func observeProvisioningFailure(tenant string, w v1alpha1.WorkerStatus) {
	workerProvisioningFailures.WithLabelValues(tenant, workerTypeBareMetal, w.InstanceType).Inc()
}

func observeProvisioningDuration(tenant string, w v1alpha1.WorkerStatus) {
	workerProvisioningDuration.WithLabelValues(tenant, workerTypeBareMetal, w.InstanceType).
		Observe(time.Since(w.CreationTimestamp.Time).Seconds())
}

func observeCorrelationDuration(tenant string, w v1alpha1.WorkerStatus) {
	workerCorrelationDuration.WithLabelValues(tenant, workerTypeBareMetal, w.InstanceType).
		Observe(time.Since(w.CreationTimestamp.Time).Seconds())
}
