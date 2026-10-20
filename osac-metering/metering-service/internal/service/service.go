/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// Package service runs the metering loops: one fold, one round, and one relay per tenant,
// discovered by listing event topics. The loops meet only in the database.
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

// DiscoverInterval is how often metering looks for new tenants and for tenants whose topics
// are gone.
const DiscoverInterval = 30 * time.Second

// DeletionPasses is how many discovery passes in a row a tenant's topic must be missing
// before metering deletes the tenant's rows. One incomplete listing, for example during a
// rolling broker restart, must not destroy billing history.
const DeletionPasses = 3

// Config configures the service.
type Config struct {
	Kafka         kafkaconn.ConnectionConfig
	Store         *database.Store
	Rules         rules.Source
	HeartbeatTick time.Duration
	DeploymentID  string
	Logger        logr.Logger

	// TopicReplicationFactor and TopicRetentionMS configure the heartbeat and dead-letter
	// topics the service creates for a tenant it serves for the first time. A factor of -1
	// leaves the broker default in place; the retention is the recovery window, because
	// metering rebuilds its database from the topics.
	TopicReplicationFactor int16
	TopicRetentionMS       string
}

// Service owns the per-tenant loops.
type Service struct {
	cfg Config

	client     sarama.Client
	consumer   sarama.Consumer
	producer   sarama.SyncProducer
	calculator *usage.Calculator

	lock     sync.Mutex
	tenants  map[string]*tenantLoops
	missing  map[string]int
	stopping bool

	// listTenants discovers tenant names; tests replace it.
	listTenants func(ctx context.Context) ([]string, error)

	// ensureTopics creates the tenant's heartbeat and dead-letter topics; tests replace it.
	ensureTopics func(ctx context.Context, tenant string) error

	// runLoops starts one tenant's loops; tests replace it.
	runLoops func(ctx context.Context, tenant string, loops *tenantLoops)
}

type tenantLoops struct {
	cancel  context.CancelFunc
	stopped chan struct{}

	// failed is set when a loop exited with an error. Discovery restarts a failed
	// tenant's loops on a later pass instead of leaving it running half-way.
	failed atomic.Bool
}

// fail marks the tenant's loops as failed.
func (l *tenantLoops) fail() { l.failed.Store(true) }

// hasFailed reports whether the tenant's loops exited with an error.
func (l *tenantLoops) hasFailed() bool { return l.failed.Load() }

// Healthy reports whether every discovered tenant's loops are running. A loop that exited
// with an error makes the pod not ready until a later discovery pass restarts the tenant.
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

// New loads rule revisions, checks them against the ones metering has used, and prepares the
// Kafka clients. It fails when metering must refuse to start.
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
	sc, err := kafkaconn.ProducerConfig(cfg.Kafka)
	if err != nil {
		return nil, err
	}
	client, err := sarama.NewClient(brokers, sc)
	if err != nil {
		return nil, fmt.Errorf("connecting to kafka: %w", err)
	}
	consumer, err := sarama.NewConsumerFromClient(client)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("creating kafka consumer: %w", err)
	}
	producer, err := sarama.NewSyncProducerFromClient(client)
	if err != nil {
		_ = consumer.Close()
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
		return kafkaconn.EnsureTenantTopics(cfg.Kafka, tenant, cfg.TopicReplicationFactor, cfg.TopicRetentionMS)
	}
	service.runLoops = service.runTenant
	return service, nil
}

// Run discovers tenants and runs their loops until the context ends.
func (s *Service) Run(ctx context.Context) error {
	defer func() {
		s.lock.Lock()
		s.stopping = true
		stopped := make([]chan struct{}, 0, len(s.tenants))
		for _, loops := range s.tenants {
			loops.cancel()
			stopped = append(stopped, loops.stopped)
		}
		s.lock.Unlock()
		// Join the loops before closing the Kafka clients: a relay that is mid-send or
		// a fold or round that is mid-consume must not hit a closed client, which can
		// panic and turn a clean exit into a crash.
		for _, done := range stopped {
			<-done
		}
		_ = s.producer.Close()
		_ = s.consumer.Close()
		_ = s.client.Close()
	}()

	ticker := time.NewTicker(DiscoverInterval)
	defer ticker.Stop()
	for {
		if err := s.discover(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// discover starts loops for new tenants and stops loops for tenants whose event topic is
// gone. A tenant's topics are deleted only after its final invoice; metering then stops the
// tenant and deletes its rows, but only after the topic stayed missing across several
// passes, because one listing can be incomplete. A tenant whose loops failed is restarted
// by a later pass rather than left running half-way.
func (s *Service) discover(ctx context.Context) error {
	tenants, err := s.listTenants(ctx)
	if err != nil {
		// A transient listing failure must not stop the running loops or the service:
		// the next pass lists again. Returning here would cancel every tenant's loops
		// and exit the process over a short broker disruption.
		s.cfg.Logger.Error(err, "listing tenant event topics; retrying on the next pass")
		return nil
	}
	live := map[string]bool{}

	s.lock.Lock()
	for _, tenant := range tenants {
		live[tenant] = true
		delete(s.missing, tenant)
		existing, running := s.tenants[tenant]
		if running && !existing.hasFailed() {
			continue
		}
		if running {
			// The tenant's loops failed; stop them and start over on this pass. The
			// per-loop tenant locks keep the old loops winding down from racing the
			// new ones.
			existing.cancel()
			delete(s.tenants, tenant)
		}
		if err := s.ensureTopics(ctx, tenant); err != nil {
			// The tenant's heartbeat and dead-letter topics are metering's own
			// infrastructure: without them the loops cannot produce or deliver, so the
			// tenant waits for a later pass instead of starting half-way.
			s.cfg.Logger.Error(err, "ensuring tenant topics; retrying on the next pass", "tenant", tenant)
			continue
		}
		tenantCtx, cancel := context.WithCancel(ctx)
		loops := &tenantLoops{cancel: cancel, stopped: make(chan struct{})}
		s.tenants[tenant] = loops
		go s.runLoops(tenantCtx, tenant, loops)
		s.cfg.Logger.Info("tenant started", "tenant", tenant)
	}
	for tenant, loops := range s.tenants {
		if live[tenant] || s.stopping {
			continue
		}
		s.missing[tenant]++
		if s.missing[tenant] < DeletionPasses {
			continue
		}
		delete(s.tenants, tenant)
		delete(s.missing, tenant)
		loops.cancel()
		go func(tenant string, loops *tenantLoops) {
			// Join the loops before deleting the rows: the deletion must not race
			// loops that still use the store.
			<-loops.stopped
			if err := s.cfg.Store.DeleteTenantRows(context.Background(), tenant); err != nil {
				s.cfg.Logger.Error(err, "deleting rows of a tenant whose topics are gone", "tenant", tenant)
			}
			s.cfg.Logger.Info("tenant stopped; rows deleted", "tenant", tenant)
		}(tenant, loops)
	}
	s.lock.Unlock()
	return nil
}

// runTenant runs one tenant's fold, round, and relay. Each loop logs its own errors and
// keeps going; a loop that exits with an error fails the tenant, cancels its sibling loops,
// and waits for them, so discovery can restart the tenant cleanly on a later pass.
func (s *Service) runTenant(ctx context.Context, tenant string, loops *tenantLoops) {
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
