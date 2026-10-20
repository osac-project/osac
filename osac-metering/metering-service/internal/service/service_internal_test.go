/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package service

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-logr/logr"

	"github.com/osac-project/osac-metering/internal/testenv"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testenv.StopContainers()
	os.Exit(code)
}

// newTestService builds a Service whose listing and loops the test controls.
func newTestService(t *testing.T) *Service {
	t.Helper()
	store := testenv.Database(t)
	s := &Service{
		cfg:     Config{Store: store, Logger: logr.Discard()},
		tenants: map[string]*tenantLoops{},
		missing: map[string]int{},
	}
	s.ensureTopics = func(context.Context, string) error { return nil }
	return s
}

// stubLoops replaces runLoops: it optionally fails the tenant before recording the start,
// and closes stopped when the context ends. Failing before the start is recorded matters:
// a test that waits for the start and immediately re-discovers must see the tenant failed.
func stubLoops(s *Service, fail bool) (starts *[]string, mu *sync.Mutex) {
	var count []string
	var lock sync.Mutex
	s.runLoops = func(ctx context.Context, tenant string, loops *tenantLoops) {
		defer close(loops.stopped)
		if fail {
			loops.fail()
		}
		lock.Lock()
		count = append(count, tenant)
		lock.Unlock()
		if fail {
			return
		}
		<-ctx.Done()
	}
	return &count, &lock
}

// TestDiscoverEnsuresTopicsBeforeLoops checks that a tenant's loops start only after its
// heartbeat and dead-letter topics exist, and that a tenant whose topics could not be
// created waits for a later pass instead of starting half-way.
func TestDiscoverEnsuresTopicsBeforeLoops(t *testing.T) {
	const tenant = "acme"
	s := newTestService(t)
	starts, mu := stubLoops(s, false)

	var ensured []string
	var fail bool
	s.listTenants = func(context.Context) ([]string, error) { return []string{tenant}, nil }
	s.ensureTopics = func(_ context.Context, name string) error {
		if fail {
			return errors.New("kafka unavailable")
		}
		ensured = append(ensured, name)
		return nil
	}

	fail = true
	if err := s.discover(context.Background()); err != nil {
		t.Fatalf("discover: %v", err)
	}
	mu.Lock()
	started := len(*starts)
	mu.Unlock()
	if started != 0 {
		t.Fatal("a tenant whose topics could not be ensured must not start")
	}

	fail = false
	if err := s.discover(context.Background()); err != nil {
		t.Fatalf("discover: %v", err)
	}
	waitUntil(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(*starts) == 1
	}, "the tenant's loops must start once its topics exist")
	if len(ensured) != 1 || ensured[0] != tenant {
		t.Fatalf("expected the tenant's topics to be ensured once, ensured %v", ensured)
	}
}

