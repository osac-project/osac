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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/osac-notifications/internal/intent"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	defaultBatch       = 50
	defaultHTTPTimeout = 30 * time.Second
)

// DeliveriesAPI lists and updates deliveries.
type DeliveriesAPI interface {
	List(ctx context.Context, request *privatev1.NotificationSubscriptionDeliveriesListRequest,
		opts ...grpc.CallOption) (*privatev1.NotificationSubscriptionDeliveriesListResponse, error)
	Get(ctx context.Context, request *privatev1.NotificationSubscriptionDeliveriesGetRequest,
		opts ...grpc.CallOption) (*privatev1.NotificationSubscriptionDeliveriesGetResponse, error)
	Update(ctx context.Context, request *privatev1.NotificationSubscriptionDeliveriesUpdateRequest,
		opts ...grpc.CallOption) (*privatev1.NotificationSubscriptionDeliveriesUpdateResponse, error)
}

// SubscriptionsAPI gets and updates subscriptions.
type SubscriptionsAPI interface {
	Get(ctx context.Context, request *privatev1.NotificationSubscriptionsGetRequest,
		opts ...grpc.CallOption) (*privatev1.NotificationSubscriptionsGetResponse, error)
	Update(ctx context.Context, request *privatev1.NotificationSubscriptionsUpdateRequest,
		opts ...grpc.CallOption) (*privatev1.NotificationSubscriptionsUpdateResponse, error)
}

// SecretsAPI loads tenant Secrets that hold webhook signing material.
type SecretsAPI interface {
	Get(ctx context.Context, request *privatev1.SecretsGetRequest,
		opts ...grpc.CallOption) (*privatev1.SecretsGetResponse, error)
}

// IntentLookup loads the original CloudEvent payload for a delivery.
type IntentLookup interface {
	Get(ctx context.Context, tenant, source, id string) (intent.Record, bool, error)
}

// Worker sends pending webhook deliveries and promotes retained ones after unpause.
type Worker struct {
	logger     *slog.Logger
	deliveries DeliveriesAPI
	subs       SubscriptionsAPI
	secrets    SecretsAPI
	intents    IntentLookup
	httpClient  *http.Client
	validateURL func(string) error // nil → ValidateURL; tests may stub
	batch       int32
	interval    time.Duration
	now         func() time.Time
}

// NewWorker builds a webhook delivery worker.
func NewWorker(logger *slog.Logger, deliveries DeliveriesAPI, subs SubscriptionsAPI,
	secrets SecretsAPI, intents IntentLookup, interval time.Duration) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Worker{
		logger:     logger,
		deliveries: deliveries,
		subs:       subs,
		secrets:    secrets,
		intents:    intents,
		httpClient: NewHTTPClient(defaultHTTPTimeout),
		batch:      defaultBatch,
		interval:   interval,
		now:        time.Now,
	}
}

