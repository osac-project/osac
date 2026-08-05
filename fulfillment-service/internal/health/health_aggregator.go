/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package health

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
)

// AggregatorBuilder contains the data and logic needed to create an aggregator. Don't create instances of this
// directly, use the NewAggregator function instead.
type AggregatorBuilder struct {
	logger *slog.Logger
	server *health.Server
}

// Aggregator tracks the health status of multiple components and reports the aggregated status to a gRPC health
// server. It only reports 'SERVING' when all registered components are healthy.
type Aggregator struct {
	logger   *slog.Logger
	server   *health.Server
	lock     sync.Mutex
	statuses map[string]healthv1.HealthCheckResponse_ServingStatus
}

// NewAggregator creates a builder that can then be used to configure and create an aggregator.
func NewAggregator() *AggregatorBuilder {
	return &AggregatorBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *AggregatorBuilder) SetLogger(value *slog.Logger) *AggregatorBuilder {
	b.logger = value
	return b
}

// SetServer sets the health server that will be used to report the aggregated health status. This is mandatory.
func (b *AggregatorBuilder) SetServer(value *health.Server) *AggregatorBuilder {
	b.server = value
	return b
}

// Build uses the data stored in the builder to create a new aggregator.
func (b *AggregatorBuilder) Build() (result *Aggregator, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.server == nil {
		err = errors.New("server is mandatory")
		return
	}

	// Create and populate the object:
	result = &Aggregator{
		logger:   b.logger,
		server:   b.server,
		statuses: make(map[string]healthv1.HealthCheckResponse_ServingStatus),
	}

	// Initially set the health server to 'NOT_SERVING' until all components report healthy:
	b.server.SetServingStatus("", healthv1.HealthCheckResponse_NOT_SERVING)

	return
}

// Report reports the health status for the given component. The aggregated status will be 'SERVING' only when all
// registered components report 'SERVING', otherwise it will be 'NOT_SERVING'.
func (a *Aggregator) Report(ctx context.Context, name string, status healthv1.HealthCheckResponse_ServingStatus) {
	a.lock.Lock()
	defer a.lock.Unlock()

	// Update the status of the component:
	a.statuses[name] = status

	// Count the number of components that aren't serving:
	count := 0
	for _, s := range a.statuses {
		if s != healthv1.HealthCheckResponse_SERVING {
			count++
		}
	}

	// Update the health server:
	var aggregated healthv1.HealthCheckResponse_ServingStatus
	logger := a.logger.With(
		slog.Int("total", len(a.statuses)),
		slog.Int("count", count),
		slog.Any("statuses", a.statuses),
	)
	if count == 0 {
		aggregated = healthv1.HealthCheckResponse_SERVING
		logger.DebugContext(
			ctx,
			"All components are healthy",
			slog.String("status", aggregated.String()),
		)
	} else {
		aggregated = healthv1.HealthCheckResponse_NOT_SERVING
		logger.WarnContext(
			ctx,
			"Some components are unhealthy",
			slog.String("status", aggregated.String()),
		)
	}
	a.server.SetServingStatus("", aggregated)
}
