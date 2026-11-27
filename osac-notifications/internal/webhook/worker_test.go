/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package webhook

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"

	"github.com/osac-project/osac/osac-notifications/internal/intent"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type fakeDeliveries struct {
	items   []*privatev1.NotificationSubscriptionDelivery
	updates []*privatev1.NotificationSubscriptionDelivery
	filter  string
	order   string
}

func (f *fakeDeliveries) List(_ context.Context, req *privatev1.NotificationSubscriptionDeliveriesListRequest,
	_ ...grpc.CallOption) (*privatev1.NotificationSubscriptionDeliveriesListResponse, error) {
	f.filter = req.GetFilter()
	f.order = req.GetOrder()
	out := make([]*privatev1.NotificationSubscriptionDelivery, 0, len(f.items))
	for _, item := range f.items {
		state := item.GetStatus().GetState()
		if strings.Contains(f.filter, "RETAINED") || strings.Contains(f.filter, " == 2") {
			if state == privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_RETAINED {
				out = append(out, item)
			}
			continue
		}
		if state == privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING {
			out = append(out, item)
		}
	}
	resp := &privatev1.NotificationSubscriptionDeliveriesListResponse{}
	resp.SetItems(out)
	resp.SetSize(int32(len(out)))
	resp.SetTotal(int32(len(out)))
	return resp, nil
}

func (f *fakeDeliveries) Get(context.Context, *privatev1.NotificationSubscriptionDeliveriesGetRequest,
	...grpc.CallOption) (*privatev1.NotificationSubscriptionDeliveriesGetResponse, error) {
	return nil, nil
}

func (f *fakeDeliveries) Update(_ context.Context, req *privatev1.NotificationSubscriptionDeliveriesUpdateRequest,
	_ ...grpc.CallOption) (*privatev1.NotificationSubscriptionDeliveriesUpdateResponse, error) {
	f.updates = append(f.updates, req.GetObject())
	resp := &privatev1.NotificationSubscriptionDeliveriesUpdateResponse{}
	resp.SetObject(req.GetObject())
	return resp, nil
}

type fakeSubs struct {
	sub     *privatev1.NotificationSubscription
	updates []*privatev1.NotificationSubscriptionsUpdateRequest
}

func (f *fakeSubs) Get(context.Context, *privatev1.NotificationSubscriptionsGetRequest,
	...grpc.CallOption) (*privatev1.NotificationSubscriptionsGetResponse, error) {
	resp := &privatev1.NotificationSubscriptionsGetResponse{}
	resp.SetObject(f.sub)
	return resp, nil
}

func (f *fakeSubs) Update(_ context.Context, req *privatev1.NotificationSubscriptionsUpdateRequest,
	_ ...grpc.CallOption) (*privatev1.NotificationSubscriptionsUpdateResponse, error) {
	f.updates = append(f.updates, req)
	f.sub = req.GetObject()
	resp := &privatev1.NotificationSubscriptionsUpdateResponse{}
	resp.SetObject(req.GetObject())
	return resp, nil
}

type fakeSecrets struct{}

func (fakeSecrets) Get(context.Context, *privatev1.SecretsGetRequest,
	...grpc.CallOption) (*privatev1.SecretsGetResponse, error) {
	secret := "whsec_" + base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	resp := &privatev1.SecretsGetResponse{}
	resp.SetObject(privatev1.Secret_builder{
		Data: map[string][]byte{"value": []byte(secret)},
	}.Build())
	return resp, nil
}

type fakeIntents struct {
	payload []byte
}

func (f fakeIntents) Get(context.Context, string, string, string) (intent.Record, bool, error) {
	return intent.Record{Payload: f.payload}, true, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func baseSub() *privatev1.NotificationSubscription {
	return privatev1.NotificationSubscription_builder{
		Id: "sub-1",
		Metadata: privatev1.Metadata_builder{
			Name:   "sub-1",
			Tenant: "acme",
		}.Build(),
		Spec: privatev1.NotificationSubscriptionSpec_builder{
			WebhookUrl: "https://example.com/hook",
			Filter:     `true`,
		}.Build(),
		Status: privatev1.NotificationSubscriptionStatus_builder{
			SigningSecret: privatev1.SecretLocalReference_builder{Id: "sec-1", Name: "whsec-sub-1"}.Build(),
		}.Build(),
	}.Build()
}

func baseDelivery(state privatev1.NotificationSubscriptionDeliveryState) *privatev1.NotificationSubscriptionDelivery {
	return privatev1.NotificationSubscriptionDelivery_builder{
		Id: "del-1",
		Metadata: privatev1.Metadata_builder{
			Name:   "del-1",
			Tenant: "acme",
		}.Build(),
		Spec: privatev1.NotificationSubscriptionDeliverySpec_builder{
			Subscription: privatev1.NotificationSubscriptionLocalReference_builder{
				Id: "sub-1", Name: "sub-1",
			}.Build(),
			Event: privatev1.NotificationSubscriptionDeliveryEvent_builder{
				Id: "evt-1", Source: "osac-fulfillment",
			}.Build(),
		}.Build(),
		Status: privatev1.NotificationSubscriptionDeliveryStatus_builder{
			State: state,
		}.Build(),
	}.Build()
}

func testWorker(d *fakeDeliveries, s *fakeSubs, status int) *Worker {
	w := NewWorker(slog.Default(), d, s, fakeSecrets{}, fakeIntents{payload: []byte(`{"ok":true}`)}, time.Second)
	w.validateURL = func(string) error { return nil }
	w.now = func() time.Time { return time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC) }
	w.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(bytes.NewReader(nil)),
			Header:     make(http.Header),
		}, nil
	})}
	return w
}

