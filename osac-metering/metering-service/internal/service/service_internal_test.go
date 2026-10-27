/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-logr/logr"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/heartbeat"
	"github.com/osac-project/osac-metering/internal/kafka"
	"github.com/osac-project/osac-metering/internal/rules"
	"github.com/osac-project/osac-metering/internal/testenv"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testenv.StopContainers()
	os.Exit(code)
}

// deadKafka is an address nothing listens on, so connecting to it fails without a
// timeout. Tests use it to make topic listing and creation fail for real.
const deadKafka = "127.0.0.1:1"

// newTestService builds a service on the real test broker and database. The Kafka
// clients come from the same configuration the service builds in production, which
// refuses to create topics implicitly, so a tenant whose event topic is missing fails
// for real: the fold cannot consume a topic that does not exist. A tenant whose event
// topic exists keeps running.
func newTestService(t *testing.T) *Service {
	t.Helper()
	brokers := []string{testenv.Broker(t)}
	saramaConfig, err := kafka.ProducerConfig(kafka.ConnectionConfig{Brokers: testenv.Broker(t)})
	if err != nil {
		t.Fatalf("building the kafka config: %v", err)
	}
	client, err := sarama.NewClient(brokers, saramaConfig)
	if err != nil {
		t.Fatalf("creating kafka client: %v", err)
	}
	consumer, err := sarama.NewConsumerFromClient(client)
	if err != nil {
		t.Fatalf("creating kafka consumer: %v", err)
	}
	producer, err := sarama.NewSyncProducer(brokers, saramaConfig)
	if err != nil {
		t.Fatalf("creating kafka producer: %v", err)
	}
	s := &Service{
		cfg: Config{
			Store:  testenv.Database(t),
			Kafka:  kafka.ConnectionConfig{Brokers: testenv.Broker(t)},
			Logger: logr.Discard(),
			// The round loop never fires during a test.
			HeartbeatTick:    time.Hour,
			TopicRetentionMS: "-1",
		},
		client:      client,
		consumer:    consumer,
		producer:    producer,
		tenants:     map[string]*tenantLoops{},
		startFailed: map[string]bool{},
		missing:     map[string]int{},
	}
	// stopAll stops whatever loops the test started and closes the clients once, even
	// for tests that never call Run.
	t.Cleanup(s.stopAll)
	return s
}

// createEventTopic creates the tenant's event topic with one event, so the tenant's
// fold has a topic to consume and its loops stay running.
func createEventTopic(t *testing.T, tenant string) {
	t.Helper()
	testenv.ProduceEvents(t, tenant, []*privatev1.Event{
		testenv.VMEvent("seed", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
			time.Now().UTC(), time.Now().UTC(), "seed", "RUNNING",
			time.Now().UTC(), "small", time.Now().UTC()),
	})
}

// runningLoops returns the tenant's current loops, failing when it has none.
func runningLoops(t *testing.T, s *Service, tenant string) *tenantLoops {
	t.Helper()
	s.lock.Lock()
	defer s.lock.Unlock()
	loops, running := s.tenants[tenant]
	if !running {
		t.Fatalf("tenant %s has no running loops", tenant)
	}
	return loops
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

// writeRevisionManifest writes one revision ConfigMap manifest into the directory, in
// the shape the chart ships and DirectorySource reads.
func writeRevisionManifest(t *testing.T, dir, number, effectiveAt, meters string) {
	t.Helper()
	manifest := `apiVersion: v1
kind: ConfigMap
metadata:
  name: ` + rules.NamePrefix + number + `
immutable: true
data:
  effective_at: "` + effectiveAt + `"
  meters.yaml: |
` + indentBlock(meters, "    ")
	if err := os.WriteFile(
		filepath.Join(dir, number+".yaml"), []byte(manifest), 0o600,
	); err != nil {
		t.Fatalf("writing manifest %s: %v", number, err)
	}
}

// indentBlock prefixes every line, for embedding a meters document in a manifest.
func indentBlock(text, prefix string) string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		out = append(out, prefix+line)
	}
	return strings.Join(out, "\n")
}