// waitUntil polls the condition until it holds or the deadline passes.
func waitUntil(t *testing.T, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal(message)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestDiscoverNeedsSeveralMissingPassesToDelete checks that one listing that omits a tenant
// does not delete its rows: the topic must stay missing across several passes.
func TestDiscoverNeedsSeveralMissingPassesToDelete(t *testing.T) {
	const tenant = "acme"
	s := newTestService(t)
	starts, mu := stubLoops(s, false)
	if err := s.cfg.Store.EnsureProgress(context.Background(), tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}
	s.listTenants = func(context.Context) ([]string, error) { return []string{tenant}, nil }
	if err := s.discover(context.Background()); err != nil {
		t.Fatalf("discover: %v", err)
	}

	// Two passes miss the tenant: its loops stay and its rows stay.
	empty := func(context.Context) ([]string, error) { return nil, nil }
	for range DeletionPasses - 1 {
		s.listTenants = empty
		if err := s.discover(context.Background()); err != nil {
			t.Fatalf("discover: %v", err)
		}
		s.lock.Lock()
		_, running := s.tenants[tenant]
		s.lock.Unlock()
		if !running {
			t.Fatal("a tenant missing from fewer passes than the deletion threshold must keep its loops")
		}
		if !tenantRowsExist(t, s, tenant) {
			t.Fatal("a tenant missing from fewer passes than the deletion threshold must keep its rows")
		}
	}

	// A pass that sees the tenant again resets the count.
	s.listTenants = func(context.Context) ([]string, error) { return []string{tenant}, nil }
	if err := s.discover(context.Background()); err != nil {
		t.Fatalf("discover: %v", err)
	}
	s.listTenants = empty
	for range DeletionPasses - 1 {
		if err := s.discover(context.Background()); err != nil {
			t.Fatalf("discover: %v", err)
		}
	}
	s.lock.Lock()
	_, running := s.tenants[tenant]
	s.lock.Unlock()
	if !running || !tenantRowsExist(t, s, tenant) {
		t.Fatal("a listing that saw the tenant again must reset the missing count")
	}

	// The threshold-th consecutive miss stops the loops, joins them, and deletes the rows.
	if err := s.discover(context.Background()); err != nil {
		t.Fatalf("discover: %v", err)
	}
	waitUntil(t, func() bool { return !tenantRowsExist(t, s, tenant) },
		"the tenant's rows must be deleted after the missing threshold")
	mu.Lock()
	defer mu.Unlock()
	if len(*starts) != 1 {
		t.Fatalf("the tenant's loops started %d times, want 1", len(*starts))
	}
}

// TestDiscoverRestartsFailedTenants checks that a tenant whose loops failed is restarted by
// a later discovery pass instead of vanishing from the map.
func TestDiscoverRestartsFailedTenants(t *testing.T) {
	const tenant = "acme"
	s := newTestService(t)
	starts, mu := stubLoops(s, true)
	s.listTenants = func(context.Context) ([]string, error) { return []string{tenant}, nil }

	if err := s.discover(context.Background()); err != nil {
		t.Fatalf("discover: %v", err)
	}
	waitUntil(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(*starts) == 1
	}, "the tenant's loops must start once")

	// The failed tenant is still registered until the next pass restarts it.
	if err := s.discover(context.Background()); err != nil {
		t.Fatalf("discover: %v", err)
	}
	waitUntil(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(*starts) == 2
	}, "a later pass must restart a tenant whose loops failed")
}

// TestDiscoverSurvivesListingFailures checks that one failed listing neither stops the
// running loops nor fails the service: the next pass lists again.
func TestDiscoverSurvivesListingFailures(t *testing.T) {
	const tenant = "acme"
	s := newTestService(t)
	starts, mu := stubLoops(s, false)
	s.listTenants = func(context.Context) ([]string, error) { return []string{tenant}, nil }
	if err := s.discover(context.Background()); err != nil {
		t.Fatalf("discover: %v", err)
	}
	waitUntil(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(*starts) == 1
	}, "the tenant's loops must start once")

	s.listTenants = func(context.Context) ([]string, error) {
		return nil, errors.New("broker unavailable")
	}
	if err := s.discover(context.Background()); err != nil {
		t.Fatalf("a failed listing must not fail discovery: %v", err)
	}
	s.lock.Lock()
	loops, running := s.tenants[tenant]
	s.lock.Unlock()
	if !running {
		t.Fatal("a failed listing must not stop the running loops")
	}
	select {
	case <-loops.stopped:
		t.Fatal("a failed listing must not stop the running loops")
	default:
	}

	s.listTenants = func(context.Context) ([]string, error) { return []string{tenant}, nil }
	if err := s.discover(context.Background()); err != nil {
		t.Fatalf("discover: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*starts) != 1 {
		t.Fatalf("a healthy pass after a failed listing must not restart the loops, got %d starts",
			len(*starts))
	}
}

// tenantRowsExist reports whether the tenant still has its progress row.
func tenantRowsExist(t *testing.T, s *Service, tenant string) bool {
	t.Helper()
	var exists bool
	err := s.cfg.Store.Pool().QueryRow(
		context.Background(),
		"SELECT EXISTS (SELECT 1 FROM progress WHERE tenant = $1)", tenant,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("reading tenant rows: %v", err)
	}
	return exists
}

