/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package ingest_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/osac-notifications/internal/dlq"
	"github.com/osac-project/osac/osac-notifications/internal/fanout"
	"github.com/osac-project/osac/osac-notifications/internal/grant"
	"github.com/osac-project/osac/osac-notifications/internal/ingest"
	"github.com/osac-project/osac/osac-notifications/internal/intent"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type fakeClient struct {
	n   int
	err error
}

func (f *fakeClient) Create(context.Context, *privatev1.NotificationsCreateRequest,
	...grpc.CallOption) (*privatev1.NotificationsCreateResponse, error) {
	f.n++
	if f.err != nil {
		return nil, f.err
	}
	return &privatev1.NotificationsCreateResponse{}, nil
}

type recordingDLQ struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (r *recordingDLQ) Publish(_ context.Context, tenant string, _ []byte, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.calls = append(r.calls, tenant+":"+reason)
	return nil
}

func (r *recordingDLQ) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func body() []byte {
	b, err := json.Marshal(map[string]any{
		"specversion":     "1.0",
		"id":              "evt-1",
		"source":          "osac-fulfillment",
		"type":            "osac.notification.event.resource_created",
		"time":            "2026-11-07T18:05:00Z",
		"datacontenttype": "application/json",
		"osactenant":      "acme",
		"kind":            "event",
		"severity":        "info",
		"data": map[string]any{
			"title": "Ready",
			"recipients": []map[string]any{
				{"type": "user", "id": "u1"},
			},
		},
	})
	Expect(err).ToNot(HaveOccurred())
	return b
}

var _ = Describe("Ingest", func() {
	It("accepts valid events, fans out, and marks intent done", func() {
		store := intent.NewTestStore()
		client := &fakeClient{}
		pipeline := ingest.New(
			slog.Default(),
			grant.DefaultMap,
			store,
			fanout.NewDispatcher(fanout.NewInbox(slog.Default(), client), nil),
			dlq.Nop{},
		)
		outcome, err := pipeline.Handle(context.Background(), "osac.notifications.acme", body())
		Expect(err).ToNot(HaveOccurred())
		Expect(outcome).To(Equal(ingest.Accepted))
		Expect(client.n).To(Equal(1))
		Expect(store.StatusOf("acme", "osac-fulfillment", "evt-1")).To(Equal(intent.StatusCompleted))
	})

	It("still accepts when eager fan-out fails transiently so Kafka can commit", func() {
		store := intent.NewTestStore()
		client := &fakeClient{err: errors.New("unavailable")}
		pipeline := ingest.New(
			slog.Default(),
			grant.DefaultMap,
			store,
			fanout.NewDispatcher(fanout.NewInbox(slog.Default(), client), nil),
			dlq.Nop{},
		)
		outcome, err := pipeline.Handle(context.Background(), "osac.notifications.acme", body())
		Expect(err).ToNot(HaveOccurred())
		Expect(outcome).To(Equal(ingest.Accepted))
		Expect(store.StatusOf("acme", "osac-fulfillment", "evt-1")).To(Equal(intent.StatusPending))
	})

	It("DLQs and marks failed on permanent fan-out errors", func() {
		store := intent.NewTestStore()
		client := &fakeClient{err: grpcstatus.Error(grpccodes.InvalidArgument, "bad name")}
		dead := &recordingDLQ{}
		pipeline := ingest.New(
			slog.Default(),
			grant.DefaultMap,
			store,
			fanout.NewDispatcher(fanout.NewInbox(slog.Default(), client), nil),
			dead,
		)
		outcome, err := pipeline.Handle(context.Background(), "osac.notifications.acme", body())
		Expect(err).ToNot(HaveOccurred())
		Expect(outcome).To(Equal(ingest.Accepted))
		Expect(store.StatusOf("acme", "osac-fulfillment", "evt-1")).To(Equal(intent.StatusFailed))
		Expect(dead.len()).To(Equal(1))
	})

	It("skips fan-out when intent is already done", func() {
		store := intent.NewTestStore()
		client := &fakeClient{}
		pipeline := ingest.New(
			slog.Default(),
			grant.DefaultMap,
			store,
			fanout.NewDispatcher(fanout.NewInbox(slog.Default(), client), nil),
			dlq.Nop{},
		)
		_, err := pipeline.Handle(context.Background(), "osac.notifications.acme", body())
		Expect(err).ToNot(HaveOccurred())
		client.n = 0
		outcome, err := pipeline.Handle(context.Background(), "osac.notifications.acme", body())
		Expect(err).ToNot(HaveOccurred())
		Expect(outcome).To(Equal(ingest.Accepted))
		Expect(client.n).To(BeZero())
	})

	It("rejects invalid events to DLQ without creating an intent", func() {
		store := intent.NewTestStore()
		client := &fakeClient{}
		dead := &recordingDLQ{}
		pipeline := ingest.New(
			slog.Default(),
			grant.DefaultMap,
			store,
			fanout.NewDispatcher(fanout.NewInbox(slog.Default(), client), nil),
			dead,
		)
		bad, err := json.Marshal(map[string]any{
			"specversion": "1.0",
			"id":          "evt-1",
			"source":      "osac-fulfillment",
			"type":        "osac.notification.announcement.x",
			"time":        "2026-11-07T18:05:00Z",
			"osactenant":  "acme",
			"kind":        "announcement",
			"severity":    "info",
			"data":        map[string]any{"title": "x"},
		})
		Expect(err).ToNot(HaveOccurred())
		outcome, err := pipeline.Handle(context.Background(), "osac.notifications.acme", bad)
		Expect(err).ToNot(HaveOccurred())
		Expect(outcome).To(Equal(ingest.Rejected))
		Expect(client.n).To(BeZero())
		Expect(store.StatusOf("acme", "osac-fulfillment", "evt-1")).To(BeEmpty())
		Expect(dead.len()).To(Equal(1))
	})
})