// Run repeatedly processes deliveries until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if err := w.Once(ctx); err != nil && ctx.Err() == nil {
			w.logger.ErrorContext(ctx, "Webhook worker pass failed", slog.Any("error", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Once promotes retained deliveries and attempts due pending posts.
func (w *Worker) Once(ctx context.Context) error {
	if w == nil || w.deliveries == nil || w.subs == nil {
		return nil
	}
	if err := w.promoteRetained(ctx); err != nil {
		return err
	}
	return w.sendPending(ctx)
}

func (w *Worker) promoteRetained(ctx context.Context) error {
	offset := int32(0)
	for {
		req := &privatev1.NotificationSubscriptionDeliveriesListRequest{}
		req.SetOffset(offset)
		req.SetLimit(w.batch)
		req.SetOrder("id")
		// CEL enum filters use numeric values (same as other FS List callers).
		req.SetFilter(fmt.Sprintf("this.status.state == %d",
			int32(privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_RETAINED)))
		resp, err := w.deliveries.List(ctx, req)
		if err != nil {
			return fmt.Errorf("list retained deliveries: %w", err)
		}
		if resp.GetSize() == 0 {
			return nil
		}
		promoted := 0
		for _, delivery := range resp.GetItems() {
			subID := delivery.GetSpec().GetSubscription().GetId()
			subResp, err := w.subs.Get(ctx, privatev1.NotificationSubscriptionsGetRequest_builder{Id: subID}.Build())
			if err != nil {
				w.logger.ErrorContext(ctx, "Failed to get subscription for retained delivery",
					slog.String("delivery_id", delivery.GetId()),
					slog.Any("error", err),
				)
				continue
			}
			sub := subResp.GetObject()
			if sub.GetSpec().GetPaused() || sub.GetStatus().GetAutoPaused() || sub.GetMetadata().HasDeletionTimestamp() {
				continue
			}
			delivery.GetStatus().SetState(privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING)
			delivery.GetStatus().SetNextAttemptTime("")
			if err := w.updateDelivery(ctx, delivery, "status.state", "status.next_attempt_time"); err != nil {
				w.logger.ErrorContext(ctx, "Failed to promote retained delivery",
					slog.String("delivery_id", delivery.GetId()),
					slog.Any("error", err),
				)
				continue
			}
			promoted++
		}
		if promoted == 0 {
			// Entire page still retained (paused/auto-paused); skip past it.
			offset += resp.GetSize()
		}
		// When promoted > 0 those rows leave RETAINED, so keep the same offset.
		if offset >= resp.GetTotal() {
			return nil
		}
	}
}

func (w *Worker) sendPending(ctx context.Context) error {
	now := w.now().UTC()
	req := &privatev1.NotificationSubscriptionDeliveriesListRequest{}
	req.SetLimit(w.batch)
	req.SetFilter(duePendingFilter(now))
	req.SetOrder("status.next_attempt_time")
	resp, err := w.deliveries.List(ctx, req)
	if err != nil {
		return fmt.Errorf("list pending deliveries: %w", err)
	}
	for _, delivery := range resp.GetItems() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.attempt(ctx, delivery, now); err != nil {
			w.logger.ErrorContext(ctx, "Webhook delivery attempt failed to persist",
				slog.String("delivery_id", delivery.GetId()),
				slog.Any("error", err),
			)
		}
	}
	return nil
}

// duePendingFilter selects PENDING deliveries that are due for an attempt.
// Empty next_attempt_time means "due now"; RFC3339 strings compare lexicographically.
func duePendingFilter(now time.Time) string {
	return fmt.Sprintf(
		`this.status.state == %d && (this.status.next_attempt_time == "" || this.status.next_attempt_time <= %s)`,
		int32(privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING),
		strconv.Quote(now.Format(time.RFC3339)),
	)
}

func (w *Worker) attempt(ctx context.Context, delivery *privatev1.NotificationSubscriptionDelivery, now time.Time) error {
	subID := delivery.GetSpec().GetSubscription().GetId()
	subResp, err := w.subs.Get(ctx, privatev1.NotificationSubscriptionsGetRequest_builder{Id: subID}.Build())
	if err != nil {
		return err
	}
	sub := subResp.GetObject()
	if sub.GetMetadata().HasDeletionTimestamp() {
		delivery.GetStatus().SetState(privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_FAILED)
		delivery.GetStatus().SetLastError("subscription deleted")
		return w.updateDelivery(ctx, delivery, "status.state", "status.last_error")
	}
	if sub.GetSpec().GetPaused() || sub.GetStatus().GetAutoPaused() {
		delivery.GetStatus().SetState(privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_RETAINED)
		return w.updateDelivery(ctx, delivery, "status.state")
	}

	url := sub.GetSpec().GetWebhookUrl()
	if err := w.checkURL(url); err != nil {
		return w.recordFailure(ctx, delivery, sub, now, 0, err.Error(), false)
	}

	body, err := w.payload(ctx, delivery)
	if err != nil {
		return w.recordFailure(ctx, delivery, sub, now, 0, err.Error(), false)
	}

	signingSecret, err := w.signingSecret(ctx, sub)
	if err != nil {
		return w.recordFailure(ctx, delivery, sub, now, 0, err.Error(), false)
	}
	msgID := delivery.GetId()
	headers, err := SignHeaders(msgID, signingSecret, now, body)
	if err != nil {
		return w.recordFailure(ctx, delivery, sub, now, 0, err.Error(), false)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return w.recordFailure(ctx, delivery, sub, now, 0, err.Error(), false)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return w.recordFailure(ctx, delivery, sub, now, 0, err.Error(), true)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		delivery.GetStatus().SetState(privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_DELIVERED)
		delivery.GetStatus().SetLastHttpStatus(int32(resp.StatusCode))
		delivery.GetStatus().SetLastError("")
		delivery.GetStatus().SetNextAttemptTime("")
		delivery.GetStatus().SetAttemptCount(delivery.GetStatus().GetAttemptCount() + 1)
		if err := w.updateDelivery(ctx, delivery,
			"status.state", "status.last_http_status", "status.last_error",
			"status.next_attempt_time", "status.attempt_count"); err != nil {
			return err
		}
		return w.touchSubscriptionStatus(ctx, sub, "delivered")
	}
	// Design: retry on timeouts, connection errors, and 5xx only.
	retryable := resp.StatusCode >= 500
	return w.recordFailure(ctx, delivery, sub, now, int32(resp.StatusCode),
		fmt.Sprintf("webhook returned HTTP %d", resp.StatusCode), retryable)
}

func (w *Worker) checkURL(raw string) error {
	if w.validateURL != nil {
		return w.validateURL(raw)
	}
	return ValidateURL(raw)
}

func (w *Worker) signingSecret(ctx context.Context, sub *privatev1.NotificationSubscription) (string, error) {
	if w.secrets == nil {
		return "", fmt.Errorf("secrets client is not configured")
	}
	ref := sub.GetStatus().GetSigningSecret()
	if ref == nil || ref.GetId() == "" {
		return "", fmt.Errorf("subscription signing secret reference is missing")
	}
	resp, err := w.secrets.Get(ctx, privatev1.SecretsGetRequest_builder{Id: ref.GetId()}.Build())
	if err != nil {
		return "", fmt.Errorf("get signing secret: %w", err)
	}
	data := resp.GetObject().GetData()
	value := data["value"]
	if len(value) == 0 {
		return "", fmt.Errorf("signing secret is missing data key %q", "value")
	}
	return string(value), nil
}

func (w *Worker) payload(ctx context.Context, delivery *privatev1.NotificationSubscriptionDelivery) ([]byte, error) {
	if w.intents == nil {
		return nil, fmt.Errorf("intent lookup is not configured")
	}
	tenant := delivery.GetMetadata().GetTenant()
	source := delivery.GetSpec().GetEvent().GetSource()
	id := delivery.GetSpec().GetEvent().GetId()
	rec, ok, err := w.intents.Get(ctx, tenant, source, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("intent not found for event %s/%s", source, id)
	}
	return rec.Payload, nil
}

func (w *Worker) recordFailure(ctx context.Context, delivery *privatev1.NotificationSubscriptionDelivery,
	sub *privatev1.NotificationSubscription, now time.Time, httpStatus int32, message string, retryable bool) error {
	status := delivery.GetStatus()
	status.SetAttemptCount(status.GetAttemptCount() + 1)
	status.SetLastHttpStatus(httpStatus)
	status.SetLastError(message)
	if status.GetFirstFailureTime() == "" {
		status.SetFirstFailureTime(now.Format(time.RFC3339))
	}

	firstFailure, parseErr := time.Parse(time.RFC3339, status.GetFirstFailureTime())
	if parseErr != nil {
		// Corrupt timestamp: restart the sustained-failure window from now.
		firstFailure = now
		status.SetFirstFailureTime(now.Format(time.RFC3339))
	}
	if !retryable || SustainedFailure(firstFailure, now) {
		status.SetState(privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_FAILED)
		status.SetNextAttemptTime("")
		if err := w.updateDelivery(ctx, delivery,
			"status.state", "status.attempt_count", "status.last_http_status",
			"status.last_error", "status.first_failure_time", "status.next_attempt_time"); err != nil {
			return err
		}
		if SustainedFailure(firstFailure, now) {
			return w.autoPause(ctx, sub, "failed")
		}
		return w.touchSubscriptionStatus(ctx, sub, "failed")
	}

	delay := NextAttemptDelay(int(status.GetAttemptCount()))
	status.SetNextAttemptTime(now.Add(delay).Format(time.RFC3339))
	status.SetState(privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING)
	return w.updateDelivery(ctx, delivery,
		"status.state", "status.attempt_count", "status.last_http_status",
		"status.last_error", "status.first_failure_time", "status.next_attempt_time")
}

func (w *Worker) autoPause(ctx context.Context, sub *privatev1.NotificationSubscription, lastStatus string) error {
	if sub.GetStatus() == nil {
		sub.SetStatus(&privatev1.NotificationSubscriptionStatus{})
	}
	sub.GetStatus().SetAutoPaused(true)
	sub.GetStatus().SetLastDeliveryStatus(lastStatus)
	req := privatev1.NotificationSubscriptionsUpdateRequest_builder{
		Object: sub,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
			"status.auto_paused", "status.last_delivery_status",
		}},
	}.Build()
	_, err := w.subs.Update(ctx, req)
	return err
}

func (w *Worker) touchSubscriptionStatus(ctx context.Context, sub *privatev1.NotificationSubscription,
	lastStatus string) error {
	if sub.GetStatus() == nil {
		sub.SetStatus(&privatev1.NotificationSubscriptionStatus{})
	}
	sub.GetStatus().SetLastDeliveryStatus(lastStatus)
	req := privatev1.NotificationSubscriptionsUpdateRequest_builder{
		Object:     sub,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.last_delivery_status"}},
	}.Build()
	_, err := w.subs.Update(ctx, req)
	return err
}

func (w *Worker) updateDelivery(ctx context.Context, delivery *privatev1.NotificationSubscriptionDelivery,
	paths ...string) error {
	req := privatev1.NotificationSubscriptionDeliveriesUpdateRequest_builder{
		Object:     delivery,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
	}.Build()
	_, err := w.deliveries.Update(ctx, req)
	return err
}
