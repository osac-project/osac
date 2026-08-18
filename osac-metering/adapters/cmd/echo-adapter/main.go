/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// echo-adapter is a test binary that consumes metering events from Kafka
// and exposes them via an HTTP query API for E2E test assertions. It
// exercises the full adapters.Runner lifecycle (dedup, out-of-order
// detection, retry, flush, offset commit) without connecting to a real
// metering provider.
//
// Events are logged to stdout and stored in a bounded ring buffer
// queryable via GET /events and GET /events/count.
//
// Usage:
//
//	export KAFKA_BROKERS="localhost:9092"
//	go run ./cmd/echo-adapter/
//
// TLS is enabled by default. For local development without TLS:
//
//	export KAFKA_TLS_ENABLED="false"
//
// Optional TLS/SASL (for cluster-deployed Kafka):
//
//	export KAFKA_TLS_CA_CERT="/path/to/ca.crt"
//	export KAFKA_SASL_USERNAME="metering-user"
//	export KAFKA_SASL_PASSWORD_FILE="/path/to/password"
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-logr/stdr"

	"github.com/osac-project/osac-metering/adapters"
	"github.com/osac-project/osac-metering/adapters/envutil"
)

// echoAdapter logs every event to stdout, stores it in a ring buffer
// for HTTP queries, and counts submissions.
type echoAdapter struct {
	store     *eventStore
	submitted atomic.Int64
	flushed   atomic.Int64
}

func (a *echoAdapter) Name() string { return "echo" }

func (a *echoAdapter) Submit(_ context.Context, event adapters.MeteringEvent) error {
	fmt.Printf("[SUBMIT] id=%-36s type=%-30s topic=%-30s partition=%d offset=%d\n",
		event.CloudEvent.ID(),
		event.CloudEvent.Type(),
		event.Topic,
		event.Partition,
		event.Offset,
	)
	a.store.add(event)
	a.submitted.Add(1)
	return nil
}

func (a *echoAdapter) Flush(_ context.Context) (adapters.SubmitResult, error) {
	n := a.flushed.Add(1)
	total := a.submitted.Load()
	fmt.Printf("[FLUSH]  #%d — %d events submitted so far\n", n, total)
	return adapters.SubmitResult{Idempotent: true}, nil
}

func (a *echoAdapter) HealthCheck(_ context.Context) error { return nil }

func (a *echoAdapter) Close() error {
	fmt.Printf("[CLOSE]  total events submitted: %d, total flushes: %d\n",
		a.submitted.Load(), a.flushed.Load())
	return nil
}

func main() {
	brokers := envutil.RequireEnv("KAFKA_BROKERS")

	group := envutil.EnvOrDefault("KAFKA_CONSUMER_GROUP", "echo-adapter-smoke-test")

	flushInterval := 5 * time.Second
	if v := os.Getenv("FLUSH_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			log.Fatalf("invalid FLUSH_INTERVAL %q: %v", v, err)
		}
		flushInterval = d
	}

	metricsAddr := envutil.EnvOrDefault("METRICS_ADDR", ":2112")

	bufferSize := defaultMaxEvents
	if v := os.Getenv("ECHO_BUFFER_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			log.Fatalf("invalid ECHO_BUFFER_SIZE %q: must be a positive integer", v)
		}
		bufferSize = n
	}

	logger := stdr.New(log.New(os.Stderr, "", log.LstdFlags))

	kafkaCfg := adapters.KafkaConfigFromEnv()

	dlqOpt, dlqClose, err := adapters.DLQOptionFromEnv(brokers, kafkaCfg)
	if err != nil {
		log.Fatalf("setting up DLQ: %v", err)
	}
	defer func() {
		if err := dlqClose(); err != nil {
			log.Printf("DLQ producer close failed: %v", err)
		}
	}()
	var opts []adapters.RunnerOption
	if dlqOpt != nil {
		opts = append(opts, dlqOpt)
		log.Printf("DLQ enabled: topic=%s", envutil.EnvOrDefault("DLQ_TOPIC", adapters.TopicDLQ))
	}

	store := newEventStore(bufferSize)
	adapter := &echoAdapter{store: store}
	runner := adapters.NewRunner(adapter, adapters.RunnerConfig{
		Brokers:       brokers,
		ConsumerGroup: group,
		Topics:        adapters.AllTopics,
		FlushInterval: flushInterval,
		Kafka:         kafkaCfg,
	}, logger, opts...)

	// Serve metrics, health, and event query endpoints.
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", runner.MetricsHandler())
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			if err := adapter.HealthCheck(r.Context()); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("GET /events", store.handleEvents)
		mux.HandleFunc("DELETE /events", store.handleDeleteEvents)
		mux.HandleFunc("GET /events/count", store.handleCount)
		mux.HandleFunc("GET /events/{id}", store.handleEventByID)
		httpServer := &http.Server{
			Addr:              metricsAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		log.Printf("HTTP server listening on %s", metricsAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server error: %v", err)
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Printf("starting echo adapter: broker_count=%d topics=%v group=%s flush=%s",
		len(strings.Split(brokers, ",")), adapters.AllTopics, group, flushInterval)

	if err := runner.Run(ctx); err != nil {
		log.Fatalf("runner error: %v", err)
	}

	log.Print("echo adapter shut down cleanly")
}
