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
//	Run            lists tenants every 30 seconds and syncs the running set.
//	syncTenants    starts loops for new tenants, stops loops for gone ones.
//	runTenantLoops runs the fold, the round, and the relay until the tenant stops.
package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-logr/logr"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/fold"
	kafkaconn "github.com/osac-project/osac-metering/internal/kafka"
	"github.com/osac-project/osac-metering/internal/metrics"
	"github.com/osac-project/osac-metering/internal/relay"
	"github.com/osac-project/osac-metering/internal/round"
	"github.com/osac-project/osac-metering/internal/rules"
	"github.com/osac-project/osac-metering/internal/usage"
)

// DiscoverInterval is how often the tenant list is refreshed.
const DiscoverInterval = 30 * time.Second

// RulesCheckInterval is how often the rule revisions are re-read. A revision that appears
// needs no restart: the next check loads it and the rounds pick it up on their next tick.
// A revision whose effective time passes unloaded makes the check fail and the service
// stop, because billing under rules metering never loaded is wrong.
const RulesCheckInterval = time.Minute

// DeletionPasses is how many passes a tenant's event topic must be missing before its
// rows are deleted. One incomplete listing must not destroy billing history.
const DeletionPasses = 3

// errRulesListing marks a failure reading the inputs of a rules check: the ConfigMaps, the
// recorded revisions, or the billing position. It is transient; the next check retries.
var errRulesListing = errors.New("reading rule revisions failed")

// errRulesDisagree marks a failure billing must not continue through: the rule revisions
// and the billing position contradict each other, or a revision is malformed and cannot
// load. It is fatal; the service exits and the startup check refuses the restart until
// the rules are fixed.
var errRulesDisagree = errors.New("rule revisions disagree with the billing position")

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

	client   sarama.Client
	consumer sarama.Consumer
	producer sarama.SyncProducer
	ruleset  *calculatorSet
	owners   map[string]usage.OwnerSpec
	loaded   []rules.Revision

	// rulesGate serializes rule-revision changes against round ticks. Every tick
	// holds the read side for its whole run, so a change cannot swap the calculator
	// between one tick's calculator calls; checkRules holds the write side, so a
	// change is both decided and applied only between ticks.
	rulesGate *sync.RWMutex

	// tenants maps each running tenant to its loops. Guarded by lock.
	lock    sync.Mutex
	tenants map[string]*tenantLoops

	// draining holds the loops a restart evicted but that have not stopped yet:
	// they still use the shared Kafka clients, so shutdown joins them before
	// closing the clients. Guarded by lock.
	draining []*tenantLoops

	// startFailed holds listed tenants whose loops could not start, so readiness
	// fails while they have no billing loops. Guarded by lock.
	startFailed map[string]bool

	// missing counts the passes a gone tenant has been gone. Guarded by lock.
	missing map[string]int

	stopping bool
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
// with an error makes the pod not ready until a later pass restarts it, and so does a
// discovered tenant that could not start: it has no billing loops at all.
func (s *Service) Healthy() bool {
	s.lock.Lock()
	defer s.lock.Unlock()
	for _, loops := range s.tenants {
		if loops.hasFailed() {
			return false
		}
	}
	return len(s.startFailed) == 0
}

// calculatorSet serves the rounds the currently loaded rule revisions. The rules check
// swaps the calculator when it approves a new revision, between rounds: a tick holds the
// read side of the rules gate for its whole run, so it reads one set for every one of its
// calculator calls and a reload needs no loop restart. It implements the round's
// Calculator interface by delegation.
type calculatorSet struct {
	current atomic.Pointer[usage.Calculator]
}

func (s *calculatorSet) swap(calculator *usage.Calculator) { s.current.Store(calculator) }

func (s *calculatorSet) Revisions() []rules.Revision { return s.current.Load().Revisions() }

func (s *calculatorSet) OwnerOf(resourceType string, facts []database.Fact) (string, string, bool) {
	return s.current.Load().OwnerOf(resourceType, facts)
}

