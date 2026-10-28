/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package relay

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	"github.com/osac-project/osac-metering/internal/metrics/metertest"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/heartbeat"
	"github.com/osac-project/osac-metering/internal/round"
	"github.com/osac-project/osac-metering/internal/rules"
	"github.com/osac-project/osac-metering/internal/testenv"
	"github.com/osac-project/osac-metering/internal/topics"
	"github.com/osac-project/osac-metering/internal/usage"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testenv.StopContainers()
	os.Exit(code)
}

var base = time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)

func key(meter string) database.LedgerKey {
	return database.LedgerKey{
		ResourceType: "ComputeInstance",
		ResourceID:   "vm-7",
		Meter:        meter,
		Component:    meter,
		Dimensions:   `{"tenant":"acme"}`,
	}
}

// TestDrainOnceStoresTheSentOffset sends a multi-heartbeat batch and checks the stored
// heartbeat offset: it must move past every record the relay sent, not past the partition
// number of the first send.
func TestDrainOnceStoresTheSentOffset(t *testing.T) {
	tenant := "relay-offset"
	store := testenv.Database(t)
	if err := store.EnsureProgress(context.Background(), tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}
	if err := testenv.Kafka(t).CreateTopic(topics.Heartbeat(tenant)); err != nil {
		t.Fatalf("creating topic: %v", err)
	}

	// Five heartbeats, one per minute, all from one round.
	roundTime := base.Add(time.Minute)
	rows := make([]database.OutboxRow, 0, 5)
	for i := range 5 {
		event, err := heartbeat.New(
			tenant, key("compute"), base.Add(time.Duration(i)*time.Minute),
			base.Add(time.Duration(i+1)*time.Minute), 60_000_000, "second",
			roundTime, int64(i), "0001",
		)
		if err != nil {
			t.Fatalf("building heartbeat: %v", err)
		}
		payload, err := event.MarshalJSON()
		if err != nil {
			t.Fatalf("marshaling heartbeat: %v", err)
		}
		rows = append(rows, database.OutboxRow{
			ID: event.ID(), Payload: payload, RoundTime: roundTime,
		})
	}
	if err := database.EnqueueHeartbeats(context.Background(), store.Pool(), tenant, rows); err != nil {
		t.Fatalf("enqueueing: %v", err)
	}

	relay, err := New(store, testenv.NewProducer(t), logr.Discard())
	if err != nil {
		t.Fatalf("creating relay: %v", err)
	}
	sent, err := relay.drainOnce(context.Background(), tenant, topics.Heartbeat(tenant))
	if err != nil {
		t.Fatalf("draining: %v", err)
	}
	if sent != len(rows) {
		t.Fatalf("expected %d sent heartbeats, got %d", len(rows), sent)
	}

	progress, err := store.GetTenantProgress(context.Background(), tenant)
	if err != nil {
		t.Fatalf("reading progress: %v", err)
	}
	// The topic held no records before this batch, so the five sends take offsets 0 to 4
	// and the stored next offset is 5.
	if progress.HeartbeatOffset != 5 {
		t.Fatalf("stored heartbeat offset is %d, want 5", progress.HeartbeatOffset)
	}

	remaining, err := database.NextOutbox(context.Background(), store.Pool(), tenant, 10)
	if err != nil {
		t.Fatalf("reading outbox: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("outbox still holds %d rows after the drain", len(remaining))
	}
}

// TestRunTenantSurvivesSendErrors checks that a transient send error does not stop the relay:
// the loop logs it and drains again once the broker is back.
func TestRunTenantSurvivesSendErrors(t *testing.T) {
	tenant := "relay-resilient"
	store := testenv.Database(t)
	if err := store.EnsureProgress(context.Background(), tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}
	if err := testenv.Kafka(t).CreateTopic(topics.Heartbeat(tenant)); err != nil {
		t.Fatalf("creating topic: %v", err)
	}

	roundTime := base.Add(time.Minute)
	event, err := heartbeat.New(
		tenant, key("compute"), base, base.Add(time.Minute), 60_000_000, "second",
		roundTime, 0, "0001",
	)
	if err != nil {
		t.Fatalf("building heartbeat: %v", err)
	}
	payload, err := event.MarshalJSON()
	if err != nil {
		t.Fatalf("marshaling heartbeat: %v", err)
	}
	if err := database.EnqueueHeartbeats(context.Background(), store.Pool(), tenant,
		[]database.OutboxRow{{ID: event.ID(), Payload: payload, RoundTime: roundTime}},
	); err != nil {
		t.Fatalf("enqueueing: %v", err)
	}

	// A producer whose broker went away fails every send; the relay must keep looping
	// rather than exit. The heartbeat is still in the outbox afterwards.
	dead, err := New(store, failingProducer{}, logr.Discard())
	if err != nil {
		t.Fatalf("creating relay: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = dead.RunTenant(ctx, tenant) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	remaining, err := database.NextOutbox(context.Background(), store.Pool(), tenant, 10)
	if err != nil {
		t.Fatalf("reading outbox: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected the outbox to keep the heartbeat, got %d rows", len(remaining))
	}
	// The failing loop still reported the backlog: the relay's depth gauge is what makes
	// an unsent outbox visible while the pod stays ready.
	if depth := outboxGauge(t, tenant); depth != 1 {
		t.Fatalf("outbox depth gauge is %v, want 1 while the relay cannot send", depth)
	}
	// The real relay then drains the same outbox.
	relay, err := New(store, testenv.NewProducer(t), logr.Discard())
	if err != nil {
		t.Fatalf("creating relay: %v", err)
	}
	sent, err := relay.drainOnce(context.Background(), tenant, topics.Heartbeat(tenant))
	if err != nil {
		t.Fatalf("draining after failures: %v", err)
	}
	if sent != 1 {
		t.Fatalf("expected the relay to send the queued heartbeat after failures, got %d", sent)
	}
}

// failingProducer fails every send, like a producer whose broker went away. The transaction
// methods exist only to satisfy the sarama.SyncProducer interface.
type failingProducer struct{}

func (failingProducer) SendMessage(*sarama.ProducerMessage) (int32, int64, error) {
	return 0, 0, errors.New("broker unreachable")
}

func (failingProducer) SendMessages([]*sarama.ProducerMessage) error {
	return errors.New("broker unreachable")
}

func (failingProducer) Close() error                            { return nil }
func (failingProducer) TxnStatus() sarama.ProducerTxnStatusFlag { return 0 }
func (failingProducer) BeginTxn() error                         { return nil }
func (failingProducer) CommitTxn() error                        { return nil }
func (failingProducer) AbortTxn() error                         { return nil }
func (failingProducer) AddOffsetsToTxn(map[string][]*sarama.PartitionOffsetMetadata, string) error {
	return nil
}

func (failingProducer) AddMessageToTxn(*sarama.ConsumerMessage, string, *string) error {
	return nil
}

func (failingProducer) IsTransactional() bool { return false }

func (failingProducer) AddOffsetsToTxnWithGroupMetadata(
	map[string][]*sarama.PartitionOffsetMetadata, *sarama.ConsumerGroupMetadata,
) error {
	return nil
}

func (failingProducer) AddMessageToTxnWithGroupMetadata(
	*sarama.ConsumerMessage, *sarama.ConsumerGroupMetadata, *string,
) error {
	return nil
}

// runRoundAt runs one heartbeat round at a fixed time.
func runRoundAt(t *testing.T, store *database.Store, tenant string, moment time.Time) {
	t.Helper()
	revision := testenv.DefaultRevision(t)
	owners, err := usage.RegistryOwners()
	if err != nil {
		t.Fatalf("reading owners: %v", err)
	}
	calculator, err := usage.NewCalculator([]rules.Revision{revision}, "us-east", owners)
	if err != nil {
		t.Fatalf("creating calculator: %v", err)
	}
	roundLoop, err := round.New(
		store, calculator, testenv.NewClient(t), testenv.NewConsumer(t), logr.Discard(),
		&sync.RWMutex{},
	)
	if err != nil {
		t.Fatalf("creating round: %v", err)
	}
	if err := roundLoop.Once(context.Background(), tenant, moment); err != nil {
		t.Fatalf("round at %s: %v", moment, err)
	}
}

// enqueueOne writes one heartbeat to the tenant's outbox and returns its ID.
func enqueueOne(
	t *testing.T, store *database.Store, tenant string, from, to, roundTime time.Time,
) string {
	t.Helper()
	event, err := heartbeat.New(
		tenant, key("compute"), from, to, 60_000_000, "second", roundTime, 0, "0001",
	)
	if err != nil {
		t.Fatalf("building heartbeat: %v", err)
	}
	payload, err := event.MarshalJSON()
	if err != nil {
		t.Fatalf("marshaling heartbeat: %v", err)
	}
	if err := database.EnqueueHeartbeats(
		context.Background(), store.Pool(), tenant,
		[]database.OutboxRow{{ID: event.ID(), Payload: payload, RoundTime: roundTime}},
	); err != nil {
		t.Fatalf("enqueueing: %v", err)
	}
	return event.ID()
}

// relayStore returns the shared test database with the tenant's progress row and topic.
func relayStore(t *testing.T, tenant string) *database.Store {
	t.Helper()
	store := testenv.Database(t)
	if err := store.EnsureProgress(context.Background(), tenant); err != nil {
		t.Fatalf("ensuring progress: %v", err)
	}
	if err := testenv.Kafka(t).CreateTopic(topics.Heartbeat(tenant)); err != nil {
		t.Fatalf("creating topic: %v", err)
	}
	return store
}

// TestTwoRelaysSendEachHeartbeatOnce checks that two relays draining one tenant take turns
// over the outbox instead of both reading and sending the same rows.
func TestTwoRelaysSendEachHeartbeatOnce(t *testing.T) {
	tenant := "relay-two-pods"
	store := relayStore(t, tenant)

	ids := []string{
		enqueueOne(t, store, tenant, base, base.Add(time.Minute), base.Add(time.Minute)),
		enqueueOne(t, store, tenant, base.Add(time.Minute), base.Add(2*time.Minute),
			base.Add(time.Minute)),
		enqueueOne(t, store, tenant, base.Add(2*time.Minute), base.Add(3*time.Minute),
			base.Add(time.Minute)),
	}

	first, err := New(store, testenv.NewProducer(t), logr.Discard())
	if err != nil {
		t.Fatalf("creating relay: %v", err)
	}
	second, err := New(store, testenv.NewProducer(t), logr.Discard())
	if err != nil {
		t.Fatalf("creating relay: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	for _, relay := range []*Relay{first, second} {
		go func(relay *Relay) {
			defer wg.Done()
			if _, err := relay.drainOnce(
				context.Background(), tenant, topics.Heartbeat(tenant),
			); err != nil {
				t.Errorf("draining: %v", err)
			}
		}(relay)
	}
	wg.Wait()

	remaining, err := database.NextOutbox(context.Background(), store.Pool(), tenant, 10)
	if err != nil {
		t.Fatalf("reading outbox: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("the outbox still holds %d rows after both relays drained", len(remaining))
	}

	records := testenv.ConsumeAll(t, topics.Heartbeat(tenant))
	if len(records) != len(ids) {
		t.Fatalf("two relays sent %d records for %d heartbeats; each must be sent once",
			len(records), len(ids))
	}
	sent := map[string]bool{}
	for _, value := range records {
		event := cloudevents.Event{}
		if err := event.UnmarshalJSON(value); err != nil {
			t.Fatalf("unmarshaling record: %v", err)
		}
		if sent[event.ID()] {
			t.Fatalf("heartbeat %s was sent twice", event.ID())
		}
		sent[event.ID()] = true
	}
	for _, id := range ids {
		if !sent[id] {
			t.Fatalf("heartbeat %s was never sent", id)
		}
	}
}

// TestRelayDoesNotAdvancePastUnreadHeartbeats checks the offset a restored database keeps:
// the relay sends at the topic's end, but the stored offset must not jump past the backlog
// catch-up has not read, so a later round still replays those heartbeats as corrections.
func TestRelayDoesNotAdvancePastUnreadHeartbeats(t *testing.T) {
	tenant := "relay-restore"
	store := relayStore(t, tenant)

	// A backlog on the heartbeat topic the database has not read: two heartbeats of
	// earlier rounds, sent straight to the topic like a previous deployment did.
	backlog := []struct {
		from, to, roundTime time.Time
	}{
		{base, base.Add(time.Minute), base.Add(time.Minute)},
		{base.Add(time.Minute), base.Add(2 * time.Minute), base.Add(2 * time.Minute)},
	}
	producer := testenv.NewProducer(t)
	for _, entry := range backlog {
		event, err := heartbeat.New(
			tenant, key("compute"), entry.from, entry.to, 60_000_000, "second",
			entry.roundTime, 0, "0001",
		)
		if err != nil {
			t.Fatalf("building heartbeat: %v", err)
		}
		payload, err := event.MarshalJSON()
		if err != nil {
			t.Fatalf("marshaling heartbeat: %v", err)
		}
		if _, _, err := producer.SendMessage(&sarama.ProducerMessage{
			Topic: topics.Heartbeat(tenant),
			Key:   sarama.StringEncoder(event.ID()),
			Value: sarama.ByteEncoder(payload),
		}); err != nil {
			t.Fatalf("producing backlog heartbeat: %v", err)
		}
	}

	// The restored database sends its own difference on top of the backlog.
	enqueueOne(t, store, tenant, base.Add(2*time.Minute), base.Add(3*time.Minute),
		base.Add(3*time.Minute))
	relay, err := New(store, testenv.NewProducer(t), logr.Discard())
	if err != nil {
		t.Fatalf("creating relay: %v", err)
	}
	if _, err := relay.drainOnce(context.Background(), tenant, topics.Heartbeat(tenant)); err != nil {
		t.Fatalf("draining: %v", err)
	}

	progress, err := store.GetTenantProgress(context.Background(), tenant)
	if err != nil {
		t.Fatalf("reading progress: %v", err)
	}
	if progress.HeartbeatOffset != 0 {
		t.Fatalf("the relay moved the stored offset to %d, past the unread backlog",
			progress.HeartbeatOffset)
	}

	// A round still reads the backlog: the offset advances through every record, and
	// catch-up folds all three minutes into the ledger. This database has no history
	// for the resource, so the round's comparison then reverses exactly what catch-up
	// applied — the reversal is the witness that nothing was skipped.
	runRoundAt(t, store, tenant, base.Add(4*time.Minute))
	progress, err = store.GetTenantProgress(context.Background(), tenant)
	if err != nil {
		t.Fatalf("reading progress: %v", err)
	}
	if progress.HeartbeatOffset != 3 {
		t.Fatalf("catch-up advanced the offset to %d, want 3", progress.HeartbeatOffset)
	}
	rows, err := database.NextOutbox(context.Background(), store.Pool(), tenant, 10)
	if err != nil {
		t.Fatalf("reading outbox: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected the reversal of the caught-up usage, got %d outbox rows", len(rows))
	}
	event := cloudevents.Event{}
	if err := event.UnmarshalJSON(rows[0].Payload); err != nil {
		t.Fatalf("unmarshaling reversal: %v", err)
	}
	data, err := heartbeat.Data(event)
	if err != nil {
		t.Fatalf("decoding reversal: %v", err)
	}
	quantity, err := heartbeat.ParseMicro(data.Usage.Quantity)
	if err != nil {
		t.Fatalf("parsing reversal quantity: %v", err)
	}
	if quantity != -180_000_000 {
		t.Fatalf("the round reversed %d micro units, want the three caught-up minutes",
			quantity)
	}
}

// outboxGauge reads the relay's outbox-depth gauge for a tenant.
func outboxGauge(t *testing.T, tenant string) float64 {
	t.Helper()
	return metertest.LabeledValue(t, "osac_metering_outbox_depth", map[string]string{"tenant": tenant})
}
