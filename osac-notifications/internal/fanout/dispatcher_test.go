/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package fanout_test

import (
	"context"
	"errors"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"

	"github.com/osac-project/osac/osac-notifications/internal/cevent"
	"github.com/osac-project/osac/osac-notifications/internal/fanout"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type recordingMatcher struct {
	n   int
	err error
}

func (m *recordingMatcher) Match(ctx context.Context, accepted *cevent.Accepted) error {
	m.n++
	return m.err
}

type dispatcherFakeClient struct {
	n   int
	err error
}

func (f *dispatcherFakeClient) Create(ctx context.Context, request *privatev1.NotificationsCreateRequest,
	opts ...grpc.CallOption) (*privatev1.NotificationsCreateResponse, error) {
	f.n++
	if f.err != nil {
		return nil, f.err
	}
	return privatev1.NotificationsCreateResponse_builder{
		Object: privatev1.Notification_builder{Id: "n1"}.Build(),
	}.Build(), nil
}

func dispatcherAccepted() *cevent.Accepted {
	return &cevent.Accepted{
		Tenant:   "acme",
		Source:   "osac-fulfillment",
		ID:       "evt-1",
		Kind:     "event",
		Type:     "osac.notification.event.resource_created",
		Severity: "info",
		Data: cevent.Data{
			Title: "hello",
			Recipients: []cevent.Recipient{
				{Type: "USER", ID: "user-1"},
			},
		},
	}
}

var _ = Describe("Dispatcher", func() {
	It("runs inbox then matcher", func() {
		client := &dispatcherFakeClient{}
		matcher := &recordingMatcher{}
		d := fanout.NewDispatcher(fanout.NewInbox(slog.Default(), client), matcher)
		Expect(d.FanOut(context.Background(), dispatcherAccepted())).To(Succeed())
		Expect(client.n).To(Equal(1))
		Expect(matcher.n).To(Equal(1))
	})

	It("skips matcher when nil", func() {
		client := &dispatcherFakeClient{}
		d := fanout.NewDispatcher(fanout.NewInbox(slog.Default(), client), nil)
		Expect(d.FanOut(context.Background(), dispatcherAccepted())).To(Succeed())
		Expect(client.n).To(Equal(1))
	})

	It("does not call matcher when inbox fails", func() {
		client := &dispatcherFakeClient{err: errors.New("boom")}
		matcher := &recordingMatcher{}
		d := fanout.NewDispatcher(fanout.NewInbox(slog.Default(), client), matcher)
		Expect(d.FanOut(context.Background(), dispatcherAccepted())).To(HaveOccurred())
		Expect(matcher.n).To(BeZero())
	})

	It("wraps matcher errors", func() {
		client := &dispatcherFakeClient{}
		matcher := &recordingMatcher{err: errors.New("match failed")}
		d := fanout.NewDispatcher(fanout.NewInbox(slog.Default(), client), matcher)
		err := d.FanOut(context.Background(), dispatcherAccepted())
		Expect(err).To(MatchError(ContainSubstring("subscription match")))
	})
})