func (s *calculatorSet) OwnedFieldOf(rootType string) (string, bool) {
	return s.current.Load().OwnedFieldOf(rootType)
}

func (s *calculatorSet) MeterAt(
	resourceType, meter string, at time.Time,
) (rules.Meter, string, bool) {
	return s.current.Load().MeterAt(resourceType, meter, at)
}

func (s *calculatorSet) Expected(
	tenant, resourceType, resourceID string, facts []database.Fact, root *usage.Root, upTo time.Time,
) ([]usage.Piece, error) {
	return s.current.Load().Expected(tenant, resourceType, resourceID, facts, root, upTo)
}

func (s *calculatorSet) Since(
	tenant, resourceType, resourceID string, facts []database.Fact, root *usage.Root,
	from, upTo time.Time, ledger []database.LedgerPiece,
) ([]usage.Piece, error) {
	return s.current.Load().Since(tenant, resourceType, resourceID, facts, root, from, upTo, ledger)
}

// approveRules reads the rule revisions, validates them against the database with the
// same rules as startup, records the approved set, and builds the calculator for it.
// Startup and the periodic reload both go through it, so a reload cannot accept a
// revision startup would refuse.
func approveRules(
	ctx context.Context, source rules.Source, store *database.Store, deploymentID string,
	owners map[string]usage.OwnerSpec,
) ([]rules.Revision, *usage.Calculator, error) {
	present, err := source.List(ctx)
	if err != nil {
		// A malformed revision is fatal, not transient: no retry fixes a YAML typo or a
		// bad effective_at, and the operator intended new pricing at that time. Billing
		// under the rules loaded before the malformed revision appeared would be wrong.
		if errors.Is(err, rules.ErrInvalid) {
			return nil, nil, fmt.Errorf("%w: a revision failed to parse: %v", errRulesDisagree, err)
		}
		return nil, nil, fmt.Errorf("%w: reading the ConfigMaps: %v", errRulesListing, err)
	}
	used, err := store.ListRuleRevisions(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: reading the recorded revisions: %v", errRulesListing, err)
	}
	// The billing position decides which revisions metering may record, not the wall
	// clock: a database restored from a backup older than a revision must record it,
	// because the rounds after the backup were billed under it.
	lastRound, err := store.LastRoundTime(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: reading the database's last round: %v", errRulesListing, err)
	}
	approved, err := rules.CheckStartup(used, present, lastRound)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", errRulesDisagree, err)
	}
	for _, revision := range approved {
		if err := store.RecordRuleRevision(ctx, database.RuleRevisionRow{
			Revision:    revision.Number,
			EffectiveAt: revision.EffectiveAt,
			MetersYAML:  revision.MetersYAML,
		}); err != nil {
			return nil, nil, fmt.Errorf("%w: recording revision %s: %v", errRulesListing, revision.Number, err)
		}
	}
	calculator, err := usage.NewCalculator(approved, deploymentID, owners)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: building the calculator: %v", errRulesListing, err)
	}
	return approved, calculator, nil
}

// sameRevisions reports whether two revision sets carry the same numbers and content.
func sameRevisions(a, b []rules.Revision) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Number != b[i].Number || a[i].EffectiveAt != b[i].EffectiveAt ||
			a[i].MetersYAML != b[i].MetersYAML {
			return false
		}
	}
	return true
}

// New loads the rule revisions, records the ones metering will bill under, and connects to
// Kafka. It fails when metering must refuse to start.
func New(ctx context.Context, cfg Config) (*Service, error) {
	owners, err := usage.RegistryOwners()
	if err != nil {
		return nil, err
	}
	approved, calculator, err := approveRules(ctx, cfg.Rules, cfg.Store, cfg.DeploymentID, owners)
	if err != nil {
		return nil, err
	}
	metrics.RulesLoaded(len(approved))
	ruleset := &calculatorSet{}
	ruleset.swap(calculator)
	rulesGate := &sync.RWMutex{}

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

	return &Service{
		cfg:         cfg,
		client:      client,
		consumer:    consumer,
		producer:    producer,
		ruleset:     ruleset,
		rulesGate:   rulesGate,
		owners:      owners,
		loaded:      approved,
		tenants:     map[string]*tenantLoops{},
		startFailed: map[string]bool{},
		missing:     map[string]int{},
	}, nil
}

