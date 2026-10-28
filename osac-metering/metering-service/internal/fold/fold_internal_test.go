/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package fold

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/osac-project/osac-metering/internal/testenv"
)

// fakePartition stands in for a sarama partition consumer.
type fakePartition struct {
	messages chan *sarama.ConsumerMessage
}

func (p *fakePartition) Messages() <-chan *sarama.ConsumerMessage { return p.messages }
func (p *fakePartition) Errors() <-chan *sarama.ConsumerError {
	errors := make(chan *sarama.ConsumerError)
	return errors
}
func (p *fakePartition) HighWaterMarkOffset() int64 { return 0 }
func (p *fakePartition) AsyncClose()                {}
func (p *fakePartition) Close() error               { return nil }
func (p *fakePartition) IsPaused() bool             { return false }
func (p *fakePartition) Pause()                     {}
func (p *fakePartition) Resume()                    {}

// fakeConsumer stands in for a sarama consumer over a topic whose oldest retained
// record sits at offset 3: consuming from any offset below 3 fails as out of range,
// and OffsetOldest restarts at 3.
type fakeConsumer struct{ sarama.Consumer }

const oldestRetained = int64(3)

func (c *fakeConsumer) ConsumePartition(
	topic string, partition int32, offset int64,
) (sarama.PartitionConsumer, error) {
	if offset != sarama.OffsetOldest && offset < oldestRetained {
		return nil, sarama.ErrOffsetOutOfRange
	}
	messages := make(chan *sarama.ConsumerMessage, 2)
	for offset := max(offset, oldestRetained); offset < oldestRetained+2; offset++ {
		messages <- &sarama.ConsumerMessage{Topic: topic, Partition: partition, Offset: offset}
	}
	fake := &fakePartition{messages: messages}
	return fake, nil
}

// TestDrainEndsWhenTheChannelCloses checks that a partition consumer whose message channel
// Kafka closed ends the drain instead of spinning on the closed channel.
func TestDrainEndsWhenTheChannelCloses(t *testing.T) {
	f, err := New(nil, nil, logr.Discard())
	if err != nil {
		t.Fatalf("creating fold: %v", err)
	}
	messages := make(chan *sarama.ConsumerMessage)
	close(messages)

	done := make(chan error, 1)
	go func() {
		done <- f.drain(context.Background(), "t-closed", &fakePartition{messages: messages}, -1)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("drain on a closed channel: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("drain spins on a closed message channel instead of returning")
	}
}

// TestOutOfRangeOffsetLogsTheSkippedRange checks the loud report when retention deleted the
// segments below the stored offset: the skipped range is logged and the fold restarts at the
// oldest retained offset.
func TestOutOfRangeOffsetLogsTheSkippedRange(t *testing.T) {
	const tenant = "t-out-of-range"
	store := testenv.Database(t)
	if err := store.EnsureProgress(context.Background(), tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}
	// The fold has consumed up to offset 1; retention deleted everything below 3.
	if _, err := store.Pool().Exec(
		context.Background(), "UPDATE progress SET event_offset = 1 WHERE tenant = $1", tenant,
	); err != nil {
		t.Fatalf("seeding progress: %v", err)
	}

	core, logs := captured()
	f, err := New(store, &fakeConsumer{}, core)
	if err != nil {
		t.Fatalf("creating fold: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.RunTenant(ctx, tenant) }()

	deadline := time.Now().Add(30 * time.Second)
	for {
		progress, err := store.GetTenantProgress(context.Background(), tenant)
		if err != nil {
			t.Fatalf("reading progress: %v", err)
		}
		if progress.EventOffset >= oldestRetained+2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fold did not restart at the oldest retained offset, stopped at %d",
				progress.EventOffset)
		}
		time.Sleep(20 * time.Millisecond)
	}

	reported := false
	for _, entry := range logs.TakeAll() {
		if strings.Contains(entry.Message, "retention deleted events") {
			reported = true
			if entry.ContextMap()["skippedFromOffset"] != int64(1) ||
				entry.ContextMap()["oldestRetainedOffset"] != int64(3) {
				t.Fatalf(
					"skipped range reported as %v..%v, want 1..3",
					entry.ContextMap()["skippedFromOffset"], entry.ContextMap()["oldestRetainedOffset"],
				)
			}
		}
	}
	if !reported {
		t.Fatal("an out-of-range offset must log the skipped range loudly")
	}

	cancel()
	select {
	case err := <-done:
		// Cancellation can land mid-query, so the fold may return the context
		// error wrapped by the query it interrupted.
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("fold run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("fold did not stop after cancellation")
	}
}

// TestFreshFoldLogsTheRetentionGap checks the same report for a fold that has not consumed
// yet: a fresh or restored-empty database starts at the oldest retained record, and the
// records retention deleted below it are gone from history the same way. Skipping the gap
// silently would under-bill with no signal.
func TestFreshFoldLogsTheRetentionGap(t *testing.T) {
	const tenant = "t-fresh-gap"
	store := testenv.Database(t)
	if err := store.EnsureProgress(context.Background(), tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}
	// No progress row seeding: the fold has consumed nothing, so it starts at
	// the oldest retained record directly, without an out-of-range error.

	core, logs := captured()
	f, err := New(store, &fakeConsumer{}, core)
	if err != nil {
		t.Fatalf("creating fold: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.RunTenant(ctx, tenant) }()

	deadline := time.Now().Add(30 * time.Second)
	for {
		progress, err := store.GetTenantProgress(context.Background(), tenant)
		if err != nil {
			t.Fatalf("reading progress: %v", err)
		}
		if progress.EventOffset >= oldestRetained+2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fold did not consume from the oldest retained offset, stopped at %d",
				progress.EventOffset)
		}
		time.Sleep(20 * time.Millisecond)
	}

	reported := false
	for _, entry := range logs.TakeAll() {
		if strings.Contains(entry.Message, "retention deleted events") {
			reported = true
			if entry.ContextMap()["skippedFromOffset"] != int64(0) ||
				entry.ContextMap()["oldestRetainedOffset"] != int64(3) {
				t.Fatalf(
					"skipped range reported as %v..%v, want 0..3",
					entry.ContextMap()["skippedFromOffset"], entry.ContextMap()["oldestRetainedOffset"],
				)
			}
		}
	}
	if !reported {
		t.Fatal("a fresh fold over a retention-deleted topic must log the skipped range loudly")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("fold run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("fold did not stop after cancellation")
	}
}

// captured returns a logger that records its entries for assertions.
func captured() (logr.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return zapr.NewLogger(zap.New(core)), logs
}
