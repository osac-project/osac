/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

// Package service runs one fold, one round, and one relay per tenant.
//
// The flow, top to bottom:
//
//	Run          lists tenants every 30 seconds and reconciles them.
//	reconcile    starts loops for new tenants, stops loops for gone ones.
//	runTenant    runs the fold, the round, and the relay until the tenant is stopped.
package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-logr/logr"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/fold"
	kafkaconn "github.com/osac-project/osac-metering/internal/kafka"
	"github.com/osac-project/osac-metering/internal/relay"
	"github.com/osac-project/osac-metering/internal/round"
	"github.com/osac-project/osac-metering/internal/rules"
	"github.com/osac-project/osac-metering/internal/usage"
)

// DiscoverInterval is how often the tenant list is refreshed.
const DiscoverInterval = 30 * time.Second

// DeletionPasses is how many passes a tenant's event topic must be missing before its
// rows are deleted. One incomplete listing must not destroy billing history.
const DeletionPasses = 3

// Config configures the service.
type Config struct {
	Kafka         kafkaconn.ConnectionConfig
	Store         *database.Store
	Rules         rules.Source
	HeartbeatTick time.Duration
	DeploymentID  string
	Logger        logr.Logger

	// TopicRetentionMS is the retention of the topics the service creates for a tenant
	// it serves for the first time. The retention is the recovery window, because
	// metering rebuilds its database from the topics.
	TopicRetentionMS string
}

// Service runs the per-tenant loops.
type Service struct {
	cfg Config

	client     sarama.Client
	consumer   sarama.Consumer
	producer   sarama.SyncProducer
	calculator *usage.Calculator

	// tenants maps each running tenant to its loops. Guarded by lock.
	lock    sync.Mutex
	tenants map[string]*tenantLoops

	// missing counts the passes a gone tenant has been gone. Guarded by lock.
	missing map[string]int

	stopping bool

	// The three hooks below are replaced by tests.
	listTenants  func(ctx context.Context) ([]string, error)
	ensureTopics func(ctx context.Context, tenant string) error
	runTenant    func(ctx context.Context, tenant string, loops *tenantLoops)
}

// tenantLoops is one tenant's running loops.
type tenantLoops struct {
	cancel  context.CancelFunc
	stopped chan struct{}
	failed  atomic.Bool
}

func (l *tenantLoops) fail()           { l.failed.Store(true) }
func (l *tenantLoops) hasFailed() bool { return l.failed.Load() }

// Healthy reports whether every tenant's loops are running. A tenant whose loops exited
// with an error makes the pod not ready until a later pass restarts it.
func (s *Service) Healthy() bool {
	s.lock.Lock()
	defer s.lock.Unlock()
	for _, loops := range s.tenants {
		if loops.hasFailed() {
			return false
		}
	}
	return true
}

// New loads the rule revisions, records the ones metering will bill under, and connects to
// Kafka. It fails when metering must refuse to start.
func New(ctx context.Context, cfg Config) (*Service, error) {
	present, err := cfg.Rules.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading rule revisions: %w", err)
	}
	used, err := cfg.Store.ListRuleRevisions(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading recorded rule revisions: %w", err)
	}
	approved, err := rules.CheckStartup(used, present, time.Now)
	if err != nil {
		return nil, fmt.Errorf("checking rule revisions: %w", err)
	}
	for _, revision := range approved {
		if err := cfg.Store.RecordRuleRevision(ctx, database.RuleRevisionRow{
			Revision:    revision.Number,
			EffectiveAt: revision.EffectiveAt,
			MetersYAML:  revision.MetersYAML,
		}); err != nil {
			return nil, err
		}
	}

	owners, err := usage.RegistryOwners()
	if err != nil {
		return nil, err
	}
	calculator, err := usage.NewCalculator(approved, cfg.DeploymentID, owners)
	if err != nil {
		return nil, err
	}

	brokers := kafkaconn.SplitAndTrim(cfg.Kafka.Brokers, ",")
	saramaConfig, err := kafkaconn.ProducerConfig(cfg.Kafka)
	if err != nil {
		return nil, err
	}
	client, err := sarama.NewClient(brokers, saramaConfig)
	if err != nil {
		return nil, fmt.Errorf("connecting to kafka: %w", err)
	}
	consumer, err := sarama.NewConsumerFromClient(client)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("creating kafka consumer: %w", err)
	}
	producer, err := sarama.NewSyncProducer(brokers, saramaConfig)
	if err != nil {
		_ = consumer.Close()
		_ = client.Close()
		return nil, fmt.Errorf("creating kafka producer: %w", err)
	}

	service := &Service{
		cfg:        cfg,
		client:     client,
		consumer:   consumer,
		producer:   producer,
		calculator: calculator,
		tenants:    map[string]*tenantLoops{},
		missing:    map[string]int{},
	}
	service.listTenants = func(ctx context.Context) ([]string, error) {
		return kafkaconn.ListEventTenants(ctx, cfg.Kafka)
	}
	service.ensureTopics = func(ctx context.Context, tenant string) error {
		return kafkaconn.EnsureTenantTopics(cfg.Kafka, tenant, cfg.TopicRetentionMS)
	}
	service.runTenant = service.runTenantLoops
	return service, nil
}