// TestNewStartsAfterRestoreBeforeFutureRevisionWasLoaded runs the restore scenario
// end to end. A backup taken while revision 0001 was used has its last round at T0;
// revision 0002 activated at T1 after T0 and spans were billed under it; the backup is
// then restored while both ConfigMaps stay. Startup must not refuse: the billing
// position is T0, 0002 is effective after it, and catch-up needs 0002 recorded to
// bill the T1..T2 spans the heartbeats on the topic already billed.
func TestNewStartsAfterRestoreBeforeFutureRevisionWasLoaded(t *testing.T) {
	ctx := context.Background()
	store := testenv.Database(t)

	// The rules the restore finds: 0001 since the epoch, and 0002 effective an hour
	// ago, which is after the backup's last round but before the wall clock.
	secondMeters := strings.Replace(
		testenv.DefaultMetersYAML(t), "states: [RUNNING]", "states: [RUNNING, PAUSED]", 1,
	)
	dir := t.TempDir()
	writeRevisionManifest(
		t, dir, "0001", "1970-01-01T00:00:00Z", testenv.DefaultMetersYAML(t),
	)
	writeRevisionManifest(
		t, dir, "0002", time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), secondMeters,
	)
	source := rules.NewDirectorySource(dir)
	present, err := source.List(ctx)
	if err != nil {
		t.Fatalf("listing revisions: %v", err)
	}

	// The restored backup: 0001 recorded as used, and a tenant whose last round ran
	// two days ago, before 0002 took effect.
	if err := store.RecordRuleRevision(ctx, database.RuleRevisionRow{
		Revision:    present[0].Number,
		EffectiveAt: present[0].EffectiveAt,
		MetersYAML:  present[0].MetersYAML,
	}); err != nil {
		t.Fatalf("recording revision 0001: %v", err)
	}
	tenant := strings.ToLower(t.Name())
	if err := store.EnsureProgress(ctx, tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}
	tx, err := store.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := database.SetLastRound(
		ctx, tx, tenant, time.Now().UTC().Add(-48*time.Hour), 0,
	); err != nil {
		t.Fatalf("setting the last round: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	svc, err := New(ctx, Config{
		Store:  store,
		Rules:  source,
		Kafka:  kafka.ConnectionConfig{Brokers: testenv.Broker(t)},
		Logger: logr.Discard(),
	})
	if err != nil {
		t.Fatalf("startup after a restore must not refuse: %v", err)
	}
	t.Cleanup(svc.stopAll)

	// Startup records both revisions, so catch-up can bill the backup gap under the
	// revision its heartbeats were already billed under.
	used, err := store.ListRuleRevisions(ctx)
	if err != nil {
		t.Fatalf("listing recorded revisions: %v", err)
	}
	recorded := map[string]bool{}
	for _, row := range used {
		recorded[row.Revision] = true
	}
	if !recorded["0001"] || !recorded["0002"] {
		t.Fatalf("startup must record revisions 0001 and 0002, got %+v", used)
	}
}

// TestReconcileEnsuresTopicsBeforeLoops checks that a tenant's loops start only after its
// heartbeat and dead-letter topics exist, and that a tenant whose topics could not be
// created waits for a later pass instead of starting half-way.
func TestReconcileEnsuresTopicsBeforeLoops(t *testing.T) {
	tenant := strings.ToLower(t.Name())
	s := newTestService(t)
	ctx := context.Background()

	// Kafka is unreachable, so ensuring the tenant's topics fails.
	s.cfg.Kafka = kafka.ConnectionConfig{Brokers: deadKafka}
	s.syncTenants(ctx, []string{tenant})
	s.lock.Lock()
	_, running := s.tenants[tenant]
	s.lock.Unlock()
	if running {
		t.Fatal("a tenant whose topics could not be ensured must not start")
	}

	// Kafka is back: the next pass ensures the topics and starts the tenant.
	s.cfg.Kafka = kafka.ConnectionConfig{Brokers: testenv.Broker(t)}
	s.syncTenants(ctx, []string{tenant})
	waitUntil(t, func() bool {
		s.lock.Lock()
		defer s.lock.Unlock()
		_, running := s.tenants[tenant]
		return running
	}, "the tenant's loops must start once its topics exist")
}

