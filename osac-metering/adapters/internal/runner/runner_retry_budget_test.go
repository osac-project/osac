/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-logr/logr"
	"github.com/osac-project/osac-metering/adapters"
)

// retryBudgetSessions bounds the regression: the scaled retry budget spans far
// fewer sessions than this.
const retryBudgetSessions = 30

// retryBudgetGroup stands in for the broker and the discovery loop. Each
// Consume call is one session that redelivers every uncommitted record on the
// partition: a record the provider always rejects, followed by a later record
// on the same partition that only submits once the first one is resolved.
type retryBudgetGroup struct {
	mockConsumerGroup
	cancel  context.CancelFunc
	adapter *mockAdapter
	dlq     *mockDLQSender
}

func (g *retryBudgetGroup) Consume(
	ctx context.Context, topics []string, handler sarama.ConsumerGroupHandler,
) error {
	g.mu.Lock()
	g.sessions = append(g.sessions, append([]string(nil), topics...))
	sessions := len(g.sessions)
	g.mu.Unlock()
	if sessions >= retryBudgetSessions || g.recordFinished() {
		g.cancel()
		return nil
	}

	session := &mockSession{ctx: ctx}
	claim := &mockClaim{topic: testTopic, messages: make(chan *sarama.ConsumerMessage, 2)}
	claim.messages <- newTestMessage("poison", "vm-1", 0)
	claim.messages <- newTestMessage("later", "vm-2", 1)
	close(claim.messages)
	if err := handler.Setup(session); err != nil {
		return err
	}
	if err := handler.ConsumeClaim(session, claim); err != nil {
		return err
	}
	<-ctx.Done()
	return handler.Cleanup(session)
}

// recordFinished reports whether the poison record was dead-lettered and the
// later record on its partition was submitted.
func (g *retryBudgetGroup) recordFinished() bool {
	g.dlq.mu.Lock()
	finished := len(g.dlq.sends) > 0
	g.dlq.mu.Unlock()
	if !finished {
		return false
	}
	g.adapter.mu.Lock()
	defer g.adapter.mu.Unlock()
	for _, event := range g.adapter.submitCalls {
		if event.CloudEvent.ID() == "later" {
			return true
		}
	}
	return false
}

// TestRetryBudgetSurvivesDiscoverySessions checks that a record which keeps failing
// with a retryable error keeps its attempt count in state that survives sessions:
// the runner restarts its consume session every SessionInterval to discover
// new tenants, and ending a session cancels the in-flight claim. Without
// session-spanning state the budget resets on every restart, the record never
// reaches the DLQ, and it blocks every later record on its partition indefinitely.
//
// Clocks are scaled: the session lifetime and the retry backoffs shrink by
// the same factor, so the full attempt budget spans several sessions.
func TestRetryBudgetSurvivesDiscoverySessions(t *testing.T) {
	const scale = 1000
	originalSleep := sleepFunc
	sleepFunc = func(ctx context.Context, duration time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(duration / scale):
			return nil
		}
	}
	t.Cleanup(func() { sleepFunc = originalSleep })

	adapter := &mockAdapter{name: "retry-budget"}
	adapter.submitFn = func(event adapters.MeteringEvent) error {
		if event.CloudEvent.ID() == "poison" {
			return errors.New("persistent provider 500")
		}
		return nil
	}
	dlq := &mockDLQSender{}
	r := NewRunner(adapter, RunnerConfig{
		TopicsProvider:  func(context.Context) ([]string, error) { return []string{testTopic}, nil },
		SessionInterval: defaultSessionInterval / scale,
		FlushInterval:   time.Hour,
	}, logr.Discard(), WithDLQ(dlq))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	group := &retryBudgetGroup{cancel: cancel, adapter: adapter, dlq: dlq}
	if err := r.Run(ctx, group); err != nil {
		t.Fatal(err)
	}

	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	dlq.mu.Lock()
	defer dlq.mu.Unlock()

	var poison, later int
	for _, event := range adapter.submitCalls {
		switch event.CloudEvent.ID() {
		case "poison":
			poison++
		case "later":
			later++
		}
	}
	sessions := len(group.sessionTopics())
	t.Logf("sessions=%d poison submits=%d later submits=%d DLQ records=%d",
		sessions, poison, later, len(dlq.sends))

	if len(dlq.sends) == 0 {
		t.Fatalf("the poison record never reached the DLQ after %d sessions and %d attempts; "+
			"the retry budget resets on every session restart", sessions, poison)
	}
	if got := dlq.sends[0].attempts; got != defaultMaxRetries {
		t.Fatalf("DLQ attempts = %d, want %d: the budget must span sessions without resetting",
			got, defaultMaxRetries)
	}
	if poison != defaultMaxRetries {
		t.Fatalf("poison submits = %d, want exactly %d", poison, defaultMaxRetries)
	}
	if later == 0 {
		t.Fatal("records after the poison record on the same partition were never submitted")
	}
	if sessions < 3 {
		t.Fatalf("sessions = %d: the scaled budget must span at least three sessions", sessions)
	}
}
