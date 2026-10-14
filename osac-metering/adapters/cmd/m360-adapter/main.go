/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// m360-adapter consumes OSAC metering CloudEvents from Kafka and
// forwards them to the Monetize360 (M360) Usage API via REST or Kafka.
//
// Usage (REST — default):
//
//	export KAFKA_BROKERS="localhost:9092"
//	export M360_API_URL="https://m360.example.com"
//	export M360_API_KEY_FILE="/path/to/api-key"
//	go run ./cmd/m360-adapter/
//
// Usage (Kafka):
//
//	export KAFKA_BROKERS="localhost:9092"
//	export M360_OUTPUT_PROTOCOL="kafka"
//	export M360_KAFKA_BROKERS="m360-kafka:9093"
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

	"github.com/osac-project/osac-metering/adapters"
	"github.com/osac-project/osac-metering/adapters/internal/envutil"
	"github.com/osac-project/osac-metering/adapters/internal/kafka"
	"github.com/osac-project/osac-metering/adapters/internal/runner"
	"github.com/osac-project/osac-metering/adapters/m360"
)

func main() {
	brokers := envutil.RequireEnv("KAFKA_BROKERS")
	logger := stdr.New(log.New(os.Stderr, "", log.LstdFlags))
	protocol := envutil.EnvOrDefault("M360_OUTPUT_PROTOCOL", "rest")

	var adapter adapters.ProviderAdapter
	switch protocol {
	case "rest":
		m360URL := envutil.RequireEnv("M360_API_URL")
		apiKeyFile := envutil.RequireEnv("M360_API_KEY_FILE")
		apiKey := envutil.ReadFileOrFatal(apiKeyFile)
		apiVersion := envutil.EnvOrDefault("M360_API_VERSION", "v1")
		adapter = m360.NewAdapter(m360URL, apiVersion, apiKey, logger)
		log.Printf("M360 output: REST api_version=%s", apiVersion)

	case "kafka":
		m360Brokers := envutil.RequireEnv("M360_KAFKA_BROKERS")
		m360Cfg := kafka.KafkaConfig{
			TLSEnabled:   os.Getenv("M360_KAFKA_TLS_ENABLED") != "false",
			TLSCACert:    os.Getenv("M360_KAFKA_TLS_CA_CERT"),
			SASLUser:     os.Getenv("M360_KAFKA_SASL_USERNAME"),
			SASLPassFile: os.Getenv("M360_KAFKA_SASL_PASSWORD_FILE"),
		}
		producerCfg, err := kafka.NewProducerConfig(m360Cfg)
		if err != nil {
			log.Fatalf("M360 Kafka producer config: %v", err)
		}

		m360BrokerList := kafka.SplitAndTrimBrokers(m360Brokers, ",")
		if len(m360BrokerList) == 0 {
			log.Fatal("M360_KAFKA_BROKERS must contain at least one broker")
		}
		client, err := sarama.NewClient(m360BrokerList, producerCfg)
		if err != nil {
			log.Fatalf("M360 Kafka client: %v", err)
		}
		producer, err := sarama.NewSyncProducerFromClient(client)
		if err != nil {
			_ = client.Close()
			log.Fatalf("M360 Kafka producer: %v", err)
		}

		m360Topics := map[string]string{
			"vmaas": envutil.EnvOrDefault("M360_KAFKA_TOPIC_VMAAS", "m360.metering.vmaas"),
			"caas":  envutil.EnvOrDefault("M360_KAFKA_TOPIC_CAAS", "m360.metering.caas"),
			"maas":  envutil.EnvOrDefault("M360_KAFKA_TOPIC_MAAS", "m360.metering.maas"),
		}
		adapter = m360.NewKafkaAdapter(producer, client, m360Topics, logger)
		log.Printf("M360 output: Kafka brokers=%v topics=%v", m360BrokerList, m360Topics)

	default:
		log.Fatalf("unknown M360_OUTPUT_PROTOCOL %q (expected rest or kafka)", protocol)
	}

	topics := kafka.AllTopics
	if value := os.Getenv("KAFKA_TOPICS"); value != "" {
		topics = envutil.SplitAndTrim(value, ",")
		if len(topics) == 0 {
			log.Fatal("KAFKA_TOPICS must contain at least one topic")
		}
	}

	group := envutil.EnvOrDefault("KAFKA_CONSUMER_GROUP", "m360-adapter")
	var flushInterval time.Duration
	if value := os.Getenv("FLUSH_INTERVAL"); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil {
			log.Fatalf("invalid FLUSH_INTERVAL %q: %v", value, err)
		}
		if duration < 0 {
			log.Fatalf("FLUSH_INTERVAL must be non-negative, got %s", duration)
		}
		flushInterval = duration
	}

	metricsAddr := envutil.EnvOrDefault("METRICS_ADDR", ":2112")
	kafkaCfg := kafka.KafkaConfigFromEnv()
	dlqOpt, dlqClose, err := runner.DLQOptionFromEnv(brokers, kafkaCfg)
	if err != nil {
		log.Fatalf("setting up DLQ: %v", err)
	}
	defer func() {
		if err := dlqClose(); err != nil {
			log.Printf("DLQ producer close failed: %v", err)
		}
	}()
	var opts []runner.RunnerOption
	if dlqOpt != nil {
		opts = append(opts, dlqOpt)
		log.Printf("DLQ enabled: topic=%s", envutil.EnvOrDefault("DLQ_TOPIC", kafka.TopicDLQ))
	}

	r := runner.NewRunner(adapter, runner.RunnerConfig{
		Brokers:       brokers,
		ConsumerGroup: group,
		Topics:        topics,
		FlushInterval: flushInterval,
		Kafka:         kafkaCfg,
	}, logger, opts...)

	mux := http.NewServeMux()
	mux.Handle("/metrics", r.MetricsHandler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, request *http.Request) {
		if err := adapter.HealthCheck(request.Context()); err != nil {
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

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	log.Printf("starting m360 adapter: protocol=%s topics=%v group=%s flush=%s",
		protocol, topics, group, flushInterval)

	runErr := r.Run(ctx)
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
