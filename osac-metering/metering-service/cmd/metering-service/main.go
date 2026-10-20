/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	kafkapkg "github.com/osac-project/osac-metering/internal/kafka"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/rules"
	"github.com/osac-project/osac-metering/internal/service"
)

type config struct {
	healthAddr    string
	kafka         kafkapkg.ConnectionConfig
	dbURLFile     string
	heartbeatTick time.Duration
	deploymentID  string

	// topicReplicationFactor and topicRetentionMS configure the heartbeat and
	// dead-letter topics metering creates for a tenant it serves for the first time.
	topicReplicationFactor int16
	topicRetentionMS       string
}

func main() {
	cfg := configFromEnv()
	if err := cfg.validate(); err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(2)
	}

	logger := setupLogger()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	if err := run(ctx, logger, cfg); err != nil {
		logger.Error(err, "metering service exited with error")
		os.Exit(1)
	}
}

func configFromEnv() *config {
	return &config{
		healthAddr: envOrDefault("HEALTH_ADDR", ":8080"),
		kafka: kafkapkg.ConnectionConfig{
			Brokers:      os.Getenv("KAFKA_BROKERS"),
			TLSCACert:    os.Getenv("KAFKA_TLS_CA_CERT"),
			SASLUser:     os.Getenv("KAFKA_SASL_USERNAME"),
			SASLPassFile: os.Getenv("KAFKA_SASL_PASSWORD_FILE"),
		},
		dbURLFile:     envOrDefault("DB_URL_FILE", "/etc/metering/db/url"),
		heartbeatTick: parseDurationOrDefault(os.Getenv("HEARTBEAT_INTERVAL"), time.Minute),
		deploymentID:  os.Getenv("METERING_DEPLOYMENT_ID"),

		topicReplicationFactor: int16OrDefault(os.Getenv("METERING_TOPIC_REPLICATION_FACTOR"), -1),
		topicRetentionMS:       envOrDefault("METERING_TOPIC_RETENTION_MS", "-1"),
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// int16OrDefault reads an int16 environment value, falling back when it is unset or empty.
func int16OrDefault(key string, fallback int16) int16 {
	if v := os.Getenv(key); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 16)
		if err != nil {
			return fallback
		}
		return int16(parsed)
	}
	return fallback
}

func (c *config) validate() error {
	if c.kafka.Brokers == "" {
		return fmt.Errorf("KAFKA_BROKERS is required")
	}
	if c.dbURLFile == "" {
		return fmt.Errorf("DB_URL_FILE is required")
	}
	if c.deploymentID == "" {
		return fmt.Errorf("METERING_DEPLOYMENT_ID is required")
	}
	if _, err := strconv.ParseInt(c.topicRetentionMS, 10, 64); err != nil {
		return fmt.Errorf("METERING_TOPIC_RETENTION_MS must be a whole number of milliseconds: %w", err)
	}
	return nil
}

func readDBURL(dir string) (string, error) {
	data, err := os.ReadFile(dir)
	if err != nil {
		return "", fmt.Errorf("reading database URL from %s: %w", dir, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// serviceHealth gates the readiness endpoint. It is ready once the service is built, and
// stops being ready while any tenant's loops are dead, so a pod that cannot bill is taken
// out of rotation even though the process is up.
type serviceHealth struct {
	ready atomic.Bool
	loops atomic.Pointer[service.Service]
}

// setLoops attaches the running service whose tenant loops readiness reflects.
func (h *serviceHealth) setLoops(s *service.Service) { h.loops.Store(s) }

// healthy reports whether the endpoint should answer ready.
func (h *serviceHealth) healthy() bool {
	if !h.ready.Load() {
		return false
	}
	loops := h.loops.Load()
	return loops == nil || loops.Healthy()
}

func run(ctx context.Context, logger logr.Logger, cfg *config) error {
	ctx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	health := &serviceHealth{}

	healthListener, err := net.Listen("tcp", cfg.healthAddr)
	if err != nil {
		return fmt.Errorf("binding health endpoint %s: %w", cfg.healthAddr, err)
	}
	go serveHealth(healthListener, health, logger, runCancel, ctx)

	dbURL, err := readDBURL(cfg.dbURLFile)
	if err != nil {
		return fmt.Errorf("reading database URL: %w", err)
	}
	dbPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("creating database connection pool: %w", err)
	}
	defer dbPool.Close()
	logger.Info("database pool connected", "urlFile", cfg.dbURLFile)

	if err := database.InitializeSchema(ctx, dbPool); err != nil {
		return fmt.Errorf("initializing database schema: %w", err)
	}

	namespace, err := podNamespace()
	if err != nil {
		return err
	}
	rulesSource, err := rules.NewConfigMapSource(namespace)
	if err != nil {
		return err
	}
	metering, err := service.New(ctx, service.Config{
		Kafka:                  cfg.kafka,
		Store:                  database.NewStore(dbPool),
		Rules:                  rulesSource,
		HeartbeatTick:          cfg.heartbeatTick,
		DeploymentID:           cfg.deploymentID,
		Logger:                 logger,
		TopicReplicationFactor: cfg.topicReplicationFactor,
		TopicRetentionMS:       cfg.topicRetentionMS,
	})
	if err != nil {
		return err
	}

	health.ready.Store(true)
	health.setLoops(metering)
	logger.Info("service ready")

	return metering.Run(ctx)
}

// podNamespace reads the namespace the pod runs in. Rule revisions are ConfigMaps in the
// service's own namespace, so no configuration can point the service at a namespace its
// RBAC does not reach.
func podNamespace() (string, error) {
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "", fmt.Errorf("reading the pod namespace: %w", err)
	}
	namespace := strings.TrimSpace(string(data))
	if namespace == "" {
		return "", errors.New("the pod namespace file is empty")
	}
	return namespace, nil
}

func setupLogger() logr.Logger {
	zapLog, err := zap.NewProduction()
	if err != nil {
		panic(fmt.Sprintf("creating zap logger: %v", err))
	}
	return zapr.NewLogger(zapLog)
}

func parseDurationOrDefault(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		fmt.Fprintf(os.Stderr, "warning: ignoring invalid duration %q, using default %s\n", s, fallback)
		return fallback
	}
	return d
}

func serveHealth(
	listener net.Listener,
	health *serviceHealth,
	logger logr.Logger,
	cancel context.CancelFunc,
	ctx context.Context,
) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !health.healthy() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprintln(w, "not ready")
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")
	})
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Error(err, "health server failed")
			cancel()
		}
	}()
	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
}
