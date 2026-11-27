/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package drain_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/osac-notifications/internal/cevent"
	"github.com/osac-project/osac/osac-notifications/internal/dlq"
	"github.com/osac-project/osac/osac-notifications/internal/drain"
	"github.com/osac-project/osac/osac-notifications/internal/fanout"
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
	n int
}

func (r *recordingDLQ) Publish(context.Context, string, []byte, string) error {
	r.n++
	return nil
}

func pendingPayload() []byte {
	b, err := json.Marshal(map[string]any{
		"specversion":     "1.0",
		"id":              "evt-pending",
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

func seedPending(store *intent.TestStore, payload []byte) {
	accepted, err := cevent.Parse(payload)
	Expect(err).ToNot(HaveOccurred())
	_, err = store.Accept(context.Background(), &cevent.Accepted{
		Event:    accepted,
		Tenant:   "acme",
		Source:   "osac-fulfillment",
		ID:       "evt-pending",
		Kind:     "event",
		Type:     "osac.notification.event.resource_created",
		Severity: "info",
		Topic:    "osac.notifications.acme",
		Payload:  payload,
		Data: cevent.Data{
			Title:      "Ready",
			Recipients: []cevent.Recipient{{Type: "user", ID: "u1"}},
		},
	})
	Expect(err).ToNot(HaveOccurred())
}

var _ = Describe("Drain", func() {
	It("fans out pending intents and marks them done", func() {
		store := intent.NewTestStore()
		payload := pendingPayload()
		seedPending(store, payload)

		client := &fakeClient{}
		d := drain.New(slog.Default(), store,
			fanout.NewDispatcher(fanout.NewInbox(slog.Default(), client), nil),
			dlq.Nop{}, time.Second)
		Expect(d.Once(context.Background())).To(Succeed())
		Expect(client.n).To(Equal(1))
		Expect(store.StatusOf("acme", "osac-fulfillment", "evt-pending")).To(Equal(intent.StatusCompleted))
	})

	It("leaves intent pending when fan-out fails transiently", func() {
		store := intent.NewTestStore()
		seedPending(store, pendingPayload())

		client := &fakeClient{err: errors.New("boom")}
		d := drain.New(slog.Default(), store,
			fanout.NewDispatcher(fanout.NewInbox(slog.Default(), client), nil),
			dlq.Nop{}, time.Second)
		Expect(d.Once(context.Background())).To(Succeed())
		Expect(store.StatusOf("acme", "osac-fulfillment", "evt-pending")).To(Equal(intent.StatusPending))
	})

	It("DLQs and marks failed on permanent fan-out errors", func() {
		store := intent.NewTestStore()
		seedPending(store, pendingPayload())

		client := &fakeClient{err: grpcstatus.Error(grpccodes.InvalidArgument, "bad")}
		dead := &recordingDLQ{}
		d := drain.New(slog.Default(), store,
			fanout.NewDispatcher(fanout.NewInbox(slog.Default(), client), nil),
			dead, time.Second)
		Expect(d.Once(context.Background())).To(Succeed())
		Expect(store.StatusOf("acme", "osac-fulfillment", "evt-pending")).To(Equal(intent.StatusFailed))
		Expect(dead.n).To(Equal(1))
	})
})