// Run lists the tenants every 30 seconds and syncs the running set with the list
// until the context ends. A tenant that onboards appears on the next pass.
func (s *Service) Run(ctx context.Context) error {
	defer s.stopAll()

	// A rules disagreement is fatal: Run returns the error, the process exits, and the
	// startup check refuses the restart until the rules are fixed. Billing under rules
	// metering never loaded would be wrong.
	rulesFatal := make(chan error, 1)
	go s.watchRules(ctx, rulesFatal)

	ticker := time.NewTicker(DiscoverInterval)
	defer ticker.Stop()
	for {
		tenants, err := kafkaconn.ListEventTenants(ctx, s.cfg.Kafka)
		if err != nil {
			// A failed listing must not stop the service: the next pass lists again.
			s.cfg.Logger.Error(err, "listing tenant event topics; retrying on the next pass")
		} else {
			s.syncTenants(ctx, tenants)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-rulesFatal:
			return err
		case <-ticker.C:
		}
	}
}

// watchRules re-runs the startup validation every RulesCheckInterval. Listing failures
// are transient and retried; a validation disagreement stops the service through the
// fatal channel.
func (s *Service) watchRules(ctx context.Context, fatal chan<- error) {
	ticker := time.NewTicker(RulesCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := s.checkRules(ctx); err != nil {
			if errors.Is(err, errRulesDisagree) {
				s.cfg.Logger.Error(err,
					"rule revisions disagree with the billing position; stopping so billing "+
						"does not continue under rules metering never loaded",
				)
				fatal <- err
				return
			}
			metrics.RulesReloadFailed("list")
			s.cfg.Logger.Error(err, "checking rule revisions; retrying on the next check")
		}
	}
}

// checkRules approves whatever the ConfigMaps now hold and swaps the calculator when the
// approved set changed. The whole check runs between rounds: it holds the write side of
// the rules gate, so it waits for every in-flight tick and reads a billing position no
// round is about to advance. A revision whose effective time a just-finished round billed
// past is rejected here instead of recorded — recorded, no later round would drag it, and
// the span it covers would keep its old pricing forever.
func (s *Service) checkRules(ctx context.Context) error {
	s.rulesGate.Lock()
	defer s.rulesGate.Unlock()
	approved, calculator, err := approveRules(ctx, s.cfg.Rules, s.cfg.Store, s.cfg.DeploymentID, s.owners)
	if err != nil {
		return err
	}
	if sameRevisions(s.loaded, approved) {
		return nil
	}
	s.ruleset.swap(calculator)
	s.loaded = approved
	metrics.RulesLoaded(len(approved))
	s.cfg.Logger.Info(
		"loaded rule revisions", "revisions", len(approved),
		"latest", approved[len(approved)-1].Number,
	)
	return nil
}

