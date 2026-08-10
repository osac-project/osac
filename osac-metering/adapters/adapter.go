/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package adapters

import (
	"context"

	cloudevents "github.com/cloudevents/sdk-go/v2"
)

// MeteringEvent wraps a CloudEvent with its Kafka coordinates.
type MeteringEvent struct {
	CloudEvent cloudevents.Event
	Topic      string
	Partition  int32
	Offset     int64
}

// SubmitResult is returned by Flush to report the outcome.
type SubmitResult struct {
	ProviderEventID string
	Idempotent      bool
}

// ProviderAdapter is the interface that concrete provider adapters implement.
// The Runner calls Submit per event and Flush on a configurable interval.
type ProviderAdapter interface {
	// Name returns the provider name used as a Prometheus label.
	Name() string
	// Submit processes a single metering event.
	Submit(ctx context.Context, event MeteringEvent) error
	// Flush uploads any buffered events. Called on the flush ticker
	// (default 10s) and on graceful shutdown.
	Flush(ctx context.Context) (SubmitResult, error)
	// HealthCheck verifies connectivity to the provider.
	HealthCheck(ctx context.Context) error
	// Close releases resources after the final Flush.
	Close() error
}

// RetryableError is an optional marker for documentation purposes. The Runner
// retries all errors by default — only errors wrapped in NonRetryableError are
// skipped. Wrapping in RetryableError makes the intent explicit but does not
// change retry behavior.
type RetryableError struct{ Err error }

func (e *RetryableError) Error() string { return e.Err.Error() }
func (e *RetryableError) Unwrap() error { return e.Err }

// NonRetryableError signals the runner should skip the event without retry.
type NonRetryableError struct{ Err error }

func (e *NonRetryableError) Error() string { return e.Err.Error() }
func (e *NonRetryableError) Unwrap() error { return e.Err }

// KafkaConfig configures the Kafka consumer connection.
type KafkaConfig struct {
	TLSEnabled   bool   // Enable TLS for broker connections
	TLSCACert    string // Path to CA certificate file (empty = system CAs)
	SASLUser     string // SASL/SCRAM username
	SASLPassFile string // Path to file containing SASL password
}
