/*
Copyright (c) 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
	kafkapub "github.com/osac-project/osac-metering/internal/kafka"
	"github.com/osac-project/osac-metering/internal/watch"
)

type config struct {
	fulfillmentAddr  string
	fulfillmentToken string
	tlsCACert        string
	kafkaTopic       string
	healthAddr       string
	kafka            kafkapub.ConnectionConfig
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
		fulfillmentAddr:  os.Getenv("FULFILLMENT_SERVER_ADDRESS"),
		fulfillmentToken: envOrDefault("FULFILLMENT_TOKEN_FILE", "/var/run/secrets/kubernetes.io/serviceaccount/token"),
		tlsCACert:        os.Getenv("TLS_CA_CERT"),
		kafkaTopic:       envOrDefault("KAFKA_TOPIC", "osac.metering.lifecycle"),
		healthAddr:       envOrDefault("HEALTH_ADDR", ":8080"),
		kafka: kafkapub.ConnectionConfig{
			Brokers:      os.Getenv("KAFKA_BROKERS"),
			TLSCACert:    os.Getenv("KAFKA_TLS_CA_CERT"),
			SASLUser:     os.Getenv("KAFKA_SASL_USERNAME"),
			SASLPassFile: os.Getenv("KAFKA_SASL_PASSWORD_FILE"),
		},
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (c *config) validate() error {
	if c.fulfillmentAddr == "" {
		return fmt.Errorf("FULFILLMENT_SERVER_ADDRESS is required")
	}
	if c.kafka.Brokers == "" {
		return fmt.Errorf("KAFKA_BROKERS is required")
	}
	if c.kafkaTopic == "" {
		return fmt.Errorf("KAFKA_TOPIC is required")
	}
	if c.kafka.SASLUser == "" {
		return fmt.Errorf("KAFKA_SASL_USERNAME is required")
	}
	if c.kafka.SASLPassFile == "" {
		return fmt.Errorf("KAFKA_SASL_PASSWORD_FILE is required")
	}
	return nil
}

type serviceHealth struct {
	ready atomic.Bool
	conn  *grpc.ClientConn
}

func run(ctx context.Context, logger logr.Logger, cfg *config) error {
	ctx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	health := &serviceHealth{}

	healthListener, err := net.Listen("tcp", cfg.healthAddr)
	if err != nil {
		return fmt.Errorf("binding health endpoint %s: %w", cfg.healthAddr, err)
	}
	go serveHealth(healthListener, health, logger, runCancel)

	grpcConn, err := dialFulfillment(cfg.fulfillmentAddr, cfg.tlsCACert, cfg.fulfillmentToken)
	if err != nil {
		return fmt.Errorf("connecting to fulfillment service: %w", err)
	}
	defer func() { _ = grpcConn.Close() }()

	connectCtx, connectCancel := context.WithTimeout(ctx, 30*time.Second)
	defer connectCancel()
	grpcConn.Connect()
	for {
		state := grpcConn.GetState()
		if state == connectivity.Ready {
			break
		}
		if !grpcConn.WaitForStateChange(connectCtx, state) {
			return fmt.Errorf("fulfillment service at %s is unreachable (state: %s)", cfg.fulfillmentAddr, grpcConn.GetState())
		}
	}
	logger.Info("connected to fulfillment service", "address", cfg.fulfillmentAddr)

	producer, err := kafkapub.NewSyncProducer(cfg.kafka)
	if err != nil {
		return fmt.Errorf("creating kafka producer: %w", err)
	}
	defer func() { _ = producer.Close() }()
	logger.Info("kafka producer connected", "brokers", cfg.kafka.Brokers)

	if err := kafkapub.VerifyTopicExists(cfg.kafka, cfg.kafkaTopic); err != nil {
		return fmt.Errorf("kafka topic %q not available: %w", cfg.kafkaTopic, err)
	}
	logger.Info("kafka topic verified", "topic", cfg.kafkaTopic)

	publisher := kafkapub.NewPublisher(producer, cfg.kafkaTopic)
	eventsClient := privatev1.NewEventsClient(grpcConn)
	consumer := watch.NewConsumer(eventsClient, publisher, logger)

	health.conn = grpcConn
	health.ready.Store(true)
	logger.Info("service ready, starting watch consumer")
	return consumer.Run(ctx)
}

func dialFulfillment(addr, caCertPath, tokenFile string) (*grpc.ClientConn, error) {
	var creds credentials.TransportCredentials
	if caCertPath != "" {
		var err error
		creds, err = tlsCredentialsFromCA(caCertPath)
		if err != nil {
			return nil, fmt.Errorf("loading TLS CA cert: %w", err)
		}
	} else {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(creds)}
	if tokenFile != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(oauth.TokenSource{
			TokenSource: &fileTokenSource{tokenFile: tokenFile},
		}))
	}
	return grpc.NewClient(addr, opts...)
}

type fileTokenSource struct {
	tokenFile string
}

func (s *fileTokenSource) Token() (*oauth2.Token, error) {
	data, err := os.ReadFile(s.tokenFile)
	if err != nil {
		return nil, fmt.Errorf("reading token from %s: %w", s.tokenFile, err)
	}
	return &oauth2.Token{AccessToken: strings.TrimSpace(string(data))}, nil
}

func tlsCredentialsFromCA(caCertPath string) (credentials.TransportCredentials, error) {
	caCert, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("reading CA cert %s: %w", caCertPath, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA cert %s", caCertPath)
	}
	return credentials.NewTLS(&tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}), nil
}

func serveHealth(listener net.Listener, health *serviceHealth, logger logr.Logger, cancel context.CancelFunc) {
	mux := http.NewServeMux()
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
		if health.conn != nil {
			state := health.conn.GetState()
			if state == connectivity.TransientFailure || state == connectivity.Shutdown {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = fmt.Fprintln(w, "not ready")
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")
	})
	logger.Info("health probe listening", "address", listener.Addr().String())
	if err := http.Serve(listener, mux); err != nil {
		logger.Error(err, "health server failed")
		cancel()
	}
}

func setupLogger() logr.Logger {
	zapLog, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create logger: %v\n", err)
		os.Exit(1)
	}
	return zapr.NewLogger(zapLog)
}