// stopAll cancels every tenant's loops and waits for them, then closes the Kafka
// clients. The waiting covers the loops a restart evicted but that are still
// stopping, so no loop is using a client when it closes. A second call does
// nothing, so shutdown never closes the clients twice.
func (s *Service) stopAll() {
	s.lock.Lock()
	if s.stopping {
		s.lock.Unlock()
		return
	}
	s.stopping = true
	stopped := make([]chan struct{}, 0, len(s.tenants)+len(s.draining))
	for _, loops := range s.tenants {
		loops.cancel()
		stopped = append(stopped, loops.stopped)
	}
	for _, loops := range s.draining {
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

// syncTenants brings the running set in line with the listed tenants: new tenants
// get loops, gone tenants lose them, failed tenants get fresh ones.
func (s *Service) syncTenants(ctx context.Context, tenants []string) {
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
		// The heartbeat and dead-letter topics are metering's own infrastructure:
		// without them the loops cannot produce or deliver, so the tenant waits for
		// the next pass instead of starting half-way.
		if err := kafkaconn.EnsureTenantTopics(s.cfg.Kafka, tenant, s.cfg.TopicRetentionMS); err != nil {
			s.cfg.Logger.Error(err, "ensuring tenant topics; retrying on the next pass", "tenant", tenant)
			// Readiness must fail while a discovered tenant has no loops at all.
			s.lock.Lock()
			s.startFailed[tenant] = true
			s.lock.Unlock()
			continue
		}
		tenantCtx, cancel := context.WithCancel(ctx)
		loops := &tenantLoops{cancel: cancel, stopped: make(chan struct{})}
		s.lock.Lock()
		_, running := s.tenants[tenant]
		if !running {
			s.tenants[tenant] = loops
		}
		delete(s.startFailed, tenant)
		s.lock.Unlock()
		if running {
			cancel()
			continue
		}
		go s.runTenantLoops(tenantCtx, tenant, loops)
		s.cfg.Logger.Info("tenant started", "tenant", tenant)
	}
}

// restartFailed stops the loops of a listed tenant whose loops failed, so startNew starts
// fresh ones on the same pass. The evicted loops may still be finishing in-flight work on
// the shared Kafka clients, so they move to draining, which shutdown joins before closing
// the clients.
func (s *Service) restartFailed(live map[string]bool) {
	s.lock.Lock()
	s.pruneDrained()
	for tenant := range live {
		loops, running := s.tenants[tenant]
		if running && loops.hasFailed() {
			loops.cancel()
			delete(s.tenants, tenant)
			s.draining = append(s.draining, loops)
		}
	}
	s.lock.Unlock()
}

// pruneDrained drops the evicted loops that have stopped, so draining cannot grow without
// bound. Callers hold the lock.
func (s *Service) pruneDrained() {
	kept := s.draining[:0]
	for _, loops := range s.draining {
		select {
		case <-loops.stopped:
		default:
			kept = append(kept, loops)
		}
	}
	s.draining = kept
}

// stopGone stops the loops of a tenant whose event topic is gone and deletes its rows.
// Only several missing passes in a row count, because one listing can be incomplete:
// a pass that sees the tenant again resets its count.
func (s *Service) stopGone(live map[string]bool) {
	s.lock.Lock()
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
		loops.cancel()
		go s.deleteRows(tenant, loops)
	}
	// A listed tenant that could not start and then left the listing no longer holds
	// readiness down: it is gone, not broken.
	for tenant := range s.startFailed {
		if !live[tenant] {
			delete(s.startFailed, tenant)
		}
	}
	s.lock.Unlock()
}

// deleteRows waits for a gone tenant's loops to end, then deletes the tenant's rows.
func (s *Service) deleteRows(tenant string, loops *tenantLoops) {
	<-loops.stopped
	// Drop the tenant's metric series, unless the tenant reappeared and new
	// loops already took over: those loops own the series now.
	s.lock.Lock()
	if _, running := s.tenants[tenant]; !running {
		metrics.DeleteTenantHealthMetrics(tenant)
	}
	s.lock.Unlock()
	if err := s.cfg.Store.DeleteTenantRows(context.Background(), tenant); err != nil {
		s.cfg.Logger.Error(err, "deleting rows of a tenant whose topics are gone", "tenant", tenant)
		return
	}
	s.cfg.Logger.Info("tenant stopped; rows deleted", "tenant", tenant)
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
		s.cfg.Store, s.ruleset, s.client, s.consumer,
		s.cfg.Logger.WithValues("loop", "round"), s.rulesGate,
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
