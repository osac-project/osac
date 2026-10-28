/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// m360-adapter consumes OSAC metering CloudEvents from Kafka and
// forwards them to the Monetize360 (M360) Usage API via REST.
//
// Usage:
//
//	export KAFKA_BROKERS="localhost:9092"
//	export M360_API_URL="https://m360.example.com"
//	export M360_API_KEY_FILE="/path/to/api-key"
//	go run ./cmd/m360-adapter/
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/IBM/sarama"

	"github.com/go-logr/stdr"

	"github.com/osac-project/osac-metering/adapters/internal/envutil"
	"github.com/osac-project/osac-metering/adapters/internal/kafka"
	"github.com/osac-project/osac-metering/adapters/internal/runner"
	"github.com/osac-project/osac-metering/adapters/m360"
)

func main() {
	brokers := envutil.RequireEnv("KAFKA_BROKERS")
	m360URL := envutil.RequireEnv("M360_API_URL")
	apiKeyFile := envutil.RequireEnv("M360_API_KEY_FILE")

	apiKey := envutil.ReadFileOrFatal(apiKeyFile)
	apiVersion := envutil.EnvOrDefault("M360_API_VERSION", "v1")

	group := envutil.EnvOrDefault("KAFKA_CONSUMER_GROUP", "m360-adapter")

	var flushInterval time.Duration
	if v := os.Getenv("FLUSH_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			log.Fatalf("invalid FLUSH_INTERVAL %q: %v", v, err)
		}
		if d < 0 {
			log.Fatalf("FLUSH_INTERVAL must be non-negative, got %s", d)
		}
		flushInterval = d
	}

	metricsAddr := envutil.EnvOrDefault("METRICS_ADDR", ":2112")

	logger := stdr.New(log.New(os.Stderr, "", log.LstdFlags))

	kafkaCfg := kafka.KafkaConfigFromEnv()

	// The DLQ is mandatory: the adapter refuses to start without one, so a rejected
	// heartbeat is never dropped. Each rejection goes to the tenant's dead-letter
	// topic.
	dlq, err := runner.NewPerTenantDLQProducer(brokers, kafkaCfg)
	if err != nil {
		log.Fatalf("creating DLQ producer: %v", err)
	}
	defer func() {
		if err := dlq.Close(); err != nil {
			log.Printf("DLQ producer close failed: %v", err)
		}
	}()

	// Every metered tenant's heartbeat topic plus the shared MaaS inference topic,
	// rediscovered as tenants come and go. The consumer group shares this client: the
	// runner closes only the group, so the client stays open for the defer below to close.
	discoveryConfig, err := kafka.NewConsumerConfig(kafkaCfg)
	if err != nil {
		log.Fatalf("creating kafka client config: %v", err)
	}
	discoveryClient, err := sarama.NewClient(kafka.SplitAndTrimBrokers(brokers, ","), discoveryConfig)
	if err != nil {
		log.Fatalf("creating kafka client: %v", err)
	}
	defer func() { _ = discoveryClient.Close() }()
	consumerGroup, err := sarama.NewConsumerGroupFromClient(group, discoveryClient)
	if err != nil {
		log.Fatalf("creating consumer group: %v", err)
	}

	adapter := m360.NewAdapter(m360URL, apiVersion, apiKey, logger)
	r := runner.NewRunner(adapter, runner.RunnerConfig{
		FlushInterval: flushInterval,
		TopicsProvider: func(ctx context.Context) ([]string, error) {
			return kafka.ConsumeTopics(discoveryClient)
		},
	}, logger, runner.WithDLQ(dlq))

	mux := http.NewServeMux()
	mux.Handle("/metrics", r.MetricsHandler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := adapter.HealthCheck(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	httpServer := &http.Server{
		Addr:              metricsAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		log.Print("HTTP server listening")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server error: %v", err)
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Printf("starting m360 adapter: group=%s api_version=%s flush=%s",
		group, apiVersion, flushInterval)

	runErr := r.Run(ctx, consumerGroup)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	if runErr != nil {
		log.Fatalf("runner error: %v", runErr)
	}

	log.Print("m360 adapter shut down cleanly")
}
