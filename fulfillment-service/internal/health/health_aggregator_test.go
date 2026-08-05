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

	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Aggregator", func() {
	Describe("Build", func() {
		It("Fails if logger is not set", func() {
			_, err := NewAggregator().
				SetServer(health.NewServer()).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger"))
		})

		It("Fails if server is not set", func() {
			_, err := NewAggregator().
				SetLogger(logger).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("server"))
		})

		It("Succeeds with all required parameters", func() {
			aggregator, err := NewAggregator().
				SetLogger(logger).
				SetServer(health.NewServer()).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(aggregator).ToNot(BeNil())
		})
	})

	Describe("Report", func() {
		var (
			server     *health.Server
			aggregator *Aggregator
		)

		BeforeEach(func() {
			var err error
			server = health.NewServer()
			aggregator, err = NewAggregator().
				SetLogger(logger).
				SetServer(server).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		var getStatus = func() healthv1.HealthCheckResponse_ServingStatus {
			ctx := context.Background()
			resp, err := server.Check(ctx, &healthv1.HealthCheckRequest{})
			if err != nil {
				return healthv1.HealthCheckResponse_SERVICE_UNKNOWN
			}
			return resp.Status
		}

		It("Reports 'NOT_SERVING' initially", func() {
			status := getStatus()
			Expect(status).To(Equal(healthv1.HealthCheckResponse_NOT_SERVING))
		})

		It("Reports 'SERVING' when single component is serving", func() {
			ctx := context.Background()
			aggregator.Report(ctx, "component-a", healthv1.HealthCheckResponse_SERVING)
			status := getStatus()
			Expect(status).To(Equal(healthv1.HealthCheckResponse_SERVING))
		})

		It("Reports 'NOT_SERVING' when single component is not serving", func() {
			ctx := context.Background()
			aggregator.Report(ctx, "component-a", healthv1.HealthCheckResponse_NOT_SERVING)
			status := getStatus()
			Expect(status).To(Equal(healthv1.HealthCheckResponse_NOT_SERVING))
		})

		It("Reports 'SERVING' when all components are serving", func() {
			ctx := context.Background()
			aggregator.Report(ctx, "component-a", healthv1.HealthCheckResponse_SERVING)
			aggregator.Report(ctx, "component-b", healthv1.HealthCheckResponse_SERVING)
			aggregator.Report(ctx, "component-c", healthv1.HealthCheckResponse_SERVING)
			status := getStatus()
			Expect(status).To(Equal(healthv1.HealthCheckResponse_SERVING))
		})

		It("Reports 'NOT_SERVING' when one component becomes not serving", func() {
			ctx := context.Background()
			aggregator.Report(ctx, "component-a", healthv1.HealthCheckResponse_SERVING)
			aggregator.Report(ctx, "component-b", healthv1.HealthCheckResponse_SERVING)
			aggregator.Report(ctx, "component-c", healthv1.HealthCheckResponse_SERVING)
			aggregator.Report(ctx, "component-b", healthv1.HealthCheckResponse_NOT_SERVING)
			status := getStatus()
			Expect(status).To(Equal(healthv1.HealthCheckResponse_NOT_SERVING))
		})

		It("Reports 'SERVING' again when not serving component recovers", func() {
			ctx := context.Background()
			aggregator.Report(ctx, "component-a", healthv1.HealthCheckResponse_SERVING)
			aggregator.Report(ctx, "component-b", healthv1.HealthCheckResponse_SERVING)
			aggregator.Report(ctx, "component-b", healthv1.HealthCheckResponse_NOT_SERVING)
			aggregator.Report(ctx, "component-b", healthv1.HealthCheckResponse_SERVING)
			status := getStatus()
			Expect(status).To(Equal(healthv1.HealthCheckResponse_SERVING))
		})

		It("Reports 'NOT_SERVING' when any component reports not serving", func() {
			ctx := context.Background()
			aggregator.Report(ctx, "component-a", healthv1.HealthCheckResponse_SERVING)
			aggregator.Report(ctx, "component-b", healthv1.HealthCheckResponse_NOT_SERVING)
			status := getStatus()
			Expect(status).To(Equal(healthv1.HealthCheckResponse_NOT_SERVING))
		})
	})
})
