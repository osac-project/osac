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
	"strings"
	"syscall"
	"time"

	"github.com/go-logr/stdr"
	"github.com/osac-project/osac-metering/adapters"
)

type m360Adapter struct {
	client *m360Client
}

func (a *m360Adapter) Name() string { return "m360" }

func (a *m360Adapter) Submit(ctx context.Context, event adapters.MeteringEvent) error {
	endpoint, payload, err := translateEvent(event.CloudEvent)
	if err != nil {
		return err
	}
	return a.client.post(ctx, endpoint, payload)
}

func (a *m360Adapter) Flush(_ context.Context) (adapters.SubmitResult, error) {
	return adapters.SubmitResult{Idempotent: true}, nil
}

func (a *m360Adapter) HealthCheck(ctx context.Context) error {
	return a.client.healthCheck(ctx)
}

func (a *m360Adapter) Close() error { return nil }

func main() {
	brokers := requireEnv("KAFKA_BROKERS")
	m360URL := requireEnv("M360_API_URL")
	apiKeyFile := requireEnv("M360_API_KEY_FILE")

	apiKey := readFileOrFatal(apiKeyFile)
	apiVersion := envOrDefault("M360_API_VERSION", "v1")

	topics := adapters.AllTopics
	if v := os.Getenv("KAFKA_TOPICS"); v != "" {
		topics = splitAndTrim(v, ",")
		if len(topics) == 0 {
			log.Fatal("KAFKA_TOPICS must contain at least one topic")
		}
	}

	group := envOrDefault("KAFKA_CONSUMER_GROUP", "m360-adapter")

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

	metricsAddr := envOrDefault("METRICS_ADDR", ":2112")

	// TLS defaults to true (!= "false") — safer for production.
	// Note: echo-adapter uses == "true" (defaults off) since it runs in
	// development/CI where Kafka may not have TLS configured.
	tlsEnabled := os.Getenv("KAFKA_TLS_ENABLED") != "false"

	logger := stdr.New(log.New(os.Stderr, "", log.LstdFlags))

	client := newM360Client(m360URL, apiVersion, apiKey)
	client.logger = logger

	adapter := &m360Adapter{client: client}
	runner := adapters.NewRunner(adapter, adapters.RunnerConfig{
		Brokers:       brokers,
		ConsumerGroup: group,
		Topics:        topics,
		FlushInterval: flushInterval,
		Kafka: adapters.KafkaConfig{
			TLSEnabled:   tlsEnabled,
			TLSCACert:    os.Getenv("KAFKA_TLS_CA_CERT"),
			SASLUser:     os.Getenv("KAFKA_SASL_USERNAME"),
			SASLPassFile: os.Getenv("KAFKA_SASL_PASSWORD_FILE"),
		},
	}, logger)

	mux := http.NewServeMux()
	mux.Handle("/metrics", runner.MetricsHandler())
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

	log.Printf("starting m360 adapter: topics=%v group=%s api_version=%s flush=%s",
		topics, group, apiVersion, flushInterval)

	runErr := runner.Run(ctx)

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

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("%s is required", key)
	}
	return v
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func readFileOrFatal(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("reading %s: %v", path, err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		log.Fatalf("%s is empty", path)
	}
	return trimmed
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	result := parts[:0]
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
