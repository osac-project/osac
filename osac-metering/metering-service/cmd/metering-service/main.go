/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/kafka"
	"github.com/osac-project/osac-metering/internal/topics"
)

type config struct {
	healthAddr      string
	kafka           kafka.ConnectionConfig
	dbURLFile       string
	heartbeatTick   time.Duration
	deploymentID    string
	rulesNamespace  string
	rulesConfigMaps string
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
		kafka: kafka.ConnectionConfig{
			Brokers:      os.Getenv("KAFKA_BROKERS"),
			TLSCACert:    os.Getenv("KAFKA_TLS_CA_CERT"),
			SASLUser:     os.Getenv("KAFKA_SASL_USERNAME"),
			SASLPassFile: os.Getenv("KAFKA_SASL_PASSWORD_FILE"),
		},
		dbURLFile:       envOrDefault("DB_URL_FILE", "/etc/metering/db/url"),
		heartbeatTick:   parseDurationOrDefault(os.Getenv("HEARTBEAT_INTERVAL"), time.Minute),
		deploymentID:    os.Getenv("METERING_DEPLOYMENT_ID"),
		rulesNamespace:  os.Getenv("METERING_RULES_NAMESPACE"),
		rulesConfigMaps: envOrDefault("METERING_RULES_CONFIGMAPS_DIR", ""),
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
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
	if c.rulesNamespace == "" && c.rulesConfigMaps == "" {
		return fmt.Errorf("METERING_RULES_NAMESPACE is required")
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

type serviceHealth struct {
	ready atomic.Bool
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

	tenants, err := kafka.ListEventTenants(ctx, cfg.kafka)
	if err != nil {
		return fmt.Errorf("listing tenant event topics: %w", err)
	}
	logger.Info("discovered tenant event topics", "count", len(tenants))
	for _, tenant := range tenants {
		logger.Info("tenant discovered", "tenant", tenant, "events", topics.Events(tenant))
	}

	health.ready.Store(true)
	logger.Info("service ready")

	<-ctx.Done()
	logger.Info("shutting down")
	return nil
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
		if !health.ready.Load() {
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
