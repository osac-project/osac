/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// cost-management-adapter consumes canonical OSAC CloudEvents from Kafka and
// batches them to Cost Management's durable ingress endpoint.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-logr/stdr"
	"github.com/osac-project/osac-metering/adapters"
	"github.com/osac-project/osac-metering/adapters/envutil"
)

func main() {
	brokers := envutil.RequireEnv("KAFKA_BROKERS")
	costURL := envutil.RequireEnv("COST_MANAGEMENT_API_URL")
	if err := validateCostManagementURL(costURL); err != nil {
		log.Fatal(err)
	}
	tokenFile := envutil.RequireEnv("COST_MANAGEMENT_API_TOKEN_FILE")
	token := envutil.ReadFileOrFatal(tokenFile)

	topics := adapters.AllTopics
	if v := os.Getenv("KAFKA_TOPICS"); v != "" {
		topics = envutil.SplitAndTrim(v, ",")
		if len(topics) == 0 {
			log.Fatal("KAFKA_TOPICS must contain at least one topic")
		}
	}
	group := envutil.EnvOrDefault("KAFKA_CONSUMER_GROUP", "cost-management-adapter")
	metricsAddr := envutil.EnvOrDefault("METRICS_ADDR", ":2112")

	var flushInterval time.Duration
	if v := os.Getenv("FLUSH_INTERVAL"); v != "" {
		var err error
		flushInterval, err = time.ParseDuration(v)
		if err != nil || flushInterval < 0 {
			log.Fatalf("invalid FLUSH_INTERVAL %q", v)
		}
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
	}
	adapter := newCostManagementAdapter(newCostManagementClient(costURL, token))
	runner := adapters.NewRunner(adapter, adapters.RunnerConfig{
		Brokers:       brokers,
		ConsumerGroup: group,
		Topics:        topics,
		FlushInterval: flushInterval,
		Kafka:         kafkaCfg,
	}, logger, opts...)

	mux := http.NewServeMux()
	mux.Handle("/metrics", runner.MetricsHandler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := adapter.HealthCheck(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	server := &http.Server{
		Addr:              metricsAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		log.Print("HTTP server listening")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server error: %v", err)
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	log.Printf("starting Cost Management adapter: topics=%v group=%s flush=%s", topics, group, flushInterval)
	runErr := runner.Run(ctx)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("metrics server shutdown error: %v", err)
	}
	if runErr != nil {
		log.Fatalf("runner error: %v", runErr)
	}
}
