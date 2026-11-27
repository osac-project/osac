/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const subscriptionWebhookURL = "https://example.com/osac-hooks/it"

var _ = Describe("NotificationSubscriptions API", Label("notifications", "vault"), func() {
	It("creates, lists, gets, pauses, and deletes a webhook subscription", func(ctx context.Context) {
		_, conn := createCatalogItemTenantAdminFixture(ctx)
		subs := publicv1.NewNotificationSubscriptionsClient(conn)
		secrets := publicv1.NewSecretsClient(conn)

		name := fmt.Sprintf("whsub-%s", uuid.New()[:8])
		created, err := subs.Create(ctx, publicv1.NotificationSubscriptionsCreateRequest_builder{
			Object: publicv1.NotificationSubscription_builder{
				Metadata: publicv1.Metadata_builder{Name: name}.Build(),
				Spec: publicv1.NotificationSubscriptionSpec_builder{
					WebhookUrl: subscriptionWebhookURL,
					Filter:     `kind == "event" && severity == "warning"`,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := created.GetObject().GetId()
		Expect(id).ToNot(BeEmpty())
		plaintext := created.GetSigningSecret()
		Expect(plaintext).To(HavePrefix("whsec_"))
		ref := created.GetObject().GetStatus().GetSigningSecret()
		Expect(ref.GetId()).ToNot(BeEmpty())
		Expect(ref.GetName()).To(Equal(fmt.Sprintf("whsec-%s", name)))
		DeferCleanup(func(ctx context.Context) {
			_, _ = subs.Delete(ctx, publicv1.NotificationSubscriptionsDeleteRequest_builder{Id: id}.Build())
		})

		secret, err := secrets.Get(ctx, publicv1.SecretsGetRequest_builder{Id: ref.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(secret.GetObject().GetData()).To(HaveKeyWithValue("value", []byte(plaintext)))

		listed, err := subs.List(ctx, publicv1.NotificationSubscriptionsListRequest_builder{
			Filter: new(fmt.Sprintf(`this.metadata.name == %q`, name)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(containsId(listed.GetItems(), id)).To(BeTrue())

		got, err := subs.Get(ctx, publicv1.NotificationSubscriptionsGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(got.GetObject().GetSpec().GetWebhookUrl()).To(Equal(subscriptionWebhookURL))
		Expect(got.GetObject().GetStatus().GetSigningSecret().GetId()).To(Equal(ref.GetId()))

		paused, err := subs.Update(ctx, publicv1.NotificationSubscriptionsUpdateRequest_builder{
			Object: publicv1.NotificationSubscription_builder{
				Id: id,
				Metadata: publicv1.Metadata_builder{
					Version: got.GetObject().GetMetadata().GetVersion(),
				}.Build(),
				Spec: publicv1.NotificationSubscriptionSpec_builder{
					Paused: true,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.paused"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(paused.GetObject().GetSpec().GetPaused()).To(BeTrue())

		_, err = subs.Delete(ctx, publicv1.NotificationSubscriptionsDeleteRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
	})

	It("denies tenant user Create of subscriptions", func(ctx context.Context) {
		userClient := publicv1.NewNotificationSubscriptionsClient(tool.ExternalView().UserConn())

		_, err := userClient.Create(ctx, publicv1.NotificationSubscriptionsCreateRequest_builder{
			Object: publicv1.NotificationSubscription_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("whsub-deny-%s", uuid.New()[:8]),
				}.Build(),
				Spec: publicv1.NotificationSubscriptionSpec_builder{
					WebhookUrl: subscriptionWebhookURL,
					Filter:     `kind == "event"`,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied))
	})

	It("rejects invalid CEL filters and non-HTTPS webhook URLs", func(ctx context.Context) {
		_, conn := createCatalogItemTenantAdminFixture(ctx)
		subs := publicv1.NewNotificationSubscriptionsClient(conn)

		_, err := subs.Create(ctx, publicv1.NotificationSubscriptionsCreateRequest_builder{
			Object: publicv1.NotificationSubscription_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("whsub-bad-filter-%s", uuid.New()[:8]),
				}.Build(),
				Spec: publicv1.NotificationSubscriptionSpec_builder{
					WebhookUrl: subscriptionWebhookURL,
					Filter:     "kind ==",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))

		_, err = subs.Create(ctx, publicv1.NotificationSubscriptionsCreateRequest_builder{
			Object: publicv1.NotificationSubscription_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("whsub-bad-url-%s", uuid.New()[:8]),
				}.Build(),
				Spec: publicv1.NotificationSubscriptionSpec_builder{
					WebhookUrl: "http://example.com/osac-hooks/it",
					Filter:     `kind == "event"`,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	It("lists privately created deliveries for tenant admins", func(ctx context.Context) {
		tenant, conn := createCatalogItemTenantAdminFixture(ctx)
		subs := publicv1.NewNotificationSubscriptionsClient(conn)
		publicDeliveries := publicv1.NewNotificationSubscriptionDeliveriesClient(conn)
		privateDeliveries := privatev1.NewNotificationSubscriptionDeliveriesClient(tool.InternalView().AdminConn())

		subName := fmt.Sprintf("whsub-deliv-%s", uuid.New()[:8])
		createdSub, err := subs.Create(ctx, publicv1.NotificationSubscriptionsCreateRequest_builder{
			Object: publicv1.NotificationSubscription_builder{
				Metadata: publicv1.Metadata_builder{Name: subName}.Build(),
				Spec: publicv1.NotificationSubscriptionSpec_builder{
					WebhookUrl: subscriptionWebhookURL,
					Filter:     `kind == "event"`,
					Paused:     true,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		subID := createdSub.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, _ = subs.Delete(ctx, publicv1.NotificationSubscriptionsDeleteRequest_builder{Id: subID}.Build())
		})

		deliveryName := fmt.Sprintf("whdeliv-%s", uuid.New()[:8])
		createdDelivery, err := privateDeliveries.Create(ctx, privatev1.NotificationSubscriptionDeliveriesCreateRequest_builder{
			Object: privatev1.NotificationSubscriptionDelivery_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   deliveryName,
					Tenant: tenant,
				}.Build(),
				Spec: privatev1.NotificationSubscriptionDeliverySpec_builder{
					Subscription: privatev1.NotificationSubscriptionLocalReference_builder{
						Id:   subID,
						Name: subName,
					}.Build(),
					Event: privatev1.NotificationSubscriptionDeliveryEvent_builder{
						Id:     fmt.Sprintf("evt-%s", uuid.New()[:8]),
						Source: "osac-notifications",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		deliveryID := createdDelivery.GetObject().GetId()
		Expect(createdDelivery.GetObject().GetStatus().GetState()).To(Equal(
			privatev1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING))
		DeferCleanup(func(ctx context.Context) {
			_, _ = privateDeliveries.Delete(ctx, privatev1.NotificationSubscriptionDeliveriesDeleteRequest_builder{
				Id: deliveryID,
			}.Build())
		})

		listed, err := publicDeliveries.List(ctx, publicv1.NotificationSubscriptionDeliveriesListRequest_builder{
			Filter: new(fmt.Sprintf(`this.metadata.name == %q`, deliveryName)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(containsId(listed.GetItems(), deliveryID)).To(BeTrue())

		got, err := publicDeliveries.Get(ctx, publicv1.NotificationSubscriptionDeliveriesGetRequest_builder{
			Id: deliveryID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(got.GetObject().GetSpec().GetSubscription().GetId()).To(Equal(subID))
		Expect(got.GetObject().GetStatus().GetState()).To(Equal(
			publicv1.NotificationSubscriptionDeliveryState_NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING))
	})
})
