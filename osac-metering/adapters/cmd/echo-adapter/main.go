/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// echo-adapter is a test binary that consumes metering events from Kafka
// and exposes them via an HTTP query API for E2E test assertions. It
// exercises the full internal/runner.Runner lifecycle (dedup, out-of-order
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
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-logr/stdr"

	"github.com/IBM/sarama"

	"github.com/osac-project/osac-metering/adapters/echo"
	"github.com/osac-project/osac-metering/adapters/internal/envutil"
	"github.com/osac-project/osac-metering/adapters/internal/kafka"
	"github.com/osac-project/osac-metering/adapters/internal/runner"
)

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

	bufferSize := echo.DefaultMaxEvents
	if v := os.Getenv("ECHO_BUFFER_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			log.Fatalf("invalid ECHO_BUFFER_SIZE %q: must be a positive integer", v)
		}
		bufferSize = n
	}

	logger := stdr.New(log.New(os.Stderr, "", log.LstdFlags))

	kafkaCfg := kafka.KafkaConfigFromEnv()

	// The DLQ is mandatory: the adapter refuses to start without one, so a rejected
	// heartbeat is never dropped. Each rejection goes to the tenant's dead-letter topic.
	dlq, err := runner.NewPerTenantDLQProducer(brokers, kafkaCfg)
	if err != nil {
		log.Fatalf("creating DLQ producer: %v", err)
	}
	defer func() {
		if err := dlq.Close(); err != nil {
			log.Printf("DLQ producer close failed: %v", err)
		}
	}()

	// Discover every tenant's heartbeat topic, like the provider adapters do. The consumer
	// group shares this client: the runner closes only the group, so the client stays open
	// for the defer below to close.
	sc, err := kafka.NewConsumerConfig(kafkaCfg)
	if err != nil {
		log.Fatalf("creating kafka client config: %v", err)
	}
	echoClient, err := sarama.NewClient(kafka.SplitAndTrimBrokers(brokers, ","), sc)
	if err != nil {
		log.Fatalf("creating kafka client: %v", err)
	}
	defer func() { _ = echoClient.Close() }()

	consumerGroup, err := sarama.NewConsumerGroupFromClient(group, echoClient)
	if err != nil {
		log.Fatalf("creating consumer group: %v", err)
	}

	store := echo.NewEventStore(bufferSize)
	adapter := echo.NewAdapter(store)
	r := runner.NewRunner(adapter, runner.RunnerConfig{
		FlushInterval: flushInterval,
		TopicsProvider: func(ctx context.Context) ([]string, error) {
			return kafka.ConsumeTopics(echoClient)
		},
	}, logger, runner.WithDLQ(dlq))

	// Serve metrics, health, and event query endpoints.
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", r.MetricsHandler())
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			if err := adapter.HealthCheck(r.Context()); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("GET /events", store.HandleEvents)
		mux.HandleFunc("DELETE /events", store.HandleDeleteEvents)
		mux.HandleFunc("GET /events/count", store.HandleCount)
		mux.HandleFunc("GET /events/{id}", store.HandleEventByID)
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

	log.Printf("starting echo adapter: broker_count=%d group=%s flush=%s",
		len(strings.Split(brokers, ",")), group, flushInterval)

	if err := r.Run(ctx, consumerGroup); err != nil {
		log.Fatalf("runner error: %v", err)
	}

	log.Print("echo adapter shut down cleanly")
}
