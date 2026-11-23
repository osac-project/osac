/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Private notifications server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateNotificationsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateNotificationsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateNotificationsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *PrivateNotificationsServer

		BeforeEach(func() {
			var err error
			server, err = NewPrivateNotificationsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		inboxObject := func(recipient, eventID string) *privatev1.Notification {
			return privatev1.Notification_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "inbox-" + recipient + "-" + eventID,
				}.Build(),
				Spec: privatev1.NotificationSpec_builder{
					Kind:     privatev1.NotificationKind_NOTIFICATION_KIND_EVENT,
					Severity: privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
					Type:     "osac.notification.event.resource_created",
					Event: privatev1.NotificationEventReference_builder{
						Id:     eventID,
						Source: "osac/fulfillment-service",
					}.Build(),
					RecipientId: recipient,
					Title:       "Resource ready",
				}.Build(),
			}.Build()
		}

		announcementObject := func(scope privatev1.NotificationScope, name string) *privatev1.Notification {
			return privatev1.Notification_builder{
				Metadata: privatev1.Metadata_builder{Name: name}.Build(),
				Spec: privatev1.NotificationSpec_builder{
					Kind:     privatev1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT,
					Severity: privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
					Type:     "osac.notification.announcement.maintenance",
					Scope:    scope,
					Title:    "Maintenance",
				}.Build(),
			}.Build()
		}

		It("Creates an inbox notification with default unread state", func() {
			response, err := server.Create(ctx, privatev1.NotificationsCreateRequest_builder{
				Object: inboxObject("user-1", "evt-1"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			created := response.GetObject()
			Expect(created.GetId()).ToNot(BeEmpty())
			Expect(created.GetSpec().GetState()).To(Equal(
				privatev1.NotificationState_NOTIFICATION_STATE_UNREAD))
			Expect(created.GetMetadata().GetTenant()).To(Equal(testTenant))
		})

		It("Updates only spec.state on inbox items", func() {
			created, err := server.Create(ctx, privatev1.NotificationsCreateRequest_builder{
				Object: inboxObject("user-1", "evt-2"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updated, err := server.Update(ctx, privatev1.NotificationsUpdateRequest_builder{
				Object: privatev1.Notification_builder{
					Id: created.GetObject().GetId(),
					Metadata: privatev1.Metadata_builder{
						Version: created.GetObject().GetMetadata().GetVersion(),
					}.Build(),
					Spec: privatev1.NotificationSpec_builder{
						State: privatev1.NotificationState_NOTIFICATION_STATE_READ,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetSpec().GetState()).To(Equal(
				privatev1.NotificationState_NOTIFICATION_STATE_READ))

			_, err = server.Update(ctx, privatev1.NotificationsUpdateRequest_builder{
				Object: privatev1.Notification_builder{
					Id: created.GetObject().GetId(),
					Spec: privatev1.NotificationSpec_builder{
						Title: "nope",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.title"}},
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("Creates tenant announcements and rejects announcement updates", func() {
			created, err := server.Create(ctx, privatev1.NotificationsCreateRequest_builder{
				Object: announcementObject(
					privatev1.NotificationScope_NOTIFICATION_SCOPE_TENANT, "maint"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(created.GetObject().GetSpec().GetKind()).To(Equal(
				privatev1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT))

			_, err = server.Update(ctx, privatev1.NotificationsUpdateRequest_builder{
				Object: privatev1.Notification_builder{
					Id: created.GetObject().GetId(),
					Spec: privatev1.NotificationSpec_builder{
						State: privatev1.NotificationState_NOTIFICATION_STATE_READ,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("Forces system tenant for platform announcements", func() {
			created, err := server.Create(ctx, privatev1.NotificationsCreateRequest_builder{
				Object: privatev1.Notification_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "platform-maint",
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.NotificationSpec_builder{
						Kind:     privatev1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT,
						Severity: privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
						Type:     "osac.notification.announcement.maintenance",
						Scope:    privatev1.NotificationScope_NOTIFICATION_SCOPE_PLATFORM,
						Title:    "Platform notice",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(created.GetObject().GetMetadata().GetTenant()).To(Equal(auth.SystemTenant))
		})

		DescribeTable("Rejects invalid creates",
			func(object *privatev1.Notification) {
				_, err := server.Create(ctx, privatev1.NotificationsCreateRequest_builder{
					Object: object,
				}.Build())
				Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			},
			Entry("missing kind", privatev1.Notification_builder{
				Metadata: privatev1.Metadata_builder{Name: "bad"}.Build(),
				Spec: privatev1.NotificationSpec_builder{
					Severity: privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
					Title:    "x",
				}.Build(),
			}.Build()),
			Entry("missing type", privatev1.Notification_builder{
				Metadata: privatev1.Metadata_builder{Name: "bad-type-missing"}.Build(),
				Spec: privatev1.NotificationSpec_builder{
					Kind:     privatev1.NotificationKind_NOTIFICATION_KIND_EVENT,
					Severity: privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
					Event: privatev1.NotificationEventReference_builder{
						Id: "e1", Source: "svc",
					}.Build(),
					RecipientId: "user-1",
					Title:       "x",
				}.Build(),
			}.Build()),
			Entry("type missing name segment", privatev1.Notification_builder{
				Metadata: privatev1.Metadata_builder{Name: "bad-type-short"}.Build(),
				Spec: privatev1.NotificationSpec_builder{
					Kind:     privatev1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT,
					Severity: privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
					Type:     "osac.notification.announcement",
					Scope:    privatev1.NotificationScope_NOTIFICATION_SCOPE_TENANT,
					Title:    "x",
				}.Build(),
			}.Build()),
			Entry("type kind mismatch", privatev1.Notification_builder{
				Metadata: privatev1.Metadata_builder{Name: "bad-type-kind"}.Build(),
				Spec: privatev1.NotificationSpec_builder{
					Kind:     privatev1.NotificationKind_NOTIFICATION_KIND_EVENT,
					Severity: privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
					Type:     "osac.notification.announcement.maintenance",
					Event: privatev1.NotificationEventReference_builder{
						Id: "e1", Source: "svc",
					}.Build(),
					RecipientId: "user-1",
					Title:       "x",
				}.Build(),
			}.Build()),
			Entry("inbox missing recipient", privatev1.Notification_builder{
				Metadata: privatev1.Metadata_builder{Name: "bad-inbox"}.Build(),
				Spec: privatev1.NotificationSpec_builder{
					Kind:     privatev1.NotificationKind_NOTIFICATION_KIND_EVENT,
					Severity: privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
					Type:     "osac.notification.event.resource_created",
					Event: privatev1.NotificationEventReference_builder{
						Id: "e1", Source: "svc",
					}.Build(),
					Title: "x",
				}.Build(),
			}.Build()),
			Entry("announcement missing scope", privatev1.Notification_builder{
				Metadata: privatev1.Metadata_builder{Name: "bad-ann"}.Build(),
				Spec: privatev1.NotificationSpec_builder{
					Kind:     privatev1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT,
					Severity: privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
					Type:     "osac.notification.announcement.maintenance",
					Title:    "x",
				}.Build(),
			}.Build()),
			Entry("announcement with recipient", privatev1.Notification_builder{
				Metadata: privatev1.Metadata_builder{Name: "bad-ann-recipient"}.Build(),
				Spec: privatev1.NotificationSpec_builder{
					Kind:        privatev1.NotificationKind_NOTIFICATION_KIND_ANNOUNCEMENT,
					Severity:    privatev1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO,
					Type:        "osac.notification.announcement.maintenance",
					Scope:       privatev1.NotificationScope_NOTIFICATION_SCOPE_TENANT,
					RecipientId: "user-1",
					Title:       "x",
				}.Build(),
			}.Build()),
		)
	})
})