// TestReconcileNeedsSeveralMissingPassesToDelete checks that one listing that omits a tenant
// does not delete its rows: the topic must stay missing across several passes.
func TestReconcileNeedsSeveralMissingPassesToDelete(t *testing.T) {
	tenant := strings.ToLower(t.Name())
	s := newTestService(t)
	ctx := context.Background()
	if err := s.cfg.Store.EnsureProgress(ctx, tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}
	// The event topic exists, so the started loops keep running for the whole test.
	createEventTopic(t, tenant)

	s.syncTenants(ctx, []string{tenant})
	loops := runningLoops(t, s, tenant)

	// Two passes miss the tenant: its loops stay and its rows stay.
	for range DeletionPasses - 1 {
		s.syncTenants(ctx, nil)
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
	s.syncTenants(ctx, []string{tenant})
	for range DeletionPasses - 1 {
		s.syncTenants(ctx, nil)
	}
	if runningLoops(t, s, tenant) != loops {
		t.Fatal("a healthy tenant must keep its original loops across the missing passes")
	}
	if !tenantRowsExist(t, s, tenant) {
		t.Fatal("a listing that saw the tenant again must reset the missing count")
	}

	// The threshold-th consecutive miss stops the loops, joins them, and deletes the rows.
	s.syncTenants(ctx, nil)
	waitUntil(t, func() bool { return !tenantRowsExist(t, s, tenant) },
		"the tenant's rows must be deleted after the missing threshold")
}

// TestReconcileRestartsFailedTenants checks that a tenant whose loops failed is restarted by
// a later discovery pass instead of vanishing from the map. The tenant has no event topic,
// so its fold fails for real and fails the loops.
func TestReconcileRestartsFailedTenants(t *testing.T) {
	tenant := strings.ToLower(t.Name())
	s := newTestService(t)
	ctx := context.Background()

	s.syncTenants(ctx, []string{tenant})
	failed := runningLoops(t, s, tenant)
	waitUntil(t, func() bool { return !s.Healthy() },
		"a tenant whose event topic is missing must fail its loops")

	// The failed tenant is still registered until the next pass restarts it.
	s.syncTenants(ctx, []string{tenant})
	waitUntil(t, func() bool {
		s.lock.Lock()
		defer s.lock.Unlock()
		current, running := s.tenants[tenant]
		return running && current != failed
	}, "a later pass must restart a tenant whose loops failed")
}

// TestReconcileSurvivesListingFailures checks that one failed listing neither stops the
// running loops nor fails the service: the next pass lists again.
func TestReconcileSurvivesListingFailures(t *testing.T) {
	tenant := strings.ToLower(t.Name())
	s := newTestService(t)
	createEventTopic(t, tenant)

	// The tenant is already running when listing breaks.
	s.syncTenants(context.Background(), []string{tenant})
	loops := runningLoops(t, s, tenant)

	// Kafka is unreachable, so every listing fails. The running loops must survive.
	s.cfg.Kafka = kafka.ConnectionConfig{Brokers: deadKafka}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	// Run lists once, immediately; the listing fails on the unreachable broker.
	time.Sleep(500 * time.Millisecond)
	select {
	case <-loops.stopped:
		t.Fatal("a failed listing must not stop the running loops")
	default:
	}
	if runningLoops(t, s, tenant) != loops {
		t.Fatal("a failed listing must not replace the running loops")
	}

	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("Run must return the context error, got %v", err)
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
	tenant := strings.ToLower(t.Name())
	s := newTestService(t)
	ctx := context.Background()

	// The tenant has no event topic: its loops fail on the first start.
	s.syncTenants(ctx, []string{tenant})
	waitUntil(t, func() bool { return !s.Healthy() },
		"a tenant whose loops died must make the service unhealthy")

	// The event topic appears; the next pass restarts the tenant, whose loops now run.
	createEventTopic(t, tenant)
	s.syncTenants(ctx, []string{tenant})
	waitUntil(t, func() bool { return s.Healthy() },
		"a restarted tenant must make the service healthy again")
}

// TestHealthyWithNoTenants checks that a service with no discovered tenants is healthy: the
// loops exist only for tenants with topics.
func TestHealthyWithNoTenants(t *testing.T) {
	s := newTestService(t)
	s.syncTenants(context.Background(), nil)
	if !s.Healthy() {
		t.Fatal("a service with no tenants must be healthy")
	}
}

// TestReadinessIncludesDiscoveredTenantsThatCouldNotStart checks that readiness
// covers a tenant discovery listed but whose loops could not start: the pod must not
// report ready while a discovered tenant has no billing loops.
func TestReadinessIncludesDiscoveredTenantsThatCouldNotStart(t *testing.T) {
	tenant := strings.ToLower(t.Name())
	s := newTestService(t)
	ctx := context.Background()

	// The tenant is listed, but Kafka is unreachable: its heartbeat and dead-letter
	// topics cannot be ensured, so its loops never start.
	s.cfg.Kafka = kafka.ConnectionConfig{Brokers: deadKafka}
	s.syncTenants(ctx, []string{tenant})
	s.lock.Lock()
	_, running := s.tenants[tenant]
	s.lock.Unlock()
	if running {
		t.Fatal("a tenant whose topics could not be ensured must not start")
	}
	if s.Healthy() {
		t.Fatal("readiness must fail while a discovered tenant could not start")
	}

	// Kafka is back and the event topic exists: the next pass starts the tenant and
	// readiness recovers.
	createEventTopic(t, tenant)
	s.cfg.Kafka = kafka.ConnectionConfig{Brokers: testenv.Broker(t)}
	s.syncTenants(ctx, []string{tenant})
	waitUntil(t, func() bool {
		s.lock.Lock()
		defer s.lock.Unlock()
		_, running := s.tenants[tenant]
		return running
	}, "the tenant's loops must start once its topics exist")
	waitUntil(t, func() bool { return s.Healthy() },
		"a tenant that finally started must make the service healthy again")

	// A tenant that cannot start and then leaves the listing no longer holds readiness
	// down: it is gone, not broken.
	gone := strings.ToLower(t.Name()) + "-gone"
	s.cfg.Kafka = kafka.ConnectionConfig{Brokers: deadKafka}
	s.syncTenants(ctx, []string{gone})
	if s.Healthy() {
		t.Fatal("readiness must fail while a discovered tenant could not start")
	}
	s.syncTenants(ctx, nil)
	if !s.Healthy() {
		t.Fatal("a tenant that left the listing must not hold readiness down")
	}
}

// closeRecordingProducer fails the test if the service closes the producer before the
// tenant's loops stopped, and delegates every call to the real producer it wraps. The
// check runs inside Close, on the goroutine that waited for the loops, so no second
// observer races the shutdown.
type closeRecordingProducer struct {
	sarama.SyncProducer
	service *Service
	tenant  string
	t       *testing.T
}

func (p *closeRecordingProducer) Close() error {
	p.service.lock.Lock()
	loops, running := p.service.tenants[p.tenant]
	p.service.lock.Unlock()
	if running {
		select {
		case <-loops.stopped:
		default:
			p.t.Error("the producer must close only after the tenant loops stopped")
		}
	}
	return p.SyncProducer.Close()
}

// TestRunJoinsLoopsBeforeClosingKafkaClients checks shutdown ordering: the tenant loops
// stop before the Kafka clients close, so a loop whose send is still in flight when the
// context ends does not hit a closed client.
func TestRunJoinsLoopsBeforeClosingKafkaClients(t *testing.T) {
	tenant := strings.ToLower(t.Name())
	s := newTestService(t)
	createEventTopic(t, tenant)
	s.producer = &closeRecordingProducer{
		SyncProducer: s.producer, service: s, tenant: tenant, t: t,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitUntil(t, func() bool {
		s.lock.Lock()
		defer s.lock.Unlock()
		return s.tenants[tenant] != nil
	}, "the tenant's loops never started")
	// The loops must be alive at shutdown, so the ordering check cannot pass vacuously.
	waitUntil(t, func() bool { return s.Healthy() }, "the tenant's loops must run before shutdown")

	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("Run must return the context error, got %v", err)
	}
}

// blockedSendProducer blocks every send until the test releases it, and records how many
// sends were still active when the service closed the producer. A send released after the
// close fails instead of using the closed producer, the way a real in-flight send fails
// when shutdown closes the client under it.
type blockedSendProducer struct {
	sarama.SyncProducer
	entered       chan struct{}
	release       chan struct{}
	active        atomic.Int64
	closed        atomic.Bool
	activeAtClose atomic.Int64
}

func (p *blockedSendProducer) SendMessage(message *sarama.ProducerMessage) (int32, int64, error) {
	p.active.Add(1)
	defer p.active.Add(-1)
	select {
	case p.entered <- struct{}{}:
	default:
	}
	<-p.release
	if p.closed.Load() {
		return 0, 0, errors.New("the service closed the producer during the send")
	}
	return p.SyncProducer.SendMessage(message)
}

func (p *blockedSendProducer) Close() error {
	p.activeAtClose.Store(p.active.Load())
	p.closed.Store(true)
	return p.SyncProducer.Close()
}

// TestRestartKeepsStoppingLoopsInShutdownJoin checks the restart race: a tenant's
// loops fail while their relay send is still in flight, the next pass restarts
// the tenant, and shutdown then closes the Kafka clients. The old loops' send is still
// active, so shutdown must join the evicted loops before closing the clients, not only
// the replacement loops. The failure is injected for real: a database trigger fails the
// fold's next history write, the way a transient PostgreSQL outage would.
func TestRestartKeepsStoppingLoopsInShutdownJoin(t *testing.T) {
	const tenant = "restart-stopping-loops"
	s := newTestService(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	creation := now.Add(-2 * time.Minute)
	initial := testenv.VMEvent("seed-created", privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
		creation, creation, "web", "STARTING", creation, "small", creation)
	initial.GetComputeInstance().GetStatus().ClearInstanceType()
	initial.GetComputeInstance().GetStatus().ClearInstanceTypeTransitionTime()
	running := testenv.VMEvent("seed-running", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
		now.Add(-time.Minute), creation, "web", "RUNNING", now.Add(-time.Minute),
		"small", now.Add(-time.Minute))
	initial.GetComputeInstance().GetMetadata().SetTenant(tenant)
	running.GetComputeInstance().GetMetadata().SetTenant(tenant)
	testenv.ProduceEvents(t, tenant, []*privatev1.Event{initial, running})
	if err := s.cfg.Store.EnsureProgress(ctx, tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}
	key := database.LedgerKey{
		ResourceType: "ComputeInstance", ResourceID: "vm-7", Meter: "compute", Component: "compute",
		Dimensions: `{"tenant":"` + tenant + `"}`,
	}
	event, err := heartbeat.New(tenant, key, now.Add(-time.Minute), now, 60_000_000,
		"second", now, 2, "0001")
	if err != nil {
		t.Fatalf("building the heartbeat: %v", err)
	}
	payload, err := event.MarshalJSON()
	if err != nil {
		t.Fatalf("marshaling the heartbeat: %v", err)
	}
	producer := &blockedSendProducer{
		SyncProducer: s.producer, entered: make(chan struct{}, 1), release: make(chan struct{}),
	}
	s.producer = producer
	var old *tenantLoops
	// shutdownDone closes once the stopAll call below returns; nil until then.
	var shutdownDone chan struct{}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(producer.release) }) }
	t.Cleanup(func() {
		if old != nil {
			old.cancel()
		}
		release()
		if old != nil {
			select {
			case <-old.stopped:
			case <-time.After(10 * time.Second):
				t.Error("the old loops never stopped after their in-flight send was released")
			}
		}
		if shutdownDone != nil {
			select {
			case <-shutdownDone:
			case <-time.After(10 * time.Second):
			}
		}
	})
	s.syncTenants(ctx, []string{tenant})
	old = runningLoops(t, s, tenant)
	waitUntil(t, func() bool {
		progress, err := s.cfg.Store.GetTenantProgress(ctx, tenant)
		return err == nil && progress.EventOffset == 2
	}, "the initial VM history must fold before the round and injected database error")

	// A round has billed one minute and left its heartbeat in the outbox, the state a
	// restart can interrupt mid-delivery.
	tx, err := s.cfg.Store.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := database.SyncLedger(ctx, tx, tenant, key, []database.LedgerPiece{
		{Key: key, StartTime: now.Add(-time.Minute), EndTime: now, QuantityMicro: 60_000_000},
	}, time.Time{}); err != nil {
		t.Fatalf("writing the ledger: %v", err)
	}
	if err := database.EnqueueHeartbeats(ctx, tx, tenant, []database.OutboxRow{
		{ID: event.ID(), Payload: payload, RoundTime: now},
	}); err != nil {
		t.Fatalf("enqueueing the heartbeat: %v", err)
	}
	if err := database.SetLastRound(ctx, tx, tenant, now, 2); err != nil {
		t.Fatalf("setting the last round: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	select {
	case <-producer.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the old relay never started its send")
	}

	// The fold's next history write fails once, while the relay send waits.
	if _, err := s.cfg.Store.Pool().Exec(ctx, `
		CREATE SEQUENCE review_history_failures;
		CREATE FUNCTION review_fail_first_history_write() RETURNS trigger AS $$
		BEGIN
			IF nextval('review_history_failures') = 1 THEN
				RAISE EXCEPTION 'review transient database error';
			END IF;
			RETURN NEW;
		END $$ LANGUAGE plpgsql;
		CREATE TRIGGER review_fail_first_history_write BEFORE INSERT ON history
		FOR EACH ROW EXECUTE FUNCTION review_fail_first_history_write();
	`); err != nil {
		t.Fatalf("installing the transient failure: %v", err)
	}
	stopped := testenv.VMEvent("stop", privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
		now.Add(time.Second), creation, "web", "STOPPED", now.Add(time.Second),
		"small", now.Add(-time.Minute))
	stopped.GetComputeInstance().GetMetadata().SetTenant(tenant)
	testenv.ProduceEvents(t, tenant, []*privatev1.Event{stopped})
	waitUntil(t, old.hasFailed, "the injected fold error must mark the old loops failed")
	select {
	case <-old.stopped:
		t.Fatal("the old loops must still be waiting for their blocked relay send")
	default:
	}

	// The next pass restarts the tenant: the failed loops are evicted while their relay
	// is still blocked in its send, and replacement loops start at once.
	s.syncTenants(ctx, []string{tenant})
	current := runningLoops(t, s, tenant)
	if current == old {
		// Keeping the old loops registered until they stop is also a valid fix.
		return
	}

	// Shutdown joins the replacement loops first. It must then keep waiting for the
	// evicted loops: their send is still in flight, and the producer may close only
	// after it finishes.
	shutdownDone = make(chan struct{})
	done := shutdownDone
	go func() {
		s.stopAll()
		close(done)
	}()
	waitUntil(t, func() bool {
		select {
		case <-current.stopped:
			return true
		default:
			return false
		}
	}, "the replacement loops must stop at shutdown")
	select {
	case <-done:
		// Shutdown finished while the evicted loops' send was still blocked, so it
		// closed the producer under an in-flight send of loops it dropped.
		t.Fatalf("shutdown finished without joining the evicted loops: the producer " +
			"closed while their send was still in flight")
	case <-time.After(2 * time.Second):
		// Shutdown is still waiting, held by the evicted loops' join.
	}

	release()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown must finish once the in-flight send was released")
	}
	select {
	case <-old.stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("the old loops never finished after the send was released")
	}
	if got := producer.activeAtClose.Load(); got != 0 {
		t.Fatalf("shutdown closed the producer with %d in-flight sends of the evicted loops", got)
	}
}