// TestHealthyReflectsDeadLoops checks the readiness signal: a tenant whose loops exited with
// an error makes the service unhealthy, and a later discovery pass that restarts the tenant
// makes it healthy again.
func TestHealthyReflectsDeadLoops(t *testing.T) {
	const tenant = "acme"
	s := newTestService(t)
	s.listTenants = func(context.Context) ([]string, error) { return []string{tenant}, nil }

	// The tenant's loops fail on the first start, then run.
	var starts atomic.Int32
	s.runLoops = func(ctx context.Context, _ string, loops *tenantLoops) {
		defer close(loops.stopped)
		if starts.Add(1) == 1 {
			loops.fail()
			return
		}
		<-ctx.Done()
	}

	if err := s.discover(context.Background()); err != nil {
		t.Fatalf("discover: %v", err)
	}
	waitUntil(t, func() bool { return !s.Healthy() },
		"a tenant whose loops died must make the service unhealthy")

	if err := s.discover(context.Background()); err != nil {
		t.Fatalf("discover: %v", err)
	}
	waitUntil(t, func() bool { return s.Healthy() },
		"a restarted tenant must make the service healthy again")
}

// TestHealthyWithNoTenants checks that a service with no discovered tenants is healthy: the
// loops exist only for tenants with topics.
func TestHealthyWithNoTenants(t *testing.T) {
	s := newTestService(t)
	s.listTenants = func(context.Context) ([]string, error) { return nil, nil }
	if err := s.discover(context.Background()); err != nil {
		t.Fatalf("discover: %v", err)
	}
	if !s.Healthy() {
		t.Fatal("a service with no tenants must be healthy")
	}
}

// shutdownProducer stands in for the relay's producer and records when it closes.
type shutdownProducer struct {
	sarama.SyncProducer
	events chan string
}

func (p *shutdownProducer) SendMessage(*sarama.ProducerMessage) (int32, int64, error) {
	return 0, 0, nil
}

func (p *shutdownProducer) SendMessages([]*sarama.ProducerMessage) error { return nil }

func (p *shutdownProducer) Close() error {
	p.events <- "producer-closed"
	return nil
}

// shutdownConsumer and shutdownClient stand in for the service's other Kafka clients; only
// their Close is called during shutdown.
type shutdownConsumer struct{ sarama.Consumer }

func (c *shutdownConsumer) Close() error { return nil }

type shutdownClient struct{ sarama.Client }

func (c *shutdownClient) Close() error { return nil }

// TestRunJoinsLoopsBeforeClosingKafkaClients checks shutdown ordering: the tenant loops
// stop before the Kafka clients close, so a loop whose send is still in flight when the
// context ends does not hit a closed client.
func TestRunJoinsLoopsBeforeClosingKafkaClients(t *testing.T) {
	const tenant = "acme"
	s := newTestService(t)
	order := make(chan string, 4)
	s.producer = &shutdownProducer{events: order}
	s.consumer = &shutdownConsumer{}
	s.client = &shutdownClient{}
	s.listTenants = func(context.Context) ([]string, error) { return []string{tenant}, nil }
	s.runLoops = func(ctx context.Context, _ string, loops *tenantLoops) {
		defer close(loops.stopped)
		<-ctx.Done()
		// A send still in flight while shutdown runs: the producer must still be open.
		if _, _, err := s.producer.SendMessage(&sarama.ProducerMessage{
			Topic: "osac.metering.heartbeat.acme",
		}); err != nil {
			t.Errorf("in-flight send after cancellation failed: %v", err)
		}
		order <- "loop-stopped"
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitUntil(t, func() bool {
		s.lock.Lock()
		defer s.lock.Unlock()
		_, running := s.tenants[tenant]
		return running
	}, "the tenant's loops never started")

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run must return the context error, got %v", err)
	}

	// Run has returned, so the deferred shutdown finished and both events are recorded.
	first, second := <-order, <-order
	if first != "loop-stopped" || second != "producer-closed" {
		t.Fatalf("shutdown must join the loops before closing the Kafka clients, "+
			"got %q then %q", first, second)
	}
}
