// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Executable process health", func() {
	DescribeTable("accepts only a healthy existing endpoint", func(ctx SpecContext, status int) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		DeferCleanup(server.Close)

		err := CheckHealth(ctx, server.URL)
		if status == http.StatusOK {
			Expect(err).NotTo(HaveOccurred())
		} else {
			Expect(err).To(MatchError(ContainSubstring("503 Service Unavailable")))
		}
	}, Entry("healthy", http.StatusOK), Entry("unhealthy", http.StatusServiceUnavailable))

	It("honors caller cancellation", func(ctx SpecContext) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		Expect(errors.Is(CheckHealth(cancelled, "http://127.0.0.1:1/healthz"), context.Canceled)).To(BeTrue())
	})

	It("fails when the existing endpoint is unavailable", func(ctx SpecContext) {
		server := httptest.NewServer(http.NotFoundHandler())
		server.Close()
		Expect(CheckHealth(ctx, server.URL)).NotTo(Succeed())
	})

	It("bounds an unresponsive existing endpoint", func(ctx SpecContext) {
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			<-request.Context().Done()
		}))
		DeferCleanup(server.Close)

		started := time.Now()
		err := CheckHealth(ctx, server.URL)

		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
		Expect(time.Since(started)).To(BeNumerically("<", 4*time.Second))
	}, SpecTimeout(5*time.Second))
})