// TestCheckRulesLoadsNewRevision checks the reload path: a revision that appears in the
// ConfigMaps after startup is validated with the same rules as startup, recorded, and
// swapped into the calculator the running rounds read, with no restart.
func TestCheckRulesLoadsNewRevision(t *testing.T) {
	ctx := context.Background()
	store := testenv.Database(t)

	dir := t.TempDir()
	writeRevisionManifest(t, dir, "0001", "1970-01-01T00:00:00Z", testenv.DefaultMetersYAML(t))
	source := rules.NewDirectorySource(dir)

	svc, err := New(ctx, Config{
		Store:  store,
		Rules:  source,
		Kafka:  kafka.ConnectionConfig{Brokers: testenv.Broker(t)},
		Logger: logr.Discard(),
	})
	if err != nil {
		t.Fatalf("startup: %v", err)
	}
	t.Cleanup(svc.stopAll)
	if got := len(svc.ruleset.Revisions()); got != 1 {
		t.Fatalf("the calculator serves %d revisions at startup, want 1", got)
	}

	// A second revision appears, effective after the billing position, so loading it
	// now is exactly what a restart would have done.
	secondMeters := strings.Replace(
		testenv.DefaultMetersYAML(t), "states: [RUNNING]", "states: [RUNNING, PAUSED]", 1,
	)
	writeRevisionManifest(
		t, dir, "0002", time.Now().UTC().Add(time.Hour).Format(time.RFC3339), secondMeters,
	)

	if err := svc.checkRules(ctx); err != nil {
		t.Fatalf("reloading a new revision: %v", err)
	}
	if got := len(svc.ruleset.Revisions()); got != 2 {
		t.Fatalf("the calculator serves %d revisions after the reload, want 2", got)
	}
	used, err := store.ListRuleRevisions(ctx)
	if err != nil {
		t.Fatalf("listing recorded revisions: %v", err)
	}
	recorded := map[string]bool{}
	for _, row := range used {
		recorded[row.Revision] = true
	}
	if !recorded["0001"] || !recorded["0002"] {
		t.Fatalf("the reload must record both revisions, got %+v", used)
	}

	// An unchanged set is a no-op: the check runs every minute and must not rebuild.
	if err := svc.checkRules(ctx); err != nil {
		t.Fatalf("rechecking an unchanged set: %v", err)
	}
	if got := len(svc.ruleset.Revisions()); got != 2 {
		t.Fatalf("the calculator serves %d revisions after a no-op check, want 2", got)
	}
}

