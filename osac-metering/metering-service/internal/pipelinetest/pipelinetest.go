/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

// Package pipelinetest provides the fold-round-relay pipeline the integration tests
// drive: the wiring the service runs, minus tenant discovery. It lives apart from
// testenv because it imports the packages under test, whose own in-package tests
// import testenv.
package pipelinetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/fold"
	"github.com/osac-project/osac-metering/internal/heartbeat"
	"github.com/osac-project/osac-metering/internal/relay"
	"github.com/osac-project/osac-metering/internal/round"
	"github.com/osac-project/osac-metering/internal/rules"
	"github.com/osac-project/osac-metering/internal/testenv"
	"github.com/osac-project/osac-metering/internal/topics"
	"github.com/osac-project/osac-metering/internal/usage"
	"github.com/osac-project/osac-metering/schema"
)

// deploymentID is the dimension every meter sends; any fixed value works.
const deploymentID = "us-east"

// SentBeat is one heartbeat read from a tenant's heartbeat topic, with its event.
type SentBeat struct {
	Event cloudevents.Event
	Data  schema.HeartbeatData
}

// Pipeline bundles a fold, a round, and a relay against one database.
type Pipeline struct {
	Tenant string
	Store  *database.Store
	Fold   *fold.Fold
	Round  *round.Round
	Relay  *relay.Relay
}

// NewPipeline builds the pipeline on a fresh database, billing under the chart's
// default revision.
func NewPipeline(t *testing.T, tenant string) *Pipeline {
	t.Helper()
	return NewPipelineOnStore(t, tenant, testenv.Database(t))
}

// NewPipelineOnStore builds the pipeline on the given store, billing under the chart's
// default revision: a restored database replays under the shipped rules.
func NewPipelineOnStore(t *testing.T, tenant string, store *database.Store) *Pipeline {
	t.Helper()
	return NewPipelineWithRevisions(t, tenant, store, []rules.Revision{testenv.DefaultRevision(t)})
}

// NewPipelineWithRevisions builds the pipeline billing under the given revisions.
func NewPipelineWithRevisions(
	t *testing.T, tenant string, store *database.Store, revisions []rules.Revision,
) *Pipeline {
	t.Helper()
	owners, err := usage.RegistryOwners()
	if err != nil {
		t.Fatalf("reading owners: %v", err)
	}
	calculator, err := usage.NewCalculator(revisions, deploymentID, owners)
	if err != nil {
		t.Fatalf("creating calculator: %v", err)
	}
	foldLoop, err := fold.New(store, testenv.NewConsumer(t), logr.Discard())
	if err != nil {
		t.Fatalf("creating fold: %v", err)
	}
	roundLoop, err := round.New(
		store, calculator, testenv.NewClient(t), testenv.NewConsumer(t), logr.Discard(),
		&sync.RWMutex{},
	)
	if err != nil {
		t.Fatalf("creating round: %v", err)
	}
	relayLoop, err := relay.New(store, testenv.NewProducer(t), logr.Discard())
	if err != nil {
		t.Fatalf("creating relay: %v", err)
	}
	return &Pipeline{
		Tenant: tenant, Store: store, Fold: foldLoop, Round: roundLoop, Relay: relayLoop,
	}
}

// StartRelay runs the relay in the background for the rest of the test.
func (p *Pipeline) StartRelay(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = p.Relay.RunTenant(ctx, p.Tenant) }()
}

// FoldAll folds until the tenant's stored offset reaches total.
func (p *Pipeline) FoldAll(t *testing.T, total int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() { _ = p.Fold.RunTenant(ctx, p.Tenant) }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		progress, err := p.Store.GetTenantProgress(context.Background(), p.Tenant)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("reading progress: %v", err)
		}
		if progress.EventOffset >= int64(total) {
			cancel()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("fold did not reach offset %d, stopped at %d", total, progress.EventOffset)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// RoundAt runs one round at a fixed time.
func (p *Pipeline) RoundAt(t *testing.T, moment time.Time) {
	t.Helper()
	if err := p.Round.Once(context.Background(), p.Tenant, moment); err != nil {
		t.Fatalf("round at %s: %v", moment, err)
	}
}

// WaitOutboxEmpty waits until the relay has sent every heartbeat the rounds wrote.
func (p *Pipeline) WaitOutboxEmpty(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		rows, err := database.NextOutbox(context.Background(), p.Store.Pool(), p.Tenant, 1)
		if err != nil {
			t.Fatalf("reading outbox: %v", err)
		}
		if len(rows) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("relay did not drain the outbox")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// SentHeartbeats reads every heartbeat the relay put on the tenant's heartbeat topic.
func SentHeartbeats(t *testing.T, tenant string) []SentBeat {
	t.Helper()
	var result []SentBeat
	for _, value := range testenv.ConsumeAll(t, topics.Heartbeat(tenant)) {
		event := cloudevents.Event{}
		if err := event.UnmarshalJSON(value); err != nil {
			t.Fatalf("unmarshaling heartbeat: %v", err)
		}
		data, err := heartbeat.Data(event)
		if err != nil {
			t.Fatalf("decoding heartbeat: %v", err)
		}
		result = append(result, SentBeat{Event: event, Data: data})
	}
	return result
}

// BeatLines renders the heartbeats as comparable text, in topic order.
func BeatLines(beats []SentBeat) []string {
	var lines []string
	for _, beat := range beats {
		lines = append(lines, fmt.Sprintf(
			"%s %s..%s %s", beat.Data.Meter, beat.Data.Usage.From, beat.Data.Usage.To,
			beat.Data.Usage.Quantity,
		))
	}
	return lines
}

// Join renders lines for a failure message.
func Join(lines []string) string {
	out := ""
	for _, line := range lines {
		out += line + "\n"
	}
	return out
}
