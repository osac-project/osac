/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package runner

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type adapterMetrics struct {
	eventsSubmitted      *prometheus.CounterVec
	eventsFailed         *prometheus.CounterVec
	usageDeliveryLag     *prometheus.HistogramVec
	retryDuration        *prometheus.HistogramVec
	duplicatesSuppressed *prometheus.CounterVec
	outOfOrderEvents     *prometheus.CounterVec
	flushDuration        *prometheus.HistogramVec
	flushErrors          *prometheus.CounterVec
	eventsDropped        *prometheus.CounterVec
	dlqEventsTotal       *prometheus.CounterVec
	dlqSendErrors        *prometheus.CounterVec
	dlqDepth             *prometheus.GaugeVec
	dlqSize              *prometheus.CounterVec
	registry             *prometheus.Registry
}

func newAdapterMetrics() *adapterMetrics {
	reg := prometheus.NewRegistry()

	m := &adapterMetrics{
		eventsSubmitted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "osac_metering_adapter_events_submitted_total",
			Help: "Total events successfully submitted to the provider.",
		}, []string{"provider", "topic"}),
		eventsFailed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "osac_metering_adapter_events_failed_total",
			Help: "Total events that failed processing.",
		}, []string{"provider", "error_type"}),
		usageDeliveryLag: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "osac_metering_adapter_usage_delivery_lag_seconds",
			Help:    "Time from CloudEvent generation until successful provider submission.",
			Buckets: []float64{0.1, 1, 5, 15, 30, 60, 300, 900, 3600, 21600, 86400},
		}, []string{"provider"}),
		retryDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "osac_metering_adapter_retry_duration_seconds",
			Help:    "Duration of retry attempts for Submit calls.",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10),
		}, []string{"provider"}),
		duplicatesSuppressed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "osac_metering_adapter_duplicates_suppressed_total",
			Help: "Total duplicate events suppressed by the dedup cache.",
		}, []string{"provider"}),
		outOfOrderEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "osac_metering_adapter_out_of_order_events_total",
			Help: "Total out-of-order events detected.",
		}, []string{"provider"}),
		flushDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "osac_metering_adapter_flush_duration_seconds",
			Help:    "Duration of flush operations.",
			Buckets: prometheus.DefBuckets,
		}, []string{"provider"}),
		flushErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "osac_metering_adapter_flush_errors_total",
			Help: "Total flush operation failures.",
		}, []string{"provider"}),
		eventsDropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "osac_metering_adapter_events_dropped_total",
			Help: "Total events permanently dropped (no DLQ configured).",
		}, []string{"provider"}),
		dlqEventsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "osac_metering_adapter_dlq_events_total",
			Help: "Total events sent to the dead letter queue.",
		}, []string{"provider"}),
		dlqSendErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "osac_metering_adapter_dlq_send_errors_total",
			Help: "Total DLQ send failures.",
		}, []string{"provider"}),
		dlqDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "osac_metering_dlq_depth",
			Help: "Records currently retained in the DLQ topic (log occupancy, not consumer lag).",
		}, []string{"topic"}),
		dlqSize: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "osac_metering_adapter_dlq_bytes_total",
			Help: "Total payload bytes sent to the DLQ.",
		}, []string{"provider"}),
		registry: reg,
	}

	reg.MustRegister(
		m.eventsSubmitted, m.eventsFailed, m.usageDeliveryLag, m.retryDuration,
		m.duplicatesSuppressed, m.outOfOrderEvents,
		m.flushDuration, m.flushErrors,
		m.eventsDropped, m.dlqEventsTotal, m.dlqSendErrors,
		m.dlqDepth, m.dlqSize,
	)

	return m
}

func (m *adapterMetrics) handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