// setBillingPosition commits a last-round time for one tenant, as a committed round
// tick would. The database's billing position is the latest across tenants.
func setBillingPosition(t *testing.T, store *database.Store, tenant string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := store.EnsureProgress(ctx, tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}
	tx, err := store.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := database.SetLastRound(ctx, tx, tenant, at, 0); err != nil {
		t.Fatalf("setting the last round: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestCheckRulesWaitsForInFlightRound regression-tests the rules race: a revision whose
// effective time falls inside an in-flight round's span used to be approved mid-round,
// because the check compared only against the committed billing position. After that
// round committed, no later round dragged the revision, so the span it covered kept its
// old pricing forever, and a replay under the final rules disagreed. The check must hold
// the write side of the rules gate — wait for the tick — and then reject the revision
// against the position the tick committed.
func TestCheckRulesWaitsForInFlightRound(t *testing.T) {
	ctx := context.Background()
	store := testenv.Database(t)

	dir := t.TempDir()
	writeRevisionManifest(t, dir, "0001", "1970-01-01T00:00:00Z", testenv.DefaultMetersYAML(t))

	svc, err := New(ctx, Config{
		Store:  store,
		Rules:  rules.NewDirectorySource(dir),
		Kafka:  kafka.ConnectionConfig{Brokers: testenv.Broker(t)},
		Logger: logr.Discard(),
	})
	if err != nil {
		t.Fatalf("startup: %v", err)
	}
	t.Cleanup(svc.stopAll)

	// A tenant whose last round ran a minute ago. A round ticking now spans the last
	// minute, and the revision below takes effect inside that span: acceptable
	// against the committed position, but not against the round that is in flight.
	tenant := strings.ToLower(t.Name())
	setBillingPosition(t, store, tenant, time.Now().UTC().Add(-time.Minute))

	secondMeters := strings.Replace(
		testenv.DefaultMetersYAML(t), "states: [RUNNING]", "states: [RUNNING, PAUSED]", 1,
	)
	writeRevisionManifest(
		t, dir, "0002", time.Now().UTC().Add(-30*time.Second).Format(time.RFC3339), secondMeters,
	)

	// The in-flight tick: Round.Once holds the read side of the rules gate for its
	// whole run, so the check must not be able to decide or swap meanwhile.
	svc.rulesGate.RLock()
	checkDone := make(chan error, 1)
	go func() { checkDone <- svc.checkRules(ctx) }()

	select {
	case err := <-checkDone:
		t.Fatalf("the check must wait for the in-flight round, returned %v", err)
	case <-time.After(200 * time.Millisecond):
		// Still waiting: the tick holds the read side.
	}

	// The tick commits: the billing position moves past the revision's effective
	// time, as a round that billed the span containing it would.
	setBillingPosition(t, store, tenant, time.Now().UTC())
	svc.rulesGate.RUnlock()

	select {
	case err := <-checkDone:
		if !errors.Is(err, errRulesDisagree) {
			t.Fatalf("a revision the in-flight round billed past must be rejected, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the check never finished after the round committed")
	}
	if got := len(svc.ruleset.Revisions()); got != 1 {
		t.Fatalf("a rejected revision must not be swapped in, calculator serves %d revisions", got)
	}
}

// TestCheckRulesStopsWhenRulesDisagree checks the fatal case: a revision whose effective
// time the billing position already passed appears for the first time. The spans it
// covers were never billed under it, so continuing under the loaded rules is wrong; the
// check must classify the refusal as a disagreement, which stops the service.
func TestCheckRulesStopsWhenRulesDisagree(t *testing.T) {
	ctx := context.Background()
	store := testenv.Database(t)

	dir := t.TempDir()
	writeRevisionManifest(t, dir, "0001", "1970-01-01T00:00:00Z", testenv.DefaultMetersYAML(t))

	svc, err := New(ctx, Config{
		Store:  store,
		Rules:  rules.NewDirectorySource(dir),
		Kafka:  kafka.ConnectionConfig{Brokers: testenv.Broker(t)},
		Logger: logr.Discard(),
	})
	if err != nil {
		t.Fatalf("startup: %v", err)
	}
	t.Cleanup(svc.stopAll)

	// A tenant's round just ran, so the billing position is now.
	tenant := strings.ToLower(t.Name())
	setBillingPosition(t, store, tenant, time.Now().UTC())

	// The new revision took effect before the billing position: the rounds after its
	// effective time were billed under 0001 and cannot be re-based.
	secondMeters := strings.Replace(
		testenv.DefaultMetersYAML(t), "states: [RUNNING]", "states: [RUNNING, PAUSED]", 1,
	)
	writeRevisionManifest(
		t, dir, "0002", time.Now().UTC().Add(-30*time.Minute).Format(time.RFC3339), secondMeters,
	)

	err = svc.checkRules(ctx)
	if !errors.Is(err, errRulesDisagree) {
		t.Fatalf("a revision effective before the billing position must disagree, got %v", err)
	}
	if got := len(svc.ruleset.Revisions()); got != 1 {
		t.Fatalf("a disagreement must not swap the calculator, which serves %d revisions", got)
	}
}

// TestCheckRulesStopsWhenRevisionMalformed checks the fatal case for a malformed
// revision: a ConfigMap the operator created with a typo cannot load, no retry fixes
// it, and the operator intended new pricing at its effective time. The check must
// classify the parse failure as a disagreement, which stops the service, instead of
// retrying it forever as a listing failure while the old calculator keeps serving.
func TestCheckRulesStopsWhenRevisionMalformed(t *testing.T) {
	ctx := context.Background()
	store := testenv.Database(t)

	dir := t.TempDir()
	writeRevisionManifest(t, dir, "0001", "1970-01-01T00:00:00Z", testenv.DefaultMetersYAML(t))

	svc, err := New(ctx, Config{
		Store:  store,
		Rules:  rules.NewDirectorySource(dir),
		Kafka:  kafka.ConnectionConfig{Brokers: testenv.Broker(t)},
		Logger: logr.Discard(),
	})
	if err != nil {
		t.Fatalf("startup: %v", err)
	}
	t.Cleanup(svc.stopAll)

	for name, malformed := range map[string]struct {
		effectiveAt string
		meters      string
	}{
		"invalid effective_at": {"not-a-time", testenv.DefaultMetersYAML(t)},
		"unknown meter key": {
			time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
			strings.Replace(
				testenv.DefaultMetersYAML(t), "states: [RUNNING]",
				"states: [RUNNING]\n    once: [READY]", 1,
			),
		},
	} {
		t.Run(name, func(t *testing.T) {
			writeRevisionManifest(t, dir, "0002", malformed.effectiveAt, malformed.meters)

			err := svc.checkRules(ctx)
			if !errors.Is(err, errRulesDisagree) {
				t.Fatalf("a malformed revision must be fatal, got %v", err)
			}
			if errors.Is(err, errRulesListing) {
				t.Fatalf("a malformed revision must not be transient, got %v", err)
			}
			if got := len(svc.ruleset.Revisions()); got != 1 {
				t.Fatalf("a malformed revision must not be swapped in, calculator serves %d", got)
			}
		})
	}
}

// failingSource is a rules source whose listing always fails, like a ConfigMap read
// during an API-server hiccup.
type failingSource struct{}

func (failingSource) List(context.Context) ([]rules.Revision, error) {
	return nil, errors.New("api server unavailable")
}

// TestCheckRulesRetriesListingFailures checks that a failed read of the ConfigMaps is
// transient: the check returns a listing error rather than a disagreement, and the
// loaded rules keep serving the rounds until the next check succeeds.
func TestCheckRulesRetriesListingFailures(t *testing.T) {
	ctx := context.Background()
	store := testenv.Database(t)

	dir := t.TempDir()
	writeRevisionManifest(t, dir, "0001", "1970-01-01T00:00:00Z", testenv.DefaultMetersYAML(t))

	svc, err := New(ctx, Config{
		Store:  store,
		Rules:  rules.NewDirectorySource(dir),
		Kafka:  kafka.ConnectionConfig{Brokers: testenv.Broker(t)},
		Logger: logr.Discard(),
	})
	if err != nil {
		t.Fatalf("startup: %v", err)
	}
	t.Cleanup(svc.stopAll)

	svc.cfg.Rules = failingSource{}
	err = svc.checkRules(ctx)
	if !errors.Is(err, errRulesListing) {
		t.Fatalf("a failed listing must be transient, got %v", err)
	}
	if errors.Is(err, errRulesDisagree) {
		t.Fatal("a failed listing must not be classified as a disagreement")
	}
	if got := len(svc.ruleset.Revisions()); got != 1 {
		t.Fatalf("the calculator serves %d revisions after a failed listing, want 1", got)
	}
}
