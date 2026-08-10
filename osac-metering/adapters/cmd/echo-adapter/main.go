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
//	export KAFKA_TOPICS="osac.metering.events"
//	go run ./cmd/echo-adapter/
//
// Optional TLS/SASL (for cluster-deployed Kafka):
//
//	export KAFKA_TLS_ENABLED="true"
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
	brokers := os.Getenv("KAFKA_BROKERS")
	if brokers == "" {
		log.Fatal("KAFKA_BROKERS is required (comma-separated broker list)")
	}

	topicsEnv := os.Getenv("KAFKA_TOPICS")
	if topicsEnv == "" {
		topicsEnv = "osac.metering.events"
	}
	topics := strings.Split(topicsEnv, ",")
	for i := range topics {
		topics[i] = strings.TrimSpace(topics[i])
	}

	group := os.Getenv("KAFKA_CONSUMER_GROUP")
	if group == "" {
		group = "echo-adapter-smoke-test"
	}

	flushInterval := 5 * time.Second
	if v := os.Getenv("FLUSH_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			log.Fatalf("invalid FLUSH_INTERVAL %q: %v", v, err)
		}
		flushInterval = d
	}

	metricsAddr := os.Getenv("METRICS_ADDR")
	if metricsAddr == "" {
		metricsAddr = ":2112"
	}

	bufferSize := defaultMaxEvents
	if v := os.Getenv("ECHO_BUFFER_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			log.Fatalf("invalid ECHO_BUFFER_SIZE %q: must be a positive integer", v)
		}
		bufferSize = n
	}

	logger := stdr.New(log.New(os.Stderr, "", log.LstdFlags))

	store := newEventStore(bufferSize)
	adapter := &echoAdapter{store: store}
	runner := adapters.NewRunner(adapter, adapters.RunnerConfig{
		Brokers:       brokers,
		ConsumerGroup: group,
		Topics:        topics,
		FlushInterval: flushInterval,
		Kafka: adapters.KafkaConfig{
			TLSEnabled:   os.Getenv("KAFKA_TLS_ENABLED") == "true",
			TLSCACert:    os.Getenv("KAFKA_TLS_CA_CERT"),
			SASLUser:     os.Getenv("KAFKA_SASL_USERNAME"),
			SASLPassFile: os.Getenv("KAFKA_SASL_PASSWORD_FILE"),
		},
	}, logger)

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
		log.Printf("HTTP server listening on %s", metricsAddr)
		if err := http.ListenAndServe(metricsAddr, mux); err != nil {
			log.Printf("metrics server error: %v", err)
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Printf("starting echo adapter: broker_count=%d topics=%v group=%s flush=%s",
		len(strings.Split(brokers, ",")), topics, group, flushInterval)

	if err := runner.Run(ctx); err != nil {
		log.Fatalf("runner error: %v", err)
	}

	log.Print("echo adapter shut down cleanly")
}
