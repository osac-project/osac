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

var _ = Describe("Notifications API", Label("notifications"), func() {
	It("creates, lists, and deletes tenant announcements", func(ctx context.Context) {
		_, conn := createCatalogItemTenantAdminFixture(ctx)
		publicClient := publicv1.NewNotificationsClient(conn)

		created, err := publicClient.Create(ctx, publicv1.NotificationsCreateRequest_builder{
			Object: publicv1.Notification_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("ann-%s", uuid.New()[:8]),
				}.Build(),
				Spec: publicv1.NotificationSpec_builder{
					Kind:     publicv1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT,
					Severity: publicv1.NotificationSeverity_NOTIFICATION_SEVERITY_WARNING,
					Type:     "osac.notification.announcement.maintenance",
					Scope:    publicv1.NotificationScope_NOTIFICATION_SCOPE_TENANT,
					Title:    "Scheduled maintenance",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := created.GetObject().GetId()
		Expect(id).ToNot(BeEmpty())
		DeferCleanup(func(ctx context.Context) {
			_, _ = publicClient.Delete(ctx, publicv1.NotificationsDeleteRequest_builder{Id: id}.Build())
		})

		listed, err := publicClient.List(ctx, publicv1.NotificationsListRequest_builder{
			Filter: new(fmt.Sprintf(`this.spec.kind == %d && this.metadata.name == %q`,
				int32(publicv1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT),
				created.GetObject().GetMetadata().GetName())),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(containsId(listed.GetItems(), id)).To(BeTrue())

		_, err = publicClient.Delete(ctx, publicv1.NotificationsDeleteRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
	})

	It("denies tenant user Delete of announcements", func(ctx context.Context) {
		// Create in the same tenant as UserConn so Delete reaches server authz
		// (cross-tenant Get would return NotFound before the role check).
		userClient := publicv1.NewNotificationsClient(tool.ExternalView().UserConn())
		privateClient := privatev1.NewNotificationsClient(tool.InternalView().AdminConn())

		created, err := privateClient.Create(ctx, privatev1.NotificationsCreateRequest_builder{
			Object: privatev1.Notification_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("ann-deny-%s", uuid.New()[:8]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.NotificationSpec_builder{
					Kind:     privatev1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT,
					Severity: privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
					Type:     "osac.notification.announcement.maintenance",
					Scope:    privatev1.NotificationScope_NOTIFICATION_SCOPE_TENANT,
					Title:    "Admin owned",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := created.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, _ = privateClient.Delete(ctx, privatev1.NotificationsDeleteRequest_builder{Id: id}.Build())
		})

		_, err = userClient.Delete(ctx, publicv1.NotificationsDeleteRequest_builder{Id: id}.Build())
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied))
	})

	It("lists privately created inbox rows and updates spec.state", func(ctx context.Context) {
		publicClient := publicv1.NewNotificationsClient(tool.ExternalView().UserConn())
		privateClient := privatev1.NewNotificationsClient(tool.InternalView().AdminConn())

		recipient := userUsername
		inboxName := fmt.Sprintf("inbox-%s", uuid.New()[:8])
		created, err := privateClient.Create(ctx, privatev1.NotificationsCreateRequest_builder{
			Object: privatev1.Notification_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   inboxName,
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.NotificationSpec_builder{
					Kind:        privatev1.NotificationKind_NOTIFICATION_KIND_EVENT,
					Severity:    privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
					Type:        "osac.notification.event.resource_created",
					Source:      "osac-fulfillment",
					RecipientId: recipient,
					Title:       "Resource ready",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := created.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, _ = privateClient.Delete(ctx, privatev1.NotificationsDeleteRequest_builder{Id: id}.Build())
		})
		Expect(created.GetObject().GetSpec().GetState()).To(Equal(
			privatev1.NotificationState_NOTIFICATION_STATE_UNREAD))
		Expect(created.GetObject().GetSpec().GetSource()).To(Equal("osac-fulfillment"))

		listed, err := publicClient.List(ctx, publicv1.NotificationsListRequest_builder{
			Filter: new(fmt.Sprintf(`this.metadata.name == %q`, inboxName)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(containsId(listed.GetItems(), id)).To(BeTrue())

		updated, err := publicClient.Update(ctx, publicv1.NotificationsUpdateRequest_builder{
			Object: publicv1.Notification_builder{
				Id: id,
				Metadata: publicv1.Metadata_builder{
					Version: created.GetObject().GetMetadata().GetVersion(),
				}.Build(),
				Spec: publicv1.NotificationSpec_builder{
					State: publicv1.NotificationState_NOTIFICATION_STATE_READ,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.GetObject().GetSpec().GetState()).To(Equal(
			publicv1.NotificationState_NOTIFICATION_STATE_READ))
	})

	It("creates platform announcements in shared and lists them for tenants", func(ctx context.Context) {
		adminClient := publicv1.NewNotificationsClient(tool.ExternalView().AdminConn())
		userClient := publicv1.NewNotificationsClient(tool.ExternalView().UserConn())

		name := fmt.Sprintf("platform-ann-%s", uuid.New()[:8])
		created, err := adminClient.Create(ctx, publicv1.NotificationsCreateRequest_builder{
			Object: publicv1.Notification_builder{
				Metadata: publicv1.Metadata_builder{Name: name}.Build(),
				Spec: publicv1.NotificationSpec_builder{
					Kind:     publicv1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT,
					Severity: publicv1.NotificationSeverity_NOTIFICATION_SEVERITY_WARNING,
					Type:     "osac.notification.announcement.maintenance",
					Scope:    publicv1.NotificationScope_NOTIFICATION_SCOPE_PLATFORM,
					Title:    "Platform maintenance",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := created.GetObject().GetId()
		Expect(created.GetObject().GetMetadata().GetTenant()).To(Equal("shared"))
		DeferCleanup(func(ctx context.Context) {
			_, _ = adminClient.Delete(ctx, publicv1.NotificationsDeleteRequest_builder{Id: id}.Build())
		})

		listed, err := userClient.List(ctx, publicv1.NotificationsListRequest_builder{
			Filter: new(fmt.Sprintf(`this.metadata.name == %q`, name)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(containsId(listed.GetItems(), id)).To(BeTrue())

		got, err := userClient.Get(ctx, publicv1.NotificationsGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(got.GetObject().GetId()).To(Equal(id))
	})

	It("rejects announcement Create with spec.state set", func(ctx context.Context) {
		_, conn := createCatalogItemTenantAdminFixture(ctx)
		publicClient := publicv1.NewNotificationsClient(conn)

		_, err := publicClient.Create(ctx, publicv1.NotificationsCreateRequest_builder{
			Object: publicv1.Notification_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("ann-state-%s", uuid.New()[:8]),
				}.Build(),
				Spec: publicv1.NotificationSpec_builder{
					Kind:     publicv1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT,
					Severity: publicv1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
					Type:     "osac.notification.announcement.maintenance",
					Scope:    publicv1.NotificationScope_NOTIFICATION_SCOPE_TENANT,
					Title:    "Should reject",
					State:    publicv1.NotificationState_NOTIFICATION_STATE_UNREAD,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	It("lists and deletes announcements that include a resource reference", func(ctx context.Context) {
		_, conn := createCatalogItemTenantAdminFixture(ctx)
		publicClient := publicv1.NewNotificationsClient(conn)

		created, err := publicClient.Create(ctx, publicv1.NotificationsCreateRequest_builder{
			Object: publicv1.Notification_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("ann-res-%s", uuid.New()[:8]),
				}.Build(),
				Spec: publicv1.NotificationSpec_builder{
					Kind:     publicv1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT,
					Severity: publicv1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
					Type:     "osac.notification.announcement.maintenance",
					Scope:    publicv1.NotificationScope_NOTIFICATION_SCOPE_TENANT,
					Title:    "With resource",
					Object: publicv1.NotificationObjectReference_builder{
						Cluster: publicv1.ClusterReference_builder{
							Id: "nonexistent-cluster",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := created.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, _ = publicClient.Delete(ctx, publicv1.NotificationsDeleteRequest_builder{Id: id}.Build())
		})

		listed, err := publicClient.List(ctx, publicv1.NotificationsListRequest_builder{
			Filter: new(fmt.Sprintf(`this.metadata.name == %q`,
				created.GetObject().GetMetadata().GetName())),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(containsId(listed.GetItems(), id)).To(BeTrue())

		got, err := publicClient.Get(ctx, publicv1.NotificationsGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(got.GetObject().GetId()).To(Equal(id))

		_, err = publicClient.Delete(ctx, publicv1.NotificationsDeleteRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
	})

	It("hides other users' inbox rows from List and Get", func(ctx context.Context) {
		publicClient := publicv1.NewNotificationsClient(tool.ExternalView().UserConn())
		privateClient := privatev1.NewNotificationsClient(tool.InternalView().AdminConn())

		otherName := fmt.Sprintf("inbox-other-%s", uuid.New()[:8])
		other, err := privateClient.Create(ctx, privatev1.NotificationsCreateRequest_builder{
			Object: privatev1.Notification_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   otherName,
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.NotificationSpec_builder{
					Kind:        privatev1.NotificationKind_NOTIFICATION_KIND_EVENT,
					Severity:    privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
					Type:        "osac.notification.event.resource_created",
					Source:      "osac-fulfillment",
					RecipientId: "other-user",
					Title:       "Other user only",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		otherID := other.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, _ = privateClient.Delete(ctx, privatev1.NotificationsDeleteRequest_builder{Id: otherID}.Build())
		})

		listed, err := publicClient.List(ctx, publicv1.NotificationsListRequest_builder{
			Filter: new(fmt.Sprintf(`this.metadata.name == %q`, otherName)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(containsId(listed.GetItems(), otherID)).To(BeFalse())

		_, err = publicClient.Get(ctx, publicv1.NotificationsGetRequest_builder{Id: otherID}.Build())
		Expect(status.Code(err)).To(Equal(codes.NotFound))
	})
})
