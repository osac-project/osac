/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package match

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"

	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/osac-notifications/internal/cevent"
	"github.com/osac-project/osac/osac-notifications/internal/fanout"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// SubscriptionsClient lists webhook subscriptions.
type SubscriptionsClient interface {
	List(ctx context.Context, request *privatev1.NotificationSubscriptionsListRequest,
		opts ...grpc.CallOption) (*privatev1.NotificationSubscriptionsListResponse, error)
}

// DeliveriesClient creates webhook deliveries.
type DeliveriesClient interface {
	Create(ctx context.Context, request *privatev1.NotificationSubscriptionDeliveriesCreateRequest,
		opts ...grpc.CallOption) (*privatev1.NotificationSubscriptionDeliveriesCreateResponse, error)
}

// Matcher creates deliveries for subscriptions that match an accepted CloudEvent.
type Matcher struct {
	logger      slog.Logger
	subs        SubscriptionsClient
	deliveries  DeliveriesClient
	listLimit   int32
}

// New builds a subscription matcher.
func New(logger *slog.Logger, subs SubscriptionsClient, deliveries DeliveriesClient) *Matcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Matcher{logger: *logger, subs: subs, deliveries: deliveries, listLimit: 200}
}

// Match lists tenant subscriptions and creates a delivery for each CEL match.
// Skipped when MatchSubs is false. AlreadyExists is treated as success.
func (m *Matcher) Match(ctx context.Context, accepted *cevent.Accepted) error {
	if m == nil || m.subs == nil || m.deliveries == nil {
		return fmt.Errorf("subscription matcher is not configured")
	}
	if accepted == nil || !accepted.MatchSubs {
		return nil
	}

	offset := int32(0)
	for {
		req := &privatev1.NotificationSubscriptionsListRequest{}
		req.SetOffset(offset)
		req.SetLimit(m.listLimit)
		req.SetFilter(fmt.Sprintf("this.metadata.tenant == %s", strconv.Quote(accepted.Tenant)))
		resp, err := m.subs.List(ctx, req)
		if err != nil {
			return fmt.Errorf("list subscriptions: %w", err)
		}
		for _, sub := range resp.GetItems() {
			if err := m.matchOne(ctx, accepted, sub); err != nil {
				return err
			}
		}
		offset += resp.GetSize()
		if offset >= resp.GetTotal() || resp.GetSize() == 0 {
			return nil
		}
	}
}

func (m *Matcher) matchOne(ctx context.Context, accepted *cevent.Accepted,
	sub *privatev1.NotificationSubscription) error {
	if sub.GetMetadata().HasDeletionTimestamp() {
		return nil
	}
	filter := sub.GetSpec().GetFilter()
	ok, err := EvalFilter(filter, accepted)
	if err != nil {
		m.logger.ErrorContext(ctx, "Subscription filter evaluation failed; skipping",
			slog.String("subscription_id", sub.GetId()),
			slog.Any("error", err),
		)
		return nil
	}
	if !ok {
		return nil
	}

	state := privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING
	if sub.GetSpec().GetPaused() || sub.GetStatus().GetAutoPaused() {
		state = privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_RETAINED
	}

	name, nameErr := deliveryName(sub.GetId(), accepted.ID)
	if nameErr != nil {
		return nameErr
	}
	object := privatev1.NotificationSubscriptionDelivery_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   name,
			Tenant: accepted.Tenant,
		}.Build(),
		Spec: privatev1.NotificationSubscriptionDeliverySpec_builder{
			Subscription: privatev1.NotificationSubscriptionLocalReference_builder{
				Id:   sub.GetId(),
				Name: sub.GetMetadata().GetName(),
			}.Build(),
			Event: privatev1.NotificationSubscriptionDeliveryEvent_builder{
				Id:     accepted.ID,
				Source: accepted.Source,
			}.Build(),
		}.Build(),
		Status: privatev1.NotificationSubscriptionDeliveryStatus_builder{
			State: state,
		}.Build(),
	}.Build()

	_, err = m.deliveries.Create(ctx, privatev1.NotificationSubscriptionDeliveriesCreateRequest_builder{
		Object: object,
	}.Build())
	if err != nil {
		if grpcstatus.Code(err) == grpccodes.AlreadyExists {
			return nil
		}
		return fmt.Errorf("create delivery for subscription %q: %w", sub.GetId(), err)
	}
	return nil
}

// deliveryName builds a deterministic RFC 1123 DNS label from subscription + event
// identity. Raw ids are often UUIDs / opaque strings that exceed 63 chars when
// concatenated; uniqueness for the match is enforced by the DB unique index on
// (tenant, subscription, event), so metadata.name only needs to be stable + valid.
func deliveryName(subscriptionID, eventID string) (string, error) {
	if subscriptionID == "" || eventID == "" {
		return "", fmt.Errorf("%w: subscription id and event id are required for delivery name",
			fanout.ErrNonRetryable)
	}
	sum := sha256.Sum256([]byte(subscriptionID + "\x00" + eventID))
	// "del-" + 32 hex chars = 36 ≤ 63; hex keeps the label DNS-1123 safe.
	return "del-" + hex.EncodeToString(sum[:16]), nil
}