var _ = Describe("Worker", func() {
	It("promotes retained deliveries when the subscription is unpaused", func() {
		d := &fakeDeliveries{items: []*privatev1.NotificationSubscriptionDelivery{
			baseDelivery(privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_RETAINED),
		}}
		s := &fakeSubs{sub: baseSub()}
		w := testWorker(d, s, http.StatusOK)
		Expect(w.promoteRetained(context.Background())).To(Succeed())
		Expect(d.updates).ToNot(BeEmpty())
		Expect(d.updates[0].GetStatus().GetState()).To(Equal(
			privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING))
	})

	It("marks delivered on 2xx and updates subscription status", func() {
		d := &fakeDeliveries{items: []*privatev1.NotificationSubscriptionDelivery{
			baseDelivery(privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING),
		}}
		s := &fakeSubs{sub: baseSub()}
		w := testWorker(d, s, http.StatusOK)
		Expect(w.Once(context.Background())).To(Succeed())
		Expect(d.updates).ToNot(BeEmpty())
		Expect(d.updates[0].GetStatus().GetState()).To(Equal(
			privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_DELIVERED))
		Expect(s.updates).ToNot(BeEmpty())
		Expect(s.updates[0].GetUpdateMask().GetPaths()).To(ContainElement("status.last_delivery_status"))
	})

	It("retries 5xx and schedules next_attempt_time", func() {
		d := &fakeDeliveries{items: []*privatev1.NotificationSubscriptionDelivery{
			baseDelivery(privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING),
		}}
		s := &fakeSubs{sub: baseSub()}
		w := testWorker(d, s, http.StatusBadGateway)
		Expect(w.Once(context.Background())).To(Succeed())
		Expect(d.updates).ToNot(BeEmpty())
		Expect(d.updates[0].GetStatus().GetState()).To(Equal(
			privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING))
		Expect(d.updates[0].GetStatus().GetNextAttemptTime()).ToNot(BeEmpty())
	})

	It("fails permanently on 4xx including 429", func() {
		d := &fakeDeliveries{items: []*privatev1.NotificationSubscriptionDelivery{
			baseDelivery(privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING),
		}}
		s := &fakeSubs{sub: baseSub()}
		w := testWorker(d, s, http.StatusTooManyRequests)
		Expect(w.Once(context.Background())).To(Succeed())
		Expect(d.updates).ToNot(BeEmpty())
		Expect(d.updates[0].GetStatus().GetState()).To(Equal(
			privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_FAILED))
	})

	It("auto-pauses after sustained failure without setting spec.paused", func() {
		del := baseDelivery(privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING)
		del.GetStatus().SetFirstFailureTime(time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC).Format(time.RFC3339))
		d := &fakeDeliveries{items: []*privatev1.NotificationSubscriptionDelivery{del}}
		s := &fakeSubs{sub: baseSub()}
		w := testWorker(d, s, http.StatusBadGateway)
		Expect(w.Once(context.Background())).To(Succeed())
		Expect(d.updates).ToNot(BeEmpty())
		Expect(d.updates[0].GetStatus().GetState()).To(Equal(
			privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_FAILED))
		Expect(s.updates).ToNot(BeEmpty())
		mask := s.updates[0].GetUpdateMask()
		Expect(mask.GetPaths()).To(ContainElements("status.auto_paused", "status.last_delivery_status"))
		Expect(mask.GetPaths()).ToNot(ContainElement("spec.paused"))
		Expect(s.sub.GetStatus().GetAutoPaused()).To(BeTrue())
		Expect(s.sub.GetSpec().GetPaused()).To(BeFalse())
	})

	It("restarts the sustained window when first_failure_time is corrupt", func() {
		del := baseDelivery(privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING)
		del.GetStatus().SetFirstFailureTime("not-a-timestamp")
		d := &fakeDeliveries{items: []*privatev1.NotificationSubscriptionDelivery{del}}
		s := &fakeSubs{sub: baseSub()}
		w := testWorker(d, s, http.StatusBadGateway)
		Expect(w.Once(context.Background())).To(Succeed())
		Expect(d.updates).ToNot(BeEmpty())
		Expect(d.updates[0].GetStatus().GetState()).To(Equal(
			privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING))
		Expect(d.updates[0].GetStatus().GetFirstFailureTime()).To(Equal("2026-11-27T12:00:00Z"))
	})
})

var _ = Describe("duePendingFilter", func() {
	It("selects due pending deliveries and is used with next_attempt_time order", func() {
		now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
		filter := duePendingFilter(now)
		Expect(filter).To(ContainSubstring(`this.status.state == 1`))
		Expect(filter).To(ContainSubstring(`next_attempt_time == ""`))
		Expect(filter).To(ContainSubstring(`"2026-11-27T12:00:00Z"`))

		d := &fakeDeliveries{items: []*privatev1.NotificationSubscriptionDelivery{
			baseDelivery(privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING),
		}}
		s := &fakeSubs{sub: baseSub()}
		w := testWorker(d, s, http.StatusOK)
		Expect(w.Once(context.Background())).To(Succeed())
		Expect(d.filter).To(Equal(filter))
		Expect(d.order).To(Equal("status.next_attempt_time"))
	})
})