// Run reconciles tenants until the context ends.
func (s *Service) Run(ctx context.Context) error {
	defer s.stopAll()

	ticker := time.NewTicker(DiscoverInterval)
	defer ticker.Stop()
	for {
		s.reconcile(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// stopAll cancels every tenant's loops and waits for them, then closes the Kafka clients.
func (s *Service) stopAll() {
	s.lock.Lock()
	s.stopping = true
	stopped := make([]chan struct{}, 0, len(s.tenants))
	for _, loops := range s.tenants {
		loops.cancel()
		stopped = append(stopped, loops.stopped)
	}
	s.lock.Unlock()
	for _, done := range stopped {
		<-done
	}
	_ = s.producer.Close()
	_ = s.consumer.Close()
	_ = s.client.Close()
}

// reconcile lists the tenants and brings the running set in line with the list: new
// tenants get loops, gone tenants lose them, failed tenants get fresh ones.
func (s *Service) reconcile(ctx context.Context) {
	tenants, err := s.listTenants(ctx)
	if err != nil {
		// A failed listing must not stop the service: the next pass lists again.
		s.cfg.Logger.Error(err, "listing tenant event topics; retrying on the next pass")
		return
	}
	live := map[string]bool{}
	for _, tenant := range tenants {
		live[tenant] = true
	}

	s.restartFailed(live)
	s.startNew(ctx, live)
	s.stopGone(live)
}

// startNew starts loops for every listed tenant that has none.
func (s *Service) startNew(ctx context.Context, live map[string]bool) {
	for tenant := range live {
		s.lock.Lock()
		_, running := s.tenants[tenant]
		s.lock.Unlock()
		if running {
			continue
		}
		// The heartbeat and dead-letter topics are metering's own infrastructure:
		// without them the loops cannot produce or deliver, so the tenant waits for
		// the next pass instead of starting half-way.
		if err := s.ensureTopics(ctx, tenant); err != nil {
			s.cfg.Logger.Error(err, "ensuring tenant topics; retrying on the next pass", "tenant", tenant)
			continue
		}
		s.start(ctx, tenant)
	}
}

// restartFailed stops the loops of a listed tenant whose loops failed, so startNew starts
// fresh ones on the same pass.
func (s *Service) restartFailed(live map[string]bool) {
	for tenant := range live {
		s.lock.Lock()
		loops, running := s.tenants[tenant]
		if running && loops.hasFailed() {
			loops.cancel()
			delete(s.tenants, tenant)
		}
		s.lock.Unlock()
	}
}

// stopGone stops the loops of a tenant whose event topic is gone and deletes its rows.
// Only several missing passes in a row count, because one listing can be incomplete:
// a pass that sees the tenant again resets its count.
func (s *Service) stopGone(live map[string]bool) {
	s.lock.Lock()
	var gone []string
	for tenant, loops := range s.tenants {
		if s.stopping {
			continue
		}
		if live[tenant] {
			delete(s.missing, tenant)
			continue
		}
		s.missing[tenant]++
		if s.missing[tenant] < DeletionPasses {
			continue
		}
		delete(s.tenants, tenant)
		delete(s.missing, tenant)
		gone = append(gone, tenant)
		loops.cancel()
		go s.deleteRows(tenant, loops)
	}
	s.lock.Unlock()
}

// deleteRows waits for a gone tenant's loops to end, then deletes the tenant's rows.
func (s *Service) deleteRows(tenant string, loops *tenantLoops) {
	<-loops.stopped
	if err := s.cfg.Store.DeleteTenantRows(context.Background(), tenant); err != nil {
		s.cfg.Logger.Error(err, "deleting rows of a tenant whose topics are gone", "tenant", tenant)
	}
	s.cfg.Logger.Info("tenant stopped; rows deleted", "tenant", tenant)
}

// start registers a tenant's loops and runs them in the background.
func (s *Service) start(ctx context.Context, tenant string) {
	tenantCtx, cancel := context.WithCancel(ctx)
	loops := &tenantLoops{cancel: cancel, stopped: make(chan struct{})}
	s.lock.Lock()
	s.tenants[tenant] = loops
	s.lock.Unlock()
	go s.runTenant(tenantCtx, tenant, loops)
	s.cfg.Logger.Info("tenant started", "tenant", tenant)
}

// runTenantLoops runs one tenant's fold, round, and relay until one of them fails or the
// context ends. Each loop logs its own errors and keeps going; a loop that exits with an
// error fails the tenant, so a later pass restarts it.
func (s *Service) runTenantLoops(ctx context.Context, tenant string, loops *tenantLoops) {
	defer close(loops.stopped)

	foldLoop, err := fold.New(s.cfg.Store, s.consumer, s.cfg.Logger.WithValues("loop", "fold"))
	if err != nil {
		s.cfg.Logger.Error(err, "creating fold", "tenant", tenant)
		loops.fail()
		return
	}
	roundLoop, err := round.New(
		s.cfg.Store, s.calculator, s.client, s.consumer,
		s.cfg.Logger.WithValues("loop", "round"),
	)
	if err != nil {
		s.cfg.Logger.Error(err, "creating round", "tenant", tenant)
		loops.fail()
		return
	}
	relayLoop, err := relay.New(
		s.cfg.Store, s.producer, s.cfg.Logger.WithValues("loop", "relay"),
	)
	if err != nil {
		s.cfg.Logger.Error(err, "creating relay", "tenant", tenant)
		loops.fail()
		return
	}

	var wg sync.WaitGroup
	run := func(name string, loop func() error) {
		defer wg.Done()
		if err := loop(); err != nil {
			s.cfg.Logger.Error(err, name+" exited", "tenant", tenant)
			loops.fail()
			loops.cancel()
		}
	}
	wg.Add(3)
	go run("fold", func() error { return foldLoop.RunTenant(ctx, tenant) })
	go run("round", func() error {
		return roundLoop.RunTenant(ctx, tenant, s.cfg.HeartbeatTick)
	})
	go run("relay", func() error { return relayLoop.RunTenant(ctx, tenant) })
	wg.Wait()
}
