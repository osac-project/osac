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

package m360

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type requestMetrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func newRequestMetrics(registerer prometheus.Registerer) *requestMetrics {
	if registerer == nil {
		return nil
	}
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "osac_billing_provider_requests_total",
		Help: "Requests made by the M360 billing adapter, by operation and outcome.",
	}, []string{"provider", "operation", "outcome"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "osac_billing_provider_request_duration_seconds",
		Help:    "Time spent making requests through a billing provider adapter.",
		Buckets: prometheus.DefBuckets,
	}, []string{"provider", "operation"})
	registeredRequests := registerRequestCounter(registerer, requests)
	registeredDuration := registerRequestHistogram(registerer, duration)
	if registeredRequests == nil || registeredDuration == nil {
		return nil
	}
	return &requestMetrics{
		requests: registeredRequests,
		duration: registeredDuration,
	}
}

func (m *requestMetrics) record(operation string, duration time.Duration, err error) {
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	m.requests.WithLabelValues("m360", operation, outcome).Inc()
	m.duration.WithLabelValues("m360", operation).Observe(duration.Seconds())
}

func registerRequestCounter(registerer prometheus.Registerer, collector *prometheus.CounterVec) *prometheus.CounterVec {
	if err := registerer.Register(collector); err != nil {
		var alreadyRegistered prometheus.AlreadyRegisteredError
		if errors.As(err, &alreadyRegistered) {
			if existing, ok := alreadyRegistered.ExistingCollector.(*prometheus.CounterVec); ok {
				return existing
			}
		}
		return nil
	}
	return collector
}

func registerRequestHistogram(registerer prometheus.Registerer, collector *prometheus.HistogramVec) *prometheus.HistogramVec {
	if err := registerer.Register(collector); err != nil {
		var alreadyRegistered prometheus.AlreadyRegisteredError
		if errors.As(err, &alreadyRegistered) {
			if existing, ok := alreadyRegistered.ExistingCollector.(*prometheus.HistogramVec); ok {
				return existing
			}
		}
		return nil
	}
	return collector
}

func m360Operation(endpoint *url.URL) string {
	if endpoint == nil {
		return "other"
	}
	segments := strings.Split(strings.Trim(endpoint.Path, "/"), "/")
	for index, segment := range segments {
		if segment != "external" || index+1 >= len(segments) {
			continue
		}
		switch segments[index+1] {
		case "accounts", "contracts", "invoices", "services":
			return segments[index+1]
		case "rated-transactions":
			return "rated_transactions"
		default:
			return "other"
		}
	}
	return "other"
}
