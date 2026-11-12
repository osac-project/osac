/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package metrics

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
)

// WorkloadClusterMetrics contains the metrics emitted by WorkloadCluster
// registration and observation.
type WorkloadClusterMetrics struct {
	Registrations        *prometheus.CounterVec
	Available            prometheus.Gauge
	Unavailable          prometheus.Gauge
	HubWatchErrors       *prometheus.CounterVec
	ConditionTransitions *prometheus.CounterVec
}

// NewWorkloadClusterMetrics registers or reuses the WorkloadCluster metrics.
func NewWorkloadClusterMetrics(registerer prometheus.Registerer) (*WorkloadClusterMetrics, error) {
	if registerer == nil {
		registerer = prometheus.DefaultRegisterer
	}
	result := &WorkloadClusterMetrics{}
	var err error
	if result.Registrations, err = registerCounterVec(registerer, prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "osac_workload_cluster_registrations_total",
			Help: "Total WorkloadCluster registration attempts.",
		}, []string{"result"})); err != nil {
		return nil, err
	}
	if result.Available, err = registerGauge(registerer, prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "osac_workload_cluster_available",
		Help: "Number of WorkloadClusters with Available=True.",
	})); err != nil {
		return nil, err
	}
	if result.Unavailable, err = registerGauge(registerer, prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "osac_workload_cluster_unavailable",
		Help: "Number of WorkloadClusters with Available=False.",
	})); err != nil {
		return nil, err
	}
	if result.HubWatchErrors, err = registerCounterVec(registerer, prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "osac_workload_cluster_hub_watch_errors_total",
			Help: "Total WorkloadCluster Hub watch connection errors.",
		}, []string{"hub_id"})); err != nil {
		return nil, err
	}
	if result.ConditionTransitions, err = registerCounterVec(registerer, prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "osac_workload_cluster_condition_transitions_total",
			Help: "Total WorkloadCluster condition status transitions.",
		}, []string{"condition", "from_status", "to_status"})); err != nil {
		return nil, err
	}
	return result, nil
}

func registerCounterVec(registerer prometheus.Registerer, collector *prometheus.CounterVec) (*prometheus.CounterVec, error) {
	if err := registerer.Register(collector); err != nil {
		var alreadyRegistered prometheus.AlreadyRegisteredError
		if !errors.As(err, &alreadyRegistered) {
			return nil, err
		}
		existing, ok := alreadyRegistered.ExistingCollector.(*prometheus.CounterVec)
		if !ok {
			return nil, errors.New("existing WorkloadCluster metric has an incompatible type")
		}
		return existing, nil
	}
	return collector, nil
}

func registerGauge(registerer prometheus.Registerer, collector prometheus.Gauge) (prometheus.Gauge, error) {
	if err := registerer.Register(collector); err != nil {
		var alreadyRegistered prometheus.AlreadyRegisteredError
		if !errors.As(err, &alreadyRegistered) {
			return nil, err
		}
		existing, ok := alreadyRegistered.ExistingCollector.(prometheus.Gauge)
		if !ok {
			return nil, errors.New("existing WorkloadCluster metric has an incompatible type")
		}
		return existing, nil
	}
	return collector, nil
}
