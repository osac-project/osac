/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/IBM/sarama"

	kafkapub "github.com/osac-project/osac-metering/internal/kafka"
)

// Bounded ring buffer for CI — drops oldest events to prevent OOM.
// Not a production store; the test adapter is a read-only consumer
// used only for E2E test assertions.
const defaultMaxEvents = 10000

var topics = kafkapub.Topics

type eventStore struct {
	mu        sync.RWMutex
	events    []json.RawMessage
	maxEvents int
}

func (s *eventStore) add(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) >= s.maxEvents {
		s.events = s.events[1:]
	}
	s.events = append(s.events, json.RawMessage(data))
}

func (s *eventStore) query(eventType, resourceID string, since time.Time) []json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []json.RawMessage
	for _, raw := range s.events {
		var ev map[string]any
		if json.Unmarshal(raw, &ev) != nil {
			continue
		}
		if eventType != "" {
			if t, _ := ev["type"].(string); t != eventType {
				continue
			}
		}
		if resourceID != "" {
			if rid, _ := ev["osacresourceid"].(string); rid != resourceID {
				continue
			}
		}
		if !since.IsZero() {
			if ts, _ := ev["time"].(string); ts != "" {
				if t, err := time.Parse(time.RFC3339Nano, ts); err == nil && t.Before(since) {
					continue
				}
			}
		}
		result = append(result, raw)
	}
	return result
}

// consumerGroupHandler implements sarama.ConsumerGroupHandler.
type consumerGroupHandler struct {
	store *eventStore
}

func (h *consumerGroupHandler) Setup(_ sarama.ConsumerGroupSession) error   { return nil }
func (h *consumerGroupHandler) Cleanup(_ sarama.ConsumerGroupSession) error { return nil }
func (h *consumerGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		h.store.add(msg.Value)
		session.MarkMessage(msg, "")
	}
	return nil
}

func main() {
	cfg := kafkapub.ConnectionConfig{
		Brokers:      os.Getenv("KAFKA_BROKERS"),
		TLSCACert:    os.Getenv("KAFKA_TLS_CA_CERT"),
		SASLUser:     os.Getenv("KAFKA_SASL_USERNAME"),
		SASLPassFile: os.Getenv("KAFKA_SASL_PASSWORD_FILE"),
	}
	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8080"
	}

	maxEvents := defaultMaxEvents
	if v := os.Getenv("MAX_EVENTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxEvents = n
		}
	}

	if cfg.Brokers == "" || cfg.SASLPassFile == "" {
		fmt.Fprintln(os.Stderr, "KAFKA_BROKERS and KAFKA_SASL_PASSWORD_FILE are required")
		os.Exit(2)
	}

	store := &eventStore{maxEvents: maxEvents}

	sc := sarama.NewConfig()
	sc.Version = sarama.V3_9_0_0
	sc.Consumer.Offsets.Initial = sarama.OffsetNewest
	if err := kafkapub.ConfigureTLS(sc, cfg.TLSCACert); err != nil {
		fmt.Fprintf(os.Stderr, "TLS config: %v\n", err)
		os.Exit(1)
	}
	if err := kafkapub.ConfigureSASL(sc, cfg.SASLUser, cfg.SASLPassFile); err != nil {
		fmt.Fprintf(os.Stderr, "SASL config: %v\n", err)
		os.Exit(1)
	}

	brokers := kafkapub.SplitAndTrim(cfg.Brokers, ",")
	group, err := sarama.NewConsumerGroup(brokers, "osac-metering-test-adapter", sc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating consumer group: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = group.Close() }()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	go func() {
		handler := &consumerGroupHandler{store: store}
		for {
			if err := group.Consume(ctx, topics, handler); err != nil {
				fmt.Fprintf(os.Stderr, "consumer error: %v\n", err)
			}
			if ctx.Err() != nil {
				return
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		eventType := r.URL.Query().Get("type")
		resourceID := r.URL.Query().Get("resource_id")
		var since time.Time
		if s := r.URL.Query().Get("since"); s != "" {
			since, _ = time.Parse(time.RFC3339Nano, s)
		}
		events := store.query(eventType, resourceID, since)
		if events == nil {
			events = []json.RawMessage{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(events)
	})

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	fmt.Printf("test-adapter listening on %s, consuming %v\n", listenAddr, topics)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "HTTP server: %v\n", err)
		os.Exit(1)
	}
}
