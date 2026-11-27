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
	"log/slog"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/osac-notifications/internal/cevent"
	"github.com/osac-project/osac/osac-notifications/internal/fanout"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type fakeClient struct {
	calls   []*privatev1.Notification
	err     error
	errOnce error
}

func (f *fakeClient) Create(_ context.Context, request *privatev1.NotificationsCreateRequest,
	_ ...grpc.CallOption) (*privatev1.NotificationsCreateResponse, error) {
	f.calls = append(f.calls, request.GetObject())
	if f.errOnce != nil {
		err := f.errOnce
		f.errOnce = nil
		return nil, err
	}
	if f.err != nil {
		return nil, f.err
	}
	return &privatev1.NotificationsCreateResponse{}, nil
}

var _ = Describe("Inbox fan-out", func() {
	It("creates one notification per USER recipient", func() {
		client := &fakeClient{}
		inbox := fanout.NewInbox(slog.Default(), client)
		accepted := &cevent.Accepted{
			Tenant:   "acme",
			Source:   "osac-fulfillment",
			ID:       "evt-1",
			Kind:     "event",
			Type:     "osac.notification.event.resource_created",
			Severity: "info",
			Data: cevent.Data{
				Title: "Ready",
				Recipients: []cevent.Recipient{
					{Type: "user", ID: "u1"},
					{Type: "group", ID: "g1"},
					{Type: "user", ID: "u2"},
				},
			},
		}
		Expect(inbox.FanOut(context.Background(), accepted)).To(Succeed())
		Expect(client.calls).To(HaveLen(2))
		Expect(client.calls[0].GetSpec().GetRecipientId()).To(Equal("u1"))
		Expect(client.calls[1].GetSpec().GetRecipientId()).To(Equal("u2"))
		Expect(client.calls[0].GetMetadata().GetTenant()).To(Equal("acme"))
		Expect(client.calls[0].GetSpec().GetKind()).To(Equal(
			privatev1.NotificationKind_NOTIFICATION_KIND_EVENT))
	})

	It("treats AlreadyExists as success", func() {
		client := &fakeClient{errOnce: grpcstatus.Error(grpccodes.AlreadyExists, "exists")}
		inbox := fanout.NewInbox(slog.Default(), client)
		accepted := &cevent.Accepted{
			Tenant:   "acme",
			Source:   "osac-fulfillment",
			ID:       "evt-1",
			Kind:     "event",
			Type:     "osac.notification.event.resource_created",
			Severity: "info",
			Data: cevent.Data{
				Title:      "Ready",
				Recipients: []cevent.Recipient{{Type: "user", ID: "u1"}},
			},
		}
		Expect(inbox.FanOut(context.Background(), accepted)).To(Succeed())
		Expect(client.calls).To(HaveLen(1))
	})

	It("creates inbox items when event ids are longer than a DNS label", func() {
		client := &fakeClient{}
		inbox := fanout.NewInbox(slog.Default(), client)
		accepted := &cevent.Accepted{
			Tenant:   "acme",
			Source:   "osac-fulfillment",
			ID:       "evt-" + strings.Repeat("a", 80),
			Kind:     "event",
			Type:     "osac.notification.event.resource_created",
			Severity: "info",
			Data: cevent.Data{
				Title:      "Ready",
				Recipients: []cevent.Recipient{{Type: "user", ID: "u1"}},
			},
		}
		Expect(inbox.FanOut(context.Background(), accepted)).To(Succeed())
		Expect(client.calls).To(HaveLen(1))
		name := client.calls[0].GetMetadata().GetName()
		Expect(name).To(HavePrefix("inbox-"))
		Expect(len(name)).To(BeNumerically("<=", 63))
	})
})
