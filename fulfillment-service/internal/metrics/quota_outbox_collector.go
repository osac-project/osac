/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// This file contains a Prometheus collector that reports the depth of the quota outbox.

package metrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// quotaOutboxQueryTimeout bounds the count query of one scrape, so a stalled database delays a
// scrape by at most this long.
const quotaOutboxQueryTimeout = 5 * time.Second

// QuotaOutboxCollector reports the number of quota warnings waiting in the quota_outbox table.
// It counts at scrape time on the pool the process already has, so it opens no connections of
// its own and holds no value between scrapes.
type QuotaOutboxCollector struct {
	logger  *slog.Logger
	pool    *pgxpool.Pool
	waiting *prometheus.Desc
}

// QuotaOutboxCollectorBuilder contains the data and logic needed to build a new quota outbox
// collector.
type QuotaOutboxCollectorBuilder struct {
	logger     *slog.Logger
	pool       *pgxpool.Pool
	registerer prometheus.Registerer
}

// NewQuotaOutboxCollector creates a builder for a new quota outbox collector.
func NewQuotaOutboxCollector() *QuotaOutboxCollectorBuilder {
	return &QuotaOutboxCollectorBuilder{}
}

// SetLogger sets the logger. It is mandatory.
func (b *QuotaOutboxCollectorBuilder) SetLogger(value *slog.Logger) *QuotaOutboxCollectorBuilder {
	b.logger = value
	return b
}

// SetPool sets the database pool the collector counts on. It is mandatory.
func (b *QuotaOutboxCollectorBuilder) SetPool(value *pgxpool.Pool) *QuotaOutboxCollectorBuilder {
	b.pool = value
	return b
}

// SetRegisterer sets the Prometheus registerer the collector is registered with. It is
// mandatory.
func (b *QuotaOutboxCollectorBuilder) SetRegisterer(value prometheus.Registerer) *QuotaOutboxCollectorBuilder {
	b.registerer = value
	return b
}

// Build creates the collector and registers it. A collector already registered with the same
// registerer is reused, so building twice does not fail.
func (b *QuotaOutboxCollectorBuilder) Build() (*QuotaOutboxCollector, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.pool == nil {
		return nil, errors.New("pool is mandatory")
	}
	if b.registerer == nil {
		return nil, errors.New("registerer is mandatory")
	}
	result := &QuotaOutboxCollector{
		logger: b.logger,
		pool:   b.pool,
		waiting: prometheus.NewDesc(
			prometheus.BuildFQName("", "quota", "outbox_waiting"),
			"Number of quota warnings waiting in the outbox for the publisher.",
			nil, nil,
		),
	}
	err := b.registerer.Register(result)
	if err != nil {
		var registered prometheus.AlreadyRegisteredError
		if errors.As(err, &registered) {
			existing, ok := registered.ExistingCollector.(*QuotaOutboxCollector)
			if !ok {
				return nil, fmt.Errorf(
					"the quota outbox waiting metric is already registered as a '%T'",
					registered.ExistingCollector,
				)
			}
			return existing, nil
		}
		return nil, fmt.Errorf("failed to register the quota outbox waiting metric: %w", err)
	}
	return result, nil
}

// Describe sends the descriptor of the metric.
func (c *QuotaOutboxCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.waiting
}

// Collect counts the rows of the outbox and sends the result as a gauge. A failed query sends no
// sample, because a zero would claim an empty outbox.
func (c *QuotaOutboxCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), quotaOutboxQueryTimeout)
	defer cancel()
	var count int64
	err := c.pool.QueryRow(ctx, "select count(*) from quota_outbox").Scan(&count)
	if err != nil {
		c.logger.WarnContext(ctx, "Failed to count the quota outbox rows",
			slog.Any("error", err))
		return
	}
	ch <- prometheus.MustNewConstMetric(c.waiting, prometheus.GaugeValue, float64(count))
}
